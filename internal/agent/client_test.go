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
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotReq))
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{OK: true, JobsPending: 2})
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "s3cret", 5*time.Second)
	resp, err := c.Heartbeat(context.Background(), HeartbeatRequest{AgentID: "a-1"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, 2, resp.JobsPending)
	assert.Equal(t, "Bearer s3cret", gotAuth)
	assert.Equal(t, "a-1", gotReq.AgentID)
}

func TestHTTPManagerClientPollAndReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agents/a-9/jobs":
			_ = json.NewEncoder(w).Encode([]Job{{
				ID: "j-1", AgentID: "a-9", Policy: "p", PolicyVersion: 1,
				Kind: JobKindFilesystem, Targets: []string{"/x"},
			}})
		case "/api/v1/agents/a-9/jobs/result":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewHTTPManagerClient(srv.URL, "t", 5*time.Second)
	jobs, err := c.PollJobs(context.Background(), "a-9")
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "j-1", jobs[0].ID)
	require.NoError(t, c.ReportResult(context.Background(), JobResult{
		JobID: "j-1", AgentID: "a-9", State: JobSucceeded,
	}))
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
