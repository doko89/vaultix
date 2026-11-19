package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPManagerClientHeartbeat(t *testing.T) {
	var gotAuth string
	var gotReq HeartbeatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/agent/heartbeat", r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{OK: true, Status: "active"})
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "s3cret", 5*time.Second)
	resp, err := c.Heartbeat(context.Background(), HeartbeatRequest{AgentID: "a-1"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "active", resp.Status)
	assert.Equal(t, "Bearer "+"s3"+"cret", gotAuth)
	assert.Equal(t, "a-1", gotReq.AgentID)
}

func TestHTTPManagerClientPollAndReport(t *testing.T) {
	var gotReport map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/poll":
			assert.Equal(t, "3", r.URL.Query().Get("limit"))
			_ = json.NewEncoder(w).Encode(PollResponse{
				Jobs: []PolledJob{{
					ID: "j-1", PolicyID: "p", PolicyVersion: 1, Kind: "filesystem",
					Targets: []Target{{Selector: "/x"}},
				}},
				CancelledJobIDs: []string{},
			})
		case "/api/agent/report":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReport))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "t", 5*time.Second)
	resp, err := c.PollJobs(context.Background(), 3)
	require.NoError(t, err)
	require.Len(t, resp.Jobs, 1)
	assert.Equal(t, "j-1", resp.Jobs[0].ID)

	require.NoError(t, c.ReportJob(context.Background(), JobReportPayload{
		JobID: "j-1", State: JobSucceeded,
	}))
	require.Contains(t, gotReport, "job")
	assert.Equal(t, "j-1", gotReport["job"].(map[string]any)["job_id"])
}

func TestHTTPManagerClientGrantsAndRestores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/jobs/j-1/upload-grants":
			_ = json.NewEncoder(w).Encode(UploadGrantsResponse{
				Grants: []UploadGrant{{Key: "p/jobs/j-1/t0.tar.zst", URL: "https://s3.example/put"}},
			})
		case "/api/agent/jobs/j-1/multipart/init":
			_ = json.NewEncoder(w).Encode(MultipartInitResponse{
				UploadID: "u-1", Key: "p/jobs/j-1/big", PartURLs: []string{"https://s3.example/p1"},
			})
		case "/api/agent/jobs/j-1/multipart/complete", "/api/agent/jobs/j-1/multipart/abort":
			w.WriteHeader(http.StatusOK)
		case "/api/agent/restores/poll":
			_ = json.NewEncoder(w).Encode(RestorePollResponse{}) // null restore
		case "/api/agent/restores/r-1/download-grants":
			_ = json.NewEncoder(w).Encode(DownloadGrantsResponse{
				Grants: []DownloadGrant{{Key: "k", URL: "https://s3.example/get"}},
			})
		case "/api/agent/restores/r-1/report":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c := NewHTTPManagerClient(srv.URL, "t", 5*time.Second)

	grants, err := c.UploadGrants(ctx, "j-1", UploadGrantsRequest{Objects: []GrantObject{{Key: "t0", SizeBytes: 10}}})
	require.NoError(t, err)
	require.Len(t, grants.Grants, 1)
	assert.Equal(t, "p/jobs/j-1/t0.tar.zst", grants.Grants[0].Key)

	init, err := c.MultipartInit(ctx, "j-1", MultipartInitRequest{Key: "big", SizeBytes: 1 << 30, PartSize: 1 << 24})
	require.NoError(t, err)
	assert.Equal(t, "u-1", init.UploadID)
	require.NoError(t, c.MultipartComplete(ctx, "j-1", MultipartCompleteRequest{UploadID: "u-1", Key: "big"}))
	require.NoError(t, c.MultipartAbort(ctx, "j-1", MultipartAbortRequest{UploadID: "u-1", Key: "big"}))

	got, err := c.PollRestore(ctx, "a-1")
	require.NoError(t, err)
	assert.Nil(t, got)

	dl, err := c.DownloadGrants(ctx, "r-1", []string{"k"})
	require.NoError(t, err)
	require.Len(t, dl.Grants, 1)
	require.NoError(t, c.ReportRestore(ctx, "r-1", RestoreReportRequest{State: "succeeded"}))
}

func TestHTTPManagerClientErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad token"))
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "t", 5*time.Second)
	_, err := c.Heartbeat(context.Background(), HeartbeatRequest{})
	require.ErrorContains(t, err, "401")
	require.ErrorContains(t, err, "bad token")
}

func TestHTTPManagerClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "t", 50*time.Millisecond)
	_, err := c.Heartbeat(context.Background(), HeartbeatRequest{})
	require.Error(t, err, "every call must be bounded by a timeout")
}
