package s3

import (
	"net/url"
	"strings"
)

// Redaction guards: signature and credential material must never appear in
// errors or logs. Presigned URLs are handed to agents out-of-band; only
// redacted forms are safe to retain.

const redactedValue = "REDACTED"

// sensitiveParams holds lower-cased query parameter names whose values are
// authentication material.
var sensitiveParams = map[string]bool{
	"x-amz-signature":      true,
	"x-amz-credential":     true,
	"x-amz-security-token": true,
	"x-amz-session-token":  true,
	"signature":            true,
	"awsaccesskeyid":       true,
	"accesskeyid":          true,
	"secretaccesskey":      true,
	"sessiontoken":         true,
}

// RedactURL replaces signature/credential query values with REDACTED so the
// result is safe for errors and logs. Non-sensitive parameters (bucket key
// path, uploadId, partNumber) are preserved for debuggability.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable-url-redacted>"
	}
	q := u.Query()
	changed := false
	for k, values := range q {
		if !sensitiveParams[strings.ToLower(k)] {
			continue
		}
		for i := range values {
			values[i] = redactedValue
		}
		q[k] = values
		changed = true
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}
