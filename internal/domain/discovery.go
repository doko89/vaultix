package domain

import (
	"fmt"
	"strings"
	"time"
)

// ResourceKind classifies a discovered resource (PRD §7.3).
type ResourceKind string

const (
	ResourceKindHostPath         ResourceKind = "host_path"
	ResourceKindMount            ResourceKind = "mount"
	ResourceKindDockerContainer  ResourceKind = "docker_container"
	ResourceKindDockerVolume     ResourceKind = "docker_volume"
	ResourceKindDockerBindMount  ResourceKind = "docker_bind_mount"
	ResourceKindComposeProject   ResourceKind = "compose_project"
	ResourceKindPostgresDatabase ResourceKind = "postgres_database"
	ResourceKindMySQLDatabase    ResourceKind = "mysql_database"
	ResourceKindMSSQLDatabase    ResourceKind = "mssql_database"
	ResourceKindRedisDataset     ResourceKind = "redis_dataset"
)

// DiscoveredResource is an informational inventory entry reported by an
// agent (PRD `discovered_resources`). Discovery never implies backup;
// only explicit policy targets are backed up.
type DiscoveredResource struct {
	ID      string
	AgentID string
	Kind    ResourceKind
	// Name is the display identifier (path, container name, volume name...).
	Name string
	// Detail carries kind-specific metadata as opaque key/value pairs
	// (container ID/image/state, digests, labels, mount points...).
	Detail       map[string]string
	DiscoveredAt time.Time
	LastSeenAt   time.Time
}

// Validate checks resource invariants.
func (r DiscoveredResource) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return fmt.Errorf("%w: agent_id is required", ErrValidation)
	}
	if r.Kind == "" {
		return fmt.Errorf("%w: kind is required", ErrValidation)
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	return nil
}
