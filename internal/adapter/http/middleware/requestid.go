package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// GetReqID returns the request ID from ctx, or "" when unset.
func GetReqID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// RequestID ensures every request has an ID: it passes through an inbound
// X-Request-ID header when present and generates one otherwise, then sets
// the response header and context value for downstream use (logs, error
// envelopes).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("middleware: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(raw[:])
}
