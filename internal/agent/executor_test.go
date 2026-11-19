package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testExecutor(t *testing.T) *Executor {
	t.Helper()
	return &Executor{WorkDir: t.TempDir(), CompressionLevel: 1}
}

func TestDispatchMatrix(t *testing.T) {
	// pg/mysql fakes for the db rows of the matrix.
	fakeBin(t, "pg_dump", "printf 'PGDATA'\n")
	fakeBin(t, "mysqldump", "printf 'MYDATA'\n")

	fsRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fsRoot, "a.txt"), []byte("filedata"), 0o644))
	volDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(volDir, "v.bin"), []byte("voldata"), 0o644))

	exec := testExecutor(t)
	exec.VolumeResolver = stubVolumeResolver{vol: DiscoveredVolume{Name: "vol1", Driver: "local", Mountpoint: volDir}}

	cases := []struct {
		name       string
		targetType string
		selector   string
		wantErr    string
		wantBytes  string // expected decompressed payload fragment
	}{
		{"host_filesystem", "host_filesystem", fsRoot, "", "filedata"},
		{"fs alias", "filesystem", fsRoot, "", "filedata"},
		{"docker_volume", "docker_volume", "vol1", "", "voldata"},
		{"docker alias", "docker-volume", "vol1", "", "voldata"},
		{"postgres", "postgres", "appdb", "", "PGDATA"},
		{"mysql", "mysql", "shopdb", "", "MYDATA"},
		{"mssql unsupported", "mssql", "db", "unsupported target type", ""},
		{"redis unsupported", "redis", "cache", "unsupported target type", ""},
		{"stack unsupported", "stack", "web", "unsupported target type", ""},
		{"unknown unsupported", "tape-robot", "x", "unsupported target type", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := PolledJob{ID: "job-" + tc.name, Kind: "backup",
				Targets: []Target{{Type: tc.targetType, Selector: tc.selector}}}
			art, err := exec.ExecuteTarget(context.Background(), job, 0, job.Targets[0])
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				var uerr *UnsupportedError
				require.ErrorAs(t, err, &uerr, "must be a structured unsupported error, not a crash")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, art)
			// Staged bytes decompress to a payload containing the source data.
			raw, err := os.ReadFile(art.Path)
			require.NoError(t, err)
			dec, err := zstd.NewReader(bytes.NewReader(raw))
			require.NoError(t, err)
			defer dec.Close()
			var plain bytes.Buffer
			_, err = plain.ReadFrom(dec)
			require.NoError(t, err)
			assert.Contains(t, plain.String(), tc.wantBytes)
			assert.False(t, art.Encrypted)
		})
	}
}

func TestDispatchFallsBackToJobKind(t *testing.T) {
	fsRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fsRoot, "a.txt"), []byte("x"), 0o644))
	exec := testExecutor(t)
	job := PolledJob{ID: "j-kind", Kind: "filesystem",
		Targets: []Target{{Selector: fsRoot}}} // legacy: no type
	art, err := exec.ExecuteTarget(context.Background(), job, 0, job.Targets[0])
	require.NoError(t, err)
	assert.Equal(t, TargetHostFilesystem, art.TargetType)
}

func TestDispatchEmptySelectorRejected(t *testing.T) {
	exec := testExecutor(t)
	job := PolledJob{ID: "j-empty", Kind: "backup",
		Targets: []Target{{Type: "host_filesystem", Selector: "  "}}}
	_, err := exec.ExecuteTarget(context.Background(), job, 0, job.Targets[0])
	require.ErrorContains(t, err, "empty selector")
}

func TestEncryptedJobRequiresKey(t *testing.T) {
	exec := testExecutor(t)
	job := PolledJob{ID: "j-nokey", Kind: "filesystem",
		Targets:    []Target{{Type: "host_filesystem", Selector: t.TempDir()}},
		Encryption: EncryptionSpec{Enabled: true},
	}
	_, err := exec.ExecuteJob(context.Background(), job)
	require.ErrorContains(t, err, "no key")
}

func TestExecuteJobEncryptedArtifact(t *testing.T) {
	fsRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fsRoot, "a.txt"), []byte("secret-data"), 0o644))
	exec := testExecutor(t)
	key := testKeyB64(t)
	job := PolledJob{ID: "j-enc", Kind: "filesystem",
		Targets:    []Target{{Type: "host_filesystem", Selector: fsRoot}},
		Encryption: EncryptionSpec{Enabled: true, KeyRef: "k1", KeyB64: key},
	}
	arts, err := exec.ExecuteJob(context.Background(), job)
	require.NoError(t, err)
	require.Len(t, arts, 1)
	art := arts[0]
	assert.True(t, art.Encrypted)
	assert.Equal(t, int64(len("secret-data")), art.BytesRead)

	raw, err := os.ReadFile(art.Path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "secret-data")
	// sha256 in the artifact matches the staged bytes.
	sum := sha256.Sum256(raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), art.SHA256)
	// Decrypt → decompress → payload contains the source.
	var dec bytes.Buffer
	_, err = DecryptStream(&dec, bytes.NewReader(raw), key)
	require.NoError(t, err)
	zr, err := zstd.NewReader(&dec)
	require.NoError(t, err)
	defer zr.Close()
	var plain bytes.Buffer
	_, err = plain.ReadFrom(zr)
	require.NoError(t, err)
	assert.Contains(t, plain.String(), "secret-data")

	exec.Cleanup(job.ID)
	_, err = os.Stat(art.Path)
	assert.True(t, os.IsNotExist(err), "cleanup removes staged files")
}

func TestExecuteJobStopsAtFirstError(t *testing.T) {
	exec := testExecutor(t)
	job := PolledJob{ID: "j-partial", Kind: "backup", Targets: []Target{
		{Type: "mssql", Selector: "db"}, // unsupported → fail fast
		{Type: "redis", Selector: "c"},
	}}
	_, err := exec.ExecuteJob(context.Background(), job)
	var uerr *UnsupportedError
	require.ErrorAs(t, err, &uerr)
	assert.Equal(t, "mssql", uerr.TargetType)
}

func TestExecuteJobRejectsEmpty(t *testing.T) {
	exec := testExecutor(t)
	_, err := exec.ExecuteJob(context.Background(), PolledJob{ID: "x"})
	require.ErrorContains(t, err, "no targets")
	_, err = exec.ExecuteJob(context.Background(), PolledJob{})
	require.ErrorContains(t, err, "missing id")
}
