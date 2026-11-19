package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// CSRF guards cookie-authenticated mutating requests against cross-site
// request forgery. Requests are rejected with 403 unless one of holds:
//   - the method is safe (GET/HEAD/OPTIONS/TRACE),
//   - no session cookie is present (non-browser clients such as agents
//     authenticate with bearer tokens and carry no cookies),
//   - the Origin header matches the request host (same-origin fetch/XHR),
//   - or an X-Requested-With header is present (non-forgeable by simple
//     cross-site forms in modern browsers).
//
// The middleware is deliberately cookie-aware rather than global: agent
// protocol and other token-authed calls pass through untouched.
func CSRF(cookieName string) func(http.Handler) http.Handler {
	if cookieName == "" {
		cookieName = "vaultix_session"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !needsCSRFCheck(r, cookieName) {
				next.ServeHTTP(w, r)
				return
			}
			if !originMatches(r) && strings.TrimSpace(r.Header.Get("X-Requested-With")) == "" {
				writeCSRFDenial(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func needsCSRFCheck(r *http.Request, cookieName string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	c, err := r.Cookie(cookieName)
	return err == nil && c.Value != ""
}

// originMatches reports whether the Origin header identifies the request's
// own host. A missing or unparsable Origin never matches.
func originMatches(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func writeCSRFDenial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":403,"message":"cross-site request rejected","request_id":"` + GetReqID(r.Context()) + `"}}`))
}
