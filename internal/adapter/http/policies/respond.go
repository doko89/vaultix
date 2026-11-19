package policies

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// maxBodyBytes bounds request bodies (1 MiB; policy/schedule DTOs are small).
const maxBodyBytes = 1 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// decode parses a JSON request body strictly (unknown fields rejected).
func decode(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("empty request body")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// writeJSON renders v with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps domain sentinels to status codes. Only validation,
// not-found, and conflict details are exposed; everything else is a
// generic 500 so internals never leak.
func writeError(w http.ResponseWriter, err error) {
	var ve *validationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: errorDetail{Code: "validation_failed", Message: ve.Error()}})
		return
	}
	switch {
	case errors.Is(err, domain.ErrValidation):
		writeJSON(w, http.StatusBadRequest, errorBody{Error: errorDetail{Code: "validation_failed", Message: err.Error()}})
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{Error: errorDetail{Code: "not_found", Message: "not found"}})
	case errors.Is(err, domain.ErrConflict):
		writeJSON(w, http.StatusConflict, errorBody{Error: errorDetail{Code: "conflict", Message: messageOf(err)}})
	case errors.Is(err, domain.ErrForbidden):
		writeJSON(w, http.StatusForbidden, errorBody{Error: errorDetail{Code: "forbidden", Message: "forbidden"}})
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: errorDetail{Code: "internal", Message: "internal error"}})
	}
}

// messageOf strips the sentinel prefix for conflict details.
func messageOf(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// validationError is a DTO-layer validation failure (always 400).
type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

// actor returns the requesting actor for audit attribution. Until the auth
// track lands middleware, callers may pass X-Actor; empty is allowed.
func actor(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Actor"))
}

// pagination parses limit/offset query params with sane bounds.
func pagination(r *http.Request) (domain.ListOptions, error) {
	opts := domain.ListOptions{Limit: 50}
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return opts, badRequest("limit must be 1..200")
		}
		opts.Limit = n
	}
	if v := strings.TrimSpace(q.Get("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return opts, badRequest("offset must be >= 0")
		}
		opts.Offset = n
	}
	opts.Query = strings.TrimSpace(q.Get("q"))
	return opts, nil
}

// versionParam parses a {v} path version number.
func versionParam(r *http.Request) (int, error) {
	v, err := strconv.Atoi(r.PathValue("v"))
	if err != nil || v <= 0 {
		return 0, badRequest("version must be a positive integer")
	}
	return v, nil
}
