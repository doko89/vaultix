package agent

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxS3ErrorBodyBytes caps how much of a failed S3 response body is read
// for error diagnosis. S3 error documents are small; anything beyond the
// cap is ignored so a huge body cannot bloat errors or logs.
const maxS3ErrorBodyBytes = 8 << 10

// s3ErrorDoc is the S3 XML error envelope:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<Error><Code>SignatureDoesNotMatch</Code><Message>...</Message>...</Error>
type s3ErrorDoc struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// parseS3ErrorCode extracts the S3 <Code> (+ <Message>) from a response
// body. It reports ok=false for malformed, non-XML, or codeless bodies,
// in which case callers keep the plain status-only message.
func parseS3ErrorCode(body []byte) (code, message string, ok bool) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return "", "", false
	}
	var doc s3ErrorDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return "", "", false
	}
	if doc.XMLName.Local != "Error" {
		return "", "", false
	}
	code = strings.TrimSpace(doc.Code)
	if code == "" {
		return "", "", false
	}
	return code, strings.TrimSpace(doc.Message), true
}

// putStatusError builds the error for a non-2xx storage PUT. The URL is
// redacted (presigned signatures stripped) and, when the capped response
// body parses as an S3 error document, the S3 code is surfaced as
// `s3:<Code>` so 403s name their cause instead of just their status.
func putStatusError(method, rawURL string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxS3ErrorBodyBytes))
	target := redactURL(rawURL)
	if code, msg, ok := parseS3ErrorCode(body); ok {
		if msg == "" {
			return fmt.Errorf("%s %s: status %s (s3:%s)", method, target, resp.Status, code)
		}
		return fmt.Errorf("%s %s: status %s (s3:%s: %s)", method, target, resp.Status, code, msg)
	}
	return fmt.Errorf("%s %s: status %s", method, target, resp.Status)
}
