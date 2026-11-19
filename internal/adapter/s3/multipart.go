package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// Multipart orchestration for large objects (PRD §7.12): the Manager
// initiates and completes uploads while the agent PUTs each part directly to
// a presigned part URL. Part grants are scoped to the job's key exactly like
// single-object grants.
//
// CHECKSUM RECONCILIATION (multipart ETags are md5-of-md5s composites, never
// content hashes, so they can never be compared to the manifest's sha256):
// completion is a verification gate, not a blind assemble. The agent ships
// the sha256 + exact length of every part it PUT; CompleteMultipartUpload
// assembles, then re-reads every stored part with ranged GETs and compares
// hashes. Only verified bytes are stamped: the manager hashes the stored
// bytes itself and persists that whole-object sha256 as x-amz-meta-checksum
// on the object (single CopyObject self-copy up to 5 GiB, multipart copy
// above). Later HEAD-based verification then reads the stamped checksum and
// behaves EXACTLY like the single-PUT path (see verify.go), which needs no
// multipart awareness on its happy path. Anything unverifiable — tampered or
// truncated parts, missing checksums, a failed stamp — fails completion and
// deletes the assembled object, so it can never verify later. Only plain
// HEAD/GET/PUT/POST/DELETE calls are used (S3Mock-compatible); no
// GetObjectAttributes or checksum-mode APIs.
//
// Per-part checksums are verified synchronously at completion rather than
// persisted: S3 user metadata is capped (~2 KB) and tags at 10 per object,
// so neither scales to thousands of parts. The durable artifact is the
// whole-object checksum metadata, computed over bytes the manager hashed
// itself (never the agent's claim), which is what later verification reads.

// CompletedPart identifies one uploaded part for completion. SHA256 is the
// bare hex sha256 of the exact part bytes the agent PUT; SizeBytes is that
// part's byte length. Both are required: completion hash-verifies every part
// against them, because the S3 ETag cannot serve that purpose.
type CompletedPart struct {
	PartNumber int
	ETag       string
	SHA256     string
	SizeBytes  int64
}

// maxS3PartBytes is the S3 per-part ceiling (5 GiB). Claimed part sizes
// above it are rejected before any arithmetic, which also keeps the parts
// size sum far from int64 overflow (10000 parts max).
const maxS3PartBytes = int64(5 << 30)

// stampCopyThresholdBytes selects the checksum-stamp tier: objects at or
// under 5 GiB are stamped with one CopyObject self-copy (the S3 single-copy
// ceiling); larger objects are re-materialized with a multipart copy whose
// initiate carries the checksum metadata. Package vars so tests exercise
// both tiers with tiny bodies.
var (
	stampCopyThresholdBytes = int64(5 << 30)
	stampCopyPartSize       = int64(64 << 20)
)

// minCopyPartSize is the S3 floor for non-terminal multipart-copy parts.
const minCopyPartSize = int64(5 << 20)

type initiateMultipartResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	UploadID string   `xml:"UploadId"`
}

type completeMultipartBody struct {
	XMLName xml.Name            `xml:"CompleteMultipartUpload"`
	Xmlns   string              `xml:"xmlns,attr,omitempty"`
	Parts   []completePartEntry `xml:"Part"`
}

type completePartEntry struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type copyPartResult struct {
	XMLName xml.Name `xml:"CopyPartResult"`
	ETag    string   `xml:"ETag"`
}

// InitiateMultipartUpload starts a multipart upload for key, which must
// belong to jobID's scope, and returns the provider upload ID.
func (c *Client) InitiateMultipartUpload(ctx context.Context, jobID, key string) (string, error) {
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return "", err
	}
	return c.initiateMultipartUpload(ctx, key)
}

// initiateMultipartUpload starts a multipart upload for a job-scoped key
// without binding a specific job (port-level use).
func (c *Client) initiateMultipartUpload(ctx context.Context, key string) (string, error) {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return "", err
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploads="
	resp, err := c.roundTrip(ctx, http.MethodPost, rawURL, nil, "")
	if err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: %w", err)
	}
	return parseInitiateResult(resp.body)
}

// initiateMultipartUploadWith starts a multipart upload with extra request
// headers (used by the >5 GiB checksum stamp to attach object metadata at
// initiate time, which S3 applies to the completed object).
func (c *Client) initiateMultipartUploadWith(ctx context.Context, key string, headers map[string]string) (string, error) {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return "", err
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploads="
	resp, err := c.doWithHeaders(ctx, http.MethodPost, rawURL, nil, "", headers, 1<<20)
	if err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: %w", err)
	}
	return parseInitiateResult(resp.body)
}

func parseInitiateResult(body []byte) (string, error) {
	var out initiateMultipartResult
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: decode response: %w", err)
	}
	if out.UploadID == "" {
		return "", fmt.Errorf("s3: initiate multipart upload: empty upload id")
	}
	return out.UploadID, nil
}

// PresignUploadPart mints a short-lived PUT grant for one part. Part numbers
// run 1..10000 per the S3 API.
func (c *Client) PresignUploadPart(ctx context.Context, jobID, key, uploadID string, partNumber int, ttl time.Duration) (Grant, error) {
	if err := ctx.Err(); err != nil {
		return Grant{}, fmt.Errorf("s3: presign part: %w", err)
	}
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return Grant{}, err
	}
	if uploadID == "" {
		return Grant{}, fmt.Errorf("s3: presign part: upload id is required")
	}
	if partNumber < 1 || partNumber > 10000 {
		return Grant{}, fmt.Errorf("s3: presign part: part number %d out of range 1..10000", partNumber)
	}
	sub := map[string]string{
		"partNumber": strconv.Itoa(partNumber),
		"uploadId":   uploadID,
	}
	g, err := c.presign(http.MethodPut, key, sub, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("s3: presign part: %w", err)
	}
	return g, nil
}

// CompleteMultipartUpload finishes an upload previously started with
// InitiateMultipartUpload, then verifies and stamps it: every stored part is
// re-read and hash-compared against its claimed sha256, and the manager's
// own whole-object sha256 over the verified bytes is persisted as
// x-amz-meta-checksum metadata. Parts are sorted by number before submission
// and before verification. Any post-assemble failure deletes the assembled
// object (best effort) and returns an error, so unverified bytes can never
// verify later.
func (c *Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return err
	}
	if uploadID == "" {
		return fmt.Errorf("s3: complete multipart upload: upload id is required")
	}
	if len(parts) == 0 {
		return fmt.Errorf("s3: complete multipart upload: at least one part is required")
	}
	ordered := slices.Clone(parts)
	slices.SortFunc(ordered, func(a, b CompletedPart) int { return a.PartNumber - b.PartNumber })
	entries := make([]completePartEntry, 0, len(ordered))
	for i, p := range ordered {
		if p.PartNumber < 1 || p.PartNumber > 10000 {
			return fmt.Errorf("s3: complete multipart upload: part number %d out of range 1..10000", p.PartNumber)
		}
		if i > 0 && p.PartNumber == ordered[i-1].PartNumber {
			return fmt.Errorf("s3: complete multipart upload: duplicate part number %d", p.PartNumber)
		}
		if p.ETag == "" {
			return fmt.Errorf("s3: complete multipart upload: part %d ETag is required", p.PartNumber)
		}
		if strings.TrimSpace(p.SHA256) == "" {
			return fmt.Errorf("s3: complete multipart upload: part %d sha256 checksum is required", p.PartNumber)
		}
		if !isSHA256Hex(p.SHA256) {
			return fmt.Errorf("s3: complete multipart upload: part %d sha256 must be 64 hex chars", p.PartNumber)
		}
		if p.SizeBytes <= 0 {
			return fmt.Errorf("s3: complete multipart upload: part %d size_bytes must be positive", p.PartNumber)
		}
		if p.SizeBytes > maxS3PartBytes {
			return fmt.Errorf("s3: complete multipart upload: part %d size %d exceeds the 5 GiB part ceiling", p.PartNumber, p.SizeBytes)
		}
		entries = append(entries, completePartEntry{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	if err := c.postCompleteMultipart(ctx, key, uploadID, entries); err != nil {
		return err
	}
	if err := c.verifyPartsAndStamp(ctx, key, ordered); err != nil {
		if delErr := c.Delete(ctx, key); delErr != nil {
			return errors.Join(err, fmt.Errorf("s3: complete multipart upload: remove unverified object: %w", delErr))
		}
		return err
	}
	return nil
}

// postCompleteMultipart submits one CompleteMultipartUpload XML document.
func (c *Client) postCompleteMultipart(ctx context.Context, key, uploadID string, parts []completePartEntry) error {
	payload, err := xml.Marshal(completeMultipartBody{Parts: parts})
	if err != nil {
		return fmt.Errorf("s3: complete multipart upload: encode request: %w", err)
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploadId=" + encodeComponent(uploadID, false)
	if _, err := c.roundTrip(ctx, http.MethodPost, rawURL, payload, "application/xml"); err != nil {
		return fmt.Errorf("s3: complete multipart upload: %w", err)
	}
	return nil
}

// verifyPartsAndStamp hash-verifies every assembled part against its claimed
// sha256 via ranged GETs, then persists the manager-computed whole-object
// sha256 as object metadata. The claimed part sizes must sum to the stored
// size exactly, and every ranged read must return its full length: short
// reads or hash mismatches mean truncated or tampered parts.
func (c *Client) verifyPartsAndStamp(ctx context.Context, key string, ordered []CompletedPart) error {
	meta, err := c.Head(ctx, key)
	if err != nil {
		return fmt.Errorf("s3: complete multipart upload: verify: head assembled object: %w", err)
	}
	if !meta.Exists {
		return fmt.Errorf("s3: complete multipart upload: verify: object %q missing after complete", key)
	}
	var sum int64
	for _, p := range ordered {
		sum += p.SizeBytes
	}
	if meta.Size != uint64(sum) {
		return fmt.Errorf("s3: complete multipart upload: verify: parts sum to %d bytes but storage holds %d (truncated?)",
			sum, meta.Size)
	}
	whole := sha256.New()
	var offset int64
	for _, p := range ordered {
		got, err := c.getObjectRange(ctx, key, offset, p.SizeBytes)
		if err != nil {
			return fmt.Errorf("s3: complete multipart upload: verify: part %d: %w", p.PartNumber, err)
		}
		partSum := sha256.Sum256(got)
		if !strings.EqualFold(hex.EncodeToString(partSum[:]), strings.TrimSpace(p.SHA256)) {
			return fmt.Errorf("s3: complete multipart upload: verify: part %d: sha256 mismatch (tampered?)", p.PartNumber)
		}
		_, _ = whole.Write(got)
		offset += p.SizeBytes
	}
	return c.stampObjectChecksum(ctx, key, meta.Size, hex.EncodeToString(whole.Sum(nil)))
}

// getObjectRange reads exactly length bytes at offset via a ranged GET. Any
// short read fails: S3 must serve the full range of an intact object.
func (c *Client) getObjectRange(ctx context.Context, key string, offset, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, fmt.Errorf("s3: range get: length must be positive")
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath)
	headers := map[string]string{"Range": "bytes=" + strconv.FormatInt(offset, 10) + "-" + strconv.FormatInt(offset+length-1, 10)}
	resp, err := c.doWithHeaders(ctx, http.MethodGet, rawURL, nil, "", headers, length)
	if err != nil {
		return nil, err
	}
	if int64(len(resp.body)) != length {
		return nil, fmt.Errorf("s3: range get: want %d bytes at offset %d, got %d (truncated?)",
			length, offset, len(resp.body))
	}
	return resp.body, nil
}

// stampObjectChecksum persists wholeHex (bare hex sha256 over verified
// bytes) as the object's x-amz-meta-checksum metadata, in the exact shape
// single-PUT uploads carry, so later HEAD verification is identical.
func (c *Client) stampObjectChecksum(ctx context.Context, key string, size uint64, wholeHex string) error {
	value := "sha256:" + strings.ToLower(strings.TrimSpace(wholeHex))
	if size <= uint64(stampCopyThresholdBytes) {
		return c.copyObjectWithChecksum(ctx, key, value)
	}
	return c.multipartCopyWithChecksum(ctx, key, size, value)
}

// copyObjectWithChecksum stamps small-enough objects with one CopyObject
// self-copy that replaces metadata (server-side; no bytes cross the wire).
func (c *Client) copyObjectWithChecksum(ctx context.Context, key, checksumValue string) error {
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath)
	headers := map[string]string{
		"x-amz-copy-source":        copySource(c.bucket, key),
		"x-amz-metadata-directive": "REPLACE",
		"x-amz-meta-checksum":      checksumValue,
	}
	if _, err := c.doWithHeaders(ctx, http.MethodPut, rawURL, nil, "", headers, 1<<20); err != nil {
		return fmt.Errorf("s3: stamp checksum: copy object: %w", err)
	}
	return nil
}

// multipartCopyWithChecksum stamps objects above the single-copy ceiling by
// re-materializing the key through a multipart copy: the copy upload is
// initiated with the checksum metadata attached, every byte range is copied
// server-side, and completion applies the metadata to the new object. Copy
// ranges are sized to stay within the S3 copy-part rules (>= 5 MiB except
// the last, <= 5 GiB, <= 10000 parts).
func (c *Client) multipartCopyWithChecksum(ctx context.Context, key string, size uint64, checksumValue string) error {
	if size == 0 {
		return fmt.Errorf("s3: stamp checksum: multipart copy needs a non-empty object")
	}
	uploadID, err := c.initiateMultipartUploadWith(ctx, key, map[string]string{"x-amz-meta-checksum": checksumValue})
	if err != nil {
		return fmt.Errorf("s3: stamp checksum: initiate copy upload: %w", err)
	}
	fail := func(err error) error {
		// Best effort: a dangling copy upload holds no assembled bytes and
		// expires via bucket lifecycle; the primary error is what matters.
		_ = c.AbortMultipartUpload(ctx, key, uploadID)
		return err
	}
	partSize := stampCopyPartSize
	if need := (int64(size) + 9999) / 10000; need > partSize {
		partSize = need
	}
	if partSize < minCopyPartSize && int64(size) > minCopyPartSize {
		partSize = minCopyPartSize
	}
	nparts := int((int64(size) + partSize - 1) / partSize)
	entries := make([]completePartEntry, 0, nparts)
	for i := 0; i < nparts; i++ {
		start := int64(i) * partSize
		end := min(start+partSize, int64(size)) - 1
		etag, err := c.uploadPartCopy(ctx, key, uploadID, i+1, start, end)
		if err != nil {
			return fail(fmt.Errorf("s3: stamp checksum: copy part %d: %w", i+1, err))
		}
		entries = append(entries, completePartEntry{PartNumber: i + 1, ETag: etag})
	}
	if err := c.postCompleteMultipart(ctx, key, uploadID, entries); err != nil {
		return fail(fmt.Errorf("s3: stamp checksum: complete copy upload: %w", err))
	}
	return nil
}

// uploadPartCopy copies one byte range of key into partNumber of the copy
// upload and returns the copied part's ETag.
func (c *Client) uploadPartCopy(ctx context.Context, key, uploadID string, partNumber int, start, end int64) (string, error) {
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) +
		"?partNumber=" + strconv.Itoa(partNumber) + "&uploadId=" + encodeComponent(uploadID, false)
	headers := map[string]string{
		"x-amz-copy-source":       copySource(c.bucket, key),
		"x-amz-copy-source-range": "bytes=" + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10),
	}
	resp, err := c.doWithHeaders(ctx, http.MethodPut, rawURL, nil, "", headers, 1<<20)
	if err != nil {
		return "", err
	}
	var out copyPartResult
	if err := xml.Unmarshal(resp.body, &out); err != nil {
		return "", fmt.Errorf("s3: upload part copy: decode response: %w", err)
	}
	etag := trimETag(out.ETag)
	if etag == "" {
		return "", fmt.Errorf("s3: upload part copy: empty ETag in response")
	}
	return etag, nil
}

// copySource renders the x-amz-copy-source header value (/bucket/key with
// each key segment URL-encoded, slashes preserved).
func copySource(bucket, key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = encodeComponent(s, false)
	}
	return "/" + bucket + "/" + strings.Join(segs, "/")
}

// doWithHeaders mirrors roundTrip's timeout/retry/error contract but attaches
// caller-supplied request headers (Range reads, CopyObject directives) and
// returns up to maxBody response bytes. It lives here rather than in
// client.go only because those call sites are all multipart paths; behavior
// (transient retry with backoff, Retry-After, redacted errors) matches
// roundTrip exactly.
func (c *Client) doWithHeaders(ctx context.Context, method, rawURL string, body []byte, contentType string, headers map[string]string, maxBody int64) (response, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	payload := body
	if payload == nil {
		payload = []byte{}
	}
	if maxBody < 0 {
		maxBody = 0
	}
	op := func() (response, error) {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(payload))
		if err != nil {
			return response{}, backoff.Permanent(fmt.Errorf("s3: build request: %w", err))
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		c.signHeader(req, sha256HexBytes(payload), c.now().UTC())
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return response{}, err // network failure: retryable
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		header := resp.Header.Clone()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return response{status: resp.StatusCode, header: header, body: respBody}, nil
		}
		serr := &statusError{
			method: method,
			url:    RedactURL(rawURL),
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(respBody)),
		}
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode >= 500 {
			if secs, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				return response{}, errors.Join(serr, backoff.RetryAfter(secs))
			}
			return response{}, serr
		}
		return response{}, backoff.Permanent(serr)
	}
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = c.retryInitial
	return backoff.Retry(ctx, op,
		backoff.WithBackOff(bo),
		backoff.WithMaxTries(c.maxTries),
	)
}

// AbortMultipartUpload cancels an in-flight multipart upload, releasing
// staged parts.
func (c *Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return err
	}
	if uploadID == "" {
		return fmt.Errorf("s3: abort multipart upload: upload id is required")
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploadId=" + encodeComponent(uploadID, false)
	if _, err := c.roundTrip(ctx, http.MethodDelete, rawURL, nil, ""); err != nil {
		return fmt.Errorf("s3: abort multipart upload: %w", err)
	}
	return nil
}

// isSHA256Hex reports whether s is a 64-char ASCII hex digest.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F' {
			continue
		}
		return false
	}
	return true
}

// parseCopySource splits an x-amz-copy-source value (/bucket/key) back into
// bucket and key, URL-decoding the key.
func parseCopySource(v string) (bucket, key string, err error) {
	trimmed := strings.TrimPrefix(v, "/")
	b, k, ok := strings.Cut(trimmed, "/")
	if !ok || b == "" || k == "" {
		return "", "", fmt.Errorf("invalid copy source %q", v)
	}
	decoded, err := url.PathUnescape(k)
	if err != nil {
		return "", "", fmt.Errorf("invalid copy source %q: %w", v, err)
	}
	return b, decoded, nil
}
