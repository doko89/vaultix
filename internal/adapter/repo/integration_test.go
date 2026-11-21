package repo

// Postgres integration tests for the repository bundle.
//
// These tests need a disposable database and are skipped unless
// TEST_DATABASE_URL is set, e.g.:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/vaultix_test?sslmode=disable \
//	  go test ./internal/adapter/repo/ -run TestIntegration -v
//
// The database is reset (public schema dropped) and migrated with a minimal
// goose-Up applier, so it must not contain anything valuable. testcontainers
// is intentionally not used: it is not in go.mod and this change adds no
// dependencies.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
	notifuc "github.com/vaultix/vaultix/internal/usecase/notification"
)

var (
	integrationOnce  sync.Once
	integrationRepos *Repos
	integrationErr   error
)

// openIntegrationRepos migrates the test database once and returns the repo
// bundle, or skips the test when TEST_DATABASE_URL is unset.
func openIntegrationRepos(t *testing.T) *Repos {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL unset; skipping Postgres integration test")
	}
	integrationOnce.Do(func() {
		integrationRepos, integrationErr = migrateTestDB(dsn)
	})
	require.NoError(t, integrationErr)
	return integrationRepos
}

func testCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func uniqueSuffix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")), time.Now().UnixNano())
}

// migrateTestDB resets the database, applies db/migrations Up sections in
// order, and returns the wired repository bundle.
func migrateTestDB(dsn string) (*Repos, error) {
	ctx, cancel := testCtx()
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// Reset to a clean slate; the target database must be disposable.
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE"); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA public"); err != nil {
		pool.Close()
		return nil, err
	}
	files, err := filepath.Glob("../../../db/migrations/*.sql")
	if err != nil {
		pool.Close()
		return nil, err
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f) //nolint:gosec // test fixture path
		if err != nil {
			pool.Close()
			return nil, err
		}
		up, err := gooseUpSection(string(body))
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, stmt := range splitStatements(up) {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				pool.Close()
				return nil, fmt.Errorf("%s: exec %q: %w", f, trunc(stmt, 80), err)
			}
		}
	}
	return New(pool), nil
}

func gooseUpSection(body string) (string, error) {
	up := strings.Index(body, "-- +goose Up")
	down := strings.Index(body, "-- +goose Down")
	if up < 0 || down < 0 || down < up {
		return "", fmt.Errorf("missing goose Up/Down annotations")
	}
	return body[up+len("-- +goose Up") : down], nil
}

// splitStatements splits an Up section on semicolons, dropping full-line
// comments. The Vaultix migrations are plain DDL/DML without semicolons in
// literals or dollar-quoted bodies, which this relies on.
func splitStatements(up string) []string {
	var stmts []string
	for _, chunk := range strings.Split(up, ";") {
		var lines []string
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			lines = append(lines, line)
		}
		stmt := strings.TrimSpace(strings.Join(lines, "\n"))
		if stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return stmts
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestIntegrationUsersAndRoles(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	u := &domain.User{Username: "u-" + suffix + "@example.com", Email: "u-" + suffix + "@example.com", DisplayName: "U"}
	require.NoError(t, repos.Users.CreateUser(ctx, u))
	require.NotEmpty(t, u.ID)

	got, err := repos.Users.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, u.Email, got.Email)
	require.Equal(t, u.Email, got.Username)

	byName, err := repos.Users.GetUserByUsername(ctx, u.Email)
	require.NoError(t, err)
	require.Equal(t, u.ID, byName.ID)

	got.DisplayName = "Updated"
	got.Disabled = true
	require.NoError(t, repos.Users.UpdateUser(ctx, got))
	got, err = repos.Users.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated", got.DisplayName)
	require.True(t, got.Disabled)

	page, err := repos.Users.ListUsers(ctx, domain.ListOptions{Limit: 10, Query: suffix})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)
	require.NotEmpty(t, page.Items)

	roles, err := repos.Users.ListRoles(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(roles), 3)

	operator, err := repos.Users.GetRoleByName(ctx, domain.RoleOperator)
	require.NoError(t, err)
	operatorByID, err := repos.Users.GetRole(ctx, operator.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleOperator, operatorByID.Name)
	require.NoError(t, repos.Users.GrantRole(ctx, u.ID, operator.ID, ""))
	mine, err := repos.Users.ListUserRoles(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, mine, 1)

	admin, err := repos.Users.GetRoleByName(ctx, domain.RoleAdministrator)
	require.NoError(t, err)
	require.NoError(t, repos.Users.ReplaceUserRoles(ctx, u.ID, []string{admin.ID, operator.ID}))
	mine, err = repos.Users.ListUserRoles(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, mine, 2)
	require.NoError(t, repos.Users.RevokeRole(ctx, u.ID, admin.ID))
	mine, err = repos.Users.ListUserRoles(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, mine, 1)

	_, err = repos.Users.GetUser(ctx, "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, domain.ErrNotFound)

	disabled := &domain.User{Username: "off-" + suffix + "@example.com", Email: "off-" + suffix + "@example.com", Disabled: true}
	require.NoError(t, repos.Users.CreateUser(ctx, disabled))
	gotDisabled, err := repos.Users.GetUser(ctx, disabled.ID)
	require.NoError(t, err)
	require.True(t, gotDisabled.Disabled)
}

func TestIntegrationOIDC(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	u := &domain.User{Username: "oidc-" + suffix + "@example.com", Email: "oidc-" + suffix + "@example.com"}
	require.NoError(t, repos.Users.CreateUser(ctx, u))

	//nolint:gosec // test fixture reference, not a real credential
	p := &domain.OIDCProvider{
		Name: "idp-" + suffix, Issuer: "https://idp-" + suffix + ".example.com",
		Enabled: true, ClientID: "cid", ClientSecretRef: "ref-1",
		Scopes: []string{"openid", "email"}, GroupClaimNames: []string{"groups"},
		GroupRoleMapping: map[string]string{"g": domain.RoleOperator},
		DefaultRole:      domain.RoleOperator, JITProvisioning: true,
		AuthURL: "https://idp/auth", TokenURL: "https://idp/token",
	}
	require.NoError(t, repos.OIDC.CreateProvider(ctx, p))
	require.NotEmpty(t, p.ID)

	got, err := repos.OIDC.GetProvider(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, p.Issuer, got.Issuer)
	require.Equal(t, "ref-1", got.ClientSecretRef)
	require.Equal(t, domain.RoleOperator, got.DefaultRole)
	require.Equal(t, []string{"openid", "email"}, got.Scopes)
	require.Equal(t, "https://idp/auth", got.AuthURL)

	byIssuer, err := repos.OIDC.GetProviderByIssuer(ctx, p.Issuer)
	require.NoError(t, err)
	require.Equal(t, p.ID, byIssuer.ID)

	got.Name = "renamed-" + suffix
	require.NoError(t, repos.OIDC.UpdateProvider(ctx, got))

	providers, err := repos.OIDC.ListProviders(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, providers)

	ident := &domain.UserExternalIdentity{
		UserID: u.ID, ProviderID: p.ID, Issuer: p.Issuer,
		Subject: "sub-" + suffix, Email: u.Email,
	}
	require.NoError(t, repos.OIDC.LinkIdentity(ctx, ident))
	require.NotEmpty(t, ident.ID)

	found, err := repos.OIDC.GetIdentity(ctx, p.Issuer, "sub-"+suffix)
	require.NoError(t, err)
	require.Equal(t, u.ID, found.UserID)

	mine, err := repos.OIDC.ListUserIdentities(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, mine, 1)

	found.Disabled = true
	require.NoError(t, repos.OIDC.UpdateIdentity(ctx, found))
	require.NoError(t, repos.OIDC.UnlinkIdentity(ctx, found.ID))
	mine, err = repos.OIDC.ListUserIdentities(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, mine)
}

func TestIntegrationAgents(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	diskTotal := uint64(1000)
	diskFree := uint64(400)
	a := &domain.Agent{
		Hostname: "host-" + suffix, IPAddress: "10.0.0.1", OS: "linux",
		Architecture: "amd64", Version: "v1", Capabilities: []string{"docker"},
		DiskTotalBytes: &diskTotal, DiskFreeBytes: &diskFree,
	}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))
	require.NotEmpty(t, a.ID)

	got, err := repos.Agents.GetAgent(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, a.Hostname, got.Hostname)
	require.Equal(t, []string{"docker"}, got.Capabilities)
	require.NotNil(t, got.DiskFreeBytes)

	got.Version = "v2"
	require.NoError(t, repos.Agents.UpdateAgent(ctx, got))
	got, err = repos.Agents.GetAgent(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "v2", got.Version)

	page, err := repos.Agents.ListAgents(ctx, domain.ListOptions{Limit: 10, Query: suffix})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)

	cred := &domain.AgentCredential{AgentID: a.ID, TokenHash: "hash-" + suffix}
	require.NoError(t, repos.Agents.StoreCredential(ctx, cred))
	require.NotEmpty(t, cred.ID)

	byHash, err := repos.Agents.GetCredentialByHash(ctx, cred.TokenHash)
	require.NoError(t, err)
	require.Equal(t, a.ID, byHash.AgentID)

	creds, err := repos.Agents.ListAgentCredentials(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, creds, 1)

	byHash.Revoked = true
	require.NoError(t, repos.Agents.UpdateCredential(ctx, byHash))
	byHash, err = repos.Agents.GetCredentialByHash(ctx, cred.TokenHash)
	require.NoError(t, err)
	require.True(t, byHash.Revoked)

	hb := &domain.AgentHeartbeat{
		AgentID: a.ID, Version: "v2", IPAddress: "10.0.0.2",
		DiskTotalBytes: &diskTotal, DiskFreeBytes: &diskFree,
		RunningJobs: 1, DockerAvailable: true,
	}
	require.NoError(t, repos.Agents.RecordHeartbeat(ctx, hb))
	require.NotEmpty(t, hb.ID)
	require.False(t, hb.ReportedAt.IsZero())

	latest, err := repos.Agents.LatestHeartbeat(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, hb.ID, latest.ID)
	require.True(t, latest.DockerAvailable)

	got, err = repos.Agents.GetAgent(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastHeartbeatAt)
}

func TestIntegrationDiscovery(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	a := &domain.Agent{Hostname: "disc-" + suffix}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))

	require.NoError(t, repos.Discovery.ReplaceAgentResources(ctx, a.ID, []domain.DiscoveredResource{
		{Kind: domain.ResourceKindPostgresDatabase, Name: "db-main", Detail: map[string]string{"size": "1g"}},
		{Kind: domain.ResourceKindMySQLDatabase, Name: "db-legacy", Detail: map[string]string{}},
		{Kind: domain.ResourceKindDockerVolume, Name: "vol-data", Detail: map[string]string{}},
	}))
	all, err := repos.Discovery.ListAgentResources(ctx, a.ID, "")
	require.NoError(t, err)
	require.Len(t, all, 3)

	pg, err := repos.Discovery.ListAgentResources(ctx, a.ID, domain.ResourceKindPostgresDatabase)
	require.NoError(t, err)
	require.Len(t, pg, 1)
	require.Equal(t, "1g", pg[0].Detail["size"])

	got, err := repos.Discovery.GetResource(ctx, pg[0].ID)
	require.NoError(t, err)
	require.Equal(t, domain.ResourceKindPostgresDatabase, got.Kind)

	require.NoError(t, repos.Discovery.ReplaceAgentResources(ctx, a.ID, []domain.DiscoveredResource{
		{Kind: domain.ResourceKindPostgresDatabase, Name: "db-main", Detail: map[string]string{"size": "2g"}},
	}))
	all, err = repos.Discovery.ListAgentResources(ctx, a.ID, "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "2g", all[0].Detail["size"])
}

func TestIntegrationStorageAndSecrets(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	d := &domain.StorageDestination{
		Name: "dest-" + suffix, Endpoint: "https://s3.example.com", Region: "us-east-1",
		Bucket: "bucket-" + suffix, Prefix: "p", CredentialRef: "cred-1",
		UsePathStyle: true, Default: true,
	}
	require.NoError(t, repos.Storage.CreateDestination(ctx, d))
	require.NotEmpty(t, d.ID)

	got, err := repos.Storage.GetDestination(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "cred-1", got.CredentialRef)
	require.True(t, got.UsePathStyle)
	require.True(t, got.Default)

	def, err := repos.Storage.GetDefaultDestination(ctx)
	require.NoError(t, err)
	require.Equal(t, d.ID, def.ID)

	got.Prefix = "p2"
	require.NoError(t, repos.Storage.UpdateDestination(ctx, got))

	d2 := &domain.StorageDestination{Name: "dest2-" + suffix, Bucket: "b2-" + suffix, Default: true}
	require.NoError(t, repos.Storage.CreateDestination(ctx, d2))
	def, err = repos.Storage.GetDefaultDestination(ctx)
	require.NoError(t, err)
	require.Equal(t, d2.ID, def.ID, "promoting a new default clears the old one")

	dests, err := repos.Storage.ListDestinations(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(dests), 2)

	s := &domain.SecretProfile{Name: "sec-" + suffix, Kind: domain.SecretKindEncryption, Ciphertext: []byte("cipher-bytes"), KeyRef: "k1"}
	require.NoError(t, repos.Storage.CreateSecret(ctx, s))
	require.NotEmpty(t, s.ID)

	full, err := repos.Storage.GetSecret(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, []byte("cipher-bytes"), full.Ciphertext)
	require.Equal(t, domain.SecretKindEncryption, full.Kind)

	listed, err := repos.Storage.ListSecrets(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	for _, item := range listed {
		require.Nil(t, item.Ciphertext, "list must redact ciphertext")
	}

	require.NoError(t, repos.Storage.DeleteSecret(ctx, s.ID))
	require.NoError(t, repos.Storage.DeleteDestination(ctx, d.ID))
	require.NoError(t, repos.Storage.DeleteDestination(ctx, d2.ID))
}

func TestIntegrationPoliciesAndSchedules(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	a := &domain.Agent{Hostname: "pol-" + suffix}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))

	p := &domain.BackupPolicy{
		Name: "pol-" + suffix, Description: "d", AgentIDs: []string{a.ID},
		Compression: domain.Compression{Algorithm: "zstd", Level: 3},
		Enabled:     true,
	}
	require.NoError(t, repos.Policies.CreatePolicy(ctx, p))
	require.NotEmpty(t, p.ID)
	require.Equal(t, 1, p.CurrentVersion)

	targets := []domain.BackupPolicyTarget{
		{Type: domain.TargetHostFilesystem, Method: domain.BackupMethodFull, Selector: "/data", Include: []string{"*.db"}},
		{Type: domain.TargetPostgres, Method: domain.BackupMethodLogicalDump, Selector: "main", CredentialRef: "db-cred"},
	}
	require.NoError(t, repos.Policies.ReplaceTargets(ctx, p.ID, targets))
	stored, err := repos.Policies.ListTargets(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, stored, 2)

	v := &domain.BackupPolicyVersion{
		PolicyID:  p.ID,
		Targets:   stored,
		Retention: domain.RetentionRule{KeepLastN: 7, KeepDays: 30, KeepMinimum: 1},
		Schedule:  domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "02:00", Timezone: "UTC"},
		Note:      "first",
	}
	require.NoError(t, repos.Policies.PublishVersion(ctx, v))
	require.Equal(t, 2, v.Version)

	got, err := repos.Policies.GetVersion(ctx, p.ID, 2)
	require.NoError(t, err)
	require.Len(t, got.Targets, 2)
	require.Equal(t, "first", got.Note)
	require.Equal(t, 7, got.Retention.KeepLastN)

	versions, err := repos.Policies.ListVersions(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	p.Description = "updated"
	require.NoError(t, repos.Policies.UpdatePolicy(ctx, p))
	fetchedPolicy, err := repos.Policies.GetPolicy(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "updated", fetchedPolicy.Description)
	require.Equal(t, []string{a.ID}, fetchedPolicy.AgentIDs)
	page, err := repos.Policies.ListPolicies(ctx, domain.ListOptions{Limit: 10, Query: suffix})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)

	rule, err := repos.Retention.UpsertRule(ctx, p.ID, domain.RetentionRule{KeepLastN: 5, KeepDays: 14})
	require.NoError(t, err)
	require.Equal(t, 5, rule.Rule.KeepLastN)
	fetched, err := repos.Retention.GetByPolicy(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, 14, fetched.Rule.KeepDays)
	require.NoError(t, repos.Retention.RecordRun(ctx, p.ID))

	next := time.Now().Add(-time.Minute).Truncate(time.Second)
	sched := &domain.Schedule{
		PolicyID: p.ID, Kind: domain.ScheduleDaily, TimeOfDay: "02:00",
		Timezone: "UTC", MaxConcurrency: 1, NextRunAt: &next,
	}
	require.NoError(t, repos.Schedules.CreateSchedule(ctx, sched))
	require.NotEmpty(t, sched.ID)

	fetchedSched, err := repos.Schedules.GetSchedule(ctx, sched.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ScheduleDaily, fetchedSched.Kind)
	require.Equal(t, "02:00", fetchedSched.TimeOfDay)

	due, err := repos.Schedules.DueSchedules(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, due)

	future := time.Now().Add(time.Hour)
	fetchedSched.NextRunAt = &future
	require.NoError(t, repos.Schedules.UpdateSchedule(ctx, fetchedSched))

	byPolicy, err := repos.Schedules.ListSchedules(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, byPolicy, 1)
	require.NoError(t, repos.Schedules.DeleteSchedule(ctx, sched.ID))

	require.NoError(t, repos.Retention.DeleteRule(ctx, p.ID))
	_, err = repos.Retention.GetByPolicy(ctx, p.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)

	require.NoError(t, repos.Policies.DeletePolicy(ctx, p.ID))
	_, err = repos.Policies.GetPolicy(ctx, p.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestIntegrationBackupJobs(t *testing.T) {
	repos, agentID, policyID := openJobsFixture(t)
	ctx, cancel := testCtx()
	defer cancel()

	j := &domain.BackupJob{
		AgentID: agentID, PolicyID: policyID, PolicyVersion: 1,
		Trigger: domain.JobTriggerManual, Encrypted: true, KeyRef: "k1",
	}
	require.NoError(t, repos.BackupJobs.CreateJob(ctx, j))
	require.NotEmpty(t, j.ID)
	require.Equal(t, domain.JobQueued, j.State)

	got, err := repos.BackupJobs.GetJob(ctx, j.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobTriggerManual, got.Trigger)
	require.True(t, got.Encrypted)

	claimed, err := repos.BackupJobs.ClaimNextJob(ctx, agentID)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, domain.JobAssigned, claimed.State)

	empty, err := repos.BackupJobs.ClaimNextJob(ctx, agentID)
	require.NoError(t, err)
	require.Nil(t, empty, "empty queue claims (nil, nil)")

	progress := 0.5
	claimed.State = domain.JobRunning
	claimed.Progress = &progress
	require.NoError(t, repos.BackupJobs.UpdateJob(ctx, claimed))

	page, err := repos.BackupJobs.ListJobs(ctx, domain.ListOptions{Limit: 10}, domain.JobFilter{AgentID: agentID})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)
	byState, err := repos.BackupJobs.ListJobs(ctx, domain.ListOptions{Limit: 10}, domain.JobFilter{State: domain.JobRunning})
	require.NoError(t, err)
	require.GreaterOrEqual(t, byState.Total, 1)
}

func TestIntegrationRestoreJobs(t *testing.T) {
	repos, agentID, _ := openJobsFixture(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	snap := &domain.Snapshot{AgentID: agentID, Selector: "snap-" + suffix, Status: domain.SnapshotVerified, TargetType: domain.TargetHostFilesystem}
	require.NoError(t, repos.Snapshots.CreateSnapshot(ctx, snap))

	r := &domain.RestoreJob{
		AgentID: agentID, SnapshotID: snap.ID, TargetPath: "/restore",
		Paths: []string{"a", "b"}, CreatedBy: "",
	}
	require.NoError(t, repos.RestoreJobs.CreateJob(ctx, r))
	require.NotEmpty(t, r.ID)

	got, err := repos.RestoreJobs.GetJob(ctx, r.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, got.Paths)

	// Actor names (not UUIDs) must round-trip: execute records the
	// confirming actor's display name in requested_by.
	named := &domain.RestoreJob{
		AgentID: agentID, SnapshotID: snap.ID, CreatedBy: "op-" + suffix,
	}
	require.NoError(t, repos.RestoreJobs.CreateJob(ctx, named))
	gotNamed, err := repos.RestoreJobs.GetJob(ctx, named.ID)
	require.NoError(t, err)
	require.Equal(t, "op-"+suffix, gotNamed.CreatedBy)

	got.State = domain.JobRunning
	require.NoError(t, repos.RestoreJobs.UpdateJob(ctx, got))

	page, err := repos.RestoreJobs.ListJobs(ctx, domain.ListOptions{Limit: 10}, agentID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)

	approver := &domain.User{Username: "appr-" + suffix + "@example.com", Email: "appr-" + suffix + "@example.com"}
	require.NoError(t, repos.Users.CreateUser(ctx, approver))
	approved := &domain.RestoreJob{
		AgentID: agentID, SnapshotID: snap.ID, CreatedBy: approver.ID, ApprovedBy: approver.ID,
	}
	require.NoError(t, repos.RestoreJobs.CreateJob(ctx, approved))
	gotApproved, err := repos.RestoreJobs.GetJob(ctx, approved.ID)
	require.NoError(t, err)
	require.Equal(t, approver.ID, gotApproved.ApprovedBy)
}

func TestIntegrationNotificationRules(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	created, err := repos.Rules.CreateRule(ctx, notifuc.Rule{
		Name:         "hook-" + suffix,
		Events:       []string{"job_failed"},
		Channel:      domain.NotificationWebhook,
		Recipient:    "https://hooks.example/x",
		Headers:      map[string]string{"Authorization": "Bearer t"},
		BodyTemplate: `{"text":"{{.subject}}"}`,
		Enabled:      true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)

	got, err := repos.Rules.GetRule(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Bearer t", got.Headers["Authorization"])
	require.Equal(t, `{"text":"{{.subject}}"}`, got.BodyTemplate)

	listed, err := repos.Rules.ListRules(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, listed)

	created.Name = "hook-renamed-" + suffix
	updated, err := repos.Rules.UpdateRule(ctx, created.ID, created)
	require.NoError(t, err)
	require.Equal(t, "hook-renamed-"+suffix, updated.Name)

	require.NoError(t, repos.Rules.DeleteRule(ctx, created.ID))
	_, err = repos.Rules.GetRule(ctx, created.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestIntegrationSnapshots(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	a := &domain.Agent{Hostname: "snap-" + suffix}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))

	stored := uint64(2048)
	s := &domain.Snapshot{
		AgentID: a.ID, Selector: "/data", Status: domain.SnapshotPending,
		TargetType: domain.TargetHostFilesystem, BytesStored: &stored, ObjectCount: 3,
	}
	require.NoError(t, repos.Snapshots.CreateSnapshot(ctx, s))
	require.NotEmpty(t, s.ID)
	require.NotNil(t, s.CompletedAt)

	m := &domain.SnapshotManifest{
		SnapshotID: s.ID, FormatVersion: 1, ObjectKey: "snap/" + suffix + "/manifest.json",
		Checksum: "sha256:abc", ObjectCount: 3, BytesStored: 128,
	}
	require.NoError(t, repos.Snapshots.PutManifest(ctx, m))
	require.NotEmpty(t, m.ID)

	fetchedManifest, err := repos.Snapshots.GetManifest(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, fetchedManifest.ID)
	require.Equal(t, "sha256:abc", fetchedManifest.Checksum)
	require.Equal(t, 1, fetchedManifest.FormatVersion)

	objects := []domain.SnapshotObject{
		{ManifestID: m.ID, SnapshotID: s.ID, ObjectKey: "chunk/0", Kind: "chunk", Size: 64, Checksum: "sha256:aa", Sequence: 0},
		{ManifestID: m.ID, SnapshotID: s.ID, ObjectKey: "chunk/1", Kind: "chunk", Size: 64, Checksum: "sha256:bb", Sequence: 1},
	}
	require.NoError(t, repos.Snapshots.PutObjects(ctx, objects))
	require.NotEmpty(t, objects[0].ID)

	listed, err := repos.Snapshots.ListObjects(ctx, m.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, "sha256:aa", listed[0].Checksum)

	got, err := repos.Snapshots.GetSnapshot(ctx, s.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ManifestID)
	require.Equal(t, domain.TargetHostFilesystem, got.TargetType)

	s.Status = domain.SnapshotVerified
	require.NoError(t, repos.Snapshots.UpdateSnapshot(ctx, s))

	page, err := repos.Snapshots.ListSnapshots(ctx, domain.ListOptions{Limit: 10}, domain.SnapshotFilter{AgentID: a.ID, Restorable: true})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)

	byType, err := repos.Snapshots.ListSnapshots(ctx, domain.ListOptions{Limit: 10}, domain.SnapshotFilter{TargetType: domain.TargetPostgres})
	require.NoError(t, err)
	require.Equal(t, 0, byType.Total)

	require.NoError(t, repos.Snapshots.DeleteSnapshot(ctx, s.ID))
	_, err = repos.Snapshots.GetSnapshot(ctx, s.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestIntegrationNotificationsAndAudit(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()

	n := &domain.Notification{
		Channel: domain.NotificationEmail, Recipient: "ops@example.com",
		Subject: "job failed", Body: "details", RefType: "backup_job",
	}
	require.NoError(t, repos.Notifications.CreateNotification(ctx, n))
	require.NotEmpty(t, n.ID)

	pending, err := repos.Notifications.ListPending(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, pending)

	n.Status = domain.NotificationSent
	n.Attempts = 1
	require.NoError(t, repos.Notifications.UpdateNotification(ctx, n))
	require.NotNil(t, n.SentAt)

	auditSuffix := uniqueSuffix(t)
	u := &domain.User{Username: "audit-" + auditSuffix + "@example.com", Email: "audit-" + auditSuffix + "@example.com"}
	require.NoError(t, repos.Users.CreateUser(ctx, u))
	entry := domain.NewAuditLog(time.Now(), "user", u.ID, domain.AuditLogin, "user", u.ID, "ok", "login ok")
	require.NoError(t, repos.Audit.Append(ctx, &entry))
	require.NotEmpty(t, entry.ID)

	page, err := repos.Audit.List(ctx, domain.ListOptions{Limit: 10}, domain.AuditFilter{ActorID: u.ID})
	require.NoError(t, err)
	require.GreaterOrEqual(t, page.Total, 1)
	require.Equal(t, "ok", page.Items[0].Outcome)
	require.Equal(t, "login ok", page.Items[0].Detail)
}

func TestIntegrationNotFoundAndConflict(t *testing.T) {
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	missing := "00000000-0000-0000-0000-000000000000"

	_, err := repos.Policies.GetPolicy(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.Policies.DeletePolicy(ctx, missing), domain.ErrNotFound)
	_, err = repos.Schedules.GetSchedule(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.Schedules.DeleteSchedule(ctx, missing), domain.ErrNotFound)
	_, err = repos.BackupJobs.GetJob(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.BackupJobs.UpdateJob(ctx, &domain.BackupJob{ID: missing, State: domain.JobQueued}), domain.ErrNotFound)
	_, err = repos.RestoreJobs.GetJob(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Snapshots.GetSnapshot(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Snapshots.GetManifest(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Snapshots.ListObjects(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.Snapshots.DeleteSnapshot(ctx, missing), domain.ErrNotFound)
	_, err = repos.Storage.GetDestination(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.Storage.DeleteDestination(ctx, missing), domain.ErrNotFound)
	_, err = repos.Storage.GetSecret(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.Storage.DeleteSecret(ctx, missing), domain.ErrNotFound)
	_, err = repos.Agents.GetCredentialByHash(ctx, "no-such-hash")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Discovery.GetResource(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = repos.Users.GetRole(ctx, missing)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, repos.OIDC.UnlinkIdentity(ctx, missing), domain.ErrNotFound)
	require.ErrorIs(t, repos.Notifications.UpdateNotification(ctx, &domain.Notification{ID: missing, Status: domain.NotificationSent}), domain.ErrNotFound)

	// Dangling foreign keys surface as conflicts, never raw SQL errors.
	require.ErrorIs(t, repos.Users.GrantRole(ctx, missing, missing, ""), domain.ErrConflict)
	require.ErrorIs(t, repos.BackupJobs.CreateJob(ctx, &domain.BackupJob{AgentID: missing}), domain.ErrConflict)
}

// openJobsFixture builds the agent+policy pair job tests need.
func openJobsFixture(t *testing.T) (*Repos, string, string) {
	t.Helper()
	repos := openIntegrationRepos(t)
	ctx, cancel := testCtx()
	defer cancel()
	suffix := uniqueSuffix(t)

	a := &domain.Agent{Hostname: "job-" + suffix}
	require.NoError(t, repos.Agents.CreateAgent(ctx, a))
	p := &domain.BackupPolicy{Name: "jobpol-" + suffix, AgentIDs: []string{a.ID}, Enabled: true}
	require.NoError(t, repos.Policies.CreatePolicy(ctx, p))
	return repos, a.ID, p.ID
}
