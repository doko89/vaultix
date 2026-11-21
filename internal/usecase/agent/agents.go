package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// HeartbeatInput is one validated heartbeat report.
type HeartbeatInput struct {
	AgentID         string
	Version         string
	OS              string
	Architecture    string
	IPAddress       string
	DiskTotalBytes  *uint64
	DiskFreeBytes   *uint64
	RunningJobs     int
	DockerAvailable bool
	// Capabilities replaces the advertised set when non-nil.
	Capabilities []string
}

// HeartbeatResult is the persisted heartbeat plus derived presence.
type HeartbeatResult struct {
	Heartbeat domain.AgentHeartbeat
	Status    domain.AgentStatus
	Outdated  bool
}

// Heartbeat ingests one heartbeat: it records the report, refreshes the
// agent's presence facts, and derives online/offline/stale status.
func (s *Service) Heartbeat(ctx context.Context, in HeartbeatInput) (*HeartbeatResult, error) {
	if strings.TrimSpace(in.AgentID) == "" {
		return nil, fmt.Errorf("%w: agent_id is required", domain.ErrValidation)
	}
	if in.RunningJobs < 0 {
		return nil, fmt.Errorf("%w: running_jobs must be >= 0", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, in.AgentID)
	if err != nil {
		return nil, err
	}
	if ag.Disabled {
		return nil, ErrAgentRevoked
	}
	now := s.clock.Now().UTC()
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	hb := &domain.AgentHeartbeat{
		ID:              id,
		AgentID:         ag.ID,
		ReportedAt:      now,
		Version:         strings.TrimSpace(in.Version),
		IPAddress:       strings.TrimSpace(in.IPAddress),
		DiskTotalBytes:  in.DiskTotalBytes,
		DiskFreeBytes:   in.DiskFreeBytes,
		RunningJobs:     in.RunningJobs,
		DockerAvailable: in.DockerAvailable,
	}
	if err := s.agents.RecordHeartbeat(ctx, hb); err != nil {
		return nil, fmt.Errorf("agent: record heartbeat: %w", err)
	}
	ag.LastHeartbeatAt = &now
	if hb.Version != "" {
		ag.Version = hb.Version
	}
	if strings.TrimSpace(in.OS) != "" {
		ag.OS = strings.TrimSpace(in.OS)
	}
	if strings.TrimSpace(in.Architecture) != "" {
		ag.Architecture = strings.TrimSpace(in.Architecture)
	}
	if hb.IPAddress != "" {
		ag.IPAddress = hb.IPAddress
	}
	// Disk facts are sticky: an omitted reading (unknown on the agent)
	// keeps the stored values instead of wiping them.
	if in.DiskTotalBytes != nil {
		ag.DiskTotalBytes = in.DiskTotalBytes
	}
	if in.DiskFreeBytes != nil {
		ag.DiskFreeBytes = in.DiskFreeBytes
	}
	if in.Capabilities != nil {
		ag.Capabilities = append([]string(nil), in.Capabilities...)
	}
	ag.UpdatedAt = now
	if err := s.agents.UpdateAgent(ctx, ag); err != nil {
		return nil, fmt.Errorf("agent: update agent presence: %w", err)
	}
	return &HeartbeatResult{
		Heartbeat: *hb,
		Status:    ag.Status(now, s.heartbeatTimeout),
		Outdated:  s.IsOutdated(ag.Version),
	}, nil
}

// RevokeAgent disables the agent and revokes all of its credentials.
// Heartbeats and authentication fail afterwards.
func (s *Service) RevokeAgent(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, id)
	if err != nil {
		return err
	}
	ag.Disabled = true
	ag.UpdatedAt = s.clock.Now().UTC()
	if err := s.agents.UpdateAgent(ctx, ag); err != nil {
		return fmt.Errorf("agent: disable agent: %w", err)
	}
	creds, err := s.agents.ListAgentCredentials(ctx, id)
	if err != nil {
		return fmt.Errorf("agent: list agent credentials: %w", err)
	}
	for i := range creds {
		creds[i].Revoke()
		if err := s.agents.UpdateCredential(ctx, &creds[i]); err != nil {
			return fmt.Errorf("agent: revoke credential: %w", err)
		}
	}
	return nil
}

// SetDisabled flips the admin disabled flag without touching credentials, so
// disable is reversible while revoke is not.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, id)
	if err != nil {
		return err
	}
	ag.Disabled = disabled
	ag.UpdatedAt = s.clock.Now().UTC()
	if err := s.agents.UpdateAgent(ctx, ag); err != nil {
		return fmt.Errorf("agent: set agent disabled=%v: %w", disabled, err)
	}
	return nil
}

// DeleteAgent permanently removes a revoked (disabled) agent. Non-revoked
// agents are refused so deletion is always a deliberate two-step flow:
// revoke first (cut access), then delete (drop the row).
func (s *Service) DeleteAgent(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, id)
	if err != nil {
		return err
	}
	if !ag.Disabled {
		return fmt.Errorf("%w: only revoked agents can be deleted; revoke the agent first", domain.ErrConflict)
	}
	if err := s.agents.DeleteAgent(ctx, id); err != nil {
		return fmt.Errorf("agent: delete agent: %w", err)
	}
	return nil
}

// AgentView is an agent plus Manager-derived status and version flag.
type AgentView struct {
	Agent    domain.Agent
	Status   domain.AgentStatus
	Outdated bool
}

// GetAgent returns one agent with derived status and outdated flag.
func (s *Service) GetAgent(ctx context.Context, id string) (*AgentView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	return &AgentView{
		Agent:    *ag,
		Status:   ag.Status(now, s.heartbeatTimeout),
		Outdated: s.IsOutdated(ag.Version),
	}, nil
}

// AgentFilter narrows ListAgents.
type AgentFilter struct {
	// Status filters the returned page; empty means all statuses.
	Status domain.AgentStatus
}

// AgentListResult is one page of agent views.
type AgentListResult struct {
	Items []AgentView
	Total int
}

// ListAgents pages agents and derives status per row. A status filter applies
// to the returned page; Total stays the repository match count.
func (s *Service) ListAgents(ctx context.Context, opts domain.ListOptions, filter AgentFilter) (*AgentListResult, error) {
	switch filter.Status {
	case "", domain.AgentStatusOnline, domain.AgentStatusOffline,
		domain.AgentStatusStale, domain.AgentStatusRevoked:
	default:
		return nil, fmt.Errorf("%w: unknown agent status %q", domain.ErrValidation, filter.Status)
	}
	res, err := s.agents.ListAgents(ctx, opts)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	out := &AgentListResult{Total: res.Total}
	for _, ag := range res.Items {
		v := AgentView{
			Agent:    ag,
			Status:   ag.Status(now, s.heartbeatTimeout),
			Outdated: s.IsOutdated(ag.Version),
		}
		if filter.Status != "" && v.Status != filter.Status {
			continue
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// PolicySummary is the inspectable slice of a policy assignment.
type PolicySummary struct {
	ID             string
	Name           string
	Enabled        bool
	CurrentVersion int
}

// AgentPolicies inspects the backup policies assigned to an agent (read-only;
// assignment itself is owned by the policy track).
func (s *Service) AgentPolicies(ctx context.Context, agentID string) ([]PolicySummary, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	if _, err := s.agents.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	const pageSize = 100
	var out []PolicySummary
	for offset := 0; ; offset += pageSize {
		res, err := s.policies.ListPolicies(ctx, domain.ListOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("agent: list policies: %w", err)
		}
		for _, p := range res.Items {
			for _, id := range p.AgentIDs {
				if id == agentID {
					out = append(out, PolicySummary{
						ID:             p.ID,
						Name:           p.Name,
						Enabled:        p.Enabled,
						CurrentVersion: p.CurrentVersion,
					})
					break
				}
			}
		}
		if len(res.Items) < pageSize || offset+len(res.Items) >= res.Total {
			break
		}
	}
	return out, nil
}
