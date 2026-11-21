package agentupdate

import (
	"fmt"

	"github.com/vaultix/vaultix/internal/domain"
)

// MaxArtifactBytes caps one uploaded agent binary (release binaries are a
// few tens of MB; the HTTP layer enforces the same cap while reading).
const MaxArtifactBytes = 64 << 20

// Service implements manager-provisioned agent updates: it stores
// manager-hosted agent binaries and drives the per-agent pending-update
// slot that the poll response advertises. It depends only on domain
// ports; adapters supply the repositories.
type Service struct {
	updates domain.AgentUpdateRepository
	agents  domain.AgentRepository
	source  ReleaseSource
}

// NewService wires the update service. Repositories are required; the
// release source is optional (WithReleaseSource enables SyncReleases).
func NewService(updates domain.AgentUpdateRepository, agents domain.AgentRepository, opts ...func(*Service)) (*Service, error) {
	if updates == nil {
		return nil, fmt.Errorf("agentupdate: AgentUpdateRepository is required")
	}
	if agents == nil {
		return nil, fmt.Errorf("agentupdate: AgentRepository is required")
	}
	s := &Service{updates: updates, agents: agents}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}
