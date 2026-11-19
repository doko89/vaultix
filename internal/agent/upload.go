package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// Upload tuning: objects at or under SingleUploadMaxBytes go out as one
// presigned PUT; larger objects use the multipart endpoints.
const (
	SingleUploadMaxBytes = 100 << 20
	MultipartPartSize    = 16 << 20
	uploadMaxRetries     = 5
	uploadInitialBackoff = time.Second
	uploadMaxBackoff     = 30 * time.Second
)

// Uploader pushes staged artifacts to presigned URLs with plain stdlib HTTP.
// It never attaches the manager bearer credential: presigned URLs carry
// their own auth, and extra headers would break the signature.
type Uploader struct {
	Manager ManagerClient
	// HTTP performs presigned requests; defaults to a bare client. Tests
	// inject httptest-backed transports through grant URLs instead.
	HTTP *http.Client
	// NewBackOff builds the retry strategy; defaults to exponential backoff.
	NewBackOff func() backoff.BackOff
	// OnProgress reports cumulative uploaded bytes during a job upload.
	OnProgress func(uploadedBytes int64)
}

func (u *Uploader) httpClient() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (u *Uploader) newBackOff() backoff.BackOff {
	if u.NewBackOff != nil {
		return u.NewBackOff()
	}
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = uploadInitialBackoff
	b.Multiplier = 2.0
	b.MaxInterval = uploadMaxBackoff
	return b
}

// UploadedObject pairs a staged artifact with its manager-issued full key.
type UploadedObject struct {
	Artifact *Artifact
	Key      string
}

// UploadJob uploads every artifact of jobID: single PUTs go through one
// upload-grants call, multipart objects each run init→parts→complete.
// It returns uploaded objects in artifact order, or an error naming the
// failed object; callers must not report success unless err is nil.
func (u *Uploader) UploadJob(ctx context.Context, jobID string, arts []*Artifact) ([]UploadedObject, error) {
	var small, large []*Artifact
	for _, a := range arts {
		if a.SizeBytes > SingleUploadMaxBytes {
			large = append(large, a)
		} else {
			small = append(small, a)
		}
	}
	byName := map[string]UploadedObject{}
	if len(small) > 0 {
		req := UploadGrantsRequest{Objects: make([]GrantObject, 0, len(small))}
		for _, a := range small {
			req.Objects = append(req.Objects, GrantObject{Key: a.Name, SizeBytes: a.SizeBytes})
		}
		resp, err := u.Manager.UploadGrants(ctx, jobID, req)
		if err != nil {
			return nil, fmt.Errorf("agent: job %s: upload grants: %w", jobID, err)
		}
		if len(resp.Grants) != len(small) {
			return nil, fmt.Errorf("agent: job %s: upload grants: want %d, got %d", jobID, len(small), len(resp.Grants))
		}
		for i, a := range small {
			g := resp.Grants[i]
			if err := u.putObject(ctx, jobID, a, g); err != nil {
				return nil, err
			}
			byName[a.Name] = UploadedObject{Artifact: a, Key: g.Key}
		}
	}
	for _, a := range large {
		key, err := u.uploadMultipart(ctx, jobID, a)
		if err != nil {
			return nil, err
		}
		byName[a.Name] = UploadedObject{Artifact: a, Key: key}
	}
	out := make([]UploadedObject, 0, len(arts))
	for _, a := range arts {
		out = append(out, byName[a.Name])
	}
	return out, nil
}

// putObject PUTs one staged file to its presigned URL with retries.
func (u *Uploader) putObject(ctx context.Context, jobID string, a *Artifact, g UploadGrant) error {
	if strings.TrimSpace(g.URL) == "" {
		return fmt.Errorf("agent: job %s: object %s: grant has no URL", jobID, a.Name)
	}
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent: job %s: object %s: aborted: %w", jobID, a.Name, err)
		}
		lastErr = u.putOnce(ctx, a.Path, g, a.SHA256)
		if lastErr == nil {
			if u.OnProgress != nil {
				u.OnProgress(a.SizeBytes)
			}
			return nil
		}
		if sleep := bo.NextBackOff(); sleep != backoff.Stop {
			select {
			case <-ctx.Done():
				return fmt.Errorf("agent: job %s: object %s: aborted: %w", jobID, a.Name, ctx.Err())
			case <-time.After(sleep):
			}
		}
	}
	return fmt.Errorf("agent: job %s: upload object %s: %v (after %d attempts)", jobID, a.Name, lastErr, uploadMaxRetries)
}

// checksumMetaHeader carries the staged payload's checksum as S3 user
// metadata so the manager's verification gate can compare it on HEAD
// (plain ETags are MD5 and never match the sha256 manifest).
const checksumMetaHeader = "x-amz-meta-checksum"

func (u *Uploader) putOnce(ctx context.Context, path string, g UploadGrant, sha256Hex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open staged file: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat staged file: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, g.URL, f)
	if err != nil {
		return fmt.Errorf("build PUT: %w", err)
	}
	req.ContentLength = fi.Size()
	for k, v := range g.Headers {
		req.Header.Set(k, v)
	}
	// Grant headers win when the manager pins the checksum itself.
	if strings.TrimSpace(sha256Hex) != "" && req.Header.Get(checksumMetaHeader) == "" {
		req.Header.Set(checksumMetaHeader, "sha256:"+strings.ToLower(strings.TrimSpace(sha256Hex)))
	}
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", redactURL(g.URL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyByte))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT %s: status %s", redactURL(g.URL), resp.Status)
	}
	return nil
}

// uploadMultipart runs init→parts→complete for one large object, aborting
// the multipart session on any failure.
func (u *Uploader) uploadMultipart(ctx context.Context, jobID string, a *Artifact) (string, error) {
	init, err := u.Manager.MultipartInit(ctx, jobID, MultipartInitRequest{
		Key: a.Name, SizeBytes: a.SizeBytes, PartSize: MultipartPartSize,
	})
	if err != nil {
		return "", fmt.Errorf("agent: job %s: object %s: multipart init: %w", jobID, a.Name, err)
	}
	fail := func(err error) (string, error) {
		abortCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = u.Manager.MultipartAbort(abortCtx, jobID, MultipartAbortRequest{UploadID: init.UploadID, Key: init.Key})
		return "", err
	}
	nparts := int((a.SizeBytes + MultipartPartSize - 1) / MultipartPartSize)
	if len(init.PartURLs) < nparts {
		return fail(fmt.Errorf("agent: job %s: object %s: multipart init: want %d part URLs, got %d",
			jobID, a.Name, nparts, len(init.PartURLs)))
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return fail(fmt.Errorf("agent: job %s: object %s: open staged file: %w", jobID, a.Name, err))
	}
	defer func() { _ = f.Close() }()
	parts := make([]MultipartPart, 0, nparts)
	buf := make([]byte, MultipartPartSize)
	for i := 0; i < nparts; i++ {
		if err := ctx.Err(); err != nil {
			return fail(fmt.Errorf("agent: job %s: object %s: aborted: %w", jobID, a.Name, err))
		}
		n, rerr := io.ReadFull(f, buf)
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return fail(fmt.Errorf("agent: job %s: object %s: read part %d: %w", jobID, a.Name, i+1, rerr))
		}
		// Hash the exact bytes about to be PUT: single streaming pass, no
		// re-reads, one reused part-sized buffer. The completion gate
		// hash-verifies every part against these claims (multipart ETags
		// are md5 composites and carry no content hash).
		sum := sha256.Sum256(buf[:n])
		etag, err := u.putPart(ctx, init.PartURLs[i], buf[:n])
		if err != nil {
			return fail(fmt.Errorf("agent: job %s: object %s: part %d: %w", jobID, a.Name, i+1, err))
		}
		parts = append(parts, MultipartPart{Number: i + 1, ETag: etag,
			SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(n)})
		if u.OnProgress != nil {
			u.OnProgress(int64(n))
		}
	}
	if err := u.Manager.MultipartComplete(ctx, jobID, MultipartCompleteRequest{
		UploadID: init.UploadID, Key: init.Key, Parts: parts,
	}); err != nil {
		return fail(fmt.Errorf("agent: job %s: object %s: multipart complete: %w", jobID, a.Name, err))
	}
	return init.Key, nil
}

// putPart PUTs one part body with retries and returns its ETag.
func (u *Uploader) putPart(ctx context.Context, partURL string, body []byte) (string, error) {
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		etag, err := u.putPartOnce(ctx, partURL, body)
		if err == nil {
			return etag, nil
		}
		lastErr = err
		if sleep := bo.NextBackOff(); sleep != backoff.Stop {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(sleep):
			}
		}
	}
	return "", fmt.Errorf("%v (after %d attempts)", lastErr, uploadMaxRetries)
}

func (u *Uploader) putPartOnce(ctx context.Context, partURL string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, partURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build PUT: %w", err)
	}
	req.ContentLength = int64(len(body))
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT %s: %w", redactURL(partURL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyByte))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("PUT %s: status %s", redactURL(partURL), resp.Status)
	}
	etag := strings.Trim(resp.Header.Get("ETag"), `"`)
	if etag == "" {
		return "", fmt.Errorf("PUT %s: missing ETag in response", redactURL(partURL))
	}
	return etag, nil
}

// redactURL strips query strings (presigned signatures) for logs/errors.
func redactURL(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + "?…"
	}
	return u
}
