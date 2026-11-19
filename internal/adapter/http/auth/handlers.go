package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	usersvc "github.com/vaultix/vaultix/internal/usecase/user"
)

// handlers.go: login/logout, OIDC, self, users/roles, identities, providers.

// --- session / login ---

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if !h.decode(w, r, &in) {
		return
	}
	res, err := h.auth.Login(r.Context(), in.Identifier, in.Password, clientIP(r), userAgent(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.setSessionCookie(w, res.Token, res.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, loginResponse{
		User: toUserJSON(res.User), Roles: toRoleList(res.Roles), ExpiresAt: res.Session.ExpiresAt,
	})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	token := ""
	if cookie, err := r.Cookie(h.cookie.Name); err == nil {
		token = cookie.Value
	}
	h.clearSessionCookie(w)
	var oidcURL string
	if token != "" {
		var err error
		oidcURL, err = h.auth.Logout(r.Context(), token, h.postOut)
		if err != nil {
			writeServiceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, logoutResponse{OIDCLogoutURL: oidcURL})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	info, _ := SessionFromContext(r.Context())
	writeJSON(w, http.StatusOK, loginResponse{
		User: toUserJSON(info.User), Roles: toRoleList(info.Roles), ExpiresAt: info.Session.ExpiresAt,
	})
}

// --- OIDC ---

func (h *handler) oidcStart(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider"))
	if providerID == "" {
		writeError(w, http.StatusBadRequest, "validation", "query parameter 'provider' is required")
		return
	}
	if h.oidcCB == "" {
		writeError(w, http.StatusServiceUnavailable, "oidc_unconfigured", "OIDC callback URL is not configured")
		return
	}
	start, err := h.auth.StartOIDCLogin(r.Context(), providerID, h.oidcCB)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	http.Redirect(w, r, start.AuthURL, http.StatusFound)
}

func (h *handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	providerID, code, state := q.Get("provider"), q.Get("code"), q.Get("state")
	if providerID == "" || code == "" || state == "" {
		h.auditCallback(r, providerID, "missing_parameters")
		writeError(w, http.StatusBadRequest, "validation", "callback is missing required parameters")
		return
	}
	if errParam := q.Get("error"); errParam != "" {
		// User denied access (or IdP error): actionable message + audit.
		h.auditCallback(r, providerID, "idp_error:"+sanitizedOIDCError(errParam))
		writeError(w, http.StatusBadRequest, "oidc_callback", "sign-in was not completed ("+sanitizedOIDCError(errParam)+"); please try again or contact your administrator")
		return
	}
	res, err := h.auth.CompleteOIDCLogin(r.Context(), providerID, state, code, h.oidcCB, clientIP(r), userAgent(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	h.setSessionCookie(w, res.Token, res.Session.ExpiresAt)
	http.Redirect(w, r, h.uiAfter, http.StatusFound)
}

// sanitizedOIDCError allowlists IdP error codes for display.
func sanitizedOIDCError(code string) string {
	switch code {
	case "access_denied", "expired_code", "invalid_state", "invalid_request",
		"unauthorized_client", "unsupported_response_type", "invalid_scope",
		"server_error", "temporarily_unavailable":
		return code
	default:
		return "error"
	}
}

func (h *handler) auditCallback(r *http.Request, providerID, detail string) {
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAuthEvent(r.Context(), authsvc.AuditEvent{
		At: h.clock.Now(), Action: "auth.oidc.callback",
		Actor: "anonymous", Target: providerID,
		IP: clientIP(r), Result: "denied", Detail: detail,
	})
}

// --- self password ---

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var in changePasswordRequest
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.users.ChangePassword(r.Context(), actorID(r), in.OldPassword, in.NewPassword); err != nil {
		writeServiceError(w, err)
		return
	}
	h.clearSessionCookie(w) // sessions revoked; force re-login
	w.WriteHeader(http.StatusNoContent)
}

// --- users (admin) ---

func (h *handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in createUserRequest
	if !h.decode(w, r, &in) {
		return
	}
	u, err := h.users.CreateUser(r.Context(), usersvc.CreateInput{
		Username: in.Username, Email: in.Email, DisplayName: in.DisplayName,
		Password: in.Password, RoleNames: in.RoleNames,
	}, actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUserJSON(u))
}

func (h *handler) listUsers(w http.ResponseWriter, r *http.Request) {
	opts := domain.ListOptions{
		Limit: queryInt(r, "limit", 50), Offset: queryInt(r, "offset", 0), Query: r.URL.Query().Get("q"),
	}
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 50
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	res, err := h.users.ListUsers(r.Context(), opts)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]userJSON, 0, len(res.Items))
	for _, u := range res.Items {
		u := u
		items = append(items, toUserJSON(&u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": res.Total})
}

func (h *handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	u, err := h.users.GetUser(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserJSON(u))
}

func (h *handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	var in updateUserRequest
	if !h.decode(w, r, &in) {
		return
	}
	u, err := h.users.UpdateUser(r.Context(), id, usersvc.UpdateInput{
		Email: in.Email, DisplayName: in.DisplayName, LocalLoginDisabled: in.LocalLoginDisabled,
	}, actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserJSON(u))
}

func (h *handler) disableUser(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "/api/auth/users/")
		if !ok {
			writeError(w, http.StatusBadRequest, "validation", "user id is required")
			return
		}
		if err := h.users.SetDisabled(r.Context(), id, disabled, actorID(r)); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) setPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	var in setPasswordRequest
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.users.SetPassword(r.Context(), id, in.Password, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.users.ListRoles(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toRoleList(roles)})
}

func (h *handler) listUserRoles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	roles, err := h.users.ListUserRoles(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toRoleList(roles)})
}

func (h *handler) grantRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	var in grantRoleRequest
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.users.GrantRole(r.Context(), id, in.Role, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) revokeRole(w http.ResponseWriter, r *http.Request) {
	id, rest, ok := pathIDRest(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	// rest: "roles/{role}"
	if !strings.HasPrefix(rest, "roles/") {
		writeError(w, http.StatusBadRequest, "validation", "role name is required")
		return
	}
	role := strings.TrimPrefix(rest, "roles/")
	if role == "" || strings.Contains(role, "/") {
		writeError(w, http.StatusBadRequest, "validation", "role name is required")
		return
	}
	if err := h.users.RevokeRole(r.Context(), id, role, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- identities (admin) ---

func (h *handler) listIdentities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	idents, err := h.auth.ListUserIdentities(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]identityJSON, 0, len(idents))
	for _, ident := range idents {
		items = append(items, toIdentityJSON(ident))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) linkIdentity(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	var in linkIdentityRequest
	if !h.decode(w, r, &in) {
		return
	}
	ident, err := h.auth.LinkIdentity(r.Context(), id, in.ProviderID, in.Issuer, in.Subject, in.Email, actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toIdentityJSON(*ident))
}

func (h *handler) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	id, rest, ok := pathIDRest(r, "/api/auth/users/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "user id is required")
		return
	}
	if !strings.HasPrefix(rest, "identities/") {
		writeError(w, http.StatusBadRequest, "validation", "identity id is required")
		return
	}
	iid := strings.TrimPrefix(rest, "identities/")
	if iid == "" || strings.Contains(iid, "/") {
		writeError(w, http.StatusBadRequest, "validation", "identity id is required")
		return
	}
	if err := h.auth.UnlinkIdentity(r.Context(), id, iid, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) disableIdentity(disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, rest, ok := pathIDRest(r, "/api/auth/users/")
		if !ok {
			writeError(w, http.StatusBadRequest, "validation", "user id is required")
			return
		}
		// rest: "identities/{iid}/disable|enable"
		if !strings.HasPrefix(rest, "identities/") {
			writeError(w, http.StatusBadRequest, "validation", "identity id is required")
			return
		}
		seg := strings.TrimPrefix(rest, "identities/")
		iid, _, _ := strings.Cut(seg, "/")
		if iid == "" {
			writeError(w, http.StatusBadRequest, "validation", "identity id is required")
			return
		}
		if err := h.auth.SetIdentityDisabled(r.Context(), id, iid, disabled, actorID(r)); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- providers (admin) ---

func (h *handler) listPublicProviders(w http.ResponseWriter, r *http.Request) {
	provs, err := h.auth.ListProviders(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := []publicProviderJSON{}
	for _, p := range provs {
		if p.Enabled {
			items = append(items, publicProviderJSON{ID: p.ID, Name: p.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) listProviders(w http.ResponseWriter, r *http.Request) {
	provs, err := h.auth.ListProviders(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]providerJSON, 0, len(provs))
	for _, p := range provs {
		p := p
		items = append(items, toProviderJSON(&p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) getProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/providers/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "provider id is required")
		return
	}
	p, err := h.auth.GetProvider(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProviderJSON(p))
}

func (h *handler) createProvider(w http.ResponseWriter, r *http.Request) {
	var in providerRequest
	if !h.decode(w, r, &in) {
		return
	}
	p, err := h.auth.CreateProvider(r.Context(), toProviderInput(in), actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProviderJSON(p))
}

func (h *handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/providers/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "provider id is required")
		return
	}
	var in providerRequest
	if !h.decode(w, r, &in) {
		return
	}
	p, err := h.auth.UpdateProvider(r.Context(), id, toProviderInput(in), actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProviderJSON(p))
}

func toProviderInput(in providerRequest) authsvc.ProviderInput {
	return authsvc.ProviderInput{
		Name: in.Name, Issuer: in.Issuer, DiscoveryURL: in.DiscoveryURL,
		AuthURL: in.AuthURL, TokenURL: in.TokenURL, JWKSURL: in.JWKSURL,
		UserInfoURL: in.UserInfoURL, EndSession: in.EndSession,
		ClientID: in.ClientID, ClientSecret: in.ClientSecret,
		Scopes: in.Scopes, GroupClaimNames: in.GroupClaimNames,
		GroupRoleMapping: in.GroupRoleMapping, DefaultRole: in.DefaultRole,
		JITProvisioning: in.JITProvisioning, Enabled: in.Enabled,
	}
}

func (h *handler) enableProvider(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "/api/auth/providers/")
		if !ok {
			writeError(w, http.StatusBadRequest, "validation", "provider id is required")
			return
		}
		if err := h.auth.SetProviderEnabled(r.Context(), id, enabled, actorID(r)); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) testProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/providers/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "provider id is required")
		return
	}
	rep, err := h.auth.TestProvider(r.Context(), id, actorID(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (h *handler) rotateSecret(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "/api/auth/providers/")
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "provider id is required")
		return
	}
	var in rotateSecretRequest
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.auth.RotateProviderSecret(r.Context(), id, in.Secret, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- settings (admin) ---

func (h *handler) setLocalLogin(w http.ResponseWriter, r *http.Request) {
	var in localLoginRequest
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.auth.SetLocalLoginEnabled(r.Context(), in.Enabled, in.Confirmed, actorID(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, localLoginResponse{Enabled: in.Enabled})
}

func (h *handler) getLocalLogin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, localLoginResponse{Enabled: h.auth.LocalLoginEnabled()})
}

// --- cookies ---

func (h *handler) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookie.Name, Value: token, Path: h.cookie.Path,
		Domain: h.cookie.Domain, Expires: expires, HttpOnly: true,
		Secure: h.cookie.Secure, SameSite: h.cookie.SameSite,
	})
}

func (h *handler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: h.cookie.Name, Value: "", Path: h.cookie.Path,
		Domain: h.cookie.Domain, MaxAge: -1, HttpOnly: true,
		Secure: h.cookie.Secure, SameSite: h.cookie.SameSite,
	})
}

// --- path helpers (router-agnostic) ---

// pathID strips a known prefix and returns the first segment: for
// "/api/auth/users/{id}[...]" with prefix "/api/auth/users/" it yields id.
func pathID(r *http.Request, prefix string) (string, bool) {
	id, _, ok := pathIDRest(r, prefix)
	return id, ok
}

func pathIDRest(r *http.Request, prefix string) (id, rest string, ok bool) {
	if v := r.PathValue("id"); v != "" {
		// stdlib ServeMux 1.22+ route: prefer its parsed value, but still
		// derive the remainder from the raw path for nested segments.
		trimmed := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), prefix)
		_, after, _ := strings.Cut(trimmed, "/")
		return v, after, true
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), prefix)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || trimmed == strings.TrimSuffix(r.URL.Path, "/") {
		return "", "", false
	}
	id, rest, _ = strings.Cut(trimmed, "/")
	if id == "" || strings.Contains(id, "/") {
		return "", "", false
	}
	return id, rest, true
}

func queryInt(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 512 {
		return ua[:512]
	}
	return ua
}
