package http

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type stubDB struct{ err error }

func (s stubDB) Ping(context.Context) error { return s.err }

type stubQueue struct{ err error }

func (s stubQueue) Health(context.Context) error { return s.err }

func testRouter() *Mux {
	return NewRouter(RouterDeps{
		Health:         HealthDeps{DB: stubDB{}, Version: "test"},
		RequestTimeout: 5 * time.Second,
		Logger:         discardLogger(),
	})
}

func TestHealthEndpointStates(t *testing.T) {
	cases := []struct {
		name   string
		deps   HealthDeps
		code   int
		status string
	}{
		{
			name:   "all ok",
			deps:   HealthDeps{DB: stubDB{}, Queue: stubQueue{}, S3Configured: true, Version: "v1"},
			code:   nethttp.StatusOK,
			status: "ok",
		},
		{
			name:   "queue unconfigured is still ok",
			deps:   HealthDeps{DB: stubDB{}, Queue: nil, S3Configured: false},
			code:   nethttp.StatusOK,
			status: "ok",
		},
		{
			name:   "queue failing degrades",
			deps:   HealthDeps{DB: stubDB{}, Queue: stubQueue{err: fmt.Errorf("stopped")}},
			code:   nethttp.StatusOK,
			status: "degraded",
		},
		{
			name:   "db down",
			deps:   HealthDeps{DB: stubDB{err: fmt.Errorf("refused")}, Queue: stubQueue{}},
			code:   nethttp.StatusServiceUnavailable,
			status: "down",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRouter(RouterDeps{Health: c.deps, Logger: discardLogger()})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))
			assert.Equal(t, c.code, rec.Code)
			assert.Contains(t, rec.Body.String(), `"status":"`+c.status+`"`)
			assert.Contains(t, rec.Body.String(), `"db"`)
			assert.Contains(t, rec.Body.String(), `"queue"`)
			assert.Contains(t, rec.Body.String(), `"s3"`)
			assert.NotEmpty(t, rec.Header().Get("X-Request-ID"), "middleware chain runs for /healthz")
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	r := testRouter()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/metrics", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
	body := rec.Body.String()
	assert.Contains(t, body, "manager_build_info")
	assert.Contains(t, body, "manager_uptime_seconds")
	// Observation happens after the handler writes, so the second scrape
	// shows the first request.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/metrics", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `http_requests_total{code="200",method="GET"} 1`)
}

func TestAPINotFoundIsJSON(t *testing.T) {
	r := testRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(nethttp.MethodGet, "/api/nope", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, nethttp.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Body.String(), `"code":404`)
	assert.Contains(t, rec.Body.String(), rec.Header().Get("X-Request-ID"))

	// Other methods behave the same.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/api/nope", nil))
	assert.Equal(t, nethttp.StatusNotFound, rec.Code)
}

func TestChiPatternTranslation(t *testing.T) {
	r := testRouter()
	r.Get("/api/things/:id", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		WriteJSON(w, nethttp.StatusOK, map[string]string{"id": req.PathValue("id")})
	})
	r.Get("/api/other/{key}", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		WriteJSON(w, nethttp.StatusOK, map[string]string{"key": req.PathValue("key")})
	})
	r.Post("/api/things", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		w.WriteHeader(nethttp.StatusCreated)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/things/abc", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"id":"abc"`)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/other/k1", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"key":"k1"`)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/api/things", nil))
	assert.Equal(t, nethttp.StatusCreated, rec.Code)

	// Wrong method on a registered path: JSON 405, not the SPA.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodDelete, "/api/things/abc", nil))
	assert.Equal(t, nethttp.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":405`)
}

func TestTranslatePattern(t *testing.T) {
	assert.Equal(t, "/api/things/{id}", translatePattern("/api/things/:id"))
	assert.Equal(t, "/api/other/{key}", translatePattern("/api/other/{key}"))
	assert.Equal(t, "/healthz", translatePattern("/healthz"))
	assert.Equal(t, "/files/{_rest...}", translatePattern("/files/*"))
	assert.Equal(t, "/a/{x}/b/{y}", translatePattern("/a/:x/b/:y"))
}

func TestPanicRecoveryThroughRouter(t *testing.T) {
	r := testRouter()
	r.Get("/api/boom", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/boom", nil))
	})
	assert.Equal(t, nethttp.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "internal server error")
}

func TestRouterTimeout(t *testing.T) {
	r := NewRouter(RouterDeps{
		Health:         HealthDeps{DB: stubDB{}},
		RequestTimeout: 20 * time.Millisecond,
		Logger:         discardLogger(),
	})
	r.Get("/api/slow", func(w nethttp.ResponseWriter, req *nethttp.Request) {
		select {
		case <-req.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/slow", nil))
	assert.Equal(t, nethttp.StatusServiceUnavailable, rec.Code)
}

func TestWriteErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(nethttp.MethodGet, "/api/x", nil)
	WriteError(rec, req, nethttp.StatusForbidden, "denied")
	assert.Equal(t, nethttp.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":403`)
	assert.Contains(t, rec.Body.String(), `"message":"denied"`)
}
