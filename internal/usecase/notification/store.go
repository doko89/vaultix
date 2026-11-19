package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/vaultix/vaultix/internal/domain"
)

// StaticRuleStore is a concurrency-safe in-memory RuleStore. Production
// wiring builds it from manager config (rules: [...] in YAML); it survives
// only for the process lifetime, which is acceptable until the repos track
// provides a DB-backed store (see the RuleStore INTEGRATION NOTE).
type StaticRuleStore struct {
	mu    sync.RWMutex
	rules map[string]Rule
	order []string
}

// NewStaticRuleStore returns a store preloaded with seed (IDs assigned when
// empty). Seed rules are validated; invalid entries are rejected.
func NewStaticRuleStore(seed []Rule) (*StaticRuleStore, error) {
	s := &StaticRuleStore{rules: make(map[string]Rule)}
	for _, r := range seed {
		if _, err := s.CreateRule(context.Background(), r); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// CreateRule validates, assigns an ID when empty, and stores the rule.
func (s *StaticRuleStore) CreateRule(_ context.Context, r Rule) (Rule, error) {
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	if r.ID == "" {
		r.ID = newRuleID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.rules[r.ID]; dup {
		return Rule{}, fmt.Errorf("%w: rule %q already exists", domain.ErrConflict, r.ID)
	}
	s.rules[r.ID] = r
	s.order = append(s.order, r.ID)
	return r, nil
}

// GetRule returns the rule by ID.
func (s *StaticRuleStore) GetRule(_ context.Context, id string) (Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[id]
	if !ok {
		return Rule{}, fmt.Errorf("%w: rule %q", domain.ErrNotFound, id)
	}
	return r, nil
}

// ListRules returns rules in creation order.
func (s *StaticRuleStore) ListRules(_ context.Context) ([]Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.rules[id])
	}
	return out, nil
}

// UpdateRule replaces the rule body, preserving the ID.
func (s *StaticRuleStore) UpdateRule(_ context.Context, id string, r Rule) (Rule, error) {
	r.ID = id
	if err := r.Validate(); err != nil {
		return Rule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return Rule{}, fmt.Errorf("%w: rule %q", domain.ErrNotFound, id)
	}
	s.rules[id] = r
	return r, nil
}

// DeleteRule removes the rule.
func (s *StaticRuleStore) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("%w: rule %q", domain.ErrNotFound, id)
	}
	delete(s.rules, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

func newRuleID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("notification: crypto/rand unavailable: " + err.Error())
	}
	return "rule-" + hex.EncodeToString(raw[:])
}
