package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestIDPassthroughAndGeneration(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = GetReqID(r.Context())
	}))

	// Passes inbound IDs through.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "client-id-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, "client-id-1", seen)
	assert.Equal(t, "client-id-1", rec.Header().Get(RequestIDHeader))

	// Generates when absent.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.NotEmpty(t, seen)
	assert.Equal(t, seen, rec.Header().Get(RequestIDHeader))
	assert.Len(t, seen, 32, "16 random bytes hex-encoded")

	assert.Empty(t, GetReqID(req.Context()), "unset context returns empty")
}

func TestRecoveryConvertsPanicToJSON500(t *testing.T) {
	h := RequestID(Recovery(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom-secret-detail")
	})))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(rec, req) })
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, rec.Body.String(), "internal server error")
	assert.NotContains(t, rec.Body.String(), "kaboom-secret-detail", "panic details must stay in logs")
	assert.Contains(t, rec.Body.String(), rec.Header().Get(RequestIDHeader))
}

func TestTimeoutReturns503(t *testing.T) {
	h := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			_, _ = w.Write([]byte("too late"))
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "timed out")
}

func TestTimeoutDisabled(t *testing.T) {
	h := Timeout(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestLoggingEmitsAccessLine(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := RequestID(Logging(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	})))
	req := httptest.NewRequest(http.MethodPost, "/api/x?token=secret", nil)
	req.Header.Set(RequestIDHeader, "req-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	line := buf.String()
	assert.Contains(t, line, "req-1")
	assert.Contains(t, line, "POST")
	assert.Contains(t, line, "/api/x")
	assert.Contains(t, line, "418")
	assert.NotContains(t, line, "secret", "query strings must not be logged")
}
