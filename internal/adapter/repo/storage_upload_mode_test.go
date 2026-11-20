package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

func TestStorageAuthUploadModeRoundTrip(t *testing.T) {
	raw, err := encodeJSON(storageAuth{CredentialRef: "sec-1", UsePathStyle: true, UploadMode: "direct"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"upload_mode":"direct"`)

	var back storageAuth
	require.NoError(t, decodeJSON(raw, &back))
	assert.Equal(t, "direct", back.UploadMode)
	assert.Equal(t, "sec-1", back.CredentialRef)
	assert.True(t, back.UsePathStyle)
}

func TestMapDestinationUploadMode(t *testing.T) {
	// Explicit direct round-trips.
	raw, err := encodeJSON(storageAuth{UploadMode: "direct"})
	require.NoError(t, err)
	d, err := mapDestination(gen.StorageDestination{Name: "d", Auth: raw})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModeDirect, d.UploadMode)

	// Explicit presigned round-trips.
	raw, err = encodeJSON(storageAuth{UploadMode: "presigned"})
	require.NoError(t, err)
	d, err = mapDestination(gen.StorageDestination{Name: "d", Auth: raw})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadModePresigned, d.UploadMode)

	// Legacy auth objects (no upload_mode key) read back as presigned, so
	// the direct-upload opt-in needs no backfill migration.
	for _, legacy := range [][]byte{[]byte(`{}`), []byte(`{"credential_ref":"s","use_path_style":true}`), nil} {
		d, err := mapDestination(gen.StorageDestination{Name: "d", Auth: legacy})
		require.NoError(t, err)
		assert.Equal(t, domain.UploadModePresigned, d.UploadMode, "auth %q", string(legacy))
	}

	// Unknown stored values pass through (reads never fail on them);
	// issuance only fires on the exact "direct" value, so they behave as
	// non-direct (fail closed) until corrected.
	raw, err = encodeJSON(storageAuth{UploadMode: "s3"})
	require.NoError(t, err)
	d, err = mapDestination(gen.StorageDestination{Name: "d", Auth: raw})
	require.NoError(t, err)
	assert.Equal(t, domain.UploadMode("s3"), d.UploadMode)
	assert.NotEqual(t, domain.UploadModeDirect, d.NormalizedUploadMode())
}
