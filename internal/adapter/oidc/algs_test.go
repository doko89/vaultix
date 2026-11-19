package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// algs_test.go: signature algorithms, client-auth fallback, discovery and
// JWKS failure modes, and userinfo merging.

// ecIdP serves EC-signed tokens for a given curve/alg.
type ecIdP struct {
	t      *testing.T
	server *httptest.Server
	key    *ecdsa.PrivateKey
	issuer string
	alg    string
	crv    string
}

func newECIdP(t *testing.T, curve elliptic.Curve, alg, crv string) *ecIdP {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &ecIdP{t: t, key: key, alg: alg, crv: crv}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.issuer, "authorization_endpoint": f.server.URL + "/auth",
			"token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		pub := f.key.Public().(*ecdsa.PublicKey)
		byteLen := (pub.Curve.Params().BitSize + 7) / 8
		x := pub.X.Bytes()
		y := pub.Y.Bytes()
		xb := make([]byte, byteLen)
		yb := make([]byte, byteLen)
		copy(xb[byteLen-len(x):], x)
		copy(yb[byteLen-len(y):], y)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []any{map[string]any{
				"kty": "EC", "kid": "ec1", "alg": f.alg, "crv": f.crv,
				"x": base64.RawURLEncoding.EncodeToString(xb),
				"y": base64.RawURLEncoding.EncodeToString(yb),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now()
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"` + f.alg + `","kid":"ec1","typ":"JWT"}`))
		body, _ := json.Marshal(map[string]any{
			"iss": f.issuer, "aud": "ec-client", "sub": "ec-user",
			"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": "n-ec",
		})
		payload := base64.RawURLEncoding.EncodeToString(body)
		var digest []byte
		switch f.alg {
		case "ES384":
			h := sha512.Sum384([]byte(header + "." + payload))
			digest = h[:]
		default:
			h := sha256.Sum256([]byte(header + "." + payload))
			digest = h[:]
		}
		r, s, err := ecdsa.Sign(rand.Reader, f.key, digest)
		if err != nil {
			f.t.Fatal(err)
		}
		// ASN.1 marshal (r, s).
		rb, sb := r.Bytes(), s.Bytes()
		if len(rb) > 0 && rb[0]&0x80 != 0 {
			rb = append([]byte{0}, rb...)
		}
		if len(sb) > 0 && sb[0]&0x80 != 0 {
			sb = append([]byte{0}, sb...)
		}
		inner := append(append([]byte{0x02, byte(len(rb))}, rb...), append([]byte{0x02, byte(len(sb))}, sb...)...)
		sig := append([]byte{0x30, byte(len(inner))}, inner...)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer",
			"id_token": header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(sig),
		})
	})
	f.server = httptest.NewServer(mux)
	f.issuer = f.server.URL
	return f
}

func TestECAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		curve elliptic.Curve
		alg   string
		crv   string
	}{
		{elliptic.P256(), "ES256", "P-256"},
		{elliptic.P384(), "ES384", "P-384"},
	} {
		t.Run(tc.alg, func(t *testing.T) {
			f := newECIdP(t, tc.curve, tc.alg, tc.crv)
			defer f.server.Close()
			c := New(WithHTTPClient(f.server.Client()))
			p := &domain.OIDCProvider{ID: "ec", Name: "ec", Issuer: f.issuer, ClientID: "ec-client"}
			claims, err := c.Exchange(ctxForTest(t), p, "code", "ver", "https://cb", "n-ec")
			if err != nil {
				t.Fatalf("Exchange(%s): %v", tc.alg, err)
			}
			if claims.Subject != "ec-user" {
				t.Fatalf("sub = %q", claims.Subject)
			}
		})
	}
}

func TestPSAlgorithms(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, alg := range []string{"PS256", "PS512"} {
		t.Run(alg, func(t *testing.T) {
			// Sign directly and verify unit-style.
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"` + alg + `","kid":"k"}`))
			payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u"}`))
			input := []byte(header + "." + payload)
			var digest []byte
			var h crypto.Hash
			if alg == "PS512" {
				sum := sha512.Sum512(input)
				digest, h = sum[:], crypto.SHA512
			} else {
				sum := sha256.Sum256(input)
				digest, h = sum[:], crypto.SHA256
			}
			sig, err := rsa.SignPSS(rand.Reader, key, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: h})
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySignature(alg, key.Public(), input, sig); err != nil {
				t.Fatalf("verify %s: %v", alg, err)
			}
			bad := append([]byte(nil), sig...)
			bad[0] ^= 0xff
			if err := verifySignature(alg, key.Public(), input, bad); err == nil {
				t.Fatalf("%s tampered signature must fail", alg)
			}
		})
	}
}

func TestRS384Verify(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	input := []byte("header.payload")
	sum := sha512.Sum384(input)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA384, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignature("RS384", key.Public(), input, sig); err != nil {
		t.Fatalf("RS384: %v", err)
	}
	if err := verifySignature("HS256", key.Public(), input, sig); err == nil {
		t.Fatal("unsupported alg must fail")
	}
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err := verifySignature("RS256", ecKey.Public(), input, sig); err == nil {
		t.Fatal("key type mismatch must fail")
	}
}

func TestClientSecretPostFallback(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	// Wrap the token handler: reject basic auth once so the client falls
	// back to client_secret_post.
	orig := f.server.Config.Handler
	var basicSeen int
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if u, _, ok := r.BasicAuth(); ok && u != "" {
				basicSeen++
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		orig.ServeHTTP(w, r)
	})
	const nonce = "n"
	f.mutate = func(claims map[string]any) { claims["nonce"] = nonce }
	c := testClient(f)
	if _, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", nonce); err != nil {
		t.Fatalf("exchange with post fallback: %v", err)
	}
	if basicSeen != 1 || f.seenAuth != "post" {
		t.Fatalf("basicSeen=%d post=%q", basicSeen, f.seenAuth)
	}
}

func TestAudArrayAndUserinfoMerge(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	const nonce = "n"
	f.mutate = func(claims map[string]any) {
		claims["nonce"] = nonce
		claims["aud"] = []any{"other", f.clientID} // array audience
		delete(claims, "email")                    // force userinfo merge
		delete(claims, "name")
		delete(claims, "groups")
	}
	// userinfo gains groups for the merge path.
	orig := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/userinfo" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "user-123", "email": "merged@example.com",
				"preferred_username": "mergednick", "groups": []any{"ops"},
			})
			return
		}
		orig.ServeHTTP(w, r)
	})
	c := testClient(f)
	claims, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", nonce)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if claims.Email != "merged@example.com" || claims.Name != "mergednick" {
		t.Fatalf("merged: %+v", claims)
	}
	if len(claims.Groups) != 1 || claims.Groups[0] != "ops" {
		t.Fatalf("merged groups: %+v", claims.Groups)
	}
}

func TestUserinfoFailureIsNonFatal(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	const nonce = "n"
	f.mutate = func(claims map[string]any) {
		claims["nonce"] = nonce
		delete(claims, "email")
	}
	orig := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/userinfo" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		orig.ServeHTTP(w, r)
	})
	c := testClient(f)
	claims, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", nonce)
	if err != nil {
		t.Fatalf("userinfo failure must be non-fatal: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("claims: %+v", claims)
	}
}

func TestDiscoveryFailures(t *testing.T) {
	ctx := ctxForTest(t)
	// Issuer mismatch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": "https://other.example", "authorization_endpoint": "http://x/a",
			"token_endpoint": "http://x/t", "jwks_uri": "http://x/j",
		})
	}))
	defer srv.Close()
	c := New(WithHTTPClient(srv.Client()))
	p := &domain.OIDCProvider{ID: "m", Name: "m", Issuer: "https://claimed.example", ClientID: "c", DiscoveryURL: srv.URL}
	if _, err := c.EnsureDiscovered(ctx, p); err == nil {
		t.Fatal("issuer mismatch must fail")
	}
	// Missing endpoints.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://e.example"})
	}))
	defer srv2.Close()
	c2 := New(WithHTTPClient(srv2.Client()))
	p2 := &domain.OIDCProvider{ID: "e", Name: "e", Issuer: "https://e.example", ClientID: "c", DiscoveryURL: srv2.URL}
	if _, err := c2.EnsureDiscovered(ctx, p2); err == nil {
		t.Fatal("missing endpoints must fail")
	}
	// Invalid JSON.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{oops"))
	}))
	defer srv3.Close()
	c3 := New(WithHTTPClient(srv3.Client()))
	p3 := &domain.OIDCProvider{ID: "j", Name: "j", Issuer: "https://j.example", ClientID: "c", DiscoveryURL: srv3.URL}
	if _, err := c3.EnsureDiscovered(ctx, p3); err == nil {
		t.Fatal("invalid discovery JSON must fail")
	}
	// Discovery cache hit: second call issues no fetch.
	f := newFakeIdP(t)
	defer f.close()
	c4 := testClient(f)
	if _, err := c4.EnsureDiscovered(ctx, f.provider()); err != nil {
		t.Fatal(err)
	}
	f.server.Close() // kill the server; cache must serve the second call
	if _, err := c4.EnsureDiscovered(ctx, f.provider()); err != nil {
		t.Fatalf("cached discovery: %v", err)
	}
}

func TestJWKSFailures(t *testing.T) {
	ctx := ctxForTest(t)
	// JWKS invalid JSON.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	c := New(WithHTTPClient(srv.Client()))
	if _, err := c.fetchJWKS(ctx, srv.URL); err == nil {
		t.Fatal("invalid JWKS JSON must fail")
	}
	// JWKS with no keys.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}))
	defer srv2.Close()
	c2 := New(WithHTTPClient(srv2.Client()))
	if _, err := c2.fetchJWKS(ctx, srv2.URL); err == nil {
		t.Fatal("empty JWKS must fail")
	}
}

func TestTestProviderFailures(t *testing.T) {
	ctx := ctxForTest(t)
	c := New()
	p := &domain.OIDCProvider{ID: "d", Name: "d", Issuer: "https://dead.example", ClientID: "c", DiscoveryURL: "http://127.0.0.1:1/x"}
	rep, err := c.TestProvider(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("unreachable discovery must report !OK")
	}
	// JWKS unreachable after good discovery.
	f := newFakeIdP(t)
	defer f.close()
	cc := testClient(f)
	prov := f.provider()
	prov.JWKSURL = "http://127.0.0.1:1/jwks"
	rep, err = cc.TestProvider(ctx, prov)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Error != "jwks unreachable" {
		t.Fatalf("report: %+v", rep)
	}
}

func TestLogoutURLFromCachedDiscovery(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	c := testClient(f)
	p := f.provider() // no EndSession override
	if _, err := c.EnsureDiscovered(ctxForTest(t), p); err != nil {
		t.Fatal(err)
	}
	got := c.LogoutURL(p, "https://app/bye")
	if !strings.HasPrefix(got, f.server.URL+"/logout") || !strings.Contains(got, "post_logout_redirect_uri=") {
		t.Fatalf("logout URL = %q", got)
	}
	bad := &domain.OIDCProvider{ID: "b", EndSession: "://bad-url"}
	if got := c.LogoutURL(bad, ""); got != "" {
		t.Fatalf("bad end_session = %q", got)
	}
}

func TestAuthURLErrors(t *testing.T) {
	c := New()
	p := &domain.OIDCProvider{ID: "x", AuthURL: "https://idp.example/auth"}
	if _, err := c.AuthURL(p, "", "n", "cc", "https://cb"); err == nil {
		t.Fatal("empty state must fail")
	}
	bad := &domain.OIDCProvider{ID: "y", AuthURL: "://bad"}
	if _, err := c.AuthURL(bad, "s", "n", "cc", "https://cb"); err == nil {
		t.Fatal("bad auth URL must fail")
	}
}

func TestUnknownKidFailsClosed(t *testing.T) {
	f := newFakeIdP(t)
	defer f.close()
	f.tokenKid = "unknown-kid" // token kid the JWKS never publishes
	const nonce = "n"
	f.mutate = func(claims map[string]any) { claims["nonce"] = nonce }
	c := testClient(f)
	if _, err := c.Exchange(ctxForTest(t), f.provider(), "code", "ver", "https://cb", nonce); err == nil {
		t.Fatal("unknown kid must fail closed")
	}
}

func TestGetRetryOnServerError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://r.example"})
	}))
	defer srv.Close()
	c := New(WithHTTPClient(srv.Client()))
	body, err := c.getRetry(ctxForTest(t), srv.URL)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !strings.Contains(string(body), "r.example") || calls != 2 {
		t.Fatalf("calls=%d body=%s", calls, body)
	}
}

func TestJWKFindMismatches(t *testing.T) {
	set := &jwkSet{Keys: []jwk{
		{Kty: "RSA", Kid: "a", Alg: "RS256"},                  // missing n/e
		{Kty: "EC", Kid: "b", Crv: "P-521", X: "eA", Y: "eQ"}, // unsupported curve
		{Kty: "RSA", Kid: "c", Alg: "RS256", N: "!!!", E: "!!!"},
	}}
	if _, err := set.find("missing", "RS256"); err == nil {
		t.Fatal("unknown kid must fail")
	}
	if _, err := set.find("", "RS256"); err == nil {
		t.Fatal("unusable keys must fail")
	}
}
