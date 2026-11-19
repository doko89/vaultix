package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ManifestObjectName is the relative object name the job manifest is
// uploaded under. The manager identifies it by exact name or by a
// "/manifest.json" suffix on the full scoped key, and completion fails
// without exactly one (see backup CompleteJobFlat).
const ManifestObjectName = "manifest.json"

// jobManifestVersion is the JobManifestDoc schema version.
const jobManifestVersion = 1

// JobManifestEntry attests one uploaded data object: the staged artifact
// name, its manager-issued full key, and the integrity facts.
type JobManifestEntry struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	TargetType string `json:"target_type"`
	Encrypted  bool   `json:"encrypted"`
}

// JobManifestDoc is the agent-attested job manifest uploaded as
// manifest.json. The manager checksum-verifies the stored bytes and serves
// the object first on restores; its content is opaque to the server.
type JobManifestDoc struct {
	Version       int                `json:"version"`
	JobID         string             `json:"job_id"`
	AgentID       string             `json:"agent_id"`
	PolicyID      string             `json:"policy_id"`
	PolicyVersion int                `json:"policy_version"`
	Objects       []JobManifestEntry `json:"objects"`
	BytesRead     int64              `json:"bytes_read"`
	Compression   CompressionInfo    `json:"compression"`
	Encryption    ManifestEncryption `json:"encryption"`
}

// StageJobManifest writes the job manifest doc for uploaded data objects
// and stages it through the same pipeline as data payloads (zstd inside,
// envelope outside when the job is encrypted) so restores consume it like
// any other object. Keys come from uploaded, which must cover arts.
func (e *Executor) StageJobManifest(job PolledJob, agentID string, arts []*Artifact, uploaded []UploadedObject) (*Artifact, error) {
	if strings.TrimSpace(job.ID) == "" {
		return nil, fmt.Errorf("agent: manifest rejected: missing job id")
	}
	keys := make(map[string]string, len(uploaded))
	for _, u := range uploaded {
		if u.Artifact != nil {
			keys[u.Artifact.Name] = u.Key
		}
	}
	doc := JobManifestDoc{
		Version: jobManifestVersion, JobID: job.ID, AgentID: agentID,
		PolicyID: job.PolicyID, PolicyVersion: job.PolicyVersion,
		Objects:     make([]JobManifestEntry, 0, len(arts)),
		Compression: CompressionInfo{Algo: "zstd", Level: e.compressionLevel()},
		Encryption:  ManifestEncryption{Enabled: job.EncryptionEnabled(), KeyRef: job.Encryption.KeyRef},
	}
	for _, a := range arts {
		key, ok := keys[a.Name]
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("agent: job %s: manifest: no uploaded key for %s", job.ID, a.Name)
		}
		doc.Objects = append(doc.Objects, JobManifestEntry{
			Name: a.Name, Key: key, SizeBytes: a.SizeBytes, SHA256: a.SHA256,
			TargetType: a.TargetType, Encrypted: a.Encrypted,
		})
		doc.BytesRead += a.BytesRead
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("agent: job %s: manifest: encode: %w", job.ID, err)
	}
	return e.stageBytes(job, ManifestObjectName, raw, "manifest")
}

// stageBytes runs in-memory bytes through the data pipeline (zstd, then the
// envelope when the job is encrypted) into the job staging dir.
func (e *Executor) stageBytes(job PolledJob, name string, plain []byte, targetType string) (*Artifact, error) {
	if err := os.MkdirAll(e.workDirFor(job.ID), 0o755); err != nil {
		return nil, fmt.Errorf("agent: job %s: create staging dir: %w", job.ID, err)
	}
	path := filepath.Join(e.workDirFor(job.ID), name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent: job %s: stage %s: %w", job.ID, name, err)
	}
	sum := sha256.New()
	out := io.MultiWriter(f, sum)

	encrypted := job.EncryptionEnabled()
	var payload io.Writer = out
	var ew *EncryptWriter
	if encrypted {
		ew, err = NewEncryptWriter(out, job.Encryption.KeyB64, job.Encryption.KeyRef)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("agent: job %s: %w", job.ID, err)
		}
		payload = ew
	}
	zenc, err := zstd.NewWriter(payload, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(e.compressionLevel())))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("agent: job %s: create zstd encoder: %w", job.ID, err)
	}
	_, werr := zenc.Write(plain)
	cerr := zenc.Close()
	if ew != nil {
		if ecerr := ew.Close(); ecerr != nil && werr == nil {
			werr = ecerr
		}
	}
	syncErr := f.Sync()
	finfo, statErr := f.Stat()
	_ = f.Close()
	if werr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent: job %s: write %s: %w", job.ID, name, werr)
	}
	if cerr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent: job %s: close zstd encoder: %w", job.ID, cerr)
	}
	if syncErr != nil || statErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent: job %s: persist staged artifact: %v %v", job.ID, syncErr, statErr)
	}
	var compressed int64 = finfo.Size()
	if ew != nil {
		compressed = ew.PlainBytes()
	}
	return &Artifact{
		Name: name, Path: path, SizeBytes: finfo.Size(), SHA256: hex.EncodeToString(sum.Sum(nil)),
		BytesRead: int64(len(plain)), BytesCompressed: compressed,
		Encrypted: encrypted, TargetType: targetType,
	}, nil
}

// uploadWithManifest uploads data artifacts, then stages the job manifest
// (which attests the data keys) and uploads it as a second object. It
// returns every uploaded object, manifest last.
func uploadWithManifest(ctx context.Context, job PolledJob, agentID string, exec *Executor, up *Uploader, arts []*Artifact) ([]UploadedObject, error) {
	uploaded, err := up.UploadJob(ctx, job.ID, arts)
	if err != nil {
		return nil, err
	}
	mart, err := exec.StageJobManifest(job, agentID, arts, uploaded)
	if err != nil {
		return nil, err
	}
	mup, err := up.UploadJob(ctx, job.ID, []*Artifact{mart})
	if err != nil {
		return nil, err
	}
	return append(uploaded, mup...), nil
}
