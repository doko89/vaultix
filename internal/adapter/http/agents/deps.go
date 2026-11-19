package agents

import (
	"context"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
)

// QueuedJob is the dispatch slice of a job handed to a polling agent. It is
// intentionally narrow: the jobs track owns the full job model and maps it
// onto this shape.
type QueuedJob struct {
	ID            string   `json:"id"`
	PolicyID      string   `json:"policy_id"`
	PolicyVersion int      `json:"policy_version"`
	Kind          string   `json:"kind"`
	Targets       []string `json:"targets"`
	State         string   `json:"state"`
	Attempt       int      `json:"attempt"`
}

// JobReport is an agent's validated progress/outcome report for one job.
type JobReport struct {
	JobID         string
	State         domain.JobState
	Error         string
	BytesRead     *uint64
	BytesUploaded *uint64
}

// JobQueue is the narrow seam to the jobs track (NOT usecase/job, which the
// parallel track owns). The jobs-track service implements it; this package
// only polls dispatchable jobs and accepts reports.
type JobQueue interface {
	Poll(ctx context.Context, agentID string, limit int) ([]QueuedJob, error)
	Report(ctx context.Context, agentID string, rep JobReport) error
}

// AgentService mirrors the agent usecase surface this package needs. Deps
// holds it as an interface so handlers stay testable without a database.
type AgentService interface {
	IssueEnrollmentToken(ctx context.Context, ttl time.Duration) (string, *domain.AgentCredential, error)
	RedeemEnrollmentToken(ctx context.Context, token string, reg agentuc.Registration) (*domain.Agent, string, error)
	Heartbeat(ctx context.Context, in agentuc.HeartbeatInput) (*agentuc.HeartbeatResult, error)
	GetAgent(ctx context.Context, id string) (*agentuc.AgentView, error)
	ListAgents(ctx context.Context, opts domain.ListOptions, filter agentuc.AgentFilter) (*agentuc.AgentListResult, error)
	RevokeAgent(ctx context.Context, id string) error
	SetDisabled(ctx context.Context, id string, disabled bool) error
	AgentPolicies(ctx context.Context, agentID string) ([]agentuc.PolicySummary, error)
	AuthenticateAgent(ctx context.Context, bearer string) (*domain.AgentCredential, error)
}

// DiscoveryService mirrors the discovery usecase surface this package needs.
type DiscoveryService interface {
	Ingest(ctx context.Context, agentID string, resources []discoveryuc.ResourceInput) (int, error)
	List(ctx context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error)
}

// Compile-time proof that the real usecase services satisfy the seams.
var (
	_ AgentService     = (*agentuc.Service)(nil)
	_ DiscoveryService = (*discoveryuc.Service)(nil)
)
