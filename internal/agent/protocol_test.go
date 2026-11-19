package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollResponseToleratesLegacyShape(t *testing.T) {
	// Pre-P3 manager: string targets, no encryption, no cancelled_job_ids.
	raw := `{"jobs":[{"id":"j-1","policy_id":"p","policy_version":2,"kind":"filesystem",
		"targets":["/var/www"],"state":"assigned","attempt":1}]}`
	var resp PollResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	require.Len(t, resp.Jobs, 1)
	j := resp.Jobs[0]
	assert.Equal(t, "j-1", j.ID)
	require.Len(t, j.Targets, 1)
	assert.Equal(t, "/var/www", j.Targets[0].Selector)
	assert.Empty(t, j.Targets[0].Type)
	assert.False(t, j.EncryptionEnabled(), "missing encryption defaults to unencrypted")
	assert.Empty(t, resp.CancelledJobIDs)
}

func TestPollResponseFullShape(t *testing.T) {
	raw := `{"jobs":[{"id":"j-2","policy_id":"p","policy_version":1,"kind":"postgres",
		"targets":[{"type":"postgres","selector":"appdb","options":{"host":"db"}}],
		"encryption":{"enabled":true,"key_ref":"k1","key_b64":"QUJD"}}],
		"cancelled_job_ids":["j-9"]}`
	var resp PollResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	require.Len(t, resp.Jobs, 1)
	j := resp.Jobs[0]
	assert.True(t, j.EncryptionEnabled())
	assert.Equal(t, "k1", j.Encryption.KeyRef)
	assert.Equal(t, "postgres", j.Targets[0].Type)
	assert.Equal(t, "appdb", j.Targets[0].Selector)
	assert.Equal(t, []string{"j-9"}, resp.CancelledJobIDs)
}

func TestNormalizeTargetType(t *testing.T) {
	cases := map[string]string{
		"host_filesystem": "host_filesystem",
		"filesystem":      "host_filesystem",
		"docker-volume":   "docker_volume",
		"docker_volume":   "docker_volume",
		"postgres":        "postgres",
		"mysql":           "mysql",
		"mssql":           "mssql",
		"redis":           "redis",
		"stack":           "stack",
		"weird-thing":     "weird-thing", // passes through for structured error
		"":                "",
	}
	for in, want := range cases {
		assert.Equal(t, want, NormalizeTargetType(in), "input %q", in)
	}
}

func TestKindToTargetType(t *testing.T) {
	assert.Equal(t, "host_filesystem", KindToTargetType("filesystem"))
	assert.Equal(t, "docker_volume", KindToTargetType("docker-volume"))
	assert.Equal(t, "postgres", KindToTargetType("postgres"))
	assert.Equal(t, "mssql", KindToTargetType("mssql"))
	assert.Equal(t, "restore-file", KindToTargetType("restore-file"))
}

func TestRestoreOverwritePolicy(t *testing.T) {
	allow := []string{"allow", "overwrite", "ALLOW", " Overwrite "}
	for _, p := range allow {
		assert.True(t, RestoreJob{OverwritePolicy: p}.OverwriteAllowed(), "policy %q", p)
	}
	deny := []string{"", "refuse", "deny", "never", "false"}
	for _, p := range deny {
		assert.False(t, RestoreJob{OverwritePolicy: p}.OverwriteAllowed(), "policy %q", p)
	}
}

func TestManifestJSONShape(t *testing.T) {
	m := Manifest{
		Objects:         []ObjectManifest{{Key: "p/jobs/j/t0.tar.zst", SizeBytes: 42, SHA256: "abc"}},
		BytesRead:       100,
		BytesCompressed: 50,
		BytesUploaded:   60,
		Compression:     CompressionInfo{Algo: "zstd", Level: 3},
		Encryption:      ManifestEncryption{Enabled: true, KeyRef: "k1"},
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Contains(t, string(raw), `"algo":"zstd"`)
	assert.NotContains(t, string(raw), "key_b64", "manifest must never carry key material")
	assert.Equal(t, "k1", decoded["encryption"].(map[string]any)["key_ref"])
}
