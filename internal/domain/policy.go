package domain

import (
	"fmt"
	"strings"
	"time"
)

// TargetType enumerates backup target types (PRD §7.4).
type TargetType string

const (
	TargetHostFilesystem TargetType = "host_filesystem"
	TargetDockerConfig   TargetType = "docker_config"
	TargetDockerVolume   TargetType = "docker_volume"
	TargetPostgres       TargetType = "postgres"
	TargetMySQL          TargetType = "mysql"
	TargetMSSQL          TargetType = "mssql"
	TargetRedis          TargetType = "redis"
	TargetAppStack       TargetType = "app_stack"
)

// Valid reports whether the target type is known.
func (t TargetType) Valid() bool {
	switch t {
	case TargetHostFilesystem, TargetDockerConfig, TargetDockerVolume,
		TargetPostgres, TargetMySQL, TargetMSSQL, TargetRedis, TargetAppStack:
		return true
	default:
		return false
	}
}

// BackupMethod selects the backup procedure for a target.
type BackupMethod string

const (
	// BackupMethodFull streams a complete copy (MVP default for all types).
	BackupMethodFull BackupMethod = "full"
	// BackupMethodLogicalDump uses database-native logical dumps
	// (pg_dump, mysqldump); the only acceptable MVP database method.
	BackupMethodLogicalDump BackupMethod = "logical_dump"
	// BackupMethodNativeFull uses SQL Server native full backup.
	BackupMethodNativeFull BackupMethod = "native_full"
	// BackupMethodRDPSnapshot backs up Redis RDB/AOF persistence artifacts.
	BackupMethodRDPSnapshot BackupMethod = "rdb_snapshot"
)

// Compression settings (PRD §7.11): zstd default, level configurable.
type Compression struct {
	Algorithm string // "zstd" (MVP); empty means zstd default
	Level     int    // 0 = default level
}

// Encryption settings (PRD §7.11): optional per policy, default disabled.
type Encryption struct {
	Enabled bool
	// KeyRef references the SecretProfile holding the data key.
	KeyRef string
}

// Validate checks encryption invariants.
func (e Encryption) Validate() error {
	if e.Enabled && strings.TrimSpace(e.KeyRef) == "" {
		return fmt.Errorf("%w: key reference is required when encryption is enabled", ErrValidation)
	}
	return nil
}

// BackupPolicy is the reusable, assignable backup definition (PRD §7.4).
// Jobs always execute against an immutable BackupPolicyVersion snapshot.
type BackupPolicy struct {
	ID          string
	Name        string
	Description string
	// AgentIDs assigns the policy to one or more agents.
	AgentIDs []string
	// StorageID selects the destination; empty means the default destination.
	StorageID   string
	Compression Compression
	Encryption  Encryption
	Enabled     bool
	// CurrentVersion is the latest published version number.
	CurrentVersion int
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Validate checks policy invariants.
func (p BackupPolicy) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: policy name is required", ErrValidation)
	}
	if len(p.AgentIDs) == 0 {
		return fmt.Errorf("%w: at least one agent assignment is required", ErrValidation)
	}
	if err := p.Encryption.Validate(); err != nil {
		return err
	}
	return nil
}

// BackupPolicyVersion is an immutable snapshot of a policy's effective
// configuration (PRD §7.4: policy edits must not rewrite history).
type BackupPolicyVersion struct {
	Version   int
	PolicyID  string
	CreatedBy string
	CreatedAt time.Time
	// Targets is the frozen target set executed by jobs on this version.
	Targets []BackupPolicyTarget
	// Schedule, retention, compression, and encryption are frozen here so
	// completed jobs keep their exact configuration.
	Schedule    Schedule
	Retention   RetentionRule
	Compression Compression
	Encryption  Encryption
	StorageID   string
	// Note explains why this version was published.
	Note string
}

// BackupPolicyTarget selects one resource for backup within a policy version.
type BackupPolicyTarget struct {
	ID       string
	PolicyID string
	Type     TargetType
	Method   BackupMethod
	// Selector identifies the resource: absolute path(s) for filesystem,
	// volume/container names for Docker, db name for databases.
	Selector string
	// Include/Exclude are glob patterns (filesystem targets).
	Include []string
	Exclude []string
	// Options carries method-specific settings (dump flags, symlink mode...).
	Options map[string]string
	// CredentialRef references the SecretProfile for database targets.
	CredentialRef string
}

// Validate checks target invariants.
func (t BackupPolicyTarget) Validate() error {
	if !t.Type.Valid() {
		return fmt.Errorf("%w: unknown target type %q", ErrValidation, t.Type)
	}
	if strings.TrimSpace(t.Selector) == "" {
		return fmt.Errorf("%w: target selector is required", ErrValidation)
	}
	switch t.Type {
	case TargetPostgres, TargetMySQL, TargetMSSQL:
		if strings.TrimSpace(t.CredentialRef) == "" {
			return fmt.Errorf("%w: database target %q requires a credential reference", ErrValidation, t.Selector)
		}
	}
	return nil
}

// RetentionRule keeps snapshots by age and/or count (PRD §7.14).
// Zero values mean "no bound" on that dimension; at least one bound or
// KeepMinimum must be set — retention must never silently delete the last
// known valid recovery point.
type RetentionRule struct {
	// KeepLastN keeps the N most recent snapshots per policy target.
	KeepLastN int
	// KeepDays keeps snapshots newer than this many days.
	KeepDays int
	// KeepMinimum floors deletion: never leave fewer than this many valid
	// recovery points unless explicitly overridden at execution time.
	KeepMinimum int
}

// Validate checks retention invariants.
func (r RetentionRule) Validate() error {
	if r.KeepLastN < 0 || r.KeepDays < 0 || r.KeepMinimum < 0 {
		return fmt.Errorf("%w: retention values must be non-negative", ErrValidation)
	}
	if r.KeepLastN == 0 && r.KeepDays == 0 {
		return fmt.Errorf("%w: retention needs at least one bound (keep_last_n or keep_days)", ErrValidation)
	}
	return nil
}

// Applies reports whether the rule prunes the snapshot with the given age
// rank (0 = newest) and age. Deletion additionally honors KeepMinimum at
// execution time.
func (r RetentionRule) Applies(rank int, age time.Duration) bool {
	if r.KeepLastN > 0 && rank >= r.KeepLastN {
		return true
	}
	if r.KeepDays > 0 && age >= time.Duration(r.KeepDays)*24*time.Hour {
		return true
	}
	return false
}
