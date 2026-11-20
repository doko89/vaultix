package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseS3ErrorCode(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
		wantOK      bool
	}{
		{
			name:        "code and message",
			body:        `<?xml version="1.0" encoding="UTF-8"?><Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match.</Message><RequestId>ABC</RequestId></Error>`,
			wantCode:    "SignatureDoesNotMatch",
			wantMessage: "The request signature we calculated does not match.",
			wantOK:      true,
		},
		{
			name:     "code only",
			body:     `<Error><Code>AccessDenied</Code></Error>`,
			wantCode: "AccessDenied",
			wantOK:   true,
		},
		{
			name:        "namespaced envelope",
			body:        `<Error xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Code>NoSuchBucket</Code><Message>nope</Message></Error>`,
			wantCode:    "NoSuchBucket",
			wantMessage: "nope",
			wantOK:      true,
		},
		{
			name:     "whitespace padded",
			body:     "  \n <Error>\n<Code> SlowDown </Code>\n<Message> calm down </Message></Error>  ",
			wantCode: "SlowDown", wantMessage: "calm down", wantOK: true,
		},
		{name: "garbage", body: `<html><body>proxy exploded</body></html>`},
		{name: "empty", body: ``},
		{name: "blank", body: "  \n\t "},
		{name: "truncated xml", body: `<Error><Code>AccessDeni`},
		{name: "wrong root", body: `<Foo><Code>AccessDenied</Code></Foo>`},
		{name: "missing code", body: `<Error><Message>no code here</Message></Error>`},
		{name: "blank code", body: `<Error><Code>   </Code><Message>m</Message></Error>`},
		{name: "plain text", body: `Internal Server Error`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg, ok := parseS3ErrorCode([]byte(tc.body))
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantCode, code)
			assert.Equal(t, tc.wantMessage, msg)
		})
	}
}

// TestPutOnceSurfacesS3Code drives the presigned single-PUT failure path:
// the S3 code (+ message) must appear while the signed query stays
// redacted.
func TestPutOnceSurfacesS3Code(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code><Message>sig mismatch</Message></Error>`))
	}))
	t.Cleanup(srv.Close)

	up := &Uploader{Manager: &StubManagerClient{}, NewBackOff: fastBackoff}
	a := stageFile(t, "t0.tar.zst", []byte("payload"))
	err := up.putOnce(t.Context(), a.Path,
		UploadGrant{Key: "k", URL: srv.URL + "/put/t0?X-Amz-Algorithm=AWS4&X-Amz-Signature=SECRET"}, a.SHA256)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403 Forbidden")
	assert.Contains(t, err.Error(), "s3:SignatureDoesNotMatch")
	assert.Contains(t, err.Error(), "sig mismatch")
	assert.Contains(t, err.Error(), "/put/t0?…")
	assert.NotContains(t, err.Error(), "SECRET", "signed query must stay redacted")
}

// TestPutPartOnceSurfacesS3Code is the multipart-part counterpart.
func TestPutPartOnceSurfacesS3Code(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	t.Cleanup(srv.Close)

	up := &Uploader{Manager: &StubManagerClient{}, NewBackOff: fastBackoff}
	_, err := up.putPartOnce(t.Context(), srv.URL+"/mp/p1?partNumber=1&uploadId=u&X-Amz-Signature=SECRET", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3:AccessDenied")
	assert.NotContains(t, err.Error(), "SECRET")
	assert.NotContains(t, err.Error(), "uploadId=u")
}

// TestPutFailureNonXMLKeepsStatusOnly pins the legacy message for bodies
// that do not parse as S3 errors.
func TestPutFailureNonXMLKeepsStatusOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`something broke badly`))
	}))
	t.Cleanup(srv.Close)

	up := &Uploader{Manager: &StubManagerClient{}, NewBackOff: fastBackoff}
	a := stageFile(t, "x", []byte("data"))
	err := up.putOnce(t.Context(), a.Path, UploadGrant{Key: "k", URL: srv.URL + "/put/x?sig=abc"}, "")
	require.Error(t, err)
	assert.Equal(t, fmt.Sprintf("PUT %s/put/x?…: status 500 Internal Server Error", srv.URL), err.Error())
}

// TestPutFailureOversizedBodyFallsBack proves the 8 KiB cap: a Code buried
// past the cap must not surface (and a huge body must not bloat the error).
func TestPutFailureOversizedBodyFallsBack(t *testing.T) {
	padding := strings.Repeat("p", maxS3ErrorBodyBytes+1024)
	body := `<Error><Message>` + padding + `</Message><Code>BuriedCode</Code></Error>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	up := &Uploader{Manager: &StubManagerClient{}, NewBackOff: fastBackoff}
	a := stageFile(t, "x", []byte("data"))
	err := up.putOnce(t.Context(), a.Path, UploadGrant{Key: "k", URL: srv.URL + "/put/x"}, "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "BuriedCode")
	assert.NotContains(t, err.Error(), padding)
	assert.Contains(t, err.Error(), "403 Forbidden")
}
