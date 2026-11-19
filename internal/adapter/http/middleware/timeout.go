package middleware

import (
	"net/http"
	"time"
)

// Timeout bounds each request's handler execution. Overruns return 503 with
// a JSON body; handlers should honor r.Context() cancellation for prompt
// termination. A non-positive timeout disables the middleware.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.TimeoutHandler(next, d, `{"error":{"code":503,"message":"request timed out"}}`)
	}
}
