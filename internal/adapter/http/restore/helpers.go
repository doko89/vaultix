package restore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
)

// maxBodyBytes caps JSON request bodies.
const maxBodyBytes = 1 << 20

// errorCodes maps failures to stable machine-readable codes.
const (
	codeValidation = "validation_error"
	codeNotFound   = "not_found"
	codeConflict   = "conflict"
	codeGone       = "gone"
	codeAuth       = "invalid_token"
	codeInternal   = "internal_error"
)

// envelopeError is the JSON error body.
type envelopeError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeJSON renders a success envelope.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// writeError renders a failure envelope.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	var body envelopeError
	body.Error.Code = code
	body.Error.Message = msg
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeServiceError maps domain/usecase errors to HTTP statuses.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, domain.ErrNotFound),
		errors.Is(err, restoreuc.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, restoreuc.ErrTokenInvalid):
		writeError(w, http.StatusUnauthorized, codeAuth, err.Error())
	case errors.Is(err, restoreuc.ErrTokenExpired),
		errors.Is(err, restoreuc.ErrPlanExpired):
		writeError(w, http.StatusGone, codeGone, err.Error())
	case errors.Is(err, restoreuc.ErrNotRestorable),
		errors.Is(err, restoreuc.ErrOrderRequired),
		errors.Is(err, restoreuc.ErrTokenReused),
		errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
	}
}

// decodeJSON parses a bounded JSON body into v.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, codeValidation, "request body is required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("invalid JSON body: %v", err))
		return false
	}
	return true
}

// requireParam fetches a non-blank query parameter.
func requireParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("query parameter %q is required", name))
		return "", false
	}
	return v, true
}
