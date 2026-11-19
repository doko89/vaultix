package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/adapter/crypto"
	platformhttp "github.com/vaultix/vaultix/internal/adapter/http/platform"
	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
)

func TestStorageSecretBoxRoundTrip(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	box, err := crypto.NewBox(key)
	require.NoError(t, err)

	s := &storageSecretBox{box: box}
	ctx := context.Background()
	ct, keyRef, err := s.Seal(ctx, []byte(`{"access_key_id":"AK","secret_access_key":"SK"}`))
	require.NoError(t, err)
	assert.Equal(t, "v1", keyRef)
	assert.NotContains(t, string(ct), "AK")

	plain, err := s.Open(ctx, ct, keyRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"access_key_id":"AK","secret_access_key":"SK"}`, string(plain))

	// Corrupt ciphertext fails closed without leaking material.
	_, err = s.Open(ctx, []byte("v1:!!!not-base64!!!"), keyRef)
	require.Error(t, err)
}

func TestProjectJobKind(t *testing.T) {
	assert.Equal(t, "filesystem", projectJobKind(domain.TargetHostFilesystem))
	assert.Equal(t, "docker-volume", projectJobKind(domain.TargetDockerVolume))
	assert.Equal(t, "docker-volume", projectJobKind(domain.TargetDockerConfig))
	assert.Equal(t, "postgres", projectJobKind(domain.TargetPostgres))
	assert.Equal(t, "mysql", projectJobKind(domain.TargetMySQL))
	// No agent-side counterpart: raw name so the agent rejects explicitly.
	assert.Equal(t, "mssql", projectJobKind(domain.TargetMSSQL))
	assert.Equal(t, "redis", projectJobKind(domain.TargetRedis))
	assert.Equal(t, "app_stack", projectJobKind(domain.TargetAppStack))
}

func TestAuthAuditAction(t *testing.T) {
	cases := []struct {
		action, result string
		want           domain.AuditAction
		target         string
	}{
		{"auth.login", "success", domain.AuditLogin, "user"},
		{"auth.login", "failed", domain.AuditLoginFailed, "user"},
		{"auth.login", "denied", domain.AuditLoginFailed, "user"},
		{"auth.logout", "success", domain.AuditLogout, "session"},
		{"auth.oidc.login", "success", domain.AuditLogin, "user"},
		{"auth.oidc.callback", "denied", domain.AuditLoginFailed, "oidc_provider"},
		{"auth.provider.create", "success", domain.AuditOIDCChange, "oidc_provider"},
		{"auth.provider.secret.rotate", "success", domain.AuditOIDCChange, "oidc_provider"},
		{"auth.identity.link", "success", domain.AuditOIDCLinkChange, "identity"},
		{"auth.local_login", "success", domain.AuditUserChange, ""},
	}
	for _, c := range cases {
		got, target := authAuditAction(c.action, c.result)
		assert.Equal(t, c.want, got, c.action)
		assert.Equal(t, c.target, target, c.action)
	}
}

func TestAuditOutcome(t *testing.T) {
	assert.Equal(t, audituc.OutcomeOK, auditOutcome("success"))
	assert.Equal(t, audituc.OutcomeDenied, auditOutcome("denied"))
	assert.Equal(t, audituc.OutcomeError, auditOutcome("failed"))
	assert.Equal(t, audituc.OutcomeError, auditOutcome("error"))
	assert.Equal(t, audituc.OutcomeError, auditOutcome("bogus"))
}

func TestIsUUID(t *testing.T) {
	assert.True(t, isUUID("123e4567-e89b-12d3-a456-426614174000"))
	assert.False(t, isUUID(""))
	assert.False(t, isUUID("anonymous"))
	assert.False(t, isUUID("admin"))
	assert.False(t, isUUID("not-a-uuid"))
}

func TestOIDCInputToAuth(t *testing.T) {
	jit := true
	in := platformhttp.OIDCProviderInput{
		Name: "corp", Issuer: "https://id.example", Enabled: true,
		ClientID: "cid", ClientSecret: "s3cret",
		Scopes: []string{"openid"}, GroupClaimNames: []string{"groups"},
		GroupRoleMapping: map[string]string{"eng": "operator"},
		DefaultRole:      "readonly", JITProvisioning: &jit,
	}
	out := oidcInputToAuth(in)
	assert.Equal(t, "corp", out.Name)
	assert.Equal(t, "https://id.example", out.Issuer)
	require.NotNil(t, out.Enabled)
	assert.True(t, *out.Enabled)
	assert.Equal(t, "cid", out.ClientID)
	assert.Equal(t, "s3cret", out.ClientSecret)
	assert.Equal(t, []string{"openid"}, out.Scopes)
	assert.Equal(t, map[string]string{"eng": "operator"}, out.GroupRoleMapping)
	assert.Equal(t, "readonly", out.DefaultRole)
	require.NotNil(t, out.JITProvisioning)
	assert.True(t, *out.JITProvisioning)

	// Disabled bit propagates (platform bool -> auth *bool).
	in.Enabled = false
	assert.False(t, *oidcInputToAuth(in).Enabled)
}

func TestSMTPConfigConversion(t *testing.T) {
	out := smtpConfig(SMTPConfig{
		Host: "smtp.example", Port: 587, From: "noreply@example.com",
		Auth: "plain", Username: "u", Password: "p", TLS: "starttls",
		DialTimeoutSecs: 5, IOTimeoutSecs: 20,
	})
	assert.Equal(t, "smtp.example", out.Host)
	assert.Equal(t, 587, out.Port)
	assert.Equal(t, "noreply@example.com", out.From)
	assert.Equal(t, "plain", out.Auth)
	assert.Equal(t, "u", out.Username)
	assert.Equal(t, "p", out.Password)
	assert.Equal(t, "starttls", out.TLS)
	assert.Equal(t, int64(5*1e9), int64(out.DialTimeout))
	assert.Equal(t, int64(20*1e9), int64(out.IOTimeout))
}
