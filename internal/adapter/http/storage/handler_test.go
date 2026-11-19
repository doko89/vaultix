package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vaultix/vaultix/internal/domain"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// contractStub is a Service stub for failure-path tests; happy paths run
// against the real use case (see testMux).
type contractStub struct {
	Service
	listErr error
}

func (s *contractStub) ListDestinations(context.Context) ([]domain.StorageDestination, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return nil, nil
}

// testMux wires the real use case with in-memory fakes behind the routes.
func testMux() (*http.ServeMux, *memRepo, *memStore, *memBox) {
	repo := &memRepo{
		destinations: map[string]*domain.StorageDestination{},
		secrets:      map[string]*domain.SecretProfile{},
	}
	store := &memStore{objects: map[string]memObject{}}
	box := &memBox{}
	svc := ustorage.NewService(repo, store, box)
	return NewMux(Deps{Service: svc, Actor: "tester"}), repo, store, box
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path string, body any) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			reader = bytes.NewBufferString(s)
		} else {
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			reader = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(raw)
}

func TestDestinationEndpoints(t *testing.T) {
	mux, _, _, _ := testMux()

	status, body := doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{
		Name: "primary", Endpoint: "https://minio:9000", Region: "us-east-1",
		Bucket: "backups", Prefix: "tenant1", UsePathStyle: true, Default: true,
	})
	require.Equal(t, http.StatusCreated, status, body)
	var created DestinationResponse
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	require.NotEmpty(t, created.ID)
	assert.Equal(t, "primary", created.Name)

	status, body = doRequest(t, mux, http.MethodGet, "/api/storage/"+created.ID, nil)
	require.Equal(t, http.StatusOK, status, body)

	status, body = doRequest(t, mux, http.MethodGet, "/api/storage", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, created.ID)

	status, body = doRequest(t, mux, http.MethodPut, "/api/storage/"+created.ID, DestinationRequest{
		Name: "primary", Region: "eu-west-1", Bucket: "backups", Prefix: "tenant1",
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "eu-west-1")

	status, _ = doRequest(t, mux, http.MethodDelete, "/api/storage/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)

	status, _ = doRequest(t, mux, http.MethodGet, "/api/storage/"+created.ID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestDestinationValidation(t *testing.T) {
	mux, _, _, _ := testMux()

	status, body := doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{Bucket: "b"})
	require.Equal(t, http.StatusBadRequest, status, body)
	assert.Contains(t, body, "validation failed")

	status, _ = doRequest(t, mux, http.MethodPost, "/api/storage", `{"name":"x","bucket":"ok-bucket","bogus":1}`)
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = doRequest(t, mux, http.MethodPost, "/api/storage", `not-json`)
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{Name: "x", Bucket: "ok-bucket", Endpoint: "notaurl"})
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = doRequest(t, mux, http.MethodPut, "/api/storage/missing", DestinationRequest{Name: "x", Bucket: "ok-bucket"})
	require.Equal(t, http.StatusNotFound, status)
}

func TestTestConnectionEndpoint(t *testing.T) {
	mux, _, store, _ := testMux()
	status, body := doRequest(t, mux, http.MethodPost, "/api/storage", DestinationRequest{Name: "d", Bucket: "ok-bucket"})
	require.Equal(t, http.StatusCreated, status, body)
	var created DestinationResponse
	require.NoError(t, json.Unmarshal([]byte(body), &created))

	status, body = doRequest(t, mux, http.MethodPost, "/api/storage/"+created.ID+"/test", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"ok":true`)

	store.pingErr = errBoom
	status, body = doRequest(t, mux, http.MethodPost, "/api/storage/"+created.ID+"/test", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"ok":false`)

	status, _ = doRequest(t, mux, http.MethodPost, "/api/storage/missing/test", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestSecretEndpointsMetadataOnly(t *testing.T) {
	mux, _, _, _ := testMux()
	marker := "MARKER-HTTP-9f8e7d6c5b4a"

	status, body := doRequest(t, mux, http.MethodPost, "/api/credentials", SecretRequest{
		Name: "db-main", Kind: "database", Plaintext: marker,
	})
	require.Equal(t, http.StatusCreated, status, body)
	assert.NotContains(t, body, marker)
	assert.NotContains(t, body, "Ciphertext")
	assert.NotContains(t, body, "Plaintext")
	var created SecretMetaResponse
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	require.NotEmpty(t, created.ID)
	assert.Equal(t, "tester", created.CreatedBy)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/credentials/" + created.ID},
		{http.MethodGet, "/api/credentials"},
	} {
		status, body := doRequest(t, mux, tc.method, tc.path, nil)
		require.Equal(t, http.StatusOK, status, body)
		assert.NotContains(t, body, marker, "%s %s", tc.method, tc.path)
	}

	status, body = doRequest(t, mux, http.MethodPut, "/api/credentials/"+created.ID, SecretRequest{
		Name: "db-main", Kind: "database", Plaintext: marker + "-rot",
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, marker)

	status, _ = doRequest(t, mux, http.MethodDelete, "/api/credentials/"+created.ID, nil)
	require.Equal(t, http.StatusNoContent, status)
}

func TestSecretValidation(t *testing.T) {
	mux, _, _, _ := testMux()
	marker := "MARKER-HTTP-9f8e7d6c5b4a"

	status, body := doRequest(t, mux, http.MethodPost, "/api/credentials", SecretRequest{
		Name: "x", Kind: "bogus", Plaintext: marker,
	})
	require.Equal(t, http.StatusBadRequest, status, body)
	assert.NotContains(t, body, marker) // details name rules, never values

	status, _ = doRequest(t, mux, http.MethodPost, "/api/credentials", SecretRequest{Name: "x", Kind: "generic"})
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = doRequest(t, mux, http.MethodGet, "/api/credentials/missing", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestInternalErrorsStayGeneric(t *testing.T) {
	stub := &contractStub{listErr: errBoom}
	mux := NewMux(Deps{Service: stub})

	status, body := doRequest(t, mux, http.MethodGet, "/api/storage", nil)
	require.Equal(t, http.StatusInternalServerError, status, body)
	assert.Equal(t, `{"error":"internal error"}`+"\n", body)
	assert.NotContains(t, body, "boom-marker")
}

// recordRouter captures Mount registrations to guard the chi path.
type recordRouter struct {
	routes []string
}

func (r *recordRouter) Get(pattern string, _ http.HandlerFunc) {
	r.routes = append(r.routes, "GET "+pattern)
}
func (r *recordRouter) Post(pattern string, _ http.HandlerFunc) {
	r.routes = append(r.routes, "POST "+pattern)
}
func (r *recordRouter) Put(pattern string, _ http.HandlerFunc) {
	r.routes = append(r.routes, "PUT "+pattern)
}
func (r *recordRouter) Patch(pattern string, _ http.HandlerFunc) {
	r.routes = append(r.routes, "PATCH "+pattern)
}
func (r *recordRouter) Delete(pattern string, _ http.HandlerFunc) {
	r.routes = append(r.routes, "DELETE "+pattern)
}

func TestMountRegistersRoutes(t *testing.T) {
	var r recordRouter
	Mount(&r, Deps{Service: &contractStub{}})
	assert.ElementsMatch(t, []string{
		"GET /api/storage",
		"POST /api/storage",
		"GET /api/storage/{id}",
		"PUT /api/storage/{id}",
		"DELETE /api/storage/{id}",
		"POST /api/storage/{id}/test",
		"GET /api/credentials",
		"POST /api/credentials",
		"GET /api/credentials/{id}",
		"PUT /api/credentials/{id}",
		"DELETE /api/credentials/{id}",
	}, r.routes)
}
