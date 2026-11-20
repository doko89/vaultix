package agent

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Published AWS Signature Version 4 test-suite vectors (AKIDEXAMPLE /
// wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY, 20150830, us-east-1, service).
// Source: the AWS suite as vendored in botocore's tests
// (tests/unit/auth/aws4_testsuite). Each case pins the canonical request,
// the string to sign, and the final Authorization header.
var v4SuiteCases = []struct {
	name    string
	method  string
	url     string
	headers map[string]string
	signed  []string

	wantCreq string
	wantSts  string
	wantAuth string
}{
	{
		name:   "get-vanilla",
		method: http.MethodGet,
		url:    "https://example.amazonaws.com/",
		headers: map[string]string{
			"x-amz-date": "20150830T123600Z",
		},
		signed: []string{"host", "x-amz-date"},
		wantCreq: "GET\n" +
			"/\n" +
			"\n" +
			"host:example.amazonaws.com\n" +
			"x-amz-date:20150830T123600Z\n" +
			"\n" +
			"host;x-amz-date\n" +
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		wantSts: "AWS4-HMAC-SHA256\n" +
			"20150830T123600Z\n" +
			"20150830/us-east-1/service/aws4_request\n" +
			"bb579772317eb040ac9ed261061d46c1f17a8133879d6129b6e1c25292927e63",
		wantAuth: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
			"SignedHeaders=host;x-amz-date, " +
			"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31",
	},
	{
		name:   "post-vanilla-query",
		method: http.MethodPost,
		url:    "https://example.amazonaws.com/?Param1=value1",
		headers: map[string]string{
			"x-amz-date": "20150830T123600Z",
		},
		signed: []string{"host", "x-amz-date"},
		wantCreq: "POST\n" +
			"/\n" +
			"Param1=value1\n" +
			"host:example.amazonaws.com\n" +
			"x-amz-date:20150830T123600Z\n" +
			"\n" +
			"host;x-amz-date\n" +
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		wantSts: "AWS4-HMAC-SHA256\n" +
			"20150830T123600Z\n" +
			"20150830/us-east-1/service/aws4_request\n" +
			"9d659678c1756bb3113e2ce898845a0a79dbbc57b740555917687f1b3340fbbd",
		wantAuth: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
			"SignedHeaders=host;x-amz-date, " +
			"Signature=28038455d6de14eafc1f9222cf5aa6f1a96197d7deb8263271d420d138af7f11",
	},
	{
		name:   "get-header-value-trim",
		method: http.MethodGet,
		url:    "https://example.amazonaws.com/",
		headers: map[string]string{
			"My-Header1": "value1",
			"My-Header2": `"a   b   c"`,
			"x-amz-date": "20150830T123600Z",
		},
		signed: []string{"host", "my-header1", "my-header2", "x-amz-date"},
		wantCreq: "GET\n" +
			"/\n" +
			"\n" +
			"host:example.amazonaws.com\n" +
			"my-header1:value1\n" +
			`my-header2:"a b c"` + "\n" +
			"x-amz-date:20150830T123600Z\n" +
			"\n" +
			"host;my-header1;my-header2;x-amz-date\n" +
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		wantSts: "AWS4-HMAC-SHA256\n" +
			"20150830T123600Z\n" +
			"20150830/us-east-1/service/aws4_request\n" +
			"a726db9b0df21c14f559d0a978e563112acb1b9e05476f0a6a1c7d68f28605c7",
		wantAuth: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
			"SignedHeaders=host;my-header1;my-header2;x-amz-date, " +
			"Signature=acc3ed3afb60bb290fc8d2dd0098b9911fcaa05412b367055dee359757a9c736",
	},
}

func TestV4SuiteVectors(t *testing.T) {
	for _, tc := range v4SuiteCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, tc.url, nil)
			require.NoError(t, err)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			gathered := make([][2]string, 0, len(tc.signed))
			for _, name := range tc.signed {
				value := req.Header.Get(name)
				if name == "host" {
					value = req.URL.Host
				}
				gathered = append(gathered, [2]string{name, value})
			}
			creq, signedHeaders := v4CanonicalRequest(req.Method, v4RequestPath(req), req.URL.Query(), gathered, v4EmptySHA256)
			assert.Equal(t, tc.wantCreq, creq, "canonical request")
			assert.Equal(t, strings.Join(tc.signed, ";"), signedHeaders)

			sts := v4StringToSign("20150830T123600Z", "20150830/us-east-1/service/aws4_request", creq)
			assert.Equal(t, tc.wantSts, sts, "string to sign")

			signer := V4Signer{AccessKey: "AKIDEXAMPLE",
				SecretKey: []byte("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"),
				Region:    "us-east-1", Service: "service"}
			signer.signWith(req, v4EmptySHA256, tc.signed, "20150830T123600Z")
			assert.Equal(t, tc.wantAuth, req.Header.Get("Authorization"), "authorization header")
		})
	}
}

// TestV4S3SignShape checks the production S3 header set on a multipart part
// URL: credential scope, signed headers, date/content headers, and a
// well-formed hex signature.
func TestV4S3SignShape(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut,
		"https://s3.us-east-1.amazonaws.com/bucket/jobs/j1/big?partNumber=2&uploadId=abc123", nil)
	require.NoError(t, err)
	payload := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" // sha256("test")

	signer := NewV4Signer("AKID", []byte("secret"), "us-east-1")
	when, err := time.Parse(v4DateFormat, "20240102T030405Z")
	require.NoError(t, err)
	signer.Sign(req, payload, when)

	assert.Equal(t, "20240102T030405Z", req.Header.Get("x-amz-date"))
	assert.Equal(t, payload, req.Header.Get("x-amz-content-sha256"))
	auth := req.Header.Get("Authorization")
	assert.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/20240102/us-east-1/s3/aws4_request, "),
		"scope must name region + s3 service: %s", auth)
	assert.Contains(t, auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date")
	sig := auth[strings.LastIndex(auth, "Signature=")+len("Signature="):]
	require.Len(t, sig, 64)
	assert.Regexp(t, `^[0-9a-f]{64}$`, sig)
}

// TestV4SignDeterminism guards the signature computation itself: identical
// inputs sign identically, and any input change (payload, time, key, URL)
// changes the signature.
func TestV4SignDeterminism(t *testing.T) {
	sign := func(t *testing.T, rawURL, payload, secret string, when time.Time) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, rawURL, nil)
		require.NoError(t, err)
		NewV4Signer("AKID", []byte(secret), "us-east-1").Sign(req, payload, when)
		return req.Header.Get("Authorization")
	}
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	base := sign(t, "https://h/b/k", strings.Repeat("a", 64), "secret", when)
	assert.Equal(t, base, sign(t, "https://h/b/k", strings.Repeat("a", 64), "secret", when))
	assert.NotEqual(t, base, sign(t, "https://h/b/k", strings.Repeat("b", 64), "secret", when), "payload change")
	assert.NotEqual(t, base, sign(t, "https://h/b/k", strings.Repeat("a", 64), "secret", when.Add(time.Second)), "time change")
	assert.NotEqual(t, base, sign(t, "https://h/b/k", strings.Repeat("a", 64), "other", when), "key change")
	assert.NotEqual(t, base, sign(t, "https://h/b/k2", strings.Repeat("a", 64), "secret", when), "path change")
	assert.NotEqual(t, base, sign(t, "https://h/b/k?partNumber=1&uploadId=u", strings.Repeat("a", 64), "secret", when), "query change")
}

func TestV4CanonicalQueryOrdering(t *testing.T) {
	query := map[string][]string{
		"uploadId":   {"u"},
		"partNumber": {"2"},
		"uploads":    {""},
		"multi":      {"b", "a"},
	}
	assert.Equal(t, "multi=a&multi=b&partNumber=2&uploadId=u&uploads=", v4CanonicalQuery(query))
	assert.Equal(t, "", v4CanonicalQuery(nil))
	assert.Equal(t, "q=a%20b%2Bc", v4CanonicalQuery(map[string][]string{"q": {"a b+c"}}),
		"space encodes as %%20, never '+'; uppercase hex")
}

func TestV4Escape(t *testing.T) {
	assert.Equal(t, "abcXYZ019-_.~", v4Escape("abcXYZ019-_.~", false))
	assert.Equal(t, "a/b", v4Escape("a/b", true), "slash preserved in paths")
	assert.Equal(t, "a%2Fb", v4Escape("a/b", false), "slash encoded in queries")
	assert.Equal(t, "%20%2B%3D", v4Escape(" +=", false), "uppercase hex")
}

func TestV4WipeZeroesSecret(t *testing.T) {
	secret := []byte("super-secret-key")
	signer := NewV4Signer("AKID", secret, "us-east-1")
	signer.Wipe()
	assert.Equal(t, make([]byte, len("super-secret-key")), secret)
}
