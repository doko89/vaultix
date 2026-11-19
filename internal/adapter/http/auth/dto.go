package auth

import (
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// DTOs: every request body is validated with validator/v10; responses never
// carry passwords, hashes, tokens, or client secrets.

// --- login / session ---

type loginRequest struct {
	Identifier string `json:"identifier" validate:"required,max=320"`
	Password   string `json:"password" validate:"required,max=1024"`
}

type userJSON struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	Email              string    `json:"email"`
	DisplayName        string    `json:"display_name"`
	Disabled           bool      `json:"disabled"`
	LocalLoginDisabled bool      `json:"local_login_disabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func toUserJSON(u *domain.User) userJSON {
	return userJSON{
		ID: u.ID, Username: u.Username, Email: u.Email,
		DisplayName: u.DisplayName, Disabled: u.Disabled,
		LocalLoginDisabled: u.LocalLoginDisabled,
		CreatedAt:          u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
}

type roleJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func toRoleJSON(r domain.Role) roleJSON {
	return roleJSON{ID: r.ID, Name: r.Name, Description: r.Description}
}

func toRoleList(roles []domain.Role) []roleJSON {
	out := make([]roleJSON, 0, len(roles))
	for _, r := range roles {
		out = append(out, toRoleJSON(r))
	}
	return out
}

type loginResponse struct {
	User      userJSON   `json:"user"`
	Roles     []roleJSON `json:"roles"`
	ExpiresAt time.Time  `json:"expires_at"`
}

type logoutResponse struct {
	// OIDCLogoutURL, when non-empty, is the RP-initiated logout URL the UI
	// should redirect to after clearing local state.
	OIDCLogoutURL string `json:"oidc_logout_url,omitempty"`
}

// --- users ---

type createUserRequest struct {
	Username    string   `json:"username" validate:"required,min=1,max=128"`
	Email       string   `json:"email" validate:"required,email,max=320"`
	DisplayName string   `json:"display_name" validate:"max=256"`
	Password    string   `json:"password" validate:"max=1024"`
	RoleNames   []string `json:"roles" validate:"dive,min=1,max=128"`
}

type updateUserRequest struct {
	Email              string `json:"email" validate:"omitempty,email,max=320"`
	DisplayName        string `json:"display_name" validate:"max=256"`
	LocalLoginDisabled *bool  `json:"local_login_disabled"`
}

type setPasswordRequest struct {
	Password string `json:"password" validate:"required,min=8,max=1024"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password" validate:"required,max=1024"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=1024"`
}

type grantRoleRequest struct {
	Role string `json:"role" validate:"required,min=1,max=128"`
}

// --- identities ---

type identityJSON struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	ProviderID string     `json:"provider_id"`
	Issuer     string     `json:"issuer"`
	Subject    string     `json:"subject"`
	Email      string     `json:"email"`
	Disabled   bool       `json:"disabled"`
	LastLogin  *time.Time `json:"last_login_at,omitempty"`
}

func toIdentityJSON(i domain.UserExternalIdentity) identityJSON {
	return identityJSON{
		ID: i.ID, UserID: i.UserID, ProviderID: i.ProviderID,
		Issuer: i.Issuer, Subject: i.Subject, Email: i.Email,
		Disabled: i.Disabled, LastLogin: i.LastLogin,
	}
}

type linkIdentityRequest struct {
	ProviderID string `json:"provider_id" validate:"max=128"`
	Issuer     string `json:"issuer" validate:"required,min=1,max=1024"`
	Subject    string `json:"subject" validate:"required,min=1,max=1024"`
	Email      string `json:"email" validate:"max=320"`
}

// --- providers ---

type providerJSON struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Issuer           string            `json:"issuer"`
	Enabled          bool              `json:"enabled"`
	DiscoveryURL     string            `json:"discovery_url,omitempty"`
	AuthURL          string            `json:"auth_url,omitempty"`
	TokenURL         string            `json:"token_url,omitempty"`
	JWKSURL          string            `json:"jwks_url,omitempty"`
	UserInfoURL      string            `json:"userinfo_url,omitempty"`
	EndSession       string            `json:"end_session_endpoint,omitempty"`
	ClientID         string            `json:"client_id"`
	HasSecret        bool              `json:"has_secret"`
	Scopes           []string          `json:"scopes"`
	GroupClaimNames  []string          `json:"group_claim_names"`
	GroupRoleMapping map[string]string `json:"group_role_mapping"`
	DefaultRole      string            `json:"default_role,omitempty"`
	JITProvisioning  bool              `json:"jit_provisioning"`
}

func toProviderJSON(p *domain.OIDCProvider) providerJSON {
	return providerJSON{
		ID: p.ID, Name: p.Name, Issuer: p.Issuer, Enabled: p.Enabled,
		DiscoveryURL: p.DiscoveryURL, AuthURL: p.AuthURL, TokenURL: p.TokenURL,
		JWKSURL: p.JWKSURL, UserInfoURL: p.UserInfoURL, EndSession: p.EndSession,
		ClientID: p.ClientID, HasSecret: p.ClientSecretRef != "",
		Scopes: p.Scopes, GroupClaimNames: p.GroupClaimNames,
		GroupRoleMapping: p.GroupRoleMapping, DefaultRole: p.DefaultRole,
		JITProvisioning: p.JITProvisioning,
	}
}

type publicProviderJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type providerRequest struct {
	Name             string            `json:"name" validate:"required,min=1,max=128"`
	Issuer           string            `json:"issuer" validate:"required,min=1,max=1024"`
	DiscoveryURL     string            `json:"discovery_url" validate:"max=2048"`
	AuthURL          string            `json:"auth_url" validate:"max=2048"`
	TokenURL         string            `json:"token_url" validate:"max=2048"`
	JWKSURL          string            `json:"jwks_url" validate:"max=2048"`
	UserInfoURL      string            `json:"userinfo_url" validate:"max=2048"`
	EndSession       string            `json:"end_session_endpoint" validate:"max=2048"`
	ClientID         string            `json:"client_id" validate:"required,min=1,max=1024"`
	ClientSecret     string            `json:"client_secret" validate:"max=4096"`
	Scopes           []string          `json:"scopes" validate:"dive,max=128"`
	GroupClaimNames  []string          `json:"group_claim_names" validate:"dive,max=256"`
	GroupRoleMapping map[string]string `json:"group_role_mapping"`
	DefaultRole      string            `json:"default_role" validate:"max=128"`
	JITProvisioning  *bool             `json:"jit_provisioning"`
	Enabled          *bool             `json:"enabled"`
}

type rotateSecretRequest struct {
	Secret string `json:"secret" validate:"required,min=1,max=4096"`
}

// --- settings ---

type localLoginRequest struct {
	Enabled   bool `json:"enabled"`
	Confirmed bool `json:"confirmed"`
}

type localLoginResponse struct {
	Enabled bool `json:"enabled"`
}
