package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vaultix/vaultix/internal/domain"
)

func TestUploadModeDefaultsPresigned(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	created, err := svc.CreateDestination(ctx, CreateDestinationInput{Name: "d", Bucket: "ok-bucket"})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModePresigned, created.UploadMode)

	got, err := svc.GetDestination(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModePresigned, got.UploadMode, "the default must persist, not just echo")
}

func TestUploadModeExplicitValues(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	presigned, err := svc.CreateDestination(ctx, CreateDestinationInput{
		Name: "p", Bucket: "p-bucket", UploadMode: domain.UploadModePresigned,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModePresigned, presigned.UploadMode)

	direct, err := svc.CreateDestination(ctx, CreateDestinationInput{
		Name: "d", Bucket: "d-bucket", UploadMode: domain.UploadModeDirect,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, direct.UploadMode)

	// " direct " trims like every other destination input; case is strict.
	padded, err := svc.CreateDestination(ctx, CreateDestinationInput{
		Name: "w", Bucket: "w-bucket", UploadMode: " direct ",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, padded.UploadMode)
}

func TestUploadModeBadValuesRejected(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()

	for _, bad := range []string{"s3", "Direct", "PRESIGNED", "query", "none", "direct,presigned", "presignex"} {
		_, err := svc.CreateDestination(ctx, CreateDestinationInput{
			Name: "n-" + bad, Bucket: "ok-bucket", UploadMode: domain.UploadMode(bad),
		})
		require.ErrorIs(t, err, domain.ErrValidation, "mode %q", bad)
		require.Contains(t, err.Error(), "presigned", "mode %q: error must name the legal values", bad)
		require.Contains(t, err.Error(), "direct", "mode %q: error must name the legal values", bad)
	}
}

func TestUploadModeUpdate(t *testing.T) {
	svc, _, _, _ := testService()
	ctx := context.Background()
	d := mustDestination(t, svc, "a")
	require.Equal(t, domain.UploadModePresigned, d.UploadMode)

	updated, err := svc.UpdateDestination(ctx, UpdateDestinationInput{
		ID: d.ID, Name: "a", Bucket: "test-bucket", UploadMode: domain.UploadModeDirect,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, updated.UploadMode)

	got, err := svc.GetDestination(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, got.UploadMode)

	// A bad update is rejected and the stored row is untouched.
	_, err = svc.UpdateDestination(ctx, UpdateDestinationInput{
		ID: d.ID, Name: "a", Bucket: "test-bucket", UploadMode: "s3",
	})
	require.ErrorIs(t, err, domain.ErrValidation)
	got, err = svc.GetDestination(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, got.UploadMode)
}

func TestUploadModeDomainValidation(t *testing.T) {
	// Legacy zero value validates (reads normalize it); garbage does not.
	require.NoError(t, domain.StorageDestination{Name: "n", Bucket: "b-bucket"}.Validate())
	require.ErrorIs(t,
		domain.StorageDestination{Name: "n", Bucket: "b-bucket", UploadMode: "s3"}.Validate(),
		domain.ErrValidation)
	assert.Equal(t, domain.UploadModePresigned,
		domain.StorageDestination{}.NormalizedUploadMode())
	assert.Equal(t, domain.UploadModeDirect,
		domain.StorageDestination{UploadMode: domain.UploadModeDirect}.NormalizedUploadMode())
}
