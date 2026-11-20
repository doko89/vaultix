package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

// fakeDestinations serves one configured default (or an error) and counts
// resolutions, proving the explicit path never consults the default.
type fakeDestinations struct {
	def   *domain.StorageDestination
	err   error
	calls int
}

func (f *fakeDestinations) GetDefaultDestination(_ context.Context) (*domain.StorageDestination, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.def, nil
}

// seedEmptyJob seeds an uploading job that names no destination: empty
// StorageID contractually means the default destination.
func seedEmptyJob(jobs *fakeJobs, id string) *domain.BackupJob {
	j := seedJob(jobs, id)
	j.StorageID = ""
	return j
}

// storeFlatUnder stores a flat manifest's objects under one destination.
func storeFlatUnder(objs *fakeObjects, dest string, m FlatManifest) {
	sums := map[string]string{"chunk-0": sumA, "chunk-1": sumB, "manifest.json": sumM}
	for _, o := range m.Objects {
		name := o.Key[strings.LastIndex(o.Key, "/")+1:]
		objs.meta[objKey(dest, o.Key)] = domain.ObjectMeta{Exists: true, Size: o.Size, Checksum: sums[name]}
	}
}

// storeAllUnder stores a structured report's objects under one destination.
func storeAllUnder(objs *fakeObjects, dest string, rep CompletionReport) {
	objs.meta[objKey(dest, rep.ManifestObjectKey)] = domain.ObjectMeta{Exists: true, Size: 64, Checksum: rep.ManifestChecksum}
	for _, tr := range rep.Targets {
		for _, o := range tr.Objects {
			objs.meta[objKey(dest, o.ObjectKey)] = domain.ObjectMeta{Exists: true, Size: o.Size, Checksum: o.Checksum}
		}
	}
}

// ---------- resolver precedence: explicit > default > error ----------

func TestResolveStorageID_Precedence(t *testing.T) {
	ctx := context.Background()
	newSvc := func(dests DefaultDestinationStore) *Service {
		d, _, _, _, _, _ := testDeps()
		d.Destinations = dests
		return mustService(t, d)
	}

	t.Run("explicit wins without consulting the default", func(t *testing.T) {
		dests := &fakeDestinations{def: &domain.StorageDestination{ID: "dest-default"}}
		got, err := newSvc(dests).resolveStorageID(ctx, &domain.BackupJob{ID: "job-1", StorageID: "store-1"})
		require.NoError(t, err)
		require.Equal(t, "store-1", got)
		require.Zero(t, dests.calls, "explicit StorageID must never consult the default")
	})

	t.Run("empty resolves the default once", func(t *testing.T) {
		dests := &fakeDestinations{def: &domain.StorageDestination{ID: "dest-default"}}
		got, err := newSvc(dests).resolveStorageID(ctx, &domain.BackupJob{ID: "job-1"})
		require.NoError(t, err)
		require.Equal(t, "dest-default", got)
		require.Equal(t, 1, dests.calls)
	})

	t.Run("blank resolves the default too", func(t *testing.T) {
		dests := &fakeDestinations{def: &domain.StorageDestination{ID: "dest-default"}}
		got, err := newSvc(dests).resolveStorageID(ctx, &domain.BackupJob{ID: "job-1", StorageID: "  "})
		require.NoError(t, err)
		require.Equal(t, "dest-default", got)
	})

	t.Run("empty with no default names cause and remediation", func(t *testing.T) {
		dests := &fakeDestinations{err: domain.ErrNotFound}
		_, err := newSvc(dests).resolveStorageID(ctx, &domain.BackupJob{ID: "job-1"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "job-1")
		require.Contains(t, err.Error(), "no default is configured")
		require.Contains(t, err.Error(), "set storage_id on the policy or configure a default destination")
		require.NotContains(t, err.Error(), "destination id is required",
			"must never surface the raw store error")
	})

	t.Run("empty with unwired seam fails the same actionable way", func(t *testing.T) {
		_, err := newSvc(nil).resolveStorageID(ctx, &domain.BackupJob{ID: "job-1"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "set storage_id on the policy or configure a default destination")
	})
}

// ---------- regression: empty storage_id + default configured ----------

func TestCompleteJobFlat_EmptyStorageIDFallsBackToDefault(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	d.Destinations = &fakeDestinations{def: &domain.StorageDestination{ID: "dest-default"}}
	s := mustService(t, d)
	seedVersion(t, pol)
	seedEmptyJob(jobs, "job-1")
	m := flatManifest()
	// Bytes land where grants put them: under the default destination only.
	// Nothing is stored under "" — any HEAD against the empty ID misses.
	storeFlatUnder(objs, "dest-default", m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.OK, "verify HEADs must hit the default destination: %+v", res.Failures)
	require.Empty(t, res.Failures)

	job, err := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, job.State)
	require.Equal(t, "dest-default", job.StorageID, "job must record the resolved destination")

	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotVerified, snap.Status)
	require.Equal(t, "dest-default", snap.StorageID, "snapshot must stamp the resolved destination, never empty")

	man, err := snaps.GetManifest(context.Background(), snap.ID)
	require.NoError(t, err)
	require.Equal(t, snap.ID, man.SnapshotID)
	require.Equal(t, 2, man.ObjectCount)
	require.NotEmpty(t, snaps.objects[man.ID], "manifest objects must be registered")
}

// The structured path shares runCompletion, so it inherits the same fix.
func TestCompleteJob_EmptyStorageIDFallsBackToDefault(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	d.Destinations = &fakeDestinations{def: &domain.StorageDestination{ID: "dest-default"}}
	s := mustService(t, d)
	seedVersion(t, pol)
	seedEmptyJob(jobs, "job-1")
	rep := fileReport()
	storeAllUnder(objs, "dest-default", rep)

	res, job := complete(t, s, jobs, rep)
	require.NotNil(t, res)
	require.True(t, res.OK, "verify HEADs must hit the default destination: %+v", res.Failures)
	require.Equal(t, domain.JobSucceeded, job.State)

	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, domain.SnapshotVerified, snap.Status)
	require.Equal(t, "dest-default", snap.StorageID)
}

// ---------- no default configured: one actionable failure ----------

func TestCompleteJobFlat_EmptyStorageIDNoDefaultFailsActionably(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	d.Destinations = &fakeDestinations{err: domain.ErrNotFound}
	s := mustService(t, d)
	seedVersion(t, pol)
	seedEmptyJob(jobs, "job-1")
	m := flatManifest()
	storeFlatUnder(objs, "dest-default", m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.Nil(t, res, "no verification runs without a destination")
	require.Error(t, err)
	var cerr *CompletionError
	require.True(t, errors.As(err, &cerr))
	require.Equal(t, "destination_missing", cerr.Code)
	require.Contains(t, cerr.Message, "no default is configured")
	require.Contains(t, cerr.Message, "set storage_id on the policy or configure a default destination")
	require.NotContains(t, cerr.Message, "destination id is required")

	job, jerr := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, jerr)
	require.Equal(t, domain.JobFailed, job.State)
	require.Equal(t, "destination_missing", job.ErrorCode)
	require.Empty(t, job.SnapshotID, "nothing is stamped when resolution fails")
	require.Empty(t, snaps.snaps, "no snapshot record without a destination")
}

// ---------- explicit path unchanged ----------

func TestCompleteJobFlat_ExplicitStorageIDSkipsDefault(t *testing.T) {
	d, pol, jobs, snaps, _, objs := testDeps()
	dests := &fakeDestinations{err: errors.New("default must not be consulted")}
	d.Destinations = dests
	s := mustService(t, d)
	seedVersion(t, pol)
	seedJob(jobs, "job-1") // explicit StorageID "store-1"
	m := flatManifest()
	storeFlat(objs, m)

	res, err := s.CompleteJobFlat(context.Background(), "job-1", m)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Zero(t, dests.calls)

	job, err := jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, domain.JobSucceeded, job.State)
	snap, err := snaps.GetSnapshot(context.Background(), job.SnapshotID)
	require.NoError(t, err)
	require.Equal(t, "store-1", snap.StorageID, "explicit StorageID stamps through untouched")
}
