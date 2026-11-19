package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	usersvc "github.com/vaultix/vaultix/internal/usecase/user"
)

// TestErrorCodeContract pins every service error to its HTTP status/code.
func TestErrorCodeContract(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{authsvc.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
		{authsvc.ErrAccountDisabled, http.StatusForbidden, "account_disabled"},
		{authsvc.ErrLocalLoginDisabled, http.StatusForbidden, "local_login_disabled"},
		{authsvc.ErrNoRolesAssigned, http.StatusForbidden, "no_role"},
		{authsvc.ErrNoMappedRole, http.StatusForbidden, "no_role"},
		{authsvc.ErrPreprovisionedOnly, http.StatusForbidden, "preprovisioned_only"},
		{authsvc.ErrInvalidState, http.StatusBadRequest, "invalid_state"},
		{authsvc.ErrProviderDisabled, http.StatusBadRequest, "provider_unavailable"},
		{authsvc.ErrNoBreakGlass, http.StatusConflict, "break_glass"},
		{usersvc.ErrLastAdmin, http.StatusConflict, "break_glass"},
		{authsvc.ErrWeakPassword, http.StatusBadRequest, "weak_password"},
		{usersvc.ErrWeakPassword, http.StatusBadRequest, "weak_password"},
		{domain.ErrNotFound, http.StatusNotFound, "not_found"},
		{domain.ErrConflict, http.StatusConflict, "conflict"},
		{domain.ErrValidation, http.StatusBadRequest, "validation"},
		{domain.ErrForbidden, http.StatusForbidden, "forbidden"},
		{errors.New("surprise"), http.StatusInternalServerError, "internal"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeServiceError(rec, tc.err)
		if rec.Code != tc.status {
			t.Fatalf("%v: status = %d, want %d", tc.err, rec.Code, tc.status)
		}
		if !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
			t.Fatalf("%v: body %s lacks code %q", tc.err, rec.Body.String(), tc.code)
		}
	}
}

// TestStdlibMuxInterop proves the handlers work on a net/http ServeMux too
// (exercises the r.PathValue branch of pathIDRest).
func TestStdlibMuxInterop(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/users/{id}", r.call("GET", "/api/auth/users/{id}"))
	mux.HandleFunc("DELETE /api/auth/users/{id}/roles/{role}", r.call("DELETE", "/api/auth/users/{id}/roles/{role}"))
	mux.HandleFunc("GET /api/auth/providers/{id}", r.call("GET", "/api/auth/providers/{id}"))
	fa.provider = &domain.OIDCProvider{ID: "p1", Name: "N"}

	req := httptest.NewRequest("GET", "/api/auth/users/u42", nil)
	req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: "tok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib getUser = %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/auth/users/u42/roles/operator", nil)
	req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: "tok"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stdlib revokeRole = %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/auth/providers/p1", nil)
	req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: "tok"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stdlib getProvider = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSmallHelpers(t *testing.T) {
	req := httptest.NewRequest("GET", "/x?limit=abc&offset=7", nil)
	if got := queryInt(req, "limit", 50); got != 50 {
		t.Fatalf("bad limit = %d", got)
	}
	if got := queryInt(req, "missing", 50); got != 50 {
		t.Fatalf("missing = %d", got)
	}
	req.RemoteAddr = "no-port-here"
	if got := clientIP(req); got != "no-port-here" {
		t.Fatalf("ip = %q", got)
	}
	long := strings.Repeat("a", 600)
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.Header.Set("User-Agent", long)
	if got := userAgent(req2); len(got) != 512 {
		t.Fatalf("ua len = %d", len(got))
	}
	// actorID without a session (public-route shape).
	if got := actorID(httptest.NewRequest("GET", "/x", nil)); got != "" {
		t.Fatalf("actor = %q", got)
	}
	// NewGate default cookie name.
	g := NewGate(&fakeAuth{}, "")
	if g.cookieName != "vaultix_session" {
		t.Fatalf("cookie = %q", g.cookieName)
	}
}
