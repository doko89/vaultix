package restore

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Kind selects the restore shape.
type Kind string

// Restore kinds (PRD §7.13).
const (
	KindFile     Kind = "file"
	KindDatabase Kind = "database"
	KindVolume   Kind = "volume"
	KindStack    Kind = "stack"
)

// Valid reports whether the kind is known.
func (k Kind) Valid() bool {
	switch k {
	case KindFile, KindDatabase, KindVolume, KindStack:
		return true
	default:
		return false
	}
}

// Database engines supported for restore planning (PRD §7.7).
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

// PlanRequest asks for a restore plan. Single-snapshot restores set
// SnapshotID; stack restores set SnapshotIDs plus an explicit Order.
type PlanRequest struct {
	Kind Kind
	// SnapshotID is the source for file/database/volume restores.
	SnapshotID string
	// SnapshotIDs are the stack members for stack restores.
	SnapshotIDs []string
	// Engine must match the snapshot target for database restores
	// ("postgres" or "mysql").
	Engine string
	// Paths limits a file restore to a subset; empty restores the whole scope.
	Paths []string
	// Destination is an alternate restore destination; empty restores in
	// place (with an overwrite warning). For databases this is an alternate
	// database name when TargetDB is empty.
	Destination string
	// TargetDB is an alternate database name for database restores.
	TargetDB string
	// Order is the explicit snapshot-ID sequence for stack restores.
	Order []string
	// RequestedBy records the planning actor for the audit trail.
	RequestedBy string
}

// Step is one ordered restore action within a plan.
type Step struct {
	Sequence int
	Kind     Kind
	// SnapshotID is the source snapshot for this step.
	SnapshotID string
	// Source is the original path, volume, or database name.
	Source string
	// Destination is the resolved target (original or alternate).
	Destination string
	Notes       []string
}

// Plan is an authorized-shape restore proposal. It authorizes nothing by
// itself: Confirm binds it to a single-use token, Execute consumes it.
type Plan struct {
	ID          string
	Kind        Kind
	AgentID     string
	SnapshotIDs []string
	Steps       []Step
	// Warnings must be shown before confirmation (overwrite, downtime, ...).
	Warnings []string
	// Compatibility carries database restore requirements and constraints.
	Compatibility []string
	// AlternateDestination reports whether the plan restores away from the
	// original location.
	AlternateDestination bool
	// Paths carries a file-restore subset; empty means the whole scope.
	Paths       []string
	RequestedBy string
	CreatedAt   int64 // unix seconds; kept numeric for stable DTOs
	ExpiresAt   int64
}

// PlanStore persists plans between plan → confirm → execute.
type PlanStore interface {
	SavePlan(ctx context.Context, p *Plan) error
	GetPlan(ctx context.Context, id string) (*Plan, error)
}

// buildPlan validates the request against loaded snapshots and assembles
// the ordered steps, warnings, and compatibility notes.
func buildPlan(now, expires int64, newID func() string, req PlanRequest, snaps []*domain.Snapshot) (*Plan, error) {
	if !req.Kind.Valid() {
		return nil, fmt.Errorf("%w: unknown restore kind %q", domain.ErrValidation, req.Kind)
	}
	if len(snaps) == 0 {
		return nil, fmt.Errorf("%w: at least one snapshot is required", domain.ErrValidation)
	}
	agent := snaps[0].AgentID
	for _, s := range snaps[1:] {
		if s.AgentID != agent {
			return nil, fmt.Errorf("%w: all restore sources must belong to one agent", domain.ErrValidation)
		}
	}
	plan := &Plan{
		ID:          newID(),
		Kind:        req.Kind,
		AgentID:     agent,
		RequestedBy: strings.TrimSpace(req.RequestedBy),
		CreatedAt:   now,
		ExpiresAt:   expires,
	}
	for _, s := range snaps {
		plan.SnapshotIDs = append(plan.SnapshotIDs, s.ID)
	}
	if snap := snaps[0]; snap.Encrypted {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("snapshot is encrypted; the agent needs key %q at restore time", snap.KeyRef))
	}

	var err error
	switch req.Kind {
	case KindFile:
		err = planFile(plan, req, snaps[0])
	case KindDatabase:
		err = planDatabase(plan, req, snaps[0])
	case KindVolume:
		err = planVolume(plan, req, snaps[0])
	case KindStack:
		err = planStack(plan, req, snaps)
	}
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// planFile plans an individual file/dir (or whole-scope) restore.
func planFile(plan *Plan, req PlanRequest, snap *domain.Snapshot) error {
	switch snap.TargetType {
	case domain.TargetHostFilesystem, domain.TargetDockerConfig:
	default:
		return fmt.Errorf("%w: file restore needs a filesystem snapshot, got %q", domain.ErrValidation, snap.TargetType)
	}
	for _, p := range req.Paths {
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("%w: restore path %q must be absolute", domain.ErrValidation, p)
		}
	}
	dest := strings.TrimSpace(req.Destination)
	step := Step{Sequence: 1, Kind: KindFile, SnapshotID: snap.ID, Source: snap.Selector}
	if dest == "" {
		step.Destination = "(original location)"
		plan.Warnings = append(plan.Warnings,
			"in-place restore will OVERWRITE existing data at the original paths")
	} else {
		if !strings.HasPrefix(dest, "/") {
			return fmt.Errorf("%w: alternate destination %q must be an absolute path", domain.ErrValidation, dest)
		}
		step.Destination = dest
		plan.AlternateDestination = true
	}
	if len(req.Paths) > 0 {
		plan.Paths = append([]string(nil), req.Paths...)
		step.Notes = append(step.Notes, fmt.Sprintf("limited to %d path(s)", len(req.Paths)))
	} else {
		step.Notes = append(step.Notes, "full snapshot scope")
	}
	plan.Steps = []Step{step}
	return nil
}

// planDatabase plans a postgres/mysql restore with compatibility notes.
func planDatabase(plan *Plan, req PlanRequest, snap *domain.Snapshot) error {
	engine := strings.ToLower(strings.TrimSpace(req.Engine))
	var want domain.TargetType
	switch engine {
	case EnginePostgres:
		want = domain.TargetPostgres
	case EngineMySQL:
		want = domain.TargetMySQL
	default:
		return fmt.Errorf("%w: database engine must be %q or %q", domain.ErrValidation, EnginePostgres, EngineMySQL)
	}
	if snap.TargetType != want {
		return fmt.Errorf("%w: cannot restore a %q snapshot as %q (cross-engine restore is refused)",
			domain.ErrValidation, snap.TargetType, engine)
	}
	targetDB := strings.TrimSpace(req.TargetDB)
	if targetDB == "" {
		targetDB = strings.TrimSpace(req.Destination)
	}
	dest := snap.Selector
	if targetDB != "" {
		if err := checkDBName(targetDB); err != nil {
			return err
		}
		dest = targetDB
		plan.AlternateDestination = true
	} else {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("in-place restore will OVERWRITE database %q; existing data will be replaced", snap.Selector))
	}
	plan.Steps = []Step{{
		Sequence: 1, Kind: KindDatabase, SnapshotID: snap.ID,
		Source: snap.Selector, Destination: dest,
	}}
	plan.Compatibility = append(plan.Compatibility, dbCompatibility(engine, snap)...)
	return nil
}

// dbCompatibility documents restore requirements and constraints (PRD §7.7).
func dbCompatibility(engine string, snap *domain.Snapshot) []string {
	switch engine {
	case EnginePostgres:
		return []string{
			"restore with pg_restore/psql from a PostgreSQL major version compatible with the source",
			"target role must own the database (or have CREATEDB) and required extensions must be installed",
			"logical dump restores schema + data; server-level roles/tablespaces from " + quoteSelector(snap) + " are not included unless dumped with them",
		}
	default:
		return []string{
			"restore with a mysql client compatible with the dump's server version",
			"target character set and collation must match the source or the restore may corrupt text",
			"routines/views with DEFINER clauses may require elevated privileges on the target",
		}
	}
}

func quoteSelector(snap *domain.Snapshot) string {
	if snap.Selector == "" {
		return "the source database"
	}
	return fmt.Sprintf("%q", snap.Selector)
}

// checkDBName validates an alternate database name conservatively.
func checkDBName(name string) error {
	if len(name) > 63 {
		return fmt.Errorf("%w: database name %q exceeds 63 characters", domain.ErrValidation, name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("%w: database name %q contains invalid character %q", domain.ErrValidation, name, string(r))
	}
	return nil
}

// planVolume plans a Docker volume data restore.
func planVolume(plan *Plan, req PlanRequest, snap *domain.Snapshot) error {
	if snap.TargetType != domain.TargetDockerVolume {
		return fmt.Errorf("%w: volume restore needs a docker_volume snapshot, got %q", domain.ErrValidation, snap.TargetType)
	}
	dest := strings.TrimSpace(req.Destination)
	step := Step{Sequence: 1, Kind: KindVolume, SnapshotID: snap.ID, Source: snap.Selector}
	if dest == "" {
		step.Destination = snap.Selector
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("in-place restore will OVERWRITE volume %q", snap.Selector))
	} else {
		step.Destination = dest
		plan.AlternateDestination = true
	}
	plan.Warnings = append(plan.Warnings,
		"stop containers using the target volume before executing; writes during restore are lost")
	plan.Steps = []Step{step}
	return nil
}

// planStack plans a multi-snapshot restore in an explicit caller-supplied
// order. The order must be an exact permutation of the snapshot set.
func planStack(plan *Plan, req PlanRequest, snaps []*domain.Snapshot) error {
	if len(req.Order) == 0 {
		return ErrOrderRequired
	}
	byID := map[string]*domain.Snapshot{}
	for _, s := range snaps {
		byID[s.ID] = s
	}
	if len(req.Order) != len(snaps) {
		return fmt.Errorf("%w: order covers %d of %d snapshots", ErrOrderRequired, len(req.Order), len(snaps))
	}
	seen := map[string]bool{}
	for i, id := range req.Order {
		s, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: order references unknown snapshot %q", domain.ErrValidation, id)
		}
		if seen[id] {
			return fmt.Errorf("%w: order lists snapshot %q twice", domain.ErrValidation, id)
		}
		seen[id] = true
		plan.Steps = append(plan.Steps, Step{
			Sequence: i + 1, Kind: KindStack, SnapshotID: s.ID,
			Source:      s.Selector,
			Destination: "(per-member plan)",
			Notes:       []string{fmt.Sprintf("member type %s", string(s.TargetType))},
		})
	}
	plan.Warnings = append(plan.Warnings,
		"application-consistent recovery is claimed only when every member restore succeeds; quiesce writers first")
	return nil
}
