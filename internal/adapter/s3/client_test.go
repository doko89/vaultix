package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCredentials returns fixed fake HMAC keys. They are not secrets.
func testCredentials() (access, signing string) {
	return "TESTACCESSKEY", "TESTSIGNINGKEY-test-only-not-a-secret"
}

func testClient(t *testing.T, fake *fakeS3, mutate func(*Config)) *Client {
	t.Helper()
	access, signing := testCredentials()
	cfg := Config{
		Endpoint:             fake.server.URL,
		Region:               "us-east-1",
		Bucket:               "test-bucket",
		Prefix:               "tenant1",
		AccessKeyID:          access,
		SecretAccessKey:      signing,
		Timeout:              5 * time.Second,
		RetryInitialInterval: time.Millisecond,
		Now:                  time.Now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(cfg)
	require.NoError(t, err)
	return c
}

func TestScopeKey(t *testing.T) {
	key, err := ScopeKey("tenant1", "jobA", "chunk-0001.zst")
	require.NoError(t, err)
	assert.Equal(t, "tenant1/jobs/jobA/chunk-0001.zst", key)

	key, err = ScopeKey("", "jobA", "a/b.bin")
	require.NoError(t, err)
	assert.Equal(t, "jobs/jobA/a/b.bin", key)

	for _, tc := range []struct {
		name   string
		prefix string
		job    string
		object string
	}{
		{"empty job", "p", "", "a.bin"},
		{"job traversal", "p", "../x", "a.bin"},
		{"job slash", "p", "a/b", "a.bin"},
		{"empty object", "p", "jobA", ""},
		{"object traversal", "p", "jobA", "../escape.bin"},
		{"object absolute", "p", "jobA", "/etc/passwd"},
		{"object dotdot segment", "p", "jobA", "a/../../b"},
		{"bad prefix", "a/../p", "jobA", "a.bin"},
	} {
		_, err := ScopeKey(tc.prefix, tc.job, tc.object)
		assert.Error(t, err, tc.name)
	}
}

func TestValidateScopedKeyIsolation(t *testing.T) {
	keyA, err := ScopeKey("tenant1", "jobA", "chunk.bin")
	require.NoError(t, err)

	require.NoError(t, ValidateScopedKey("tenant1", "jobA", keyA))
	// A grant for job A cannot address job B paths: the same key fails
	// validation under any other job.
	require.Error(t, ValidateScopedKey("tenant1", "jobB", keyA))
	require.Error(t, ValidateScopedKey("", "jobA", keyA))
	require.Error(t, ValidateScopedKey("tenant1", "jobA", "tenant1/jobs/jobB/other.bin"))
	require.Error(t, ValidateScopedKey("tenant1", "jobA", "tenant1/jobs/jobA"))
	require.NoError(t, ValidateScopedKey("tenant1", "jobA", "tenant1/jobs/jobA/nested/deep.bin"))
	require.Error(t, ValidateScopedKey("tenant1", "jobA", "tenant1/jobs/jobA/../jobB/x.bin"))
	require.Error(t, ValidateScopedKey("tenant1", "jobA", "other/prefix.bin"))
}

func TestValidateJobScopedShape(t *testing.T) {
	require.NoError(t, ValidateJobScopedShape("tenant1", "tenant1/jobs/any/x.bin"))
	require.NoError(t, ValidateJobScopedShape("", "jobs/any/x.bin"))
	require.Error(t, ValidateJobScopedShape("tenant1", "tenant1/jobs/onlyjob"))
	require.Error(t, ValidateJobScopedShape("tenant1", "tenant1/raw/x.bin"))
	require.Error(t, ValidateJobScopedShape("tenant1", "tenant1/jobs/../x.bin"))
}

func TestClientConfigValidation(t *testing.T) {
	access, signing := testCredentials()
	base := Config{Bucket: "b", AccessKeyID: access, SecretAccessKey: signing}
	c, err := New(base)
	require.NoError(t, err)
	assert.Equal(t, DefaultRegion, c.region)

	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing bucket", func(c *Config) { c.Bucket = "" }},
		{"missing access", func(c *Config) { c.AccessKeyID = "" }},
		{"missing signing", func(c *Config) { c.SecretAccessKey = "" }},
		{"bad scheme", func(c *Config) { c.Endpoint = "ftp://x" }},
		{"no host", func(c *Config) { c.Endpoint = "https://" }},
		{"userinfo", func(c *Config) { c.Endpoint = "https://user:pw@host" }},
	} {
		cfg := base
		tc.mutate(&cfg)
		_, err := New(cfg)
		assert.Error(t, err, tc.name)
	}
}

func TestAddressingStyle(t *testing.T) {
	newFor := func(endpoint, bucket string, pathStyle bool) *Client {
		access, signing := testCredentials()
		c, err := New(Config{
			Endpoint: endpoint, Region: "eu-west-1", Bucket: bucket,
			AccessKeyID: access, SecretAccessKey: signing, UsePathStyle: pathStyle,
		})
		require.NoError(t, err)
		return c
	}

	// Custom DNS endpoint defaults to virtual-hosted style.
	c := newFor("https://s3.example.com", "mybucket", false)
	host, path := c.target("jobs/a/x.bin")
	assert.Equal(t, "mybucket.s3.example.com", host)
	assert.Equal(t, "/jobs/a/x.bin", path)

	// Explicit path style, IP hosts, and non-DNS buckets stay path-style.
	c = newFor("https://s3.example.com", "mybucket", true)
	host, path = c.target("k")
	assert.Equal(t, "s3.example.com", host)
	assert.Equal(t, "/mybucket/k", path)

	c = newFor("http://127.0.0.1:9000", "mybucket", false)
	host, path = c.target("k")
	assert.Equal(t, "127.0.0.1:9000", host)
	assert.Equal(t, "/mybucket/k", path)

	c = newFor("https://s3.example.com", "UPPER", false)
	host, _ = c.target("k")
	assert.Equal(t, "s3.example.com", host)

	// AWS default endpoint.
	access, signing := testCredentials()
	c, err := New(Config{Region: "eu-west-1", Bucket: "mybucket", AccessKeyID: access, SecretAccessKey: signing})
	require.NoError(t, err)
	host, path = c.target("k")
	assert.Equal(t, "mybucket.s3.eu-west-1.amazonaws.com", host)
	assert.Equal(t, "/k", path)
}

func TestPresignGrantScoping(t *testing.T) {
	fake := newFakeS3(t)
	fixed := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	c := testClient(t, fake, func(cfg *Config) { cfg.Now = func() time.Time { return fixed } })
	ctx := context.Background()

	keyA, err := ScopeKey("tenant1", "jobA", "chunk.bin")
	require.NoError(t, err)

	put, err := c.PresignPut(ctx, "jobA", keyA, 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, put.Method)
	assert.Equal(t, "test-bucket", put.Bucket)
	assert.Equal(t, keyA, put.ObjectKey)
	assert.True(t, put.ExpiresAt.Equal(fixed.Add(5*time.Minute)))

	u, err := url.Parse(put.URL)
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "AWS4-HMAC-SHA256", q.Get("X-Amz-Algorithm"))
	assert.Equal(t, "300", q.Get("X-Amz-Expires"))
	assert.Len(t, q.Get("X-Amz-Signature"), 64)
	assert.Contains(t, u.Path, "test-bucket/tenant1/jobs/jobA/chunk.bin")

	get, err := c.PresignGet(ctx, "jobA", keyA, 0)
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, get.Method)

	// Cross-job grants are refused: job A's grant cannot address job B paths.
	keyB, err := ScopeKey("tenant1", "jobB", "chunk.bin")
	require.NoError(t, err)
	_, err = c.PresignPut(ctx, "jobA", keyB, time.Minute)
	require.Error(t, err)
	_, err = c.PresignGet(ctx, "jobB", keyA, time.Minute)
	require.Error(t, err)
	_, err = c.PresignPut(ctx, "jobA", "tenant1/raw/x.bin", time.Minute)
	require.Error(t, err)
	_, err = c.PresignPut(ctx, "jobA", "tenant1/jobs/jobA/../../jobB/x.bin", time.Minute)
	require.Error(t, err)
}

func TestPresignTTLBounds(t *testing.T) {
	fake := newFakeS3(t)
	fixed := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	c := testClient(t, fake, func(cfg *Config) { cfg.Now = func() time.Time { return fixed } })
	ctx := context.Background()
	key, err := ScopeKey("tenant1", "jobA", "a.bin")
	require.NoError(t, err)

	// Zero selects the default.
	g, err := c.PresignPut(ctx, "jobA", key, 0)
	require.NoError(t, err)
	assert.True(t, g.ExpiresAt.Equal(fixed.Add(DefaultGrantTTL)))
	u, err := url.Parse(g.URL)
	require.NoError(t, err)
	assert.Equal(t, "900", u.Query().Get("X-Amz-Expires"))

	// Over-maximum TTL is rejected so grants stay short-lived.
	_, err = c.PresignPut(ctx, "jobA", key, 2*time.Hour)
	require.Error(t, err)
	_, err = c.PresignGet(ctx, "jobA", key, 2*time.Hour)
	require.Error(t, err)
	_, err = c.PresignPut(ctx, "jobA", key, 500*time.Millisecond)
	require.Error(t, err)

	// Configured max is honored.
	c2 := testClient(t, fake, func(cfg *Config) { cfg.MaxTTL = 5 * time.Minute })
	_, err = c2.PresignPut(ctx, "jobA", key, 10*time.Minute)
	require.Error(t, err)
	_, err = c2.PresignPut(ctx, "jobA", key, 5*time.Minute)
	require.NoError(t, err)
}

func TestHeadDeletePing(t *testing.T) {
	fake := newFakeS3(t)
	c := testClient(t, fake, nil)
	ctx := context.Background()
	key, err := ScopeKey("tenant1", "jobA", "a.bin")
	require.NoError(t, err)
	body := []byte("backup-bytes")
	fake.putObject("test-bucket", key, body)

	require.NoError(t, c.Ping(ctx))

	meta, err := c.Head(ctx, key)
	require.NoError(t, err)
	assert.True(t, meta.Exists)
	assert.Equal(t, uint64(len(body)), meta.Size)
	sum := sha256.Sum256(body)
	assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), meta.Checksum)

	missing, err := c.Head(ctx, "tenant1/jobs/jobA/absent.bin")
	require.NoError(t, err)
	assert.False(t, missing.Exists)

	require.NoError(t, c.Delete(ctx, key))
	gone, err := c.Head(ctx, key)
	require.NoError(t, err)
	assert.False(t, gone.Exists)
	// Deleting again is success (idempotent prune).
	require.NoError(t, c.Delete(ctx, key))

	assert.Empty(t, fake.authGapsSnapshot())
}

func TestVerifyObjects(t *testing.T) {
	fake := newFakeS3(t)
	c := testClient(t, fake, nil)
	ctx := context.Background()
	key, err := ScopeKey("tenant1", "jobA", "a.bin")
	require.NoError(t, err)
	body := []byte("manifest-covered-bytes")
	fake.putObject("test-bucket", key, body)
	sum := sha256.Sum256(body)
	want := "sha256:" + hex.EncodeToString(sum[:])

	ok, results, err := c.VerifyObjects(ctx, []ExpectedObject{
		{Key: key, Checksum: want, Size: uint64(len(body)), CheckSize: true},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK)

	// Checksum mismatch fails the gate without an error.
	ok, results, err = c.VerifyObjects(ctx, []ExpectedObject{
		{Key: key, Checksum: "sha256:deadbeef"},
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Contains(t, results[0].Reason, "checksum mismatch")

	// Missing object fails the gate.
	ok, _, err = c.VerifyObjects(ctx, []ExpectedObject{
		{Key: "tenant1/jobs/jobA/absent.bin", Checksum: want},
	})
	require.NoError(t, err)
	assert.False(t, ok)

	// Size mismatch fails the gate.
	ok, results, err = c.VerifyObjects(ctx, []ExpectedObject{
		{Key: key, Size: 1, CheckSize: true},
	})
	require.NoError(t, err)
	require.False(t, ok)
	assert.Contains(t, results[0].Reason, "size mismatch")

	// Nothing to verify is a fail-closed error, never success.
	_, _, err = c.VerifyObjects(ctx, nil)
	require.Error(t, err)
}

func TestMultipartOrchestration(t *testing.T) {
	fake := newFakeS3(t)
	c := testClient(t, fake, nil)
	ctx := context.Background()
	key, err := ScopeKey("tenant1", "jobA", "large.bin")
	require.NoError(t, err)

	uploadID, err := c.InitiateMultipartUpload(ctx, "jobA", key)
	require.NoError(t, err)
	require.NotEmpty(t, uploadID)

	// Cross-job initiation is refused.
	_, err = c.InitiateMultipartUpload(ctx, "jobB", key)
	require.Error(t, err)

	for _, part := range []int{1, 2} {
		g, err := c.PresignUploadPart(ctx, "jobA", key, uploadID, part, 5*time.Minute)
		require.NoError(t, err)
		assert.Equal(t, http.MethodPut, g.Method)
		u, err := url.Parse(g.URL)
		require.NoError(t, err)
		assert.Equal(t, uploadID, u.Query().Get("uploadId"))
		assert.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
	}
	// Part grants honor job scope and part ranges.
	_, err = c.PresignUploadPart(ctx, "jobB", key, uploadID, 1, time.Minute)
	require.Error(t, err)
	_, err = c.PresignUploadPart(ctx, "jobA", key, uploadID, 0, time.Minute)
	require.Error(t, err)
	_, err = c.PresignUploadPart(ctx, "jobA", key, uploadID, 10001, time.Minute)
	require.Error(t, err)
	_, err = c.PresignUploadPart(ctx, "jobA", key, "", 1, time.Minute)
	require.Error(t, err)

	require.NoError(t, c.CompleteMultipartUpload(ctx, key, uploadID, []CompletedPart{
		{PartNumber: 2, ETag: "etag-2"},
		{PartNumber: 1, ETag: "etag-1"},
	}))
	u, ok := fake.getUpload(uploadID)
	require.True(t, ok)
	assert.True(t, u.completed)
	assert.False(t, u.aborted)
	assert.Equal(t, map[int]string{1: "etag-1", 2: "etag-2"}, u.parts)

	// Completion validates its inputs.
	require.Error(t, c.CompleteMultipartUpload(ctx, key, uploadID, nil))
	require.Error(t, c.CompleteMultipartUpload(ctx, key, "", []CompletedPart{{PartNumber: 1, ETag: "e"}}))
	require.Error(t, c.CompleteMultipartUpload(ctx, "raw/x.bin", uploadID, []CompletedPart{{PartNumber: 1, ETag: "e"}}))

	// Abort path.
	uploadID2, err := c.InitiateMultipartUpload(ctx, "jobA", key)
	require.NoError(t, err)
	require.NoError(t, c.AbortMultipartUpload(ctx, key, uploadID2))
	u2, ok := fake.getUpload(uploadID2)
	require.True(t, ok)
	assert.True(t, u2.aborted)
	require.Error(t, c.AbortMultipartUpload(ctx, key, ""))

	assert.Empty(t, fake.authGapsSnapshot())
}

func TestRetryOnTransient(t *testing.T) {
	fake := newFakeS3(t)
	c := testClient(t, fake, nil)
	ctx := context.Background()
	key, err := ScopeKey("tenant1", "jobA", "a.bin")
	require.NoError(t, err)
	fake.putObject("test-bucket", key, []byte("x"))

	// Two 500s then success: retried.
	fake.fail["HEAD /test-bucket/"+key] = 2
	before := fake.hitCount()
	meta, err := c.Head(ctx, key)
	require.NoError(t, err)
	assert.True(t, meta.Exists)
	assert.Equal(t, 3, fake.hitCount()-before)

	// 404 is terminal: no retry.
	before = fake.hitCount()
	_, err = c.Head(ctx, "tenant1/jobs/jobA/absent.bin")
	require.NoError(t, err)
	assert.Equal(t, 1, fake.hitCount()-before)
}

func TestRetryAfterHonored(t *testing.T) {
	fake := newFakeS3(t)
	fake.retryAfter = "0"
	c := testClient(t, fake, nil)
	ctx := context.Background()
	require.NoError(t, c.Ping(ctx)) // baseline, no failures configured
	fake.fail["HEAD /test-bucket"] = 1
	before := fake.hitCount()
	require.NoError(t, c.Ping(ctx))
	assert.Equal(t, 2, fake.hitCount()-before)
}

func TestContextTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)
	access, signing := testCredentials()
	c, err := New(Config{
		Endpoint: slow.URL, Region: "us-east-1", Bucket: "b",
		AccessKeyID: access, SecretAccessKey: signing,
		Timeout: 50 * time.Millisecond, RetryInitialInterval: time.Millisecond, MaxAttempts: 1,
	})
	require.NoError(t, err)
	err = c.Ping(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), signing)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.PresignPut(cancelled, "jobA", "jobs/jobA/a.bin", time.Minute)
	require.Error(t, err)
}

func TestErrorRedaction(t *testing.T) {
	// RedactURL strips signature/credential material but keeps debugging aids.
	raw := "https://s3.example.com/b/k?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIA%2Fscope&X-Amz-Signature=abcdef0123456789&uploadId=u-1&partNumber=2"
	redacted := RedactURL(raw)
	assert.NotContains(t, redacted, "abcdef0123456789")
	assert.NotContains(t, redacted, "AKIA")
	assert.Contains(t, redacted, "REDACTED")
	assert.Contains(t, redacted, "uploadId=u-1")
	assert.Contains(t, redacted, "partNumber=2")

	plain := "https://s3.example.com/b/k?uploadId=u-1"
	assert.Equal(t, plain, RedactURL(plain))
	assert.Equal(t, "<unparseable-url-redacted>", RedactURL("http://exa mple.com/%zz"))

	// Client errors never carry the signing key or a signature value.
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(denied.Close)
	access, signing := testCredentials()
	c, err := New(Config{
		Endpoint: denied.URL, Region: "us-east-1", Bucket: "b",
		AccessKeyID: access, SecretAccessKey: signing,
		RetryInitialInterval: time.Millisecond,
	})
	require.NoError(t, err)
	ctx := context.Background()
	for _, err := range []error{
		func() error { _, e := c.Head(ctx, "jobs/a/x.bin"); return e }(),
		c.Delete(ctx, "jobs/a/x.bin"),
		c.Ping(ctx),
	} {
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
		assert.NotContains(t, err.Error(), signing)
		assert.NotContains(t, err.Error(), "X-Amz-Signature=")
	}

	// Presign failures carry no URL-with-signature either.
	_, err = c.PresignPut(ctx, "jobA", "not-scoped.bin", time.Minute)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "X-Amz-Signature=")
}
