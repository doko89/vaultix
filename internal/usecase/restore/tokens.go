package restore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ConfirmationToken binds one plan to one execution. Tokens are single-use
// and expire; Execute consumes them.
type ConfirmationToken struct {
	Token     string
	PlanID    string
	IssuedTo  string
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// Expired reports whether the token is past its deadline.
func (t *ConfirmationToken) Expired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// Used reports whether the token was already consumed.
func (t *ConfirmationToken) Used() bool { return t.UsedAt != nil }

// TokenStore persists confirmation tokens.
type TokenStore interface {
	SaveToken(ctx context.Context, t *ConfirmationToken) error
	GetToken(ctx context.Context, token string) (*ConfirmationToken, error)
	MarkTokenUsed(ctx context.Context, token string, at time.Time) error
}

// MemoryTokenStore is an in-process TokenStore for single-manager
// deployments and tests. It evicts expired tokens opportunistically.
type MemoryTokenStore struct {
	mu     sync.Mutex
	tokens map[string]*ConfirmationToken
}

// NewMemoryTokenStore builds an empty in-process token store.
func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{tokens: map[string]*ConfirmationToken{}}
}

// SaveToken stores a new token.
func (m *MemoryTokenStore) SaveToken(_ context.Context, t *ConfirmationToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	m.tokens[t.Token] = &cp
	return nil
}

// GetToken loads a token by value.
func (m *MemoryTokenStore) GetToken(_ context.Context, token string) (*ConfirmationToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[token]
	if !ok {
		return nil, ErrTokenInvalid
	}
	cp := *t
	return &cp, nil
}

// MarkTokenUsed records single-use consumption.
func (m *MemoryTokenStore) MarkTokenUsed(_ context.Context, token string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[token]
	if !ok {
		return ErrTokenInvalid
	}
	cp := *t
	cp.UsedAt = &at
	m.tokens[token] = &cp
	return nil
}

// MemoryPlanStore is an in-process PlanStore for single-manager
// deployments and tests.
type MemoryPlanStore struct {
	mu    sync.Mutex
	plans map[string]*Plan
}

// NewMemoryPlanStore builds an empty in-process plan store.
func NewMemoryPlanStore() *MemoryPlanStore {
	return &MemoryPlanStore{plans: map[string]*Plan{}}
}

// SavePlan stores a plan.
func (m *MemoryPlanStore) SavePlan(_ context.Context, p *Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *p
	m.plans[p.ID] = &cp
	return nil
}

// GetPlan loads a plan by ID.
func (m *MemoryPlanStore) GetPlan(_ context.Context, id string) (*Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	if !ok {
		return nil, ErrPlanNotFound
	}
	cp := *p
	return &cp, nil
}

// randomToken returns a 256-bit hex token.
func randomToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
