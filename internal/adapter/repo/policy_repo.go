package repo

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// PolicyRepo implements domain.PolicyRepository on
// backup_policies/backup_policy_versions/backup_policy_targets.
//
// Mapping notes: AgentIDs persists the first entry only (the schema has a
// single agent_id column); multi-agent policies need a junction table that
// is out of scope. Versions freeze the effective configuration
// (targets/schedule/retention/compression/encryption/storage/note) into
// backup_policy_versions.config as JSON. The working target set lives in
// backup_policy_targets; ReplaceTargets/ListTargets expose it as concrete
// methods because the port has no target operations (a later domain change
// should promote them).
type PolicyRepo struct {
	baseRepo
}

// policyVersionConfig is the backup_policy_versions.config JSON codec.
type policyVersionConfig struct {
	Targets     []domain.BackupPolicyTarget `json:"targets"`
	Schedule    domain.Schedule             `json:"schedule"`
	Retention   domain.RetentionRule        `json:"retention"`
	Compression domain.Compression          `json:"compression"`
	Encryption  domain.Encryption           `json:"encryption"`
	StorageID   string                      `json:"storage_id"`
	Note        string                      `json:"note"`
}

// policyTargetConfig is the backup_policy_targets.target_config JSON codec.
type policyTargetConfig struct {
	OrigType      string            `json:"_type,omitempty"`
	Method        string            `json:"method,omitempty"`
	Include       []string          `json:"include,omitempty"`
	Exclude       []string          `json:"exclude,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
	CredentialRef string            `json:"credential_ref,omitempty"`
}

// CreatePolicy inserts a policy. The ID is database-generated and
// CurrentVersion starts at 1.
func (r *PolicyRepo) CreatePolicy(ctx context.Context, p *domain.BackupPolicy) error {
	if p == nil {
		return validationError("policy is required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	agentID, err := firstAgentID(p.AgentIDs)
	if err != nil {
		return err
	}
	storageID, err := mustOptUUID(p.StorageID, "storage id")
	if err != nil {
		return err
	}
	createdBy, err := mustOptUUID(p.CreatedBy, "created_by id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreatePolicy(ctx, gen.CreatePolicyParams{
		Name:                   p.Name,
		Description:            p.Description,
		AgentID:                agentID,
		StorageDestinationID:   storageID,
		SecretProfileID:        pgtype.UUID{},
		Compression:            compressionAlgorithm(p.Compression.Algorithm),
		CompressionLevel:       int32OrZero(p.Compression.Level),
		EncryptionEnabled:      p.Encryption.Enabled,
		EncryptionKeyReference: p.Encryption.KeyRef,
		IsEnabled:              p.Enabled,
		CreatedBy:              createdBy,
	})
	if err != nil {
		return mapErr(err)
	}
	p.ID = uuidString(row.ID)
	p.CurrentVersion = int(row.CurrentVersion)
	p.CreatedAt = timeOrZero(row.CreatedAt)
	p.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetPolicy returns one policy by ID.
func (r *PolicyRepo) GetPolicy(ctx context.Context, id string) (*domain.BackupPolicy, error) {
	pid, err := mustParseUUID(id, "policy id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetPolicyByID(ctx, gen.GetPolicyByIDParams{ID: pid})
	if err != nil {
		return nil, mapErr(err)
	}
	p := mapPolicy(row)
	return &p, nil
}

// UpdatePolicy rewrites a policy. The version counter is owned by
// PublishVersion and is never touched here.
func (r *PolicyRepo) UpdatePolicy(ctx context.Context, p *domain.BackupPolicy) error {
	if p == nil {
		return validationError("policy is required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	pid, err := mustParseUUID(p.ID, "policy id")
	if err != nil {
		return err
	}
	agentID, err := firstAgentID(p.AgentIDs)
	if err != nil {
		return err
	}
	storageID, err := mustOptUUID(p.StorageID, "storage id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdatePolicy(ctx, gen.UpdatePolicyParams{
		ID:                     pid,
		Name:                   p.Name,
		Description:            p.Description,
		AgentID:                agentID,
		StorageDestinationID:   storageID,
		Compression:            compressionAlgorithm(p.Compression.Algorithm),
		CompressionLevel:       int32OrZero(p.Compression.Level),
		EncryptionEnabled:      p.Encryption.Enabled,
		EncryptionKeyReference: p.Encryption.KeyRef,
		IsEnabled:              p.Enabled,
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// DeletePolicy removes a policy (versions, targets, schedules, and
// retention cascade; jobs/snapshots keep history with NULL policy).
func (r *PolicyRepo) DeletePolicy(ctx context.Context, id string) error {
	pid, err := mustParseUUID(id, "policy id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetPolicyByID(ctx, gen.GetPolicyByIDParams{ID: pid}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeletePolicy(ctx, gen.DeletePolicyParams{ID: pid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ListPolicies returns one page of policies filtered by name.
func (r *PolicyRepo) ListPolicies(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	limit, offset := page(opts)
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.SearchPolicies(ctx, gen.SearchPoliciesParams{
		Column1: opts.Query,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return domain.ListResult[domain.BackupPolicy]{}, mapErr(err)
	}
	total, err := r.q.CountSearchPolicies(ctx, gen.CountSearchPoliciesParams{Column1: opts.Query})
	if err != nil {
		return domain.ListResult[domain.BackupPolicy]{}, mapErr(err)
	}
	out := make([]domain.BackupPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapPolicy(row))
	}
	return domain.ListResult[domain.BackupPolicy]{Items: out, Total: int(total)}, nil
}

// PublishVersion freezes a new immutable version. The version number is
// always assigned by the repository (the input Version is ignored);
// v.Version/v.CreatedAt are populated from the row.
func (r *PolicyRepo) PublishVersion(ctx context.Context, v *domain.BackupPolicyVersion) error {
	if v == nil {
		return validationError("policy version is required")
	}
	pid, err := mustParseUUID(v.PolicyID, "policy id")
	if err != nil {
		return err
	}
	createdBy, err := mustOptUUID(v.CreatedBy, "created_by id")
	if err != nil {
		return err
	}
	config, err := encodeJSON(policyVersionConfig{
		Targets:     nonNilTargets(v.Targets),
		Schedule:    v.Schedule,
		Retention:   v.Retention,
		Compression: v.Compression,
		Encryption:  v.Encryption,
		StorageID:   v.StorageID,
		Note:        v.Note,
	})
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	err = r.inTx(ctx, func(q *gen.Queries) error {
		policy, err := q.BumpPolicyVersion(ctx, gen.BumpPolicyVersionParams{ID: pid})
		if err != nil {
			return err
		}
		row, err := q.CreatePolicyVersion(ctx, gen.CreatePolicyVersionParams{
			PolicyID:  pid,
			Version:   policy.CurrentVersion,
			Config:    config,
			CreatedBy: createdBy,
		})
		if err != nil {
			return err
		}
		v.Version = int(row.Version)
		v.CreatedAt = timeOrZero(row.CreatedAt)
		return nil
	})
	return mapErr(err)
}

// GetVersion returns one frozen version by (policy, number).
func (r *PolicyRepo) GetVersion(ctx context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetPolicyVersion(ctx, gen.GetPolicyVersionParams{
		PolicyID: pid,
		Version:  int32OrZero(version),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	v, err := mapPolicyVersion(row)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ListVersions returns all versions of a policy, newest first.
func (r *PolicyRepo) ListVersions(ctx context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListPolicyVersions(ctx, gen.ListPolicyVersionsParams{
		PolicyID: pid,
		Limit:    math.MaxInt32,
		Offset:   0,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.BackupPolicyVersion, 0, len(rows))
	for _, row := range rows {
		v, err := mapPolicyVersion(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ReplaceTargets swaps the working target set of a policy. This is a
// concrete method (no domain port covers targets); frozen copies live on
// published versions.
func (r *PolicyRepo) ReplaceTargets(ctx context.Context, policyID string, targets []domain.BackupPolicyTarget) error {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return err
	}
	encoded := make([]gen.AddPolicyTargetParams, 0, len(targets))
	for i := range targets {
		if err := targets[i].Validate(); err != nil {
			return err
		}
		config, err := encodePolicyTargetConfig(&targets[i])
		if err != nil {
			return err
		}
		encoded = append(encoded, gen.AddPolicyTargetParams{
			PolicyID:     pid,
			TargetType:   policyTargetTypeToDB(targets[i].Type),
			TargetRef:    targets[i].Selector,
			TargetConfig: config,
		})
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
		if err := q.DeletePolicyTargets(ctx, gen.DeletePolicyTargetsParams{PolicyID: pid}); err != nil {
			return err
		}
		for j, params := range encoded {
			row, err := q.AddPolicyTarget(ctx, params)
			if err != nil {
				return err
			}
			targets[j].ID = uuidString(row.ID)
			targets[j].PolicyID = policyID
		}
		return nil
	}))
}

// ListTargets returns the working target set of a policy.
func (r *PolicyRepo) ListTargets(ctx context.Context, policyID string) ([]domain.BackupPolicyTarget, error) {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListPolicyTargets(ctx, gen.ListPolicyTargetsParams{PolicyID: pid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.BackupPolicyTarget, 0, len(rows))
	for _, row := range rows {
		t, err := mapPolicyTarget(row)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// firstAgentID persists the first assigned agent (see mapping notes).
func firstAgentID(agentIDs []string) (pgtype.UUID, error) {
	if len(agentIDs) == 0 {
		return pgtype.UUID{}, nil
	}
	for _, id := range agentIDs {
		if _, err := parseUUID(id); err != nil {
			return pgtype.UUID{}, validationError("malformed agent id")
		}
	}
	return mustOptUUID(agentIDs[0], "agent id")
}

func compressionAlgorithm(algorithm string) string {
	if algorithm == "" {
		return "zstd"
	}
	return algorithm
}

func mapPolicy(row gen.BackupPolicy) domain.BackupPolicy {
	var agents []string
	if row.AgentID.Valid {
		agents = []string{uuidString(row.AgentID)}
	}
	return domain.BackupPolicy{
		ID:             uuidString(row.ID),
		Name:           row.Name,
		Description:    row.Description,
		AgentIDs:       agents,
		StorageID:      uuidString(row.StorageDestinationID),
		Compression:    domain.Compression{Algorithm: row.Compression, Level: int(row.CompressionLevel)},
		Encryption:     domain.Encryption{Enabled: row.EncryptionEnabled, KeyRef: row.EncryptionKeyReference},
		Enabled:        row.IsEnabled,
		CurrentVersion: int(row.CurrentVersion),
		CreatedBy:      uuidString(row.CreatedBy),
		CreatedAt:      timeOrZero(row.CreatedAt),
		UpdatedAt:      timeOrZero(row.UpdatedAt),
	}
}

func mapPolicyVersion(row gen.BackupPolicyVersion) (domain.BackupPolicyVersion, error) {
	var config policyVersionConfig
	if err := decodeJSON(row.Config, &config); err != nil {
		return domain.BackupPolicyVersion{}, err
	}
	return domain.BackupPolicyVersion{
		Version:     int(row.Version),
		PolicyID:    uuidString(row.PolicyID),
		CreatedBy:   uuidString(row.CreatedBy),
		CreatedAt:   timeOrZero(row.CreatedAt),
		Targets:     config.Targets,
		Schedule:    config.Schedule,
		Retention:   config.Retention,
		Compression: config.Compression,
		Encryption:  config.Encryption,
		StorageID:   config.StorageID,
		Note:        config.Note,
	}, nil
}

// policyTargetTypeToDB maps domain target types onto the schema check values.
func policyTargetTypeToDB(t domain.TargetType) string {
	switch t {
	case domain.TargetHostFilesystem:
		return "host_path"
	case domain.TargetDockerConfig:
		return "container_meta"
	case domain.TargetDockerVolume:
		return "volume"
	case domain.TargetPostgres:
		return "postgres"
	case domain.TargetMySQL:
		return "mysql"
	case domain.TargetMSSQL:
		return "mssql"
	case domain.TargetRedis:
		return "redis"
	case domain.TargetAppStack:
		return "stack"
	default:
		return "host_path"
	}
}

// policyTargetTypeFromDB restores the domain type, preferring the stored
// original when present. Foreign "bind_mount" rows map to docker_volume.
func policyTargetTypeFromDB(dbType string, config policyTargetConfig) domain.TargetType {
	if domain.TargetType(config.OrigType).Valid() {
		return domain.TargetType(config.OrigType)
	}
	switch dbType {
	case "container_meta":
		return domain.TargetDockerConfig
	case "volume", "bind_mount":
		return domain.TargetDockerVolume
	case "postgres":
		return domain.TargetPostgres
	case "mysql":
		return domain.TargetMySQL
	case "mssql":
		return domain.TargetMSSQL
	case "redis":
		return domain.TargetRedis
	case "stack":
		return domain.TargetAppStack
	default:
		return domain.TargetHostFilesystem
	}
}

func encodePolicyTargetConfig(t *domain.BackupPolicyTarget) ([]byte, error) {
	return encodeJSON(policyTargetConfig{
		OrigType:      string(t.Type),
		Method:        string(t.Method),
		Include:       t.Include,
		Exclude:       t.Exclude,
		Options:       t.Options,
		CredentialRef: t.CredentialRef,
	})
}

func mapPolicyTarget(row gen.BackupPolicyTarget) (domain.BackupPolicyTarget, error) {
	var config policyTargetConfig
	if err := decodeJSON(row.TargetConfig, &config); err != nil {
		return domain.BackupPolicyTarget{}, err
	}
	return domain.BackupPolicyTarget{
		ID:            uuidString(row.ID),
		PolicyID:      uuidString(row.PolicyID),
		Type:          policyTargetTypeFromDB(row.TargetType, config),
		Method:        domain.BackupMethod(config.Method),
		Selector:      row.TargetRef,
		Include:       config.Include,
		Exclude:       config.Exclude,
		Options:       config.Options,
		CredentialRef: config.CredentialRef,
	}, nil
}

func nonNilTargets(in []domain.BackupPolicyTarget) []domain.BackupPolicyTarget {
	if in == nil {
		return []domain.BackupPolicyTarget{}
	}
	return in
}
