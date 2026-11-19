package repo

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// OIDCRepo implements domain.OIDCProviderRepository on
// oidc_providers/user_external_identities.
//
// Mapping notes: endpoint overrides live in the endpoints JSON object;
// group claim names and the group->role map live in claims_mapping;
// DefaultRole (a role name) resolves to default_role_id on write and back
// on read. ClientSecretRef lives in endpoints JSON only: reads never
// select the secret column, and writes never touch it —
// client_secret_encrypted holds the auth track's Box envelope, owned
// exclusively by auth.ProviderSecretStore (see db/queries/oidc.sql).
type OIDCRepo struct {
	baseRepo
}

// oidcEndpoints is the endpoints JSON codec.
type oidcEndpoints struct {
	AuthURL         string `json:"auth_url,omitempty"`
	TokenURL        string `json:"token_url,omitempty"`
	JWKSURL         string `json:"jwks_url,omitempty"`
	UserInfoURL     string `json:"userinfo_url,omitempty"`
	EndSession      string `json:"end_session,omitempty"`
	ClientSecretRef string `json:"client_secret_ref,omitempty"`
}

// oidcClaimsMapping is the claims_mapping JSON codec.
type oidcClaimsMapping struct {
	GroupClaims      []string          `json:"group_claims"`
	GroupRoleMapping map[string]string `json:"group_role_mapping"`
}

// CreateProvider inserts a provider config. The ID is database-generated;
// ClientSecretRef holds a reference, never plaintext.
func (r *OIDCRepo) CreateProvider(ctx context.Context, p *domain.OIDCProvider) error {
	if p == nil {
		return validationError("provider is required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	defaultRoleID, err := r.resolveDefaultRoleID(ctx, p.DefaultRole)
	if err != nil {
		return err
	}
	endpoints, err := encodeOIDCEndpoints(p)
	if err != nil {
		return err
	}
	mapping, err := encodeOIDCMapping(p)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateProvider(ctx, gen.CreateProviderParams{
		Name:         p.Name,
		Issuer:       p.Issuer,
		DiscoveryUrl: textOrNull(p.DiscoveryURL),
		Endpoints:    endpoints,
		ClientID:     p.ClientID,
		// NULL: the secret envelope arrives later through
		// auth.ProviderSecretStore; the reference rides in endpoints JSON.
		ClientSecretEncrypted: textOrNull(""),
		Scopes:                oidcScopesString(p.Scopes),
		ClaimsMapping:         mapping,
		DefaultRoleID:         defaultRoleID,
		IsEnabled:             p.Enabled,
		AllowJitProvisioning:  p.JITProvisioning,
	})
	if err != nil {
		return mapErr(err)
	}
	p.ID = uuidString(row.ID)
	p.CreatedAt = timeOrZero(row.CreatedAt)
	p.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetProvider returns one provider by ID.
func (r *OIDCRepo) GetProvider(ctx context.Context, id string) (*domain.OIDCProvider, error) {
	pid, err := mustParseUUID(id, "provider id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetProviderByID(ctx, gen.GetProviderByIDParams{ID: pid})
	if err != nil {
		return nil, mapErr(err)
	}
	return r.mapProviderRow(ctx, providerRow{
		ID: row.ID, Name: row.Name, Issuer: row.Issuer, DiscoveryURL: row.DiscoveryUrl,
		Endpoints: row.Endpoints, ClientID: row.ClientID, Scopes: row.Scopes,
		ClaimsMapping: row.ClaimsMapping, DefaultRoleID: row.DefaultRoleID,
		IsEnabled: row.IsEnabled, JIT: row.AllowJitProvisioning,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
}

// GetProviderByIssuer resolves a provider by its issuer.
func (r *OIDCRepo) GetProviderByIssuer(ctx context.Context, issuer string) (*domain.OIDCProvider, error) {
	if issuer == "" {
		return nil, notFound("provider")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetProviderByIssuer(ctx, gen.GetProviderByIssuerParams{Issuer: issuer})
	if err != nil {
		return nil, mapErr(err)
	}
	return r.mapProviderRow(ctx, providerRow{
		ID: row.ID, Name: row.Name, Issuer: row.Issuer, DiscoveryURL: row.DiscoveryUrl,
		Endpoints: row.Endpoints, ClientID: row.ClientID, Scopes: row.Scopes,
		ClaimsMapping: row.ClaimsMapping, DefaultRoleID: row.DefaultRoleID,
		IsEnabled: row.IsEnabled, JIT: row.AllowJitProvisioning,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
}

// UpdateProvider rewrites the provider config.
func (r *OIDCRepo) UpdateProvider(ctx context.Context, p *domain.OIDCProvider) error {
	if p == nil {
		return validationError("provider is required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	pid, err := mustParseUUID(p.ID, "provider id")
	if err != nil {
		return err
	}
	defaultRoleID, err := r.resolveDefaultRoleID(ctx, p.DefaultRole)
	if err != nil {
		return err
	}
	endpoints, err := encodeOIDCEndpoints(p)
	if err != nil {
		return err
	}
	mapping, err := encodeOIDCMapping(p)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateProvider(ctx, gen.UpdateProviderParams{
		ID:                   pid,
		Name:                 p.Name,
		Issuer:               p.Issuer,
		DiscoveryUrl:         textOrNull(p.DiscoveryURL),
		Endpoints:            endpoints,
		ClientID:             p.ClientID,
		Scopes:               oidcScopesString(p.Scopes),
		ClaimsMapping:        mapping,
		DefaultRoleID:        defaultRoleID,
		IsEnabled:            p.Enabled,
		AllowJitProvisioning: p.JITProvisioning,
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// ListProviders returns all providers ordered by name. Role names resolve
// through a per-call cache so the list costs one extra query per distinct
// default role, not per row.
func (r *OIDCRepo) ListProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListProviders(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	cache := map[string]string{}
	out := make([]domain.OIDCProvider, 0, len(rows))
	for _, row := range rows {
		p, err := r.mapProviderRowCached(ctx, providerRow{
			ID: row.ID, Name: row.Name, Issuer: row.Issuer, DiscoveryURL: row.DiscoveryUrl,
			Endpoints: row.Endpoints, ClientID: row.ClientID, Scopes: row.Scopes,
			ClaimsMapping: row.ClaimsMapping, DefaultRoleID: row.DefaultRoleID,
			IsEnabled: row.IsEnabled, JIT: row.AllowJitProvisioning,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}, cache)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// LinkIdentity creates a (provider issuer, subject) -> user link.
func (r *OIDCRepo) LinkIdentity(ctx context.Context, i *domain.UserExternalIdentity) error {
	if i == nil {
		return validationError("identity is required")
	}
	if err := i.Validate(); err != nil {
		return err
	}
	uid, err := mustParseUUID(i.UserID, "user id")
	if err != nil {
		return err
	}
	providerID, err := mustOptUUID(i.ProviderID, "provider id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateIdentity(ctx, gen.CreateIdentityParams{
		UserID:     uid,
		ProviderID: providerID,
		Issuer:     i.Issuer,
		Subject:    i.Subject,
		Email:      i.Email,
		Claims:     []byte("{}"),
	})
	if err != nil {
		return mapErr(err)
	}
	i.ID = uuidString(row.ID)
	i.Disabled = row.IsDisabled
	i.CreatedAt = timeOrZero(row.CreatedAt)
	i.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetIdentity resolves an identity by its stable (issuer, subject) pair.
func (r *OIDCRepo) GetIdentity(ctx context.Context, issuer, subject string) (*domain.UserExternalIdentity, error) {
	if issuer == "" || subject == "" {
		return nil, notFound("identity")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetIdentityByIssuerSubject(ctx, gen.GetIdentityByIssuerSubjectParams{
		Issuer:  issuer,
		Subject: subject,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	ident := mapIdentity(row)
	return &ident, nil
}

// ListUserIdentities returns all identity links for a user.
func (r *OIDCRepo) ListUserIdentities(ctx context.Context, userID string) ([]domain.UserExternalIdentity, error) {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListIdentitiesByUser(ctx, gen.ListIdentitiesByUserParams{UserID: uid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.UserExternalIdentity, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapIdentity(row))
	}
	return out, nil
}

// UpdateIdentity rewrites email/disabled state. Raw claims and the last
// login timestamp are preserved (claims have no domain field; logins flow
// through UpdateIdentityLogin inside the OIDC login path).
func (r *OIDCRepo) UpdateIdentity(ctx context.Context, i *domain.UserExternalIdentity) error {
	if i == nil {
		return validationError("identity is required")
	}
	if err := i.Validate(); err != nil {
		return err
	}
	iid, err := mustParseUUID(i.ID, "identity id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	current, err := r.q.GetIdentityByID(ctx, gen.GetIdentityByIDParams{ID: iid})
	if err != nil {
		return mapErr(err)
	}
	lastLogin := current.LastLoginAt
	if i.LastLogin != nil {
		lastLogin = timestamptzPtr(i.LastLogin)
	}
	_, err = r.q.UpdateIdentity(ctx, gen.UpdateIdentityParams{
		ID:          iid,
		Email:       i.Email,
		Claims:      current.Claims,
		IsDisabled:  i.Disabled,
		LastLoginAt: lastLogin,
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// UnlinkIdentity deletes an identity link.
func (r *OIDCRepo) UnlinkIdentity(ctx context.Context, id string) error {
	iid, err := mustParseUUID(id, "identity id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetIdentityByID(ctx, gen.GetIdentityByIDParams{ID: iid}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeleteIdentity(ctx, gen.DeleteIdentityParams{ID: iid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// providerRow unifies the redacted provider row shapes.
type providerRow struct {
	ID            pgtype.UUID
	Name          string
	Issuer        string
	DiscoveryURL  pgtype.Text
	Endpoints     []byte
	ClientID      string
	Scopes        string
	ClaimsMapping []byte
	DefaultRoleID pgtype.UUID
	IsEnabled     bool
	JIT           bool
	CreatedAt     pgtype.Timestamptz
	UpdatedAt     pgtype.Timestamptz
}

func (r *OIDCRepo) mapProviderRow(ctx context.Context, row providerRow) (*domain.OIDCProvider, error) {
	p, err := r.mapProviderRowCached(ctx, row, map[string]string{})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *OIDCRepo) mapProviderRowCached(ctx context.Context, row providerRow, cache map[string]string) (domain.OIDCProvider, error) {
	var endpoints oidcEndpoints
	if err := decodeJSON(row.Endpoints, &endpoints); err != nil {
		return domain.OIDCProvider{}, err
	}
	var mapping oidcClaimsMapping
	if err := decodeJSON(row.ClaimsMapping, &mapping); err != nil {
		return domain.OIDCProvider{}, err
	}
	var defaultRole string
	if row.DefaultRoleID.Valid {
		name, err := r.roleNameCached(ctx, row.DefaultRoleID, cache)
		if err != nil {
			return domain.OIDCProvider{}, err
		}
		defaultRole = name
	}
	return domain.OIDCProvider{
		ID:               uuidString(row.ID),
		Name:             row.Name,
		Issuer:           row.Issuer,
		Enabled:          row.IsEnabled,
		DiscoveryURL:     textOrEmpty(row.DiscoveryURL),
		AuthURL:          endpoints.AuthURL,
		TokenURL:         endpoints.TokenURL,
		JWKSURL:          endpoints.JWKSURL,
		UserInfoURL:      endpoints.UserInfoURL,
		EndSession:       endpoints.EndSession,
		ClientID:         row.ClientID,
		ClientSecretRef:  endpoints.ClientSecretRef,
		Scopes:           oidcScopesSlice(row.Scopes),
		GroupClaimNames:  mapping.GroupClaims,
		GroupRoleMapping: mapping.GroupRoleMapping,
		DefaultRole:      defaultRole,
		JITProvisioning:  row.JIT,
		CreatedAt:        timeOrZero(row.CreatedAt),
		UpdatedAt:        timeOrZero(row.UpdatedAt),
	}, nil
}

func (r *OIDCRepo) roleNameCached(ctx context.Context, id pgtype.UUID, cache map[string]string) (string, error) {
	key := uuidString(id)
	if name, ok := cache[key]; ok {
		return name, nil
	}
	row, err := r.q.GetRoleByID(ctx, gen.GetRoleByIDParams{ID: id})
	if err != nil {
		return "", mapErr(err)
	}
	cache[key] = row.Name
	return row.Name, nil
}

// resolveDefaultRoleID maps a role name to its ID; "" maps to NULL.
func (r *OIDCRepo) resolveDefaultRoleID(ctx context.Context, roleName string) (pgtype.UUID, error) {
	if roleName == "" {
		return pgtype.UUID{}, nil
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetRoleByName(ctx, gen.GetRoleByNameParams{Name: roleName})
	if err != nil {
		if mapped := mapErr(err); mapped != nil {
			return pgtype.UUID{}, mapped
		}
		return pgtype.UUID{}, err
	}
	return row.ID, nil
}

func encodeOIDCEndpoints(p *domain.OIDCProvider) ([]byte, error) {
	return encodeJSON(oidcEndpoints{
		AuthURL:         p.AuthURL,
		TokenURL:        p.TokenURL,
		JWKSURL:         p.JWKSURL,
		UserInfoURL:     p.UserInfoURL,
		EndSession:      p.EndSession,
		ClientSecretRef: p.ClientSecretRef,
	})
}

func encodeOIDCMapping(p *domain.OIDCProvider) ([]byte, error) {
	groups := p.GroupClaimNames
	if groups == nil {
		groups = []string{}
	}
	roles := p.GroupRoleMapping
	if roles == nil {
		roles = map[string]string{}
	}
	return encodeJSON(oidcClaimsMapping{GroupClaims: groups, GroupRoleMapping: roles})
}

func oidcScopesString(scopes []string) string {
	if len(scopes) == 0 {
		return "openid profile email"
	}
	return strings.Join(scopes, " ")
}

func oidcScopesSlice(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Fields(s)
}

func mapIdentity(row gen.UserExternalIdentity) domain.UserExternalIdentity {
	return domain.UserExternalIdentity{
		ID:         uuidString(row.ID),
		UserID:     uuidString(row.UserID),
		ProviderID: uuidString(row.ProviderID),
		Issuer:     row.Issuer,
		Subject:    row.Subject,
		Email:      row.Email,
		Disabled:   row.IsDisabled,
		LastLogin:  timePtr(row.LastLoginAt),
		CreatedAt:  timeOrZero(row.CreatedAt),
		UpdatedAt:  timeOrZero(row.UpdatedAt),
	}
}
