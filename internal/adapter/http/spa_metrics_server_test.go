package http

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSPAFileServeAndFallback(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<html>vaultix</html>")},
		"assets/app.js":    {Data: []byte("console.log(1)")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	h := newSPAHandler(SPAConfig{FS: fsys}, discardLogger())

	// Exact file.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/assets/app.js", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "console.log")

	// Client-side route falls back to index.html.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/dashboard/agents", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "vaultix")

	// Root serves index.html.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)

	// Non-GET is rejected, not fallen back.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodPost, "/dashboard", nil))
	assert.Equal(t, nethttp.StatusMethodNotAllowed, rec.Code)
}

func TestSPAMissingDistExplainsBuild(t *testing.T) {
	h := newSPAHandler(SPAConfig{Dir: filepath.Join(t.TempDir(), "nope")}, discardLogger())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	assert.Equal(t, nethttp.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "npm --prefix web run build")
}

func TestSPADiskDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>disk</html>"), 0o644))
	h := newSPAHandler(SPAConfig{Dir: dir}, discardLogger())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/agents", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "disk")
}

func TestRouterSPAFallbackVsAPI404(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("<html>vaultix</html>")}}
	r := NewRouter(RouterDeps{
		Health: HealthDeps{DB: stubDB{}},
		SPA:    SPAConfig{FS: fsys},
		Logger: discardLogger(),
	})

	// Unknown app path -> SPA.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/agents", nil))
	require.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "vaultix")

	// Unknown API path -> JSON 404, never the SPA.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/api/v9/nonexistent", nil))
	assert.Equal(t, nethttp.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "vaultix")
	assert.Contains(t, rec.Body.String(), `"code":404`)
}

func TestMetricsExposition(t *testing.T) {
	m := NewMetrics("v1.2.3", discardLogger())
	m.ObserveRequest("GET", 200)
	m.ObserveRequest("GET", 200)
	m.ObserveRequest("POST", 500)
	m.SetGauge("scheduler_queue_depth", 7, map[string]string{"queue": "backup"})
	m.IncCounter("backup_jobs_total", 2, map[string]string{"state": "succeeded"})

	out := m.Exposition()
	assert.Contains(t, out, `manager_build_info{version="v1.2.3"} 1`)
	assert.Contains(t, out, "manager_uptime_seconds")
	assert.Contains(t, out, `http_requests_total{code="200",method="GET"} 2`)
	assert.Contains(t, out, `http_requests_total{code="500",method="POST"} 1`)
	assert.Contains(t, out, `scheduler_queue_depth{queue="backup"} 7`)
	assert.Contains(t, out, `backup_jobs_total{state="succeeded"} 2`)
}

func TestServerGracefulShutdown(t *testing.T) {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/ping", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	// Exercise Run's listen + shutdown path on a loopback port.
	addr := "127.0.0.1:18083"
	s := Server{Addr: addr, Handler: mux, Logger: discardLogger(), ShutdownTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	require.Eventually(t, func() bool {
		resp, err := nethttp.Get("http://" + addr + "/ping") //nolint:gosec,noctx // test-local probe
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == nethttp.StatusOK
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}
	_, err := nethttp.Get("http://" + addr + "/ping") //nolint:gosec,noctx // test-local probe; must fail after shutdown
	require.Error(t, err, "listener must be closed after shutdown")
}
