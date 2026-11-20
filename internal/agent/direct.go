package agent

// Direct header-auth uploads.
//
// When a polled job carries upload_mode:"direct" with a
// destination_credential, the agent uploads to S3 itself using SigV4 header
// authentication (see sigv4.go) instead of manager-issued presigned grants.
// This path exists because some S3 implementations reject SigV4 query-auth
// (presigned) PUTs while accepting header auth.
//
// The credential lives in memory only for the upload: it is copied into a
// byte slice at construction, never logged or persisted, and zeroed
// best-effort when the upload finishes (see V4Signer.Wipe). Error strings
// on this path carry redacted URLs and S3 codes only, never key material.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// directInitiateResult decodes POST ?uploads= responses.
type directInitiateResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	UploadID string   `xml:"UploadId"`
}

// directCompleteBody encodes POST ?uploadId= completion documents.
type directCompleteBody struct {
	XMLName xml.Name             `xml:"CompleteMultipartUpload"`
	Parts   []directCompletePart `xml:"Part"`
}

type directCompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// maxInitiateBodyBytes caps CreateMultipartUpload responses (tiny XML).
const maxInitiateBodyBytes = 64 << 10

// directEndpoint is a validated direct-upload target: endpoint addressing,
// key prefix, and the job-scoped signer.
type directEndpoint struct {
	scheme    string
	host      string
	bucket    string
	prefix    string
	pathStyle bool
	signer    V4Signer
}

// newDirectEndpoint validates cred and resolves endpoint addressing. A nil
// or incomplete credential is a loud error naming the missing piece: the
// caller must never silently fall back to presigned grants.
func newDirectEndpoint(jobID string, cred *DestinationCredential) (*directEndpoint, error) {
	if cred == nil {
		return nil, fmt.Errorf("agent: job %s: upload_mode %q but no destination_credential on the job "+
			"(manager must attach one; refusing to fall back to presigned grants)", jobID, UploadModeDirect)
	}
	if strings.TrimSpace(cred.AccessKeyID) == "" {
		return nil, fmt.Errorf("agent: job %s: direct upload: destination_credential.access_key_id is required", jobID)
	}
	if cred.SecretAccessKey == "" {
		return nil, fmt.Errorf("agent: job %s: direct upload: destination_credential.secret_access_key is required", jobID)
	}
	if strings.TrimSpace(cred.Region) == "" {
		return nil, fmt.Errorf("agent: job %s: direct upload: destination_credential.region is required", jobID)
	}
	if strings.TrimSpace(cred.Bucket) == "" {
		return nil, fmt.Errorf("agent: job %s: direct upload: destination_credential.bucket is required", jobID)
	}
	scheme, host, err := parseDirectEndpoint(cred.Endpoint, cred.Region)
	if err != nil {
		return nil, fmt.Errorf("agent: job %s: direct upload: %w", jobID, err)
	}
	prefix := strings.Trim(cred.Prefix, "/")
	if prefix != "" {
		if err := validDirectSegments(prefix); err != nil {
			return nil, fmt.Errorf("agent: job %s: direct upload: invalid prefix: %w", jobID, err)
		}
	}
	secret := []byte(cred.SecretAccessKey)
	return &directEndpoint{
		scheme: scheme, host: host, bucket: cred.Bucket, prefix: prefix,
		pathStyle: cred.UsePathStyle || isDirectIPHost(host) || !isDirectDNSBucket(cred.Bucket),
		signer:    NewV4Signer(cred.AccessKeyID, secret, cred.Region),
	}, nil
}

// parseDirectEndpoint resolves scheme/host from the credential endpoint. An
// empty endpoint selects the AWS S3 endpoint for the region.
func parseDirectEndpoint(endpoint, region string) (scheme, host string, err error) {
	if strings.TrimSpace(endpoint) == "" {
		return "https", "s3." + strings.TrimSpace(region) + ".amazonaws.com", nil
	}
	u, perr := url.Parse(strings.TrimSpace(endpoint))
	if perr != nil {
		return "", "", fmt.Errorf("destination_credential.endpoint %q: %v", endpoint, perr)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("destination_credential.endpoint %q: scheme must be http or https", endpoint)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("destination_credential.endpoint %q: host is required", endpoint)
	}
	return u.Scheme, u.Host, nil
}

// isDirectIPHost reports whether hostport is a literal IP (port allowed).
// Virtual-hosted style cannot work there (no wildcard DNS), so IP
// endpoints always use path style.
func isDirectIPHost(hostport string) bool {
	h := hostport
	if bare, _, err := net.SplitHostPort(hostport); err == nil {
		h = bare
	}
	return net.ParseIP(strings.Trim(h, "[]")) != nil
}

// isDirectDNSBucket reports whether bucket can sit in a virtual-hosted DNS
// name. Buckets that cannot always use path style.
func isDirectDNSBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || net.ParseIP(bucket) != nil {
		return false
	}
	for i := 0; i < len(bucket); i++ {
		c := bucket[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	if strings.HasPrefix(bucket, ".") || strings.HasPrefix(bucket, "-") ||
		strings.HasSuffix(bucket, ".") || strings.HasSuffix(bucket, "-") {
		return false
	}
	return !strings.Contains(bucket, "..")
}

// objectURL builds the request URL for key with a pre-encoded raw query.
// The encoded path is pinned via RawPath so the wire bytes and the signed
// canonical URI always agree.
func (e *directEndpoint) objectURL(key, rawQuery string) *url.URL {
	var host, path string
	if e.pathStyle {
		host, path = e.host, "/"+e.bucket+"/"+key
	} else {
		host, path = e.bucket+"."+e.host, "/"+key
	}
	u := &url.URL{Scheme: e.scheme, Host: host, Path: path, RawQuery: rawQuery}
	if enc := v4Escape(path, true); enc != path {
		u.RawPath = enc
	}
	return u
}

// directObjectKey derives the deterministic object key for one job's
// artifact: <prefix>/jobs/<jobID>/<name> (prefix omitted when empty). It
// rejects empty job IDs, empty/traversal object names, and bad prefixes so
// a direct upload can never address outside its job scope.
func directObjectKey(prefix, jobID, name string) (string, error) {
	if strings.TrimSpace(jobID) == "" || strings.ContainsAny(jobID, "/\\") || jobID == "." || jobID == ".." {
		return "", fmt.Errorf("invalid job id %q", jobID)
	}
	if name == "" || name[0] == '/' || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid object name %q", name)
	}
	if err := validDirectSegments(name); err != nil {
		return "", fmt.Errorf("invalid object name %q", name)
	}
	dir := "jobs/" + jobID
	if p := strings.Trim(prefix, "/"); p != "" {
		if err := validDirectSegments(p); err != nil {
			return "", fmt.Errorf("invalid prefix %q", prefix)
		}
		dir = p + "/" + dir
	}
	return dir + "/" + name, nil
}

// validDirectSegments rejects empty, dot, and dot-dot path segments.
func validDirectSegments(p string) error {
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("bad path segment in %q", p)
		}
	}
	return nil
}

// UploadJobFor uploads every artifact of job, branching on its upload mode:
// absent/"presigned" runs the classic grant flow byte-for-byte;
// "direct" uploads with header auth against the job's credential; anything
// else is a loud error (an unknown mode must never silently pick a path).
func (u *Uploader) UploadJobFor(ctx context.Context, job PolledJob, arts []*Artifact) ([]UploadedObject, error) {
	switch mode := strings.ToLower(strings.TrimSpace(job.UploadMode)); mode {
	case "", UploadModePresigned:
		return u.UploadJob(ctx, job.ID, arts)
	case UploadModeDirect:
		return u.uploadDirectJob(ctx, job, arts)
	default:
		return nil, fmt.Errorf("agent: job %s: unknown upload_mode %q (want %q or %q)",
			job.ID, job.UploadMode, UploadModePresigned, UploadModeDirect)
	}
}

// uploadDirectJob uploads every artifact with SigV4 header auth: objects at
// or under SingleUploadMaxBytes as single PUTs, larger ones multipart. The
// credential is zeroed best-effort on return.
func (u *Uploader) uploadDirectJob(ctx context.Context, job PolledJob, arts []*Artifact) ([]UploadedObject, error) {
	ep, err := newDirectEndpoint(job.ID, job.DestinationCredential)
	if err != nil {
		return nil, err
	}
	defer ep.signer.Wipe()
	keys := make([]string, len(arts))
	for i, a := range arts {
		key, kerr := directObjectKey(ep.prefix, job.ID, a.Name)
		if kerr != nil {
			return nil, fmt.Errorf("agent: job %s: object %s: %v", job.ID, a.Name, kerr)
		}
		keys[i] = key
	}
	out := make([]UploadedObject, 0, len(arts))
	for i, a := range arts {
		var uerr error
		if a.SizeBytes > SingleUploadMaxBytes {
			uerr = u.uploadMultipartDirect(ctx, ep, job.ID, a, keys[i])
		} else {
			uerr = u.putObjectDirect(ctx, ep, job.ID, a, keys[i])
		}
		if uerr != nil {
			return nil, uerr
		}
		out = append(out, UploadedObject{Artifact: a, Key: keys[i]})
	}
	return out, nil
}

// putObjectDirect PUTs one staged file to its deterministic key with
// retries. The payload is hashed in a streaming pre-pass so the signature
// always covers the exact bytes sent.
func (u *Uploader) putObjectDirect(ctx context.Context, ep *directEndpoint, jobID string, a *Artifact, key string) error {
	sumHex, err := hashStagedFile(a.Path)
	if err != nil {
		return fmt.Errorf("agent: job %s: object %s: hash staged file: %w", jobID, a.Name, err)
	}
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent: job %s: object %s: aborted: %w", jobID, a.Name, err)
		}
		lastErr = u.putObjectDirectOnce(ctx, ep, a, key, sumHex)
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

func (u *Uploader) putObjectDirectOnce(ctx context.Context, ep *directEndpoint, a *Artifact, key, sumHex string) error {
	f, err := os.Open(a.Path)
	if err != nil {
		return fmt.Errorf("open staged file: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat staged file: %w", err)
	}
	target := ep.objectURL(key, "").String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, f)
	if err != nil {
		return fmt.Errorf("build PUT: %w", err)
	}
	req.ContentLength = fi.Size()
	if attested := strings.ToLower(strings.TrimSpace(a.SHA256)); attested != "" {
		req.Header.Set(checksumMetaHeader, "sha256:"+attested)
	}
	ep.signer.Sign(req, sumHex, time.Now().UTC())
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", redactURL(target), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return putStatusError(http.MethodPut, target, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// hashStagedFile returns the lowercase hex sha256 of the file at path.
func hashStagedFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// uploadMultipartDirect runs CreateMultipartUpload→UploadPart*→
// CompleteMultipartUpload against S3, aborting the session on any failure.
// The whole-object checksum is stamped at initiate time (S3 applies
// initiate-time metadata to the completed object), so later HEAD-based
// verification reads it exactly like the single-PUT path.
func (u *Uploader) uploadMultipartDirect(ctx context.Context, ep *directEndpoint, jobID string, a *Artifact, key string) (err error) {
	uploadID, err := u.directInitiate(ctx, ep, a, key)
	if err != nil {
		return fmt.Errorf("agent: job %s: object %s: direct multipart init: %w", jobID, a.Name, err)
	}
	fail := func(ferr error) error {
		abortCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = u.directAbort(abortCtx, ep, key, uploadID)
		return ferr
	}
	nparts := int((a.SizeBytes + MultipartPartSize - 1) / MultipartPartSize)
	f, err := os.Open(a.Path)
	if err != nil {
		return fail(fmt.Errorf("agent: job %s: object %s: open staged file: %w", jobID, a.Name, err))
	}
	defer func() { _ = f.Close() }()
	parts := make([]directCompletePart, 0, nparts)
	buf := make([]byte, MultipartPartSize)
	for i := 0; i < nparts; i++ {
		if err := ctx.Err(); err != nil {
			return fail(fmt.Errorf("agent: job %s: object %s: aborted: %w", jobID, a.Name, err))
		}
		n, rerr := io.ReadFull(f, buf)
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return fail(fmt.Errorf("agent: job %s: object %s: read part %d: %w", jobID, a.Name, i+1, rerr))
		}
		etag, perr := u.putPartDirect(ctx, ep, key, uploadID, i+1, buf[:n])
		if perr != nil {
			return fail(fmt.Errorf("agent: job %s: object %s: part %d: %w", jobID, a.Name, i+1, perr))
		}
		parts = append(parts, directCompletePart{PartNumber: i + 1, ETag: etag})
		if u.OnProgress != nil {
			u.OnProgress(int64(n))
		}
	}
	if err := u.directComplete(ctx, ep, key, uploadID, parts); err != nil {
		return fail(fmt.Errorf("agent: job %s: object %s: direct multipart complete: %w", jobID, a.Name, err))
	}
	return nil
}

// directInitiate starts a multipart upload with retries and returns the
// provider upload ID.
func (u *Uploader) directInitiate(ctx context.Context, ep *directEndpoint, a *Artifact, key string) (string, error) {
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		uploadID, err := u.directInitiateOnce(ctx, ep, a, key)
		if err == nil {
			return uploadID, nil
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

func (u *Uploader) directInitiateOnce(ctx context.Context, ep *directEndpoint, a *Artifact, key string) (string, error) {
	target := ep.objectURL(key, "uploads=").String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return "", fmt.Errorf("build POST: %w", err)
	}
	if attested := strings.ToLower(strings.TrimSpace(a.SHA256)); attested != "" {
		req.Header.Set(checksumMetaHeader, "sha256:"+attested)
	}
	ep.signer.Sign(req, v4EmptySHA256, time.Now().UTC())
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", redactURL(target), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", putStatusError(http.MethodPost, target, resp)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxInitiateBodyBytes))
	var out directInitiateResult
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("POST %s: decode initiate response: %w", redactURL(target), err)
	}
	if strings.TrimSpace(out.UploadID) == "" {
		return "", fmt.Errorf("POST %s: empty upload id in response", redactURL(target))
	}
	return out.UploadID, nil
}

// putPartDirect PUTs one part body with retries and returns its ETag.
func (u *Uploader) putPartDirect(ctx context.Context, ep *directEndpoint, key, uploadID string, number int, body []byte) (string, error) {
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		etag, err := u.putPartDirectOnce(ctx, ep, key, uploadID, number, body)
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

func (u *Uploader) putPartDirectOnce(ctx context.Context, ep *directEndpoint, key, uploadID string, number int, body []byte) (string, error) {
	sum := sha256.Sum256(body)
	query := fmt.Sprintf("partNumber=%d&uploadId=%s", number, v4Escape(uploadID, false))
	target := ep.objectURL(key, query).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build PUT: %w", err)
	}
	req.ContentLength = int64(len(body))
	ep.signer.Sign(req, hex.EncodeToString(sum[:]), time.Now().UTC())
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT %s: %w", redactURL(target), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", putStatusError(http.MethodPut, target, resp)
	}
	etag := strings.Trim(resp.Header.Get("ETag"), `"`)
	if etag == "" {
		return "", fmt.Errorf("PUT %s: missing ETag in response", redactURL(target))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return etag, nil
}

// directComplete submits the CompleteMultipartUpload XML document with
// retries.
func (u *Uploader) directComplete(ctx context.Context, ep *directEndpoint, key, uploadID string, parts []directCompletePart) error {
	payload, err := xml.Marshal(directCompleteBody{Parts: parts})
	if err != nil {
		return fmt.Errorf("encode complete request: %w", err)
	}
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	bo := u.newBackOff()
	var lastErr error
	for attempt := 0; attempt < uploadMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cerr := u.directCompleteOnce(ctx, ep, key, uploadID, payload, sumHex); cerr == nil {
			return nil
		} else {
			lastErr = cerr
		}
		if sleep := bo.NextBackOff(); sleep != backoff.Stop {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
		}
	}
	return fmt.Errorf("%v (after %d attempts)", lastErr, uploadMaxRetries)
}

func (u *Uploader) directCompleteOnce(ctx context.Context, ep *directEndpoint, key, uploadID string, payload []byte, sumHex string) error {
	target := ep.objectURL(key, "uploadId="+v4Escape(uploadID, false)).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build POST: %w", err)
	}
	req.ContentLength = int64(len(payload))
	req.Header.Set("Content-Type", "application/xml")
	ep.signer.Sign(req, sumHex, time.Now().UTC())
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", redactURL(target), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return putStatusError(http.MethodPost, target, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// directAbort cancels an in-flight multipart upload. Single attempt: abort
// runs on the failure path where the primary error is what matters.
func (u *Uploader) directAbort(ctx context.Context, ep *directEndpoint, key, uploadID string) error {
	target := ep.objectURL(key, "uploadId="+v4Escape(uploadID, false)).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	ep.signer.Sign(req, v4EmptySHA256, time.Now().UTC())
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return putStatusError(http.MethodDelete, target, resp)
	}
	return nil
}
