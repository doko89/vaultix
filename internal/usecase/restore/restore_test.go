package restore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---------- fakes ----------

type fakeSnapshots struct {
	mu sync.Mutex
	m  map[string]*domain.Snapshot
}

func (f *fakeSnapshots) seed(s *domain.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*domain.Snapshot{}
	}
	f.m[s.ID] = s
}
func (f *fakeSnapshots) CreateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.seed(s)
	return nil
}
func (f *fakeSnapshots) GetSnapshot(_ context.Context, id string) (*domain.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (f *fakeSnapshots) UpdateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.seed(s)
	return nil
}
func (f *fakeSnapshots) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
	return nil
}
func (f *fakeSnapshots) ListSnapshots(_ context.Context, _ domain.ListOptions, _ domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	return domain.ListResult[domain.Snapshot]{}, nil
}
func (f *fakeSnapshots) PutManifest(_ context.Context, _ *domain.SnapshotManifest) error {
	return nil
}
func (f *fakeSnapshots) GetManifest(_ context.Context, _ string) (*domain.SnapshotManifest, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeSnapshots) PutObjects(_ context.Context, _ []domain.SnapshotObject) error {
	return nil
}
func (f *fakeSnapshots) ListObjects(_ context.Context, _ string) ([]domain.SnapshotObject, error) {
	return nil, nil
}

type fakeReporter struct {
	mu sync.Mutex
	m  map[string]*domain.RestoreJob
}

func (f *fakeReporter) CreateRestoreJob(_ context.Context, j *domain.RestoreJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*domain.RestoreJob{}
	}
	f.m[j.ID] = j
	return nil
}
func (f *fakeReporter) UpdateRestoreJob(_ context.Context, j *domain.RestoreJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[j.ID] = j
	return nil
}
func (f *fakeReporter) GetRestoreJob(_ context.Context, id string) (*domain.RestoreJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return j, nil
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---------- helpers ----------

func verifiedSnap(id string, target domain.TargetType, selector string) *domain.Snapshot {
	return &domain.Snapshot{ID: id, AgentID: "agent-1", PolicyID: "pol-1",
		TargetType: target, Selector: selector, Status: domain.SnapshotVerified, ManifestID: "man-" + id}
}

func testService(snaps ...*domain.Snapshot) (*Service, *fakeReporter, *mutableClock) {
	fs := &fakeSnapshots{}
	for _, s := range snaps {
		fs.seed(s)
	}
	rep := &fakeReporter{}
	clock := &mutableClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	var n int
	s, err := New(Deps{
		Snapshots: fs, Reporter: rep, Clock: clock,
		IDGen:    func() string { n++; return fmt.Sprintf("id-%d", n) },
		TokenGen: func() string { n++; return fmt.Sprintf("tok-%d", n) },
	})
	if err != nil {
		panic(err)
	}
	return s, rep, clock
}

// ---------- gating ----------

func TestPlan_RefusesUnverifiedSnapshots(t *testing.T) {
	for _, status := range []domain.SnapshotStatus{
		domain.SnapshotPending, domain.SnapshotVerifying, domain.SnapshotFailed, domain.SnapshotExpired,
	} {
		s, _, _ := testService(&domain.Snapshot{ID: "s-1", AgentID: "a", TargetType: domain.TargetHostFilesystem,
			Selector: "/srv", Status: status, ManifestID: "man-1"})
		_, err := s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
		require.ErrorIs(t, err, ErrNotRestorable, "status %q must not be restorable", status)
	}
	// Verified but without a manifest is equally unrestorable.
	s, _, _ := testService(&domain.Snapshot{ID: "s-1", AgentID: "a", TargetType: domain.TargetHostFilesystem,
		Selector: "/srv", Status: domain.SnapshotVerified})
	_, err := s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
	require.ErrorIs(t, err, ErrNotRestorable)
}

func TestPlan_FileOverwriteWarningAndAlternateDestination(t *testing.T) {
	s, _, _ := testService(verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv/data"))

	inPlace, err := s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
	require.NoError(t, err)
	require.False(t, inPlace.AlternateDestination)
	require.Contains(t, strings.Join(inPlace.Warnings, "\n"), "OVERWRITE")

	alt, err := s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", Destination: "/tmp/restore", Paths: []string{"/srv/data/a"}, RequestedBy: "op"})
	require.NoError(t, err)
	require.True(t, alt.AlternateDestination)
	require.Equal(t, "/tmp/restore", alt.Steps[0].Destination)
	require.Equal(t, []string{"/srv/data/a"}, alt.Paths)
	require.NotContains(t, strings.Join(alt.Warnings, "\n"), "OVERWRITE")

	_, err = s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", Paths: []string{"relative/path"}, RequestedBy: "op"})
	require.Error(t, err, "relative restore paths must be rejected")
}

func TestPlan_DatabaseCompatibilityAndCrossEngineRefusal(t *testing.T) {
	s, _, _ := testService(verifiedSnap("pg-1", domain.TargetPostgres, "appdb"))

	plan, err := s.Plan(context.Background(), PlanRequest{Kind: KindDatabase, SnapshotID: "pg-1", Engine: "postgres", RequestedBy: "op"})
	require.NoError(t, err)
	joined := strings.Join(plan.Compatibility, "\n")
	require.Contains(t, joined, "pg_restore")
	require.Contains(t, joined, "extensions")

	_, err = s.Plan(context.Background(), PlanRequest{Kind: KindDatabase, SnapshotID: "pg-1", Engine: "mysql", RequestedBy: "op"})
	require.Error(t, err, "cross-engine restore must be refused")

	alt, err := s.Plan(context.Background(), PlanRequest{Kind: KindDatabase, SnapshotID: "pg-1", Engine: "postgres", TargetDB: "appdb_copy", RequestedBy: "op"})
	require.NoError(t, err)
	require.True(t, alt.AlternateDestination)
	require.Equal(t, "appdb_copy", alt.Steps[0].Destination)

	_, err = s.Plan(context.Background(), PlanRequest{Kind: KindDatabase, SnapshotID: "pg-1", Engine: "postgres", TargetDB: "bad;name", RequestedBy: "op"})
	require.Error(t, err)
}

func TestPlan_VolumeWarnsToStopContainers(t *testing.T) {
	s, _, _ := testService(verifiedSnap("v-1", domain.TargetDockerVolume, "data"))
	plan, err := s.Plan(context.Background(), PlanRequest{Kind: KindVolume, SnapshotID: "v-1", RequestedBy: "op"})
	require.NoError(t, err)
	require.Contains(t, strings.Join(plan.Warnings, "\n"), "stop containers")
}

// ---------- stack order ----------

func TestPlan_StackRequiresExplicitOrder(t *testing.T) {
	s, _, _ := testService(
		verifiedSnap("db-1", domain.TargetPostgres, "appdb"),
		verifiedSnap("vol-1", domain.TargetDockerVolume, "data"),
	)
	_, err := s.Plan(context.Background(), PlanRequest{
		Kind: KindStack, SnapshotIDs: []string{"db-1", "vol-1"}, RequestedBy: "op",
	})
	require.ErrorIs(t, err, ErrOrderRequired)

	_, err = s.Plan(context.Background(), PlanRequest{
		Kind: KindStack, SnapshotIDs: []string{"db-1", "vol-1"},
		Order: []string{"db-1"}, RequestedBy: "op", // incomplete order
	})
	require.ErrorIs(t, err, ErrOrderRequired)

	_, err = s.Plan(context.Background(), PlanRequest{
		Kind: KindStack, SnapshotIDs: []string{"db-1", "vol-1"},
		Order: []string{"db-1", "db-1"}, RequestedBy: "op", // duplicate
	})
	require.Error(t, err)
}

func TestPlan_StackEmitsStepsInExplicitOrder(t *testing.T) {
	s, _, _ := testService(
		verifiedSnap("db-1", domain.TargetPostgres, "appdb"),
		verifiedSnap("vol-1", domain.TargetDockerVolume, "data"),
	)
	plan, err := s.Plan(context.Background(), PlanRequest{
		Kind: KindStack, SnapshotIDs: []string{"db-1", "vol-1"},
		Order: []string{"vol-1", "db-1"}, RequestedBy: "op",
	})
	require.NoError(t, err)
	require.Len(t, plan.Steps, 2)
	require.Equal(t, "vol-1", plan.Steps[0].SnapshotID)
	require.Equal(t, 1, plan.Steps[0].Sequence)
	require.Equal(t, "db-1", plan.Steps[1].SnapshotID)
	require.Equal(t, 2, plan.Steps[1].Sequence)
}

// ---------- tokens: single-use + expiry ----------

func TestConfirmExecute_SingleUseToken(t *testing.T) {
	s, rep, _ := testService(verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv"))
	ctx := context.Background()
	plan, err := s.Plan(ctx, PlanRequest{Kind: KindFile, SnapshotID: "s-1", Destination: "/tmp/r", RequestedBy: "op"})
	require.NoError(t, err)
	conf, err := s.Confirm(ctx, plan.ID, "admin")
	require.NoError(t, err)
	require.NotEmpty(t, conf.Token)

	job, err := s.Execute(ctx, conf.Token, "admin")
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, job.State)
	require.Equal(t, "admin", job.ApprovedBy)
	require.Equal(t, "/tmp/r", job.TargetPath)

	_, err = s.Execute(ctx, conf.Token, "admin")
	require.ErrorIs(t, err, ErrTokenReused, "tokens are single-use")

	stored, err := rep.GetRestoreJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.ID, stored.ID)

	status, err := s.Status(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, status.State)
}

func TestExecute_ExpiredTokenRefused(t *testing.T) {
	s, _, clock := testService(verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv"))
	ctx := context.Background()
	plan, err := s.Plan(ctx, PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
	require.NoError(t, err)
	conf, err := s.Confirm(ctx, plan.ID, "admin")
	require.NoError(t, err)
	clock.advance(DefaultTokenTTL + time.Second)
	_, err = s.Execute(ctx, conf.Token, "admin")
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestExecute_UnknownTokenRefused(t *testing.T) {
	s, _, _ := testService(verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv"))
	_, err := s.Execute(context.Background(), "nope", "admin")
	require.ErrorIs(t, err, ErrTokenInvalid)
}

func TestConfirm_ExpiredPlanRefused(t *testing.T) {
	s, _, clock := testService(verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv"))
	ctx := context.Background()
	plan, err := s.Plan(ctx, PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
	require.NoError(t, err)
	clock.advance(DefaultPlanTTL + time.Second)
	_, err = s.Confirm(ctx, plan.ID, "admin")
	require.ErrorIs(t, err, ErrPlanExpired)
}

func TestPlan_EncryptedSnapshotWarnsAboutKey(t *testing.T) {
	snap := verifiedSnap("s-1", domain.TargetHostFilesystem, "/srv")
	snap.Encrypted = true
	snap.KeyRef = "key-9"
	s, _, _ := testService(snap)
	plan, err := s.Plan(context.Background(), PlanRequest{Kind: KindFile, SnapshotID: "s-1", RequestedBy: "op"})
	require.NoError(t, err)
	require.Contains(t, strings.Join(plan.Warnings, "\n"), "key-9")
}
