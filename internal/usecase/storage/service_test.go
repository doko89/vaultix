package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vaultix/vaultix/internal/domain"
)

func TestDestinationsCRUD(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	created, err := svc.CreateDestination(ctx, CreateDestinationInput{
		Name: "primary", Endpoint: "https://minio:9000", Region: "us-east-1",
		Bucket: "backups", Prefix: "/tenant1/", UsePathStyle: true, Default: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "id-1", created.ID)
	assert.Equal(t, "tenant1", created.Prefix)
	assert.True(t, created.Default)

	got, err := svc.GetDestination(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Name, got.Name)

	updated, err := svc.UpdateDestination(ctx, UpdateDestinationInput{
		ID: created.ID, Name: "primary", Region: "eu-west-1",
		Bucket: "backups", Prefix: "tenant1", Default: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", updated.Region)
	assert.Equal(t, created.CreatedAt, updated.CreatedAt)

	all, err := svc.ListDestinations(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	require.NoError(t, svc.DeleteDestination(ctx, created.ID))
	_, err = svc.GetDestination(ctx, created.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDestinationsValidation(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	_, err := svc.CreateDestination(ctx, CreateDestinationInput{Bucket: "b"})
	assert.ErrorIs(t, err, domain.ErrValidation) // missing name

	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "x"})
	assert.ErrorIs(t, err, domain.ErrValidation) // missing bucket

	for _, bucket := range []string{"ab", "UPPER", "has space", "-lead", "trail-", "do..ts", strings.Repeat("a", 64)} {
		_, err := svc.CreateDestination(ctx, CreateDestinationInput{Name: "n-" + bucket, Bucket: bucket})
		assert.ErrorIs(t, err, domain.ErrValidation, "bucket %q", bucket)
	}

	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "e1", Bucket: "ok-bucket", Endpoint: "gopher://x"})
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "e2", Bucket: "ok-bucket", Endpoint: "https://user:pw@host"})
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "e3", Bucket: "ok-bucket", CredentialRef: "nope"})
	assert.ErrorIs(t, err, domain.ErrValidation) // dangling credential ref

	mustDestination(t, svc, "dup")
	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "DUP", Bucket: "other-bucket"})
	assert.ErrorIs(t, err, domain.ErrConflict)

	_, err = svc.GetDestination(ctx, "")
	assert.ErrorIs(t, err, domain.ErrValidation)
	require.Error(t, svc.DeleteDestination(ctx, "missing"))
}

func TestDestinationsDefaultReassignment(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()
	a := mustDestination(t, svc, "a")
	b, err := svc.CreateDestination(ctx, CreateDestinationInput{Name: "b", Bucket: "b-bucket", Default: true})
	require.NoError(t, err)

	gotA, err := svc.GetDestination(ctx, a.ID)
	require.NoError(t, err)
	assert.False(t, gotA.Default)
	assert.True(t, b.Default)

	// Promoting A demotes B.
	_, err = svc.UpdateDestination(ctx, UpdateDestinationInput{ID: a.ID, Name: "a", Bucket: "test-bucket", Default: true})
	require.NoError(t, err)
	gotB, err := svc.GetDestination(ctx, b.ID)
	require.NoError(t, err)
	assert.False(t, gotB.Default)
}

func TestResolveForPolicy(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	_, err := svc.ResolveForPolicy(ctx, "")
	assert.ErrorIs(t, err, domain.ErrNotFound) // nothing configured

	a := mustDestination(t, svc, "a")
	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "d", Bucket: "d-bucket", Default: true})
	require.NoError(t, err)

	explicit, err := svc.ResolveForPolicy(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, explicit.ID)

	def, err := svc.ResolveForPolicy(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "d", def.Name)

	_, err = svc.ResolveForPolicy(ctx, "missing")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestTestDestination(t *testing.T) {
	svc, _, store, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")

	require.NoError(t, svc.TestDestination(ctx, d.ID))
	require.ErrorIs(t, func() error { return svc.TestDestination(ctx, "") }(), domain.ErrValidation)
	require.ErrorIs(t, func() error { return svc.TestDestination(ctx, "missing") }(), domain.ErrNotFound)

	store.pingErr = errors.New("connection refused")
	require.ErrorContains(t, svc.TestDestination(ctx, d.ID), "connection refused")
}

func TestSecretsWriteOnlyPlaintext(t *testing.T) {
	svc, repo, _, box := testService()
	ctx := context.Background()
	marker := "MARKER-VALUE-9f8e7d6c5b4a"

	created, err := svc.CreateSecret(ctx, CreateSecretInput{
		Name: "db-main", Kind: domain.SecretKindDatabase,
		Plaintext: []byte(marker), CreatedBy: "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, box.seals)
	assert.Equal(t, "test-key-1", created.KeyRef)
	assertNotLeaked(t, created, marker)

	// The repository holds sealed bytes that only the box can open.
	stored, err := repo.GetSecret(ctx, created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Ciphertext)
	opened, err := box.Open(ctx, stored.Ciphertext, stored.KeyRef)
	require.NoError(t, err)
	assert.Equal(t, marker, string(opened))

	// Every read path returns metadata without the value: get, list, update.
	got, err := svc.GetSecret(ctx, created.ID)
	require.NoError(t, err)
	assertNotLeaked(t, got, marker)

	listed, err := svc.ListSecrets(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assertNotLeaked(t, listed, marker)

	rotated, err := svc.UpdateSecret(ctx, UpdateSecretInput{
		ID: created.ID, Name: "db-main", Kind: domain.SecretKindDatabase,
		Plaintext: []byte(marker + "-rotated"),
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, rotated.ID)
	assert.Equal(t, created.CreatedBy, rotated.CreatedBy)
	assert.Equal(t, created.CreatedAt, rotated.CreatedAt)
	assertNotLeaked(t, rotated, marker)
	assertNotLeaked(t, rotated, marker+"-rotated")

	// Emulated update (no UpdateSecret on repo) preserved the row identity.
	after, err := repo.GetSecret(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.CreatedAt, after.CreatedAt)
}

func assertNotLeaked(t *testing.T, v any, marker string) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), marker)
	assert.NotContains(t, string(raw), "Ciphertext")
	assert.NotContains(t, string(raw), "Plaintext")
}

func TestSecretsValidationAndRefs(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	_, err := svc.CreateSecret(ctx, CreateSecretInput{Kind: domain.SecretKindGeneric, Plaintext: []byte("v")})
	assert.ErrorIs(t, err, domain.ErrValidation) // missing name
	_, err = svc.CreateSecret(ctx, CreateSecretInput{Name: "n", Kind: "bogus", Plaintext: []byte("v")})
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.CreateSecret(ctx, CreateSecretInput{Name: "n", Kind: domain.SecretKindGeneric})
	assert.ErrorIs(t, err, domain.ErrValidation) // missing value
	_, err = svc.CreateSecret(ctx, CreateSecretInput{Name: "n", Kind: domain.SecretKindGeneric, Plaintext: make([]byte, MaxSecretBytes+1)})
	assert.ErrorIs(t, err, domain.ErrValidation)

	m, err := svc.CreateSecret(ctx, CreateSecretInput{Name: "s1", Kind: domain.SecretKindStorage, Plaintext: []byte("v")})
	require.NoError(t, err)
	_, err = svc.CreateSecret(ctx, CreateSecretInput{Name: "S1", Kind: domain.SecretKindStorage, Plaintext: []byte("v")})
	assert.ErrorIs(t, err, domain.ErrConflict)

	// A destination may reference the profile; deletion is then refused.
	_, err = svc.CreateDestination(ctx, CreateDestinationInput{Name: "d", Bucket: "d-bucket", CredentialRef: m.ID})
	require.NoError(t, err)
	assert.ErrorIs(t, svc.DeleteSecret(ctx, m.ID), domain.ErrConflict)

	_, err = svc.GetSecret(ctx, "")
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.UpdateSecret(ctx, UpdateSecretInput{ID: m.ID, Name: "s1", Kind: domain.SecretKindStorage})
	assert.ErrorIs(t, err, domain.ErrValidation) // rotation needs a value
	require.Error(t, svc.DeleteSecret(ctx, "missing"))
}

func TestSecretsUpdateWithUpdater(t *testing.T) {
	repo := &fakeRepoWithUpdate{fakeRepo: newFakeRepo()}
	svc := NewService(repo, newFakeStore(), newFakeBox())
	ctx := context.Background()
	m, err := svc.CreateSecret(ctx, CreateSecretInput{Name: "s", Kind: domain.SecretKindGeneric, Plaintext: []byte("v1")})
	require.NoError(t, err)
	updated, err := svc.UpdateSecret(ctx, UpdateSecretInput{ID: m.ID, Name: "s2", Kind: domain.SecretKindGeneric, Plaintext: []byte("v2")})
	require.NoError(t, err)
	assert.Equal(t, m.ID, updated.ID)
	assert.Equal(t, "s2", updated.Name)
}

func TestServiceWithoutOptionals(t *testing.T) {
	svc := NewService(newFakeRepo(), nil, nil)
	ctx := context.Background()
	_, err := svc.CreateSecret(ctx, CreateSecretInput{Name: "s", Kind: domain.SecretKindGeneric, Plaintext: []byte("v")})
	require.ErrorContains(t, err, "not configured")
	_, err = svc.IssueUploadGrant(ctx, "d", "jobA", "a.bin", 0)
	require.ErrorContains(t, err, "not configured")
}

func TestIssueUploadGrantScopeIsolation(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")

	grant, err := svc.IssueUploadGrant(ctx, d.ID, "jobA", "chunk.bin", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "PUT", grant.Method)
	assert.Equal(t, d.ID, grant.DestinationID)
	assert.Equal(t, "jobA", grant.JobID)
	assert.Equal(t, "tenant1/jobs/jobA/chunk.bin", grant.ObjectKey)
	assert.Contains(t, grant.URL, "signed.test/put")
	assert.True(t, grant.ExpiresAt.Equal(time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)))

	// The grant for job A cannot address job B paths.
	require.Error(t, ValidateJobKey("tenant1", "jobB", grant.ObjectKey))
	require.NoError(t, ValidateJobKey("tenant1", "jobA", grant.ObjectKey))

	// Zero TTL selects the default; over-maximum is rejected.
	def, err := svc.IssueUploadGrant(ctx, d.ID, "jobA", "b.bin", 0)
	require.NoError(t, err)
	assert.True(t, def.ExpiresAt.Equal(time.Date(2026, 11, 19, 12, 15, 0, 0, time.UTC)))
	_, err = svc.IssueUploadGrant(ctx, d.ID, "jobA", "b.bin", 2*time.Hour)
	assert.ErrorIs(t, err, domain.ErrValidation)

	// Traversal and unknown destinations are refused.
	_, err = svc.IssueUploadGrant(ctx, d.ID, "jobA", "../escape.bin", time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.IssueUploadGrant(ctx, "missing", "jobA", "a.bin", time.Minute)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = svc.IssueUploadGrant(ctx, "", "jobA", "a.bin", time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestIssueDownloadGrant(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")

	// Restore reads objects written by earlier backup jobs: an explicit
	// job-scoped key from another job is allowed.
	grant, err := svc.IssueDownloadGrant(ctx, d.ID, "restore-1", "tenant1/jobs/backup-9/dump.zst", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "GET", grant.Method)
	assert.Equal(t, "tenant1/jobs/backup-9/dump.zst", grant.ObjectKey)

	_, err = svc.IssueDownloadGrant(ctx, d.ID, "restore-1", "tenant1/raw/x.bin", time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.IssueDownloadGrant(ctx, d.ID, "", "tenant1/jobs/a/x.bin", time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestMultipartOrchestration(t *testing.T) {
	svc, _, store, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")

	key, uploadID, err := svc.InitiateJobUpload(ctx, d.ID, "jobA", "large.bin")
	require.NoError(t, err)
	assert.Equal(t, "tenant1/jobs/jobA/large.bin", key)
	require.NotEmpty(t, uploadID)

	parts, err := svc.PresignJobParts(ctx, d.ID, "jobA", key, uploadID, []int{1, 2}, time.Minute)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, 1, parts[0].PartNumber)
	assert.Equal(t, []int{1, 2}, store.parts[uploadID])

	// Part grants honor the job binding: job B cannot presign job A's key.
	_, err = svc.PresignJobParts(ctx, d.ID, "jobB", key, uploadID, []int{1}, time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.PresignJobParts(ctx, d.ID, "jobA", key, uploadID, []int{0}, time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)
	_, err = svc.PresignJobParts(ctx, d.ID, "jobA", key, uploadID, nil, time.Minute)
	assert.ErrorIs(t, err, domain.ErrValidation)

	require.NoError(t, svc.CompleteJobUpload(ctx, d.ID, "jobA", key, uploadID,
		[]MultipartPart{{PartNumber: 1, ETag: "e1"}, {PartNumber: 2, ETag: "e2"}}))
	assert.Len(t, store.finished[uploadID], 2)
	require.ErrorIs(t,
		svc.CompleteJobUpload(ctx, d.ID, "jobB", key, uploadID, []MultipartPart{{PartNumber: 1, ETag: "e1"}}),
		domain.ErrValidation)

	key2, uploadID2, err := svc.InitiateJobUpload(ctx, d.ID, "jobA", "abort.bin")
	require.NoError(t, err)
	require.NoError(t, svc.AbortJobUpload(ctx, d.ID, "jobA", key2, uploadID2))
	assert.True(t, store.aborted[uploadID2])
	require.ErrorIs(t, svc.AbortJobUpload(ctx, d.ID, "jobB", key2, uploadID2), domain.ErrValidation)
}

func TestVerifyJobObjects(t *testing.T) {
	svc, _, store, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")
	key := "tenant1/jobs/jobA/a.bin"
	store.objects[d.ID+"/"+key] = storedObject{size: 7, checksum: "sha256:abc"}

	ok, details, err := svc.VerifyJobObjects(ctx, d.ID, []ExpectedObject{
		{ObjectKey: key, Checksum: "sha256:abc", Size: 7, CheckSize: true},
	})
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, details, 1)
	assert.True(t, details[0].OK)

	ok, details, err = svc.VerifyJobObjects(ctx, d.ID, []ExpectedObject{
		{ObjectKey: key, Checksum: "sha256:other"},
	})
	require.NoError(t, err)
	assert.False(t, ok)
	require.Len(t, details, 1)
	assert.False(t, details[0].OK)

	_, _, err = svc.VerifyJobObjects(ctx, d.ID, nil)
	assert.ErrorIs(t, err, domain.ErrValidation) // fail-closed on empty set
}
