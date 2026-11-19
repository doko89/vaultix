package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSRF(t *testing.T) {
	inner := CSRF("vaultix_session")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(t *testing.T, method, target string, setup func(r *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, nil)
		req.Host = "manager.example:8080"
		if setup != nil {
			setup(req)
		}
		inner.ServeHTTP(rec, req)
		return rec
	}
	withCookie := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "vaultix_session", Value: "tok"})
	}

	// Safe methods pass with or without a cookie.
	assert.Equal(t, http.StatusNoContent, serve(t, http.MethodGet, "/api/x", withCookie).Code)
	assert.Equal(t, http.StatusNoContent, serve(t, http.MethodGet, "/api/x", nil).Code)

	// Mutating requests without a cookie pass (bearer-token clients).
	assert.Equal(t, http.StatusNoContent, serve(t, http.MethodPost, "/api/x", nil).Code)

	// Cookie + matching Origin passes.
	rec := serve(t, http.MethodPost, "/api/x", func(r *http.Request) {
		withCookie(r)
		r.Header.Set("Origin", "http://manager.example:8080")
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Cookie + X-Requested-With passes without Origin.
	rec = serve(t, http.MethodDelete, "/api/x", func(r *http.Request) {
		withCookie(r)
		r.Header.Set("X-Requested-With", "fetch")
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Cookie + cross-site Origin is rejected.
	rec = serve(t, http.MethodPost, "/api/x", func(r *http.Request) {
		withCookie(r)
		r.Header.Set("Origin", "https://evil.example")
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":403`)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	// Cookie + missing Origin is rejected.
	rec = serve(t, http.MethodPut, "/api/x", withCookie)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
