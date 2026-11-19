package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// AgentRepo implements domain.AgentRepository on
// agents/agent_credentials/agent_heartbeats.
//
// Mapping notes: the schema name column mirrors the hostname (domain has no
// separate name). Disabled maps to status 'revoked'/'disabled' on read;
// UpdateAgent never clobbers an online/offline status except through the
// Disabled flag. Denormalized disk facts live in agents.metadata as JSON.
// Heartbeat ReportedAt is server-stamped (received_at) and DockerAvailable
// rides in the heartbeat payload JSON. Credential labels carry the
// enrollment flag ("enrollment" label); unclaimed enrollment tokens with an
// empty AgentID cannot be stored (agent_id is NOT NULL).
type AgentRepo struct {
	baseRepo
}

// enrollmentLabel marks agent-bound enrollment credentials.
const enrollmentLabel = "enrollment"

// agentMetadata is the agents.metadata JSON codec for denormalized facts.
type agentMetadata struct {
	DiskTotalBytes *uint64 `json:"disk_total_bytes,omitempty"`
	DiskFreeBytes  *uint64 `json:"disk_free_bytes,omitempty"`
}

// heartbeatPayload is the agent_heartbeats.payload JSON codec.
type heartbeatPayload struct {
	DockerAvailable bool `json:"docker_available"`
}

// CreateAgent registers an agent. The ID is database-generated; the row
// starts in 'pending' status until the first heartbeat or enrollment.
func (r *AgentRepo) CreateAgent(ctx context.Context, a *domain.Agent) error {
	if a == nil {
		return validationError("agent is required")
	}
	if err := a.Validate(); err != nil {
		return err
	}
	caps, err := encodeJSON(nonNilStrings(a.Capabilities))
	if err != nil {
		return err
	}
	meta, err := encodeJSON(agentMetadata{DiskTotalBytes: a.DiskTotalBytes, DiskFreeBytes: a.DiskFreeBytes})
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateAgent(ctx, gen.CreateAgentParams{
		Name:      a.Hostname,
		Hostname:  a.Hostname,
		IpAddress: a.IPAddress,
		Os:        a.OS,
		Arch:      a.Architecture,
	})
	if err != nil {
		return mapErr(err)
	}
	status := "pending"
	if a.Disabled {
		status = "revoked"
	}
	updated, err := r.q.UpdateAgent(ctx, gen.UpdateAgentParams{
		ID: row.ID, Name: row.Name, Hostname: row.Hostname,
		IpAddress: row.IpAddress, Os: row.Os, Arch: row.Arch,
		AgentVersion: a.Version, Status: status,
		Capabilities: caps, Metadata: meta,
	})
	if err != nil {
		return mapErr(err)
	}
	a.ID = uuidString(updated.ID)
	a.Disabled = updated.Status == "revoked" || updated.Status == "disabled"
	a.EnrolledAt = timeOrZero(updated.EnrolledAt)
	a.CreatedAt = timeOrZero(updated.CreatedAt)
	a.UpdatedAt = timeOrZero(updated.UpdatedAt)
	return nil
}

// GetAgent returns one agent by ID.
func (r *AgentRepo) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	aid, err := mustParseUUID(id, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetAgentByID(ctx, gen.GetAgentByIDParams{ID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	a, err := mapAgent(row)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAgent rewrites the mutable agent fields, preserving liveness
// status except through the Disabled flag.
func (r *AgentRepo) UpdateAgent(ctx context.Context, a *domain.Agent) error {
	if a == nil {
		return validationError("agent is required")
	}
	if err := a.Validate(); err != nil {
		return err
	}
	aid, err := mustParseUUID(a.ID, "agent id")
	if err != nil {
		return err
	}
	caps, err := encodeJSON(nonNilStrings(a.Capabilities))
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	current, err := r.q.GetAgentByID(ctx, gen.GetAgentByIDParams{ID: aid})
	if err != nil {
		return mapErr(err)
	}
	meta, err := mergeAgentMetadata(current.Metadata, a.DiskTotalBytes, a.DiskFreeBytes)
	if err != nil {
		return err
	}
	_, err = r.q.UpdateAgent(ctx, gen.UpdateAgentParams{
		ID: aid, Name: a.Hostname, Hostname: a.Hostname,
		IpAddress: a.IPAddress, Os: a.OS, Arch: a.Architecture,
		AgentVersion: a.Version, Status: agentStatusForUpdate(current.Status, a.Disabled),
		Capabilities: caps, Metadata: meta,
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// ListAgents returns one page of agents filtered by hostname/name.
func (r *AgentRepo) ListAgents(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.Agent], error) {
	limit, offset := page(opts)
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.SearchAgents(ctx, gen.SearchAgentsParams{
		Column1: opts.Query,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return domain.ListResult[domain.Agent]{}, mapErr(err)
	}
	total, err := r.q.CountSearchAgents(ctx, gen.CountSearchAgentsParams{Column1: opts.Query})
	if err != nil {
		return domain.ListResult[domain.Agent]{}, mapErr(err)
	}
	out := make([]domain.Agent, 0, len(rows))
	for _, row := range rows {
		a, err := mapAgent(row)
		if err != nil {
			return domain.ListResult[domain.Agent]{}, err
		}
		out = append(out, a)
	}
	return domain.ListResult[domain.Agent]{Items: out, Total: int(total)}, nil
}

// StoreCredential persists an agent credential hash. AgentID is required
// (see mapping notes); only the hash is stored, never plaintext.
func (r *AgentRepo) StoreCredential(ctx context.Context, c *domain.AgentCredential) error {
	if c == nil {
		return validationError("credential is required")
	}
	if c.AgentID == "" {
		return validationError("credential agent_id is required")
	}
	if c.TokenHash == "" {
		return validationError("credential hash is required")
	}
	aid, err := mustParseUUID(c.AgentID, "agent id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateAgentCredential(ctx, gen.CreateAgentCredentialParams{
		AgentID:        aid,
		CredentialHash: c.TokenHash,
		Label:          credentialLabel(c.Enrollment),
		ExpiresAt:      timestamptzPtr(c.ExpiresAt),
	})
	if err != nil {
		return mapErr(err)
	}
	if c.Revoked {
		if _, err := r.q.UpdateAgentCredential(ctx, gen.UpdateAgentCredentialParams{
			ID: row.ID, Label: row.Label, ExpiresAt: row.ExpiresAt,
			Column4: true, LastUsedAt: row.LastUsedAt,
		}); err != nil {
			return mapErr(err)
		}
	}
	c.ID = uuidString(row.ID)
	c.CreatedAt = timeOrZero(row.CreatedAt)
	return nil
}

// GetCredentialByHash resolves a credential by its hash for authentication.
func (r *AgentRepo) GetCredentialByHash(ctx context.Context, hash string) (*domain.AgentCredential, error) {
	if hash == "" {
		return nil, notFound("credential")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetCredentialByHash(ctx, gen.GetCredentialByHashParams{CredentialHash: hash})
	if err != nil {
		return nil, mapErr(err)
	}
	c := mapCredential(row)
	return &c, nil
}

// ListAgentCredentials returns all credentials (including revoked) for an agent.
func (r *AgentRepo) ListAgentCredentials(ctx context.Context, agentID string) ([]domain.AgentCredential, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListAgentCredentials(ctx, gen.ListAgentCredentialsParams{AgentID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.AgentCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapCredential(row))
	}
	return out, nil
}

// UpdateCredential rewrites label/expiry/revocation state. A nil LastUsedAt
// preserves the stored value.
func (r *AgentRepo) UpdateCredential(ctx context.Context, c *domain.AgentCredential) error {
	if c == nil {
		return validationError("credential is required")
	}
	cid, err := mustParseUUID(c.ID, "credential id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateAgentCredential(ctx, gen.UpdateAgentCredentialParams{
		ID:         cid,
		Label:      credentialLabel(c.Enrollment),
		ExpiresAt:  timestamptzPtr(c.ExpiresAt),
		Column4:    c.Revoked,
		LastUsedAt: timestamptzPtr(c.LastUsedAt),
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// RecordHeartbeat stores a heartbeat and refreshes the agent's liveness
// pointer plus denormalized disk facts, atomically. ReportedAt is
// server-stamped from received_at.
func (r *AgentRepo) RecordHeartbeat(ctx context.Context, h *domain.AgentHeartbeat) error {
	if h == nil {
		return validationError("heartbeat is required")
	}
	aid, err := mustParseUUID(h.AgentID, "agent id")
	if err != nil {
		return err
	}
	if h.RunningJobs < 0 {
		return validationError("running jobs must be non-negative")
	}
	payload, err := encodeJSON(heartbeatPayload{DockerAvailable: h.DockerAvailable})
	if err != nil {
		return err
	}
	stats := mustEncodeJSON(agentMetadata{DiskTotalBytes: h.DiskTotalBytes, DiskFreeBytes: h.DiskFreeBytes})
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var id string
	err = r.inTx(ctx, func(q *gen.Queries) error {
		row, err := q.RecordHeartbeat(ctx, gen.RecordHeartbeatParams{
			AgentID:        aid,
			Status:         "online",
			AgentVersion:   h.Version,
			IpAddress:      h.IPAddress,
			DiskTotalBytes: uint64OrZero(h.DiskTotalBytes),
			DiskFreeBytes:  uint64OrZero(h.DiskFreeBytes),
			RunningJobs:    int32OrZero(h.RunningJobs),
			Payload:        payload,
		})
		if err != nil {
			return err
		}
		id = uuidString(row.ID)
		h.ReportedAt = timeOrZero(row.ReceivedAt)
		if err := q.UpdateAgentHeartbeatPointer(ctx, gen.UpdateAgentHeartbeatPointerParams{
			ID: aid, IpAddress: h.IPAddress, AgentVersion: h.Version,
		}); err != nil {
			return err
		}
		return q.UpdateAgentHeartbeatStats(ctx, gen.UpdateAgentHeartbeatStatsParams{
			ID: aid, Column2: stats,
		})
	})
	if err != nil {
		return mapErr(err)
	}
	h.ID = id
	return nil
}

// LatestHeartbeat returns the most recent heartbeat for an agent.
func (r *AgentRepo) LatestHeartbeat(ctx context.Context, agentID string) (*domain.AgentHeartbeat, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetLatestHeartbeat(ctx, gen.GetLatestHeartbeatParams{AgentID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	h, err := mapHeartbeat(row)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func mapAgent(row gen.Agent) (domain.Agent, error) {
	var meta agentMetadata
	if err := decodeJSON(row.Metadata, &meta); err != nil {
		return domain.Agent{}, err
	}
	return domain.Agent{
		ID:              uuidString(row.ID),
		Hostname:        row.Hostname,
		IPAddress:       row.IpAddress,
		OS:              row.Os,
		Architecture:    row.Arch,
		Version:         row.AgentVersion,
		Capabilities:    stringSlice(row.Capabilities),
		Disabled:        row.Status == "revoked" || row.Status == "disabled",
		EnrolledAt:      timeOrZero(row.EnrolledAt),
		LastHeartbeatAt: timePtr(row.LastHeartbeatAt),
		DiskTotalBytes:  meta.DiskTotalBytes,
		DiskFreeBytes:   meta.DiskFreeBytes,
		CreatedAt:       timeOrZero(row.CreatedAt),
		UpdatedAt:       timeOrZero(row.UpdatedAt),
	}, nil
}

// agentStatusForUpdate preserves liveness except through Disabled.
func agentStatusForUpdate(current string, disabled bool) string {
	if disabled {
		return "revoked"
	}
	if current == "revoked" || current == "disabled" {
		return "offline"
	}
	if current == "" {
		return "pending"
	}
	return current
}

// mergeAgentMetadata overlays fresh disk facts onto stored metadata.
func mergeAgentMetadata(stored []byte, total, free *uint64) ([]byte, error) {
	var meta agentMetadata
	if err := decodeJSON(stored, &meta); err != nil {
		return nil, err
	}
	meta.DiskTotalBytes = total
	meta.DiskFreeBytes = free
	return encodeJSON(meta)
}

func credentialLabel(enrollment bool) string {
	if enrollment {
		return enrollmentLabel
	}
	return ""
}

func mapCredential(row gen.AgentCredential) domain.AgentCredential {
	return domain.AgentCredential{
		ID:         uuidString(row.ID),
		AgentID:    uuidString(row.AgentID),
		TokenHash:  row.CredentialHash,
		Enrollment: row.Label == enrollmentLabel,
		Revoked:    row.RevokedAt.Valid,
		ExpiresAt:  timePtr(row.ExpiresAt),
		LastUsedAt: timePtr(row.LastUsedAt),
		CreatedAt:  timeOrZero(row.CreatedAt),
	}
}

func mapHeartbeat(row gen.AgentHeartbeat) (domain.AgentHeartbeat, error) {
	var payload heartbeatPayload
	if err := decodeJSON(row.Payload, &payload); err != nil {
		return domain.AgentHeartbeat{}, err
	}
	return domain.AgentHeartbeat{
		ID:              uuidString(row.ID),
		AgentID:         uuidString(row.AgentID),
		ReportedAt:      timeOrZero(row.ReceivedAt),
		Version:         row.AgentVersion,
		IPAddress:       row.IpAddress,
		DiskTotalBytes:  uint64Ptr(row.DiskTotalBytes),
		DiskFreeBytes:   uint64Ptr(row.DiskFreeBytes),
		RunningJobs:     int(row.RunningJobs),
		DockerAvailable: payload.DockerAvailable,
	}, nil
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
