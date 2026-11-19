package http

import (
	"encoding/json"
	nethttp "net/http"

	"github.com/vaultix/vaultix/internal/adapter/http/middleware"
)

// ErrorEnvelope is the JSON error shape for all API failures.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the machine-readable code, a human message safe for
// clients, and the request ID for log correlation.
type ErrorBody struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON encodes v as JSON with the given status.
func WriteJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an ErrorEnvelope. message must be safe for clients
// (no internal details); use the request ID to correlate with logs.
func WriteError(w nethttp.ResponseWriter, r *nethttp.Request, status int, message string) {
	WriteJSON(w, status, ErrorEnvelope{Error: ErrorBody{
		Code:      status,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
	}})
}

// RequestID returns the request ID for r, or "" when the middleware did not
// run (e.g. direct handler tests).
func RequestID(r *nethttp.Request) string {
	return middleware.GetReqID(r.Context())
}
