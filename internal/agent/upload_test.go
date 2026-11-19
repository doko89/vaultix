package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGrantServer emulates presigned-URL storage: PUT stores bodies by path,
// GET serves them. failsFirstN PUTs fail with 500 to exercise retries.
type fakeGrantServer struct {
	t          *testing.T
	mu         chan struct{}
	store      map[string][]byte
	putCount   atomic.Int64
	failsFirst int64
	authSeen   atomic.Bool
}

func newFakeGrantServer(t *testing.T, failsFirst int64) (*fakeGrantServer, *httptest.Server) {
	t.Helper()
	f := &fakeGrantServer{t: t, store: map[string][]byte{}, failsFirst: failsFirst}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			f.authSeen.Store(true)
		}
		switch r.Method {
		case http.MethodPut:
			n := f.putCount.Add(1)
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			if n <= f.failsFirst {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			f.store[r.URL.Path] = body
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%s"`, r.URL.Path))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			body, ok := f.store[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func stageFile(t *testing.T, name string, body []byte) *Artifact {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, body, 0o600))
	return &Artifact{Name: name, Path: p, SizeBytes: int64(len(body))}
}

func fastBackoff() backoff.BackOff { return backoff.NewConstantBackOff(0) }

func TestUploadSinglePUT(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		require.Len(t, req.Objects, 2)
		assert.Equal(t, "job-1", jobID)
		return UploadGrantsResponse{Grants: []UploadGrant{
			{Key: "tenant/jobs/job-1/t0.tar.zst", URL: srv.URL + "/put/t0?sig=abc"},
			{Key: "tenant/jobs/job-1/t1.tar.zst", URL: srv.URL + "/put/t1?sig=abc"},
		}}, nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	var progressTotal int64
	up.OnProgress = func(n int64) { progressTotal += n }

	arts := []*Artifact{
		stageFile(t, "t0.tar.zst", []byte("payload-zero")),
		stageFile(t, "t1.tar.zst", []byte("payload-one")),
	}
	got, err := up.UploadJob(context.Background(), "job-1", arts)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Manager-returned keys used verbatim, matched by index.
	assert.Equal(t, "tenant/jobs/job-1/t0.tar.zst", got[0].Key)
	assert.Equal(t, "tenant/jobs/job-1/t1.tar.zst", got[1].Key)
	assert.Equal(t, []byte("payload-zero"), f.store["/put/t0"])
	assert.Equal(t, []byte("payload-one"), f.store["/put/t1"])
	assert.False(t, f.authSeen.Load(), "presigned PUTs must not carry the bearer token")
	assert.Equal(t, int64(len("payload-zero")+len("payload-one")), progressTotal)
}

func TestUploadSinglePUTSendsChecksumMetadata(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	var gotMeta atomic.Value
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMeta.Store(r.Header.Get(checksumMetaHeader))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		f.store[r.URL.Path] = body
		w.WriteHeader(http.StatusOK)
	})
	stub := &StubManagerClient{}
	stub.OnUploadGrants = func(_ string, _ UploadGrantsRequest) (UploadGrantsResponse, error) {
		return UploadGrantsResponse{Grants: []UploadGrant{
			{Key: "tenant/jobs/job-1/t0.tar.zst", URL: srv.URL + "/put/t0"},
		}}, nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	a := stageFile(t, "t0.tar.zst", []byte("payload-zero"))
	a.SHA256 = "ABCDEF0123456789"
	_, err := up.UploadJob(context.Background(), "job-1", []*Artifact{a})
	require.NoError(t, err)
	assert.Equal(t, "sha256:abcdef0123456789", gotMeta.Load(),
		"PUT must carry the payload checksum for the manager verify gate")
}

func TestUploadRetriesThenSucceeds(t *testing.T) {
	f2, srv2 := newFakeGrantServer(t, 2) // first 2 PUTs fail
	stub := &StubManagerClient{}
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		return UploadGrantsResponse{Grants: []UploadGrant{
			{Key: "k", URL: srv2.URL + "/put/x"},
		}}, nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	_, err := up.UploadJob(context.Background(), "job-1", []*Artifact{stageFile(t, "x", []byte("data"))})
	require.NoError(t, err)
	assert.Equal(t, int64(3), f2.putCount.Load())
	assert.Equal(t, []byte("data"), f2.store["/put/x"])
}

func TestUploadRetriesExhausted(t *testing.T) {
	_, srv := newFakeGrantServer(t, 100) // always fail
	stub := &StubManagerClient{}
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		return UploadGrantsResponse{Grants: []UploadGrant{{Key: "k", URL: srv.URL + "/put/x"}}}, nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	_, err := up.UploadJob(context.Background(), "job-1", []*Artifact{stageFile(t, "x", []byte("data"))})
	require.ErrorContains(t, err, "after 5 attempts")
}

func TestUploadMultipartFlow(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	var completed bool
	var gotParts []MultipartPart
	stub.OnMultipartInit = func(jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
		assert.Equal(t, "job-9", jobID)
		assert.Equal(t, int64(MultipartPartSize), req.PartSize)
		return MultipartInitResponse{
			UploadID: "u-1", Key: "tenant/jobs/job-9/big",
			PartURLs: []string{srv.URL + "/mp/p1", srv.URL + "/mp/p2"},
		}, nil
	}
	stub.OnMultipartDone = func(jobID string, req MultipartCompleteRequest) error {
		completed = true
		assert.Equal(t, "u-1", req.UploadID)
		gotParts = req.Parts
		return nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	// Small staged file → single part through the multipart path.
	body := bytes.Repeat([]byte("z"), 1000)
	key, err := up.uploadMultipart(context.Background(), "job-9", stageFile(t, "big", body))
	require.NoError(t, err)
	assert.Equal(t, "tenant/jobs/job-9/big", key)
	assert.True(t, completed)
	require.Len(t, gotParts, 1)
	assert.Equal(t, 1, gotParts[0].Number)
	assert.NotEmpty(t, gotParts[0].ETag)
	assert.Len(t, f.store["/mp/p1"], 1000)
	// The completion call carries the part's content hash + length, which
	// the manager gate verifies (ETags alone are md5 composites).
	sum := sha256.Sum256(body)
	assert.Equal(t, hex.EncodeToString(sum[:]), gotParts[0].SHA256)
	assert.Equal(t, int64(len(body)), gotParts[0].SizeBytes)
}

func TestUploadMultipartSendsChecksumsPerPart(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	var gotParts []MultipartPart
	stub.OnMultipartInit = func(jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
		return MultipartInitResponse{
			UploadID: "u-multi", Key: "tenant/jobs/job-9/big",
			PartURLs: []string{srv.URL + "/mp/p1", srv.URL + "/mp/p2"},
		}, nil
	}
	stub.OnMultipartDone = func(jobID string, req MultipartCompleteRequest) error {
		gotParts = req.Parts
		return nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	// Just over one part: exercises a full part plus a short tail.
	first := bytes.Repeat([]byte("a"), MultipartPartSize)
	tail := bytes.Repeat([]byte("b"), 100)
	body := append(append([]byte(nil), first...), tail...)
	key, err := up.uploadMultipart(context.Background(), "job-9", stageFile(t, "big", body))
	require.NoError(t, err)
	assert.Equal(t, "tenant/jobs/job-9/big", key)
	require.Len(t, gotParts, 2)
	for i, want := range [][]byte{first, tail} {
		sum := sha256.Sum256(want)
		assert.Equal(t, i+1, gotParts[i].Number)
		assert.NotEmpty(t, gotParts[i].ETag)
		assert.Equal(t, hex.EncodeToString(sum[:]), gotParts[i].SHA256, "part %d hash", i+1)
		assert.Equal(t, int64(len(want)), gotParts[i].SizeBytes, "part %d length", i+1)
	}
	assert.Len(t, f.store["/mp/p1"], MultipartPartSize)
	assert.Len(t, f.store["/mp/p2"], 100)
}

func TestUploadMultipartAbortsOnPartFailure(t *testing.T) {
	_, srv := newFakeGrantServer(t, 100)
	stub := &StubManagerClient{}
	var aborted bool
	stub.OnMultipartInit = func(jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
		return MultipartInitResponse{UploadID: "u-2", Key: "k", PartURLs: []string{srv.URL + "/mp/p1"}}, nil
	}
	stub.OnMultipartAbort = func(jobID string, req MultipartAbortRequest) error {
		aborted = true
		assert.Equal(t, "u-2", req.UploadID)
		return nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	_, err := up.uploadMultipart(context.Background(), "job-9", stageFile(t, "big", []byte("data")))
	require.Error(t, err)
	assert.True(t, aborted, "failed multipart must be aborted")
}

func TestUploadSelectsMultipartAboveThreshold(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	var grantCalls, initCalls int
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		grantCalls++
		grants := make([]UploadGrant, 0, len(req.Objects))
		for i := range req.Objects {
			grants = append(grants, UploadGrant{Key: "k", URL: fmt.Sprintf("%s/put/s%d", srv.URL, i)})
		}
		return UploadGrantsResponse{Grants: grants}, nil
	}
	stub.OnMultipartInit = func(jobID string, req MultipartInitRequest) (MultipartInitResponse, error) {
		initCalls++
		urls := make([]string, 0, 8)
		for i := range 8 {
			urls = append(urls, fmt.Sprintf("%s/mp/%d", srv.URL, i))
		}
		return MultipartInitResponse{UploadID: "u-big", Key: "tenant/jobs/job-1/big", PartURLs: urls}, nil
	}
	stub.OnMultipartDone = func(jobID string, req MultipartCompleteRequest) error {
		assert.Len(t, req.Parts, 7, "101MB at 16MB parts")
		return nil
	}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}

	// Sparse >100MB file: reads as zeros, routes to multipart.
	bigPath := filepath.Join(t.TempDir(), "big.bin")
	big, err := os.Create(bigPath)
	require.NoError(t, err)
	require.NoError(t, big.Truncate(SingleUploadMaxBytes+1))
	require.NoError(t, big.Close())
	bigArt := &Artifact{Name: "big.bin", Path: bigPath, SizeBytes: SingleUploadMaxBytes + 1}
	smallArt := stageFile(t, "small.bin", []byte("small"))

	got, err := up.UploadJob(context.Background(), "job-1", []*Artifact{smallArt, bigArt})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, grantCalls, "small object uses single-PUT grants")
	assert.Equal(t, 1, initCalls, "large object uses multipart")
	assert.Equal(t, "tenant/jobs/job-1/big", got[1].Key)
	assert.Equal(t, []byte("small"), f.store["/put/s0"])
}
