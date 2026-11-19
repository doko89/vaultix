package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vaultix/vaultix/internal/domain"
)

type testRig struct {
	svc   *Service
	users *fakeUsers
	provs *fakeProviders
	sess  *fakeSessions
	oidc  *fakeOIDC
	sec   *fakeSecrets
	audit *fakeAudit
	clock *fixedClock
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	rig := &testRig{
		users: newFakeUsers(),
		provs: newFakeProviders(),
		sess:  newFakeSessions(),
		oidc:  &fakeOIDC{},
		sec:   newFakeSecrets(),
		audit: &fakeAudit{},
		clock: &fixedClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)},
	}
	svc, err := NewService(Deps{
		Users: rig.users, Providers: rig.provs, Sessions: rig.sess,
		OIDC: rig.oidc, Tester: rig.oidc, Secrets: rig.sec, Box: fakeBox{},
		Hash: BcryptHasher{Cost: bcrypt.MinCost}, Audit: rig.audit, Clock: rig.clock,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rig.svc = svc
	return rig
}

func (r *testRig) seedUser(t *testing.T, username, password, role string) *domain.User {
	t.Helper()
	h := ""
	if password != "" {
		sum, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		h = string(sum)
	}
	u := &domain.User{Username: username, Email: username + "@example.com", PasswordHash: h}
	if err := r.users.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		rl, err := r.users.GetRoleByName(context.Background(), role)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.users.GrantRole(context.Background(), u.ID, rl.ID, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

func (r *testRig) seedProvider(t *testing.T) *domain.OIDCProvider {
	t.Helper()
	p := &domain.OIDCProvider{
		Name: "test-idp", Issuer: "https://idp.example", ClientID: "cid",
		Enabled: true, JITProvisioning: true,
		GroupClaimNames:  []string{"groups"},
		GroupRoleMapping: map[string]string{"admins": domain.RoleAdministrator, "ops": domain.RoleOperator},
	}
	if err := r.provs.CreateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewServiceRequiresDeps(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Fatal("empty deps must fail")
	}
}

func TestLoginSuccess(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	res, err := r.svc.Login(context.Background(), "alice", "correct-horse-1", "10.0.0.1", "ua")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.Token == "" || res.Session == nil || res.User.Username != "alice" {
		t.Fatalf("bad result: %+v", res)
	}
	if len(res.Roles) != 1 || res.Roles[0].Name != domain.RoleOperator {
		t.Fatalf("roles: %+v", res.Roles)
	}
	if !r.audit.has("auth.login", "success") {
		t.Fatal("missing success audit event")
	}
	// Token hash (not the token) is what the store holds.
	if _, err := r.sess.GetByTokenHash(context.Background(), res.Token); err == nil {
		t.Fatal("raw token must not be usable as a hash lookup")
	}
	if _, err := r.sess.GetByTokenHash(context.Background(), HashSessionToken(res.Token)); err != nil {
		t.Fatalf("hash lookup: %v", err)
	}
}

func TestLoginFailures(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) *testRig {
		r := newRig(t)
		r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
		disabled := r.seedUser(t, "mallory", "correct-horse-1", domain.RoleOperator)
		disabled.Disabled = true
		if err := r.users.UpdateUser(ctx, disabled); err != nil {
			t.Fatal(err)
		}
		r.seedUser(t, "noroles", "correct-horse-1", "")
		return r
	}
	cases := []struct {
		name       string
		identifier string
		password   string
		wantErr    error
	}{
		{"bad password", "alice", "wrong", ErrInvalidCredentials},
		{"unknown user", "ghost", "whatever", ErrInvalidCredentials},
		{"unknown email", "ghost@example.com", "whatever", ErrInvalidCredentials},
		{"disabled account (right password)", "mallory", "correct-horse-1", ErrAccountDisabled},
		{"disabled account (wrong password)", "mallory", "wrong", ErrInvalidCredentials},
		{"no roles", "noroles", "correct-horse-1", ErrNoRolesAssigned},
		{"empty", "", "", ErrInvalidCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := setup(t)
			_, err := r.svc.Login(ctx, tc.identifier, tc.password, "1.1.1.1", "ua")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), tc.password) && tc.password != "" {
				t.Fatalf("error leaks password: %v", err)
			}
		})
	}
}

func TestLoginByEmailFallback(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	res, err := r.svc.Login(context.Background(), "ALICE@example.com", "correct-horse-1", "", "")
	if err != nil {
		t.Fatalf("email login: %v", err)
	}
	if res.User.Username != "alice" {
		t.Fatalf("user = %q", res.User.Username)
	}
}

func TestLocalLoginKillSwitch(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	// Refused without confirmation.
	if err := r.svc.SetLocalLoginEnabled(ctx, false, false, "admin"); !errors.Is(err, ErrNoBreakGlass) {
		t.Fatalf("unconfirmed disable err = %v", err)
	}
	// Refused when no OIDC path exists even with confirmation.
	if err := r.svc.SetLocalLoginEnabled(ctx, false, true, "admin"); !errors.Is(err, ErrNoBreakGlass) {
		t.Fatalf("no-provider disable err = %v", err)
	}
	r.seedProvider(t)
	if err := r.svc.SetLocalLoginEnabled(ctx, false, true, "admin"); err != nil {
		t.Fatalf("confirmed disable: %v", err)
	}
	if _, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", ""); !errors.Is(err, ErrLocalLoginDisabled) {
		t.Fatalf("login err = %v, want ErrLocalLoginDisabled", err)
	}
	if err := r.svc.SetLocalLoginEnabled(ctx, true, false, "admin"); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", ""); err != nil {
		t.Fatalf("login after re-enable: %v", err)
	}
}

func TestValidateSessionLifecycle(t *testing.T) {
	r := newRig(t)
	u := r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := r.svc.ValidateSession(ctx, res.Token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if info.User.ID != u.ID || info.Session.ID != res.Session.ID {
		t.Fatalf("info mismatch: %+v", info)
	}
	if got := r.sess.touches; got != 0 {
		t.Fatalf("fresh session must not be touched yet, touches=%d", got)
	}
	// Within idle window but past touch threshold: lazy touch fires once.
	r.clock.advance(2 * time.Minute)
	if _, err := r.svc.ValidateSession(ctx, res.Token); err != nil {
		t.Fatal(err)
	}
	if got := r.sess.touches; got != 1 {
		t.Fatalf("touches = %d, want 1", got)
	}
	// Past idle timeout: rejected and pruned.
	r.clock.advance(31 * time.Minute)
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("idle err = %v", err)
	}
	if _, err := r.sess.GetByTokenHash(ctx, HashSessionToken(res.Token)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("idle session not pruned: %v", err)
	}
}

func TestValidateSessionAbsoluteExpiry(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r.clock.advance(12*time.Hour + time.Minute)
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired err = %v", err)
	}
}

func TestValidateSessionRejectsUnknownAndMalformed(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for _, tok := range []string{"", "short", "does-not-exist-in-store-0123456789"} {
		if _, err := r.svc.ValidateSession(ctx, tok); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("token %q err = %v", tok, err)
		}
	}
}

func TestValidateSessionDisabledUser(t *testing.T) {
	r := newRig(t)
	u := r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	if err := r.users.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogout(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	url, err := r.svc.Logout(ctx, res.Token, "https://app/bye")
	if err != nil {
		t.Fatal(err)
	}
	if url != "" {
		t.Fatalf("local logout must not yield OIDC URL, got %q", url)
	}
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("session survives logout: %v", err)
	}
	// Unknown tokens logout silently.
	if _, err := r.svc.Logout(ctx, "unknown-token-0123456789abcdef", ""); err != nil {
		t.Fatalf("unknown logout: %v", err)
	}
	if !r.audit.has("auth.logout", "success") {
		t.Fatal("missing logout audit event")
	}
}
