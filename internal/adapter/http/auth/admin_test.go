package auth

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// admin_test.go: round-trips for the admin user/identity/provider handlers.

func TestUserAdminHandlers(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()

	rec := doRequest(r.call("POST", "/api/auth/users"), "POST", "/api/auth/users",
		`{"username":"bob","email":"bob@example.com","roles":["operator"]}`, "tok")
	if rec.Code != http.StatusCreated {
		t.Fatalf("createUser = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("create echoes hash: %s", rec.Body.String())
	}

	rec = doRequest(r.call("GET", "/api/auth/users"), "GET", "/api/auth/users?limit=5&offset=0&q=bob", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("listUsers = %d", rec.Code)
	}
	rec = doRequest(r.call("GET", "/api/auth/users"), "GET", "/api/auth/users?limit=9999&offset=-3", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("listUsers clamped = %d", rec.Code)
	}

	rec = doRequest(r.call("PATCH", "/api/auth/users/{id}"), "PATCH", "/api/auth/users/u1",
		`{"display_name":"A2"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("updateUser = %d: %s", rec.Code, rec.Body.String())
	}

	for _, route := range []string{
		"/api/auth/users/{id}/disable", "/api/auth/users/{id}/enable",
	} {
		method := "POST"
		path := "/api/auth/users/u1/" + strings.TrimPrefix(route, "/api/auth/users/{id}/")
		rec = doRequest(r.call(method, route), method, path, "", "tok")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s = %d", route, rec.Code)
		}
	}

	rec = doRequest(r.call("POST", "/api/auth/users/{id}/password"), "POST", "/api/auth/users/u1/password",
		`{"password":"reset-password-1"}`, "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("setPassword = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(r.call("POST", "/api/auth/users/{id}/password"), "POST", "/api/auth/users/u1/password",
		`{"password":"short"}`, "tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("setPassword weak = %d, want 400", rec.Code)
	}

	rec = doRequest(r.call("GET", "/api/auth/users/{id}/roles"), "GET", "/api/auth/users/u1/roles", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("listUserRoles = %d", rec.Code)
	}
	rec = doRequest(r.call("POST", "/api/auth/users/{id}/roles"), "POST", "/api/auth/users/u1/roles",
		`{"role":"operator"}`, "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("grantRole = %d: %s", rec.Code, rec.Body.String())
	}
	// revokeRole with a malformed nested path.
	rec = doRequest(r.call("DELETE", "/api/auth/users/{id}/roles/{role}"), "DELETE", "/api/auth/users/u1/roles/", "", "tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("revokeRole empty role = %d, want 400", rec.Code)
	}
}

func TestIdentityHandlers(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	fa.idents = []domain.UserExternalIdentity{
		{ID: "i1", UserID: "u1", Issuer: "https://idp.example", Subject: "s1", Email: "a@example.com"},
	}

	rec := doRequest(r.call("GET", "/api/auth/users/{id}/identities"), "GET", "/api/auth/users/u1/identities", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"subject":"s1"`) {
		t.Fatalf("list body: %s", rec.Body.String())
	}

	rec = doRequest(r.call("POST", "/api/auth/users/{id}/identities"), "POST", "/api/auth/users/u1/identities",
		`{"issuer":"https://idp.example","subject":"s2","email":"b@example.com"}`, "tok")
	if rec.Code != http.StatusCreated {
		t.Fatalf("link = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(r.call("DELETE", "/api/auth/users/{id}/identities/{iid}"), "DELETE", "/api/auth/users/u1/identities/i1", "", "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(r.call("DELETE", "/api/auth/users/{id}/identities/{iid}"), "DELETE", "/api/auth/users/u1/identities/", "", "tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unlink empty = %d, want 400", rec.Code)
	}

	for _, action := range []string{"disable", "enable"} {
		route := "/api/auth/users/{id}/identities/{iid}/" + action
		rec = doRequest(r.call("POST", route), "POST", "/api/auth/users/u1/identities/i1/"+action, "", "tok")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s = %d: %s", action, rec.Code, rec.Body.String())
		}
	}
}

func TestProviderHandlers(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	fa.providers = []domain.OIDCProvider{
		{ID: "p1", Name: "Okta", Issuer: "https://okta.example", ClientID: "c", Enabled: true, ClientSecretRef: "enc:v1:p1"},
	}
	fa.provider = &fa.providers[0]
	fa.testRep = &authsvc.ProviderTestReport{OK: true, Issuer: "https://okta.example", KeyCount: 2, HasUserInfo: true}

	rec := doRequest(r.call("GET", "/api/auth/providers"), "GET", "/api/auth/providers", "", "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"has_secret":true`) {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(r.call("PUT", "/api/auth/providers/{id}"), "PUT", "/api/auth/providers/p1",
		`{"name":"Okta2","issuer":"https://okta.example","client_id":"c"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", rec.Code, rec.Body.String())
	}

	for _, action := range []string{"enable", "disable"} {
		route := "/api/auth/providers/{id}/" + action
		rec = doRequest(r.call("POST", route), "POST", "/api/auth/providers/p1/"+action, "", "tok")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s = %d: %s", action, rec.Code, rec.Body.String())
		}
	}

	rec = doRequest(r.call("POST", "/api/auth/providers/{id}/test"), "POST", "/api/auth/providers/p1/test", "", "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"key_count":2`) {
		t.Fatalf("test = %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(r.call("POST", "/api/auth/providers/{id}/secret/rotate"), "POST", "/api/auth/providers/p1/secret/rotate",
		`{"secret":"brand-new-secret"}`, "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("rotate = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "brand-new-secret") {
		t.Fatalf("rotate echoes secret: %s", rec.Body.String())
	}

	// Unknown service failure collapses to a generic 500.
	fa.provErr = errBoomTest
	rec = doRequest(r.call("GET", "/api/auth/providers"), "GET", "/api/auth/providers", "", "tok")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("boom = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("500 leaks detail: %s", rec.Body.String())
	}
}

func TestSettingsHandlers(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()

	rec := doRequest(r.call("GET", "/api/auth/settings/local-login"), "GET", "/api/auth/settings/local-login", "", "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(r.call("POST", "/api/auth/settings/local-login"), "POST", "/api/auth/settings/local-login",
		`{"enabled":false,"confirmed":true}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body.String())
	}
	if fa.localLogin {
		t.Fatal("fake should record the flip")
	}
}

func TestOIDCErrorPaths(t *testing.T) {
	// OIDC routes without a configured callback URL.
	r := newFakeRouter()
	fa := &fakeAuth{localLogin: true}
	var events []authsvc.AuditEvent
	Mount(r, Deps{Auth: fa, Users: &fakeUsers{}, Audit: testAuditor{&events}})
	rec := doRequest(r.call("GET", "/api/auth/oidc/start"), "GET", "/api/auth/oidc/start?provider=p1", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured start = %d, want 503", rec.Code)
	}

	r2, fa2, _, _ := testSetup()
	fa2.startErr = authsvc.ErrProviderDisabled
	rec = doRequest(r2.call("GET", "/api/auth/oidc/start"), "GET", "/api/auth/oidc/start?provider=p1", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start failure = %d, want 400", rec.Code)
	}

	fa2.startErr = nil
	fa2.completeErr = authsvc.ErrInvalidState
	rec = doRequest(r2.call("GET", "/api/auth/oidc/callback"), "GET",
		"/api/auth/oidc/callback?provider=p1&code=c&state=s", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("callback failure = %d, want 400", rec.Code)
	}

	// Known IdP error code passes through allowlisted.
	rec = doRequest(r2.call("GET", "/api/auth/oidc/callback"), "GET",
		"/api/auth/oidc/callback?provider=p1&code=c&state=s&error=access_denied", "", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "access_denied") {
		t.Fatalf("idp deny = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogoutOIDCAndError(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.logoutURL = "https://idp.example/logout?post=x"
	rec := doRequest(r.call("POST", "/api/auth/logout"), "POST", "/api/auth/logout", "", "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "idp.example/logout") {
		t.Fatalf("oidc logout = %d: %s", rec.Code, rec.Body.String())
	}
	// No cookie: still 200, no service call needed.
	fa.logoutErr = errBoomTest
	rec = doRequest(r.call("POST", "/api/auth/logout"), "POST", "/api/auth/logout", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie-less logout = %d, want 200", rec.Code)
	}
	rec = doRequest(r.call("POST", "/api/auth/logout"), "POST", "/api/auth/logout", "", "tok")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("logout boom = %d, want 500", rec.Code)
	}
}

func TestChangePasswordError(t *testing.T) {
	r, fa, fu, _ := testSetup()
	fa.info = adminInfo()
	fu.err = errBoomTest
	rec := doRequest(r.call("POST", "/api/auth/password/change"), "POST", "/api/auth/password/change",
		`{"old_password":"old-12345678","new_password":"new-12345678"}`, "tok")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
}

func TestSessionCookieName(t *testing.T) {
	if got := (Deps{}).SessionCookieName(); got != "vaultix_session" {
		t.Fatalf("default = %q", got)
	}
	if got := (Deps{Cookie: CookieConfig{Name: "x"}}).SessionCookieName(); got != "x" {
		t.Fatalf("custom = %q", got)
	}
}
