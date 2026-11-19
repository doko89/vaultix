package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	apikeysvc "github.com/vaultix/vaultix/internal/usecase/apikey"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// fakeKeys verifies canned API keys by token.
type fakeKeys struct {
	byToken map[string]*apikeysvc.VerifiedKey
	calls   []string
}

func (f *fakeKeys) Verify(_ context.Context, secret string) (*apikeysvc.VerifiedKey, error) {
	f.calls = append(f.calls, secret)
	vk, ok := f.byToken[secret]
	if !ok {
		return nil, apikeysvc.ErrInvalidKey
	}
	return vk, nil
}

var _ KeyVerifier = (*fakeKeys)(nil)

func keyFixture(id string, scopes ...string) *apikeysvc.VerifiedKey {
	return &apikeysvc.VerifiedKey{
		Key:  &domain.ApiKey{ID: id, UserID: "u-alice", Name: "cli", KeyPrefix: "vxk_test1", Scopes: scopes},
		User: &domain.User{ID: "u-alice", Username: "alice", Email: "alice@example.com"},
	}
}

func sessionFixture() *authsvc.SessionInfo {
	return &authsvc.SessionInfo{
		Session: &domain.Session{ID: "s1", UserID: "u-alice", ExpiresAt: time.Now().Add(time.Hour)},
		User:    &domain.User{ID: "u-alice", Username: "alice"},
		Roles:   []domain.Role{{Name: domain.RoleReadOnly}},
	}
}

// serve runs h with the given method, cookie, and Authorization header.
func serve(t *testing.T, h http.HandlerFunc, method, cookie, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/thing", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "vaultix_session", Value: cookie})
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func okHandler(captured **authsvc.SessionInfo, capturedKey **apikeysvc.VerifiedKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, _ := SessionFromContext(r.Context())
		*captured = info
		vk, _ := KeyActorFromContext(r.Context())
		*capturedKey = vk
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestGateAPIKeyMatrix(t *testing.T) {
	const (
		readKey  = "vxk_read00000000000000000000000001"
		writeKey = "vxk_write0000000000000000000000001"
		adminKey = "vxk_admin0000000000000000000000001"
	)
	newGate := func() (*Gate, *fakeKeys, *fakeAuth) {
		auth := &fakeAuth{info: sessionFixture()}
		keys := &fakeKeys{byToken: map[string]*apikeysvc.VerifiedKey{
			readKey:  keyFixture("k-read", domain.ScopeRead),
			writeKey: keyFixture("k-write", domain.ScopeWrite),
			adminKey: keyFixture("k-admin", domain.ScopeAdmin),
		}}
		g := NewGate(auth, "")
		g.SetKeyVerifier(keys)
		return g, keys, auth
	}

	t.Run("no credentials 401", func(t *testing.T) {
		g, keys, _ := newGate()
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
		if len(keys.calls) != 0 {
			t.Fatal("no Bearer [REDACTED] no verifier call")
		}
	})

	t.Run("bad key 401", func(t *testing.T) {
		g, _, _ := newGate()
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "", "Bearer vxk_bogus00000000000000000000001")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
	})

	t.Run("session still passes", func(t *testing.T) {
		g, keys, _ := newGate()
		var info *authsvc.SessionInfo
		var vk *apikeysvc.VerifiedKey
		rec := serve(t, g.RequireAuth(okHandler(&info, &vk)), "GET", "sess-token", "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d", rec.Code)
		}
		if info == nil || info.User.ID != "u-alice" {
			t.Fatalf("session info not attached: %+v", info)
		}
		if vk != nil {
			t.Fatal("session caller must not see a key actor")
		}
		if len(keys.calls) != 0 {
			t.Fatal("session path must not consult the verifier")
		}
	})

	t.Run("read key GET passes", func(t *testing.T) {
		g, _, _ := newGate()
		var info *authsvc.SessionInfo
		var vk *apikeysvc.VerifiedKey
		rec := serve(t, g.RequireAuth(okHandler(&info, &vk)), "GET", "", "Bearer "+readKey)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d", rec.Code)
		}
		if vk == nil || vk.Key.ID != "k-read" {
			t.Fatalf("key actor not attached: %+v", vk)
		}
		if info == nil || info.User.ID != "u-alice" || info.Session == nil {
			t.Fatalf("synthesized session info missing: %+v", info)
		}
	})

	t.Run("read key POST 403", func(t *testing.T) {
		g, _, _ := newGate()
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "POST", "", "Bearer "+readKey)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rec.Code)
		}
	})

	t.Run("write key POST passes", func(t *testing.T) {
		g, _, _ := newGate()
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "POST", "", "Bearer "+writeKey)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d", rec.Code)
		}
	})

	t.Run("Bearer [REDACTED] over cookie", func(t *testing.T) {
		g, keys, auth := newGate()
		auth.infoErr = errBoomTest // session would fail; Bearer [REDACTED] win
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "sess-token", "Bearer "+readKey)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d", rec.Code)
		}
		if len(keys.calls) != 1 {
			t.Fatalf("verifier calls = %d", len(keys.calls))
		}
	})

	t.Run("non-vxk Bearer [REDACTED] through to cookie", func(t *testing.T) {
		g, keys, _ := newGate()
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "sess-token", "Bearer agent-token-opaque")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d", rec.Code)
		}
		if len(keys.calls) != 0 {
			t.Fatal("agent-style Bearer [REDACTED] not touch the key verifier")
		}
	})

	t.Run("unwired gate ignores vxk bearers", func(t *testing.T) {
		g := NewGate(&fakeAuth{info: sessionFixture()}, "")
		rec := serve(t, g.RequireAuth(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "", "Bearer "+readKey)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401 via cookie path", rec.Code)
		}
	})
}

func TestGateAPIKeyRoles(t *testing.T) {
	const (
		readKey  = "vxk_read00000000000000000000000001"
		writeKey = "vxk_write0000000000000000000000001"
		adminKey = "vxk_admin0000000000000000000000001"
	)
	keys := &fakeKeys{byToken: map[string]*apikeysvc.VerifiedKey{
		readKey:  keyFixture("k-read", domain.ScopeRead),
		writeKey: keyFixture("k-write", domain.ScopeRead, domain.ScopeWrite),
		adminKey: keyFixture("k-admin", domain.ScopeAdmin),
	}}
	g := NewGate(&fakeAuth{infoErr: errBoomTest}, "")
	g.SetKeyVerifier(keys)

	op := g.RequireRole(domain.RoleAdministrator, domain.RoleOperator)
	admin := g.RequireRole(domain.RoleAdministrator)

	cases := []struct {
		name    string
		handler func(http.HandlerFunc) http.HandlerFunc
		method  string
		token   string
		want    int
	}{
		{"read key on operator route", op, "GET", readKey, http.StatusForbidden},
		{"write key on operator route", op, "POST", writeKey, http.StatusNoContent},
		{"write key on admin route", admin, "GET", adminKey, http.StatusNoContent},
		{"admin key on operator route", op, "DELETE", adminKey, http.StatusNoContent},
		{"write key on admin-only route denied", admin, "GET", writeKey, http.StatusForbidden},
		{"bad key on role route 401", op, "GET", "vxk_nope0000000000000000000000001", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, tc.handler(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), tc.method, "", "Bearer "+tc.token)
			if rec.Code != tc.want {
				t.Fatalf("code = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestGateRequireSessionIgnoresBearers(t *testing.T) {
	keys := &fakeKeys{byToken: map[string]*apikeysvc.VerifiedKey{
		"vxk_read00000000000000000000000001": keyFixture("k-read", domain.ScopeRead),
	}}
	g := NewGate(&fakeAuth{info: sessionFixture()}, "")
	g.SetKeyVerifier(keys)
	// Valid Bearer [REDACTED] valid session: session wins (key never consulted).
	rec := serve(t, g.RequireSession(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "sess", "Bearer vxk_read00000000000000000000000001")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	if len(keys.calls) != 0 {
		t.Fatal("RequireSession must never consult the verifier")
	}
	// Valid Bearer [REDACTED] no session: 401 (keys cannot use session-only routes).
	rec = serve(t, g.RequireSession(okHandler(new(*authsvc.SessionInfo), new(*apikeysvc.VerifiedKey))), "GET", "", "Bearer vxk_read00000000000000000000000001")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestRolesForScopes(t *testing.T) {
	names := func(roles []domain.Role) []string {
		var out []string
		for _, r := range roles {
			out = append(out, r.Name)
		}
		return out
	}
	if got := names(rolesForScopes([]string{"read"})); len(got) != 1 || got[0] != domain.RoleReadOnly {
		t.Fatalf("read = %v", got)
	}
	if got := names(rolesForScopes([]string{"write"})); len(got) != 2 {
		t.Fatalf("write = %v", got)
	}
	if got := names(rolesForScopes([]string{"admin"})); len(got) != 3 || got[0] != domain.RoleAdministrator {
		t.Fatalf("admin = %v", got)
	}
}
