package auth

import (
	"bytes"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

// session_cookie_test.go: regression tests for the login-loop bug — the
// manager hardcoded CookieConfig{Secure: true}, so over plain HTTP browsers
// never stored the session cookie and every post-login request was
// anonymous (401 → frontend bounced back to /login). These tests pin the
// fix: Secure follows the cookie_secure mode and the request transport.

// muxRouter adapts Mount's Router interface onto a stdlib ServeMux so the
// login→me flow runs over a real httptest.Server with a real cookie jar.
type muxRouter struct{ mux *http.ServeMux }

func (m *muxRouter) Use(...func(http.Handler) http.Handler) {}
func (m *muxRouter) Get(p string, h http.HandlerFunc)       { m.mux.HandleFunc("GET "+p, h) }
func (m *muxRouter) Post(p string, h http.HandlerFunc)      { m.mux.HandleFunc("POST "+p, h) }
func (m *muxRouter) Put(p string, h http.HandlerFunc)       { m.mux.HandleFunc("PUT "+p, h) }
func (m *muxRouter) Patch(p string, h http.HandlerFunc)     { m.mux.HandleFunc("PATCH "+p, h) }
func (m *muxRouter) Delete(p string, h http.HandlerFunc)    { m.mux.HandleFunc("DELETE "+p, h) }

var _ Router = (*muxRouter)(nil)

// serveAuth mounts the slice with cookie cfg over HTTP or TLS and returns a
// client with a real cookie jar plus the fake auth service.
func serveAuth(t *testing.T, cookie CookieConfig, useTLS bool) (*httptest.Server, *http.Client, *fakeAuth) {
	t.Helper()
	info := adminInfo()
	fa := &fakeAuth{
		localLogin: true,
		loginRes: &authsvc.LoginResult{
			Token: "tok-jar-1", Session: info.Session, User: info.User, Roles: info.Roles,
		},
		info: info,
	}
	fu := &fakeUsers{user: info.User}
	mux := &muxRouter{mux: http.NewServeMux()}
	Mount(mux, Deps{Auth: fa, Users: fu, Cookie: cookie})
	var srv *httptest.Server
	if useTLS {
		srv = httptest.NewTLSServer(mux.mux)
	} else {
		srv = httptest.NewServer(mux.mux)
	}
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := srv.Client()
	client.Jar = jar
	// Never follow redirects (OIDC paths); login/me don't redirect anyway.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return srv, client, fa
}

func postLogin(t *testing.T, client *http.Client, base string) *http.Response {
	t.Helper()
	resp, err := client.Post(base+"/api/auth/login", "application/json",
		strings.NewReader(`{"identifier":"alice","password":"pw-12345678"}`))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp
}

// TestLoginMeOverPlainHTTP is the exact reported bug: login→me over plain
// HTTP in auto mode. On the old code (Secure hardcoded true) the jar drops
// the Secure cookie and /api/auth/me returns 401; with the fix the cookie
// has no Secure attribute and me returns 200.
func TestLoginMeOverPlainHTTP(t *testing.T) {
	srv, client, _ := serveAuth(t, CookieConfig{}, false)

	resp := postLogin(t, client, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login code = %d, want 200", resp.StatusCode)
	}
	setCookie := resp.Header.Get("Set-Cookie")
	if strings.Contains(setCookie, "Secure") {
		t.Fatalf("plain-HTTP auto cookie must not carry Secure: %q", setCookie)
	}
	for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("cookie lost %s: %q", want, setCookie)
		}
	}

	me, err := client.Get(srv.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me request: %v", err)
	}
	defer me.Body.Close()
	_, _ = io.Copy(io.Discard, me.Body)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me code = %d, want 200 (cookie jar round-trip failed)", me.StatusCode)
	}
}

// TestSecureAttributeSensitivity is the negative control for the harness:
// the same plain-HTTP login with mode=on emits a Secure attribute. So the
// "no Secure" assertion in TestLoginMeOverPlainHTTP is sensitive to the
// code path — under the old hardcoded Secure:true it fails. (A jar
// round-trip cannot show the drop: like browsers, Go's jar treats
// loopback as a secure origin and sends Secure cookies to httptest
// servers. On any real non-loopback host a Secure cookie set over plain
// HTTP is dropped and /api/auth/me 401s — the reported loop.)
func TestSecureAttributeSensitivity(t *testing.T) {
	srv, client, _ := serveAuth(t, CookieConfig{SecureMode: SecureOn}, false)

	resp := postLogin(t, client, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login code = %d, want 200", resp.StatusCode)
	}
	if setCookie := resp.Header.Get("Set-Cookie"); !strings.Contains(setCookie, "Secure") {
		t.Fatal("mode=on harness check failed: Secure attribute missing, " +
			"TestLoginMeOverPlainHTTP would be vacuous")
	}
}

// TestLoginOverTLSAutoSetsSecure pins the other half: over a secure
// transport auto mode must set Secure.
func TestLoginOverTLSAutoSetsSecure(t *testing.T) {
	srv, client, _ := serveAuth(t, CookieConfig{}, true)

	resp := postLogin(t, client, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login code = %d, want 200", resp.StatusCode)
	}
	if setCookie := resp.Header.Get("Set-Cookie"); !strings.Contains(setCookie, "Secure") {
		t.Fatalf("TLS auto cookie must carry Secure: %q", setCookie)
	}

	me, err := client.Get(srv.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me request: %v", err)
	}
	defer me.Body.Close()
	_, _ = io.Copy(io.Discard, me.Body)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me code = %d, want 200", me.StatusCode)
	}
}

// TestCookieSecureExplicitModes pins on/off overriding the transport.
func TestCookieSecureExplicitModes(t *testing.T) {
	// on: Secure even over plain HTTP (a real browser would then drop it
	// — that is the operator's explicit choice for strict TLS setups).
	srv, client, _ := serveAuth(t, CookieConfig{SecureMode: SecureOn}, false)
	resp := postLogin(t, client, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login code = %d, want 200", resp.StatusCode)
	}
	if setCookie := resp.Header.Get("Set-Cookie"); !strings.Contains(setCookie, "Secure") {
		t.Fatalf("mode=on must set Secure even over HTTP: %q", setCookie)
	}

	// off: no Secure even over TLS.
	srv, client, _ = serveAuth(t, CookieConfig{SecureMode: SecureOff}, true)
	resp = postLogin(t, client, srv.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login code = %d, want 200", resp.StatusCode)
	}
	if setCookie := resp.Header.Get("Set-Cookie"); strings.Contains(setCookie, "Secure") {
		t.Fatalf("mode=off must not set Secure even over TLS: %q", setCookie)
	}
}

// proxyProbe mimics the platform's transport probe (direct TLS, else
// trusted-proxy X-Forwarded-Proto:https) without importing the sibling
// agents package — this package stays self-contained by design.
func proxyProbe(trustedRemote string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if r.TLS != nil {
			return true
		}
		if !strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
			return false
		}
		host := r.RemoteAddr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		return strings.Trim(host, "[]") == trustedRemote
	}
}

// TestCookieSecureTrustedProxy pins the auto-mode proxy semantics:
// trusted proxy + https signal → Secure; untrusted peer + https signal →
// not Secure (fail closed — the header alone is not trusted).
func TestCookieSecureTrustedProxy(t *testing.T) {
	setup := func(probe func(*http.Request) bool) *fakeRouter {
		r := newFakeRouter()
		info := adminInfo()
		Mount(r, Deps{
			Auth: &fakeAuth{
				localLogin: true,
				loginRes: &authsvc.LoginResult{
					Token: "tok-1", Session: info.Session, User: info.User, Roles: info.Roles,
				},
			},
			Users:  &fakeUsers{user: info.User},
			Cookie: CookieConfig{SecureMode: SecureAuto, IsSecure: probe},
		})
		return r
	}
	loginWith := func(r *fakeRouter, remoteAddr, proto string) *http.Cookie {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"identifier":"alice","password":"pw-12345678"}`))
		req.RemoteAddr = remoteAddr
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		r.call("POST", "/api/auth/login")(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login code = %d", rec.Code)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == "vaultix_session" {
				return c
			}
		}
		t.Fatal("session cookie missing")
		return nil
	}

	probe := proxyProbe("10.0.0.9")
	r := setup(probe)
	if c := loginWith(r, "10.0.0.9:1", "https"); !c.Secure {
		t.Error("trusted proxy + X-Forwarded-Proto:https must set Secure")
	}
	if c := loginWith(r, "203.0.113.7:1", "https"); c.Secure {
		t.Error("UNTRUSTED peer + X-Forwarded-Proto:https must NOT set Secure (fail closed)")
	}
	if c := loginWith(r, "10.0.0.9:1", "http"); c.Secure {
		t.Error("trusted proxy + X-Forwarded-Proto:http must not set Secure")
	}
	if c := loginWith(r, "10.0.0.9:1", ""); c.Secure {
		t.Error("trusted proxy without proto signal must not set Secure")
	}

	// Nil probe: direct-TLS detection only; plain HTTP reads insecure.
	r = setup(nil)
	if c := loginWith(r, "10.0.0.1:1", ""); c.Secure {
		t.Error("nil probe over plain HTTP must not set Secure")
	}
}

// TestLogoutClearMirrorsSecure pins logout/change-password clears carrying
// the same Secure decision as login: a clear without Secure never removes
// a Secure cookie, so the session would linger.
func TestLogoutClearMirrorsSecure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		useTLS  bool
		wantSec bool
	}{
		{"tls set+clear Secure", true, true},
		{"plain set+clear insecure", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, client, fa := serveAuth(t, CookieConfig{}, tc.useTLS)
			resp := postLogin(t, client, srv.URL)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("login code = %d", resp.StatusCode)
			}
			gotSet := strings.Contains(resp.Header.Get("Set-Cookie"), "Secure")
			if gotSet != tc.wantSec {
				t.Fatalf("login Secure = %v, want %v", gotSet, tc.wantSec)
			}

			info := adminInfo()
			fa.info = info
			logout, err := client.Post(srv.URL+"/api/auth/logout", "application/json", nil)
			if err != nil {
				t.Fatalf("logout request: %v", err)
			}
			defer logout.Body.Close()
			_, _ = io.Copy(io.Discard, logout.Body)
			if logout.StatusCode != http.StatusOK {
				t.Fatalf("logout code = %d", logout.StatusCode)
			}
			clearHdr := logout.Header.Get("Set-Cookie")
			gotClear := strings.Contains(clearHdr, "Secure")
			if gotClear != tc.wantSec {
				t.Errorf("logout clear Secure = %v, want %v (must mirror login): %q",
					gotClear, tc.wantSec, clearHdr)
			}
			if !strings.Contains(strings.ToLower(clearHdr), "max-age=0") {
				t.Errorf("logout must expire the cookie: %q", clearHdr)
			}
		})
	}
}

// TestExpiredSessionClearMirrorsSecure pins the middleware clear path
// (invalid session → 401 + clear) mirroring the Secure decision too.
func TestExpiredSessionClearMirrorsSecure(t *testing.T) {
	r := newFakeRouter()
	Mount(r, Deps{
		Auth:   &fakeAuth{infoErr: errBoomTest},
		Users:  &fakeUsers{},
		Cookie: CookieConfig{SecureMode: SecureOn},
	})
	rec := doRequest(r.call("GET", "/api/auth/me"), "GET", "/api/auth/me", "", "stale-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me code = %d, want 401", rec.Code)
	}
	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "vaultix_session" {
			cleared = c
		}
	}
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("invalid session must clear the cookie")
	}
	if !cleared.Secure {
		t.Error("mode=on must clear with Secure so the browser accepts the clear")
	}
}

// TestLoginLogsDecisionWithoutToken pins the debug log: mode + outcome are
// logged, token material never is.
func TestLoginLogsDecisionWithoutToken(t *testing.T) {
	var buf bytes.Buffer
	r := newFakeRouter()
	info := adminInfo()
	Mount(r, Deps{
		Auth: &fakeAuth{
			localLogin: true,
			loginRes: &authsvc.LoginResult{
				Token: "tok-SUPERSECRET", Session: info.Session, User: info.User, Roles: info.Roles,
			},
		},
		Users:  &fakeUsers{user: info.User},
		Cookie: CookieConfig{SecureMode: SecureAuto},
		Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	rec := doRequest(r.call("POST", "/api/auth/login"), "POST", "/api/auth/login",
		`{"identifier":"alice","password":"pw-12345678"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login code = %d", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "mode=auto") || !strings.Contains(logged, "secure=false") {
		t.Errorf("login must log mode + decision at debug: %q", logged)
	}
	if strings.Contains(logged, "tok-SUPERSECRET") {
		t.Errorf("token material must never reach the logs: %q", logged)
	}
}

func TestParseSecureMode(t *testing.T) {
	for in, want := range map[string]SecureMode{
		"": SecureAuto, "auto": SecureAuto, "AUTO": SecureAuto, " auto ": SecureAuto,
		"on": SecureOn, "ON": SecureOn,
		"off": SecureOff, "Off": SecureOff,
	} {
		got, err := ParseSecureMode(in)
		if err != nil || got != want {
			t.Errorf("ParseSecureMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"always", "yes", "1", "secure", "auto!"} {
		if _, err := ParseSecureMode(bad); err == nil {
			t.Errorf("ParseSecureMode(%q) must fail", bad)
		}
	}
}

// TestSecureForDefaults keeps zero-value CookieConfig on auto semantics:
// plain HTTP insecure, direct TLS secure.
func TestSecureForDefaults(t *testing.T) {
	var c CookieConfig
	plain := httptest.NewRequest("GET", "/", nil)
	if c.secureFor(plain) {
		t.Error("zero CookieConfig over plain HTTP must read insecure")
	}
	tlsReq := httptest.NewRequest("GET", "/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !c.secureFor(tlsReq) {
		t.Error("zero CookieConfig over TLS must read secure")
	}
	if c.secureFor(nil) {
		t.Error("nil request must read insecure (fail closed)")
	}
}
