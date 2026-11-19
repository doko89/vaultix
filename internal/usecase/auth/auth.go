// Secrets (passwords, client secrets, tokens, ID tokens) must never appear
// in errors, audit details, or logs produced here; tests assert redaction.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Sentinel errors. Messages are generic on purpose (no user enumeration,
// no secret material); details go to audit events, never to callers.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrAccountDisabled    = errors.New("auth: account disabled")
	ErrLocalLoginDisabled = errors.New("auth: local login is disabled")
	ErrNoRolesAssigned    = errors.New("auth: no roles assigned")
	ErrNoMappedRole       = errors.New("auth: no mapped role for OIDC groups")
	ErrPreprovisionedOnly = errors.New("auth: OIDC login requires a pre-provisioned account")
	ErrInvalidState       = errors.New("auth: invalid or expired login state")
	ErrProviderDisabled   = errors.New("auth: OIDC provider is disabled")
	ErrNoBreakGlass       = errors.New("auth: operation would remove the last local administrator path")
	ErrWeakPassword       = errors.New("auth: password does not meet policy")
)

// Config tunes the auth service. Zero values select documented defaults.
type Config struct {
	// SessionAbsoluteTTL caps session lifetime (default 12h).
	SessionAbsoluteTTL time.Duration
	// SessionIdleTimeout expires idle sessions (default 30m).
	SessionIdleTimeout time.Duration
	// TouchThreshold skips session-store writes until LastSeenAt is this
	// old (default 1m); bounds write amplification on ValidateSession.
	TouchThreshold time.Duration
	// PendingLoginTTL caps OIDC state/nonce lifetime (default 5m).
	PendingLoginTTL time.Duration
	// LocalLoginEnabled gates password login (default true). Disabling
	// requires explicit confirmation via SetLocalLoginEnabled.
	LocalLoginEnabled *bool
	// MinPasswordLength gates admin-set passwords (default 8).
	MinPasswordLength int
}

func (c Config) withDefaults() Config {
	if c.SessionAbsoluteTTL <= 0 {
		c.SessionAbsoluteTTL = domain.DefaultSessionAbsoluteTTL
	}
	if c.SessionIdleTimeout <= 0 {
		c.SessionIdleTimeout = domain.DefaultSessionIdleTimeout
	}
	if c.TouchThreshold <= 0 {
		c.TouchThreshold = time.Minute
	}
	if c.PendingLoginTTL <= 0 {
		c.PendingLoginTTL = 5 * time.Minute
	}
	if c.LocalLoginEnabled == nil {
		t := true
		c.LocalLoginEnabled = &t
	}
	if c.MinPasswordLength <= 0 {
		c.MinPasswordLength = 8
	}
	return c
}

// Hasher hashes and verifies passwords (bcrypt in production).
type Hasher interface {
	Hash(password string) (string, error)
	Verify(hash, password string) error
}

// SecretBox encrypts provider secrets at rest (adapter/crypto.Box).
type SecretBox interface {
	Encrypt(plaintext []byte) (string, error)
	Decrypt(envelope string) ([]byte, error)
}

// ProviderSecretStore persists encrypted provider secrets, keyed by
// provider ID.
//
// INTEGRATION NOTE (repo track): implement on
// oidc_providers.client_secret_encrypted via db/queries/oidc.sql
// (GetProviderSecret exists; add Set/Delete statements through sqlc).
type ProviderSecretStore interface {
	GetProviderSecret(ctx context.Context, providerID string) (string, error)
	SetProviderSecret(ctx context.Context, providerID, ciphertext string) error
	DeleteProviderSecret(ctx context.Context, providerID string) error
}

// ProviderTester runs discovery + JWKS checks (adapter/oidc.Client).
type ProviderTester interface {
	TestProvider(ctx context.Context, p *domain.OIDCProvider) (*ProviderTestReport, error)
}

// ProviderDiscoverer warms endpoint discovery before AuthURL.
// domain.OIDCClient does not include it; adapters may implement it
// optionally (adapter/oidc.Client does via DiscoverProvider).
type ProviderDiscoverer interface {
	DiscoverProvider(ctx context.Context, p *domain.OIDCProvider) error
}

// ProviderTestReport is the admin-facing provider self-test result.
// It never carries secrets.
type ProviderTestReport struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error,omitempty"`
	Issuer           string `json:"issuer,omitempty"`
	AuthorizationURL string `json:"authorization_url,omitempty"`
	TokenURL         string `json:"token_url,omitempty"`
	JWKSURL          string `json:"jwks_url,omitempty"`
	HasUserInfo      bool   `json:"has_userinfo"`
	HasEndSession    bool   `json:"has_end_session"`
	KeyCount         int    `json:"key_count"`
}

// AuditEvent is a security-relevant auth event. Detail must never contain
// secrets, passwords, tokens, or raw claims.
type AuditEvent struct {
	At     time.Time
	Action string // e.g. "auth.login", "auth.oidc.login", "auth.provider.test"
	Actor  string // actor user ID, username, or "anonymous"
	Target string // affected user/provider/identity ID or name
	IP     string
	Result string // "success" | "denied" | "failed" | "error"
	Detail string // short reason class, e.g. "bad_password", "no_mapped_role"
}

// Auditor records security events.
//
// INTEGRATION NOTE (platform track): implement by appending to the audit
// log (domain.AuditRepository via the audit usecase). It must redact
// defensively and never persist secrets.
type Auditor interface {
	RecordAuthEvent(ctx context.Context, e AuditEvent) error
}

// Deps wires the Service. All fields are required except Audit (nil
// disables auditing — tests only, never production).
type Deps struct {
	Users     domain.UserRepository
	Providers domain.OIDCProviderRepository
	Sessions  domain.SessionStore
	OIDC      domain.OIDCClient
	Tester    ProviderTester
	Secrets   ProviderSecretStore
	Box       SecretBox
	Hash      Hasher
	Audit     Auditor
	Clock     domain.Clock
	Config    Config
}

// LoginResult is returned for successful local and OIDC logins. Token is
// the raw opaque session token: handle once (set-cookie), never log.
type LoginResult struct {
	Token          string
	Session        *domain.Session
	User           *domain.User
	Roles          []domain.Role
	OIDCProviderID string
}

// SessionInfo is the validated-session view used by middleware.
type SessionInfo struct {
	Session *domain.Session
	User    *domain.User
	Roles   []domain.Role
}

// OIDCStart begins an OIDC login: redirect the browser to AuthURL.
type OIDCStart struct {
	AuthURL string
	State   string
}

// ProviderInput carries admin provider writes. ClientSecret is write-only:
// it is encrypted immediately and never echoed back.
type ProviderInput struct {
	Name             string
	Issuer           string
	DiscoveryURL     string
	AuthURL          string
	TokenURL         string
	JWKSURL          string
	UserInfoURL      string
	EndSession       string
	ClientID         string
	ClientSecret     string // write-only
	Scopes           []string
	GroupClaimNames  []string
	GroupRoleMapping map[string]string
	DefaultRole      string
	JITProvisioning  *bool
	Enabled          *bool
}
