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

var errBoom = errors.New("boom")

type failAudit struct{}

func (failAudit) RecordAuthEvent(context.Context, AuditEvent) error { return errBoom }

type failBox struct{}

func (failBox) Encrypt([]byte) (string, error) { return "", errBoom }
func (failBox) Decrypt(string) ([]byte, error) { return nil, errBoom }

func TestLoginRepoErrors(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()

	r.users.lookupErr = errBoom
	if _, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", ""); !errors.Is(err, errBoom) {
		t.Fatalf("lookup err = %v", err)
	}
	r.users.lookupErr = nil

	r.users.listErr = errBoom
	if _, err := r.svc.Login(ctx, "ghost@example.com", "x", "", ""); !errors.Is(err, errBoom) {
		t.Fatalf("list err = %v", err)
	}
	r.users.listErr = nil

	r.users.rolesErr = errBoom
	if _, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", ""); !errors.Is(err, errBoom) {
		t.Fatalf("roles err = %v", err)
	}
	r.users.rolesErr = nil

	r.sess.createErr = errBoom
	if _, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", ""); !errors.Is(err, errBoom) {
		t.Fatalf("create err = %v", err)
	}
}

func TestValidateSessionStoreErrors(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r.sess.getErr = errBoom
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, errBoom) {
		t.Fatalf("get err = %v", err)
	}
	r.sess.getErr = nil

	r.clock.advance(2 * time.Minute)
	r.sess.touchErr = errBoom
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, errBoom) {
		t.Fatalf("touch err = %v", err)
	}
	r.sess.touchErr = nil

	// User row deleted after login: session no longer validates.
	delete(r.users.users, res.User.ID)
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deleted-user err = %v", err)
	}
}

func TestValidateSessionRolesError(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r.users.rolesErr = errBoom
	if _, err := r.svc.ValidateSession(ctx, res.Token); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogoutStoreErrors(t *testing.T) {
	r := newRig(t)
	r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	res, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r.sess.getErr = errBoom
	if _, err := r.svc.Logout(ctx, res.Token, ""); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogoutMissingProvider(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "s", Issuer: p.Issuer, Email: "s@example.com", Groups: []string{"ops"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(r.provs.provs, p.ID) // provider removed after login
	url, err := r.svc.Logout(context.Background(), res.Token, "https://app/bye")
	if err != nil || url != "" {
		t.Fatalf("url=%q err=%v", url, err)
	}
}

func TestRevokeUserSessions(t *testing.T) {
	r := newRig(t)
	u := r.seedUser(t, "alice", "correct-horse-1", domain.RoleOperator)
	ctx := context.Background()
	a, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Login(ctx, "alice", "correct-horse-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RevokeUserSessions(ctx, u.ID, a.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ValidateSession(ctx, a.Token); err != nil {
		t.Fatalf("kept session: %v", err)
	}
	if _, err := r.svc.ValidateSession(ctx, b.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked session survives: %v", err)
	}
}

func TestAuditFailurePropagatesOnAdminOps(t *testing.T) {
	r := newRig(t)
	p, err := r.svc.CreateProvider(context.Background(), ProviderInput{
		Name: "x", Issuer: "https://x.example", ClientID: "c",
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	// Swap in a failing auditor and retry an audited admin op.
	r.svc.audit = failAudit{}
	if err := r.svc.SetProviderEnabled(context.Background(), p.ID, false, "admin"); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want audit failure", err)
	}
}

func TestProviderSecretDecryptFailure(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	if err := r.sec.SetProviderSecret(ctx, p.ID, "not-an-envelope"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.ProviderSecret(ctx, p.ID); err == nil {
		t.Fatal("corrupt ciphertext must fail")
	}
}

func TestStoreSecretBoxFailure(t *testing.T) {
	r := newRig(t)
	r.svc.box = failBox{}
	_, err := r.svc.CreateProvider(context.Background(), ProviderInput{
		Name: "x", Issuer: "https://x.example", ClientID: "c", ClientSecret: "s",
	}, "admin")
	if err == nil || !strings.Contains(err.Error(), "encryption failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestTestProviderPaths(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.svc.TestProvider(ctx, "missing", "admin"); err == nil {
		t.Fatal("missing provider must fail")
	}
	// Service without a Tester reports unavailability.
	bare, err := NewService(Deps{
		Users: r.users, Providers: r.provs, Sessions: r.sess, OIDC: r.oidc,
		Secrets: r.sec, Box: fakeBox{}, Hash: BcryptHasher{Cost: 4}, Clock: r.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := r.seedProvider(t)
	if _, err := bare.TestProvider(ctx, p.ID, "admin"); err == nil {
		t.Fatal("nil tester must report unavailability")
	}
}

func TestLinkIdentityValidation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	u := r.seedUser(t, "alice", "x", domain.RoleOperator)
	p := r.seedProvider(t)
	if _, err := r.svc.LinkIdentity(ctx, u.ID, p.ID, "", "s", "", "a"); err == nil {
		t.Fatal("empty issuer must fail")
	}
	if _, err := r.svc.LinkIdentity(ctx, "missing", p.ID, p.Issuer, "s", "", "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing user err = %v", err)
	}
	if _, err := r.svc.LinkIdentity(ctx, u.ID, "missing", p.Issuer, "s", "", "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing provider err = %v", err)
	}
	if _, err := r.svc.ListUserIdentities(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list missing user err = %v", err)
	}
}

func TestSetProviderEnabledPaths(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.svc.SetProviderEnabled(ctx, "missing", true, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	p := r.seedProvider(t)
	r.provs.listErr = errBoom
	if err := r.svc.SetProviderEnabled(ctx, p.ID, false, "a"); !errors.Is(err, errBoom) {
		t.Fatalf("list err = %v", err)
	}
}

func TestSetLocalLoginListError(t *testing.T) {
	r := newRig(t)
	r.provs.listErr = errBoom
	if err := r.svc.SetLocalLoginEnabled(context.Background(), false, true, "a"); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestRotateSecretPaths(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.svc.RotateProviderSecret(ctx, "x", "", "a"); err == nil {
		t.Fatal("empty secret must fail")
	}
	if err := r.svc.RotateProviderSecret(ctx, "missing", "s", "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestUpdateProviderPaths(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.svc.UpdateProvider(ctx, "missing", ProviderInput{Name: "x"}, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	p := r.seedProvider(t)
	if _, err := r.svc.UpdateProvider(ctx, p.ID, ProviderInput{Name: ""}, "a"); err == nil {
		t.Fatal("empty name must fail validation")
	}
	if _, err := r.svc.UpdateProvider(ctx, p.ID, ProviderInput{Name: "n", Issuer: p.Issuer, ClientID: "c", DefaultRole: "nope"}, "a"); err == nil {
		t.Fatal("unknown default role must fail")
	}
}

func TestStartOIDCEdgeCases(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	if _, err := r.svc.StartOIDCLogin(ctx, p.ID, ""); err == nil {
		t.Fatal("empty redirect must fail")
	}
	r.oidc.authErr = errBoom
	if _, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/cb"); !errors.Is(err, errBoom) {
		t.Fatalf("authURL err = %v", err)
	}
	if !r.audit.has("auth.oidc.start", "error") {
		t.Fatal("start failure must audit")
	}
}

func TestCompleteOIDCProviderDisabledAfterStart(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	ctx := context.Background()
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatal(err)
	}
	p.Enabled = false
	if err := r.provs.UpdateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	r.oidc.claims = &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer}
	if _, err := r.svc.CompleteOIDCLogin(ctx, p.ID, start.State, "c", "https://app/callback", "", ""); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteOIDCProviderMismatch(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	other := &domain.OIDCProvider{Name: "o", Issuer: "https://o.example", ClientID: "c", Enabled: true}
	if err := r.provs.CreateProvider(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start, err := r.svc.StartOIDCLogin(ctx, p.ID, "https://app/callback")
	if err != nil {
		t.Fatal(err)
	}
	r.oidc.claims = &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer}
	if _, err := r.svc.CompleteOIDCLogin(ctx, other.ID, start.State, "c", "https://app/callback", "", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err = %v", err)
	}
}

func TestJITRequiresEmail(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	_, err := completeFlow(t, r, p, &domain.OIDCClaims{Subject: "s", Issuer: p.Issuer, Groups: []string{"ops"}})
	if !errors.Is(err, ErrPreprovisionedOnly) {
		t.Fatalf("err = %v", err)
	}
}

func TestJITUsernameFallback(t *testing.T) {
	r := newRig(t)
	p := r.seedProvider(t)
	// Email with no usable username runes falls back to oidc-user.
	res, err := completeFlow(t, r, p, &domain.OIDCClaims{
		Subject: "s", Issuer: p.Issuer, Email: "@", Groups: []string{"ops"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User.Username != "oidc-user" {
		t.Fatalf("username = %q", res.User.Username)
	}
}

func TestBcryptDefaultCost(t *testing.T) {
	h := BcryptHasher{}
	if h.cost() != bcrypt.DefaultCost {
		t.Fatalf("default cost = %d", h.cost())
	}
	sum, err := BcryptHasher{Cost: 4}.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := (BcryptHasher{}).Verify(sum, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := (BcryptHasher{}).Verify("", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty verify err = %v", err)
	}
}
