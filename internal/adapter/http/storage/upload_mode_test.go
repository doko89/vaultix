package storage

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDestinationUploadModeEndpoints(t *testing.T) {
	mux, _, _, _ := testMux()

	// Omitted mode defaults to presigned.
	status, body := doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{
		Name: "legacy", Bucket: "ok-bucket",
	})
	require.Equal(t, http.StatusCreated, status, body)
	var created DestinationResponse
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	assert.Equal(t, "presigned", created.UploadMode)

	// Explicit direct round-trips.
	status, body = doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{
		Name: "rustfs", Bucket: "ok-bucket", UploadMode: "direct",
	})
	require.Equal(t, http.StatusCreated, status, body)
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	assert.Equal(t, "direct", created.UploadMode)

	// Update flips the mode.
	status, body = doRequest(t, mux, http.MethodPut, "/api/storage/"+created.ID, DestinationRequest{
		Name: "rustfs", Bucket: "ok-bucket", UploadMode: "presigned",
	})
	require.Equal(t, http.StatusOK, status, body)
	var updated DestinationResponse
	require.NoError(t, json.Unmarshal([]byte(body), &updated))
	assert.Equal(t, "presigned", updated.UploadMode)
}

func TestDestinationUploadModeValidation(t *testing.T) {
	mux, _, _, _ := testMux()

	for _, bad := range []string{"s3", "Direct", "PRESIGNED", "none"} {
		status, body := doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{
			Name: "n-" + bad, Bucket: "ok-bucket", UploadMode: bad,
		})
		require.Equal(t, http.StatusBadRequest, status, "mode %q: %s", bad, body)
		assert.Contains(t, body, "uploadmode", "mode %q must name the offending field", bad)
	}
}
