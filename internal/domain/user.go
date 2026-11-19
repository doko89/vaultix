package domain

import (
	"fmt"
	"strings"
	"time"
)

// Built-in role names (PRD §5). Custom roles share the Role entity.
const (
	RoleAdministrator = "administrator"
	RoleOperator      = "operator"
	RoleReadOnly      = "readonly"
)

// User is a local Manager account. OIDC-authenticated users are provisioned
// as User rows linked via UserExternalIdentity (PRD §7.1.1).
type User struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string // empty for OIDC-only users; never serialized to API
	Disabled     bool
	// LocalLoginDisabled lets an admin force SSO-only for this user.
	LocalLoginDisabled bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Validate checks user invariants at the domain boundary.
func (u User) Validate() error {
	if strings.TrimSpace(u.Username) == "" {
		return fmt.Errorf("%w: username is required", ErrValidation)
	}
	if strings.TrimSpace(u.Email) == "" || !strings.Contains(u.Email, "@") {
		return fmt.Errorf("%w: valid email is required", ErrValidation)
	}
	return nil
}

// Active reports whether the user may authenticate.
func (u User) Active() bool { return !u.Disabled }

// Role is a named set of permissions; the permission matrix itself is
// configuration, while membership is tracked via UserRole.
type Role struct {
	ID          string
	Name        string // administrator | operator | readonly | custom
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Validate checks role invariants.
func (r Role) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: role name is required", ErrValidation)
	}
	return nil
}

// IsBuiltIn reports whether the role is one of the PRD-defined roles.
func (r Role) IsBuiltIn() bool {
	switch r.Name {
	case RoleAdministrator, RoleOperator, RoleReadOnly:
		return true
	default:
		return false
	}
}

// UserRole links a user to a role (PRD `user_roles`).
type UserRole struct {
	UserID    string
	RoleID    string
	GrantedBy string // actor user ID; empty for JIT provisioning
	CreatedAt time.Time
}

// OIDCProvider configures one OIDC Relying-Party integration (PRD §7.1.1).
// MVP supports at least one provider; the model allows several.
type OIDCProvider struct {
	ID string
	// Name is the human label shown on the login SSO button.
	Name    string
	Issuer  string
	Enabled bool

	// DiscoveryURL overrides the default issuer + /.well-known/openid-configuration.
	DiscoveryURL string
	// Endpoint overrides; empty means "from discovery".
	AuthURL     string
	TokenURL    string
	JWKSURL     string
	UserInfoURL string
	EndSession  string

	ClientID string
	// ClientSecretRef references the encrypted secret; the plaintext secret
	// is never stored on this entity and never returned by read APIs.
	ClientSecretRef string

	Scopes []string // default: openid profile email
	// GroupClaimNames lists claim names (e.g. groups, roles) mapped to roles.
	GroupClaimNames []string
	// GroupRoleMapping maps provider group -> Vaultix role name.
	GroupRoleMapping map[string]string
	// DefaultRole is assigned when no group maps; empty denies access.
	DefaultRole string
	// JITProvisioning creates the user on first login (default true).
	// When false, only pre-linked identities may log in.
	JITProvisioning bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks provider configuration invariants.
func (p OIDCProvider) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: provider name is required", ErrValidation)
	}
	if strings.TrimSpace(p.Issuer) == "" {
		return fmt.Errorf("%w: issuer is required", ErrValidation)
	}
	if strings.TrimSpace(p.ClientID) == "" {
		return fmt.Errorf("%w: client_id is required", ErrValidation)
	}
	return nil
}

// ScopesOrDefault returns the configured scopes or the PRD default.
func (p OIDCProvider) ScopesOrDefault() []string {
	if len(p.Scopes) == 0 {
		return []string{"openid", "profile", "email"}
	}
	return p.Scopes
}

// RoleForGroups resolves the Vaultix role for the given provider groups.
// It returns ("", false) when no group maps and no default role is set,
// in which case the login must be denied with an audit event.
func (p OIDCProvider) RoleForGroups(groups []string) (string, bool) {
	best := ""
	for _, g := range groups {
		if role, ok := p.GroupRoleMapping[g]; ok && role != "" {
			// Highest privilege wins: administrator > operator > readonly.
			if best == "" || rolePrecedence(role) > rolePrecedence(best) {
				best = role
			}
		}
	}
	if best != "" {
		return best, true
	}
	if p.DefaultRole != "" {
		return p.DefaultRole, true
	}
	return "", false
}

func rolePrecedence(role string) int {
	switch role {
	case RoleAdministrator:
		return 3
	case RoleOperator:
		return 2
	case RoleReadOnly:
		return 1
	default:
		return 0
	}
}

// UserExternalIdentity links a local user to an OIDC identity, keyed by the
// stable (provider issuer, subject) pair (PRD §7.1.1).
type UserExternalIdentity struct {
	ID         string
	UserID     string
	ProviderID string
	Issuer     string
	Subject    string // OIDC `sub`
	Email      string // last-seen email claim
	Disabled   bool
	LastLogin  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Validate checks identity invariants.
func (i UserExternalIdentity) Validate() error {
	if strings.TrimSpace(i.UserID) == "" {
		return fmt.Errorf("%w: user_id is required", ErrValidation)
	}
	if strings.TrimSpace(i.Issuer) == "" || strings.TrimSpace(i.Subject) == "" {
		return fmt.Errorf("%w: issuer and subject are required", ErrValidation)
	}
	return nil
}
