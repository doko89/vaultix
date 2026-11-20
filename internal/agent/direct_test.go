package agent

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeS3 emulates the S3 XML APIs the direct path uses: single PUT,
// CreateMultipartUpload, UploadPart, CompleteMultipartUpload, and
// AbortMultipartUpload. Every request's SigV4 header signature is verified
// by independent recomputation (see verifyS3Signature).
type fakeS3 struct {
	t      *testing.T
	secret string
	region string
	bucket string

	mu             sync.Mutex
	objects        map[string][]byte
	objectMeta     map[string]string // key -> x-amz-meta-checksum seen
	uploads        map[string]map[int][]byte
	initMeta       map[string]string // uploadID -> checksum header at initiate
	aborted        map[string]bool
	completeBodies map[string][]byte // uploadID -> raw CompleteMultipartUpload XML
	verified       int
	failedParts    map[int]int // partNumber -> failures remaining
	failComplete   bool
	failSinglePUT  bool
}

func newFakeS3(t *testing.T, secret, region, bucket string) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{
		t: t, secret: secret, region: region, bucket: bucket,
		objects: map[string][]byte{}, objectMeta: map[string]string{},
		uploads: map[string]map[int][]byte{}, initMeta: map[string]string{},
		aborted: map[string]bool{}, completeBodies: map[string][]byte{},
		failedParts: map[int]int{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	assert.NoError(f.t, err)
	verifyS3Signature(f.t, r, body, f.secret, f.region)
	f.mu.Lock()
	f.verified++
	f.mu.Unlock()

	key, ok := strings.CutPrefix(r.URL.EscapedPath(), "/"+f.bucket+"/")
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>BadRequest</Code></Error>`))
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPut && len(q) == 0:
		f.servePutObject(w, r, key, body)
	case r.Method == http.MethodPost && hasKey(q, "uploads"):
		f.serveInitiate(w, r, key)
	case r.Method == http.MethodPut && q.Get("partNumber") != "":
		f.serveUploadPart(w, key, q, body)
	case r.Method == http.MethodPost && q.Get("uploadId") != "":
		f.serveComplete(w, key, q.Get("uploadId"), body)
	case r.Method == http.MethodDelete && q.Get("uploadId") != "":
		f.mu.Lock()
		f.aborted[q.Get("uploadId")] = true
		delete(f.uploads, q.Get("uploadId"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>BadRequest</Code></Error>`))
	}
}

func hasKey(q url.Values, k string) bool {
	_, ok := q[k]
	return ok
}

func (f *fakeS3) servePutObject(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSinglePUT {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied by policy</Message></Error>`))
		return
	}
	f.objects[key] = body
	f.objectMeta[key] = r.Header.Get(checksumMetaHeader)
	sum := md5.Sum(body)
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sum))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) serveInitiate(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uploadID := fmt.Sprintf("upload-%d-%s", len(f.uploads)+1, strings.ReplaceAll(key, "/", "-"))
	f.uploads[uploadID] = map[int][]byte{}
	f.initMeta[uploadID] = r.Header.Get(checksumMetaHeader)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`,
		f.bucket, key, uploadID)
}

func (f *fakeS3) serveUploadPart(w http.ResponseWriter, key string, q url.Values, body []byte) {
	var num int
	_, _ = fmt.Sscanf(q.Get("partNumber"), "%d", &num)
	uploadID := q.Get("uploadId")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failedParts[num] > 0 {
		f.failedParts[num]--
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
		return
	}
	parts, ok := f.uploads[uploadID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchUpload</Code></Error>`))
		return
	}
	parts[num] = body
	sum := md5.Sum(body)
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sum))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) serveComplete(w http.ResponseWriter, key, uploadID string, body []byte) {
	var doc struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []struct {
			Number int    `xml:"PartNumber"`
			ETag   string `xml:"ETag"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>MalformedXML</Code></Error>`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failComplete {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<Error><Code>InternalError</Code></Error>`))
		return
	}
	parts, ok := f.uploads[uploadID]
	if !ok || len(doc.Parts) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<Error><Code>InvalidRequest</Code></Error>`))
		return
	}
	f.completeBodies[uploadID] = body
	var assembled []byte
	for _, p := range doc.Parts {
		part, ok := parts[p.Number]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>InvalidPart</Code></Error>`))
			return
		}
		if strings.Trim(p.ETag, `"`) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>InvalidPart</Code></Error>`))
			return
		}
		assembled = append(assembled, part...)
	}
	f.objects[key] = assembled
	if meta := f.initMeta[uploadID]; meta != "" {
		f.objectMeta[key] = meta
	}
	delete(f.uploads, uploadID)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CompleteMultipartUploadResult><Location>http://fake/%s</Location><Bucket>%s</Bucket><Key>%s</Key><ETag>"assembled"</ETag></CompleteMultipartUploadResult>`,
		key, f.bucket, key)
}

// verifyS3Signature recomputes the request's SigV4 header signature from
// scratch and fails the test on any mismatch.
//
// NOTE ON CIRCULARITY: this verifier is deliberately self-contained test
// code — plain spec steps using only crypto/sha256, crypto/hmac, and
// string sorting — sharing no helpers with sigv4.go. It is cross-checked
// two independent ways: (1) sigv4_test.go pins the implementation against
// the published AWS suite vectors; (2) these tests additionally assert live
// behavior (bytes landed under exact keys, completion XML parsed by
// encoding/xml, ETags round-tripped, aborts observed). A bug common to both
// implementations would still have to satisfy the published vectors.
func verifyS3Signature(t *testing.T, r *http.Request, body []byte, secret, region string) {
	t.Helper()
	const algo = "AWS4-HMAC-SHA256"
	auth := r.Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, algo+" "), "missing SigV4 Authorization, got %q", auth)

	// Credential=<akid>/<scope>, SignedHeaders=<names>, Signature=<hex>
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(auth, algo+" "), ",") {
		part = strings.TrimSpace(part)
		k, v, _ := strings.Cut(part, "=")
		fields[k] = v
	}
	require.Contains(t, fields, "Credential")
	require.Contains(t, fields, "Signature")
	// The S3 rule, enforced from the request itself: every x-amz-* header
	// on the wire must appear in SignedHeaders (an unsigned one is
	// rejected with 403 AccessDenied). The verifier derives the expected
	// set independently — it does not trust the transmitted list.
	want := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	for k := range r.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") && lk != "x-amz-content-sha256" && lk != "x-amz-date" {
			want = append(want, lk)
		}
	}
	sort.Strings(want)
	require.Equal(t, strings.Join(want, ";"), fields["SignedHeaders"])

	credParts := strings.Split(fields["Credential"], "/")
	require.Len(t, credParts, 5, "credential scope shape: %q", fields["Credential"])
	day, scopeRegion, service, terminator := credParts[1], credParts[2], credParts[3], credParts[4]
	require.Equal(t, region, scopeRegion)
	require.Equal(t, "s3", service)
	require.Equal(t, "aws4_request", terminator)

	amzDate := r.Header.Get("x-amz-date")
	require.Regexp(t, `^\d{8}T\d{6}Z$`, amzDate)
	require.Equal(t, day, amzDate[:8], "scope date must match x-amz-date")

	payloadHash := r.Header.Get("x-amz-content-sha256")
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), payloadHash, "content hash must cover the exact body bytes")

	escape := func(s string, path bool) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			c := s[i]
			unreserved := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
				c == '-' || c == '_' || c == '.' || c == '~'
			switch {
			case unreserved, c == '/' && path:
				b.WriteByte(c)
			default:
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		return b.String()
	}
	query := r.URL.Query()
	names := make([]string, 0, len(query))
	for k := range query {
		names = append(names, k)
	}
	sort.Strings(names)
	var cq strings.Builder
	for i, k := range names {
		vals := append([]string(nil), query[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			if cq.Len() > 0 {
				cq.WriteByte('&')
			}
			_ = i
			cq.WriteString(escape(k, false))
			cq.WriteByte('=')
			cq.WriteString(escape(v, false))
		}
	}
	var ch strings.Builder
	for _, n := range want {
		v := r.Header.Get(n)
		if n == "host" {
			v = r.Host
		}
		ch.WriteString(n + ":" + v + "\n")
	}
	canonical := strings.Join([]string{
		r.Method,
		escape(r.URL.EscapedPath(), true),
		cq.String(),
		ch.String(),
		strings.Join(want, ";"),
		payloadHash,
	}, "\n")
	csum := sha256.Sum256([]byte(canonical))
	stringToSign := algo + "\n" + amzDate + "\n" + strings.Join(credParts[1:], "/") + "\n" + hex.EncodeToString(csum[:])

	mac := func(key []byte, s string) []byte {
		h := hmac.New(sha256.New, key)
		_, _ = h.Write([]byte(s))
		return h.Sum(nil)
	}
	k := mac([]byte("AWS4"+secret), day)
	k = mac(k, region)
	k = mac(k, "s3")
	k = mac(k, "aws4_request")
	require.Equal(t, hex.EncodeToString(mac(k, stringToSign)), fields["Signature"], "signature mismatch")
}

const (
	directTestAccess = "TESTACCESSKEY"
	directTestSecret = "TESTSECRETKEY-d0-not-real-0123456789abcdef"
	directTestRegion = "us-east-1"
	directTestBucket = "vaultix-test"
)

func directTestJob(jobID string, srv *httptest.Server) PolledJob {
	return PolledJob{
		ID: jobID, PolicyID: "p1", PolicyVersion: 1, Kind: "filesystem",
		UploadMode: UploadModeDirect,
		DestinationCredential: &DestinationCredential{
			AccessKeyID: directTestAccess, SecretAccessKey: directTestSecret,
			Endpoint: srv.URL, Region: directTestRegion,
			Bucket: directTestBucket, UsePathStyle: true, Prefix: "tenant-a",
		},
	}
}

// noGrantStub fails any test whose direct path wrongly calls the manager
// grant/multipart endpoints.
func noGrantStub(t *testing.T) *StubManagerClient {
	t.Helper()
	stub := &StubManagerClient{}
	stub.OnUploadGrants = func(string, UploadGrantsRequest) (UploadGrantsResponse, error) {
		t.Fatal("direct upload must not call UploadGrants")
		return UploadGrantsResponse{}, nil
	}
	stub.OnMultipartInit = func(string, MultipartInitRequest) (MultipartInitResponse, error) {
		t.Fatal("direct upload must not call MultipartInit")
		return MultipartInitResponse{}, nil
	}
	stub.OnMultipartDone = func(string, MultipartCompleteRequest) error {
		t.Fatal("direct upload must not call MultipartComplete")
		return nil
	}
	stub.OnMultipartAbort = func(string, MultipartAbortRequest) error {
		t.Fatal("direct upload must not call MultipartAbort")
		return nil
	}
	return stub
}

func TestDirectSinglePUT(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}
	var progress int64
	up.OnProgress = func(n int64) { progress += n }

	job := directTestJob("job-1", srv)
	a0 := stageFile(t, "t0.tar.zst", []byte("payload-zero"))
	a0.SHA256 = "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	a1 := stageFile(t, "t1.tar.zst", []byte("payload-one"))

	got, err := up.UploadJobFor(context.Background(), job, []*Artifact{a0, a1})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "tenant-a/jobs/job-1/t0.tar.zst", got[0].Key)
	assert.Equal(t, "tenant-a/jobs/job-1/t1.tar.zst", got[1].Key)
	assert.Equal(t, []byte("payload-zero"), f.objects["tenant-a/jobs/job-1/t0.tar.zst"])
	assert.Equal(t, []byte("payload-one"), f.objects["tenant-a/jobs/job-1/t1.tar.zst"])
	// The attested checksum rides as object metadata for the manager
	// verify gate; the unstamped object carries none.
	assert.Equal(t, "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		f.objectMeta["tenant-a/jobs/job-1/t0.tar.zst"])
	assert.Empty(t, f.objectMeta["tenant-a/jobs/job-1/t1.tar.zst"])
	assert.Equal(t, 2, f.verified, "every request signature-verified")
	assert.Equal(t, int64(len("payload-zero")+len("payload-one")), progress)
}

func TestDirectMultipartFlow(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}

	job := directTestJob("job-9", srv)
	bigPath := filepath.Join(t.TempDir(), "big.bin")
	big, err := os.Create(bigPath)
	require.NoError(t, err)
	require.NoError(t, big.Truncate(SingleUploadMaxBytes+1))
	require.NoError(t, big.Close())
	whole := sha256.Sum256(make([]byte, SingleUploadMaxBytes+1))
	bigArt := &Artifact{Name: "big.bin", Path: bigPath,
		SizeBytes: SingleUploadMaxBytes + 1, SHA256: hex.EncodeToString(whole[:])}

	got, err := up.UploadJobFor(context.Background(), job, []*Artifact{bigArt})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "tenant-a/jobs/job-9/big.bin", got[0].Key)

	stored := f.objects["tenant-a/jobs/job-9/big.bin"]
	require.Len(t, stored, int(SingleUploadMaxBytes+1), "assembled object must hold every part byte")
	assert.Equal(t, make([]byte, SingleUploadMaxBytes+1), stored)
	// Whole-object checksum stamped at initiate, readable via HEAD later.
	assert.Equal(t, "sha256:"+hex.EncodeToString(whole[:]), f.objectMeta["tenant-a/jobs/job-9/big.bin"])
	assert.Empty(t, f.aborted, "successful multipart must not abort")
	// 1 initiate + 7 parts + 1 complete, all signature-verified.
	assert.Equal(t, 9, f.verified)

	// The completion document lists 7 ordered parts with ETags.
	require.Len(t, f.completeBodies, 1)
	var doc struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []struct {
			Number int    `xml:"PartNumber"`
			ETag   string `xml:"ETag"`
		} `xml:"Part"`
	}
	for _, raw := range f.completeBodies {
		require.NoError(t, xml.Unmarshal(raw, &doc))
	}
	require.Len(t, doc.Parts, 7)
	for i, p := range doc.Parts {
		assert.Equal(t, i+1, p.Number)
		assert.NotEmpty(t, p.ETag)
	}
}

func TestDirectMultipartAbortsOnPartFailure(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	f.failedParts[2] = 100 // part 2 always fails: retries exhaust, then abort
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}

	job := directTestJob("job-9", srv)
	bigPath := filepath.Join(t.TempDir(), "big.bin")
	big, err := os.Create(bigPath)
	require.NoError(t, err)
	require.NoError(t, big.Truncate(SingleUploadMaxBytes+1))
	require.NoError(t, big.Close())
	bigArt := &Artifact{Name: "big.bin", Path: bigPath, SizeBytes: SingleUploadMaxBytes + 1}

	_, err = up.UploadJobFor(context.Background(), job, []*Artifact{bigArt})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "part 2")
	assert.Contains(t, err.Error(), "s3:AccessDenied")
	require.Len(t, f.aborted, 1, "failed direct multipart must be aborted server-side")
	assert.Empty(t, f.objects, "no assembled object may survive a failed multipart")
	assert.NotContains(t, err.Error(), directTestSecret)
	assert.NotContains(t, err.Error(), directTestAccess)
}

func TestDirectSinglePUTSurfacesS3Code(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	f.failSinglePUT = true
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}

	_, err := up.UploadJobFor(context.Background(), directTestJob("job-1", srv),
		[]*Artifact{stageFile(t, "x", []byte("data"))})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3:AccessDenied")
	assert.Contains(t, err.Error(), "denied by policy")
	assert.NotContains(t, err.Error(), directTestSecret)
}

func TestDirectCompleteFailureSurfacesS3Code(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	f.failComplete = true
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}
	ep, err := newDirectEndpoint("job-1", directTestJob("job-1", srv).DestinationCredential)
	require.NoError(t, err)
	t.Cleanup(ep.signer.Wipe)

	key := "tenant-a/jobs/job-1/x"
	uploadID, err := up.directInitiateOnce(context.Background(), ep, stageFile(t, "x", []byte("data")), key)
	require.NoError(t, err)
	err = up.directComplete(context.Background(), ep, key, uploadID,
		[]directCompletePart{{PartNumber: 1, ETag: "etag-1"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3:InternalError")
	assert.NotContains(t, err.Error(), directTestSecret)
}

func TestDirectMissingCredentialLoud(t *testing.T) {
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}

	job := PolledJob{ID: "job-1", UploadMode: "direct", DestinationCredential: nil}
	_, err := up.UploadJobFor(context.Background(), job, []*Artifact{stageFile(t, "x", []byte("data"))})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "destination_credential")
	assert.Contains(t, err.Error(), "refusing to fall back to presigned grants")
}

func TestDirectIncompleteCredential(t *testing.T) {
	_, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	full := *directTestJob("job-1", srv).DestinationCredential
	cases := []struct {
		name   string
		mutate func(*DestinationCredential)
		want   string
	}{
		{"empty access", func(c *DestinationCredential) { c.AccessKeyID = "" }, "access_key_id is required"},
		{"empty secret", func(c *DestinationCredential) { c.SecretAccessKey = "" }, "secret_access_key is required"},
		{"empty region", func(c *DestinationCredential) { c.Region = "" }, "region is required"},
		{"empty bucket", func(c *DestinationCredential) { c.Bucket = "" }, "bucket is required"},
		{"bad scheme", func(c *DestinationCredential) { c.Endpoint = "ftp://host/x" }, "scheme must be http or https"},
		{"no host", func(c *DestinationCredential) { c.Endpoint = "https://" }, "host is required"},
		{"bad prefix", func(c *DestinationCredential) { c.Prefix = "a/../b" }, "invalid prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred := full
			tc.mutate(&cred)
			up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}
			job := PolledJob{ID: "job-1", UploadMode: "direct", DestinationCredential: &cred}
			_, err := up.UploadJobFor(context.Background(), job, []*Artifact{stageFile(t, "x", []byte("data"))})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), directTestSecret)
		})
	}
}

func TestUploadModeBranchMatrix(t *testing.T) {
	_, gsrv := newFakeGrantServer(t, 0)
	_, ssrv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)

	t.Run("absent mode uses presigned grants", func(t *testing.T) {
		stub := &StubManagerClient{}
		grantCalls := 0
		stub.OnUploadGrants = func(string, UploadGrantsRequest) (UploadGrantsResponse, error) {
			grantCalls++
			return UploadGrantsResponse{Grants: []UploadGrant{{Key: "k", URL: gsrv.URL + "/put/x"}}}, nil
		}
		up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
		_, err := up.UploadJobFor(context.Background(),
			PolledJob{ID: "job-1"}, []*Artifact{stageFile(t, "x", []byte("data"))})
		require.NoError(t, err)
		assert.Equal(t, 1, grantCalls)
	})

	t.Run("explicit presigned mode uses grants", func(t *testing.T) {
		stub := &StubManagerClient{}
		grantCalls := 0
		stub.OnUploadGrants = func(string, UploadGrantsRequest) (UploadGrantsResponse, error) {
			grantCalls++
			return UploadGrantsResponse{Grants: []UploadGrant{{Key: "k", URL: gsrv.URL + "/put/x"}}}, nil
		}
		up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
		_, err := up.UploadJobFor(context.Background(),
			PolledJob{ID: "job-1", UploadMode: "presigned"}, []*Artifact{stageFile(t, "x", []byte("data"))})
		require.NoError(t, err)
		assert.Equal(t, 1, grantCalls)
	})

	t.Run("direct mode never touches grants", func(t *testing.T) {
		up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}
		got, err := up.UploadJobFor(context.Background(), directTestJob("job-1", ssrv),
			[]*Artifact{stageFile(t, "x", []byte("data"))})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "tenant-a/jobs/job-1/x", got[0].Key)
	})

	t.Run("unknown mode is a loud error", func(t *testing.T) {
		up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}
		_, err := up.UploadJobFor(context.Background(),
			PolledJob{ID: "job-1", UploadMode: "carrier-pigeon"}, []*Artifact{stageFile(t, "x", []byte("data"))})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown upload_mode "carrier-pigeon"`)
	})
}

func TestDirectObjectKeyVectors(t *testing.T) {
	valid := []struct{ prefix, job, name, want string }{
		{"tenant-a", "job-1", "t0.tar.zst", "tenant-a/jobs/job-1/t0.tar.zst"},
		{"", "job-1", "t0.tar.zst", "jobs/job-1/t0.tar.zst"},
		{"/tenant-a/", "job-1", "manifest.json", "tenant-a/jobs/job-1/manifest.json"},
		{"a/b", "j", "n", "a/b/jobs/j/n"},
	}
	for _, tc := range valid {
		got, err := directObjectKey(tc.prefix, tc.job, tc.name)
		require.NoError(t, err, "%+v", tc)
		assert.Equal(t, tc.want, got)
	}
	invalid := []struct{ prefix, job, name string }{
		{"tenant-a", "job-1", "../escape"},
		{"tenant-a", "job-1", "a/../b"},
		{"tenant-a", "job-1", "/abs"},
		{"tenant-a", "job-1", "a//b"},
		{"tenant-a", "job-1", ""},
		{"tenant-a", "job-1", `.`},
		{"tenant-a", "job-1", ".."},
		{"tenant-a", "job-1", `a\b`},
		{"tenant-a", "", "n"},
		{"tenant-a", "  ", "n"},
		{"tenant-a", "a/b", "n"},
		{"tenant-a", "..", "n"},
		{"a/../b", "job-1", "n"},
	}
	for _, tc := range invalid {
		_, err := directObjectKey(tc.prefix, tc.job, tc.name)
		require.Error(t, err, "%+v must be rejected", tc)
	}
}

func TestDirectEndpointAddressing(t *testing.T) {
	ep, err := newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: "s", Region: "us-east-1",
		Endpoint: "https://s3.us-east-1.amazonaws.com", Bucket: "mybucket",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://mybucket.s3.us-east-1.amazonaws.com/key",
		ep.objectURL("key", "").String(), "dns bucket + no path-style flag = virtual-hosted")

	ep, err = newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: "s", Region: "us-east-1",
		Endpoint: "https://s3.us-east-1.amazonaws.com", Bucket: "mybucket", UsePathStyle: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "https://s3.us-east-1.amazonaws.com/mybucket/key",
		ep.objectURL("key", "").String(), "explicit path style")

	ep, err = newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: "s", Region: "us-east-1",
		Endpoint: "http://127.0.0.1:9000", Bucket: "mybucket",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9000/mybucket/key",
		ep.objectURL("key", "").String(), "IP endpoints always use path style")

	ep, err = newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: "s", Region: "us-east-1",
		Endpoint: "http://127.0.0.1:9000", Bucket: "not_dns_compatible!",
	})
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9000/not_dns_compatible%21/key",
		ep.objectURL("key", "").String(),
		"non-unreserved bytes are escaped identically on the wire and in the signature")

	ep, err = newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: "s", Region: "eu-west-1", Bucket: "bkt",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://bkt.s3.eu-west-1.amazonaws.com/key",
		ep.objectURL("key", "").String(),
		"empty endpoint defaults to AWS (virtual-hosted for DNS buckets)")

	assert.Equal(t, "https://h/b/k?partNumber=3&uploadId=u%2B1",
		(&directEndpoint{scheme: "https", host: "h", bucket: "b", pathStyle: true}).
			objectURL("k", "partNumber=3&uploadId=u%2B1").String(),
		"pre-encoded query passes through verbatim")
}

func TestDirectCredentialNeverPersistedOrLogged(t *testing.T) {
	f, srv := newFakeS3(t, directTestSecret, directTestRegion, directTestBucket)
	f.failSinglePUT = true
	up := &Uploader{Manager: noGrantStub(t), NewBackOff: fastBackoff}

	_, err := up.UploadJobFor(context.Background(), directTestJob("job-1", srv),
		[]*Artifact{stageFile(t, "x", []byte("data"))})
	require.Error(t, err)
	require.Contains(t, err.Error(), "s3:AccessDenied")

	// The failure a runner would persist (LastError) must not smuggle
	// key material into the state file.
	store, serr := NewStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, serr)
	require.NoError(t, store.Upsert(JobRecord{JobID: "job-1", LastError: err.Error()}))
	raw, rerr := os.ReadFile(store.path)
	require.NoError(t, rerr)
	assert.NotContains(t, string(raw), directTestSecret)
	assert.NotContains(t, string(raw), directTestAccess)

	// By construction the record shape has nowhere to hold a
	// destination credential; fail loudly if that ever changes.
	rawRec, _ := json.Marshal(JobRecord{})
	assert.NotContains(t, string(rawRec), "destination_credential")
	assert.NotContains(t, string(rawRec), "secret_access_key")
}

func TestDirectEndpointSecretWiped(t *testing.T) {
	ep, err := newDirectEndpoint("job-1", &DestinationCredential{
		AccessKeyID: "a", SecretAccessKey: directTestSecret,
		Region: "r", Bucket: "b", Endpoint: "http://127.0.0.1:9",
	})
	require.NoError(t, err)
	require.NotEqual(t, make([]byte, len(directTestSecret)), []byte(ep.signer.SecretKey))
	ep.signer.Wipe()
	assert.Equal(t, make([]byte, len(directTestSecret)), []byte(ep.signer.SecretKey))
}

func TestPolledJobDirectFieldsJSON(t *testing.T) {
	t.Run("new shape decodes", func(t *testing.T) {
		raw := `{"id":"j1","policy_id":"p","policy_version":1,"kind":"filesystem",` +
			`"targets":[],"encryption":{"enabled":false},"upload_mode":"direct",` +
			`"destination_credential":{"access_key_id":"AK","secret_access_key":"SK",` +
			`"endpoint":"http://s3:9000","region":"us-east-1","bucket":"b",` +
			`"use_path_style":true,"prefix":"t"}}`
		var job PolledJob
		require.NoError(t, json.Unmarshal([]byte(raw), &job))
		assert.Equal(t, "direct", job.UploadMode)
		require.NotNil(t, job.DestinationCredential)
		assert.Equal(t, "AK", job.DestinationCredential.AccessKeyID)
		assert.Equal(t, "SK", job.DestinationCredential.SecretAccessKey)
		assert.Equal(t, "http://s3:9000", job.DestinationCredential.Endpoint)
		assert.Equal(t, "us-east-1", job.DestinationCredential.Region)
		assert.Equal(t, "b", job.DestinationCredential.Bucket)
		assert.True(t, job.DestinationCredential.UsePathStyle)
		assert.Equal(t, "t", job.DestinationCredential.Prefix)
	})

	t.Run("old shape defaults to presigned", func(t *testing.T) {
		var job PolledJob
		require.NoError(t, json.Unmarshal([]byte(
			`{"id":"j1","policy_id":"p","policy_version":1,"kind":"filesystem","targets":[]}`), &job))
		assert.Empty(t, job.UploadMode)
		assert.Nil(t, job.DestinationCredential)
	})
}
