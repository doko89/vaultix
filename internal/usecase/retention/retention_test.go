package retention

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---------- fakes ----------

type fakeSnapshots struct {
	mu       sync.Mutex
	snaps    map[string]*domain.Snapshot
	manifest map[string]*domain.SnapshotManifest
	objects  map[string][]domain.SnapshotObject
	deleted  []string
}

func (f *fakeSnapshots) seed(s *domain.Snapshot, objs ...domain.SnapshotObject) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snaps == nil {
		f.snaps = map[string]*domain.Snapshot{}
		f.manifest = map[string]*domain.SnapshotManifest{}
		f.objects = map[string][]domain.SnapshotObject{}
	}
	f.snaps[s.ID] = s
	m := &domain.SnapshotManifest{ID: "man-" + s.ID, SnapshotID: s.ID,
		ObjectKey: "snap/" + s.ID + "/manifest.json", Checksum: "sha256:00",
		ObjectCount: len(objs)}
	f.manifest[s.ID] = m
	for _, o := range objs {
		o.ManifestID = m.ID
		o.SnapshotID = s.ID
		f.objects[m.ID] = append(f.objects[m.ID], o)
	}
}
func (f *fakeSnapshots) CreateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.seed(s)
	return nil
}
func (f *fakeSnapshots) GetSnapshot(_ context.Context, id string) (*domain.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snaps[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (f *fakeSnapshots) UpdateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps[s.ID] = s
	return nil
}
func (f *fakeSnapshots) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, id)
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeSnapshots) ListSnapshots(_ context.Context, opts domain.ListOptions, filter domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []domain.Snapshot
	for _, s := range f.snaps {
		if filter.PolicyID != "" && s.PolicyID != filter.PolicyID {
			continue
		}
		items = append(items, *s)
	}
	total := len(items)
	start := min(opts.Offset, total)
	end := total
	if opts.Limit > 0 {
		end = min(start+opts.Limit, total)
	}
	return domain.ListResult[domain.Snapshot]{Items: items[start:end], Total: total}, nil
}
func (f *fakeSnapshots) PutManifest(_ context.Context, _ *domain.SnapshotManifest) error {
	return nil
}
func (f *fakeSnapshots) GetManifest(_ context.Context, snapshotID string) (*domain.SnapshotManifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.manifest[snapshotID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return m, nil
}
func (f *fakeSnapshots) PutObjects(_ context.Context, _ []domain.SnapshotObject) error {
	return nil
}
func (f *fakeSnapshots) ListObjects(_ context.Context, manifestID string) ([]domain.SnapshotObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SnapshotObject(nil), f.objects[manifestID]...), nil
}

type fakeObjects struct {
	mu      sync.Mutex
	present map[string]bool
	failOn  map[string]error
	deleted []string
}

func (f *fakeObjects) Delete(_ context.Context, dest, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := dest + "\x00" + key
	if err, ok := f.failOn[k]; ok {
		return err
	}
	delete(f.present, k)
	f.deleted = append(f.deleted, k)
	return nil
}

// ---------- helpers ----------

func testService() (*Service, *fakeSnapshots, *fakeObjects, time.Time) {
	snaps := &fakeSnapshots{}
	objs := &fakeObjects{present: map[string]bool{}, failOn: map[string]error{}}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var n int
	s, err := New(Deps{
		Snapshots: snaps, Objects: objs,
		Clock: fixedClock{now: now},
		IDGen: func() string { n++; return fmt.Sprintf("plan-%d", n) },
	})
	if err != nil {
		panic(err)
	}
	return s, snaps, objs, now
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// seedVerified creates a verified snapshot `age` old with two data objects.
func seedVerified(snaps *fakeSnapshots, objs *fakeObjects, id string, age time.Duration, now time.Time) {
	done := now.Add(-age)
	snaps.seed(&domain.Snapshot{ID: id, AgentID: "a", PolicyID: "pol-1",
		TargetType: domain.TargetHostFilesystem, Selector: "/srv",
		Status: domain.SnapshotVerified, ManifestID: "man-" + id,
		StorageID: "store-1", CompletedAt: &done, CreatedAt: done},
		domain.SnapshotObject{ObjectKey: "snap/" + id + "/c0", Kind: "chunk", Size: 100, Checksum: "sha256:aa"},
		domain.SnapshotObject{ObjectKey: "snap/" + id + "/c1", Kind: "chunk", Size: 200, Checksum: "sha256:bb"},
	)
	objs.present["store-1\x00snap/"+id+"/manifest.json"] = true
	objs.present["store-1\x00snap/"+id+"/c0"] = true
	objs.present["store-1\x00snap/"+id+"/c1"] = true
}

// ---------- evaluation ----------

func TestEvaluate_CountRuleCondemnsOldest(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "new", 1*time.Hour, now)
	seedVerified(snaps, objs, "mid", 25*time.Hour, now)
	seedVerified(snaps, objs, "old", 49*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 2}, EvaluateOptions{Actor: "op", DryRun: true})
	require.NoError(t, err)
	require.True(t, plan.DryRun)
	require.Len(t, plan.Deletes, 1)
	require.Equal(t, "old", plan.Deletes[0].SnapshotID)
	require.Contains(t, plan.Deletes[0].Reason, "keep_last_n=2")
	require.Equal(t, 2, plan.Kept)
	require.False(t, plan.OverrideRequired)
	require.Len(t, plan.Deletes[0].ObjectKeys, 3, "manifest artifact + 2 data objects")
	require.Empty(t, snaps.deleted, "evaluate must be side-effect free")
	require.Empty(t, objs.deleted, "dry-run must not delete objects")
}

func TestEvaluate_AgeRuleCondemnsByAge(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "fresh", 1*time.Hour, now)
	seedVerified(snaps, objs, "stale", 30*24*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 100, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Deletes, 1)
	require.Equal(t, "stale", plan.Deletes[0].SnapshotID)
	require.Contains(t, plan.Deletes[0].Reason, "keep_days=7")
}

func TestEvaluate_LastRecoveryPointProtected(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "only", 90*24*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 5, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	require.Empty(t, plan.Deletes, "the only recovery point must be spared")
	require.Len(t, plan.Protected, 1)
	require.Equal(t, "only", plan.Protected[0].SnapshotID)
	require.True(t, plan.OverrideRequired)
}

func TestEvaluate_OverrideKeepsCondemnedAndAudits(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "only", 90*24*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 5, KeepDays: 7},
		EvaluateOptions{OverrideLastRecoveryPoint: true, Actor: "admin"})
	require.NoError(t, err)
	require.Len(t, plan.Deletes, 1)
	require.True(t, plan.OverrideGranted)
	require.False(t, plan.OverrideRequired)
}

func TestEvaluate_KeepMinimumFloor(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "s1", 1*time.Hour, now)
	seedVerified(snaps, objs, "s2", 2*time.Hour, now)
	seedVerified(snaps, objs, "s3", 3*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 1, KeepMinimum: 2}, EvaluateOptions{})
	require.NoError(t, err)
	// s1 kept by count; s2 protected by floor; only s3 condemned.
	require.Len(t, plan.Deletes, 1)
	require.Equal(t, "s3", plan.Deletes[0].SnapshotID)
	require.Len(t, plan.Protected, 1)
	require.Equal(t, "s2", plan.Protected[0].SnapshotID)
	require.True(t, plan.OverrideRequired)
}

func TestEvaluate_FailedSnapshotsNotProtected(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "good", 1*time.Hour, now)
	done := now.Add(-90 * 24 * time.Hour)
	snaps.seed(&domain.Snapshot{ID: "bad", AgentID: "a", PolicyID: "pol-1",
		TargetType: domain.TargetHostFilesystem, Selector: "/srv",
		Status: domain.SnapshotFailed, ManifestID: "man-bad",
		StorageID: "store-1", CompletedAt: &done, CreatedAt: done})

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 100, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Deletes, 1)
	require.Equal(t, "bad", plan.Deletes[0].SnapshotID)
	require.False(t, plan.Deletes[0].Verified)
	require.False(t, plan.OverrideRequired, "failed snapshots are not recovery points")
}

// ---------- apply ----------

func TestApply_DeletesObjectsThenMetadata(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "new", 1*time.Hour, now)
	seedVerified(snaps, objs, "old", 90*24*time.Hour, now)

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 100, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Deletes, 1)

	res, err := s.Apply(context.Background(), plan, ApplyOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, res.DeletedSnapshots)
	require.Equal(t, 3, res.DeletedObjects)
	require.Equal(t, uint64(300), res.DeletedBytes)
	require.Len(t, objs.deleted, 3)
	_, err = snaps.GetSnapshot(context.Background(), "old")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestApply_RefusesOverrideRequiredWithoutConfirmation(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "only", 90*24*time.Hour, now)

	protected, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 5, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	require.True(t, protected.OverrideRequired)

	// Simulate an operator confirming the override: re-evaluate with it.
	overridden, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 5, KeepDays: 7},
		EvaluateOptions{OverrideLastRecoveryPoint: true})
	require.NoError(t, err)
	// But the plan still refuses when Apply is called without confirmation
	// if it was flagged required — craft the guarded shape explicitly.
	overridden.OverrideRequired = true
	_, err = s.Apply(context.Background(), overridden, ApplyOptions{})
	require.ErrorIs(t, err, ErrLastRecoveryPoint)
	require.Empty(t, objs.deleted)

	res, err := s.Apply(context.Background(), overridden, ApplyOptions{Override: true})
	require.NoError(t, err)
	require.Equal(t, []string{"only"}, res.DeletedSnapshots)
}

func TestApply_ObjectFailureLeavesMetadataForRetry(t *testing.T) {
	s, snaps, objs, now := testService()
	seedVerified(snaps, objs, "new", 1*time.Hour, now)
	seedVerified(snaps, objs, "old", 90*24*time.Hour, now)
	objs.failOn["store-1\x00snap/old/c0"] = errors.New("s3 500")

	plan, err := s.Evaluate(context.Background(), "pol-1",
		domain.RetentionRule{KeepLastN: 100, KeepDays: 7}, EvaluateOptions{})
	require.NoError(t, err)
	res, err := s.Apply(context.Background(), plan, ApplyOptions{})
	require.Error(t, err)
	require.Equal(t, []string{"old"}, res.FailedSnapshots)
	require.Empty(t, res.DeletedSnapshots)
	_, err = snaps.GetSnapshot(context.Background(), "old")
	require.NoError(t, err, "failed snapshot metadata must stay for retry")
}
