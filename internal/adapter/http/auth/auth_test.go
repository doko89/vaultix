package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	usersvc "github.com/vaultix/vaultix/internal/usecase/user"
)

// Wiring assertions: the real usecase services satisfy the local Deps
// interfaces, so cmd can pass them straight into Mount.
var (
	_ AuthService = (*authsvc.Service)(nil)
	_ UserService = (*usersvc.Service)(nil)
	_ Auditor     = testAuditor{}
)

// errBoomTest simulates unexpected service failures.
var errBoomTest = errors.New("boom")

type testAuditor struct{ events *[]authsvc.AuditEvent }

func (a testAuditor) RecordAuthEvent(_ context.Context, e authsvc.AuditEvent) error {
	*a.events = append(*a.events, e)
	return nil
}

type fakeRouter struct {
	routes map[string]http.HandlerFunc // "METHOD pattern" -> handler
}

func newFakeRouter() *fakeRouter { return &fakeRouter{routes: map[string]http.HandlerFunc{}} }

func (f *fakeRouter) Use(...func(http.Handler) http.Handler) {}
func (f *fakeRouter) Get(p string, h http.HandlerFunc)       { f.routes["GET "+p] = h }
func (f *fakeRouter) Post(p string, h http.HandlerFunc)      { f.routes["POST "+p] = h }
func (f *fakeRouter) Put(p string, h http.HandlerFunc)       { f.routes["PUT "+p] = h }
func (f *fakeRouter) Patch(p string, h http.HandlerFunc)     { f.routes["PATCH "+p] = h }
func (f *fakeRouter) Delete(p string, h http.HandlerFunc)    { f.routes["DELETE "+p] = h }

var _ Router = (*fakeRouter)(nil)

func (f *fakeRouter) call(method, pattern string) http.HandlerFunc {
	h, ok := f.routes[method+" "+pattern]
	if !ok {
		panic("route not registered: " + method + " " + pattern)
	}
	return h
}

type fakeAuth struct {
	loginRes    *authsvc.LoginResult
	loginErr    error
	info        *authsvc.SessionInfo
	infoErr     error
	logoutURL   string
	logoutErr   error
	start       *authsvc.OIDCStart
	startErr    error
	completeRes *authsvc.LoginResult
	completeErr error
	providers   []domain.OIDCProvider
	provider    *domain.OIDCProvider
	provErr     error
	testRep     *authsvc.ProviderTestReport
	idents      []domain.UserExternalIdentity
	ident       *domain.UserExternalIdentity
	identErr    error
	localLogin  bool
}

func (f *fakeAuth) Login(context.Context, string, string, string, string) (*authsvc.LoginResult, error) {
	return f.loginRes, f.loginErr
}
func (f *fakeAuth) Logout(context.Context, string, string) (string, error) {
	return f.logoutURL, f.logoutErr
}
func (f *fakeAuth) ValidateSession(context.Context, string) (*authsvc.SessionInfo, error) {
	return f.info, f.infoErr
}
func (f *fakeAuth) StartOIDCLogin(context.Context, string, string) (*authsvc.OIDCStart, error) {
	return f.start, f.startErr
}
func (f *fakeAuth) CompleteOIDCLogin(context.Context, string, string, string, string, string, string) (*authsvc.LoginResult, error) {
	return f.completeRes, f.completeErr
}
func (f *fakeAuth) ListProviders(context.Context) ([]domain.OIDCProvider, error) {
	return f.providers, f.provErr
}
func (f *fakeAuth) GetProvider(context.Context, string) (*domain.OIDCProvider, error) {
	return f.provider, f.provErr
}
func (f *fakeAuth) CreateProvider(_ context.Context, in authsvc.ProviderInput, _ string) (*domain.OIDCProvider, error) {
	if f.provErr != nil {
		return nil, f.provErr
	}
	return &domain.OIDCProvider{ID: "p1", Name: in.Name, Issuer: in.Issuer, ClientID: in.ClientID, Enabled: true}, nil
}
func (f *fakeAuth) UpdateProvider(_ context.Context, id string, in authsvc.ProviderInput, _ string) (*domain.OIDCProvider, error) {
	if f.provErr != nil {
		return nil, f.provErr
	}
	p := *f.provider
	p.ID, p.Name = id, in.Name
	return &p, nil
}
func (f *fakeAuth) SetProviderEnabled(context.Context, string, bool, string) error {
	return f.provErr
}
func (f *fakeAuth) RotateProviderSecret(context.Context, string, string, string) error {
	return f.provErr
}
func (f *fakeAuth) TestProvider(context.Context, string, string) (*authsvc.ProviderTestReport, error) {
	return f.testRep, f.provErr
}
func (f *fakeAuth) ListUserIdentities(context.Context, string) ([]domain.UserExternalIdentity, error) {
	return f.idents, f.identErr
}
func (f *fakeAuth) LinkIdentity(_ context.Context, userID, _, issuer, subject, email, _ string) (*domain.UserExternalIdentity, error) {
	if f.identErr != nil {
		return nil, f.identErr
	}
	return &domain.UserExternalIdentity{ID: "i1", UserID: userID, Issuer: issuer, Subject: subject, Email: email}, nil
}
func (f *fakeAuth) UnlinkIdentity(context.Context, string, string, string) error {
	return f.identErr
}
func (f *fakeAuth) SetIdentityDisabled(context.Context, string, string, bool, string) error {
	return f.identErr
}
func (f *fakeAuth) SetLocalLoginEnabled(_ context.Context, enabled, _ bool, _ string) error {
	f.localLogin = enabled
	return nil
}
func (f *fakeAuth) LocalLoginEnabled() bool { return f.localLogin }

var _ AuthService = (*fakeAuth)(nil)

type fakeUsers struct {
	user    *domain.User
	users   []domain.User
	total   int
	err     error
	roles   []domain.Role
	roleErr error
}

func (f *fakeUsers) CreateUser(_ context.Context, in usersvc.CreateInput, _ string) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.User{ID: "u9", Username: in.Username, Email: in.Email, DisplayName: in.DisplayName}, nil
}
func (f *fakeUsers) GetUser(context.Context, string) (*domain.User, error) { return f.user, f.err }
func (f *fakeUsers) ListUsers(context.Context, domain.ListOptions) (domain.ListResult[domain.User], error) {
	return domain.ListResult[domain.User]{Items: f.users, Total: f.total}, f.err
}
func (f *fakeUsers) UpdateUser(_ context.Context, id string, in usersvc.UpdateInput, _ string) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	u := *f.user
	u.ID = id
	if in.DisplayName != "" {
		u.DisplayName = in.DisplayName
	}
	return &u, nil
}
func (f *fakeUsers) SetDisabled(context.Context, string, bool, string) error      { return f.err }
func (f *fakeUsers) SetPassword(context.Context, string, string, string) error    { return f.err }
func (f *fakeUsers) ChangePassword(context.Context, string, string, string) error { return f.err }
func (f *fakeUsers) ListRoles(context.Context) ([]domain.Role, error)             { return f.roles, f.roleErr }
func (f *fakeUsers) ListUserRoles(context.Context, string) ([]domain.Role, error) {
	return f.roles, f.roleErr
}
func (f *fakeUsers) GrantRole(context.Context, string, string, string) error  { return f.err }
func (f *fakeUsers) RevokeRole(context.Context, string, string, string) error { return f.err }

var _ UserService = (*fakeUsers)(nil)

func testSetup() (*fakeRouter, *fakeAuth, *fakeUsers, *[]authsvc.AuditEvent) {
	r := newFakeRouter()
	fa := &fakeAuth{localLogin: true}
	fu := &fakeUsers{
		user:  &domain.User{ID: "u1", Username: "alice", Email: "a@example.com"},
		users: []domain.User{{ID: "u1", Username: "alice", Email: "a@example.com"}},
		total: 1,
		roles: []domain.Role{{ID: "r1", Name: domain.RoleAdministrator}},
	}
	var events []authsvc.AuditEvent
	Mount(r, Deps{
		Auth: fa, Users: fu, Audit: testAuditor{&events},
		OIDCRedirectURL: "https://app/api/auth/oidc/callback",
	})
	return r, fa, fu, &events
}

func adminInfo() *authsvc.SessionInfo {
	return &authsvc.SessionInfo{
		Session: &domain.Session{ID: "s1", UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
		User:    &domain.User{ID: "u1", Username: "alice", Email: "a@example.com"},
		Roles:   []domain.Role{{ID: "r1", Name: domain.RoleAdministrator}},
	}
}

func doRequest(h http.HandlerFunc, method, target, body, cookie string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = "10.0.0.1:1234"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: cookie})
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestMountRegistersRoutes(t *testing.T) {
	r, _, _, _ := testSetup()
	for _, route := range []string{
		"POST /api/auth/login", "POST /api/auth/logout",
		"GET /api/auth/oidc/start", "GET /api/auth/oidc/callback",
		"GET /api/auth/providers/public", "GET /api/auth/me",
		"POST /api/auth/password/change",
		"GET /api/auth/users", "POST /api/auth/users",
		"GET /api/auth/users/{id}", "PATCH /api/auth/users/{id}",
		"POST /api/auth/users/{id}/disable", "POST /api/auth/users/{id}/enable",
		"POST /api/auth/users/{id}/password",
		"GET /api/auth/users/{id}/roles", "POST /api/auth/users/{id}/roles",
		"DELETE /api/auth/users/{id}/roles/{role}", "GET /api/auth/roles",
		"GET /api/auth/users/{id}/identities", "POST /api/auth/users/{id}/identities",
		"DELETE /api/auth/users/{id}/identities/{iid}",
		"POST /api/auth/users/{id}/identities/{iid}/disable",
		"POST /api/auth/users/{id}/identities/{iid}/enable",
		"GET /api/auth/providers", "POST /api/auth/providers",
		"GET /api/auth/providers/{id}", "PUT /api/auth/providers/{id}",
		"POST /api/auth/providers/{id}/enable", "POST /api/auth/providers/{id}/disable",
		"POST /api/auth/providers/{id}/test", "POST /api/auth/providers/{id}/secret/rotate",
		"POST /api/auth/settings/local-login", "GET /api/auth/settings/local-login",
	} {
		if _, ok := r.routes[route]; !ok {
			t.Errorf("missing route %s", route)
		}
	}
}

func TestLoginLogoutMe(t *testing.T) {
	r, fa, _, _ := testSetup()
	info := adminInfo()
	fa.loginRes = &authsvc.LoginResult{
		Token: "tok-123", Session: info.Session, User: info.User, Roles: info.Roles,
	}
	rec := doRequest(r.call("POST", "/api/auth/login"), "POST", "/api/auth/login",
		`{"identifier":"alice","password":"pw-12345678"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login code = %d: %s", rec.Code, rec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vaultix_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value != "tok-123" {
		t.Fatalf("session cookie missing: %v", rec.Result().Cookies())
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if strings.Contains(rec.Body.String(), "tok-123") {
		t.Fatal("login body must not echo the token")
	}
	// me
	fa.info = info
	rec = doRequest(r.call("GET", "/api/auth/me"), "GET", "/api/auth/me", "", "tok-123")
	if rec.Code != http.StatusOK {
		t.Fatalf("me code = %d", rec.Code)
	}
	// logout clears the cookie
	fa.info = info
	rec = doRequest(r.call("POST", "/api/auth/logout"), "POST", "/api/auth/logout", "", "tok-123")
	if rec.Code != http.StatusOK {
		t.Fatalf("logout code = %d", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vaultix_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout must clear the session cookie")
	}
}

func TestLoginValidation(t *testing.T) {
	r, _, _, _ := testSetup()
	for name, body := range map[string]string{
		"empty":        `{}`,
		"missing pass": `{"identifier":"a"}`,
		"bad json":     `{oops`,
		"unknown":      `{"identifier":"a","password":"b","hacker":1}`,
	} {
		rec := doRequest(r.call("POST", "/api/auth/login"), "POST", "/api/auth/login", body, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400", name, rec.Code)
		}
	}
}

func TestLoginServiceErrorMapping(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.loginErr = authsvc.ErrInvalidCredentials
	rec := doRequest(r.call("POST", "/api/auth/login"), "POST", "/api/auth/login",
		`{"identifier":"a","password":"b"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("error echoes field: %s", rec.Body.String())
	}
}

func TestMiddlewareMatrix(t *testing.T) {
	r, fa, _, _ := testSetup()
	me := r.call("GET", "/api/auth/me")
	admin := r.call("GET", "/api/auth/roles")

	// No cookie -> 401 on both.
	if rec := doRequest(me, "GET", "/api/auth/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-cookie me = %d, want 401", rec.Code)
	}
	if rec := doRequest(admin, "GET", "/api/auth/roles", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-cookie admin = %d, want 401", rec.Code)
	}
	// Invalid session -> 401 + cookie cleared.
	fa.info, fa.infoErr = nil, authsvc.ErrInvalidCredentials
	rec := doRequest(me, "GET", "/api/auth/me", "", "bad")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad session = %d, want 401", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vaultix_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("invalid session must clear the cookie")
	}
	// Readonly on admin route -> 403; on self route -> 200.
	ro := adminInfo()
	ro.Roles = []domain.Role{{ID: "r3", Name: domain.RoleReadOnly}}
	fa.info, fa.infoErr = ro, nil
	if rec := doRequest(admin, "GET", "/api/auth/roles", "", "tok"); rec.Code != http.StatusForbidden {
		t.Fatalf("readonly admin = %d, want 403", rec.Code)
	}
	if rec := doRequest(me, "GET", "/api/auth/me", "", "tok"); rec.Code != http.StatusOK {
		t.Fatalf("readonly me = %d, want 200", rec.Code)
	}
	// Admin bypasses role checks.
	fa.info = adminInfo()
	if rec := doRequest(admin, "GET", "/api/auth/roles", "", "tok"); rec.Code != http.StatusOK {
		t.Fatalf("admin = %d, want 200", rec.Code)
	}
}

func TestGateRequireRoleMatrix(t *testing.T) {
	_, fa, _, _ := testSetup()
	gate := NewGate(fa, "")
	probe := gate.RequireRole(domain.RoleOperator, domain.RoleReadOnly)(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	op := adminInfo()
	op.Roles = []domain.Role{{Name: domain.RoleOperator}}
	fa.info, fa.infoErr = op, nil
	if rec := doRequest(probe, "GET", "/x", "", "tok"); rec.Code != http.StatusNoContent {
		t.Fatalf("operator = %d, want 204", rec.Code)
	}
	// Admin passes even when not listed.
	fa.info = adminInfo()
	if rec := doRequest(probe, "GET", "/x", "", "tok"); rec.Code != http.StatusNoContent {
		t.Fatalf("admin bypass = %d, want 204", rec.Code)
	}
	// User with an unrelated custom role is denied.
	custom := adminInfo()
	custom.Roles = []domain.Role{{Name: "auditor"}}
	fa.info = custom
	if rec := doRequest(probe, "GET", "/x", "", "tok"); rec.Code != http.StatusForbidden {
		t.Fatalf("custom role = %d, want 403", rec.Code)
	}
}

func TestOIDCStartCallback(t *testing.T) {
	r, fa, _, events := testSetup()
	fa.start = &authsvc.OIDCStart{AuthURL: "https://idp.example/auth?x=1", State: "s1"}
	rec := doRequest(r.call("GET", "/api/auth/oidc/start"), "GET", "/api/auth/oidc/start?provider=p1", "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("start code = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != fa.start.AuthURL {
		t.Fatalf("location = %q", loc)
	}
	rec = doRequest(r.call("GET", "/api/auth/oidc/start"), "GET", "/api/auth/oidc/start", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider = %d, want 400", rec.Code)
	}
	// Callback: missing params -> 400 + audit.
	rec = doRequest(r.call("GET", "/api/auth/oidc/callback"), "GET", "/api/auth/oidc/callback?provider=p1", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing params = %d, want 400", rec.Code)
	}
	if len(*events) == 0 {
		t.Fatal("callback param failure must audit")
	}
	// Callback: IdP error -> actionable 400 + audit, code allowlisted.
	rec = doRequest(r.call("GET", "/api/auth/oidc/callback"), "GET",
		"/api/auth/oidc/callback?provider=p1&code=c&state=s&error=<script>alert(1)</script>", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("idp error = %d, want 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("idp error code reflected: %s", rec.Body.String())
	}
	// Callback success -> 302 + cookie.
	info := adminInfo()
	fa.completeRes = &authsvc.LoginResult{Token: "tok-oidc", Session: info.Session, User: info.User, Roles: info.Roles}
	rec = doRequest(r.call("GET", "/api/auth/oidc/callback"), "GET",
		"/api/auth/oidc/callback?provider=p1&code=c&state=s", "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("callback ok = %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vaultix_session" && c.Value == "tok-oidc" {
			found = true
		}
	}
	if !found {
		t.Fatal("callback must set the session cookie")
	}
}

func TestAdminCRUDPaths(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	// getUser parses the id segment.
	rec := doRequest(r.call("GET", "/api/auth/users/{id}"), "GET", "/api/auth/users/u42", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("getUser = %d: %s", rec.Code, rec.Body.String())
	}
	// revokeRole parses nested segments.
	rec = doRequest(r.call("DELETE", "/api/auth/users/{id}/roles/{role}"), "DELETE", "/api/auth/users/u42/roles/operator", "", "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revokeRole = %d: %s", rec.Code, rec.Body.String())
	}
	// grantRole validates DTO.
	rec = doRequest(r.call("POST", "/api/auth/users/{id}/roles"), "POST", "/api/auth/users/u42/roles", `{}`, "tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grantRole empty = %d, want 400", rec.Code)
	}
	// changePassword clears the cookie (sessions revoked server-side).
	rec = doRequest(r.call("POST", "/api/auth/password/change"), "POST", "/api/auth/password/change",
		`{"old_password":"old-12345678","new_password":"new-12345678"}`, "tok")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("changePassword = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvidersPublicFiltersDisabled(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.providers = []domain.OIDCProvider{
		{ID: "p1", Name: "Okta", Enabled: true},
		{ID: "p2", Name: "Old", Enabled: false},
	}
	rec := doRequest(r.call("GET", "/api/auth/providers/public"), "GET", "/api/auth/providers/public", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Okta") || strings.Contains(body, "Old") {
		t.Fatalf("public list wrong: %s", body)
	}
}

func TestProviderResponsesNeverCarrySecrets(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	fa.provider = &domain.OIDCProvider{ID: "p1", Name: "Okta", Issuer: "https://okta.example", ClientID: "cid", ClientSecretRef: "enc:v1:p1"}
	rec := doRequest(r.call("GET", "/api/auth/providers/{id}"), "GET", "/api/auth/providers/p1", "", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "client_secret") || strings.Contains(body, "enc:v1") {
		t.Fatalf("provider response leaks secret material: %s", body)
	}
	if !strings.Contains(body, `"has_secret":true`) {
		t.Fatalf("has_secret missing: %s", body)
	}
	// Create echoes config but never the submitted secret.
	rec = doRequest(r.call("POST", "/api/auth/providers"), "POST", "/api/auth/providers",
		`{"name":"N","issuer":"https://n.example","client_id":"c","client_secret":"ULTRA-SECRET-9"}`, "tok")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ULTRA-SECRET-9") {
		t.Fatalf("create echoes secret: %s", rec.Body.String())
	}
}

func TestErrorMappingAndSanitize(t *testing.T) {
	r, fa, _, _ := testSetup()
	fa.info = adminInfo()
	fa.provErr = domain.ErrNotFound
	rec := doRequest(r.call("GET", "/api/auth/providers/{id}"), "GET", "/api/auth/providers/nope", "", "tok")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if got := Sanitize("oops token=abc123&x=1"); !strings.Contains(got, "token=[redacted]") {
		t.Fatalf("sanitize = %q", got)
	}
	if got := Sanitize("prefix-ULTRA-suffix", "ULTRA"); strings.Contains(got, "ULTRA") {
		t.Fatalf("explicit sanitize = %q", got)
	}
	if got := Sanitize("short", "abc"); got != "short" {
		t.Fatalf("short secrets must not rewrite: %q", got)
	}
}

func TestPathIDParsing(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		id     string
		rest   string
		ok     bool
	}{
		{"/api/auth/users/u42", "/api/auth/users/", "u42", "", true},
		{"/api/auth/users/u42/", "/api/auth/users/", "u42", "", true},
		{"/api/auth/users/u42/roles/operator", "/api/auth/users/", "u42", "roles/operator", true},
		{"/api/auth/users/", "/api/auth/users/", "", "", false},
		{"/api/auth/other", "/api/auth/users/", "", "", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		id, rest, ok := pathIDRest(req, tc.prefix)
		if id != tc.id || rest != tc.rest || ok != tc.ok {
			t.Fatalf("%s: got (%q,%q,%v)", tc.path, id, rest, ok)
		}
	}
}
