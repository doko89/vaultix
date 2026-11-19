package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// StackRepo implements domain.StackRepository as a thread-safe in-memory
// store. The schema in scope has no application_stacks tables and
// migrations are frozen, so this interim implementation keeps stacks
// working per-process until a stacks migration lands — at which point it
// should be replaced by a pgx+sqlc implementation behind the same port
// without touching callers.
//
// IDs passed empty are generated (16 random bytes, hex); timestamps behave
// like database defaults (CreatedAt set once, UpdatedAt on every write).
type StackRepo struct {
	mu        sync.RWMutex
	stacks    map[string]*domain.ApplicationStack
	resources map[string][]domain.ApplicationStackResource
}

func newStackRepo() *StackRepo {
	return &StackRepo{
		stacks:    map[string]*domain.ApplicationStack{},
		resources: map[string][]domain.ApplicationStackResource{},
	}
}

// CreateStack inserts a stack.
func (r *StackRepo) CreateStack(ctx context.Context, s *domain.ApplicationStack) error {
	if s == nil {
		return validationError("stack is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ID == "" {
		s.ID = newStackID()
	}
	now := time.Now()
	s.CreatedAt = now
	s.UpdatedAt = now
	stored := *s
	r.stacks[s.ID] = &stored
	return nil
}

// GetStack returns one stack by ID.
func (r *StackRepo) GetStack(ctx context.Context, id string) (*domain.ApplicationStack, error) {
	if id == "" {
		return nil, notFound("stack")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	stored, ok := r.stacks[id]
	if !ok {
		return nil, notFound("stack")
	}
	out := *stored
	return &out, nil
}

// UpdateStack rewrites a stack.
func (r *StackRepo) UpdateStack(ctx context.Context, s *domain.ApplicationStack) error {
	if s == nil {
		return validationError("stack is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.stacks[s.ID]
	if !ok {
		return notFound("stack")
	}
	s.CreatedAt = stored.CreatedAt
	s.UpdatedAt = time.Now()
	updated := *s
	r.stacks[s.ID] = &updated
	return nil
}

// DeleteStack removes a stack and its resource membership.
func (r *StackRepo) DeleteStack(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.stacks[id]; !ok {
		return notFound("stack")
	}
	delete(r.stacks, id)
	delete(r.resources, id)
	return nil
}

// ListStacks returns an agent's stacks (all stacks when agentID is empty).
func (r *StackRepo) ListStacks(ctx context.Context, agentID string) ([]domain.ApplicationStack, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []domain.ApplicationStack{}
	for _, stored := range r.stacks {
		if agentID != "" && stored.AgentID != agentID {
			continue
		}
		out = append(out, *stored)
	}
	return out, nil
}

// SetStackResources replaces a stack's resource membership.
func (r *StackRepo) SetStackResources(ctx context.Context, stackID string, resources []domain.ApplicationStackResource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.stacks[stackID]; !ok {
		return notFound("stack")
	}
	stored := make([]domain.ApplicationStackResource, 0, len(resources))
	for _, res := range resources {
		res.StackID = stackID
		stored = append(stored, res)
	}
	r.resources[stackID] = stored
	return nil
}

// ListStackResources returns a stack's resource membership.
func (r *StackRepo) ListStackResources(ctx context.Context, stackID string) ([]domain.ApplicationStackResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.stacks[stackID]; !ok {
		return nil, notFound("stack")
	}
	out := make([]domain.ApplicationStackResource, 0, len(r.resources[stackID]))
	out = append(out, r.resources[stackID]...)
	return out, nil
}

func newStackID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(raw[:])
}
