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

const (
	credentialKey ctxKey = iota
	transportSecureKey
)

func credentialFrom(ctx context.Context) *domain.AgentCredential {
	cred, _ := ctx.Value(credentialKey).(*domain.AgentCredential)
	return cred
}

// ContextWithTransportSecure marks whether the agent connection arrived over
// TLS. Key issuance (poll/restore encryption envelopes) reads it: key_b64 is
// minted only on TLS transports, never over plaintext.
func ContextWithTransportSecure(ctx context.Context, secure bool) context.Context {
	return context.WithValue(ctx, transportSecureKey, secure)
}

// TransportSecure reports whether ctx was marked TLS-secured. Missing marks
// (direct service calls, tests) read as insecure: key material is withheld.
func TransportSecure(ctx context.Context) bool {
	secure, _ := ctx.Value(transportSecureKey).(bool)
	return secure
}

// transportSecure reads TLS state from the live request: direct TLS only
// (r.TLS != nil). Terminated-proxies must re-encrypt to the manager; an
// X-Forwarded-Proto claim alone never unlocks key issuance.
func transportSecure(r *http.Request) bool { return r.TLS != nil }

// errNotConfigured marks protocol seams the wiring left unwired. It maps to
// an explicit 501, never a silent no-op.
var errNotConfigured = errors.New("service not configured")

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
	case errors.Is(err, errNotConfigured):
		return http.StatusNotImplemented, "not_configured"
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

// jobIDFromAgentPath parses /api/agent/jobs/{id}[/...] without router help.
func jobIDFromAgentPath(r *http.Request) string {
	return segmentAfter(r, "/api/agent/jobs/")
}

// restoreIDFromPath parses /api/agent/restores/{id}[/...] without router
// help. The bare /api/agent/restores/poll path yields "".
func restoreIDFromPath(r *http.Request) string {
	id := segmentAfter(r, "/api/agent/restores/")
	if id == "poll" {
		return ""
	}
	return id
}

func segmentAfter(r *http.Request, prefix string) string {
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

func requireJobID(r *http.Request) (string, error) {
	id := jobIDFromAgentPath(r)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: job id is required", domain.ErrValidation)
	}
	return id, nil
}

func requireRestoreID(r *http.Request) (string, error) {
	id := restoreIDFromPath(r)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: restore id is required", domain.ErrValidation)
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
