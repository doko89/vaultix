package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Grant lifetime bounds for job-scoped presigned URLs.
const (
	// DefaultGrantTTL applies when issuance names no TTL.
	DefaultGrantTTL = 15 * time.Minute
	// MaxGrantTTL caps grant lifetime so agents hold least privilege.
	MaxGrantTTL = time.Hour
	// MaxSecretBytes caps a single secret payload (1 MiB).
	MaxSecretBytes = 1 << 20
)

// Service orchestrates destinations, secret profiles, and per-job grants.
// It programs against domain.StorageRepository, the ObjectStore port, and
// the SecretBox port; cmd wires the concrete adapters.
type Service struct {
	repo       domain.StorageRepository
	store      ObjectStore
	box        SecretBox
	clock      domain.Clock
	defaultTTL time.Duration
	maxTTL     time.Duration
	newID      func() string
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the clock (tests).
func WithClock(c domain.Clock) Option {
	return func(s *Service) { s.clock = c }
}

// WithGrantTTL overrides the default and maximum grant TTL.
func WithGrantTTL(def, max time.Duration) Option {
	return func(s *Service) {
		if def > 0 {
			s.defaultTTL = def
		}
		if max > 0 {
			s.maxTTL = max
		}
	}
}

// WithIDGenerator overrides ID generation (tests).
func WithIDGenerator(f func() string) Option {
	return func(s *Service) { s.newID = f }
}

// NewService builds a storage Service. repo is required; store may be nil
// when only metadata CRUD is exercised (grant/test/verify calls then fail
// with a descriptive error), and box may be nil when no secret writes occur.
func NewService(repo domain.StorageRepository, store ObjectStore, box SecretBox, opts ...Option) *Service {
	s := &Service{
		repo:       repo,
		store:      store,
		box:        box,
		clock:      domain.SystemClock{},
		defaultTTL: DefaultGrantTTL,
		maxTTL:     MaxGrantTTL,
		newID:      newID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
