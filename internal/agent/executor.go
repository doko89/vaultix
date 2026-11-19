package agent

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// UnsupportedError is the structured, actionable error for target types and
// job kinds this agent cannot execute (mssql, redis, stack members, ...).
// It is a job failure, never a crash.
type UnsupportedError struct {
	JobID      string
	Kind       string
	TargetType string
	Selector   string
	Detail     string
}

func (e *UnsupportedError) Error() string {
	what := e.TargetType
	if what == "" {
		what = "kind " + e.Kind
	}
	msg := fmt.Sprintf("agent: job %s: unsupported target type %q (selector %q)", e.JobID, what, e.Selector)
	if e.Detail != "" {
		msg += ": " + e.Detail
	} else {
		msg += ": this agent build handles host_filesystem, docker_volume, postgres, mysql"
	}
	return msg
}

// Artifact is one staged backup payload ready for upload. Path holds final
// bytes (compressed, envelope-encrypted when the job requires it).
type Artifact struct {
	Name            string // proposed object name, e.g. t0-host_filesystem.tar.zst
	Path            string // staged file (final upload bytes)
	SizeBytes       int64
	SHA256          string
	BytesRead       int64 // pre-compression payload bytes
	BytesCompressed int64 // post-compression bytes (pre-encryption)
	Encrypted       bool
	TargetType      string
}

// Executor runs backup targets into staged artifacts. One job at a time
// (MVP); the runner serializes calls.
type Executor struct {
	WorkDir          string
	CompressionLevel int
	VolumeResolver   VolumeResolver
	LookPath         func(string) (string, error)
	OnTargetComplete func(done, total int, art *Artifact)
}

// compressionLevel clamps to zstd's 1..22 range (default 3).
func (e *Executor) compressionLevel() int {
	if e.CompressionLevel >= 1 && e.CompressionLevel <= 22 {
		return e.CompressionLevel
	}
	return 3
}

// ExecuteJob runs every target of job in order. It stops at the first error;
// successfully staged artifacts are returned alongside the error so the
// caller can clean up.
func (e *Executor) ExecuteJob(ctx context.Context, job PolledJob) ([]*Artifact, error) {
	if strings.TrimSpace(job.ID) == "" {
		return nil, fmt.Errorf("agent: job rejected: missing id")
	}
	if len(job.Targets) == 0 {
		return nil, fmt.Errorf("agent: job %s rejected: no targets", job.ID)
	}
	if job.EncryptionEnabled() && strings.TrimSpace(job.Encryption.KeyB64) == "" {
		return nil, fmt.Errorf("agent: job %s: encryption enabled but manager sent no key (refusing silent plaintext)", job.ID)
	}
	if err := os.MkdirAll(e.workDirFor(job.ID), 0o755); err != nil {
		return nil, fmt.Errorf("agent: job %s: create staging dir: %w", job.ID, err)
	}
	var arts []*Artifact
	for i, tgt := range job.Targets {
		if err := ctx.Err(); err != nil {
			return arts, fmt.Errorf("agent: job %s: aborted: %w", job.ID, err)
		}
		art, err := e.ExecuteTarget(ctx, job, i, tgt)
		if err != nil {
			return arts, err
		}
		arts = append(arts, art)
		if e.OnTargetComplete != nil {
			e.OnTargetComplete(len(arts), len(job.Targets), art)
		}
	}
	return arts, nil
}

func (e *Executor) workDirFor(jobID string) string {
	base := e.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "vaultix-agent", "jobs", jobID)
}

// Cleanup removes staged files for jobID (best-effort).
func (e *Executor) Cleanup(jobID string) {
	_ = os.RemoveAll(e.workDirFor(jobID))
}

// ExecuteTarget backs up exactly one authorized target. kind falls back to
// the job kind when the target carries no explicit type.
func (e *Executor) ExecuteTarget(ctx context.Context, job PolledJob, idx int, tgt Target) (*Artifact, error) {
	ttype := NormalizeTargetType(tgt.Type)
	if ttype == "" {
		ttype = KindToTargetType(job.Kind)
	}
	if strings.TrimSpace(tgt.Selector) == "" {
		return nil, fmt.Errorf("agent: job %s target %d (%s): empty selector", job.ID, idx, ttype)
	}
	if err := os.MkdirAll(e.workDirFor(job.ID), 0o755); err != nil {
		return nil, fmt.Errorf("agent: job %s: create staging dir: %w", job.ID, err)
	}
	switch ttype {
	case TargetHostFilesystem, TargetDockerVolume, TargetPostgres, TargetMySQL:
		return e.runSupportedTarget(ctx, job, idx, ttype, tgt)
	default:
		return nil, &UnsupportedError{JobID: job.ID, Kind: job.Kind, TargetType: ttype, Selector: tgt.Selector}
	}
}

// runSupportedTarget stages one payload: producer -> zstd -> [envelope] -> file+sha256.
func (e *Executor) runSupportedTarget(ctx context.Context, job PolledJob, idx int, ttype string, tgt Target) (*Artifact, error) {
	name := fmt.Sprintf("t%d-%s%s", idx, ttype, artifactSuffix(ttype))
	path := filepath.Join(e.workDirFor(job.ID), name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent: job %s: stage %s: %w", job.ID, name, err)
	}
	sum := sha256.New()
	fileWithHash := io.MultiWriter(f, sum)

	encrypted := job.EncryptionEnabled()
	var payloadWriter io.Writer = fileWithHash
	var ew *EncryptWriter
	if encrypted {
		ew, err = NewEncryptWriter(fileWithHash, job.Encryption.KeyB64, job.Encryption.KeyRef)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		payloadWriter = ew
	}

	// Compression sits inside encryption: ciphertext must not be compressed.
	zenc, err := zstd.NewWriter(payloadWriter, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(e.compressionLevel())))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("agent: job %s: create zstd encoder: %w", job.ID, err)
	}
	bytesRead, runErr := e.produce(ctx, job, ttype, tgt, zenc)
	closeErr := zenc.Close()
	if ew != nil {
		if cerr := ew.Close(); cerr != nil && runErr == nil {
			runErr = cerr
		}
	}
	syncErr := f.Sync()
	finfo, statErr := f.Stat()
	_ = f.Close()

	if runErr != nil {
		_ = os.Remove(path)
		return nil, runErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent: job %s: close zstd encoder: %w", job.ID, closeErr)
	}
	if syncErr != nil || statErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent: job %s: persist staged artifact: %v %v", job.ID, syncErr, statErr)
	}
	// Post-compression, pre-encryption bytes: the encrypt writer's plaintext
	// count when enveloped, else the staged file size.
	var compressed int64 = finfo.Size()
	if ew != nil {
		compressed = ew.PlainBytes()
	}
	return &Artifact{
		Name: name, Path: path, SizeBytes: finfo.Size(), SHA256: hex.EncodeToString(sum.Sum(nil)),
		BytesRead: bytesRead, BytesCompressed: compressed,
		Encrypted: encrypted, TargetType: ttype,
	}, nil
}

func artifactSuffix(ttype string) string {
	switch ttype {
	case TargetPostgres:
		return ".dump.zst"
	case TargetMySQL:
		return ".sql.zst"
	default:
		return ".tar.zst"
	}
}

// produce runs the target-type producer into the zstd stream and returns
// pre-compression bytes read. Callers close the stream.
func (e *Executor) produce(ctx context.Context, job PolledJob, ttype string, tgt Target, zw io.Writer) (bytesRead int64, err error) {
	opts := tgt.Options
	if opts == nil {
		opts = map[string]any{}
	}
	switch ttype {
	case TargetHostFilesystem:
		fopts, err := ParseFilesystemOptions(opts)
		if err != nil {
			return 0, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		tw := tar.NewWriter(zw)
		n, werr := writeFSTar(ctx, tw, tgt.Selector, fopts)
		cerr := tw.Close()
		if werr != nil {
			return n, fmt.Errorf("agent: job %s: %w", job.ID, werr)
		}
		if cerr != nil {
			return n, fmt.Errorf("agent: job %s: close tar writer: %w", job.ID, cerr)
		}
		return n, nil
	case TargetDockerVolume:
		dopts, err := ParseDockerVolumeOptions(opts)
		if err != nil {
			return 0, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		tw := tar.NewWriter(zw)
		n, werr := writeDockerTar(ctx, tw, tgt.Selector, dopts, e.VolumeResolver)
		cerr := tw.Close()
		if werr != nil {
			return n, werr
		}
		if cerr != nil {
			return n, fmt.Errorf("agent: job %s: close tar writer: %w", job.ID, cerr)
		}
		return n, nil
	case TargetPostgres:
		dopts, err := ParseDumpOptions(opts)
		if err != nil {
			return 0, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		return DumpPostgres(ctx, zw, job.ID, tgt.Selector, dopts, e.LookPath)
	case TargetMySQL:
		dopts, err := ParseDumpOptions(opts)
		if err != nil {
			return 0, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		return DumpMySQL(ctx, zw, job.ID, tgt.Selector, dopts, e.LookPath)
	default:
		return 0, &UnsupportedError{JobID: job.ID, Kind: job.Kind, TargetType: ttype, Selector: tgt.Selector}
	}
}
