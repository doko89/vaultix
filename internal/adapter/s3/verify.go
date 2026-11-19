package s3

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// Integrity verification (PRD §7.12): uploaded objects are verified via HEAD
// + checksum comparison before a snapshot may be marked successful. Head
// records the stored size/checksum; VerifyObjects compares them against the
// manifest expectations and reports per-object results.

// ExpectedObject is one manifest entry to verify.
type ExpectedObject struct {
	Key      string
	Checksum string // "algorithm:hex", e.g. "sha256:ab12..."
	// Size is compared only when CheckSize is set.
	Size      uint64
	CheckSize bool
}

// VerifyResult is the per-object verification outcome.
type VerifyResult struct {
	ObjectKey string
	OK        bool
	// Reason explains a negative outcome (mismatch, missing object).
	Reason string
}

// VerifyObjects HEADs each expected key and compares stored size/checksum.
// It returns allOK=false with per-object reasons on any mismatch; a non-nil
// error means the gate itself could not decide (transport failure) and must
// be treated as not-verified, never as success.
func (c *Client) VerifyObjects(ctx context.Context, expected []ExpectedObject) (bool, []VerifyResult, error) {
	if len(expected) == 0 {
		return false, nil, fmt.Errorf("s3: verify: nothing to verify")
	}
	results := make([]VerifyResult, 0, len(expected))
	allOK := true
	for _, e := range expected {
		meta, err := c.Head(ctx, e.Key)
		if err != nil {
			return false, results, fmt.Errorf("s3: verify %q: %w", e.Key, err)
		}
		res := VerifyResult{ObjectKey: e.Key, OK: true}
		switch {
		case !meta.Exists:
			res.OK = false
			res.Reason = "object not found"
		case e.Checksum != "" && !checksumEqual(e.Checksum, meta.Checksum):
			res.OK = false
			res.Reason = fmt.Sprintf("checksum mismatch: want %s got %s",
				e.Checksum, displayChecksum(meta.Checksum))
		case e.CheckSize && meta.Size != e.Size:
			res.OK = false
			res.Reason = fmt.Sprintf("size mismatch: want %d got %d", e.Size, meta.Size)
		}
		if !res.OK {
			allOK = false
		}
		results = append(results, res)
	}
	return allOK, results, nil
}

// checksumFromHeaders extracts the stored checksum in "algorithm:hex" form,
// preferring explicit checksum headers over the ETag.
func checksumFromHeaders(h http.Header) string {
	if v := strings.TrimSpace(h.Get("x-amz-checksum-sha256")); v != "" {
		if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
			return "sha256:" + hex.EncodeToString(raw)
		}
		return "sha256-b64:" + v
	}
	if v := strings.TrimSpace(h.Get("x-amz-meta-checksum")); v != "" {
		return v
	}
	etag := trimETag(h.Get("ETag"))
	if etag == "" {
		return ""
	}
	if isMD5Hex(etag) {
		return "md5:" + strings.ToLower(etag)
	}
	return "etag:" + etag
}

// trimETag strips quotes and weak prefixes from an ETag header value.
func trimETag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "W/")
	return strings.Trim(v, `"`)
}

// isMD5Hex reports whether s looks like a single-part MD5 ETag.
func isMD5Hex(s string) bool {
	if len(s) != 32 {
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

// checksumEqual compares checksums case-insensitively after trimming.
func checksumEqual(want, got string) bool {
	return strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got))
}

func displayChecksum(v string) string {
	if v == "" {
		return "<none>"
	}
	return v
}
