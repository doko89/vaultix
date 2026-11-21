package domain

import (
	"fmt"
	"strings"
	"time"
)

// Agent update states for the per-agent pending-update slot.
const (
	AgentUpdatePending = "pending"
	AgentUpdateApplied = "applied"
	AgentUpdateFailed  = "failed"
)

// AgentArtifact is one manager-hosted agent binary release.
type AgentArtifact struct {
	ID         string
	Version    string
	GOOS       string
	GOARCH     string
	SHA256     []byte
	SizeBytes  int64
	Bytes      []byte
	UploadedBy string
	CreatedAt  time.Time
}

// Validate checks artifact invariants (bytes presence, not content).
func (a AgentArtifact) Validate() error {
	if strings.TrimSpace(a.Version) == "" {
		return fmt.Errorf("%w: artifact version is required", ErrValidation)
	}
	if strings.TrimSpace(a.GOOS) == "" || strings.TrimSpace(a.GOARCH) == "" {
		return fmt.Errorf("%w: artifact platform (goos/goarch) is required", ErrValidation)
	}
	if len(a.Bytes) == 0 {
		return fmt.Errorf("%w: artifact bytes are required", ErrValidation)
	}
	return nil
}

// AgentUpdate is one agent's pending-update slot.
type AgentUpdate struct {
	AgentID       string
	TargetVersion string
	GOOS          string
	GOARCH        string
	Status        string
	Error         string
	RequestedBy   string
	RequestedAt   time.Time
	UpdatedAt     time.Time
}
