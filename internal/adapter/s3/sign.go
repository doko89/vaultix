package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
)

// SigV4 helpers for S3 query authentication (presigned URLs) and header
// authentication (direct Manager calls). The implementation covers exactly
// what this client needs: PUT/GET presigning with optional subresources and
// signed direct requests with a small fixed header set.

const (
	amzAlgorithm = "AWS4-HMAC-SHA256"
	s3Service    = "s3"
	aws4Request  = "aws4_request"
)

func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// deriveSigningKey builds kSigning = HMAC(HMAC(HMAC(HMAC("AWS4"+secret,
// date), region), service), "aws4_request").
func deriveSigningKey(secret, dateStamp, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, s3Service)
	return hmacSHA256(kService, aws4Request)
}

// encodeComponent percent-encodes s per SigV4 (RFC 3986 unreserved set plus
// uppercase hex). Slashes are preserved only for paths.
func encodeComponent(s string, isPath bool) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && isPath:
			b.WriteByte(c)
		default:
			const hexdigits = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hexdigits[c>>4])
			b.WriteByte(hexdigits[c&0x0f])
		}
	}
	return b.String()
}

// encodePath encodes an absolute request path, preserving slashes.
func encodePath(path string) string {
	return encodeComponent(path, true)
}

// canonicalQuery renders sorted, encoded k=v pairs joined by '&'.
func canonicalQuery(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for i, k := range keys {
		values := slices.Clone(params[k])
		slices.Sort(values)
		for j, v := range values {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(encodeComponent(k, false))
			b.WriteByte('=')
			b.WriteString(encodeComponent(v, false))
		}
	}
	return b.String()
}
