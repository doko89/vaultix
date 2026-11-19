package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaM = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func flatManifest() FlatManifest {
	return FlatManifest{
		Objects: []AgentObject{
			{Key: "jobs/job-1/chunk-0", Size: 100, SHA256: shaA},
			{Key: "jobs/job-1/chunk-1", Size: 200, SHA256: shaB},
			{Key: "jobs/job-1/manifest.json", Size: 50, SHA256: shaM},
		},
	}
}

func storeFlat(objs *fakeObjects, m FlatManifest) {
	sums := map[string]string{"chunk-0": sumA, "chunk-1": sumB, "manifest.json": sumM}
	for _, o := range m.Objects {
		name := o.Key[strings.LastIndex(o.Key, "/")+1:]
		objs.meta[objKey("store-1", o.Key)] = domain.ObjectMeta{Exists: true, Size: o.Size, Checksum: sums[name]}
	}
}

func TestCompleteJobFlat_HappyPathSucceeds(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	m := flatManifest()
	storeFlat(objs, m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.OK)
	require.Empty(t, res.Failures)

	job, err := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, job.State)
	require.NotEmpty(t, job.SnapshotID)
	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotVerified, snap.Status)
	require.True(t, snap.IsRestorable())
	require.Equal(t, 2, snap.ObjectCount, "manifest artifact is not a data object")
}

func TestCompleteJobFlat_VerifyFailureFailsJob(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	m := flatManifest()
	storeFlat(objs, m)
	// chunk-1 never uploaded: verification must fail the job, never succeed it.
	delete(objs.meta, objKey("store-1", "jobs/job-1/chunk-1"))

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.Error(t, err)
	require.NotNil(t, res, "verification ran, so the result accompanies the error")
	require.False(t, res.OK)

	var cerr *CompletionError
	require.True(t, errors.As(err, &cerr))
	require.Equal(t, "verification_failed", cerr.Code)
	require.NotNil(t, cerr.Result)
	require.Contains(t, cerr.Message, "jobs/job-1/chunk-1")

	job, jerr := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, jerr)
	require.Equal(t, domain.JobFailed, job.State)
	require.Equal(t, "verification_failed", job.ErrorCode)
	require.Contains(t, job.ErrorMessage, "jobs/job-1/chunk-1")
	snap, _ := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.Equal(t, domain.SnapshotFailed, snap.Status)
	require.False(t, snap.IsRestorable())
}

func TestCompleteJobFlat_MissingManifestObjectFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	m := flatManifest()
	m.Objects = m.Objects[:2] // no manifest.json
	storeFlat(objs, m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.Error(t, err)
	require.Nil(t, res, "verification never ran")
	var cerr *CompletionError
	require.True(t, errors.As(err, &cerr))
	require.Equal(t, "manifest_missing", cerr.Code)
	require.Contains(t, cerr.Message, "manifest.json")

	job, _ := jobs.GetJob(context.Background(), "job-1")
	require.Equal(t, domain.JobFailed, job.State)
	require.Equal(t, "manifest_missing", job.ErrorCode)
}

func TestCompleteJobFlat_BadSHAFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	m := flatManifest()
	m.Objects[0].SHA256 = "not-hex"
	storeFlat(objs, m)

	_, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.Error(t, err)
	var cerr *CompletionError
	require.True(t, errors.As(err, &cerr))
	require.Equal(t, "manifest_invalid", cerr.Code)
	require.Contains(t, cerr.Message, "64 hex")

	job, _ := jobs.GetJob(context.Background(), "job-1")
	require.Equal(t, domain.JobFailed, job.State)
}

func TestCompleteJobFlat_EncryptionMismatchFails(t *testing.T) {
	d, pol, jobs, _, _, objs := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	j := seedJob(jobs, "job-1")
	j.Encrypted = true
	j.KeyRef = "enc-key-1"
	m := flatManifest()
	off := false
	m.Encrypted = &off
	storeFlat(objs, m)

	_, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.Error(t, err)
	var cerr *CompletionError
	require.True(t, errors.As(err, &cerr))
	require.Equal(t, "manifest_invalid", cerr.Code)
	require.Contains(t, cerr.Message, "enc-key-1")

	job, _ := jobs.GetJob(context.Background(), "job-1")
	require.Equal(t, domain.JobFailed, job.State)
}

func TestCompleteJobFlat_WrongStateRefused(t *testing.T) {
	d, pol, jobs, _, _, _ := testDeps()
	s := mustService(t, d)
	seedVersion(t, pol)
	j := &domain.BackupJob{ID: "job-1", AgentID: "a", PolicyID: "pol-1", PolicyVersion: 3, State: domain.JobRunning}
	require.NoError(t, jobs.CreateJob(context.Background(), j))
	_, err := s.CompleteJobFlat(context.Background(), "job-1", flatManifest())
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestCompletionErrorFormat(t *testing.T) {
	err := &CompletionError{Code: "verification_failed", Message: "1 failure"}
	require.Equal(t, "backup completion failed [verification_failed]: 1 failure", err.Error())
}

// dbAssignedSnapshots simulates the SQL store: CreateSnapshot ignores the
// assembled ID and writes back a database-assigned one, like the
// gen_random_uuid() column default.
type dbAssignedSnapshots struct {
	domain.SnapshotRepository
	inner    *fakeSnapshots
	assigned string
}

func (f *dbAssignedSnapshots) CreateSnapshot(ctx context.Context, s *domain.Snapshot) error {
	s.ID = f.assigned
	return f.inner.CreateSnapshot(ctx, s)
}

func TestCompleteJobFlat_StoredSnapshotIDPropagates(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	d.Snapshots = &dbAssignedSnapshots{SnapshotRepository: snaps, inner: snaps, assigned: "db-snap-9"}
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1")
	m := flatManifest()
	storeFlat(objs, m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.NoError(t, err)
	require.True(t, res.OK)

	job, err := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "db-snap-9", job.SnapshotID)

	man, err := snaps.GetManifest(context.Background(), "db-snap-9")
	require.NoError(t, err, "manifest must reference the stored snapshot ID")
	require.Equal(t, "db-snap-9", man.SnapshotID)
	for _, o := range snaps.objects[man.ID] {
		require.Equal(t, "db-snap-9", o.SnapshotID)
	}
	require.NotEmpty(t, snaps.objects[man.ID])
}
