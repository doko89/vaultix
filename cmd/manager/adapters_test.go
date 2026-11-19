package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/adapter/crypto"
	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
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

// ---------- DB credential issuance (F2) ----------

const testDBPassword = "s3cr3t-db-pw"

// sealDBSecret stores a database secret profile sealed with the rig's box.
func sealDBSecret(t *testing.T, r *protoRig, id, payload string) {
	t.Helper()
	ct, _, err := fakeBox{}.Seal(context.Background(), []byte(payload))
	require.NoError(t, err)
	r.store.secrets[id] = &domain.SecretProfile{
		ID: id, Name: id, Kind: domain.SecretKindDatabase, Ciphertext: ct, KeyRef: "v1",
	}
}

// dbTarget returns a postgres target carrying the given credential ref.
func dbTarget(ref string) domain.BackupPolicyTarget {
	return domain.BackupPolicyTarget{
		ID: "tdb", PolicyID: "p1", Type: domain.TargetPostgres,
		Selector: "appdb", CredentialRef: ref,
	}
}

// refusedJob loads the job and asserts the refusal contract: terminal
// failed, actionable secret-free message, refusal code.
func refusedJob(t *testing.T, r *protoRig, jobID string, wantSubstr ...string) *domain.BackupJob {
	t.Helper()
	stored, err := r.jobs.GetJob(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, stored.State, "refused poll items must fail, never dispatch")
	require.Equal(t, dbCredentialRefusedCode, stored.ErrorCode)
	for _, want := range wantSubstr {
		require.Contains(t, stored.ErrorMessage, want)
	}
	require.NotContains(t, stored.ErrorMessage, testDBPassword, "refusal reasons must never carry secret material")
	return stored
}

func TestPollDBCredentialIssuedOverTLS(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-1")}
	sealDBSecret(t, r, "db-1", `{"username":"vaultix","password":"`+testDBPassword+`","host":"db.internal","port":5433}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)

	opts := queued[0].Targets[0].Options
	require.Contains(t, opts, "db_credential")
	var cred struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
	}
	require.NoError(t, json.Unmarshal([]byte(opts["db_credential"]), &cred))
	assert.Equal(t, "vaultix", cred.Username)
	assert.Equal(t, testDBPassword, cred.Password)
	assert.Equal(t, "db.internal", cred.Host)
	assert.Equal(t, 5433, cred.Port)
	assert.NotContains(t, opts, "credential_ref", "the ref itself is not echoed; only the resolved credential ships")

	// The pinned version is never mutated by issuance.
	pinned := r.policies.versions["p1@3"].Targets[0].Options
	assert.NotContains(t, pinned, "db_credential")

	// Issuance is audited without secret material.
	require.Len(t, r.audit.entries, 1)
	entry := r.audit.entries[0]
	assert.Equal(t, dbCredentialAuditAction, entry.Action)
	assert.Equal(t, "db-1", entry.TargetID)
	assert.Contains(t, entry.Detail, "ag-1")
	assert.Contains(t, entry.Detail, "job-1")
	assert.NotContains(t, entry.Detail, testDBPassword)
	assert.NotContains(t, entry.Detail, "vaultix")
	assert.NotContains(t, string(entry.Action)+entry.TargetType+entry.TargetID, testDBPassword)
}

func TestPollDBCredentialMinimalShapeOmitsEmpties(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-min")}
	sealDBSecret(t, r, "db-min", `{"username":"u","password":"p"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	var wire map[string]any
	require.NoError(t, json.Unmarshal([]byte(queued[0].Targets[0].Options["db_credential"]), &wire))
	assert.Equal(t, map[string]any{"username": "u", "password": "p"}, wire)
}

func TestPollDBCredentialRefusedOverPlaintext(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-1")}
	sealDBSecret(t, r, "db-1", `{"username":"vaultix","password":"`+testDBPassword+`"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// Plaintext transport: the item is refused (failed), not silently
	// delivered for trust auth — and the poll itself stays healthy.
	queued, err := r.queue.Poll(context.Background(), "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued, "refused items must not dispatch")
	refusedJob(t, r, "job-1", "TLS", "db-1")
	require.Empty(t, r.audit.entries, "refused issuance audits nothing")
}

func TestPollDBCredentialMissingProfile(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-missing")}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedJob(t, r, "job-1", "db-missing", "unavailable")
	require.Empty(t, r.audit.entries)
}

func TestPollDBCredentialInvalidProfiles(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"not JSON", `not-json-at-all`, "not valid JSON"},
		{"missing password", `{"username":"u"}`, `"password"`},
		{"missing username", `{"password":"` + testDBPassword + `"}`, `"username"`},
		{"empty password", `{"username":"u","password":""}`, `"password"`},
		{"port out of range", `{"username":"u","password":"p","port":99999}`, "port"},
		{"port not a number", `{"username":"u","password":"p","port":"abc"}`, "port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newProtoRig(t)
			r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-bad")}
			sealDBSecret(t, r, "db-bad", tc.payload)
			r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

			tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
			queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
			require.NoError(t, err)
			require.Empty(t, queued)
			refusedJob(t, r, "job-1", "db-bad", tc.want)
			require.Empty(t, r.audit.entries)
		})
	}
}

func TestPollDBCredentialUndecryptable(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{dbTarget("db-rot")}
	// Ciphertext the box cannot open (wrong envelope prefix).
	r.store.secrets["db-rot"] = &domain.SecretProfile{
		ID: "db-rot", Name: "db-rot", Kind: domain.SecretKindDatabase,
		Ciphertext: []byte("v9:opaque"), KeyRef: "v9",
	}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedJob(t, r, "job-1", "db-rot", "undecryptable")
	require.Empty(t, r.audit.entries)
}

func TestPollDBCredentialMissingRef(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{{
		ID: "tdb", PolicyID: "p1", Type: domain.TargetPostgres, Selector: "appdb",
	}}
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedJob(t, r, "job-1", "credential-profile reference", "appdb")
	require.Empty(t, r.audit.entries)
}

func TestPollDBCredentialRefusalSkipsOnlyBadItem(t *testing.T) {
	r := newProtoRig(t)
	// p1@3 keeps the healthy filesystem target; p2@1 carries the bad DB ref.
	r.policies.policies["p2"] = &domain.BackupPolicy{ID: "p2", Enabled: true, CurrentVersion: 1}
	r.policies.versions["p2@1"] = &domain.BackupPolicyVersion{
		Version: 1, PolicyID: "p2", StorageID: "dest-1",
		Targets: []domain.BackupPolicyTarget{dbTarget("db-missing")},
	}
	r.seedJob(t, "job-good", "ag-1", domain.JobQueued)
	r.seedJob(t, "job-bad", "ag-1", domain.JobQueued)
	bad, err := r.jobs.GetJob(context.Background(), "job-bad")
	require.NoError(t, err)
	bad.PolicyID, bad.PolicyVersion = "p2", 1
	require.NoError(t, r.jobs.UpdateJob(context.Background(), bad))

	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1, "one refused item must not starve the queue")
	assert.Equal(t, "job-good", queued[0].ID)
	refusedJob(t, r, "job-bad", "db-missing")
}

func TestPollDBCredentialMySQLAndAuditFailure(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{{
		ID: "tdb", PolicyID: "p1", Type: domain.TargetMySQL,
		Selector: "shopdb", CredentialRef: "db-my",
	}}
	sealDBSecret(t, r, "db-my", `{"username":"backup","password":"`+testDBPassword+`","port":"3306"}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// A failing audit trail withholds the credential (fail closed).
	r.audit.fail = assert.AnError
	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued)
	refusedJob(t, r, "job-1", "audit")

	// String ports decode; a healthy audit issues for mysql too.
	r.audit.fail = nil
	r.seedJob(t, "job-2", "ag-1", domain.JobQueued)
	queued, err = r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	var cred struct {
		Username string `json:"username"`
		Port     int    `json:"port"`
	}
	require.NoError(t, json.Unmarshal([]byte(queued[0].Targets[0].Options["db_credential"]), &cred))
	assert.Equal(t, "backup", cred.Username)
	assert.Equal(t, 3306, cred.Port)
	require.Len(t, r.audit.entries, 1)
	assert.NotContains(t, r.audit.entries[0].Detail, testDBPassword)
}

func TestPollDBCredentialMSSQLIssuedOverTLSRefusedOverPlaintext(t *testing.T) {
	r := newProtoRig(t)
	r.policies.versions["p1@3"].Targets = []domain.BackupPolicyTarget{{
		ID: "tdb", PolicyID: "p1", Type: domain.TargetMSSQL,
		Selector: "salesdb", CredentialRef: "db-ms",
	}}
	sealDBSecret(t, r, "db-ms", `{"username":"sa_app","password":"`+testDBPassword+`","host":"mssql.internal","port":1433}`)
	r.seedJob(t, "job-1", "ag-1", domain.JobQueued)

	// Secure channel: same shape as postgres/mysql.
	tlsCtx := agentshttp.ContextWithTransportSecure(context.Background(), true)
	queued, err := r.queue.Poll(tlsCtx, "ag-1", 10)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	opts := queued[0].Targets[0].Options
	require.Contains(t, opts, "db_credential")
	var cred struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
	}
	require.NoError(t, json.Unmarshal([]byte(opts["db_credential"]), &cred))
	assert.Equal(t, "sa_app", cred.Username)
	assert.Equal(t, testDBPassword, cred.Password)
	assert.Equal(t, "mssql.internal", cred.Host)
	assert.Equal(t, 1433, cred.Port)
	assert.NotContains(t, opts, "credential_ref")
	require.Len(t, r.audit.entries, 1)
	assert.Equal(t, dbCredentialAuditAction, r.audit.entries[0].Action)
	assert.NotContains(t, r.audit.entries[0].Detail, testDBPassword)

	// Plaintext channel: refused (failed), never dispatched.
	r.seedJob(t, "job-2", "ag-1", domain.JobQueued)
	queued, err = r.queue.Poll(context.Background(), "ag-1", 10)
	require.NoError(t, err)
	require.Empty(t, queued, "refused items must not dispatch")
	refusedJob(t, r, "job-2", "TLS", "db-ms")
	require.Len(t, r.audit.entries, 1, "refused issuance audits nothing")
}

func TestParseDBCredentialUnits(t *testing.T) {
	// Padded identifiers trim; passwords stay verbatim.
	cred, err := parseDBCredential([]byte(`{"username":"  u ","password":"  p  ","host":" h "}`))
	require.NoError(t, err)
	assert.Equal(t, "u", cred.Username)
	assert.Equal(t, "  p  ", cred.Password)
	assert.Equal(t, "h", cred.Host)
	assert.Zero(t, cred.Port)

	// Empty object names the first missing required field.
	_, err = parseDBCredential([]byte(`{}`))
	require.ErrorContains(t, err, `"username"`)

	// Fractional ports are rejected, not truncated.
	_, err = parseDBCredential([]byte(`{"username":"u","password":"p","port":5432.5}`))
	require.ErrorContains(t, err, "port")

	// Error text never echoes values.
	_, err = parseDBCredential([]byte(`{"username":"u","password":"` + testDBPassword + `","port":true}`))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testDBPassword)
	assert.True(t, strings.Contains(err.Error(), "port"), "got %q", err.Error())
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
