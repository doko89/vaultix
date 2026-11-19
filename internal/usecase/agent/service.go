package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

const (
	// DefaultEnrollmentTTL applies when issue is called with ttl <= 0.
	DefaultEnrollmentTTL = 24 * time.Hour
	// MaxEnrollmentTTL caps enrollment token lifetime.
	MaxEnrollmentTTL = 7 * 24 * time.Hour
)

// Service implements the agent registry (PRD §7.2). It depends only on
// domain ports; adapters supply the repositories and clock.
type Service struct {
	agents           domain.AgentRepository
	policies         domain.PolicyRepository
	clock            domain.Clock
	minVersion       string
	heartbeatTimeout time.Duration
	newToken         func() (string, error)
	newID            func() (string, error)
}

// Option tunes a Service.
type Option func(*Service)

// WithMinVersion sets the minimum supported agent version. Agents reporting
// an older (or empty) version are flagged outdated. Empty disables the flag.
func WithMinVersion(v string) Option {
	return func(s *Service) { s.minVersion = strings.TrimSpace(v) }
}

// WithHeartbeatTimeout overrides domain.DefaultHeartbeatTimeout for presence
// derivation. Non-positive values keep the default.
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.heartbeatTimeout = d
		}
	}
}

// WithTokenGenerator overrides secure token generation (tests only).
func WithTokenGenerator(fn func() (string, error)) Option {
	return func(s *Service) {
		if fn != nil {
			s.newToken = fn
		}
	}
}

// WithIDGenerator overrides secure ID generation (tests only).
func WithIDGenerator(fn func() (string, error)) Option {
	return func(s *Service) {
		if fn != nil {
			s.newID = fn
		}
	}
}

// NewService wires the agent registry. A nil clock defaults to the system
// clock; repositories are required.
func NewService(agents domain.AgentRepository, policies domain.PolicyRepository, clock domain.Clock, opts ...Option) (*Service, error) {
	if agents == nil {
		return nil, fmt.Errorf("agent: AgentRepository is required")
	}
	if policies == nil {
		return nil, fmt.Errorf("agent: PolicyRepository is required")
	}
	if clock == nil {
		clock = domain.SystemClock{}
	}
	s := &Service{
		agents:           agents,
		policies:         policies,
		clock:            clock,
		heartbeatTimeout: domain.DefaultHeartbeatTimeout,
		newToken:         defaultToken,
		newID:            defaultID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// HashToken hashes a plaintext token for storage/lookup. Only hashes rest in
// the database; plaintext exists transiently at issuance and in transit.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func defaultToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func defaultID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
