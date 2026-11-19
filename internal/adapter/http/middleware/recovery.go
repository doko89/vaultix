package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery converts handler panics into JSON 500 responses. The panic value
// and stack go to the log only; clients get a generic message plus the
// request ID for correlation.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("http handler panic",
						"request_id", GetReqID(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", fmt.Sprint(rec),
						"stack", string(debug.Stack()),
					)
					body := fmt.Sprintf(`{"error":{"code":%d,"message":"internal server error","request_id":%q}}`,
						http.StatusInternalServerError, GetReqID(r.Context()))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(body))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
