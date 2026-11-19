package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Manager API paths. The Manager endpoints may not exist yet; the client is a
// stub against this contract with timeouts on every call.
const (
	pathHeartbeat    = "/api/v1/agents/heartbeat"
	pathJobs         = "/api/v1/agents/%s/jobs"
	pathJobResult    = "/api/v1/agents/%s/jobs/result"
	maxErrorBodyByte = 4 << 10
)

// HeartbeatRequest carries agent liveness and capacity information.
type HeartbeatRequest struct {
	AgentID      string    `json:"agent_id"`
	Hostname     string    `json:"hostname"`
	Version      string    `json:"version"`
	Capabilities []string  `json:"capabilities,omitempty"`
	DiskTotal    uint64    `json:"disk_total_bytes,omitempty"`
	DiskFree     uint64    `json:"disk_free_bytes,omitempty"`
	At           time.Time `json:"at"`
}

// HeartbeatResponse is the Manager's acknowledgement.
type HeartbeatResponse struct {
	OK          bool      `json:"ok"`
	JobsPending int       `json:"jobs_pending,omitempty"`
	ServerTime  time.Time `json:"server_time,omitempty"`
}

// JobResult reports a terminal job outcome to the Manager.
type JobResult struct {
	JobID     string    `json:"job_id"`
	AgentID   string    `json:"agent_id"`
	State     JobState  `json:"state"`
	Error     string    `json:"error,omitempty"`
	BytesRead uint64    `json:"bytes_read,omitempty"`
	At        time.Time `json:"at"`
}

// ManagerClient is the agent's view of the Manager control plane.
type ManagerClient interface {
	Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error)
	PollJobs(ctx context.Context, agentID string) ([]Job, error)
	ReportResult(ctx context.Context, res JobResult) error
}

// HTTPManagerClient is an HTTPS polling client stub. Every call carries an
// explicit timeout; callers may additionally bound calls with ctx.
type HTTPManagerClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	timeout    time.Duration
}

// NewHTTPManagerClient builds a client for managerURL. The token is attached
// as a Bearer credential and never logged.
func NewHTTPManagerClient(managerURL, token string, timeout time.Duration) *HTTPManagerClient {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return &HTTPManagerClient{
		baseURL:    strings.TrimSuffix(managerURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

func (c *HTTPManagerClient) Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	var out HeartbeatResponse
	if err := c.do(ctx, http.MethodPost, c.baseURL+pathHeartbeat, req, &out); err != nil {
		return HeartbeatResponse{}, err
	}
	return out, nil
}

func (c *HTTPManagerClient) PollJobs(ctx context.Context, agentID string) ([]Job, error) {
	var out []Job
	url := fmt.Sprintf(c.baseURL+pathJobs, agentID)
	if err := c.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPManagerClient) ReportResult(ctx context.Context, res JobResult) error {
	url := fmt.Sprintf(c.baseURL+pathJobResult, res.AgentID)
	return c.do(ctx, http.MethodPost, url, res, nil)
}

func (c *HTTPManagerClient) do(ctx context.Context, method, url string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("agent: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent: manager %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyByte))
		return fmt.Errorf("agent: manager %s %s: status %s: %s",
			method, url, resp.Status, strings.TrimSpace(string(detail)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("agent: decode manager response: %w", err)
	}
	return nil
}

// StubManagerClient is an in-memory ManagerClient for tests and offline runs.
type StubManagerClient struct {
	OnHeartbeat func(req HeartbeatRequest) (HeartbeatResponse, error)
	OnPollJobs  func(agentID string) ([]Job, error)
	OnReport    func(res JobResult) error

	Heartbeats []HeartbeatRequest
	Reports    []JobResult
}

func (s *StubManagerClient) Heartbeat(_ context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	s.Heartbeats = append(s.Heartbeats, req)
	if s.OnHeartbeat != nil {
		return s.OnHeartbeat(req)
	}
	return HeartbeatResponse{OK: true}, nil
}

func (s *StubManagerClient) PollJobs(_ context.Context, agentID string) ([]Job, error) {
	if s.OnPollJobs != nil {
		return s.OnPollJobs(agentID)
	}
	return nil, nil
}

func (s *StubManagerClient) ReportResult(_ context.Context, res JobResult) error {
	s.Reports = append(s.Reports, res)
	if s.OnReport != nil {
		return s.OnReport(res)
	}
	return nil
}
