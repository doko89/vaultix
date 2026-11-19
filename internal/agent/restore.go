package agent

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Restorer materializes snapshots dispatched by the manager. Downloads use
// plain stdlib HTTP against presigned URLs (no bearer credential).
type Restorer struct {
	Manager ManagerClient
	WorkDir string
	HTTP    *http.Client
	// MaxExtractBytes caps total extracted content per object.
	MaxExtractBytes int64
}

func (r *Restorer) httpClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (r *Restorer) maxExtractBytes() int64 {
	if r.MaxExtractBytes > 0 {
		return r.MaxExtractBytes
	}
	return DefaultArchiveMaxBytes
}

func (r *Restorer) workDirFor(restoreID string) string {
	base := r.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "vaultix-agent", "restores", restoreID)
}

// Cleanup removes staged restore files (best-effort).
func (r *Restorer) Cleanup(restoreID string) {
	_ = os.RemoveAll(r.workDirFor(restoreID))
}

// Execute runs one restore end to end: grants → download+verify → decrypt →
// decompress → extract. It reports status to the manager itself and returns
// the terminal state ("succeeded" or "failed") plus any error.
func (r *Restorer) Execute(ctx context.Context, job RestoreJob) (string, error) {
	if strings.TrimSpace(job.ID) == "" {
		return "failed", fmt.Errorf("agent: restore rejected: missing id")
	}
	if strings.TrimSpace(job.Destination.Path) == "" {
		err := fmt.Errorf("agent: restore %s: missing destination path", job.ID)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	if len(job.Objects) == 0 {
		err := fmt.Errorf("agent: restore %s: snapshot has no objects", job.ID)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	if job.Encryption.Enabled && strings.TrimSpace(job.Encryption.KeyB64) == "" {
		err := fmt.Errorf("agent: restore %s: snapshot is encrypted but manager sent no key", job.ID)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	// Selective restore (PRD 7.13): a non-empty path list extracts only
	// the listed files/dirs; empty restores the whole scope as before.
	wants, err := normalizeRestorePaths(job.Paths)
	if err != nil {
		err = fmt.Errorf("agent: restore %s: %w", job.ID, err)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	objs := job.Objects
	if len(wants) > 0 {
		// Selective restore: the job-manifest metadata object can never
		// satisfy a user path — drop it before grants/download instead of
		// failing the restore on it. Full restores still materialize it
		// (known sidecar; see TECH_DEBT F8).
		kept := make([]RestoreObject, 0, len(objs))
		for _, o := range objs {
			if isManifestObject(o.Key) {
				continue
			}
			kept = append(kept, o)
		}
		if len(kept) == 0 {
			err := fmt.Errorf("agent: restore %s: selective restore (%d path(s)) found no data objects in snapshot", job.ID, len(wants))
			r.report(ctx, job.ID, "failed", err, nil, nil)
			return "failed", err
		}
		objs = kept
	}
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		keys = append(keys, o.Key)
	}
	grants, err := r.Manager.DownloadGrants(ctx, job.ID, keys)
	if err != nil {
		err = fmt.Errorf("agent: restore %s: download grants: %w", job.ID, err)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	if len(grants.Grants) != len(objs) {
		err = fmt.Errorf("agent: restore %s: download grants: want %d, got %d", job.ID, len(objs), len(grants.Grants))
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	if err := os.MkdirAll(r.workDirFor(job.ID), 0o755); err != nil {
		err = fmt.Errorf("agent: restore %s: create staging dir: %w", job.ID, err)
		r.report(ctx, job.ID, "failed", err, nil, nil)
		return "failed", err
	}
	defer r.Cleanup(job.ID)
	matched := make(map[string]bool, len(wants))
	for i, obj := range objs {
		if err := ctx.Err(); err != nil {
			rerr := fmt.Errorf("agent: restore %s: aborted: %w", job.ID, err)
			r.report(ctx, job.ID, "failed", rerr, orderedMatched(wants, matched), nil)
			return "failed", rerr
		}
		got, err := r.restoreObject(ctx, job, obj, grants.Grants[i], wants)
		if err != nil {
			r.report(ctx, job.ID, "failed", err, orderedMatched(wants, matched), nil)
			return "failed", err
		}
		for _, w := range got {
			matched[w] = true
		}
	}
	restored := orderedMatched(wants, matched)
	if missing := orderedMissing(wants, matched); len(missing) > 0 {
		err := fmt.Errorf("agent: restore %s: %d requested path(s) not found in snapshot: %s",
			job.ID, len(missing), quoteEach(missing))
		r.report(ctx, job.ID, "failed", err, restored, missing)
		return "failed", err
	}
	r.report(ctx, job.ID, "succeeded", nil, restored, nil)
	return "succeeded", nil
}

func (r *Restorer) report(ctx context.Context, restoreID, state string, err error, restored, missing []string) {
	req := RestoreReportRequest{State: state, RestoredPaths: restored, MissingPaths: missing}
	if err != nil {
		req.Error = err.Error()
	}
	// Independent timeout: the report must go out even when the run ctx died.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = r.Manager.ReportRestore(rctx, restoreID, req)
}

// extractDir resolves the tar extraction root. File/volume archives store
// entries under the source's base name, so in-place ("original") restores
// extract into the parent of the original path (the archived base lands
// back in place); alternate restores extract into the alternate directory
// itself.
func extractDir(job RestoreJob) string {
	if strings.EqualFold(strings.TrimSpace(job.Destination.Mode), "original") {
		return filepath.Dir(job.Destination.Path)
	}
	return job.Destination.Path
}

// destDir anchors relative destinations (e.g. database names) at the agent
// work dir so a restore never materializes relative to the process working
// directory. Absolute destinations pass through unchanged.
func (r *Restorer) destDir(dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	base := r.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, dir)
}

// restoreObject downloads one object, verifies sha256, decrypts, and either
// extracts (tar payloads) or writes (db dumps) into the destination. It
// returns the normalized requested paths this object satisfied.
func (r *Restorer) restoreObject(ctx context.Context, job RestoreJob, obj RestoreObject, g DownloadGrant, wants []string) ([]string, error) {
	staged := filepath.Join(r.workDirFor(job.ID), fmt.Sprintf("obj-%x.bin", sha256.Sum256([]byte(obj.Key))))
	if err := r.download(ctx, job.ID, obj, g, staged); err != nil {
		return nil, err
	}
	f, err := os.Open(staged)
	if err != nil {
		return nil, fmt.Errorf("agent: restore %s: open staged object: %w", job.ID, err)
	}
	defer func() { _ = f.Close() }()

	var payload io.Reader = f
	if job.Encryption.Enabled {
		pr, pw := io.Pipe()
		decErr := make(chan error, 1)
		go func() {
			_, err := DecryptStream(pw, f, job.Encryption.KeyB64)
			_ = pw.CloseWithError(err)
			decErr <- err
		}()
		defer func() { _ = pr.Close() }()
		payload = &pipeReader{r: pr, errCh: decErr}
	}
	// Every backup payload is zstd-compressed (inside the envelope when
	// encrypted): tar.zst for files/volumes, dump.zst/sql.zst for databases.
	dec, err := zstd.NewReader(payload)
	if err != nil {
		return nil, fmt.Errorf("agent: restore %s: object %s: create zstd decoder (wrong key or corrupt data): %w", job.ID, obj.Key, err)
	}
	defer dec.Close()

	var matched []string
	if isTarPayload(job.Kind, obj.Key) {
		m, err := ExtractTarSelective(dec, r.destDir(extractDir(job)), job.OverwriteAllowed(), r.maxExtractBytes(), wants)
		if err != nil {
			return nil, fmt.Errorf("agent: restore %s: object %s: %w", job.ID, obj.Key, err)
		}
		matched = m
	} else {
		if len(wants) > 0 {
			return nil, fmt.Errorf("agent: restore %s: object %s: selective restore (%d path(s)) needs a file/volume snapshot, got %q",
				job.ID, obj.Key, len(wants), job.Kind)
		}
		name := restoreFileName(job.Kind, obj.Key)
		if err := WriteRestoreFile(dec, r.destDir(job.Destination.Path), name, job.OverwriteAllowed(), r.maxExtractBytes()); err != nil {
			return nil, fmt.Errorf("agent: restore %s: object %s: %w", job.ID, obj.Key, err)
		}
		// Deferred-reimport kinds (mssql) deliver a native backup file the
		// operator re-imports; log the compatibility note so it is visible
		// in the agent log next to the delivery.
		if note := RestoreNoteForKind(job.Kind); note != "" {
			slog.Default().Info("agent: database restore delivered as file",
				"restore_id", job.ID, "kind", NormalizeTargetType(job.Kind),
				"file", name, "note", note)
		}
	}
	// Drain decoder errors (e.g. decrypt failures surfacing mid-stream).
	if pr, ok := payload.(*pipeReader); ok {
		if err := pr.Wait(); err != nil {
			return nil, fmt.Errorf("agent: restore %s: object %s: decrypt: %w", job.ID, obj.Key, err)
		}
	}
	return matched, nil
}

// download fetches one presigned URL to staged, verifying size and sha256.
func (r *Restorer) download(ctx context.Context, restoreID string, obj RestoreObject, g DownloadGrant, staged string) error {
	if strings.TrimSpace(g.URL) == "" {
		return fmt.Errorf("agent: restore %s: object %s: grant has no URL", restoreID, obj.Key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		return fmt.Errorf("agent: restore %s: object %s: build GET: %w", restoreID, obj.Key, err)
	}
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("agent: restore %s: object %s: GET %s: %w", restoreID, obj.Key, redactURL(g.URL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyByte))
		return fmt.Errorf("agent: restore %s: object %s: GET %s: status %s", restoreID, obj.Key, redactURL(g.URL), resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		return fmt.Errorf("agent: restore %s: create staging dir: %w", restoreID, err)
	}
	f, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("agent: restore %s: stage object: %w", restoreID, err)
	}
	sum := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, sum), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("agent: restore %s: object %s: download: %w", restoreID, obj.Key, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("agent: restore %s: object %s: persist: %w", restoreID, obj.Key, closeErr)
	}
	if obj.SizeBytes > 0 && n != obj.SizeBytes {
		_ = os.Remove(staged)
		return fmt.Errorf("agent: restore %s: object %s: size mismatch (manifest %d, got %d)", restoreID, obj.Key, obj.SizeBytes, n)
	}
	got := hex.EncodeToString(sum.Sum(nil))
	if obj.SHA256 != "" && !strings.EqualFold(got, obj.SHA256) {
		_ = os.Remove(staged)
		return fmt.Errorf("agent: restore %s: object %s: sha256 mismatch (manifest %.12s, got %.12s...)", restoreID, obj.Key, obj.SHA256, got)
	}
	return nil
}

// pipeReader surfaces the background decrypt error after the stream ends.
type pipeReader struct {
	r     *io.PipeReader
	errCh chan error
}

func (p *pipeReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// Wait closes the reader and returns the background error, if any.
func (p *pipeReader) Wait() error {
	_ = p.r.Close()
	return <-p.errCh
}

// isManifestObject reports whether key is the job-manifest metadata object:
// exactly ManifestObjectName or a job-scoped key ending in
// "/manifest.json" (same rule the manager uses to split completions).
func isManifestObject(key string) bool {
	return key == ManifestObjectName || strings.HasSuffix(key, "/"+ManifestObjectName)
}

func isTarPayload(kind, key string) bool {
	k := NormalizeTargetType(kind)
	if k == TargetPostgres || k == TargetMySQL || k == TargetMSSQL {
		return false
	}
	if k == TargetHostFilesystem || k == TargetDockerVolume {
		return true
	}
	return strings.HasSuffix(key, ".tar.zst")
}

func restoreFileName(kind, key string) string {
	base := key
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".zst")
	if strings.TrimSpace(base) == "" {
		base = "restore-" + NormalizeTargetType(kind) + ".dump"
	}
	return base
}

// ExtractTar extracts a tar stream into destDir, creating dirs, restoring
// regular files and symlinks. It rejects traversal and absolute paths, caps
// total bytes, and refuses to overwrite existing files unless allowOverwrite
// is set.
func ExtractTar(r io.Reader, destDir string, allowOverwrite bool, maxTotalBytes int64) error {
	_, err := ExtractTarSelective(r, destDir, allowOverwrite, maxTotalBytes, nil)
	return err
}

// ExtractTarSelective extracts only the tar entries covered by wants (see
// normalizeRestorePaths); a nil/empty wants extracts everything, exactly
// like ExtractTar. It returns the normalized wanted paths that had at least
// one entry extracted, in wants order.
//
// Non-matching entries are skipped before any filesystem access, so hostile
// entries (traversal, absolute paths) outside the selection can neither
// fail the restore nor escape the destination. Matching entries keep the
// full protection: safeJoin anchoring, overwrite policy, and byte cap.
func ExtractTarSelective(r io.Reader, destDir string, allowOverwrite bool, maxTotalBytes int64, wants []string) ([]string, error) {
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultArchiveMaxBytes
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("extract: create destination: %w", err)
	}
	selective := len(wants) > 0
	matched := make(map[string]bool, len(wants))
	tr := tar.NewReader(r)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return orderedMatched(wants, matched), nil
		}
		if err != nil {
			return nil, fmt.Errorf("extract: read tar entry: %w", err)
		}
		if selective {
			hits := matchRestoreWants(hdr.Name, wants)
			if len(hits) == 0 {
				// Skip unselected content; nothing is materialized.
				_, _ = io.Copy(io.Discard, tr)
				continue
			}
			for _, w := range hits {
				matched[w] = true
			}
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return nil, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("extract: create dir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := checkOverwrite(target, allowOverwrite); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("extract: create dir: %w", err)
			}
			if err := writeFile(target, tr, hdr, &total, maxTotalBytes); err != nil {
				return nil, err
			}
		case tar.TypeSymlink:
			if err := checkOverwrite(target, allowOverwrite); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("extract: create dir: %w", err)
			}
			// Confine the link target: absolute links and links escaping
			// destDir are refused rather than created.
			linkTarget := hdr.Linkname
			if filepath.IsAbs(linkTarget) {
				return nil, fmt.Errorf("extract: symlink %q points to absolute path (refused)", hdr.Name)
			}
			resolved := filepath.Join(filepath.Dir(target), linkTarget)
			if _, err := safeJoin(destDir, mustRel(destDir, resolved)); err != nil {
				return nil, fmt.Errorf("extract: symlink %q escapes destination (refused)", hdr.Name)
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				return nil, fmt.Errorf("extract: create symlink %s: %w", hdr.Name, err)
			}
		default:
			// Skip sockets, devices, hardlinks, and other special entries.
			continue
		}
	}
}

// normalizeRestorePaths cleans and dedupes the requested selective-restore
// paths (order-preserving). Absolute source paths (the server contract)
// and archive-relative paths are both accepted; empty, "." and
// ".."-escaping requests are rejected. Reports use this normalized form.
func normalizeRestorePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		c := path.Clean(strings.TrimSpace(p))
		if c == "" || c == "." {
			return nil, fmt.Errorf("invalid restore path %q: empty path", p)
		}
		if c == ".." || strings.HasPrefix(c, "../") {
			return nil, fmt.Errorf("invalid restore path %q: must not escape the snapshot root", p)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// matchRestoreWants returns the subset of normalized wants covered by one
// tar entry name: exact hits plus directory prefixes. Absolute wants (e.g.
// "/srv/data/a.txt") match archive entries ("data/a.txt") by segment-aligned
// suffix, since archives store entries under the source base name and the
// agent is not told the source root; archive-relative wants match by exact
// or dir-prefix comparison. Traversal and absolute entry names can never
// match: no want cleans to ".." or carries a leading slash past its root.
func matchRestoreWants(entry string, wants []string) []string {
	e := path.Clean(strings.TrimPrefix(strings.TrimSpace(entry), "./"))
	if e == "" || e == "." || e == ".." || strings.HasPrefix(e, "../") || strings.HasPrefix(e, "/") {
		return nil
	}
	var hits []string
	for _, w := range wants {
		if strings.HasPrefix(w, "/") {
			if strings.HasSuffix(w, "/"+e) {
				hits = append(hits, w)
				continue
			}
			// Directory request: the entry extends a segment-aligned
			// tail of the want ("/srv/data" covers "data/a.txt").
			for t := strings.TrimPrefix(w, "/"); ; {
				if strings.HasPrefix(e, t+"/") {
					hits = append(hits, w)
					break
				}
				i := strings.Index(t, "/")
				if i < 0 {
					break
				}
				t = t[i+1:]
			}
			continue
		}
		if e == w || strings.HasPrefix(e, w+"/") {
			hits = append(hits, w)
		}
	}
	return hits
}

// orderedMatched returns the wants present in matched, in wants order.
func orderedMatched(wants []string, matched map[string]bool) []string {
	var out []string
	for _, w := range wants {
		if matched[w] {
			out = append(out, w)
		}
	}
	return out
}

// orderedMissing returns the wants absent from matched, in wants order.
func orderedMissing(wants []string, matched map[string]bool) []string {
	var out []string
	for _, w := range wants {
		if !matched[w] {
			out = append(out, w)
		}
	}
	return out
}

func quoteEach(paths []string) string {
	q := make([]string, 0, len(paths))
	for _, p := range paths {
		q = append(q, fmt.Sprintf("%q", p))
	}
	return strings.Join(q, ", ")
}

func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return ".."
	}
	return rel
}

func checkOverwrite(target string, allow bool) error {
	if allow {
		_ = os.Remove(target)
		return nil
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("extract: refusing to overwrite existing %s without overwrite allow (set overwrite_policy=allow)", target)
	}
	return nil
}

func writeFile(target string, tr *tar.Reader, hdr *tar.Header, total *int64, maxTotal int64) error {
	//nolint:gosec // target is confined to destDir by safeJoin.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
	if err != nil {
		return fmt.Errorf("extract: create %s: %w", hdr.Name, err)
	}
	n, copyErr := io.CopyN(f, tr, maxTotal-*total+1)
	*total += n
	closeErr := f.Close()
	if copyErr != nil && copyErr != io.EOF {
		return fmt.Errorf("extract: write %s: %w", hdr.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("extract: close %s: %w", hdr.Name, closeErr)
	}
	if *total > maxTotal {
		return fmt.Errorf("extract: payload exceeds %d bytes", maxTotal)
	}
	return nil
}

// WriteRestoreFile streams a non-tar payload (db dump) to destDir/name.
func WriteRestoreFile(r io.Reader, destDir, name string, allowOverwrite bool, maxTotalBytes int64) error {
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultArchiveMaxBytes
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("extract: create destination: %w", err)
	}
	target, err := safeJoin(destDir, name)
	if err != nil {
		return err
	}
	if err := checkOverwrite(target, allowOverwrite); err != nil {
		return err
	}
	//nolint:gosec // target is confined to destDir by safeJoin.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("extract: create %s: %w", name, err)
	}
	n, copyErr := io.CopyN(f, r, maxTotalBytes+1)
	closeErr := f.Close()
	if copyErr != nil && copyErr != io.EOF {
		_ = os.Remove(target)
		return fmt.Errorf("extract: write %s: %w", name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("extract: close %s: %w", name, closeErr)
	}
	if n > maxTotalBytes {
		_ = os.Remove(target)
		return fmt.Errorf("extract: payload exceeds %d bytes", maxTotalBytes)
	}
	return nil
}
