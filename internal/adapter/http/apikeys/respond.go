package apikeys

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	apikeysvc "github.com/vaultix/vaultix/internal/usecase/apikey"
)

// respond.go: JSON envelopes and error mapping.

// maxBody caps request bodies (issue DTOs are small).
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
	writeJSON(w, status, errorJSON{Error: errorDetail{Code: code, Message: message}})
}

// writeServiceError maps domain/usecase errors to status codes. Unknown
// failures collapse to a generic 500 (no secret material ever echoes).
func writeServiceError(w http.ResponseWriter, err error) {
	status, code, message := mapError(err)
	writeError(w, status, code, message)
}

func mapError(err error) (int, string, string) {
	switch {
	case errors.Is(err, apikeysvc.ErrInvalidKey),
		errors.Is(err, apikeysvc.ErrKeyRevoked),
		errors.Is(err, apikeysvc.ErrKeyExpired):
		return http.StatusUnauthorized, "invalid_api_key", "API key is invalid, revoked, or expired"
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
