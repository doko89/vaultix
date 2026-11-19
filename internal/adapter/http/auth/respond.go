package auth

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
	usersvc "github.com/vaultix/vaultix/internal/usecase/user"
)

// respond.go: JSON envelopes, error mapping, and output redaction.

// maxBody caps request bodies (DTOs are small).
const maxBody = 1 << 20

type errorJSON struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorJSON{Error: errorDetail{Code: code, Message: Sanitize(message)}})
}

// writeServiceError maps domain/usecase errors to status codes. Messages are
// sanitized; unknown failures collapse to a generic 500.
func writeServiceError(w http.ResponseWriter, err error) {
	status, code, message := mapError(err)
	writeError(w, status, code, message)
}

func mapError(err error) (int, string, string) {
	switch {
	case errors.Is(err, authsvc.ErrInvalidCredentials):
		return http.StatusUnauthorized, "invalid_credentials", "invalid credentials"
	case errors.Is(err, authsvc.ErrAccountDisabled):
		return http.StatusForbidden, "account_disabled", "account is disabled"
	case errors.Is(err, authsvc.ErrLocalLoginDisabled):
		return http.StatusForbidden, "local_login_disabled", "local login is disabled"
	case errors.Is(err, authsvc.ErrNoRolesAssigned),
		errors.Is(err, authsvc.ErrNoMappedRole):
		return http.StatusForbidden, "no_role", "no role assigned; contact your administrator"
	case errors.Is(err, authsvc.ErrPreprovisionedOnly):
		return http.StatusForbidden, "preprovisioned_only", "account is not provisioned for OIDC login"
	case errors.Is(err, authsvc.ErrInvalidState):
		return http.StatusBadRequest, "invalid_state", "login session expired; please try again"
	case errors.Is(err, authsvc.ErrProviderDisabled):
		return http.StatusBadRequest, "provider_unavailable", "OIDC provider is unavailable"
	case errors.Is(err, authsvc.ErrNoBreakGlass),
		errors.Is(err, usersvc.ErrLastAdmin):
		return http.StatusConflict, "break_glass", "operation refused: it would remove the last administrator login path"
	case errors.Is(err, authsvc.ErrWeakPassword),
		errors.Is(err, usersvc.ErrWeakPassword):
		return http.StatusBadRequest, "weak_password", "password does not meet policy"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found", "not found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict", "already exists"
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation", "invalid request"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden", "forbidden"
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}

// secretPattern redacts key=value shaped secrets that might slip into an
// error string (defense in depth; services must never emit them).
var secretPattern = regexp.MustCompile(`(?i)(client_secret|client-secret|password|passwd|pwd|id_token|access_token|refresh_token|code|token|secret|authorization)(=|:)([^\s&;,}]+)`)

// Sanitize redacts secret-shaped material from strings destined for API
// responses. Known secret values can be passed explicitly for exact
// redaction; patterns catch the rest.
func Sanitize(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return secretPattern.ReplaceAllString(s, "$1$2[redacted]")
}

// decode validates content length, decodes JSON, and runs validator/v10.
func (h *handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "request body is not valid JSON")
		return false
	}
	if err := h.validate.Struct(dst); err != nil {
		writeError(w, http.StatusBadRequest, "validation", describeValidation(err))
		return false
	}
	return true
}

// describeValidation renders validator errors as "field: rule" pairs.
// Values are never echoed.
func describeValidation(err error) string {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return "invalid request"
	}
	parts := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		field := strings.ToLower(fe.Field())
		parts = append(parts, field+": "+fe.Tag())
	}
	return "invalid request: " + strings.Join(parts, ", ")
}

// clientIP extracts the peer IP for audit context.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
