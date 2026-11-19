package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

const (
	// maxBodyBytes caps request bodies (discovery reports can be large).
	maxBodyBytes = 4 << 20
	// defaultPageLimit / maxPageLimit bound UI list pagination.
	defaultPageLimit = 50
	maxPageLimit     = 200
	// defaultPollLimit / maxPollLimit bound agent job polling.
	defaultPollLimit = 10
	maxPollLimit     = 50
)

var errBodyTooLarge = errors.New("request body too large")

type ctxKey int

const credentialKey ctxKey = iota

func credentialFrom(ctx context.Context) *domain.AgentCredential {
	cred, _ := ctx.Value(credentialKey).(*domain.AgentCredential)
	return cred
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// statusFor maps domain/usecase errors to HTTP status + stable code.
func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, agentuc.ErrInvalidToken),
		errors.Is(err, agentuc.ErrExpiredToken),
		errors.Is(err, agentuc.ErrTokenReused),
		errors.Is(err, agentuc.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, agentuc.ErrAgentRevoked),
		errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation_error"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, errBodyTooLarge):
		return http.StatusRequestEntityTooLarge, "body_too_large"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		// Never leak internals; the log keeps the detail. Plaintext
		// credentials never reach here — usecase errors carry none.
		h.log.Error("agents request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
		msg = "internal error"
	}
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a required JSON body with a size cap. Validation failures
// wrap domain.ErrValidation so they map to 400.
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("%w: request body is required", domain.ErrValidation)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read request body: %v", domain.ErrValidation, err)
	}
	if len(raw) > maxBodyBytes {
		return errBodyTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("%w: request body is required", domain.ErrValidation)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: malformed JSON: %v", domain.ErrValidation, err)
	}
	return nil
}

// check runs validator tags and wraps failures as validation errors.
func (h *handler) check(v any) error {
	if err := h.validate.Struct(v); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return nil
}

// bearerToken extracts a Bearer credential; "" when missing or malformed.
func bearerToken(r *http.Request) string {
	head := r.Header.Get("Authorization")
	if head == "" {
		return ""
	}
	scheme, token, _ := strings.Cut(head, " ")
	if !strings.EqualFold(strings.TrimSpace(scheme), "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// agentIDFromPath parses /api/agents/{id}[/...] without router help, so the
// handlers work under chi (":id" patterns) or any compatible router.
func agentIDFromPath(r *http.Request) string {
	const prefix = "/api/agents/"
	path := r.URL.EscapedPath()
	if path == "" {
		path = r.URL.Path
	}
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok || rest == "" {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	unescaped, err := url.PathUnescape(id)
	if err != nil {
		return ""
	}
	return unescaped
}

func requireAgentID(r *http.Request) (string, error) {
	id := agentIDFromPath(r)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: agent id is required", domain.ErrValidation)
	}
	return id, nil
}

func parseLimitOffset(q url.Values, def, max int) (limit, offset int, err error) {
	limit = def
	offset = 0
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > max {
			return 0, 0, fmt.Errorf("%w: limit must be 1..%d", domain.ErrValidation, max)
		}
	}
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("%w: offset must be >= 0", domain.ErrValidation)
		}
	}
	return limit, offset, nil
}

// validJobState reports whether s names a known lifecycle state.
func validJobState(s string) bool {
	switch domain.JobState(s) {
	case domain.JobQueued, domain.JobAssigned, domain.JobRunning,
		domain.JobUploading, domain.JobVerifying, domain.JobSucceeded,
		domain.JobFailed, domain.JobCancelled:
		return true
	default:
		return false
	}
}
