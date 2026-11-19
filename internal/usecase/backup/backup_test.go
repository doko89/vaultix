package backup

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

type fakePolicies struct {
	versions map[string]*domain.BackupPolicyVersion
}

func pvKey(policyID string, v int) string { return fmt.Sprintf("%s@%d", policyID, v) }

func (f *fakePolicies) CreatePolicy(_ context.Context, _ *domain.BackupPolicy) error {
	return nil
}
func (f *fakePolicies) GetPolicy(_ context.Context, _ string) (*domain.BackupPolicy, error) {
	return nil, domain.ErrNotFound
}
func (f *fakePolicies) UpdatePolicy(_ context.Context, _ *domain.BackupPolicy) error {
	return nil
}
func (f *fakePolicies) DeletePolicy(_ context.Context, _ string) error { return nil }
func (f *fakePolicies) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	return domain.ListResult[domain.BackupPolicy]{}, nil
}
func (f *fakePolicies) PublishVersion(_ context.Context, v *domain.BackupPolicyVersion) error {
	if f.versions == nil {
		f.versions = map[string]*domain.BackupPolicyVersion{}
	}
	f.versions[pvKey(v.PolicyID, v.Version)] = v
	return nil
}
func (f *fakePolicies) GetVersion(_ context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	v, ok := f.versions[pvKey(policyID, version)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return v, nil
}
func (f *fakePolicies) ListVersions(_ context.Context, _ string) ([]domain.BackupPolicyVersion, error) {
	return nil, nil
}

type fakeJobs struct {
	mu sync.Mutex
	m  map[string]*domain.BackupJob
}

func (f *fakeJobs) CreateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*domain.BackupJob{}
	}
	f.m[j.ID] = j
	return nil
}
func (f *fakeJobs) GetJob(_ context.Context, id string) (*domain.BackupJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return j, nil
}
func (f *fakeJobs) UpdateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[j.ID] = j
	return nil
}
func (f *fakeJobs) ListJobs(_ context.Context, _ domain.ListOptions, _ domain.JobFilter) (domain.ListResult[domain.BackupJob], error) {
	return domain.ListResult[domain.BackupJob]{}, nil
}
func (f *fakeJobs) ClaimNextJob(_ context.Context, _ string) (*domain.BackupJob, error) {
	return nil, domain.ErrNotFound
}

type fakeSnapshots struct {
	mu       sync.Mutex
	snaps    map[string]*domain.Snapshot
	manifest map[string]*domain.SnapshotManifest // by snapshot ID
	objects  map[string][]domain.SnapshotObject  // by manifest ID
}

func (f *fakeSnapshots) CreateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snaps == nil {
		f.snaps = map[string]*domain.Snapshot{}
	}
	f.snaps[s.ID] = s
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
	return nil
}
func (f *fakeSnapshots) ListSnapshots(_ context.Context, _ domain.ListOptions, _ domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	return domain.ListResult[domain.Snapshot]{}, nil
}
func (f *fakeSnapshots) PutManifest(_ context.Context, m *domain.SnapshotManifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.manifest == nil {
		f.manifest = map[string]*domain.SnapshotManifest{}
	}
	f.manifest[m.SnapshotID] = m
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
func (f *fakeSnapshots) PutObjects(_ context.Context, objects []domain.SnapshotObject) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.objects == nil {
		f.objects = map[string][]domain.SnapshotObject{}
	}
	for _, o := range objects {
		f.objects[o.ManifestID] = append(f.objects[o.ManifestID], o)
	}
	return nil
}
func (f *fakeSnapshots) ListObjects(_ context.Context, manifestID string) ([]domain.SnapshotObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SnapshotObject(nil), f.objects[manifestID]...), nil
}

type fakeStacks struct {
	members map[string][]domain.ApplicationStackResource
}

func (f *fakeStacks) CreateStack(_ context.Context, _ *domain.ApplicationStack) error {
	return nil
}
func (f *fakeStacks) GetStack(_ context.Context, _ string) (*domain.ApplicationStack, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeStacks) UpdateStack(_ context.Context, _ *domain.ApplicationStack) error {
	return nil
}
func (f *fakeStacks) DeleteStack(_ context.Context, _ string) error { return nil }
func (f *fakeStacks) ListStacks(_ context.Context, _ string) ([]domain.ApplicationStack, error) {
	return nil, nil
}
func (f *fakeStacks) SetStackResources(_ context.Context, _ string, _ []domain.ApplicationStackResource) error {
	return nil
}
func (f *fakeStacks) ListStackResources(_ context.Context, stackID string) ([]domain.ApplicationStackResource, error) {
	return f.members[stackID], nil
}

type fakeObjects struct {
	mu    sync.Mutex
	meta  map[string]domain.ObjectMeta // by destinationID + "\x00" + key
	errOn map[string]error
}

func objKey(dest, key string) string { return dest + "\x00" + key }

func (f *fakeObjects) Head(_ context.Context, dest, key string) (domain.ObjectMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errOn[objKey(dest, key)]; ok {
		return domain.ObjectMeta{}, err
	}
	m, ok := f.meta[objKey(dest, key)]
	if !ok {
		return domain.ObjectMeta{Exists: false}, nil
	}
	return m, nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// ---------- helpers ----------

func testDeps() (Deps, *fakePolicies, *fakeJobs, *fakeSnapshots, *fakeStacks, *fakeObjects) {
	pol, jobs, snaps, stacks, objs := &fakePolicies{}, &fakeJobs{}, &fakeSnapshots{}, &fakeStacks{members: map[string][]domain.ApplicationStackResource{}}, &fakeObjects{meta: map[string]domain.ObjectMeta{}}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var n int
	d := Deps{
		Policies: pol, Jobs: jobs, Snapshots: snaps, Stacks: stacks, Objects: objs,
		Clock: fixedClock{now: now},
		IDGen: func() string { n++; return fmt.Sprintf("id-%d", n) },
	}
	return d, pol, jobs, snaps, stacks, objs
}

func mustService(t *testing.T, d Deps) *Service {
	t.Helper()
	s, err := New(d)
	require.NoError(t, err)
	return s
}

const (
	sumA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sumM = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func seedJob(jobs *fakeJobs, id string) *domain.BackupJob {
	j := &domain.BackupJob{ID: id, AgentID: "agent-1", PolicyID: "pol-1", PolicyVersion: 3,
		State: domain.JobUploading, StorageID: "store-1"}
	_ = jobs.CreateJob(context.Background(), j)
	return j
}

func seedVersion(t *testing.T, pol *fakePolicies) {
	t.Helper()
	err := pol.PublishVersion(context.Background(), &domain.BackupPolicyVersion{
		Version: 3, PolicyID: "pol-1",
		Targets: []domain.BackupPolicyTarget{
			{ID: "t1", PolicyID: "pol-1", Type: domain.TargetHostFilesystem, Selector: "/srv/data"},
		},
	})
	require.NoError(t, err)
}

func fileReport() CompletionReport {
	return CompletionReport{
		ManifestObjectKey: "snap/manifest.json",
		ManifestChecksum:  sumM,
		Targets: []TargetResult{{
			TargetID: "t1", Type: domain.TargetHostFilesystem, Selector: "/srv/data",
			Method: domain.BackupMethodFull, BytesOriginal: 300,
			Objects: []ReportedObject{
				{ObjectKey: "snap/chunk-0", Kind: ObjectKindChunk, Size: 100, Checksum: sumA, Sequence: 0, Source: "/srv/data"},
				{ObjectKey: "snap/chunk-1", Kind: ObjectKindChunk, Size: 200, Checksum: sumB, Sequence: 1, Source: "/srv/data"},
			},
		}},
	}
}

// storeAll places every reported object plus the manifest artifact in fake storage.
func storeAll(objs *fakeObjects, rep CompletionReport) {
	objs.meta[objKey("store-1", rep.ManifestObjectKey)] = domain.ObjectMeta{Exists: true, Size: 64, Checksum: rep.ManifestChecksum}
	for _, tr := range rep.Targets {
		for _, o := range tr.Objects {
			objs.meta[objKey("store-1", o.ObjectKey)] = domain.ObjectMeta{Exists: true, Size: o.Size, Checksum: o.Checksum}
		}
	}
}

// ---------- consistency rules ----------

func TestResolveTarget_DatabaseMethodsEnforced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  domain.BackupPolicyTarget
		wantErr bool
	}{
		{"pg logical ok", domain.BackupPolicyTarget{Type: domain.TargetPostgres, Selector: "appdb", Method: domain.BackupMethodLogicalDump, CredentialRef: "cred-1"}, false},
		{"pg file copy rejected", domain.BackupPolicyTarget{Type: domain.TargetPostgres, Selector: "appdb", Method: domain.BackupMethodFull, CredentialRef: "cred-1"}, true},
		{"pg empty method defaults to logical", domain.BackupPolicyTarget{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "cred-1"}, false},
		{"mysql logical ok", domain.BackupPolicyTarget{Type: domain.TargetMySQL, Selector: "appdb", Method: domain.BackupMethodLogicalDump, CredentialRef: "cred-1"}, false},
		{"mysql file copy rejected", domain.BackupPolicyTarget{Type: domain.TargetMySQL, Selector: "appdb", Method: domain.BackupMethodFull, CredentialRef: "cred-1"}, true},
		{"mssql native ok", domain.BackupPolicyTarget{Type: domain.TargetMSSQL, Selector: "appdb", Method: domain.BackupMethodNativeFull, CredentialRef: "cred-1"}, false},
		{"mssql logical rejected", domain.BackupPolicyTarget{Type: domain.TargetMSSQL, Selector: "appdb", Method: domain.BackupMethodLogicalDump, CredentialRef: "cred-1"}, true},
		{"redis rdb ok", domain.BackupPolicyTarget{Type: domain.TargetRedis, Selector: "cache", Method: domain.BackupMethodRDPSnapshot}, false},
		{"redis full rejected", domain.BackupPolicyTarget{Type: domain.TargetRedis, Selector: "cache", Method: domain.BackupMethodFull}, true},
		{"fs defaults to full", domain.BackupPolicyTarget{Type: domain.TargetHostFilesystem, Selector: "/srv"}, false},
		{"volume defaults to full", domain.BackupPolicyTarget{Type: domain.TargetDockerVolume, Selector: "data"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := ResolveTarget(tc.target)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, rt.EffectiveMethod)
		})
	}
	rt, err := ResolveTarget(domain.BackupPolicyTarget{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "cred-1"})
	require.NoError(t, err)
	require.Equal(t, domain.BackupMethodLogicalDump, rt.EffectiveMethod)
}

func TestResolveTarget_LiveFileCaveatsRecorded(t *testing.T) {
	rt, err := ResolveTarget(domain.BackupPolicyTarget{
		Type: domain.TargetHostFilesystem, Selector: "/srv",
		Exclude: []string{"*.tmp"}, Options: map[string]string{"symlink": "follow"},
	})
	require.NoError(t, err)
	joined := strings.Join(rt.Caveats, "\n")
	require.Contains(t, joined, "live files may change")
	require.Contains(t, joined, "follow")
	require.Contains(t, joined, "exclude")
}

func TestService_ResolveTargetsUsesPinnedVersion(t *testing.T) {
	d, pol, _, _, stacks, _ := testDeps()
	stacks.members["stack-1"] = []domain.ApplicationStackResource{
		{StackID: "stack-1", Selector: "vol-data", Type: domain.TargetDockerVolume, RestoreOrder: 1, Required: true},
		{StackID: "stack-1", Selector: "appdb", Type: domain.TargetPostgres, RestoreOrder: 2, Required: true},
	}
	s := mustService(t, d)
	require.NoError(t, pol.PublishVersion(context.Background(), &domain.BackupPolicyVersion{
		Version: 7, PolicyID: "pol-1",
		Targets: []domain.BackupPolicyTarget{
			{ID: "fs", PolicyID: "pol-1", Type: domain.TargetHostFilesystem, Selector: "/srv"},
			{ID: "st", PolicyID: "pol-1", Type: domain.TargetAppStack, Selector: "stack-1", CredentialRef: "cred-db"},
		},
	}))
	// A "live" policy edit would publish version 8; resolution must still use 7.
	require.NoError(t, pol.PublishVersion(context.Background(), &domain.BackupPolicyVersion{
		Version: 8, PolicyID: "pol-1",
		Targets: []domain.BackupPolicyTarget{
			{ID: "other", PolicyID: "pol-1", Type: domain.TargetHostFilesystem, Selector: "/other"},
		},
	}))
	got, err := s.ResolveTargets(context.Background(), "pol-1", 7)
	require.NoError(t, err)
	require.Len(t, got, 3) // 1 fs + 2 stack members
	require.Equal(t, "/srv", got[0].Target.Selector)
	require.Equal(t, domain.BackupMethodFull, got[0].EffectiveMethod)
	require.NotNil(t, got[1].StackMember)
	require.Equal(t, domain.TargetDockerVolume, got[1].Target.Type)
	require.Equal(t, domain.BackupMethodLogicalDump, got[2].EffectiveMethod)
}

// ---------- manifest assembly ----------

func TestAssembleManifest_RecordsEncryptionMetadata(t *testing.T) {
	var n int
	idgen := func() string { n++; return fmt.Sprintf("x-%d", n) }
	job := &domain.BackupJob{ID: "job-1", AgentID: "a", PolicyID: "p", PolicyVersion: 1, Encrypted: false}
	rep := fileReport()
	snap, manifest, objects, err := AssembleManifest(ManifestInput{
		Job: job, Targets: rep.Targets,
		ManifestObjectKey: rep.ManifestObjectKey, ManifestChecksum: rep.ManifestChecksum,
	}, idgen)
	require.NoError(t, err)
	require.False(t, snap.Encrypted, "unencrypted snapshots must record Encrypted=false for catalog labeling")
	require.Empty(t, snap.KeyRef)
	require.Equal(t, domain.SnapshotVerifying, snap.Status)
	require.Len(t, objects, 2)
	require.Equal(t, 2, manifest.ObjectCount)
	require.Equal(t, uint64(300), manifest.BytesStored)
	require.NotNil(t, snap.BytesOriginal)
	require.Equal(t, uint64(300), *snap.BytesOriginal)
}

func TestAssembleManifest_EncryptedRequiresKeyRef(t *testing.T) {
	job := &domain.BackupJob{ID: "job-1", AgentID: "a", PolicyID: "p", Encrypted: true}
	rep := fileReport()
	_, _, _, err := AssembleManifest(ManifestInput{
		Job: job, Targets: rep.Targets,
		ManifestObjectKey: rep.ManifestObjectKey, ManifestChecksum: rep.ManifestChecksum,
	}, randomID)
	require.Error(t, err)
}

func TestAssembleManifest_RejectsBadChecksums(t *testing.T) {
	job := &domain.BackupJob{ID: "job-1", AgentID: "a", PolicyID: "p"}
	for _, bad := range []string{"", "noalgo", "sha256:xyz", "sha256:ab"} {
		rep := fileReport()
		rep.Targets[0].Objects[0].Checksum = bad
		_, _, _, err := AssembleManifest(ManifestInput{
			Job: job, Targets: rep.Targets,
			ManifestObjectKey: rep.ManifestObjectKey, ManifestChecksum: rep.ManifestChecksum,
		}, randomID)
		require.Error(t, err, "checksum %q must be rejected", bad)
	}
}

// ---------- verification gate matrix ----------

func complete(t *testing.T, s *Service, jobs *fakeJobs, rep CompletionReport) (*VerificationResult, *domain.BackupJob) {
	t.Helper()
	res, err := s.CompleteJob(context.Background(), "job-1", rep)
	job, jerr := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, jerr)
	if res != nil && res.OK {
		require.NoError(t, err)
	}
	return res, job
}

func TestCompleteJob_HappyPathSucceeds(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	rep := fileReport()
	storeAll(objs, rep)

	res, job := complete(t, s, jobs, rep)
	require.NotNil(t, res)
	require.True(t, res.OK)
	require.Empty(t, res.Failures)
	require.Equal(t, domain.JobSucceeded, job.State)
	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotVerified, snap.Status)
	require.True(t, snap.IsRestorable())
}

func TestCompleteJob_MissingObjectFails(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	rep := fileReport()
	storeAll(objs, rep)
	delete(objs.meta, objKey("store-1", "snap/chunk-1")) // object never uploaded

	res, job := complete(t, s, jobs, rep)
	require.NotNil(t, res)
	require.False(t, res.OK, "missing object must fail verification")
	require.NotEqual(t, domain.JobSucceeded, job.State)
	require.Equal(t, domain.JobFailed, job.State)
	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotFailed, snap.Status)
	require.False(t, snap.IsRestorable())
	require.Contains(t, job.ErrorMessage, "snap/chunk-1")
}

func TestCompleteJob_BadChecksumFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	rep := fileReport()
	storeAll(objs, rep)
	objs.meta[objKey("store-1", "snap/chunk-0")] = domain.ObjectMeta{Exists: true, Size: 100,
		Checksum: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}

	res, job := complete(t, s, jobs, rep)
	require.False(t, res.OK)
	require.Equal(t, domain.JobFailed, job.State)
	require.Contains(t, job.ErrorMessage, ReasonChecksumMismatch)
}

func TestCompleteJob_SizeMismatchFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	rep := fileReport()
	storeAll(objs, rep)
	objs.meta[objKey("store-1", "snap/chunk-0")] = domain.ObjectMeta{Exists: true, Size: 99, Checksum: sumA}

	res, job := complete(t, s, jobs, rep)
	require.False(t, res.OK)
	require.Equal(t, domain.JobFailed, job.State)
}

func TestCompleteJob_MissingManifestArtifactFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	rep := fileReport()
	storeAll(objs, rep)
	delete(objs.meta, objKey("store-1", rep.ManifestObjectKey))

	res, job := complete(t, s, jobs, rep)
	require.False(t, res.OK)
	require.Equal(t, domain.JobFailed, job.State)
}

func TestVerifySnapshot_IncompleteManifestFails(t *testing.T) {
	d, _, _, snaps, _, objs := testDeps()
	s := mustService(t, d)
	ctx := context.Background()
	now := time.Now()
	// Manifest declares 2 objects but only 1 registered; byte total off too.
	require.NoError(t, snaps.CreateSnapshot(ctx, &domain.Snapshot{ID: "snap-1", StorageID: "store-1", ManifestID: "man-1"}))
	require.NoError(t, snaps.PutManifest(ctx, &domain.SnapshotManifest{ID: "man-1", SnapshotID: "snap-1", ObjectKey: "m", Checksum: sumM, ObjectCount: 2, BytesStored: 999}))
	require.NoError(t, snaps.PutObjects(ctx, []domain.SnapshotObject{{ID: "o-1", ManifestID: "man-1", SnapshotID: "snap-1", ObjectKey: "k-1", Kind: "chunk", Size: 10, Checksum: sumA}}))
	objs.meta[objKey("store-1", "m")] = domain.ObjectMeta{Exists: true, Size: 5, Checksum: sumM}
	objs.meta[objKey("store-1", "k-1")] = domain.ObjectMeta{Exists: true, Size: 10, Checksum: sumA}

	res, err := s.VerifySnapshot(ctx, "snap-1")
	require.NoError(t, err)
	require.False(t, res.OK)
	require.GreaterOrEqual(t, len(res.Failures), 2)
	snap, _ := snaps.GetSnapshot(ctx, "snap-1")
	require.Equal(t, domain.SnapshotFailed, snap.Status)
	_ = now
}

func TestVerifySnapshot_MissingManifestRecordFails(t *testing.T) {
	d, _, _, snaps, _, _ := testDeps()
	s := mustService(t, d)
	ctx := context.Background()
	require.NoError(t, snaps.CreateSnapshot(ctx, &domain.Snapshot{ID: "snap-1", StorageID: "store-1"}))
	res, err := s.VerifySnapshot(ctx, "snap-1")
	require.NoError(t, err)
	require.False(t, res.OK)
}

func TestVerifySnapshot_ZeroObjectsFails(t *testing.T) {
	d, _, _, snaps, _, objs := testDeps()
	s := mustService(t, d)
	ctx := context.Background()
	require.NoError(t, snaps.CreateSnapshot(ctx, &domain.Snapshot{ID: "snap-1", StorageID: "store-1", ManifestID: "man-1"}))
	require.NoError(t, snaps.PutManifest(ctx, &domain.SnapshotManifest{ID: "man-1", SnapshotID: "snap-1", ObjectKey: "m", Checksum: sumM}))
	objs.meta[objKey("store-1", "m")] = domain.ObjectMeta{Exists: true, Size: 5, Checksum: sumM}
	res, err := s.VerifySnapshot(ctx, "snap-1")
	require.NoError(t, err)
	require.False(t, res.OK, "a manifest with zero objects is incomplete")
}

func TestCompleteJob_WrongStateRefused(t *testing.T) {
	d, pol, jobs, _, _, _ := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	j := &domain.BackupJob{ID: "job-1", AgentID: "a", PolicyID: "pol-1", PolicyVersion: 3, State: domain.JobRunning}
	require.NoError(t, jobs.CreateJob(context.Background(), j))
	_, err := s.CompleteJob(context.Background(), "job-1", fileReport())
	require.Error(t, err)
}
