package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

func testRegistry(t *testing.T, fake *fakeS3, prefix string) *Registry {
	t.Helper()
	access, signing := testCredentials()
	return NewRegistry(func(_ context.Context, destinationID string) (Config, error) {
		return Config{
			Endpoint:             fake.server.URL,
			Region:               "us-east-1",
			Bucket:               "test-bucket",
			Prefix:               prefix,
			AccessKeyID:          access,
			SecretAccessKey:      signing,
			RetryInitialInterval: time.Millisecond,
		}, nil
	}, nil)
}

func TestRegistryPorts(t *testing.T) {
	fake := newFakeS3(t)
	r := testRegistry(t, fake, "tenant1")
	ctx := context.Background()
	key := "tenant1/jobs/jobA/a.bin"
	body := []byte("registry-bytes")
	fake.putObject("test-bucket", key, body)

	putURL, err := r.PresignUpload(ctx, "dest-1", key, 5*time.Minute)
	require.NoError(t, err)
	assert.Contains(t, putURL, "X-Amz-Signature=")

	getURL, err := r.PresignDownload(ctx, "dest-1", key, 5*time.Minute)
	require.NoError(t, err)
	assert.Contains(t, getURL, "X-Amz-Signature=")

	// Port-level defense in depth: unshaped keys are refused even though the
	// authorized job is unknown here.
	_, err = r.PresignUpload(ctx, "dest-1", "raw/x.bin", time.Minute)
	require.Error(t, err)
	_, err = r.PresignDownload(ctx, "dest-1", "../escape.bin", time.Minute)
	require.Error(t, err)

	meta, err := r.Head(ctx, "dest-1", key)
	require.NoError(t, err)
	assert.True(t, meta.Exists)

	require.NoError(t, r.Ping(ctx, "dest-1"))
	require.NoError(t, r.Delete(ctx, "dest-1", key))
	meta, err = r.Head(ctx, "dest-1", key)
	require.NoError(t, err)
	assert.False(t, meta.Exists)

	_, err = r.PresignUpload(ctx, "", key, time.Minute)
	require.Error(t, err)
}

func TestRegistryMultipartAndVerify(t *testing.T) {
	fake := newFakeS3(t)
	r := testRegistry(t, fake, "")
	ctx := context.Background()
	key := "jobs/jobA/large.bin"

	uploadID, err := r.InitiateMultipartUpload(ctx, "dest-1", key)
	require.NoError(t, err)
	_, err = r.InitiateMultipartUpload(ctx, "dest-1", "raw/x.bin")
	require.Error(t, err)

	pg, err := r.PresignUploadPart(ctx, "dest-1", key, uploadID, 1, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, pg.PartNumber)
	assert.Contains(t, pg.URL, "uploadId="+uploadID)
	_, err = r.PresignUploadPart(ctx, "dest-1", key, uploadID, 0, time.Minute)
	require.Error(t, err)

	// Completion carries per-part checksums end to end and stamps the
	// whole-object sha256, which HEAD then reports like a single-PUT.
	partBody := []byte("registry-part-bytes")
	fake.storePart(uploadID, 1, partBody)
	partSum := sha256.Sum256(partBody)
	require.NoError(t, r.CompleteMultipartUpload(ctx, "dest-1", key, uploadID,
		[]ustorage.MultipartPart{{
			PartNumber: 1, ETag: "e1",
			SHA256:    hex.EncodeToString(partSum[:]),
			SizeBytes: int64(len(partBody)),
		}}))
	u, ok := fake.getUpload(uploadID)
	require.True(t, ok)
	assert.True(t, u.completed)
	meta, err := r.Head(ctx, "dest-1", key)
	require.NoError(t, err)
	require.True(t, meta.Exists)
	assert.Equal(t, "sha256:"+hex.EncodeToString(partSum[:]), meta.Checksum)

	// Parts without checksums are refused, never completed silently.
	uploadID0, err := r.InitiateMultipartUpload(ctx, "dest-1", key)
	require.NoError(t, err)
	require.Error(t, r.CompleteMultipartUpload(ctx, "dest-1", key, uploadID0,
		[]ustorage.MultipartPart{{PartNumber: 1, ETag: "e1"}}))

	uploadID2, err := r.InitiateMultipartUpload(ctx, "dest-1", key)
	require.NoError(t, err)
	require.NoError(t, r.AbortMultipartUpload(ctx, "dest-1", key, uploadID2))
	u2, ok := fake.getUpload(uploadID2)
	require.True(t, ok)
	assert.True(t, u2.aborted)

	body := []byte("verified")
	vkey := "jobs/jobA/v.bin"
	fake.putObject("test-bucket", vkey, body)
	sum := sha256.Sum256(body)
	ok, details, err := r.VerifyObjects(ctx, "dest-1", []ustorage.ExpectedObject{
		{ObjectKey: vkey, Checksum: "sha256:" + hex.EncodeToString(sum[:])},
	})
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, details, 1)
	assert.True(t, details[0].OK)

	ok, _, err = r.VerifyObjects(ctx, "dest-1", []ustorage.ExpectedObject{
		{ObjectKey: vkey, Checksum: "sha256:00"},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRegistryResolveError(t *testing.T) {
	r := NewRegistry(func(_ context.Context, _ string) (Config, error) {
		return Config{}, assert.AnError
	}, nil)
	_, err := r.PresignUpload(context.Background(), "dest-1", "jobs/a/x.bin", time.Minute)
	require.Error(t, err)

	r = NewRegistry(nil, nil)
	require.Error(t, r.Ping(context.Background(), "dest-1"))
}
