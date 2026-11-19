package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/vaultix/vaultix/internal/domain"
)

func TestBcryptHasher(t *testing.T) {
	h := BcryptHasher{Cost: bcrypt.MinCost}
	sum, err := h.Hash("s3cret-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if sum == "s3cret-password" || !strings.HasPrefix(sum, "$2a$") {
		t.Fatalf("not a bcrypt hash: %q", sum)
	}
	if err := h.Verify(sum, "s3cret-password"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := h.Verify(sum, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if err := h.Verify("not-a-hash", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad hash err = %v", err)
	}
	if _, err := h.Hash(""); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("empty password err = %v", err)
	}
	// Same password => different hashes (salt).
	other, _ := h.Hash("s3cret-password")
	if sum == other {
		t.Fatal("bcrypt must salt hashes")
	}
}

func TestSessionTokenHelpers(t *testing.T) {
	a, err := GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || len(a) != 43 {
		t.Fatalf("tokens: %q %q", a, b)
	}
	h1, h2 := HashSessionToken(a), HashSessionToken(a)
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("hash unstable: %q", h1)
	}
	if h1 == HashSessionToken(b) {
		t.Fatal("distinct tokens must hash distinctly")
	}
	if strings.Contains(h1, a) {
		t.Fatal("hash must not embed the token")
	}
	if !EqualTokenHash(h1, h2) || EqualTokenHash(h1, HashSessionToken(b)) {
		t.Fatal("EqualTokenHash broken")
	}
}

func TestPKCEHelpers(t *testing.T) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 43 || challenge == "" || verifier == challenge {
		t.Fatalf("verifier=%q challenge=%q", verifier, challenge)
	}
	if got := ChallengeS256(verifier); got != challenge {
		t.Fatalf("ChallengeS256 = %q, want %q", got, challenge)
	}
	// RFC 7636 appendix B vector.
	const vecVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	if got := ChallengeS256(vecVerifier); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("RFC vector mismatch: %q", got)
	}
}

func TestStateNonceHelpers(t *testing.T) {
	s1, err := GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	n1, err := GenerateNonce()
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 || s1 == "" || n1 == "" {
		t.Fatalf("state/nonce: %q %q %q", s1, s2, n1)
	}
	if len(s1) != 22 || len(n1) != 22 { // 128-bit base64url
		t.Fatalf("lengths: %d %d", len(s1), len(n1))
	}
}

func TestProvidersCRUDAndSecrets(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tru := true
	created, err := r.svc.CreateProvider(ctx, ProviderInput{
		Name: "okta", Issuer: "https://okta.example", ClientID: "cid",
		ClientSecret:    "shh-its-secret",
		GroupClaimNames: []string{"groups"},
		GroupRoleMapping: map[string]string{
			"a": "administrator",
		},
		JITProvisioning: &tru,
	}, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || !created.Enabled || !created.JITProvisioning {
		t.Fatalf("created: %+v", created)
	}
	// Secret is encrypted at rest, never on the entity.
	raw, err := r.sec.GetProviderSecret(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "shh-its-secret" || !strings.HasPrefix(raw, "enc:") {
		t.Fatalf("secret not enveloped: %q", raw)
	}
	got, err := r.svc.ProviderSecret(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "shh-its-secret" {
		t.Fatalf("decrypted = %q", got)
	}
	// Reads never echo the secret.
	fetched, err := r.svc.GetProvider(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fetched.ClientSecretRef, "shh") {
		t.Fatalf("ref leaks secret: %q", fetched.ClientSecretRef)
	}
	listed, err := r.svc.ListProviders(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	// Unknown role in mapping is rejected.
	if _, err := r.svc.CreateProvider(ctx, ProviderInput{
		Name: "bad", Issuer: "https://bad.example", ClientID: "c",
		GroupRoleMapping: map[string]string{"g": "nope"},
	}, "admin"); err == nil {
		t.Fatal("unknown mapped role must fail")
	}
	// Rotation.
	if err := r.svc.RotateProviderSecret(ctx, created.ID, "rotated-secret", "admin"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.ProviderSecret(ctx, created.ID); got != "rotated-secret" {
		t.Fatalf("rotated = %q", got)
	}
	// Update without secret keeps the stored one.
	updated, err := r.svc.UpdateProvider(ctx, created.ID, ProviderInput{
		Name: "okta", Issuer: "https://okta.example", ClientID: "cid",
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "okta" {
		t.Fatalf("updated: %+v", updated)
	}
	if got, _ := r.svc.ProviderSecret(ctx, created.ID); got != "rotated-secret" {
		t.Fatalf("kept = %q", got)
	}
	// Test (audited, read-only).
	rep, err := r.svc.TestProvider(ctx, created.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK {
		t.Fatalf("report: %+v", rep)
	}
	if !r.audit.has("auth.provider.test", "success") {
		t.Fatal("missing test audit event")
	}
}

func TestSetProviderEnabledLockoutGuard(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	p, err := r.svc.CreateProvider(ctx, ProviderInput{Name: "only", Issuer: "https://only.example", ClientID: "c"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetLocalLoginEnabled(ctx, false, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetProviderEnabled(ctx, p.ID, false, "admin"); !errors.Is(err, ErrNoBreakGlass) {
		t.Fatalf("last-path disable err = %v", err)
	}
}

func TestIdentityAdminOps(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	u := r.seedUser(t, "alice", "x", "operator")
	p := r.seedProvider(t)
	ident, err := r.svc.LinkIdentity(ctx, u.ID, p.ID, p.Issuer, "sub-1", "a@example.com", "admin")
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	// Idempotent re-link to the same user.
	same, err := r.svc.LinkIdentity(ctx, u.ID, p.ID, p.Issuer, "sub-1", "a@example.com", "admin")
	if err != nil || same.ID != ident.ID {
		t.Fatalf("re-link: %+v %v", same, err)
	}
	// Conflict when linked to another user.
	other := r.seedUser(t, "bob", "x", "operator")
	if _, err := r.svc.LinkIdentity(ctx, other.ID, p.ID, p.Issuer, "sub-1", "b@example.com", "admin"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflict err = %v", err)
	}
	// Disable/enable round-trip.
	if err := r.svc.SetIdentityDisabled(ctx, u.ID, ident.ID, true, "admin"); err != nil {
		t.Fatal(err)
	}
	got, err := r.provs.GetIdentity(ctx, p.Issuer, "sub-1")
	if err != nil || !got.Disabled {
		t.Fatalf("disabled: %+v %v", got, err)
	}
	if err := r.svc.SetIdentityDisabled(ctx, u.ID, ident.ID, false, "admin"); err != nil {
		t.Fatal(err)
	}
	// Cross-user operations are rejected.
	if err := r.svc.SetIdentityDisabled(ctx, other.ID, ident.ID, true, "admin"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-user disable err = %v", err)
	}
	if err := r.svc.UnlinkIdentity(ctx, other.ID, ident.ID, "admin"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-user unlink err = %v", err)
	}
	// Unlink.
	if err := r.svc.UnlinkIdentity(ctx, u.ID, ident.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.provs.GetIdentity(ctx, p.Issuer, "sub-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("identity survives unlink: %v", err)
	}
	if !r.audit.has("auth.identity.link", "success") || !r.audit.has("auth.identity.unlink", "success") {
		t.Fatal("missing identity audit events")
	}
}

func TestAuthRedaction(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_, err := r.svc.CreateProvider(ctx, ProviderInput{
		Name: "s", Issuer: "https://s.example", ClientID: "c", ClientSecret: "ULTRA-SECRET-1",
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	r.seedUser(t, "alice", "alice-password-9", "operator")
	res, err := r.svc.Login(ctx, "alice", "alice-password-9", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var haystack strings.Builder
	for _, e := range r.audit.events {
		haystack.WriteString(e.Action + e.Actor + e.Target + e.IP + e.Result + e.Detail)
	}
	hay := haystack.String()
	for _, secret := range []string{"ULTRA-SECRET-1", "alice-password-9", res.Token, res.Session.TokenHash} {
		if secret != "" && strings.Contains(hay, secret) {
			t.Fatalf("audit trail leaks %q", secret)
		}
	}
	// Missing secrets select the public-client path without error material.
	if _, err := r.svc.ProviderSecret(ctx, "missing"); err != nil {
		t.Fatalf("missing secret should select public-client path, got %v", err)
	}
}
