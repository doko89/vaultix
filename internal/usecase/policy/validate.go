package policy

import (
	"fmt"
	"path"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Methods allowed per target type (PRD §7.7: database backup must use
// database-aware procedures; copying live data dirs as files is rejected).
var methodsPerType = map[domain.TargetType][]domain.BackupMethod{
	domain.TargetHostFilesystem: {domain.BackupMethodFull},
	domain.TargetDockerConfig:   {domain.BackupMethodFull},
	domain.TargetDockerVolume:   {domain.BackupMethodFull},
	domain.TargetPostgres:       {domain.BackupMethodLogicalDump},
	domain.TargetMySQL:          {domain.BackupMethodLogicalDump},
	domain.TargetMSSQL:          {domain.BackupMethodNativeFull},
	domain.TargetRedis:          {domain.BackupMethodRDPSnapshot},
	domain.TargetAppStack:       {domain.BackupMethodFull},
}

// defaultMethod returns the method used when the caller leaves Method empty.
func defaultMethod(t domain.TargetType) domain.BackupMethod {
	if ms, ok := methodsPerType[t]; ok && len(ms) > 0 {
		return ms[0]
	}
	return domain.BackupMethodFull
}

// TargetInput is one validated backup target selection.
type TargetInput struct {
	Type          domain.TargetType
	Method        domain.BackupMethod // "" = per-type default
	Selector      string
	Include       []string
	Exclude       []string
	Options       map[string]string
	CredentialRef string
}

// validateTarget enforces per-type rules. Existence checks against refErr
// report unknown credential/profile references as validation errors.
func validateTarget(in TargetInput) error {
	if !in.Type.Valid() {
		return fmt.Errorf("%w: unknown target type %q", domain.ErrValidation, in.Type)
	}
	method := in.Method
	if method == "" {
		method = defaultMethod(in.Type)
	}
	allowed := false
	for _, m := range methodsPerType[in.Type] {
		if m == method {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: method %q is not allowed for target type %q",
			domain.ErrValidation, in.Method, in.Type)
	}
	if strings.TrimSpace(in.Selector) == "" {
		return fmt.Errorf("%w: target selector is required", domain.ErrValidation)
	}
	if len(in.Include) > 0 || len(in.Exclude) > 0 {
		if in.Type != domain.TargetHostFilesystem {
			return fmt.Errorf("%w: include/exclude patterns are only supported for %q targets",
				domain.ErrValidation, domain.TargetHostFilesystem)
		}
		for _, p := range append(append([]string{}, in.Include...), in.Exclude...) {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("%w: include/exclude patterns must not be blank", domain.ErrValidation)
			}
			if _, err := path.Match(p, ""); err != nil {
				return fmt.Errorf("%w: invalid glob pattern %q: %v", domain.ErrValidation, p, err)
			}
		}
	}

	switch in.Type {
	case domain.TargetHostFilesystem:
		return validateHostTarget(in)
	case domain.TargetDockerConfig:
		return validateDockerConfigTarget(in)
	case domain.TargetDockerVolume:
		return validateDockerVolumeTarget(in)
	case domain.TargetPostgres, domain.TargetMySQL:
		return validateLogicalDBTarget(in)
	case domain.TargetMSSQL:
		return validateMSSQLTarget(in)
	case domain.TargetRedis:
		return validateRedisTarget(in)
	case domain.TargetAppStack:
		return validateStackTarget(in)
	default:
		return fmt.Errorf("%w: unknown target type %q", domain.ErrValidation, in.Type)
	}
}

// validateHostTarget: PRD §7.5 — multiple absolute paths, include/exclude
// patterns. One target carries one path; multi-path policies use one target
// per path.
func validateHostTarget(in TargetInput) error {
	if !path.IsAbs(in.Selector) {
		return fmt.Errorf("%w: host filesystem selector must be an absolute path, got %q",
			domain.ErrValidation, in.Selector)
	}
	if strings.TrimSpace(in.CredentialRef) != "" {
		return fmt.Errorf("%w: host filesystem targets take no credential reference", domain.ErrValidation)
	}
	if v, ok := in.Options["symlink"]; ok {
		switch v {
		case "follow", "skip", "record":
		default:
			return fmt.Errorf("%w: host option symlink must be follow|skip|record, got %q",
				domain.ErrValidation, v)
		}
	}
	if err := checkBoolOption(in.Options, "one_file_system"); err != nil {
		return err
	}
	return nil
}

// validateDockerConfigTarget: container metadata / compose config (PRD §7.6).
// Selector is a container name/ID or compose project reference.
func validateDockerConfigTarget(in TargetInput) error {
	if strings.TrimSpace(in.CredentialRef) != "" {
		return fmt.Errorf("%w: docker config targets take no credential reference", domain.ErrValidation)
	}
	if err := checkBoolOption(in.Options, "include_compose"); err != nil {
		return err
	}
	return nil
}

// validateDockerVolumeTarget: named volumes and bind mounts (PRD §7.6).
// Selector is a volume name or bind-mount source path.
func validateDockerVolumeTarget(in TargetInput) error {
	if strings.TrimSpace(in.CredentialRef) != "" {
		return fmt.Errorf("%w: docker volume targets take no credential reference", domain.ErrValidation)
	}
	return nil
}

// validateLogicalDBTarget: PostgreSQL / MySQL logical dumps (PRD §7.7).
// Selector is a database name (or cluster marker); credentials are mandatory
// and referenced by profile — never embedded.
func validateLogicalDBTarget(in TargetInput) error {
	if err := checkDBName(in.Selector); err != nil {
		return err
	}
	if strings.TrimSpace(in.CredentialRef) == "" {
		return fmt.Errorf("%w: database target %q requires a credential-profile reference",
			domain.ErrValidation, in.Selector)
	}
	if v, ok := in.Options["scope"]; ok {
		switch v {
		case "database", "cluster":
		default:
			return fmt.Errorf("%w: database option scope must be database|cluster, got %q",
				domain.ErrValidation, v)
		}
	}
	return nil
}

// validateMSSQLTarget: native full backup (PRD §7.7). Differential and log
// backups are post-MVP and explicitly rejected.
func validateMSSQLTarget(in TargetInput) error {
	if err := checkDBName(in.Selector); err != nil {
		return err
	}
	if strings.TrimSpace(in.CredentialRef) == "" {
		return fmt.Errorf("%w: database target %q requires a credential-profile reference",
			domain.ErrValidation, in.Selector)
	}
	if v, ok := in.Options["backup_type"]; ok && v != "full" {
		return fmt.Errorf("%w: mssql backup_type %q is post-MVP; only \"full\" is supported",
			domain.ErrValidation, v)
	}
	return nil
}

// validateRedisTarget: RDB/AOF persistence artifacts (PRD §7.7). Selector is
// an instance label or persistence path; auth profile is optional.
func validateRedisTarget(in TargetInput) error {
	if v, ok := in.Options["persistence"]; ok {
		switch v {
		case "rdb", "aof", "both":
		default:
			return fmt.Errorf("%w: redis option persistence must be rdb|aof|both, got %q",
				domain.ErrValidation, v)
		}
	}
	if err := checkBoolOption(in.Options, "verify"); err != nil {
		return err
	}
	return nil
}

// validateStackTarget: application stack membership (PRD §7.8). Selector is
// the stack ID; member resources carry their own selectors and credentials,
// so the top-level target takes none.
func validateStackTarget(in TargetInput) error {
	if strings.TrimSpace(in.CredentialRef) != "" {
		return fmt.Errorf("%w: app-stack targets take no credential reference; member resources carry their own",
			domain.ErrValidation)
	}
	return nil
}

// checkDBName rejects empty names, paths, and control characters.
func checkDBName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return fmt.Errorf("%w: database name is required", domain.ErrValidation)
	}
	if n != name {
		return fmt.Errorf("%w: database name %q must not have leading/trailing whitespace",
			domain.ErrValidation, name)
	}
	if strings.ContainsAny(n, "/\x00") || strings.ContainsAny(n, " \t\r\n") {
		return fmt.Errorf("%w: database name %q must be a plain identifier, not a path",
			domain.ErrValidation, name)
	}
	return nil
}

// checkBoolOption validates an optional boolean-valued option key.
func checkBoolOption(opts map[string]string, key string) error {
	v, ok := opts[key]
	if !ok {
		return nil
	}
	if v != "true" && v != "false" {
		return fmt.Errorf("%w: option %q must be true|false, got %q", domain.ErrValidation, key, v)
	}
	return nil
}

// toDomainTarget converts a validated input into a domain target. Callers
// must run validateTarget first; method defaults are applied here.
func toDomainTarget(policyID string, in TargetInput) domain.BackupPolicyTarget {
	method := in.Method
	if method == "" {
		method = defaultMethod(in.Type)
	}
	return domain.BackupPolicyTarget{
		PolicyID:      policyID,
		Type:          in.Type,
		Method:        method,
		Selector:      in.Selector,
		Include:       append([]string{}, in.Include...),
		Exclude:       append([]string{}, in.Exclude...),
		Options:       cloneOptions(in.Options),
		CredentialRef: in.CredentialRef,
	}
}

// cloneOptions deep-copies an options map (nil stays nil).
func cloneOptions(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// cloneTargets deep-copies targets so published versions are immune to later
// caller-side slice/map mutation.
func cloneTargets(in []domain.BackupPolicyTarget) []domain.BackupPolicyTarget {
	out := make([]domain.BackupPolicyTarget, len(in))
	for i, t := range in {
		t.Include = append([]string{}, t.Include...)
		t.Exclude = append([]string{}, t.Exclude...)
		t.Options = cloneOptions(t.Options)
		out[i] = t
	}
	return out
}

// validateCompression enforces zstd-only (MVP) with a bounded level.
// Algorithm "" means the zstd default; level 0 means the default level.
func validateCompression(c domain.Compression) error {
	algo := strings.ToLower(strings.TrimSpace(c.Algorithm))
	if algo != "" && algo != "zstd" {
		return fmt.Errorf("%w: unsupported compression algorithm %q; only \"zstd\" is supported",
			domain.ErrValidation, c.Algorithm)
	}
	if c.Level < 0 || c.Level > 22 {
		return fmt.Errorf("%w: compression level must be 0..22 (0 = default), got %d",
			domain.ErrValidation, c.Level)
	}
	return nil
}
