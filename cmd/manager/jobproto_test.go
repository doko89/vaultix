package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	backupuc "github.com/vaultix/vaultix/internal/usecase/backup"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// ---------- fakes ----------

type memJobs struct {
	mu sync.Mutex
	m  map[string]*domain.BackupJob
}

func (f *memJobs) CreateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*domain.BackupJob{}
	}
	cp := *j
	f.m[j.ID] = &cp
	return nil
}
func (f *memJobs) GetJob(_ context.Context, id string) (*domain.BackupJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *j
	return &cp, nil
}
func (f *memJobs) UpdateJob(_ context.Context, j *domain.BackupJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[j.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *j
	f.m[j.ID] = &cp
	return nil
}
func (f *memJobs) ListJobs(_ context.Context, opts domain.ListOptions, filter domain.JobFilter) (domain.ListResult[domain.BackupJob], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BackupJob
	for _, j := range f.m {
		if filter.AgentID != "" && j.AgentID != filter.AgentID {
			continue
		}
		if filter.PolicyID != "" && j.PolicyID != filter.PolicyID {
			continue
		}
		if filter.State != "" && j.State != filter.State {
			continue
		}
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	total := len(out)
	if opts.Offset < len(out) {
		out = out[opts.Offset:]
	} else {
		out = nil
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return domain.ListResult[domain.BackupJob]{Items: out, Total: total}, nil
}
func (f *memJobs) ClaimNextJob(_ context.Context, _ string) (*domain.BackupJob, error) {
	return nil, domain.ErrNotFound
}

type memAgents struct{ agents map[string]*domain.Agent }

func (f *memAgents) CreateAgent(_ context.Context, _ *domain.Agent) error { return nil }
func (f *memAgents) GetAgent(_ context.Context, id string) (*domain.Agent, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return a, nil
}
func (f *memAgents) UpdateAgent(_ context.Context, _ *domain.Agent) error { return nil }
func (f *memAgents) ListAgents(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.Agent], error) {
	return domain.ListResult[domain.Agent]{}, nil
}
func (f *memAgents) StoreCredential(_ context.Context, _ *domain.AgentCredential) error { return nil }
func (f *memAgents) GetCredentialByHash(_ context.Context, _ string) (*domain.AgentCredential, error) {
	return nil, domain.ErrNotFound
}
func (f *memAgents) ListAgentCredentials(_ context.Context, _ string) ([]domain.AgentCredential, error) {
	return nil, nil
}
func (f *memAgents) UpdateCredential(_ context.Context, _ *domain.AgentCredential) error { return nil }
func (f *memAgents) RecordHeartbeat(_ context.Context, _ *domain.AgentHeartbeat) error   { return nil }
func (f *memAgents) LatestHeartbeat(_ context.Context, _ string) (*domain.AgentHeartbeat, error) {
	return nil, domain.ErrNotFound
}

type memPolicies struct {
	policies map[string]*domain.BackupPolicy
	versions map[string]*domain.BackupPolicyVersion
}

func (f *memPolicies) CreatePolicy(_ context.Context, _ *domain.BackupPolicy) error { return nil }
func (f *memPolicies) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	p, ok := f.policies[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return p, nil
}
func (f *memPolicies) UpdatePolicy(_ context.Context, _ *domain.BackupPolicy) error { return nil }
func (f *memPolicies) DeletePolicy(_ context.Context, _ string) error               { return nil }
func (f *memPolicies) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	return domain.ListResult[domain.BackupPolicy]{}, nil
}
func (f *memPolicies) PublishVersion(_ context.Context, _ *domain.BackupPolicyVersion) error {
	return nil
}
func (f *memPolicies) GetVersion(_ context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	v, ok := f.versions[fmt.Sprintf("%s@%d", policyID, version)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return v, nil
}
func (f *memPolicies) ListVersions(_ context.Context, _ string) ([]domain.BackupPolicyVersion, error) {
	return nil, nil
}

type memStorage struct {
	dests   map[string]*domain.StorageDestination
	secrets map[string]*domain.SecretProfile
}

func (f *memStorage) CreateDestination(_ context.Context, _ *domain.StorageDestination) error {
	return nil
}
func (f *memStorage) GetDestination(_ context.Context, id string) (*domain.StorageDestination, error) {
	d, ok := f.dests[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}
func (f *memStorage) GetDefaultDestination(_ context.Context) (*domain.StorageDestination, error) {
	for _, d := range f.dests {
		if d.Default {
			return d, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f *memStorage) UpdateDestination(_ context.Context, _ *domain.StorageDestination) error {
	return nil
}
func (f *memStorage) DeleteDestination(_ context.Context, _ string) error { return nil }
func (f *memStorage) ListDestinations(_ context.Context) ([]domain.StorageDestination, error) {
	return nil, nil
}
func (f *memStorage) CreateSecret(_ context.Context, _ *domain.SecretProfile) error { return nil }
func (f *memStorage) GetSecret(_ context.Context, id string) (*domain.SecretProfile, error) {
	s, ok := f.secrets[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (f *memStorage) DeleteSecret(_ context.Context, _ string) error { return nil }
func (f *memStorage) ListSecrets(_ context.Context) ([]domain.SecretProfile, error) {
	return nil, nil
}

type memSnapshots struct {
	mu       sync.Mutex
	snaps    map[string]*domain.Snapshot
	manifest map[string]*domain.SnapshotManifest
	objects  map[string][]domain.SnapshotObject
}

func (f *memSnapshots) CreateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snaps == nil {
		f.snaps = map[string]*domain.Snapshot{}
	}
	f.snaps[s.ID] = s
	return nil
}
func (f *memSnapshots) GetSnapshot(_ context.Context, id string) (*domain.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snaps[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}
func (f *memSnapshots) UpdateSnapshot(_ context.Context, s *domain.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps[s.ID] = s
	return nil
}
func (f *memSnapshots) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, id)
	return nil
}
func (f *memSnapshots) ListSnapshots(_ context.Context, _ domain.ListOptions, _ domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	return domain.ListResult[domain.Snapshot]{}, nil
}
func (f *memSnapshots) PutManifest(_ context.Context, m *domain.SnapshotManifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.manifest == nil {
		f.manifest = map[string]*domain.SnapshotManifest{}
	}
	f.manifest[m.SnapshotID] = m
	return nil
}
func (f *memSnapshots) GetManifest(_ context.Context, snapshotID string) (*domain.SnapshotManifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.manifest[snapshotID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return m, nil
}
func (f *memSnapshots) PutObjects(_ context.Context, objects []domain.SnapshotObject) error {
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
func (f *memSnapshots) ListObjects(_ context.Context, manifestID string) ([]domain.SnapshotObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SnapshotObject(nil), f.objects[manifestID]...), nil
}

type memRestoreJobs struct {
	mu sync.Mutex
	m  map[string]*domain.RestoreJob
}

func (f *memRestoreJobs) CreateJob(_ context.Context, j *domain.RestoreJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*domain.RestoreJob{}
	}
	cp := *j
	f.m[j.ID] = &cp
	return nil
}
func (f *memRestoreJobs) GetJob(_ context.Context, id string) (*domain.RestoreJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *j
	return &cp, nil
}
func (f *memRestoreJobs) UpdateJob(_ context.Context, j *domain.RestoreJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *j
	f.m[j.ID] = &cp
	return nil
}
func (f *memRestoreJobs) ListJobs(_ context.Context, opts domain.ListOptions, agentID string) (domain.ListResult[domain.RestoreJob], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.RestoreJob
	for _, j := range f.m {
		if agentID != "" && j.AgentID != agentID {
			continue
		}
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	total := len(out)
	if opts.Offset < len(out) {
		out = out[opts.Offset:]
	} else {
		out = nil
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return domain.ListResult[domain.RestoreJob]{Items: out, Total: total}, nil
}

type memStacks struct{}

func (memStacks) CreateStack(_ context.Context, _ *domain.ApplicationStack) error { return nil }
func (memStacks) GetStack(_ context.Context, _ string) (*domain.ApplicationStack, error) {
	return nil, domain.ErrNotFound
}
func (memStacks) UpdateStack(_ context.Context, _ *domain.ApplicationStack) error { return nil }
func (memStacks) DeleteStack(_ context.Context, _ string) error                   { return nil }
func (memStacks) ListStacks(_ context.Context, _ string) ([]domain.ApplicationStack, error) {
	return nil, nil
}
func (memStacks) SetStackResources(_ context.Context, _ string, _ []domain.ApplicationStackResource) error {
	return nil
}
func (memStacks) ListStackResources(_ context.Context, _ string) ([]domain.ApplicationStackResource, error) {
	return nil, nil
}

// fakeStore is an in-memory ObjectStore: presigns deterministic URLs and
// serves Head from an uploaded set.
type fakeStore struct {
	mu       sync.Mutex
	meta     map[string]domain.ObjectMeta
	uploads  map[string]string // uploadID -> key
	uploadN  int
	aborted  []string
	complete []string
}

func objMetaKey(dest, key string) string { return dest + "\x00" + key }

func (f *fakeStore) PresignUpload(_ context.Context, dest, key string, ttl time.Duration) (string, error) {
	return "https://store/" + dest + "/" + key + "?ttl=" + ttl.String(), nil
}
func (f *fakeStore) PresignDownload(_ context.Context, dest, key string, ttl time.Duration) (string, error) {
	return "https://store/" + dest + "/" + key + "?dl&ttl=" + ttl.String(), nil
}
func (f *fakeStore) Head(_ context.Context, dest, key string) (domain.ObjectMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.meta[objMetaKey(dest, key)]
	if !ok {
		return domain.ObjectMeta{Exists: false}, nil
	}
	return m, nil
}
func (f *fakeStore) Delete(_ context.Context, dest, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.meta, objMetaKey(dest, key))
	return nil
}
func (f *fakeStore) Ping(_ context.Context, _ string) error { return nil }
func (f *fakeStore) InitiateMultipartUpload(_ context.Context, _, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadN++
	id := fmt.Sprintf("up-%d", f.uploadN)
	if f.uploads == nil {
		f.uploads = map[string]string{}
	}
	f.uploads[id] = key
	return id, nil
}
func (f *fakeStore) PresignUploadPart(_ context.Context, dest, key, _ string, part int, _ time.Duration) (ustorage.PartGrant, error) {
	return ustorage.PartGrant{Method: "PUT", URL: fmt.Sprintf("https://store/%s/%s?p=%d", dest, key, part)}, nil
}
func (f *fakeStore) CompleteMultipartUpload(_ context.Context, _, _, uploadID string, _ []ustorage.MultipartPart) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.complete = append(f.complete, uploadID)
	return nil
}
func (f *fakeStore) AbortMultipartUpload(_ context.Context, _, _, uploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, uploadID)
	return nil
}
func (f *fakeStore) VerifyObjects(_ context.Context, _ string, _ []ustorage.ExpectedObject) (bool, []ustorage.VerifyDetail, error) {
	return true, nil, nil
}

// fakeBox is a reversible test SecretBox ("sealed:" prefix).
type fakeBox struct{}

func (fakeBox) Seal(_ context.Context, plaintext []byte) ([]byte, string, error) {
	return append([]byte("sealed:"), plaintext...), "v1", nil
}
func (fakeBox) Open(_ context.Context, ciphertext []byte, _ string) ([]byte, error) {
	plain, ok := cutPrefixBytes(ciphertext, []byte("sealed:"))
	if !ok {
		return nil, errors.New("fakeBox: undecryptable")
	}
	return plain, nil
}

func cutPrefixBytes(b, prefix []byte) ([]byte, bool) {
	if len(b) < len(prefix) {
		return nil, false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return nil, false
		}
	}
	return b[len(prefix):], true
}

type memAudit struct {
	mu      sync.Mutex
	entries []domain.AuditLog
	fail    error
}

func (f *memAudit) Append(_ context.Context, e *domain.AuditLog) error {
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, *e)
	return nil
}
func (f *memAudit) List(_ context.Context, _ domain.ListOptions, _ domain.AuditFilter) (domain.ListResult[domain.AuditLog], error) {
	return domain.ListResult[domain.AuditLog]{}, nil
}

// ---------- rig ----------

type protoRig struct {
	jobs     *memJobs
	agents   *memAgents
	policies *memPolicies
	store    *memStorage
	snaps    *memSnapshots
	rjobs    *memRestoreJobs
	objects  *fakeStore
	audit    *memAudit

	jobSvc  *jobuc.Service
	backup  *backupuc.Service
	storage *ustorage.Service
	queue   *agentJobQueue
	plane   *agentDataPlane
}

func newProtoRig(t *testing.T) *protoRig {
	t.Helper()
	r := &protoRig{
		jobs:   &memJobs{},
		agents: &memAgents{agents: map[string]*domain.Agent{"ag-1": {ID: "ag-1"}, "ag-2": {ID: "ag-2"}}},
		policies: &memPolicies{
			policies: map[string]*domain.BackupPolicy{"p1": {ID: "p1", Enabled: true, CurrentVersion: 3}},
			versions: map[string]*domain.BackupPolicyVersion{
				"p1@3": {
					Version: 3, PolicyID: "p1", StorageID: "dest-1",
					Targets: []domain.BackupPolicyTarget{
						{ID: "t1", PolicyID: "p1", Type: domain.TargetHostFilesystem, Selector: "/data"},
					},
				},
			},
		},
		store: &memStorage{
			dests: map[string]*domain.StorageDestination{
				"dest-1": {ID: "dest-1", Bucket: "b", Prefix: "vault"},
			},
			secrets: map[string]*domain.SecretProfile{},
		},
		snaps:   &memSnapshots{},
		rjobs:   &memRestoreJobs{},
		objects: &fakeStore{meta: map[string]domain.ObjectMeta{}},
		audit:   &memAudit{},
	}
	r.jobSvc = jobuc.NewService(jobuc.Deps{
		Jobs: r.jobs, Agents: r.agents, Policies: r.policies,
		Cancels: jobuc.NewMemoryCancelNotifier(),
	})
	var err error
	r.backup, err = backupuc.New(backupuc.Deps{
		Policies: r.policies, Jobs: r.jobs, Snapshots: r.snaps,
		Stacks: memStacks{}, Objects: r.objects,
	})
	require.NoError(t, err)
	r.storage = ustorage.NewService(r.store, r.objects, fakeBox{})
	auditSvc, err := audituc.New(r.audit, nil)
	require.NoError(t, err)
	keys := &keyIssuer{store: r.store, box: fakeBox{}, audit: auditSvc, log: testLogger()}
	r.queue = &agentJobQueue{
		jobs: r.jobSvc, agents: r.agents, policies: r.policies,
		backup: r.backup, keys: keys, log: testLogger(),
	}
	r.plane = &agentDataPlane{
		jobs: r.jobSvc, restores: r.rjobs, snapshots: r.snaps,
		storage: r.storage, store: r.store, keys: keys, clock: domain.SystemClock{},
	}
	return r
}

func (r *protoRig) seedJob(t *testing.T, id, agentID string, state domain.JobState) *domain.BackupJob {
	t.Helper()
	j := &domain.BackupJob{
		ID: id, AgentID: agentID, PolicyID: "p1", PolicyVersion: 3,
		State: state, StorageID: "dest-1", Attempt: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, r.jobs.CreateJob(context.Background(), j))
	return j
}

// ---------- completion ----------

const (
	testSHA0 = "0000000000000000000000000000000000000000000000000000000000000000"
	testSHA1 = "1111111111111111111111111111111111111111111111111111111111111111"
	testSHAM = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func testManifest(jobID string) *agentshttp.JobManifest {
	br := uint64(300)
	return &agentshttp.JobManifest{
		Objects: []agentshttp.ManifestObject{
			{Key: "vault/jobs/" + jobID + "/chunk-0", SizeBytes: 100, SHA256: testSHA0},
			{Key: "vault/jobs/" + jobID + "/chunk-1", SizeBytes: 200, SHA256: testSHA1},
			{Key: "vault/jobs/" + jobID + "/manifest.json", SizeBytes: 50, SHA256: testSHAM},
		},
		BytesRead: &br,
	}
}

func (r *protoRig) storeManifest(t *testing.T, m *agentshttp.JobManifest) {
	t.Helper()
	for _, o := range m.Objects {
		r.objects.meta[objMetaKey("dest-1", o.Key)] = domain.ObjectMeta{
			Exists: true, Size: o.SizeBytes, Checksum: "sha256:" + o.SHA256,
		}
	}
}

func TestCompleteWithManifest_VerifySuccess(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobAssigned)
	m := testManifest("job-1")
	r.storeManifest(t, m)

	out, err := r.queue.CompleteWithManifest(ctx, "ag-1", agentshttp.JobReport{
		JobID: "job-1", State: domain.JobUploading, Manifest: m,
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, out.Verified)
	require.NotEmpty(t, out.SnapshotID)

	job, err := r.jobSvc.Get(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, job.State)
	require.Equal(t, out.SnapshotID, job.SnapshotID)
}

func TestCompleteWithManifest_VerifyFailureFailsJob(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)
	m := testManifest("job-1")
	r.storeManifest(t, m)
	// chunk-1 never uploaded.
	delete(r.objects.meta, objMetaKey("dest-1", "vault/jobs/job-1/chunk-1"))

	out, err := r.queue.CompleteWithManifest(ctx, "ag-1", agentshttp.JobReport{
		JobID: "job-1", Manifest: m,
	})
	require.NoError(t, err, "verification failure is an outcome, not a call error")
	require.NotNil(t, out)
	require.False(t, out.Verified)
	require.NotEmpty(t, out.Failures)
	require.Contains(t, out.Failures[0], "chunk-1")

	job, err := r.jobSvc.Get(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, job.State)
	require.Equal(t, "verification_failed", job.ErrorCode)
}

func TestCompleteWithManifest_FailedReportHonored(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)

	out, err := r.queue.CompleteWithManifest(ctx, "ag-1", agentshttp.JobReport{
		JobID: "job-1", State: domain.JobFailed, Error: "disk on fire", Manifest: testManifest("job-1"),
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.Verified)

	job, err := r.jobSvc.Get(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, job.State)
	require.Equal(t, "disk on fire", job.ErrorMessage)
	require.Empty(t, job.SnapshotID, "failed reports must not produce snapshots")
}

func TestCompleteWithManifest_WrongAgentForbidden(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)
	_, err := r.queue.CompleteWithManifest(ctx, "ag-2", agentshttp.JobReport{
		JobID: "job-1", Manifest: testManifest("job-1"),
	})
	require.ErrorIs(t, err, domain.ErrForbidden)
}

// ---------- grants ----------

func TestGrantScoping(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-A", "ag-1", domain.JobRunning)
	r.seedJob(t, "job-B", "ag-1", domain.JobRunning)

	// Relative names derive into the job's own scope.
	grants, err := r.plane.IssueUploadGrants(ctx, "ag-1", "job-A",
		[]agentshttp.UploadObjectRequest{{Key: "chunk-0", SizeBytes: 10}})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, "vault/jobs/job-A/chunk-0", grants[0].Key)
	require.Contains(t, grants[0].URL, "vault/jobs/job-A/chunk-0")
	require.False(t, grants[0].ExpiresAt.IsZero())

	// Full key inside the job's scope is accepted.
	_, err = r.plane.IssueUploadGrants(ctx, "ag-1", "job-A",
		[]agentshttp.UploadObjectRequest{{Key: "vault/jobs/job-A/other"}})
	require.NoError(t, err)

	// Job A cannot mint for job B paths.
	_, err = r.plane.IssueUploadGrants(ctx, "ag-1", "job-A",
		[]agentshttp.UploadObjectRequest{{Key: "vault/jobs/job-B/evil"}})
	require.ErrorIs(t, err, domain.ErrValidation)

	// Traversal and absolute keys are rejected.
	for _, bad := range []string{"../escape", "/abs", "a/../../b", ""} {
		_, err = r.plane.IssueUploadGrants(ctx, "ag-1", "job-A",
			[]agentshttp.UploadObjectRequest{{Key: bad}})
		require.Error(t, err, "key %q", bad)
	}

	// Cross-agent minting is forbidden; unknown jobs are 404.
	_, err = r.plane.IssueUploadGrants(ctx, "ag-2", "job-A",
		[]agentshttp.UploadObjectRequest{{Key: "x"}})
	require.ErrorIs(t, err, domain.ErrForbidden)
	_, err = r.plane.IssueUploadGrants(ctx, "ag-1", "job-nope",
		[]agentshttp.UploadObjectRequest{{Key: "x"}})
	require.ErrorIs(t, err, domain.ErrNotFound)

	// Terminal jobs cannot mint.
	r.seedJob(t, "job-T", "ag-1", domain.JobSucceeded)
	_, err = r.plane.IssueUploadGrants(ctx, "ag-1", "job-T",
		[]agentshttp.UploadObjectRequest{{Key: "x"}})
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestMultipartFlow(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)

	// Init returns the scoped key, upload ID, and the FULL part-URL list.
	key, uploadID, urls, err := r.plane.InitMultipartUpload(ctx, "ag-1", "job-1", "big.zst", 20_000_000, 16_777_216)
	require.NoError(t, err)
	require.Equal(t, "vault/jobs/job-1/big.zst", key)
	require.NotEmpty(t, uploadID)
	require.Len(t, urls, 2, "20MB at 16MiB parts needs 2 URLs")
	require.Contains(t, urls[0], "p=1")
	require.Contains(t, urls[1], "p=2")

	// A key shaped for another job's scope is rejected at init.
	_, _, _, err = r.plane.InitMultipartUpload(ctx, "ag-1", "job-1", "vault/jobs/job-X/big.zst", 20_000_000, 16_777_216)
	require.ErrorIs(t, err, domain.ErrValidation)

	// Absurd part sizes are rejected.
	_, _, _, err = r.plane.InitMultipartUpload(ctx, "ag-1", "job-1", "big.zst", 20_000_000, 1024)
	require.ErrorIs(t, err, domain.ErrValidation)

	require.NoError(t, r.plane.CompleteMultipartUpload(ctx, "ag-1", "job-1", key, uploadID,
		[]agentshttp.MultipartPart{{PartNumber: 1, ETag: "e1"}}))
	require.Contains(t, r.objects.complete, uploadID)

	_, up2, _, err := r.plane.InitMultipartUpload(ctx, "ag-1", "job-1", "tmp.zst", 100, 16_777_216)
	require.NoError(t, err)
	require.NoError(t, r.plane.AbortMultipartUpload(ctx, "ag-1", "job-1", "vault/jobs/job-1/tmp.zst", up2))
	require.Contains(t, r.objects.aborted, up2)

	// Cross-agent multipart is forbidden.
	_, _, _, err = r.plane.InitMultipartUpload(ctx, "ag-2", "job-1", "x", 100, 16_777_216)
	require.ErrorIs(t, err, domain.ErrForbidden)
}

// ---------- restores ----------

func (r *protoRig) seedSnapshot(t *testing.T, id, agentID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, r.snaps.CreateSnapshot(ctx, &domain.Snapshot{
		ID: id, AgentID: agentID, PolicyID: "p1", PolicyVersion: 3,
		TargetType: domain.TargetHostFilesystem, Selector: "/data",
		Status: domain.SnapshotVerified, StorageID: "dest-1", ManifestID: "man-" + id,
	}))
	require.NoError(t, r.snaps.PutManifest(ctx, &domain.SnapshotManifest{
		ID: "man-" + id, SnapshotID: id, ObjectKey: "vault/jobs/job-0/manifest.json",
		Checksum: "sha256:" + testSHA0, ObjectCount: 1, BytesStored: 100,
	}))
	require.NoError(t, r.snaps.PutObjects(ctx, []domain.SnapshotObject{
		{ID: "o-1", ManifestID: "man-" + id, SnapshotID: id,
			ObjectKey: "vault/jobs/job-0/chunk-0", Kind: "chunk", Size: 100, Checksum: "sha256:" + testSHA0},
	}))
}

func (r *protoRig) seedRestore(t *testing.T, id, agentID, snapID string, state domain.JobState) {
	t.Helper()
	require.NoError(t, r.rjobs.CreateJob(context.Background(), &domain.RestoreJob{
		ID: id, AgentID: agentID, SnapshotID: snapID, State: state,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
}

func TestRestorePollGrantReportFlow(t *testing.T) {
	r := newProtoRig(t)
	r.seedSnapshot(t, "snap-1", "ag-1")
	r.seedRestore(t, "rst-1", "ag-1", "snap-1", domain.JobQueued)

	// Nothing due for other agents.
	got, err := r.plane.PollRestore(context.Background(), "ag-2")
	require.NoError(t, err)
	require.Nil(t, got)

	// Poll dispatches (queued -> assigned) with the full shape.
	asg, err := r.plane.PollRestore(context.Background(), "ag-1")
	require.NoError(t, err)
	require.NotNil(t, asg)
	require.Equal(t, "rst-1", asg.ID)
	require.Equal(t, "snap-1", asg.SnapshotID)
	require.Equal(t, "file", asg.Kind)
	require.Equal(t, "original", asg.Destination.Mode)
	require.Equal(t, "warn", asg.OverwritePolicy)
	require.False(t, asg.Encryption.Enabled)
	require.Empty(t, asg.Encryption.KeyB64)
	require.Len(t, asg.Objects, 2, "manifest artifact + data object")
	require.Equal(t, "vault/jobs/job-0/manifest.json", asg.Objects[0].Key)
	require.Equal(t, "vault/jobs/job-0/chunk-0", asg.Objects[1].Key)
	require.Equal(t, uint64(100), asg.Objects[1].SizeBytes)
	require.Equal(t, testSHA0, asg.Objects[1].SHA256, "bare hex digest")

	stored, err := r.rjobs.GetJob(context.Background(), "rst-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobAssigned, stored.State)

	// Download grants are confined to the snapshot's keys.
	grants, err := r.plane.IssueDownloadGrants(context.Background(), "ag-1", "rst-1",
		[]string{"vault/jobs/job-0/chunk-0"})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Contains(t, grants[0].URL, "vault/jobs/job-0/chunk-0")

	_, err = r.plane.IssueDownloadGrants(context.Background(), "ag-1", "rst-1",
		[]string{"vault/jobs/job-9/foreign"})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = r.plane.IssueDownloadGrants(context.Background(), "ag-2", "rst-1",
		[]string{"vault/jobs/job-0/chunk-0"})
	require.ErrorIs(t, err, domain.ErrForbidden)

	// Execution reports track state; terminal reports conflict.
	require.NoError(t, r.plane.ReportRestore(context.Background(), "ag-1", "rst-1",
		agentshttp.RestoreReport{State: domain.JobRunning}))
	require.NoError(t, r.plane.ReportRestore(context.Background(), "ag-1", "rst-1",
		agentshttp.RestoreReport{State: domain.JobUploading}))
	require.NoError(t, r.plane.ReportRestore(context.Background(), "ag-1", "rst-1",
		agentshttp.RestoreReport{State: domain.JobVerifying}))
	require.NoError(t, r.plane.ReportRestore(context.Background(), "ag-1", "rst-1",
		agentshttp.RestoreReport{State: domain.JobSucceeded}))
	stored, err = r.rjobs.GetJob(context.Background(), "rst-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, stored.State)
	require.NotNil(t, stored.FinishedAt)

	err = r.plane.ReportRestore(context.Background(), "ag-1", "rst-1",
		agentshttp.RestoreReport{State: domain.JobRunning})
	require.ErrorIs(t, err, domain.ErrConflict)
}

// ---------- key issuance + cancellations ----------

func TestPollEncryptionIssuance(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Encryption = domain.Encryption{Enabled: true, KeyRef: "enc-1"}
	ct, _, err := fakeBox{}.Seal(context.Background(), []byte("raw-data-key-32-bytes-long!!!!!!"))
	require.NoError(t, err)
	r.store.secrets["enc-1"] = &domain.SecretProfile{ID: "enc-1", Name: "enc", Kind: domain.SecretKindEncryption, Ciphertext: ct, KeyRef: "v1"}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// TLS transport: key_b64 issued + audited, never logged.
	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.True(t, queued[0].Encryption.Enabled)
	require.Equal(t, "enc-1", queued[0].Encryption.KeyRef)
	require.NotEmpty(t, queued[0].Encryption.KeyB64)
	require.Len(t, r.audit.entries, 1)
	require.Equal(t, keyIssueAuditAction, r.audit.entries[0].Action)
	require.NotContains(t, r.audit.entries[0].Detail, queued[0].Encryption.KeyB64)

	// Plaintext transport: envelope without key material.
	r.seedJob(t, "job-2", "ag-1", domain.JobQueued)
	queued, err = r.queue.Poll(context.Background(), "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.True(t, queued[0].Encryption.Enabled)
	require.Empty(t, queued[0].Encryption.KeyB64)
	require.Len(t, r.audit.entries, 1, "no issuance, no audit")
}

func TestPollEncryptionUndecryptableDegrades(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Encryption = domain.Encryption{Enabled: true, KeyRef: "enc-missing"}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1, "one bad key must not starve the queue")
	require.True(t, queued[0].Encryption.Enabled)
	require.Empty(t, queued[0].Encryption.KeyB64)
	require.Empty(t, r.audit.entries)
}

func TestQueueCancelledIDs(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)

	ids, err := r.queue.CancelledIDs(ctx, "ag-1")
	require.NoError(t, err)
	require.Empty(t, ids)

	_, err = r.jobSvc.Cancel(ctx, "job-1")
	require.NoError(t, err)
	ids, err = r.queue.CancelledIDs(ctx, "ag-1")
	require.NoError(t, err)
	require.Equal(t, []string{"job-1"}, ids)
}
