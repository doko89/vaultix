package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "agent-state.json")

	s, err := NewStore(path)
	require.NoError(t, err)
	assert.Empty(t, s.All())

	require.NoError(t, s.Upsert(JobRecord{
		JobID: "job-1", Policy: "daily", PolicyVersion: 2, State: JobRunning, Attempt: 1,
	}))
	require.NoError(t, s.Upsert(JobRecord{
		JobID: "job-2", Policy: "daily", PolicyVersion: 2, State: JobSucceeded, Attempt: 1,
	}))

	// File exists, is owner-only, and no temp files leak.
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".agent-state-*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)

	// Reopen: records survive the restart.
	s2, err := NewStore(path)
	require.NoError(t, err)
	rec, ok := s2.Get("job-1")
	require.True(t, ok)
	assert.Equal(t, JobRunning, rec.State)
	assert.Equal(t, "daily", rec.Policy)
	assert.Equal(t, 2, rec.PolicyVersion)
	assert.False(t, rec.UpdatedAt.IsZero())

	// Incomplete() returns only non-terminal records.
	inc := s2.Incomplete()
	require.Len(t, inc, 1)
	assert.Equal(t, "job-1", inc[0].JobID)

	// Delete persists too.
	require.NoError(t, s2.Delete("job-1"))
	s3, err := NewStore(path)
	require.NoError(t, err)
	_, ok = s3.Get("job-1")
	assert.False(t, ok)
}

func TestStoreCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-state.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	_, err := NewStore(path)
	require.Error(t, err)
}

func TestStoreBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":999,"jobs":{}}`), 0o600))
	_, err := NewStore(path)
	require.ErrorContains(t, err, "unsupported state version")
}
