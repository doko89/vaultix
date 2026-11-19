package domain

import (
	"fmt"
	"strings"
	"time"
)

// AgentStatus is the Manager-computed liveness of an agent.
type AgentStatus string

const (
	AgentStatusOnline  AgentStatus = "online"
	AgentStatusOffline AgentStatus = "offline"
	AgentStatusRevoked AgentStatus = "revoked"
	AgentStatusStale   AgentStatus = "stale" // heartbeat older than threshold but not revoked
)

// DefaultHeartbeatTimeout is the duration after the last heartbeat after
// which an agent is considered offline.
const DefaultHeartbeatTimeout = 5 * time.Minute

// Agent is a registered Vaultix Agent on a target VM (PRD §7.2).
type Agent struct {
	ID           string
	Hostname     string
	IPAddress    string
	OS           string
	Architecture string
	Version      string
	// Capabilities advertises agent features (e.g. docker, mssql).
	Capabilities []string

	Disabled   bool // admin-revoked; distinct from transient offline
	EnrolledAt time.Time

	// LastHeartbeatAt is nil when the agent never reported.
	LastHeartbeatAt *time.Time
	// System facts from the latest heartbeat (denormalized for listings).
	DiskTotalBytes *uint64
	DiskFreeBytes  *uint64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks agent invariants.
func (a Agent) Validate() error {
	if strings.TrimSpace(a.Hostname) == "" {
		return fmt.Errorf("%w: hostname is required", ErrValidation)
	}
	return nil
}

// Status derives liveness from Disabled and the last heartbeat.
func (a Agent) Status(now time.Time, timeout time.Duration) AgentStatus {
	if a.Disabled {
		return AgentStatusRevoked
	}
	if a.LastHeartbeatAt == nil {
		return AgentStatusOffline
	}
	if now.Sub(*a.LastHeartbeatAt) > timeout {
		return AgentStatusStale
	}
	return AgentStatusOnline
}

// HasCapability reports whether the agent advertised a capability.
func (a Agent) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// AgentCredential is an agent identity credential (PRD `agent_credentials`).
// Only hashes are stored; plaintext tokens exist only at issuance.
type AgentCredential struct {
	ID string
	// AgentID is empty for unclaimed enrollment tokens.
	AgentID    string
	TokenHash  string // hash of the enrollment/API token; never the plaintext
	Enrollment bool   // true until exchanged for an agent-bound credential
	Revoked    bool
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// Valid reports whether the credential may authenticate at now.
func (c AgentCredential) Valid(now time.Time) bool {
	if c.Revoked {
		return false
	}
	if c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
		return false
	}
	return true
}

// Revoke marks the credential revoked.
func (c *AgentCredential) Revoke() { c.Revoked = true }

// AgentHeartbeat is one heartbeat report (PRD `agent_heartbeats`).
type AgentHeartbeat struct {
	ID              string
	AgentID         string
	ReportedAt      time.Time
	Version         string
	IPAddress       string
	DiskTotalBytes  *uint64
	DiskFreeBytes   *uint64
	RunningJobs     int
	DockerAvailable bool
}
