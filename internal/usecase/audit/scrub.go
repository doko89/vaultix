package audit

import (
	"regexp"
	"strings"
)

// ScrubSecrets redacts secret-looking key=value fragments from free-text
// audit detail so credentials never land in audit storage (PRD §10.2, §13.15).
// It keeps the key name (useful for debugging which field was present) and
// replaces the value with "[redacted]".
//
// Recognized keys (case-insensitive): password, passwd, secret, token,
// api_key/apikey, client_secret, access_key, secret_key, session_key,
// private_key, authorization. Both "key=value" and `"key": "value"` (JSON)
// spellings are scrubbed.
var scrubPatterns = []*regexp.Regexp{
	// key=value, key:value, key => value (stops at whitespace, comma, quote, semicolon)
	regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|client[_-]?secret|access[_-]?key|secret[_-]?key|session[_-]?key|private[_-]?key)\b\s*([:=]|=>)\s*['"]?[^'"',;\s]+['"]?`),
	// "key": "value" JSON spelling
	regexp.MustCompile(`(?i)"(password|passwd|secret|token|api[_-]?key|client[_-]?secret|access[_-]?key|secret[_-]?key|session[_-]?key|private[_-]?key)"\s*:\s*"[^"]*"`),
	// Authorization: <scheme> <credentials>
	regexp.MustCompile(`(?i)\b(authorization)\b\s*:\s*[A-Za-z0-9\-._~+/]+=*(\s+[A-Za-z0-9\-._~+/]+=*)?`),
}

const redacted = "[redacted]"

// ScrubSecrets returns s with secret values replaced by "[redacted]".
func ScrubSecrets(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, re := range scrubPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			// Preserve the key (and separator style) for debuggability.
			loc := re.FindStringSubmatchIndex(m)
			if len(loc) < 4 || loc[2] < 0 {
				return redacted
			}
			key := m[loc[2]:loc[3]]
			rest := strings.TrimSpace(m[loc[3]:])
			sep := "="
			switch {
			case strings.HasPrefix(rest, "=>"):
				sep = "=>"
			case strings.HasPrefix(rest, ":"):
				sep = ":"
			case strings.HasPrefix(rest, "="):
				sep = "="
			}
			if strings.HasPrefix(m, `"`) {
				return `"` + key + `": "` + redacted + `"`
			}
			return key + sep + redacted
		})
	}
	return out
}
