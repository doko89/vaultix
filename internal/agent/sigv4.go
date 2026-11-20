package agent

// SigV4 header authentication for agent-direct S3 uploads.
//
// This signer is implemented from the AWS Signature Version 4 specification
// (https://docs.aws.amazon.com/IAM/latest/UserGuide/create-signed-request.html)
// and verified against the published AWS test vectors in sigv4_test.go. It
// intentionally shares no code with the manager-side adapter: the agent
// must stay dependency-light and wire-compatible, not code-coupled.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	v4Algorithm   = "AWS4-HMAC-SHA256"
	v4ServiceS3   = "s3"
	v4Terminator  = "aws4_request"
	v4DateFormat  = "20060102T150405Z"
	v4DayFormat   = "20060102"
	v4EmptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// V4Signer applies SigV4 header authentication to direct S3 requests. The
// zero value is useless; build one with NewV4Signer. SecretKey is held as a
// byte slice so callers can zero it after the job (see Wipe).
type V4Signer struct {
	AccessKey string
	SecretKey []byte
	Region    string
	Service   string
}

// NewV4Signer builds an S3 signer for the given credential and region.
func NewV4Signer(accessKey string, secretKey []byte, region string) V4Signer {
	return V4Signer{AccessKey: accessKey, SecretKey: secretKey, Region: region, Service: v4ServiceS3}
}

// Wipe zeroes the secret key bytes. Best effort: copies the runtime made
// (string conversions at signing time) cannot be reached.
func (s V4Signer) Wipe() {
	for i := range s.SecretKey {
		s.SecretKey[i] = 0
	}
}

// Sign authenticates req with the S3 header set (host,
// x-amz-content-sha256, x-amz-date). payloadSHA256Hex is the lowercase hex
// sha256 of the exact request body. now is the signing instant (UTC).
func (s V4Signer) Sign(req *http.Request, payloadSHA256Hex string, now time.Time) {
	stamp := now.UTC().Format(v4DateFormat)
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payloadSHA256Hex)
	s.signWith(req, payloadSHA256Hex, []string{"host", "x-amz-content-sha256", "x-amz-date"}, stamp)
}

// signWith signs req over the named (lowercase, pre-sorted) headers and
// sets the Authorization header. amzDate must already be stamped on the
// request when it is one of the signed headers.
func (s V4Signer) signWith(req *http.Request, payloadHash string, names []string, amzDate string) {
	headers := make([][2]string, 0, len(names))
	for _, name := range names {
		value := req.Header.Get(name)
		if name == "host" {
			value = req.Host
			if value == "" {
				value = req.URL.Host
			}
		}
		headers = append(headers, [2]string{name, value})
	}
	creq, signedHeaders := v4CanonicalRequest(req.Method, v4RequestPath(req), req.URL.Query(), headers, payloadHash)
	day := amzDate[:8]
	scope := day + "/" + s.Region + "/" + s.Service + "/" + v4Terminator
	sts := v4StringToSign(amzDate, scope, creq)
	sig := v4Hex(v4HMAC(v4SigningKey(s.SecretKey, day, s.Region, s.Service), sts))
	req.Header.Set("Authorization", v4Algorithm+" Credential="+s.AccessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+sig)
}

// v4RequestPath returns the encoded request path for the canonical request,
// defaulting an empty path to "/".
func v4RequestPath(req *http.Request) string {
	if p := req.URL.EscapedPath(); p != "" {
		return p
	}
	return "/"
}

// v4CanonicalRequest renders the canonical request and the semicolon-joined
// signed header names. headers must be sorted by lowercase name already.
func v4CanonicalRequest(method, encodedPath string, query url.Values, headers [][2]string, payloadHash string) (creq, signedHeaders string) {
	var heads, names strings.Builder
	for i, h := range headers {
		if i > 0 {
			names.WriteByte(';')
		}
		names.WriteString(h[0])
		heads.WriteString(h[0])
		heads.WriteByte(':')
		heads.WriteString(v4TrimHeader(h[1]))
		heads.WriteByte('\n')
	}
	creq = strings.Join([]string{
		method, encodedPath, v4CanonicalQuery(query),
		heads.String(), names.String(), payloadHash,
	}, "\n")
	return creq, names.String()
}

// v4CanonicalQuery renders query parameters sorted by encoded name (then
// encoded value), joined as k=v pairs with '&'. Empty values render as
// bare "name=" (e.g. "?uploads=").
func v4CanonicalQuery(query url.Values) string {
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	first := true
	for _, name := range names {
		values := append([]string(nil), query[name]...)
		sort.Strings(values)
		for _, value := range values {
			if !first {
				out.WriteByte('&')
			}
			first = false
			out.WriteString(v4Escape(name, false))
			out.WriteByte('=')
			out.WriteString(v4Escape(value, false))
		}
	}
	return out.String()
}

// v4StringToSign renders "Algorithm\namzDate\nscope\nhex(sha256(creq))".
func v4StringToSign(amzDate, scope, canonicalRequest string) string {
	sum := sha256.Sum256([]byte(canonicalRequest))
	return v4Algorithm + "\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
}

// v4SigningKey derives kSigning from the secret, date stamp, region, and
// service per the spec's HMAC chain.
func v4SigningKey(secret []byte, day, region, service string) []byte {
	k := v4HMAC(append([]byte("AWS4"), secret...), day)
	k = v4HMAC(k, region)
	k = v4HMAC(k, service)
	return v4HMAC(k, v4Terminator)
}

func v4HMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func v4Hex(b []byte) string { return hex.EncodeToString(b) }

// v4Escape percent-encodes s using the SigV4 rules: RFC 3986 unreserved
// characters pass through, '/' passes through only in paths, everything
// else becomes %XY with uppercase hex.
func v4Escape(s string, isPath bool) string {
	var out strings.Builder
	out.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' && isPath {
			out.WriteByte(c)
			continue
		}
		if v4Unreserved(c) {
			out.WriteByte(c)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", c)
	}
	return out.String()
}

func v4Unreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '~':
		return true
	}
	return false
}

// v4TrimHeader collapses a header value per the spec: leading/trailing
// whitespace removed, interior runs folded to one space.
func v4TrimHeader(v string) string {
	return strings.Join(strings.Fields(v), " ")
}
