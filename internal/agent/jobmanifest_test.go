package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeStaged reverses the data pipeline: optional envelope, then zstd.
func decodeStaged(t *testing.T, art *Artifact, keyB64 string) []byte {
	t.Helper()
	raw, err := os.ReadFile(art.Path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), art.SHA256, "artifact sha must cover staged bytes")
	require.Equal(t, int64(len(raw)), art.SizeBytes)
	payload := io.Reader(bytes.NewReader(raw))
	if keyB64 != "" {
		var dec bytes.Buffer
		_, err = DecryptStream(&dec, payload, keyB64)
		require.NoError(t, err)
		payload = &dec
	}
	zr, err := zstd.NewReader(payload)
	require.NoError(t, err)
	defer zr.Close()
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return out
}

func TestStageJobManifestPlain(t *testing.T) {
	exec := &Executor{WorkDir: t.TempDir(), CompressionLevel: 1}
	a := stageFile(t, "t0-host_filesystem.tar.zst", []byte("payload"))
	a.SHA256 = "abc123"
	a.BytesRead = 9
	a.TargetType = TargetHostFilesystem
	up := []UploadedObject{{Artifact: a, Key: "jobs/job-1/t0-host_filesystem.tar.zst"}}

	art, err := exec.StageJobManifest(
		PolledJob{ID: "job-1", PolicyID: "p", PolicyVersion: 2}, "ag-1",
		[]*Artifact{a}, up)
	require.NoError(t, err)
	assert.Equal(t, ManifestObjectName, art.Name)
	assert.Equal(t, "manifest", art.TargetType)
	assert.False(t, art.Encrypted)

	var doc JobManifestDoc
	require.NoError(t, json.Unmarshal(decodeStaged(t, art, ""), &doc))
	assert.Equal(t, 1, doc.Version)
	assert.Equal(t, "job-1", doc.JobID)
	assert.Equal(t, "ag-1", doc.AgentID)
	assert.Equal(t, "p", doc.PolicyID)
	assert.Equal(t, 2, doc.PolicyVersion)
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "t0-host_filesystem.tar.zst", doc.Objects[0].Name)
	assert.Equal(t, "jobs/job-1/t0-host_filesystem.tar.zst", doc.Objects[0].Key)
	assert.Equal(t, "abc123", doc.Objects[0].SHA256)
	assert.Equal(t, int64(9), doc.BytesRead)
	assert.Equal(t, "zstd", doc.Compression.Algo)
	assert.False(t, doc.Encryption.Enabled)
}

func TestStageJobManifestEncrypted(t *testing.T) {
	exec := &Executor{WorkDir: t.TempDir(), CompressionLevel: 1}
	keyB64 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32))
	a := stageFile(t, "t0-postgres.sql.zst", []byte("dump"))
	a.SHA256 = "def456"
	a.TargetType = TargetPostgres
	a.Encrypted = true
	up := []UploadedObject{{Artifact: a, Key: "jobs/job-9/t0-postgres.sql.zst"}}

	art, err := exec.StageJobManifest(
		PolledJob{ID: "job-9", Encryption: EncryptionSpec{Enabled: true, KeyRef: "enc-1", KeyB64: keyB64}},
		"ag-1", []*Artifact{a}, up)
	require.NoError(t, err)
	assert.True(t, art.Encrypted)

	var doc JobManifestDoc
	require.NoError(t, json.Unmarshal(decodeStaged(t, art, keyB64), &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "jobs/job-9/t0-postgres.sql.zst", doc.Objects[0].Key)
	assert.True(t, doc.Objects[0].Encrypted)
	assert.True(t, doc.Encryption.Enabled)
	assert.Equal(t, "enc-1", doc.Encryption.KeyRef)
	assert.NotContains(t, string(decodeStaged(t, art, keyB64)), keyB64, "key material must never land in the doc")
}

func TestStageJobManifestRequiresKeys(t *testing.T) {
	exec := &Executor{WorkDir: t.TempDir()}
	a := stageFile(t, "t0.tar.zst", []byte("x"))
	_, err := exec.StageJobManifest(PolledJob{ID: "job-1"}, "ag-1",
		[]*Artifact{a}, nil)
	require.ErrorContains(t, err, "no uploaded key")
}

func TestStageJobManifestRejectsMissingJob(t *testing.T) {
	exec := &Executor{WorkDir: t.TempDir()}
	_, err := exec.StageJobManifest(PolledJob{}, "ag-1", nil, nil)
	require.ErrorContains(t, err, "missing job id")
}

func TestUploadWithManifestUploadsDataThenManifest(t *testing.T) {
	f, srv := newFakeGrantServer(t, 0)
	stub := &StubManagerClient{}
	var calls int
	stub.OnUploadGrants = func(jobID string, req UploadGrantsRequest) (UploadGrantsResponse, error) {
		calls++
		grants := make([]UploadGrant, 0, len(req.Objects))
		for _, o := range req.Objects {
			grants = append(grants, UploadGrant{
				Key: fmt.Sprintf("jobs/%s/%s", jobID, o.Key),
				URL: fmt.Sprintf("%s/put/%s?sig=x", srv.URL, o.Key),
			})
		}
		return UploadGrantsResponse{Grants: grants}, nil
	}
	exec := &Executor{WorkDir: t.TempDir(), CompressionLevel: 1}
	up := &Uploader{Manager: stub, NewBackOff: fastBackoff}
	a := stageFile(t, "t0.tar.zst", []byte("data-bytes"))
	a.SHA256 = "aaa"
	a.TargetType = TargetHostFilesystem

	got, err := uploadWithManifest(context.Background(),
		PolledJob{ID: "job-1", PolicyID: "p"}, "ag-1", exec, up, []*Artifact{a})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, calls, "data grants, then manifest grants")
	assert.Equal(t, "jobs/job-1/t0.tar.zst", got[0].Key)
	assert.Equal(t, "jobs/job-1/manifest.json", got[1].Key)
	assert.Contains(t, string(f.store["/put/t0.tar.zst"]), "data-bytes")

	var doc JobManifestDoc
	dec, err := zstd.NewReader(bytes.NewReader(f.store["/put/manifest.json"]))
	require.NoError(t, err)
	raw, err := io.ReadAll(dec)
	dec.Close()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "jobs/job-1/t0.tar.zst", doc.Objects[0].Key)
}
