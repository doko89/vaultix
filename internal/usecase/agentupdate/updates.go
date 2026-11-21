package agentupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

// Directive is what the poll response advertises: the exact bytes the
// agent must download, verify, and swap to.
type Directive struct {
	Version   string
	GOOS      string
	GOARCH    string
	SHA256    []byte
	SizeBytes int64
}

// Info describes update availability for one agent (UI button state).
type Info struct {
	// Latest is the newest artifact for the agent's platform (nil when
	// none). Available is true when Latest outranks the agent version.
	Latest    *domain.AgentArtifact
	Available bool
	// Pending is the agent's update slot (nil when never targeted).
	Pending *domain.AgentUpdate
}

// UploadArtifact stores one agent binary release. The version+platform
// triple must be unique; a re-upload of the same triple conflicts.
func (s *Service) UploadArtifact(ctx context.Context, version, goos, goarch string, data []byte, by string) (*domain.AgentArtifact, error) {
	version = strings.TrimSpace(version)
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)
	if version == "" || goos == "" || goarch == "" {
		return nil, fmt.Errorf("%w: version, goos, and goarch are required", domain.ErrValidation)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: artifact bytes are required", domain.ErrValidation)
	}
	if len(data) > MaxArtifactBytes {
		return nil, fmt.Errorf("%w: artifact exceeds %d bytes", domain.ErrValidation, MaxArtifactBytes)
	}
	sum := sha256.Sum256(data)
	a := &domain.AgentArtifact{
		Version: version, GOOS: goos, GOARCH: goarch,
		SHA256: sum[:], SizeBytes: int64(len(data)),
		Bytes: append([]byte(nil), data...), UploadedBy: strings.TrimSpace(by),
	}
	if err := s.updates.CreateArtifact(ctx, a); err != nil {
		return nil, err
	}
	out := *a
	out.Bytes = nil
	return &out, nil
}

// ListArtifacts returns artifact metadata newest-first (no bytes).
func (s *Service) ListArtifacts(ctx context.Context) ([]domain.AgentArtifact, error) {
	return s.updates.ListArtifacts(ctx)
}

// DeleteArtifact removes one artifact. Pending slots targeting it resolve
// to a failed "artifact removed" state on next poll instead of bricking.
func (s *Service) DeleteArtifact(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: artifact id is required", domain.ErrValidation)
	}
	return s.updates.DeleteArtifact(ctx, id)
}

// RequestUpdate targets the agent at one explicit artifact. The artifact
// must outrank the agent's reported version, and when the agent's
// platform is known it must match (refusing otherwise keeps an amd64
// binary off an arm64 box). Unknown platforms are the admin's call:
// they picked the artifact explicitly.
func (s *Service) RequestUpdate(ctx context.Context, agentID, artifactID, by string) (*domain.AgentUpdate, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	if strings.TrimSpace(artifactID) == "" {
		return nil, fmt.Errorf("%w: artifact id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if ag.Disabled {
		return nil, fmt.Errorf("%w: revoked agents cannot be updated", domain.ErrConflict)
	}
	art, err := s.updates.GetArtifact(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if agentuc.CompareVersions(ag.Version, art.Version) >= 0 {
		return nil, fmt.Errorf("%w: agent already runs %q (target %q is not newer)",
			domain.ErrConflict, ag.Version, art.Version)
	}
	if ag.OS != "" && !strings.EqualFold(ag.OS, art.GOOS) {
		return nil, fmt.Errorf("%w: artifact is %s/%s but agent reports %s/%s",
			domain.ErrConflict, art.GOOS, art.GOARCH, ag.OS, ag.Architecture)
	}
	if ag.Architecture != "" && !strings.EqualFold(ag.Architecture, art.GOARCH) {
		return nil, fmt.Errorf("%w: artifact is %s/%s but agent reports %s/%s",
			domain.ErrConflict, art.GOOS, art.GOARCH, ag.OS, ag.Architecture)
	}
	return s.updates.UpsertUpdate(ctx, agentID, art.Version, art.GOOS, art.GOARCH, strings.TrimSpace(by))
}

// PendingDirective resolves the agent's pending slot to downloadable
// bytes metadata, or (nil, nil) when nothing is due. A pending slot
// whose artifact was deleted self-heals to failed instead of hanging.
func (s *Service) PendingDirective(ctx context.Context, agentID string) (*Directive, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	slot, err := s.updates.GetUpdate(ctx, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if slot.Status != domain.AgentUpdatePending {
		return nil, nil
	}
	art, err := s.findArtifact(ctx, slot.TargetVersion, slot.GOOS, slot.GOARCH)
	if err != nil {
		return nil, err
	}
	if art == nil {
		msg := fmt.Sprintf("artifact %s/%s/%s was removed", slot.TargetVersion, slot.GOOS, slot.GOARCH)
		if err := s.updates.SetUpdateStatus(ctx, agentID, domain.AgentUpdateFailed, msg); err != nil {
			return nil, fmt.Errorf("agentupdate: fail orphaned update: %w", err)
		}
		return nil, nil
	}
	// The agent may have updated out of band (manual swap): a pending
	// target that no longer outranks the reported version is done.
	ag, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agentuc.CompareVersions(ag.Version, slot.TargetVersion) >= 0 {
		if err := s.updates.SetUpdateStatus(ctx, agentID, domain.AgentUpdateApplied, ""); err != nil {
			return nil, fmt.Errorf("agentupdate: settle out-of-band update: %w", err)
		}
		return nil, nil
	}
	return &Directive{
		Version: art.Version, GOOS: art.GOOS, GOARCH: art.GOARCH,
		SHA256: append([]byte(nil), art.SHA256...), SizeBytes: art.SizeBytes,
	}, nil
}

// DownloadArtifact returns the pending target's bytes for the agent's own
// download call. NotFound when nothing is due (or the bytes are gone).
func (s *Service) DownloadArtifact(ctx context.Context, agentID string) (*domain.AgentArtifact, error) {
	dir, err := s.PendingDirective(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return nil, fmt.Errorf("%w: no pending update", domain.ErrNotFound)
	}
	arts, err := s.updates.ListPlatformArtifacts(ctx, dir.GOOS, dir.GOARCH)
	if err != nil {
		return nil, err
	}
	for _, a := range arts {
		if a.Version == dir.Version {
			return s.updates.GetArtifact(ctx, a.ID)
		}
	}
	return nil, fmt.Errorf("%w: no pending update", domain.ErrNotFound)
}

// ReportUpdate records the agent's applied/failed report. Stale reports
// (a target that was since retargeted) are ignored, never clobbering.
func (s *Service) ReportUpdate(ctx context.Context, agentID, status, version, errMsg string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	if status != domain.AgentUpdateApplied && status != domain.AgentUpdateFailed {
		return fmt.Errorf("%w: status must be applied or failed", domain.ErrValidation)
	}
	slot, err := s.updates.GetUpdate(ctx, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	if slot.TargetVersion != strings.TrimSpace(version) {
		return nil
	}
	return s.updates.SetUpdateStatus(ctx, agentID, status, strings.TrimSpace(errMsg))
}

// Describe returns update availability for one agent (UI state).
func (s *Service) Describe(ctx context.Context, agentID string) (*Info, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	ag, err := s.agents.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	info := &Info{}
	arts, err := s.updates.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	latest := newestFor(arts, ag.OS, ag.Architecture)
	if latest == nil && (ag.OS != "" || ag.Architecture != "") {
		// Platform-reporting agents with no platform build: fall back to
		// the newest artifact of any platform so the UI can still offer
		// it (RequestUpdate refuses true mismatches).
		latest = newestFor(arts, "", "")
	}
	if latest != nil {
		cp := *latest
		cp.Bytes = nil
		info.Latest = &cp
		info.Available = !ag.Disabled && agentuc.CompareVersions(ag.Version, latest.Version) < 0
	}
	slot, err := s.updates.GetUpdate(ctx, agentID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	} else {
		info.Pending = slot
	}
	return info, nil
}

// BatchResult is one agent's batch-update outcome: requested (slot
// targeted), skipped (already current or no build), or failed.
type BatchResult struct {
	AgentID       string
	TargetVersion string
	Status        string
	Error         string
}

// Batch statuses.
const (
	BatchRequested = "requested"
	BatchSkipped   = "skipped"
	BatchFailed    = "failed"
)

// maxBatchSize caps one batch request.
const maxBatchSize = 200

// BatchRequestUpdate targets every listed agent at its latest cached
// build. Per-agent failures never abort the batch: each agent reports
// its own outcome.
func (s *Service) BatchRequestUpdate(ctx context.Context, agentIDs []string, by string) ([]BatchResult, error) {
	if len(agentIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one agent id is required", domain.ErrValidation)
	}
	if len(agentIDs) > maxBatchSize {
		return nil, fmt.Errorf("%w: at most %d agents per batch", domain.ErrValidation, maxBatchSize)
	}
	seen := map[string]bool{}
	out := make([]BatchResult, 0, len(agentIDs))
	for _, id := range agentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, s.batchOne(ctx, id, by))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: at least one agent id is required", domain.ErrValidation)
	}
	return out, nil
}

func (s *Service) batchOne(ctx context.Context, agentID, by string) BatchResult {
	info, err := s.Describe(ctx, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return BatchResult{AgentID: agentID, Status: BatchFailed, Error: "agent not found"}
		}
		return BatchResult{AgentID: agentID, Status: BatchFailed, Error: err.Error()}
	}
	if !info.Available || info.Latest == nil {
		return BatchResult{AgentID: agentID, Status: BatchSkipped, Error: "already up to date"}
	}
	slot, err := s.RequestUpdate(ctx, agentID, info.Latest.ID, by)
	if err != nil {
		return BatchResult{AgentID: agentID, Status: BatchFailed, Error: err.Error()}
	}
	return BatchResult{AgentID: agentID, TargetVersion: slot.TargetVersion, Status: BatchRequested}
}

// findArtifact locates one version+platform build, or (nil, nil).
func (s *Service) findArtifact(ctx context.Context, version, goos, goarch string) (*domain.AgentArtifact, error) {
	arts, err := s.updates.ListPlatformArtifacts(ctx, goos, goarch)
	if err != nil {
		return nil, err
	}
	for i := range arts {
		if arts[i].Version == version {
			return &arts[i], nil
		}
	}
	return nil, nil
}

// newestFor picks the highest-versioned artifact, optionally confined to
// one platform (empty goos+goarch matches everything).
func newestFor(arts []domain.AgentArtifact, goos, goarch string) *domain.AgentArtifact {
	var best *domain.AgentArtifact
	for i := range arts {
		a := &arts[i]
		if goos != "" && !strings.EqualFold(a.GOOS, goos) {
			continue
		}
		if goarch != "" && !strings.EqualFold(a.GOARCH, goarch) {
			continue
		}
		if best == nil || agentuc.CompareVersions(best.Version, a.Version) < 0 {
			best = a
		}
	}
	return best
}
