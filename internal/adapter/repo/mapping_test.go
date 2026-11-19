package repo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

func TestScheduleCodecRoundTrips(t *testing.T) {
	weekday := time.Wednesday
	runAt := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	next := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	cases := []domain.Schedule{
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleOneTime, RunAt: &runAt, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &runAt},
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleHourly, IntervalHours: 6, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &next},
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleDaily, TimeOfDay: "09:30", Timezone: "Asia/Jakarta", MaxConcurrency: 2, NextRunAt: &next},
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleWeekly, TimeOfDay: "23:15", Weekday: &weekday, Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &next},
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleCron, CronExpr: "15 2 * * 1", Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &next},
		{PolicyID: "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", Kind: domain.ScheduleDisabled, Timezone: "UTC", MaxConcurrency: 1},
	}
	for _, s := range cases {
		codec, err := encodeSchedule(&s)
		require.NoError(t, err, s.Kind)
		row := gen.Schedule{
			ScheduleType: codec.scheduleType, CronExpression: codec.cronExpression,
			IntervalMinutes: codec.intervalMinutes, Timezone: s.Timezone,
			NextRunAt: codec.nextRunAt, IsPaused: s.Paused,
			ConcurrencyLimit: int32OrZero(s.MaxConcurrency),
		}
		got, err := mapSchedule(row)
		require.NoError(t, err, s.Kind)
		require.Equal(t, s.Kind, got.Kind)
		switch s.Kind {
		case domain.ScheduleOneTime:
			require.NotNil(t, got.RunAt)
			require.True(t, got.RunAt.Equal(*s.RunAt))
		case domain.ScheduleHourly:
			require.Equal(t, s.IntervalHours, got.IntervalHours)
		case domain.ScheduleDaily:
			require.Equal(t, s.TimeOfDay, got.TimeOfDay)
		case domain.ScheduleWeekly:
			require.NotNil(t, got.Weekday)
			require.Equal(t, *s.Weekday, *got.Weekday)
			require.Equal(t, s.TimeOfDay, got.TimeOfDay)
		case domain.ScheduleCron:
			require.Equal(t, s.CronExpr, got.CronExpr)
		case domain.ScheduleDisabled:
			require.Nil(t, got.NextRunAt, "disabled schedules are never due")
		}
	}
}

func TestScheduleCodecRejectsUnknownKind(t *testing.T) {
	s := domain.Schedule{Kind: "fortnightly"}
	_, err := encodeSchedule(&s)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = mapSchedule(gen.Schedule{ScheduleType: "fortnightly"})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestScheduleCodecRejectsCorruptPayloads(t *testing.T) {
	_, err := mapSchedule(gen.Schedule{ScheduleType: "once", CronExpression: textOrNull("v1:once:not-a-time")})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = mapSchedule(gen.Schedule{ScheduleType: "weekly", CronExpression: textOrNull("v1:weekly:9:99:99")})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = mapSchedule(gen.Schedule{ScheduleType: "daily", CronExpression: textOrNull("v1:nope:x")})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestChecksumSplitJoin(t *testing.T) {
	sum, alg := splitChecksum("sha256:abc123")
	require.Equal(t, "abc123", sum)
	require.Equal(t, "sha256", alg)
	require.Equal(t, "sha256:abc123", joinChecksum(sum, alg))

	sum, alg = splitChecksum("barevalue")
	require.Equal(t, "barevalue", sum)
	require.Equal(t, "", alg)
	require.Equal(t, "barevalue", joinChecksum(sum, alg))
}

func TestSnapshotStatusMapping(t *testing.T) {
	pairs := map[domain.SnapshotStatus]string{
		domain.SnapshotPending:   "pending",
		domain.SnapshotVerifying: "verifying",
		domain.SnapshotVerified:  "valid",
		domain.SnapshotFailed:    "invalid",
		domain.SnapshotExpired:   "expired",
	}
	for dom, db := range pairs {
		got, err := snapshotStateToDB(dom)
		require.NoError(t, err)
		require.Equal(t, db, got)
		back, err := snapshotStateFromDB(db)
		require.NoError(t, err)
		require.Equal(t, dom, back)
	}
	deleted, err := snapshotStateFromDB("deleted")
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotExpired, deleted)
	_, err = snapshotStateToDB("melted")
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = snapshotStateFromDB("melted")
	require.ErrorIs(t, err, domain.ErrValidation)
	// Empty status defaults to pending on write.
	def, err := snapshotStateToDB("")
	require.NoError(t, err)
	require.Equal(t, "pending", def)
}

func TestDiscoveryKindRoundTrips(t *testing.T) {
	kinds := []domain.ResourceKind{
		domain.ResourceKindHostPath, domain.ResourceKindMount,
		domain.ResourceKindDockerContainer, domain.ResourceKindDockerVolume,
		domain.ResourceKindDockerBindMount, domain.ResourceKindComposeProject,
		domain.ResourceKindPostgresDatabase, domain.ResourceKindMySQLDatabase,
		domain.ResourceKindMSSQLDatabase, domain.ResourceKindRedisDataset,
	}
	for _, kind := range kinds {
		dbType := discoveryTypeToDB(kind)
		raw, err := encodeDiscoveryDetails(kind, map[string]string{"a": "b"})
		require.NoError(t, err)
		detail := stringMap(raw)
		require.Equal(t, "b", detail["a"])
		require.Equal(t, kind, discoveryTypeFromDB(dbType, detail))
	}
	// Legacy rows without a marker still decode to a sane default.
	require.Equal(t, domain.ResourceKindDockerContainer, discoveryTypeFromDB("container", map[string]string{}))
	require.Equal(t, domain.ResourceKindHostPath, discoveryTypeFromDB("unknown", map[string]string{}))
}

func TestPolicyTargetTypeRoundTrips(t *testing.T) {
	types := []domain.TargetType{
		domain.TargetHostFilesystem, domain.TargetDockerConfig, domain.TargetDockerVolume,
		domain.TargetPostgres, domain.TargetMySQL, domain.TargetMSSQL,
		domain.TargetRedis, domain.TargetAppStack,
	}
	for _, typ := range types {
		dbType := policyTargetTypeToDB(typ)
		raw, err := encodePolicyTargetConfig(&domain.BackupPolicyTarget{
			Type: typ, Method: domain.BackupMethodFull, Selector: "/x",
			Include: []string{"*.go"}, Options: map[string]string{"k": "v"},
			CredentialRef: "cred-1",
		})
		require.NoError(t, err)
		var config policyTargetConfig
		require.NoError(t, decodeJSON(raw, &config))
		require.Equal(t, typ, policyTargetTypeFromDB(dbType, config))
		require.Equal(t, "cred-1", config.CredentialRef)
	}
	require.Equal(t, domain.TargetDockerVolume, policyTargetTypeFromDB("bind_mount", policyTargetConfig{}))
}

func TestBackupJobExtrasRoundTrip(t *testing.T) {
	j := &domain.BackupJob{Trigger: domain.JobTriggerSchedule, StorageID: "s1", RetryOf: "r1", CreatedBy: "u1"}
	raw, err := encodeBackupJobExtras(j)
	require.NoError(t, err)
	extras, err := decodeBackupJobExtras(raw)
	require.NoError(t, err)
	require.Equal(t, "schedule", extras.Trigger)
	require.Equal(t, "s1", extras.StorageID)
	require.Equal(t, "r1", extras.RetryOf)
	require.Equal(t, "u1", extras.CreatedBy)

	empty, err := encodeBackupJobExtras(&domain.BackupJob{})
	require.NoError(t, err)
	require.Equal(t, []byte("[]"), empty)
	decoded, err := decodeBackupJobExtras(empty)
	require.NoError(t, err)
	require.Equal(t, backupJobExtras{}, decoded)
	decoded, err = decodeBackupJobExtras(nil)
	require.NoError(t, err)
	require.Equal(t, backupJobExtras{}, decoded)
}

func TestSecretKindRoundTrip(t *testing.T) {
	for _, kind := range []domain.SecretKind{
		domain.SecretKindDatabase, domain.SecretKindStorage,
		domain.SecretKindEncryption, domain.SecretKindGeneric,
	} {
		owner := secretKindToOwner(kind)
		require.Contains(t, []string{"database", "storage", "env"}, owner)
		require.Equal(t, kind, secretKindFromRow(owner, string(kind)))
	}
	require.Equal(t, domain.SecretKindGeneric, secretKindFromRow("env", ""))
	require.Equal(t, domain.SecretKindDatabase, secretKindFromRow("database", ""))
}

func TestAuditOutcomeMapping(t *testing.T) {
	for _, ok := range []string{"", "ok", "success"} {
		db, err := auditOutcomeToDB(ok)
		require.NoError(t, err)
		require.Equal(t, "success", db)
	}
	db, err := auditOutcomeToDB("denied")
	require.NoError(t, err)
	require.Equal(t, "denied", db)
	_, err = auditOutcomeToDB("maybe")
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestAgentStatusForUpdate(t *testing.T) {
	require.Equal(t, "revoked", agentStatusForUpdate("online", true))
	require.Equal(t, "revoked", agentStatusForUpdate("revoked", true))
	require.Equal(t, "online", agentStatusForUpdate("online", false))
	require.Equal(t, "offline", agentStatusForUpdate("revoked", false))
	require.Equal(t, "offline", agentStatusForUpdate("disabled", false))
	require.Equal(t, "pending", agentStatusForUpdate("", false))
}

func TestMapUserAndRole(t *testing.T) {
	u := mapUser(gen.User{Email: "a@b.c", DisplayName: "A", IsActive: true})
	require.Equal(t, "a@b.c", u.Username)
	require.Equal(t, "a@b.c", u.Email)
	require.False(t, u.Disabled)
	u = mapUser(gen.User{Email: "a@b.c", IsActive: false})
	require.True(t, u.Disabled)
	r := mapRole(gen.Role{Name: "operator", Description: "d"})
	require.Equal(t, "operator", r.Name)
}

func TestMapCredential(t *testing.T) {
	c := mapCredential(gen.AgentCredential{CredentialHash: "h", Label: "enrollment"})
	require.True(t, c.Enrollment)
	require.False(t, c.Revoked)
	c = mapCredential(gen.AgentCredential{CredentialHash: "h", RevokedAt: timestamptz(time.Now())})
	require.True(t, c.Revoked)
	require.False(t, c.Enrollment)
}

func TestMapHeartbeat(t *testing.T) {
	h, err := mapHeartbeat(gen.AgentHeartbeat{DiskTotalBytes: 100, DiskFreeBytes: 40, RunningJobs: 2, Payload: []byte(`{"docker_available":true}`)})
	require.NoError(t, err)
	require.NotNil(t, h.DiskTotalBytes)
	require.Equal(t, uint64(100), *h.DiskTotalBytes)
	require.True(t, h.DockerAvailable)
	h, err = mapHeartbeat(gen.AgentHeartbeat{})
	require.NoError(t, err)
	require.Nil(t, h.DiskTotalBytes)
	require.False(t, h.DockerAvailable)
}

func TestMapAgent(t *testing.T) {
	row := gen.Agent{
		Hostname: "h1", Os: "linux", Arch: "amd64", AgentVersion: "v1",
		Status: "online", Capabilities: []byte(`["docker"]`),
		Metadata: []byte(`{"disk_total_bytes":10,"disk_free_bytes":4}`),
	}
	a, err := mapAgent(row)
	require.NoError(t, err)
	require.Equal(t, "h1", a.Hostname)
	require.False(t, a.Disabled)
	require.Equal(t, []string{"docker"}, a.Capabilities)
	require.NotNil(t, a.DiskFreeBytes)
	row.Status = "disabled"
	a, err = mapAgent(row)
	require.NoError(t, err)
	require.True(t, a.Disabled)
}

func TestOIDCCodecs(t *testing.T) {
	//nolint:gosec // test fixture reference, not a real credential
	p := &domain.OIDCProvider{
		AuthURL: "https://idp/auth", TokenURL: "https://idp/token",
		ClientSecretRef: "ref-1", Scopes: []string{"openid", "email"},
		GroupClaimNames: []string{"groups"}, GroupRoleMapping: map[string]string{"g": "operator"},
	}
	raw, err := encodeOIDCEndpoints(p)
	require.NoError(t, err)
	var endpoints oidcEndpoints
	require.NoError(t, decodeJSON(raw, &endpoints))
	require.Equal(t, "https://idp/auth", endpoints.AuthURL)
	require.Equal(t, "ref-1", endpoints.ClientSecretRef)
	raw, err = encodeOIDCMapping(p)
	require.NoError(t, err)
	var mapping oidcClaimsMapping
	require.NoError(t, decodeJSON(raw, &mapping))
	require.Equal(t, []string{"groups"}, mapping.GroupClaims)
	require.Equal(t, "operator", mapping.GroupRoleMapping["g"])
	require.Equal(t, "openid email", oidcScopesString(p.Scopes))
	require.Equal(t, "openid profile email", oidcScopesString(nil))
	require.Equal(t, []string{"openid", "email"}, oidcScopesSlice("openid email"))
	require.Nil(t, oidcScopesSlice("  "))
}

func TestRestoreTypeDerivation(t *testing.T) {
	require.Equal(t, "file", restoreTypeFor(&domain.RestoreJob{Paths: []string{"a"}}))
	require.Equal(t, "directory", restoreTypeFor(&domain.RestoreJob{}))
}

func TestNewExposesEveryPort(t *testing.T) {
	r := New(nil)
	require.NotNil(t, r.Users)
	require.NotNil(t, r.OIDC)
	require.NotNil(t, r.Agents)
	require.NotNil(t, r.Discovery)
	require.NotNil(t, r.Storage)
	require.NotNil(t, r.Policies)
	require.NotNil(t, r.Retention)
	require.NotNil(t, r.Schedules)
	require.NotNil(t, r.BackupJobs)
	require.NotNil(t, r.RestoreJobs)
	require.NotNil(t, r.Snapshots)
	require.NotNil(t, r.Stacks)
	require.NotNil(t, r.Notifications)
	require.NotNil(t, r.Audit)
}

func TestStackRepoRoundTrip(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	a := &domain.Agent{Hostname: "stack-" + suffix}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))

	s := &domain.ApplicationStack{AgentID: a.ID, Name: "web-" + suffix, Description: "d", ComposePath: "/srv/docker-compose.yml"}
	require.NoError(t, repos.Stacks.CreateStack(ctx, s))
	require.NotEmpty(t, s.ID)
	require.False(t, s.CreatedAt.IsZero())

	got, err := repos.Stacks.GetStack(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, "web-"+suffix, got.Name)
	require.Equal(t, a.ID, got.AgentID)

	s.Description = "updated"
	require.NoError(t, repos.Stacks.UpdateStack(ctx, s))
	got, err = repos.Stacks.GetStack(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, "updated", got.Description)

	require.NoError(t, repos.Stacks.SetStackResources(ctx, s.ID, []domain.ApplicationStackResource{
		{ResourceID: "res-2", Selector: "/logs", Type: domain.TargetHostFilesystem, RestoreOrder: 2},
		{ResourceID: "res-1", Selector: "/data", Type: domain.TargetHostFilesystem, RestoreOrder: 1, Required: true},
	}))
	members, err := repos.Stacks.ListStackResources(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, s.ID, members[0].StackID)
	require.Equal(t, "res-1", members[0].ResourceID, "ordered by restore_order")
	require.True(t, members[0].Required)

	// Replace clears removed members.
	require.NoError(t, repos.Stacks.SetStackResources(ctx, s.ID, nil))
	members, err = repos.Stacks.ListStackResources(ctx, s.ID)
	require.NoError(t, err)
	require.Empty(t, members)

	stacks, err := repos.Stacks.ListStacks(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, stacks, 1)

	all, err := repos.Stacks.ListStacks(ctx, "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 1)

	require.NoError(t, repos.Stacks.DeleteStack(ctx, s.ID))
	_, err = repos.Stacks.GetStack(ctx, s.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Stacks.ListStackResources(ctx, s.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMapStackHelpers(t *testing.T) {
	id, err := parseUUID("6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944")
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := mapStack(gen.ApplicationStack{
		ID: id, AgentID: id, Name: "web", Description: "d",
		ComposePath: "/c", CreatedAt: timestamptz(now), UpdatedAt: timestamptz(now),
	})
	require.Equal(t, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", s.ID)
	require.Equal(t, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", s.AgentID)
	require.Equal(t, "web", s.Name)
	require.True(t, now.Equal(s.CreatedAt))
	require.NotNil(t, mapStacks(nil))
	require.Empty(t, mapStacks(nil))

	members := mapStackResources(s.ID, []gen.ApplicationStackResource{
		{ResourceID: "res-1", Selector: "/data", ResourceType: "host_filesystem", RestoreOrder: 3, Required: true},
	})
	require.Len(t, members, 1)
	require.Equal(t, s.ID, members[0].StackID)
	require.Equal(t, domain.TargetHostFilesystem, members[0].Type)
	require.Equal(t, 3, members[0].RestoreOrder)
	require.True(t, members[0].Required)
	require.NotNil(t, mapStackResources(s.ID, nil))
}

func TestStackRepoValidation(t *testing.T) {
	ctx := context.Background()
	repos := New(nil)
	require.Error(t, repos.Stacks.CreateStack(ctx, &domain.ApplicationStack{}))
	require.ErrorIs(t, func() error {
		_, err := repos.Stacks.GetStack(ctx, "")
		return err
	}(), domain.ErrNotFound)
	require.Error(t, repos.Stacks.SetStackResources(ctx, "missing", nil))
}
