package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

func TestStartOIDCLogin(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if start.AuthURL == "" || start.State == "" {
		t.Fatalf("bad start: %+v", start)
	}
	if r.svc.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", r.svc.PendingCount())
	}
	if r.oidc.lastState != start.State || r.oidc.lastNonce == "" {
		t.Fatalf("state/nonce not propagated: %+v", r.oidc)
	}
}

func TestStartOIDCLoginDisabledProvider(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	p.Enabled = false
	if err := r.provs.UpdateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.StartOIDCLogin(context.Background(), p.ID, "https://app/cb"); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.svc.StartOIDCLogin(context.Background(), "missing", "https://app/cb"); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestStartOIDCDiscoveryFailure(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	r.oidc.discoverErr = errors.New("idp down")
	if _, err := r.svc.StartOIDCLogin(context.Background(), p.ID, "https://app/cb"); err == nil {
		t.Fatal("expected discovery failure")
	}
	if r.svc.PendingCount() != 0 {
		t.Fatal("failed start must not leave pending state")
	}
}

// completeFlow runs start→callback with scripted claims.
func completeFlow(t *testing.T, r *testRig, p *domain.OIDCProvider, claims *domain.OIDCClaims) (*LoginResult, error) {
	t.Helper()
	ctx := context.Background()
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.oidc.claims = claims
	return r.svc.CompleteOIDCLogin(ctx, p.ID, start.State, "code-1", "https://app/callback", "10.0.0.2", "ua")
}

func TestCompleteOIDCJITProvisioning(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "sub-1", Issuer: p.Issuer, Email: "oidc.alice@example.com",
		Name: "OIDC Alice", Groups: []string{"ops"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.User.Email != "oidc.alice@example.com" || res.Token == "" {
		t.Fatalf("bad result: %+v", res.User)
	}
	if !res.User.LocalLoginDisabled {
		t.Fatal("JIT user must be SSO-only by default")
	}
	if res.User.PasswordHash != "" {
		t.Fatal("JIT user must have no password hash")
	}
	if len(res.Roles) != 1 || res.Roles[0].Name != domain.RoleOperator {
		t.Fatalf("roles: %+v", res.Roles)
	}
	if res.OIDCProviderID != p.ID {
		t.Fatalf("provider = %q", res.OIDCProviderID)
	}
	// Identity link persisted.
	idents, err := r.svc.ListUserIdentities(context.Background(), res.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idents) != 1 || idents[0].Subject != "sub-1" {
		t.Fatalf("idents: %+v", idents)
	}
	if !r.audit.has("auth.oidc.login", "success") {
		t.Fatal("missing OIDC success audit")
	}
}

func TestCompleteOIDCClaimRefresh(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	claims := &domain.OIDCClaims{Subject: "sub-1", Issuer: p.Issuer, Email: "a@example.com", Groups: []string{"admins"}}
	res, err := completeFlow(t, r, p, claims)
	if err != nil {
		t.Fatal(err)
	}
	if res.Roles[0].Name != domain.RoleAdministrator {
		t.Fatalf("roles: %+v", res.Roles)
	}
	// IdP removes the user from admins: next login demotes to operator.
	claims.Groups = []string{"ops"}
	res2, err := completeFlow(t, r, p, claims)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Roles[0].Name != domain.RoleOperator {
		t.Fatalf("roles after refresh: %+v", res2.Roles)
	}
	if res2.User.ID != res.User.ID {
		t.Fatal("second login must reuse the provisioned user")
	}
	stored, err := r.users.ListUserRoles(ctx, res.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Name != domain.RoleOperator {
		t.Fatalf("stored roles: %+v", stored)
	}
}

func TestCompleteOIDCDefaultRole(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	p.DefaultRole = domain.RoleReadOnly
	if err := r.provs.UpdateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "sub-9", Issuer: p.Issuer, Email: "b@example.com", Groups: []string{"unmapped"},
	})
	if err != nil {
		t.Fatalf("default-role login: %v", err)
	}
	if res.Roles[0].Name != domain.RoleReadOnly {
		t.Fatalf("roles: %+v", res.Roles)
	}
}

func TestCompleteOIDCDenyNoMappedRole(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t) // no DefaultRole
	_, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "sub-9", Issuer: p.Issuer, Email: "b@example.com", Groups: []string{"unmapped"},
	})
	if !errors.Is(err, ErrNoMappedRole) {
		t.Fatalf("err = %v, want ErrNoMappedRole", err)
	}
	if !r.audit.has("auth.oidc.login", "denied") {
		t.Fatal("no-mapped-role deny must emit an audit event")
	}
}

func TestCompleteOIDCPreprovisionedOnly(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	p.JITProvisioning = false
	if err := r.provs.UpdateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "stranger", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"ops"},
	})
	if !errors.Is(err, ErrPreprovisionedOnly) {
		t.Fatalf("err = %v, want ErrPreprovisionedOnly", err)
	}
	// Pre-linked identity may log in.
	u := r.seedUser(t, "linked", "", domain.RoleOperator)
	if _, err := r.svc.LinkIdentity(context.Background(), u.ID, p.ID, p.Issuer, "stranger", "s@example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "stranger", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"ops"},
	})
	if err != nil {
		t.Fatalf("linked login: %v", err)
	}
	if res.User.ID != u.ID {
		t.Fatal("must reuse the pre-provisioned user")
	}
}

func TestCompleteOIDCStateSingleUseAndMismatch(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	r.oidc.claims = &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"ops"}}
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatal(err)
	}
	// Wrong redirect URI: rejected, state consumed.
	if _, err := r.svc.CompleteOIDCLogin(ctx, p.ID, start.State, "c", "https://evil/cb", "", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("redirect mismatch err = %v", err)
	}
	// Replay of consumed state: rejected.
	if _, err := r.svc.CompleteOIDCLogin(ctx, p.ID, start.State, "c", "https://app/callback", "", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replay err = %v", err)
	}
	// Unknown state: rejected.
	if _, err := r.svc.CompleteOIDCLogin(ctx, p.ID, "bogus", "c", "https://app/callback", "", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bogus err = %v", err)
	}
}

func TestCompleteOIDCPendingExpiry(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatal(err)
	}
	r.clock.advance(6 * time.Minute)
	r.oidc.claims = &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer, Email: "s@example.com"}
	if _, err := r.svc.CompleteOIDCLogin(ctx, p.ID, start.State, "c", "https://app/callback", "", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expired err = %v", err)
	}
}

func TestCompleteOIDCDisabledIdentity(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	u := r.seedUser(t, "linked", "", domain.RoleOperator)
	ident, err := r.svc.LinkIdentity(ctx, u.ID, p.ID, p.Issuer, "sub-x", "x@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetIdentityDisabled(ctx, u.ID, ident.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}
	_, err = completeFlow(t, r, p, &domain.OIDCClaims{Subject: "sub-x", Issuer: p.Issuer, Email: "x@example.com", Groups: []string{"ops"}})
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("err = %v, want ErrAccountDisabled", err)
	}
}

func TestCompleteOIDCExchangeError(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	r.oidc.exchErr = errors.New("expired code")
	_, err := completeFlow(t, r, p, &domain.OIDCClaims{})
	if err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Fatalf("err = %v, want exchange failure", err)
	}
	if !r.audit.has("auth.oidc.callback", "failed") {
		t.Fatal("exchange failure must emit an audit event")
	}
}

func TestCompleteOIDCUnmappedRoleName(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	p.GroupRoleMapping = map[string]string{"g": "nonexistent-role"}
	if err := r.provs.UpdateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, err := completeFlow(t, r, p, &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"g"}})
	if err == nil || !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("err = %v", err)
	}
}

func TestOIDCLogoutURL(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	r.oidc.logoutURL = "https://idp.example/logout"
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"ops"}})
	if err != nil {
		t.Fatal(err)
	}
	url, err := r.svc.Logout(context.Background(), res.Token, "https://app/bye")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "https://idp.example/logout") {
		t.Fatalf("logout URL = %q", url)
	}
}

func TestJITUsernameDisambiguation(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	r.seedUser(t, "sam", "x", domain.RoleOperator)
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{Subject: "s2", Issuer: p.Issuer, Email: "sam@example.com", Groups: []string{"ops"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.User.Username == "sam" {
		t.Fatal("JIT username must disambiguate against existing users")
	}
}
