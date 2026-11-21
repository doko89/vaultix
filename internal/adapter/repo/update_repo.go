package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// UpdateRepo implements domain.AgentUpdateRepository on
// agent_artifacts/agent_updates.
type UpdateRepo struct {
	baseRepo
}

func mapArtifact(row gen.AgentArtifact) domain.AgentArtifact {
	return domain.AgentArtifact{
		ID: uuidString(row.ID), Version: row.Version,
		GOOS: row.Goos, GOARCH: row.Goarch,
		SHA256:    append([]byte(nil), row.Sha256...),
		SizeBytes: row.SizeBytes, Bytes: append([]byte(nil), row.Bytes...),
		UploadedBy: row.UploadedBy, CreatedAt: timeOrZero(row.CreatedAt),
	}
}

func mapUpdate(row gen.AgentUpdate) domain.AgentUpdate {
	return domain.AgentUpdate{
		AgentID: uuidString(row.AgentID), TargetVersion: row.TargetVersion,
		GOOS: row.Goos, GOARCH: row.Goarch,
		Status: row.Status, Error: row.Error, RequestedBy: row.RequestedBy,
		RequestedAt: timeOrZero(row.RequestedAt), UpdatedAt: timeOrZero(row.UpdatedAt),
	}
}

// CreateArtifact stores one agent binary release.
func (r *UpdateRepo) CreateArtifact(ctx context.Context, a *domain.AgentArtifact) error {
	if a == nil {
		return validationError("artifact is required")
	}
	if err := a.Validate(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateAgentArtifact(ctx, gen.CreateAgentArtifactParams{
		Version: a.Version, Goos: a.GOOS, Goarch: a.GOARCH,
		Sha256: a.SHA256, SizeBytes: a.SizeBytes, Bytes: a.Bytes,
		UploadedBy: a.UploadedBy,
	})
	if err != nil {
		return mapErr(err)
	}
	*a = mapArtifact(row)
	return nil
}

// ListArtifacts returns artifact metadata newest-first (no bytes).
func (r *UpdateRepo) ListArtifacts(ctx context.Context) ([]domain.AgentArtifact, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListAgentArtifacts(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.AgentArtifact, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AgentArtifact{
			ID: uuidString(row.ID), Version: row.Version,
			GOOS: row.Goos, GOARCH: row.Goarch,
			SHA256:     append([]byte(nil), row.Sha256...),
			SizeBytes:  row.SizeBytes,
			UploadedBy: row.UploadedBy, CreatedAt: timeOrZero(row.CreatedAt),
		})
	}
	return out, nil
}

// ListPlatformArtifacts returns metadata for one platform, newest-first.
func (r *UpdateRepo) ListPlatformArtifacts(ctx context.Context, goos, goarch string) ([]domain.AgentArtifact, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListPlatformArtifacts(ctx, gen.ListPlatformArtifactsParams{Goos: goos, Goarch: goarch})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.AgentArtifact, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AgentArtifact{
			ID: uuidString(row.ID), Version: row.Version,
			GOOS: row.Goos, GOARCH: row.Goarch,
			SHA256:     append([]byte(nil), row.Sha256...),
			SizeBytes:  row.SizeBytes,
			UploadedBy: row.UploadedBy, CreatedAt: timeOrZero(row.CreatedAt),
		})
	}
	return out, nil
}

// GetArtifact returns one artifact including its bytes.
func (r *UpdateRepo) GetArtifact(ctx context.Context, id string) (*domain.AgentArtifact, error) {
	aid, err := mustParseUUID(id, "artifact id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetAgentArtifact(ctx, gen.GetAgentArtifactParams{ID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	a := mapArtifact(row)
	return &a, nil
}

// DeleteArtifact removes one artifact.
func (r *UpdateRepo) DeleteArtifact(ctx context.Context, id string) error {
	aid, err := mustParseUUID(id, "artifact id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	n, err := r.q.DeleteAgentArtifact(ctx, gen.DeleteAgentArtifactParams{ID: aid})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return notFound("artifact")
	}
	return nil
}

// UpsertUpdate (re)targets the agent's pending-update slot.
func (r *UpdateRepo) UpsertUpdate(ctx context.Context, agentID, targetVersion, goos, goarch, requestedBy string) (*domain.AgentUpdate, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.UpsertAgentUpdate(ctx, gen.UpsertAgentUpdateParams{
		AgentID: aid, TargetVersion: targetVersion, Goos: goos, Goarch: goarch,
		RequestedBy: requestedBy,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	u := mapUpdate(row)
	return &u, nil
}

// GetUpdate returns the agent's update slot, or NotFound when never targeted.
func (r *UpdateRepo) GetUpdate(ctx context.Context, agentID string) (*domain.AgentUpdate, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetAgentUpdate(ctx, gen.GetAgentUpdateParams{AgentID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	u := mapUpdate(row)
	return &u, nil
}

// SetUpdateStatus records an applied/failed terminal (or re-pending) state.
func (r *UpdateRepo) SetUpdateStatus(ctx context.Context, agentID, status, errMsg string) error {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.q.SetAgentUpdateStatus(ctx, gen.SetAgentUpdateStatusParams{
		AgentID: aid, Status: status, Error: errMsg,
	}))
}
