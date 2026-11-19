package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

func ctxForTest(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// roundTripFail builds a client transport that fails every request.
func roundTripFail(t *testing.T, msg string) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(msg)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeIdP is a minimal OpenID provider for tests.
type fakeIdP struct {
	t        *testing.T
	server   *httptest.Server
	key      *rsa.PrivateKey
	kid      string
	issuer   string
	clientID string
	// mutate lets tests break the id_token per-case.
	mutate func(claims map[string]any)
	// rotate, when set, swaps the JWKS key (rotation test).
	rotate *rsa.PrivateKey
	// signWith, when set, signs tokens with a different key than the
	// JWKS publishes (bad-signature test).
	signWith *rsa.PrivateKey
	// tokenKid, when set, overrides the kid in the token header.
	tokenKid string
	// tokenStatus overrides the token endpoint status.
	tokenStatus int
	// seenAuth records the last token request auth mode.
	seenAuth string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, kid: "k1", clientID: "vaultix-test-client"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.serveDiscovery)
	mux.HandleFunc("/jwks", f.serveJWKS)
	mux.HandleFunc("/token", f.serveToken)
	mux.HandleFunc("/userinfo", f.serveUserinfo)
	f.server = httptest.NewServer(mux)
	f.issuer = f.server.URL
	return f
}

func (f *fakeIdP) close() { f.server.Close() }

func (f *fakeIdP) provider() *domain.OIDCProvider {
	return &domain.OIDCProvider{
		ID: "prov-1", Name: "test-idp", Issuer: f.issuer,
		ClientID: f.clientID, GroupClaimNames: []string{"groups"},
		GroupRoleMapping: map[string]string{"admins": domain.RoleAdministrator},
	}
}

func (f *fakeIdP) serveDiscovery(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 f.issuer,
		"authorization_endpoint": f.server.URL + "/auth",
		"token_endpoint":         f.server.URL + "/token",
		"jwks_uri":               f.server.URL + "/jwks",
		"userinfo_endpoint":      f.server.URL + "/userinfo",
		"end_session_endpoint":   f.server.URL + "/logout",
	})
}

func (f *fakeIdP) activeKey() *rsa.PrivateKey {
	if f.rotate != nil {
		return f.rotate
	}
	return f.key
}

func (f *fakeIdP) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := f.activeKey().Public().(*rsa.PublicKey)
	e := big.NewInt(int64(pub.E)).Bytes()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []any{map[string]any{
			"kty": "RSA", "use": "sig", "kid": f.kid, "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}},
	})
}

func (f *fakeIdP) serveToken(w http.ResponseWriter, r *http.Request) {
	if f.tokenStatus != 0 {
		w.WriteHeader(f.tokenStatus)
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if u, _, ok := r.BasicAuth(); ok && u != "" {
		f.seenAuth = "basic"
	} else if r.Form.Get("client_secret") != "" {
		f.seenAuth = "post"
	}
	now := time.Now()
	claims := map[string]any{
		"iss": f.issuer, "aud": f.clientID, "sub": "user-123",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"nonce": r.Form.Get("nonce_hint"),
		"email": "oidc-user@example.com", "name": "OIDC User",
		"groups": []string{"admins"},
	}
	if f.mutate != nil {
		f.mutate(claims)
	}
	idToken := f.mint(claims)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token", "token_type": "Bearer",
		"expires_in": 3600, "id_token": idToken,
	})
}

func (f *fakeIdP) serveUserinfo(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sub": "user-123", "email": "oidc-user@example.com", "name": "OIDC User",
	})
}

func (f *fakeIdP) mint(claims map[string]any) string {
	kid := f.kid
	if f.tokenKid != "" {
		kid = f.tokenKid
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"` + kid + `","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	key := f.activeKey()
	if f.signWith != nil {
		key = f.signWith
	}
	h := sha256.Sum256([]byte(header + "." + payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testClient(f *fakeIdP) *Client {
	return New(
		WithHTTPClient(f.server.Client()),
		WithSecretResolver(func(context.Context, *domain.OIDCProvider) (string, error) {
			return "test-secret", nil
		}),
	)
}

func TestDiscoveryAndAuthURL(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	c := testClient(f)
	p := f.provider()
	if _, err := c.EnsureDiscovered(ctxForTest(t), p); err != nil {
		t.Fatalf("discovery: %v", err)
	}
	authURL, err := c.AuthURL(p, "state-1", "nonce-1", "challenge-1", "https://vaultix.local/cb")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	for _, want := range []string{
		"response_type=code", "client_id=" + f.clientID,
		"state=state-1", "nonce=nonce-1",
		"code_challenge=challenge-1", "code_challenge_method=S256",
		"scope=openid", "redirect_uri=",
	} {
		if !strings.Contains(authURL, want) {
			t.Fatalf("auth URL missing %q: %s", want, authURL)
		}
	}
}

func TestAuthURLRequiresDiscovery(t *testing.T) {
	c := New()
	p := &domain.OIDCProvider{ID: "x", Issuer: "https://idp.example", ClientID: "c"}
	if _, err := c.AuthURL(p, "s", "n", "cc", "https://cb"); err != ErrDiscoveryRequired {
		t.Fatalf("expected ErrDiscoveryRequired, got %v", err)
	}
}

func TestManualOverrideSkipsNetwork(t *testing.T) {
	c := New(WithHTTPClient(&http.Client{
		Transport: roundTripFail(t, "no network expected with full overrides"),
	}))
	p := &domain.OIDCProvider{
		ID: "p", Name: "manual", Issuer: "https://idp.example", ClientID: "c",
		AuthURL: "https://idp.example/auth", TokenURL: "https://idp.example/token",
		JWKSURL: "https://idp.example/jwks", EndSession: "https://idp.example/logout",
	}
	ep, err := c.EnsureDiscovered(ctxForTest(t), p)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Authorization != p.AuthURL || ep.Token != p.TokenURL || ep.JWKS != p.JWKSURL {
		t.Fatalf("overrides not honored: %+v", ep)
	}
	if got := c.LogoutURL(p, "https://app/bye"); !strings.HasPrefix(got, p.EndSession) {
		t.Fatalf("logout URL: %q", got)
	}
}

func TestExchangeSuccess(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	// The fake IdP echoes nonce_hint from the token form, but our client
	// never sends it; instead mutate to a fixed nonce the test expects.
	const nonce = "expected-nonce"
	f.mutate = func(claims map[string]any) { claims["nonce"] = nonce }
	c := testClient(f)
	claims, err := c.Exchange(ctxForTest(t), f.provider(), "code-1", "verifier-1", "https://vaultix.local/cb", nonce)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if claims.Subject != "user-123" || claims.Email != "oidc-user@example.com" {
		t.Fatalf("claims: %+v", claims)
	}
	if len(claims.Groups) != 1 || claims.Groups[0] != "admins" {
		t.Fatalf("groups: %+v", claims.Groups)
	}
	if f.seenAuth != "basic" {
		t.Fatalf("expected basic auth on token endpoint, got %q", f.seenAuth)
	}
}

func TestExchangeRejects(t *testing.T) {
	const nonce = "expected-nonce"
	cases := map[string]func(f *fakeIdP, c *Client) (string, string){
		"nonce mismatch": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) { claims["nonce"] = "other" }
			return nonce, "nonce"
		},
		"expired": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) {
				claims["nonce"] = nonce
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			}
			return nonce, "expired"
		},
		"wrong audience": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) {
				claims["nonce"] = nonce
				claims["aud"] = "someone-else"
			}
			return nonce, "audience"
		},
		"wrong issuer": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) {
				claims["nonce"] = nonce
				claims["iss"] = "https://evil.example"
			}
			return nonce, "issuer"
		},
		"future iat": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) {
				claims["nonce"] = nonce
				claims["iat"] = time.Now().Add(time.Hour).Unix()
			}
			return nonce, "future"
		},
		"missing sub": func(f *fakeIdP, _ *Client) (string, string) {
			f.mutate = func(claims map[string]any) {
				claims["nonce"] = nonce
				delete(claims, "sub")
			}
			return nonce, "sub"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeIdP(t)
			defer f.close()
			c := testClient(f)
			wantNonce, wantSub := setup(f, c)
			_, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", wantNonce)
			if err == nil {
				t.Fatalf("%s: expected rejection", name)
			}
			if !strings.Contains(err.Error(), wantSub) {
				t.Fatalf("%s: error %q should mention %q", name, err, wantSub)
			}
		})
	}
}

func TestExchangeRejectsBadSignature(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.signWith = other // sign with a key the JWKS does not publish
	const nonce = "n"
	f.mutate = func(claims map[string]any) { claims["nonce"] = nonce }
	c := testClient(f)
	if _, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", nonce); err == nil {
		t.Fatal("expected bad-signature rejection")
	}
}

func TestJWKSRotationRefresh(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	const nonce = "n"
	f.mutate = func(claims map[string]any) { claims["nonce"] = nonce }
	c := testClient(f)
	ctx := ctxForTest(t)
	if _, err := c.Exchange(ctx, f.provider(), "c1", "v1", "https://cb", nonce); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	// Rotate: new key signs, JWKS publishes only the new key, same kid.
	rotated, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.rotate = rotated
	if _, err := c.Exchange(ctx, f.provider(), "c2", "v2", "https://cb", nonce); err != nil {
		t.Fatalf("exchange after rotation (refresh expected): %v", err)
	}
}

func TestExchangeTokenEndpointError(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	f.tokenStatus = http.StatusBadRequest
	c := testClient(f)
	_, err := c.Exchange(ctxForTest(t), f.provider(), "bad-code", "v", "https://cb", "n")
	if err == nil {
		t.Fatal("expected token-endpoint error")
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error leaks secret: %v", err)
	}
}

func TestTestProvider(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	c := testClient(f)
	rep, err := c.TestProvider(ctxForTest(t), f.provider())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.KeyCount != 1 || !rep.HasUserInfo || !rep.HasEndSession {
		t.Fatalf("unexpected report: %+v", rep)
	}
	out, _ := json.Marshal(rep)
	if strings.Contains(string(out), "test-secret") {
		t.Fatalf("report leaks secret: %s", out)
	}
}

func TestLogoutURLWithoutEndSession(t *testing.T) {
	c := New()
	p := &domain.OIDCProvider{ID: "no-disc", Issuer: "https://idp.example", ClientID: "c"}
	if got := c.LogoutURL(p, "https://app"); got != "" {
		t.Fatalf("expected empty logout URL, got %q", got)
	}
}

func TestGroupClaimDotPath(t *testing.T) {
	p := &domain.OIDCProvider{GroupClaimNames: []string{"realm_access.roles", "groups"}}
	claims := map[string]any{
		"sub": "u", "realm_access": map[string]any{"roles": []any{"a", "b"}},
		"groups": "solo",
	}
	got := extractClaims(p, claims)
	if len(got.Groups) != 3 {
		t.Fatalf("groups = %v, want 3", got.Groups)
	}
}
