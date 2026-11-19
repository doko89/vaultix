package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Service ingests and serves discovered-resource inventory (PRD §7.3). It
// depends only on domain ports and writes only to the discovery repository:
// ingest can never create policies or protection.
type Service struct {
	discovery domain.DiscoveryRepository
	agents    domain.AgentRepository
	clock     domain.Clock
	newID     func() (string, error)
}

// Option tunes a Service.
type Option func(*Service)

// WithIDGenerator overrides secure ID generation (tests only).
func WithIDGenerator(fn func() (string, error)) Option {
	return func(s *Service) {
		if fn != nil {
			s.newID = fn
		}
	}
}

// NewService wires discovery. A nil clock defaults to the system clock;
// repositories are required.
func NewService(discovery domain.DiscoveryRepository, agents domain.AgentRepository, clock domain.Clock, opts ...Option) (*Service, error) {
	if discovery == nil {
		return nil, fmt.Errorf("discovery: DiscoveryRepository is required")
	}
	if agents == nil {
		return nil, fmt.Errorf("discovery: AgentRepository is required")
	}
	if clock == nil {
		clock = domain.SystemClock{}
	}
	s := &Service{discovery: discovery, agents: agents, clock: clock, newID: defaultID}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func defaultID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("discovery: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidKind reports whether k is a known resource kind.
func ValidKind(k domain.ResourceKind) bool {
	switch k {
	case domain.ResourceKindHostPath, domain.ResourceKindMount,
		domain.ResourceKindDockerContainer, domain.ResourceKindDockerVolume,
		domain.ResourceKindDockerBindMount, domain.ResourceKindComposeProject,
		domain.ResourceKindPostgresDatabase, domain.ResourceKindMySQLDatabase,
		domain.ResourceKindMSSQLDatabase, domain.ResourceKindRedisDataset:
		return true
	default:
		return false
	}
}

// ResourceInput is one validated inventory entry: host path/mount, container
// (id, name, image, state, mounts, volumes), named volume, compose metadata,
// or labels — all carried in Detail.
type ResourceInput struct {
	Kind   domain.ResourceKind
	Name   string
	Detail map[string]string
}

// Ingest replaces the agent's inventory snapshot with the reported resources.
// An empty list clears the snapshot (the agent sees nothing). Ingest is
// informational only: it never creates policies, schedules, or jobs.
func (s *Service) Ingest(ctx context.Context, agentID string, resources []ResourceInput) (int, error) {
	if strings.TrimSpace(agentID) == "" {
		return 0, fmt.Errorf("%w: agent_id is required", domain.ErrValidation)
	}
	if _, err := s.agents.GetAgent(ctx, agentID); err != nil {
		return 0, err
	}
	now := s.clock.Now().UTC()
	built := make([]domain.DiscoveredResource, 0, len(resources))
	for i, in := range resources {
		if !ValidKind(in.Kind) {
			return 0, fmt.Errorf("%w: resources[%d]: unknown kind %q", domain.ErrValidation, i, in.Kind)
		}
		id, err := s.newID()
		if err != nil {
			return 0, err
		}
		r := domain.DiscoveredResource{
			ID:           id,
			AgentID:      agentID,
			Kind:         in.Kind,
			Name:         strings.TrimSpace(in.Name),
			Detail:       cloneDetail(in.Detail),
			DiscoveredAt: now,
			LastSeenAt:   now,
		}
		if err := r.Validate(); err != nil {
			return 0, fmt.Errorf("resources[%d]: %w", i, err)
		}
		built = append(built, r)
	}
	if err := s.discovery.ReplaceAgentResources(ctx, agentID, built); err != nil {
		return 0, fmt.Errorf("discovery: replace inventory: %w", err)
	}
	return len(built), nil
}

// List returns the agent's inventory, optionally narrowed to one kind.
// Empty kind returns everything.
func (s *Service) List(ctx context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("%w: agent_id is required", domain.ErrValidation)
	}
	if kind != "" && !ValidKind(kind) {
		return nil, fmt.Errorf("%w: unknown kind %q", domain.ErrValidation, kind)
	}
	if _, err := s.agents.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	return s.discovery.ListAgentResources(ctx, agentID, kind)
}

// Get returns one resource by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.DiscoveredResource, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: resource id is required", domain.ErrValidation)
	}
	return s.discovery.GetResource(ctx, id)
}

func cloneDetail(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
