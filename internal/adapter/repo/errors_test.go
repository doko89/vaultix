package repo

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// TestRepoValidationWithoutDB exercises every argument-validation branch
// that fails before any database call (nil entities, malformed IDs,
// domain validation). It runs against New(nil) and must never touch the DB.
func TestRepoValidationWithoutDB(t *testing.T) {
	ctx := context.Background()
	repos := New(nil)
	badID := "not-a-uuid"
	calls := map[string]func() error{
		"CreateUser/nil":            func() error { return repos.Users.CreateUser(ctx, nil) },
		"CreateUser/invalid":        func() error { return repos.Users.CreateUser(ctx, &domain.User{}) },
		"GetUser/bad":               func() error { _, err := repos.Users.GetUser(ctx, badID); return err },
		"GetUserByUsername/empty":   func() error { _, err := repos.Users.GetUserByUsername(ctx, ""); return err },
		"UpdateUser/nil":            func() error { return repos.Users.UpdateUser(ctx, nil) },
		"GetRole/bad":               func() error { _, err := repos.Users.GetRole(ctx, badID); return err },
		"GetRoleByName/empty":       func() error { _, err := repos.Users.GetRoleByName(ctx, ""); return err },
		"ListUserRoles/bad":         func() error { _, err := repos.Users.ListUserRoles(ctx, badID); return err },
		"GrantRole/bad":             func() error { return repos.Users.GrantRole(ctx, badID, badID, "") },
		"RevokeRole/bad":            func() error { return repos.Users.RevokeRole(ctx, badID, badID) },
		"ReplaceUserRoles/bad-user": func() error { return repos.Users.ReplaceUserRoles(ctx, badID, nil) },
		"ReplaceUserRoles/bad-role": func() error {
			return repos.Users.ReplaceUserRoles(ctx, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", []string{badID})
		},
		"CreateProvider/nil":        func() error { return repos.OIDC.CreateProvider(ctx, nil) },
		"CreateProvider/invalid":    func() error { return repos.OIDC.CreateProvider(ctx, &domain.OIDCProvider{}) },
		"GetProvider/bad":           func() error { _, err := repos.OIDC.GetProvider(ctx, badID); return err },
		"GetProviderByIssuer/empty": func() error { _, err := repos.OIDC.GetProviderByIssuer(ctx, ""); return err },
		"UpdateProvider/nil":        func() error { return repos.OIDC.UpdateProvider(ctx, nil) },
		"LinkIdentity/nil":          func() error { return repos.OIDC.LinkIdentity(ctx, nil) },
		"LinkIdentity/invalid":      func() error { return repos.OIDC.LinkIdentity(ctx, &domain.UserExternalIdentity{}) },
		"GetIdentity/empty":         func() error { _, err := repos.OIDC.GetIdentity(ctx, "", ""); return err },
		"ListUserIdentities/bad":    func() error { _, err := repos.OIDC.ListUserIdentities(ctx, badID); return err },
		"UpdateIdentity/nil":        func() error { return repos.OIDC.UpdateIdentity(ctx, nil) },
		"UnlinkIdentity/bad":        func() error { return repos.OIDC.UnlinkIdentity(ctx, badID) },
		"CreateAgent/nil":           func() error { return repos.Agents.CreateAgent(ctx, nil) },
		"CreateAgent/invalid":       func() error { return repos.Agents.CreateAgent(ctx, &domain.Agent{}) },
		"GetAgent/bad":              func() error { _, err := repos.Agents.GetAgent(ctx, badID); return err },
		"UpdateAgent/nil":           func() error { return repos.Agents.UpdateAgent(ctx, nil) },
		"StoreCredential/nil":       func() error { return repos.Agents.StoreCredential(ctx, nil) },
		"StoreCredential/no-agent":  func() error { return repos.Agents.StoreCredential(ctx, &domain.AgentCredential{TokenHash: "h"}) },
		"StoreCredential/no-hash": func() error {
			return repos.Agents.StoreCredential(ctx, &domain.AgentCredential{AgentID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944"})
		},
		"GetCredentialByHash/empty": func() error { _, err := repos.Agents.GetCredentialByHash(ctx, ""); return err },
		"ListAgentCredentials/bad":  func() error { _, err := repos.Agents.ListAgentCredentials(ctx, badID); return err },
		"UpdateCredential/nil":      func() error { return repos.Agents.UpdateCredential(ctx, nil) },
		"RecordHeartbeat/nil":       func() error { return repos.Agents.RecordHeartbeat(ctx, nil) },
		"RecordHeartbeat/negative": func() error {
			return repos.Agents.RecordHeartbeat(ctx, &domain.AgentHeartbeat{AgentID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", RunningJobs: -1})
		},
		"LatestHeartbeat/bad":        func() error { _, err := repos.Agents.LatestHeartbeat(ctx, badID); return err },
		"ReplaceResources/bad-agent": func() error { return repos.Discovery.ReplaceAgentResources(ctx, badID, nil) },
		"ReplaceResources/invalid": func() error {
			return repos.Discovery.ReplaceAgentResources(ctx, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", []domain.DiscoveredResource{{}})
		},
		"ListAgentResources/bad":    func() error { _, err := repos.Discovery.ListAgentResources(ctx, badID, ""); return err },
		"GetResource/bad":           func() error { _, err := repos.Discovery.GetResource(ctx, badID); return err },
		"CreateDestination/nil":     func() error { return repos.Storage.CreateDestination(ctx, nil) },
		"CreateDestination/invalid": func() error { return repos.Storage.CreateDestination(ctx, &domain.StorageDestination{}) },
		"GetDestination/bad":        func() error { _, err := repos.Storage.GetDestination(ctx, badID); return err },
		"UpdateDestination/nil":     func() error { return repos.Storage.UpdateDestination(ctx, nil) },
		"DeleteDestination/bad":     func() error { return repos.Storage.DeleteDestination(ctx, badID) },
		"CreateSecret/nil":          func() error { return repos.Storage.CreateSecret(ctx, nil) },
		"CreateSecret/invalid":      func() error { return repos.Storage.CreateSecret(ctx, &domain.SecretProfile{}) },
		"GetSecret/bad":             func() error { _, err := repos.Storage.GetSecret(ctx, badID); return err },
		"DeleteSecret/bad":          func() error { return repos.Storage.DeleteSecret(ctx, badID) },
		"CreatePolicy/nil":          func() error { return repos.Policies.CreatePolicy(ctx, nil) },
		"CreatePolicy/invalid":      func() error { return repos.Policies.CreatePolicy(ctx, &domain.BackupPolicy{}) },
		"CreatePolicy/bad-agent": func() error {
			return repos.Policies.CreatePolicy(ctx, &domain.BackupPolicy{Name: "p", AgentIDs: []string{badID}})
		},
		"GetPolicy/bad":      func() error { _, err := repos.Policies.GetPolicy(ctx, badID); return err },
		"UpdatePolicy/nil":   func() error { return repos.Policies.UpdatePolicy(ctx, nil) },
		"DeletePolicy/bad":   func() error { return repos.Policies.DeletePolicy(ctx, badID) },
		"PublishVersion/nil": func() error { return repos.Policies.PublishVersion(ctx, nil) },
		"PublishVersion/bad": func() error { return repos.Policies.PublishVersion(ctx, &domain.BackupPolicyVersion{PolicyID: badID}) },
		"GetVersion/bad":     func() error { _, err := repos.Policies.GetVersion(ctx, badID, 1); return err },
		"ListVersions/bad":   func() error { _, err := repos.Policies.ListVersions(ctx, badID); return err },
		"ReplaceTargets/bad": func() error { return repos.Policies.ReplaceTargets(ctx, badID, nil) },
		"ReplaceTargets/invalid": func() error {
			return repos.Policies.ReplaceTargets(ctx, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", []domain.BackupPolicyTarget{{}})
		},
		"ListTargets/bad": func() error { _, err := repos.Policies.ListTargets(ctx, badID); return err },
		"Retention/upsert-invalid": func() error {
			_, err := repos.Retention.UpsertRule(ctx, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", domain.RetentionRule{})
			return err
		},
		"Retention/get-bad":         func() error { _, err := repos.Retention.GetByPolicy(ctx, badID); return err },
		"Retention/delete-bad":      func() error { return repos.Retention.DeleteRule(ctx, badID) },
		"Retention/record-bad":      func() error { return repos.Retention.RecordRun(ctx, badID) },
		"CreateSchedule/nil":        func() error { return repos.Schedules.CreateSchedule(ctx, nil) },
		"CreateSchedule/invalid":    func() error { return repos.Schedules.CreateSchedule(ctx, &domain.Schedule{}) },
		"GetSchedule/bad":           func() error { _, err := repos.Schedules.GetSchedule(ctx, badID); return err },
		"UpdateSchedule/nil":        func() error { return repos.Schedules.UpdateSchedule(ctx, nil) },
		"DeleteSchedule/bad":        func() error { return repos.Schedules.DeleteSchedule(ctx, badID) },
		"ListSchedules/bad":         func() error { _, err := repos.Schedules.ListSchedules(ctx, badID); return err },
		"CreateBackupJob/nil":       func() error { return repos.BackupJobs.CreateJob(ctx, nil) },
		"CreateBackupJob/bad-agent": func() error { return repos.BackupJobs.CreateJob(ctx, &domain.BackupJob{AgentID: badID}) },
		"GetBackupJob/bad":          func() error { _, err := repos.BackupJobs.GetJob(ctx, badID); return err },
		"UpdateBackupJob/nil":       func() error { return repos.BackupJobs.UpdateJob(ctx, nil) },
		"ListBackupJobs/bad-filter": func() error {
			_, err := repos.BackupJobs.ListJobs(ctx, domain.ListOptions{}, domain.JobFilter{AgentID: badID})
			return err
		},
		"ClaimNextJob/bad":           func() error { _, err := repos.BackupJobs.ClaimNextJob(ctx, badID); return err },
		"CreateRestoreJob/nil":       func() error { return repos.RestoreJobs.CreateJob(ctx, nil) },
		"CreateRestoreJob/no-agent":  func() error { return repos.RestoreJobs.CreateJob(ctx, &domain.RestoreJob{}) },
		"GetRestoreJob/bad":          func() error { _, err := repos.RestoreJobs.GetJob(ctx, badID); return err },
		"UpdateRestoreJob/nil":       func() error { return repos.RestoreJobs.UpdateJob(ctx, nil) },
		"ListRestoreJobs/bad":        func() error { _, err := repos.RestoreJobs.ListJobs(ctx, domain.ListOptions{}, badID); return err },
		"CreateSnapshot/nil":         func() error { return repos.Snapshots.CreateSnapshot(ctx, nil) },
		"CreateSnapshot/bad-storage": func() error { return repos.Snapshots.CreateSnapshot(ctx, &domain.Snapshot{StorageID: badID}) },
		"GetSnapshot/bad":            func() error { _, err := repos.Snapshots.GetSnapshot(ctx, badID); return err },
		"UpdateSnapshot/nil":         func() error { return repos.Snapshots.UpdateSnapshot(ctx, nil) },
		"DeleteSnapshot/bad":         func() error { return repos.Snapshots.DeleteSnapshot(ctx, badID) },
		"ListSnapshots/bad": func() error {
			_, err := repos.Snapshots.ListSnapshots(ctx, domain.ListOptions{}, domain.SnapshotFilter{AgentID: badID})
			return err
		},
		"PutManifest/nil":         func() error { return repos.Snapshots.PutManifest(ctx, nil) },
		"PutManifest/bad":         func() error { return repos.Snapshots.PutManifest(ctx, &domain.SnapshotManifest{SnapshotID: badID}) },
		"GetManifest/bad":         func() error { _, err := repos.Snapshots.GetManifest(ctx, badID); return err },
		"PutObjects/bad-snapshot": func() error { return repos.Snapshots.PutObjects(ctx, []domain.SnapshotObject{{SnapshotID: badID}}) },
		"PutObjects/empty-key": func() error {
			return repos.Snapshots.PutObjects(ctx, []domain.SnapshotObject{{SnapshotID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944"}})
		},
		"ListObjects/bad":            func() error { _, err := repos.Snapshots.ListObjects(ctx, badID); return err },
		"CreateNotification/nil":     func() error { return repos.Notifications.CreateNotification(ctx, nil) },
		"CreateNotification/no-chan": func() error { return repos.Notifications.CreateNotification(ctx, &domain.Notification{Recipient: "r"}) },
		"CreateNotification/no-rcpt": func() error {
			return repos.Notifications.CreateNotification(ctx, &domain.Notification{Channel: domain.NotificationEmail})
		},
		"UpdateNotification/nil": func() error { return repos.Notifications.UpdateNotification(ctx, nil) },
		"UpdateNotification/bad": func() error { return repos.Notifications.UpdateNotification(ctx, &domain.Notification{ID: badID}) },
		"AuditAppend/nil":        func() error { return repos.Audit.Append(ctx, nil) },
		"AuditAppend/no-action":  func() error { return repos.Audit.Append(ctx, &domain.AuditLog{}) },
		"AuditAppend/bad-outcome": func() error {
			return repos.Audit.Append(ctx, &domain.AuditLog{Action: domain.AuditLogin, Outcome: "maybe"})
		},
		"AuditList/bad-actor": func() error {
			_, err := repos.Audit.List(ctx, domain.ListOptions{}, domain.AuditFilter{ActorID: badID})
			return err
		},
	}
	for name, call := range calls {
		err := call()
		require.Error(t, err, name)
	}
}

func TestPutObjectsEmptyIsNoOp(t *testing.T) {
	repos := New(nil) // must not touch the DB for empty input
	require.NoError(t, repos.Snapshots.PutObjects(context.Background(), nil))
}

func TestWithTimeout(t *testing.T) {
	ctx, cancel := withTimeout(context.Background())
	defer cancel()
	_, ok := ctx.Deadline()
	require.True(t, ok, "timeout applied without caller deadline")

	withDeadline, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	ctx2, cancel2 := withTimeout(withDeadline)
	defer cancel2()
	d, ok := ctx2.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(time.Minute), d, 5*time.Second)
}

func TestEncodeScheduleMissingFields(t *testing.T) {
	_, err := encodeSchedule(&domain.Schedule{Kind: domain.ScheduleOneTime})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = encodeSchedule(&domain.Schedule{Kind: domain.ScheduleHourly})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = encodeSchedule(&domain.Schedule{Kind: domain.ScheduleWeekly, TimeOfDay: "01:00"})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestMapCorruptRows(t *testing.T) {
	_, err := mapAgent(gen.Agent{Metadata: []byte("{oops")})
	require.Error(t, err)
	_, err = mapHeartbeat(gen.AgentHeartbeat{Payload: []byte("{oops")})
	require.Error(t, err)
	_, err = mapDiscoveredResource(gen.DiscoveredResource{Details: []byte("{oops")})
	require.NoError(t, err, "details tolerate corruption as empty")
	_, err = mapDestination(gen.StorageDestination{Auth: []byte("{oops")})
	require.Error(t, err)
	_, err = mapPolicyVersion(gen.BackupPolicyVersion{Config: []byte("{oops")})
	require.Error(t, err)
	_, err = mapPolicyTarget(gen.BackupPolicyTarget{TargetConfig: []byte("{oops")})
	require.Error(t, err)
	_, err = mapRestoreJob(gen.RestoreJob{SourcePaths: []byte("{oops")})
	require.Error(t, err)
	_, err = mapSnapshot(gen.Snapshot{State: "melted"})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = mapManifest(gen.SnapshotManifest{Content: []byte("{oops")})
	require.Error(t, err)
	_, err = mapAuditLog(gen.AuditLog{Details: []byte("{oops")})
	require.Error(t, err)
	_, err = mapBackupJob(gen.BackupJob{TargetResources: []byte(`{"trigger":123}`)})
	require.Error(t, err)
	_, err = mapSecret(gen.SecretProfile{SecretEncrypted: "!!!not-base64!!!"})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestMapWellFormedRows(t *testing.T) {
	j, err := mapBackupJob(gen.BackupJob{TargetResources: []byte("[]")})
	require.NoError(t, err)
	require.Equal(t, "", string(j.Trigger))

	s, err := mapSnapshot(gen.Snapshot{State: "valid", StoragePrefix: "postgres", SnapshotTime: timestamptz(time.Now())})
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotVerified, s.Status)
	require.Equal(t, domain.TargetPostgres, s.TargetType)
	require.NotNil(t, s.CompletedAt)

	s, err = mapSnapshot(gen.Snapshot{State: "pending", StoragePrefix: "/foreign/prefix"})
	require.NoError(t, err)
	require.Equal(t, domain.TargetType(""), s.TargetType, "foreign prefixes do not fabricate types")

	m, err := mapManifest(gen.SnapshotManifest{Content: []byte(`{"format_version":2,"object_count":9}`), ManifestSizeBytes: 12})
	require.NoError(t, err)
	require.Equal(t, 2, m.FormatVersion)
	require.Equal(t, 9, m.ObjectCount)
	require.Equal(t, uint64(12), m.BytesStored)

	obj := mapSnapshotObject("mid", gen.SnapshotObject{ObjectKey: "k", SizeBytes: 5, Checksum: "h", ChecksumAlg: "", PartIndex: 3})
	require.Equal(t, "mid", obj.ManifestID)
	require.Equal(t, "h", obj.Checksum)

	ident := mapIdentity(gen.UserExternalIdentity{Issuer: "i", Subject: "s"})
	require.Equal(t, "i", ident.Issuer)

	rec := mapRetention(gen.RetentionRule{ProtectLastValid: true})
	require.Equal(t, 1, rec.Rule.KeepMinimum)
	rec = mapRetention(gen.RetentionRule{})
	require.Equal(t, 0, rec.Rule.KeepMinimum)

	n := mapNotification(gen.Notification{Channel: "email", State: "pending"})
	require.Equal(t, domain.NotificationPending, n.Status)
}

func TestMapProviderRowWithoutRoleLookup(t *testing.T) {
	repos := New(nil) // invalid DefaultRoleID skips the DB entirely
	p, err := repos.OIDC.mapProviderRowCached(context.Background(), providerRow{
		Name: "p", Issuer: "iss", ClientID: "cid",
		Endpoints:     []byte(`{"auth_url":"https://a","client_secret_ref":"r"}`),
		ClaimsMapping: []byte(`{"group_claims":["g"],"group_role_mapping":{"g":"operator"}}`),
		Scopes:        "openid email",
	}, map[string]string{})
	require.NoError(t, err)
	require.Equal(t, "https://a", p.AuthURL)
	require.Equal(t, "r", p.ClientSecretRef)
	require.Equal(t, "", p.DefaultRole)
	require.Equal(t, []string{"openid", "email"}, p.Scopes)

	_, err = repos.OIDC.mapProviderRowCached(context.Background(), providerRow{Endpoints: []byte("{oops")}, map[string]string{})
	require.Error(t, err)
	_, err = repos.OIDC.mapProviderRowCached(context.Background(), providerRow{Endpoints: []byte("{}"), ClaimsMapping: []byte("{oops")}, map[string]string{})
	require.Error(t, err)
}

func TestRoleNameCached(t *testing.T) {
	repos := New(nil)
	id, err := parseUUID("6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944")
	require.NoError(t, err)
	name, err := repos.OIDC.roleNameCached(context.Background(), id, map[string]string{
		"6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944": "operator",
	})
	require.NoError(t, err)
	require.Equal(t, "operator", name)
}

func TestDiscoveryTypeBranches(t *testing.T) {
	require.Equal(t, "host_path", discoveryTypeToDB("bogus-kind"))
	require.Equal(t, domain.ResourceKindDockerVolume, discoveryTypeFromDB("volume", map[string]string{}))
	require.Equal(t, domain.ResourceKindDockerBindMount, discoveryTypeFromDB("bind_mount", map[string]string{}))
	require.Equal(t, domain.ResourceKindComposeProject, discoveryTypeFromDB("compose_project", map[string]string{}))
}

func TestPolicyTargetTypeBranches(t *testing.T) {
	require.Equal(t, "host_path", policyTargetTypeToDB("bogus"))
	require.Equal(t, domain.TargetDockerConfig, policyTargetTypeFromDB("container_meta", policyTargetConfig{}))
	require.Equal(t, domain.TargetPostgres, policyTargetTypeFromDB("postgres", policyTargetConfig{}))
	require.Equal(t, domain.TargetMySQL, policyTargetTypeFromDB("mysql", policyTargetConfig{}))
	require.Equal(t, domain.TargetMSSQL, policyTargetTypeFromDB("mssql", policyTargetConfig{}))
	require.Equal(t, domain.TargetRedis, policyTargetTypeFromDB("redis", policyTargetConfig{}))
	require.Equal(t, domain.TargetAppStack, policyTargetTypeFromDB("stack", policyTargetConfig{}))
	require.Equal(t, domain.TargetHostFilesystem, policyTargetTypeFromDB("host_path", policyTargetConfig{}))
	require.Equal(t, domain.TargetHostFilesystem, policyTargetTypeFromDB("bogus", policyTargetConfig{}))
}

func TestSaturatingHelpers(t *testing.T) {
	require.Equal(t, int32(math.MaxInt32), saturatingInt32(math.MaxInt64))
	require.Equal(t, int32(math.MinInt32), saturatingInt32(math.MinInt64))
	require.Equal(t, int32(7), saturatingInt32(7))
	require.Equal(t, int32(0), int32OrZero(-3))
	require.Equal(t, int32(math.MaxInt32), int32OrZero(math.MaxInt64))
	require.Equal(t, int64(0), int64OrZero(-1))
	require.Equal(t, int64(4), int64OrZero(4))
	require.Equal(t, int64(math.MaxInt64), uint64ToInt64(math.MaxUint64))
	require.Equal(t, uint64(0), uint64FromInt64(-2))
	require.Equal(t, "zstd", compressionAlgorithm(""))
	require.Equal(t, "lz4", compressionAlgorithm("lz4"))
	require.Equal(t, []string{}, nonNilStrings(nil))
	require.Equal(t, []domain.BackupPolicyTarget{}, nonNilTargets(nil))
	require.False(t, int4OrNull(math.MaxInt64).Valid)
}

func TestChecksumMultiColon(t *testing.T) {
	sum, alg := splitChecksum("a:b:c")
	require.Equal(t, "a", alg)
	require.Equal(t, "b:c", sum)
	require.Equal(t, "a:b:c", joinChecksum(sum, alg))
}

func TestFirstAgentID(t *testing.T) {
	u, err := firstAgentID(nil)
	require.NoError(t, err)
	require.False(t, u.Valid)
	_, err = firstAgentID([]string{"bogus"})
	require.ErrorIs(t, err, domain.ErrValidation)
	u, err = firstAgentID([]string{"6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", "bogus-second"})
	require.ErrorIs(t, err, domain.ErrValidation, "all entries are validated, not just the first")
	require.False(t, u.Valid)
}

func TestMergeAgentMetadata(t *testing.T) {
	_, err := mergeAgentMetadata([]byte("{oops"), nil, nil)
	require.Error(t, err)
	total := uint64(9)
	raw, err := mergeAgentMetadata([]byte("{}"), &total, nil)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"disk_total_bytes":9`)
}

func TestStackRepoContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repos := New(nil)
	require.Error(t, repos.Stacks.CreateStack(ctx, &domain.ApplicationStack{AgentID: "a", Name: "n"}))
	_, err := repos.Stacks.GetStack(ctx, "x")
	require.Error(t, err)
	require.Error(t, repos.Stacks.UpdateStack(ctx, &domain.ApplicationStack{ID: "x", AgentID: "a", Name: "n"}))
	require.Error(t, repos.Stacks.DeleteStack(ctx, "x"))
	_, err = repos.Stacks.ListStacks(ctx, "")
	require.Error(t, err)
	require.Error(t, repos.Stacks.SetStackResources(ctx, "x", nil))
	_, err = repos.Stacks.ListStackResources(ctx, "x")
	require.Error(t, err)
	require.Error(t, repos.Stacks.UpdateStack(context.Background(), &domain.ApplicationStack{AgentID: "a", Name: "n"}), "missing row")
}

func TestMapAuditOutcomeNormalization(t *testing.T) {
	entry, err := mapAuditLog(gen.AuditLog{Outcome: "success"})
	require.NoError(t, err)
	require.Equal(t, "ok", entry.Outcome)
	entry, err = mapAuditLog(gen.AuditLog{Outcome: "denied"})
	require.NoError(t, err)
	require.Equal(t, "denied", entry.Outcome)
}
