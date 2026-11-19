package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRunner(t *testing.T, stub *StubManagerClient) *Runner {
	t.Helper()
	cfg := testConfig()
	cfg.WorkDir = t.TempDir()
	cfg.CompressionLevel = 1
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)
	return &Runner{
		Client:   stub,
		Config:   cfg,
		Store:    store,
		Executor: &Executor{WorkDir: cfg.WorkDir, CompressionLevel: 1},
		Uploader: &Uploader{Manager: stub, NewBackOff: fastBackoff},
		Restorer: &Restorer{Manager: stub, WorkDir: cfg.WorkDir},
	}
}

func TestRunOneJobSuccessEndToEnd(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	var grantCalls atomic.Int64
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		grants := make([]UploadGrant, 0, len(req.Objects))
		for _, o := range req.Objects {
			n := grantCalls.Add(1) - 1
			grants = append(grants, UploadGrant{
				Key: fmt.Sprintf("tenant/jobs/%s/%s", jobID, o.Key),
				URL: fmt.Sprintf("%s/up/%d", srv.URL, n),
			})
		}
		return UploadGrantsResponse{Grants: grants}, nil
	}
	r := testRunner(t, stub)

	src := t.TempDir()
	content := []byte("backup-me")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f.txt"), content, 0o644))
	r.runOneJob(context.Background(), PolledJob{
		ID: "job-e2e", PolicyID: "p", PolicyVersion: 1, Kind: "filesystem",
		Targets: []Target{{Type: "host_filesystem", Selector: src}},
	})

	require.NotEmpty(t, stub.Reports)
	last := stub.Reports[len(stub.Reports)-1]
	assert.Equal(t, JobSucceeded, last.State)
	require.NotNil(t, last.Manifest, "success must carry a manifest")
	m := last.Manifest
	require.Len(t, m.Objects, 2, "data object + job manifest")
	assert.Equal(t, "tenant/jobs/job-e2e/t0-host_filesystem.tar.zst", m.Objects[0].Key)
	// Manifest sha256 matches the bytes the fake storage received.
	sum := sha256.Sum256(f.store["/up/0"])
	assert.Equal(t, hex.EncodeToString(sum[:]), m.Objects[0].SHA256)
	assert.Equal(t, int64(len(f.store["/up/0"])), m.Objects[0].SizeBytes)
	assert.Equal(t, uint64(len(content)), m.BytesRead)
	assert.Equal(t, "zstd", m.Compression.Algo)
	assert.False(t, m.Encryption.Enabled)
	// The job manifest is uploaded second and attests the data key.
	require.Equal(t, "tenant/jobs/job-e2e/manifest.json", m.Objects[1].Key)
	msum := sha256.Sum256(f.store["/up/1"])
	assert.Equal(t, hex.EncodeToString(msum[:]), m.Objects[1].SHA256)
	assert.Equal(t, int64(len(f.store["/up/1"])), m.Objects[1].SizeBytes)
	dec, err := zstd.NewReader(bytes.NewReader(f.store["/up/1"]))
	require.NoError(t, err)
	raw, err := io.ReadAll(dec)
	dec.Close()
	require.NoError(t, err)
	var doc JobManifestDoc
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Equal(t, "job-e2e", doc.JobID)
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "t0-host_filesystem.tar.zst", doc.Objects[0].Name)
	assert.Equal(t, m.Objects[0].Key, doc.Objects[0].Key)
	assert.Equal(t, m.Objects[0].SHA256, doc.Objects[0].SHA256)
	// Progress reports precede the terminal one.
	assert.Greater(t, len(stub.Reports), 1)
	assert.Equal(t, JobRunning, stub.Reports[0].State)

	rec, ok := r.Store.Get("job-e2e")
	require.True(t, ok)
	assert.Equal(t, JobSucceeded, rec.State)
	assert.True(t, rec.Reported)
}

func TestRunOneJobSkipsAlreadyReported(t *testing.T) {
	stub := &StubManagerClient{}
	polls := atomic.Int64{}
	stub.OnPollJobs = func(limit int) (PollResponse, error) {
		polls.Add(1)
		return PollResponse{Jobs: []PolledJob{{
			ID: "job-done", PolicyID: "p", Kind: "filesystem",
			Targets: []Target{{Type: "mssql", Selector: "db"}}, // would fail if executed
		}}}, nil
	}
	r := testRunner(t, stub)
	require.NoError(t, r.Store.Upsert(JobRecord{
		JobID: "job-done", Policy: "p", State: JobSucceeded, Reported: true,
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.NoError(t, r.RunBackupLoop(ctx))
	assert.Empty(t, stub.Reports, "already-reported jobs must never be re-run or double-reported")
	assert.GreaterOrEqual(t, polls.Load(), int64(1))
}

func TestRunOneJobUnsupportedReportsFailed(t *testing.T) {
	stub := &StubManagerClient{}
	r := testRunner(t, stub)
	r.runOneJob(context.Background(), PolledJob{
		ID: "job-unsup", PolicyID: "p", Kind: "backup",
		Targets: []Target{{Type: "redis", Selector: "cache"}},
	})
	require.NotEmpty(t, stub.Reports)
	last := stub.Reports[len(stub.Reports)-1]
	assert.Equal(t, JobFailed, last.State)
	assert.Contains(t, last.Error, "unsupported target type")
	assert.Nil(t, last.Manifest, "failure must not carry a success manifest")
	rec, _ := r.Store.Get("job-unsup")
	assert.Equal(t, JobFailed, rec.State)
}

func TestCancelAbortPath(t *testing.T) {
	// Blocking dump: the job is guaranteed in-flight when cancel lands.
	fakeBin(t, "pg_dump", "sleep 30\n")
	stub := &StubManagerClient{}
	r := testRunner(t, stub)

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runOneJob(context.Background(), PolledJob{
			ID: "job-cancel", PolicyID: "p", Kind: "postgres",
			Targets: []Target{{Type: "postgres", Selector: "appdb"}},
		})
	}()
	// Wait until the job is running, then deliver a cancellation.
	require.Eventually(t, func() bool {
		id, ok := r.RunningJob()
		return ok && id == "job-cancel"
	}, 5*time.Second, 5*time.Millisecond)
	r.applyCancellations(context.Background(), []string{"job-cancel", "other"})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runOneJob did not finish after cancel")
	}
	require.NotEmpty(t, stub.Reports)
	last := stub.Reports[len(stub.Reports)-1]
	assert.Equal(t, JobCancelled, last.State)
	rec, _ := r.Store.Get("job-cancel")
	assert.Equal(t, JobCancelled, rec.State)
}

func TestBackupLoopSkipsCancelledJobs(t *testing.T) {
	stub := &StubManagerClient{}
	stub.OnPollJobs = func(limit int) (PollResponse, error) {
		return PollResponse{
			Jobs: []PolledJob{{
				ID: "job-skip", PolicyID: "p", Kind: "backup",
				Targets: []Target{{Type: "redis", Selector: "c"}},
			}},
			CancelledJobIDs: []string{"job-skip"},
		}, nil
	}
	r := testRunner(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	require.NoError(t, r.RunBackupLoop(ctx))
	assert.Empty(t, stub.Reports, "cancelled jobs must not execute")
}

func TestRestoreLoopExecutesOne(t *testing.T) {
	payload := makeTarZst(t, map[string]string{"a.txt": "r"})
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	stub := &StubManagerClient{}
	served := atomic.Bool{}
	stub.OnPollRestore = func(agentID string) (*RestoreJob, error) {
		if served.Swap(true) {
			return nil, nil
		}
		return &RestoreJob{
			ID: "r-loop", SnapshotID: "s", Kind: "filesystem",
			Destination:     RestoreDestination{Mode: "path", Path: t.TempDir()},
			OverwritePolicy: "allow",
			Objects:         []RestoreObject{{Key: "k", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}},
		}, nil
	}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: "k", URL: srv.URL + "/o"}}}, nil
	}
	r := testRunner(t, stub)
	r.Config.RestorePollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.NoError(t, r.RunRestoreLoop(ctx))
	require.Len(t, stub.Restores, 1)
	assert.Equal(t, "succeeded", stub.Restores[0].State)
}

func TestRunnerValidate(t *testing.T) {
	r := testRunner(t, &StubManagerClient{})
	assert.NoError(t, r.Validate())
	r.Client = nil
	assert.Error(t, r.Validate())
}

func TestBuildManifestAggregates(t *testing.T) {
	job := PolledJob{ID: "j", Encryption: EncryptionSpec{Enabled: true, KeyRef: "k", KeyB64: "x"}}
	a1 := &Artifact{Name: "t0", SizeBytes: 10, SHA256: "aa", BytesRead: 100, BytesCompressed: 20}
	a2 := &Artifact{Name: "t1", SizeBytes: 30, SHA256: "bb", BytesRead: 300, BytesCompressed: 40}
	m := buildManifest(job, []*Artifact{a1, a2},
		[]UploadedObject{{Artifact: a1, Key: "k0"}, {Artifact: a2, Key: "k1"}}, 400, 40, 5)
	assert.Len(t, m.Objects, 2)
	assert.Equal(t, uint64(400), m.BytesRead)
	assert.Equal(t, uint64(60), m.BytesCompressed)
	assert.Equal(t, uint64(40), m.BytesUploaded)
	assert.Equal(t, 5, m.Compression.Level)
	assert.True(t, m.Encryption.Enabled)
	assert.Equal(t, "k", m.Encryption.KeyRef)
}
