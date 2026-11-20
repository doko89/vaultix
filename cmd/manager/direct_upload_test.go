package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	"github.com/vaultix/vaultix/internal/domain"
)

// ---------- Direct-upload issuance (poll enrichment) ----------

const (
	testStorageAccess = "AKIA-DIRECT-TEST-ONLY"
	testStorageSecret = "direct-secret-marker-9f8e7d6c"
)

// sealStorageSecret stores a storage secret profile sealed with the rig's box.
func sealStorageSecret(t *testing.T, r *protoRig, id, payload string) {
	t.Helper()
	ct, _, err := fakeBox{}.Seal(context.Background(), []byte(payload))
	require.NoError(t, err)
	r.store.secrets[id] = &domain.SecretProfile{
		ID: id, Name: id, Kind: domain.SecretKindStorage, Ciphertext: ct, KeyRef: "v1",
	}
}

// directDest turns dest-1 into a direct-mode destination with a full row.
func directDest(r *protoRig, ref string) *domain.StorageDestination {
	d := r.store.dests["dest-1"]
	d.Name = "rustfs"
	d.Endpoint = "https://rustfs.internal:9000"
	d.Region = "us-east-1"
	d.Bucket = "backups"
	d.Prefix = "vault"
	d.CredentialRef = ref
	d.UsePathStyle = true
	d.UploadMode = domain.UploadModeDirect
	return d
}

// refusedDirectJob loads the job and asserts the direct-upload refusal
// contract: terminal failed, refusal code, actionable secret-free message.
func refusedDirectJob(t *testing.T, r *protoRig, jobID string, wantSubstr ...string) *domain.BackupJob {
	t.Helper()
	stored, err := r.jobs.GetJob(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, stored.State, "refused poll items must fail, never dispatch")
	require.Equal(t, directUploadRefusedCode, stored.ErrorCode)
	for _, want := range wantSubstr {
		require.Contains(t, stored.ErrorMessage, want)
	}
	require.NotContains(t, stored.ErrorMessage, testStorageSecret, "refusal reasons must never carry secret material")
	require.NotContains(t, stored.ErrorMessage, testStorageAccess)
	return stored
}

func TestPollDirectUploadIssuedOverTLS(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "st-1")
	sealStorageSecret(t, r, "st-1", `{"access_key_id":"`+testStorageAccess+`","secret_access_key":"`+testStorageSecret+`"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)
	before := *r.store.dests["dest-1"]

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)

	// All seven credential fields resolve from row + profile.
	require.Equal(t, "direct", queued[0].UploadMode)
	require.NotNil(t, queued[0].DestinationCredential)
	cred := queued[0].DestinationCredential
	assert.Equal(t, testStorageAccess, cred.AccessKeyID)
	assert.Equal(t, testStorageSecret, cred.SecretAccessKey)
	assert.Equal(t, "https://rustfs.internal:9000", cred.Endpoint)
	assert.Equal(t, "us-east-1", cred.Region)
	assert.Equal(t, "backups", cred.Bucket)
	assert.True(t, cred.UsePathStyle)
	assert.Equal(t, "vault", cred.Prefix)

	// Exact poll JSON shape (the sibling contract).
	raw, err := json.Marshal(queued[0])
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "direct", wire["upload_mode"])
	dc, ok := wire["destination_credential"].(map[string]any)
	require.True(t, ok, "destination_credential must be an object: %s", raw)
	require.Len(t, dc, 7, "all seven fields, no more: %s", raw)
	assert.Equal(t, testStorageAccess, dc["access_key_id"])
	assert.Equal(t, testStorageSecret, dc["secret_access_key"])
	assert.Equal(t, "https://rustfs.internal:9000", dc["endpoint"])
	assert.Equal(t, "us-east-1", dc["region"])
	assert.Equal(t, "backups", dc["bucket"])
	assert.Equal(t, true, dc["use_path_style"])
	assert.Equal(t, "vault", dc["prefix"])

	// The destination row is never mutated by issuance.
	assert.Equal(t, before, *r.store.dests["dest-1"])

	// Issuance is audited with refs only — no secret material anywhere.
	require.Len(t, r.audit.entries, 1)
	entry := r.audit.entries[0]
	assert.Equal(t, directUploadAuditAction, entry.Action)
	assert.Equal(t, "secret", entry.TargetType)
	assert.Equal(t, "st-1", entry.TargetID)
	assert.Contains(t, entry.Detail, "ag-1")
	assert.Contains(t, entry.Detail, "job-1")
	assert.Contains(t, entry.Detail, "dest-1")
	assert.NotContains(t, entry.Detail, testStorageSecret)
	assert.NotContains(t, entry.Detail, testStorageAccess)
}

func TestPollDirectUploadPresignedOmitsFields(t *testing.T) {
	for _, mode := range []domain.UploadMode{domain.UploadModePresigned, ""} {
		t.Run("mode="+string(mode), func(t *testing.T) {
			r := newProtoRig(t)
			r.store.dests["dest-1"].UploadMode = mode // "" = legacy row
			r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

			tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
			queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
			require.NoError(t, err)
			require.Len(t, queued, 1)
			assert.Empty(t, queued[0].UploadMode)
			assert.Nil(t, queued[0].DestinationCredential)

			raw, err := json.Marshal(queued[0])
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "upload_mode", "presigned poll shape must be byte-identical")
			assert.NotContains(t, string(raw), "destination_credential")
			assert.Empty(t, r.audit.entries, "presigned dispatch audits no issuance")
		})
	}
}

func TestPollDirectUploadRefusedOverPlaintext(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "st-1")
	sealStorageSecret(t, r, "st-1", `{"access_key_id":"`+testStorageAccess+`","secret_access_key":"`+testStorageSecret+`"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// Plaintext transport: the item is refused (failed), never silently
	// downgraded to presigned — and the poll itself stays healthy.
	queued, err := r.queue.Poll(context.Background(), "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued, "refused items must not dispatch")
	refusedDirectJob(t, r, "job-1", "TLS", "rustfs")
	require.Empty(t, r.audit.entries, "refused issuance audits nothing")
}

func TestPollDirectUploadMissingRef(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "") // direct mode but no credential profile
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedDirectJob(t, r, "job-1", "rustfs", "credential-profile reference", "presigned")
	require.Empty(t, r.audit.entries)
}

func TestPollDirectUploadMissingProfile(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "st-missing") // dangling ref
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedDirectJob(t, r, "job-1", "st-missing", "unavailable")
	require.Empty(t, r.audit.entries)
}

func TestPollDirectUploadInvalidProfiles(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"not JSON", `not-json-at-all`, "not valid JSON"},
		{"missing access_key_id", `{"secret_access_key":"` + testStorageSecret + `"}`, `"access_key_id"`},
		{"missing secret_access_key", `{"access_key_id":"` + testStorageAccess + `"}`, `"secret_access_key"`},
		{"empty secret", `{"access_key_id":"` + testStorageAccess + `","secret_access_key":""}`, `"secret_access_key"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newProtoRig(t)
			directDest(r, "st-bad")
			sealStorageSecret(t, r, "st-bad", tc.payload)
			r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

			tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
			queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
			require.NoError(t, err)
			require.Empty(t, queued)
			refusedDirectJob(t, r, "job-1", "st-bad", tc.want)
			require.Empty(t, r.audit.entries)
		})
	}
}

func TestPollDirectUploadUndecryptable(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "st-rot")
	// Ciphertext the box cannot open (wrong envelope prefix).
	r.store.secrets["st-rot"] = &domain.SecretProfile{
		ID: "st-rot", Name: "st-rot", Kind: domain.SecretKindStorage,
		Ciphertext: []byte("v9:opaque"), KeyRef: "v9",
	}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedDirectJob(t, r, "job-1", "st-rot", "undecryptable")
	require.Empty(t, r.audit.entries)
}

func TestPollDirectUploadAuditFailure(t *testing.T) {
	r := newProtoRig(t)
	directDest(r, "st-1")
	sealStorageSecret(t, r, "st-1", `{"access_key_id":"`+testStorageAccess+`","secret_access_key":"`+testStorageSecret+`"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// A failing audit trail withholds the credential (fail closed).
	r.audit.fail = assert.AnError
	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedDirectJob(t, r, "job-1", "audit")
}

func TestPollDirectUploadRefusalSkipsOnlyBadItem(t *testing.T) {
	r := newProtoRig(t)
	// dest-1 stays presigned (legacy mode); dest-direct is broken-direct.
	r.store.dests["dest-direct"] = &domain.StorageDestination{
		ID: "dest-direct", Name: "rustfs", Bucket: "b", Prefix: "vault",
		CredentialRef: "st-missing", UploadMode: domain.UploadModeDirect,
	}
	r.seedJob(t, "job-good", "ag-1", domain.JobQueued)
	r.seedJob(t, "job-bad", "ag-1", domain.JobQueued)
	bad, err := r.jobs.GetJob(context.Background(), "job-bad")
	require.NoError(t, err)
	bad.StorageID = "dest-direct"
	require.NoError(t, r.jobs.UpdateJob(context.Background(), bad))

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1, "one refused item must not starve the queue")
	assert.Equal(t, "job-good", queued[0].ID)
	assert.Empty(t, queued[0].UploadMode, "the healthy presigned job carries no direct fields")
	assert.Nil(t, queued[0].DestinationCredential)
	refusedDirectJob(t, r, "job-bad", "st-missing")
}

func TestPollDirectUploadDefaultDestination(t *testing.T) {
	r := newProtoRig(t)
	d := directDest(r, "st-1")
	d.Default = true
	sealStorageSecret(t, r, "st-1", `{"access_key_id":"`+testStorageAccess+`","secret_access_key":"`+testStorageSecret+`"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)
	j, err := r.jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	j.StorageID = "" // no pinned destination: the default resolves
	require.NoError(t, r.jobs.UpdateJob(context.Background(), j))

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.Equal(t, "direct", queued[0].UploadMode)
	require.NotNil(t, queued[0].DestinationCredential)
	assert.Equal(t, testStorageAccess, queued[0].DestinationCredential.AccessKeyID)
}

func TestPollUnresolvableDestinationKeepsPresignedBehavior(t *testing.T) {
	r := newProtoRig(t)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)
	j, err := r.jobs.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	j.StorageID = "dest-nope" // unknown destination, no default configured
	require.NoError(t, r.jobs.UpdateJob(context.Background(), j))

	// The job still dispatches presigned-shaped (no direct fields); the
	// grant step reports the bad destination later, exactly as before
	// direct mode existed.
	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	assert.Empty(t, queued[0].UploadMode)
	assert.Nil(t, queued[0].DestinationCredential)
}

// ---------- Grant refusal for direct destinations (old agents) ----------

func TestDirectUploadGrantsRefused(t *testing.T) {
	ctx := context.Background()
	r := newProtoRig(t)
	directDest(r, "st-1")
	r.seedJob(t, "job-1", "ag-1", domain.JobRunning)

	_, err := r.plane.IssueUploadGrants(ctx, "ag-1", "job-1",
		[]agentshttp.UploadObjectRequest{{Key: "chunk-0"}})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.Contains(t, err.Error(), "direct", "refusal must name the mode")
	require.Contains(t, err.Error(), "upgrade the agent", "refusal must tell old agents what to do")

	_, _, _, err = r.plane.InitMultipartUpload(ctx, "ag-1", "job-1", "big.zst", 20_000_000, 16_777_216)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.Contains(t, err.Error(), "direct")

	// Flipping back to presigned re-enables grants: the presigned path is
	// untouched by the guard.
	r.store.dests["dest-1"].UploadMode = domain.UploadModePresigned
	grants, err := r.plane.IssueUploadGrants(ctx, "ag-1", "job-1",
		[]agentshttp.UploadObjectRequest{{Key: "chunk-0"}})
	require.NoError(t, err)
	require.Len(t, grants, 1)
}
