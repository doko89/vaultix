package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/vaultix/vaultix/internal/domain"
	auth "github.com/vaultix/vaultix/internal/usecase/auth"
)

const (
	// httpTimeout bounds every IdP call (TECH_SPEC §3).
	httpTimeout = 10 * time.Second
	// discoveryCacheTTL bounds cached discovery documents and JWKS.
	discoveryCacheTTL = time.Hour
	// jwksCacheTTL bounds cached signing keys between rotations.
	jwksCacheTTL = time.Hour
	// defaultLeeway tolerates small clock skew on exp/iat checks.
	defaultLeeway = 30 * time.Second
	// maxIDPBody caps IdP response bodies.
	maxIDPBody = 1 << 20
)

var (
	// ErrDiscoveryRequired means AuthURL was called before discovery and no
	// manual endpoint override exists; call EnsureDiscovered first.
	ErrDiscoveryRequired = errors.New("oidc: provider discovery required")
	// ErrInvalidToken means the ID token failed validation. The message
	// carries the reason class only — never token content.
	ErrInvalidToken = errors.New("oidc: invalid id_token")
)

// SecretResolver returns the decrypted client secret for a provider at
// token-exchange time. The usecase layer wires it to the encrypted secret
// store; returning "" selects the public-client (PKCE-only) path.
type SecretResolver func(ctx context.Context, p *domain.OIDCProvider) (string, error)

// Endpoints are the effective IdP endpoints after override.
type Endpoints struct {
	Authorization string
	Token         string
	JWKS          string
	UserInfo      string
	EndSession    string
}

// Client implements domain.OIDCClient.
type Client struct {
	http     *http.Client
	resolve  SecretResolver
	now      func() time.Time
	leeway   time.Duration
	mu       sync.Mutex
	discover map[string]*cachedDiscovery
	jwks     map[string]*cachedJWKS
}

type cachedDiscovery struct {
	doc Endpoints
	at  time.Time
}

type cachedJWKS struct {
	set *jwkSet
	at  time.Time
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default 10s-timeout client (tests).
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.http = c }
}

// WithSecretResolver wires client-secret lookup for the token exchange.
func WithSecretResolver(r SecretResolver) Option {
	return func(cl *Client) { cl.resolve = r }
}

// WithNow overrides the clock (tests).
func WithNow(fn func() time.Time) Option {
	return func(cl *Client) { cl.now = fn }
}

// New builds a Client.
func New(opts ...Option) *Client {
	c := &Client{
		http:     &http.Client{Timeout: httpTimeout},
		now:      time.Now,
		leeway:   defaultLeeway,
		discover: map[string]*cachedDiscovery{},
		jwks:     map[string]*cachedJWKS{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

var _ domain.OIDCClient = (*Client)(nil)

// discoveryDocument is the subset of openid-configuration we consume.
type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// discoveryCacheKey prefers the stable provider ID, else the issuer.
func discoveryCacheKey(p *domain.OIDCProvider) string {
	if p.ID != "" {
		return "id:" + p.ID
	}
	return "iss:" + p.Issuer
}

// discoveryURL returns the effective discovery document URL.
func discoveryURL(p *domain.OIDCProvider) string {
	if strings.TrimSpace(p.DiscoveryURL) != "" {
		return strings.TrimSpace(p.DiscoveryURL)
	}
	return strings.TrimRight(p.Issuer, "/") + "/.well-known/openid-configuration"
}

// EnsureDiscovered fetches and caches the provider's discovery document
// (unless every endpoint is manually overridden) and returns the effective
// endpoints. Call it before AuthURL/Exchange when overrides are absent.
func (c *Client) EnsureDiscovered(ctx context.Context, p *domain.OIDCProvider) (*Endpoints, error) {
	key := discoveryCacheKey(p)
	c.mu.Lock()
	if d, ok := c.discover[key]; ok && c.now().Sub(d.at) < discoveryCacheTTL {
		defer c.mu.Unlock()
		ep := d.doc
		return &ep, nil
	}
	c.mu.Unlock()

	if p.AuthURL != "" && p.TokenURL != "" && p.JWKSURL != "" {
		ep := Endpoints{
			Authorization: p.AuthURL, Token: p.TokenURL, JWKS: p.JWKSURL,
			UserInfo: p.UserInfoURL, EndSession: p.EndSession,
		}
		c.mu.Lock()
		c.discover[key] = &cachedDiscovery{doc: ep, at: c.now()}
		c.mu.Unlock()
		out := ep
		return &out, nil
	}

	doc, err := c.fetchDiscovery(ctx, discoveryURL(p))
	if err != nil {
		return nil, err
	}
	// Strict issuer check (PRD §10.2): the document must belong to us.
	if doc.Issuer != p.Issuer {
		return nil, fmt.Errorf("oidc: discovery issuer mismatch for provider %q", p.Name)
	}
	ep := Endpoints{
		Authorization: firstNonEmpty(p.AuthURL, doc.AuthorizationEndpoint),
		Token:         firstNonEmpty(p.TokenURL, doc.TokenEndpoint),
		JWKS:          firstNonEmpty(p.JWKSURL, doc.JWKSURI),
		UserInfo:      firstNonEmpty(p.UserInfoURL, doc.UserinfoEndpoint),
		EndSession:    firstNonEmpty(p.EndSession, doc.EndSessionEndpoint),
	}
	if ep.Authorization == "" || ep.Token == "" || ep.JWKS == "" {
		return nil, fmt.Errorf("oidc: provider %q discovery is missing required endpoints", p.Name)
	}
	c.mu.Lock()
	c.discover[key] = &cachedDiscovery{doc: ep, at: c.now()}
	c.mu.Unlock()
	out := ep
	return &out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (c *Client) fetchDiscovery(ctx context.Context, rawURL string) (*discoveryDocument, error) {
	body, err := c.getRetry(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery fetch failed: %w", err)
	}
	var doc discoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("oidc: discovery document is not valid JSON")
	}
	if doc.Issuer == "" {
		return nil, errors.New("oidc: discovery document has no issuer")
	}
	return &doc, nil
}

// DiscoverProvider warms the discovery cache; it satisfies the optional
// usecase/auth.ProviderDiscoverer seam used before AuthURL.
func (c *Client) DiscoverProvider(ctx context.Context, p *domain.OIDCProvider) error {
	_, err := c.EnsureDiscovered(ctx, p)
	return err
}

// TestProvider runs discovery plus a JWKS reachability check without
// requiring user login, so admins can validate config before activation.
// It never transmits or returns secrets.
// TestProvider implements usecase/auth.ProviderTester: the report type lives
// in the usecase so both the service and HTTP layers share it.
func (c *Client) TestProvider(ctx context.Context, p *domain.OIDCProvider) (*auth.ProviderTestReport, error) {
	ep, err := c.EnsureDiscovered(ctx, p)
	if err != nil {
		return &auth.ProviderTestReport{OK: false, Error: err.Error()}, nil
	}
	set, err := c.fetchJWKS(ctx, ep.JWKS)
	if err != nil {
		return &auth.ProviderTestReport{OK: false, Error: "jwks unreachable"}, nil
	}
	return &auth.ProviderTestReport{
		OK:               true,
		Issuer:           p.Issuer,
		AuthorizationURL: ep.Authorization,
		TokenURL:         ep.Token,
		JWKSURL:          ep.JWKS,
		HasUserInfo:      ep.UserInfo != "",
		HasEndSession:    ep.EndSession != "",
		KeyCount:         len(set.Keys),
	}, nil
}

// AuthURL builds the authorization URL (code flow + PKCE; state/nonce
// supplied by the caller). It needs prior EnsureDiscovered unless the auth
// endpoint is manually overridden.
func (c *Client) AuthURL(p *domain.OIDCProvider, state, nonce, codeChallenge, redirectURI string) (string, error) {
	if state == "" || nonce == "" || codeChallenge == "" || redirectURI == "" {
		return "", errors.New("oidc: state, nonce, code_challenge, and redirect_uri are required")
	}
	authURL := strings.TrimSpace(p.AuthURL)
	if authURL == "" {
		c.mu.Lock()
		d, ok := c.discover[discoveryCacheKey(p)]
		c.mu.Unlock()
		if !ok {
			return "", ErrDiscoveryRequired
		}
		authURL = d.doc.Authorization
	}
	u, err := url.Parse(authURL)
	if err != nil {
		return "", errors.New("oidc: provider authorization endpoint is not a valid URL")
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(p.ScopesOrDefault(), " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// tokenResponse is the subset of the RFC 6749 access-token response we use.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// Exchange validates the callback: code-for-token swap, strict ID-token
// verification (signature, iss, aud, exp, iat, nonce), and claim extraction.
func (c *Client) Exchange(ctx context.Context, p *domain.OIDCProvider, code, codeVerifier, redirectURI, expectedNonce string) (*domain.OIDCClaims, error) {
	if code == "" || codeVerifier == "" || redirectURI == "" || expectedNonce == "" {
		return nil, fmt.Errorf("%w: missing callback parameters", ErrInvalidToken)
	}
	ep, err := c.EnsureDiscovered(ctx, p)
	if err != nil {
		return nil, err
	}
	var secret string
	if c.resolve != nil {
		secret, err = c.resolve(ctx, p)
		if err != nil {
			return nil, errors.New("oidc: client secret unavailable")
		}
	}
	tok, err := c.swapCode(ctx, ep.Token, p.ClientID, secret, code, codeVerifier, redirectURI)
	if err != nil {
		return nil, err
	}
	if tok.IDToken == "" {
		return nil, fmt.Errorf("%w: response has no id_token", ErrInvalidToken)
	}
	verified, err := c.verifyIDToken(ctx, p, ep, tok.IDToken, expectedNonce)
	if err != nil {
		return nil, err
	}
	c.mergeUserinfo(ctx, p, ep, tok.AccessToken, verified)
	return verified, nil
}

func (c *Client) swapCode(ctx context.Context, tokenURL, clientID, secret, code, verifier, redirectURI string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	res, err := c.postFormRetry(ctx, tokenURL, form, clientID, secret)
	if err != nil {
		return nil, fmt.Errorf("oidc: token request failed: %w", err)
	}
	body, status := res.data, res.status
	if status == http.StatusUnauthorized && secret != "" {
		// Fall back to client_secret_post for providers that reject basic auth.
		post := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"client_id":     {clientID},
			"client_secret": {secret},
			"code_verifier": {verifier},
		}
		res, err = c.postFormRetry(ctx, tokenURL, post, "", "")
		if err != nil {
			return nil, fmt.Errorf("oidc: token request failed: %w", err)
		}
		body, status = res.data, res.status
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("oidc: token endpoint returned status %d", status)
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, errors.New("oidc: token response is not valid JSON")
	}
	if tok.AccessToken == "" {
		return nil, errors.New("oidc: token response has no access_token")
	}
	return &tok, nil
}

// LogoutURL builds the RP-initiated logout URL, or "" when the provider
// publishes no end_session_endpoint (caller then ends the local session only).
func (c *Client) LogoutURL(p *domain.OIDCProvider, postLogoutRedirect string) string {
	end := strings.TrimSpace(p.EndSession)
	if end == "" {
		c.mu.Lock()
		d, ok := c.discover[discoveryCacheKey(p)]
		c.mu.Unlock()
		if !ok {
			return ""
		}
		end = d.doc.EndSession
	}
	if end == "" {
		return ""
	}
	u, err := url.Parse(end)
	if err != nil {
		return ""
	}
	if postLogoutRedirect != "" {
		q := u.Query()
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
		// client_id helps providers without id_token_hint route the logout.
		q.Set("client_id", p.ClientID)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// mergeUserinfo fills missing email/name/groups from userinfo (best effort;
// some providers only expose group membership there).
func (c *Client) mergeUserinfo(ctx context.Context, p *domain.OIDCProvider, ep *Endpoints, accessToken string, claims *domain.OIDCClaims) {
	if ep.UserInfo == "" || accessToken == "" {
		return
	}
	if claims.Email != "" && claims.Name != "" && len(claims.Groups) > 0 {
		return
	}
	body, err := c.getBearer(ctx, ep.UserInfo, accessToken)
	if err != nil {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}
	if claims.Email == "" {
		claims.Email, _ = raw["email"].(string)
	}
	if claims.Name == "" {
		if name, _ := raw["name"].(string); name != "" {
			claims.Name = name
		} else if nick, _ := raw["preferred_username"].(string); nick != "" {
			claims.Name = nick
		}
	}
	if len(claims.Groups) == 0 {
		seen := map[string]bool{}
		for _, claimName := range p.GroupClaimNames {
			for _, g := range stringsFromPath(raw, claimName) {
				if g != "" && !seen[g] {
					seen[g] = true
					claims.Groups = append(claims.Groups, g)
				}
			}
		}
	}
}

// getRetry performs a GET with backoff on transient failures.
func (c *Client) getRetry(ctx context.Context, rawURL string) ([]byte, error) {
	return backoff.Retry(ctx, func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxIDPBody))
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return body, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			return nil, fmt.Errorf("idp status %d", resp.StatusCode)
		default:
			return nil, backoff.Permanent(fmt.Errorf("idp status %d", resp.StatusCode))
		}
	}, backoff.WithMaxTries(3))
}

// getBearer performs a single authenticated GET (no retry: bearer replay is
// safe but userinfo is best-effort, so one attempt suffices).
func (c *Client) getBearer(ctx context.Context, rawURL, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("idp status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxIDPBody))
}

// formResult carries a token-endpoint response through backoff.Retry.
type formResult struct {
	status int
	data   []byte
}

// postFormRetry POSTs a form with backoff on transient failures. An empty
// user sends no Authorization header (public client or secret-in-body).
func (c *Client) postFormRetry(ctx context.Context, rawURL string, form url.Values, user, pass string) (formResult, error) {
	return backoff.Retry(ctx, func() (formResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL,
			bytes.NewBufferString(form.Encode()))
		if err != nil {
			return formResult{}, backoff.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return formResult{}, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxIDPBody))
		if err != nil {
			return formResult{}, backoff.Permanent(err)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return formResult{}, fmt.Errorf("idp status %d", resp.StatusCode)
		}
		return formResult{status: resp.StatusCode, data: data}, nil
	}, backoff.WithMaxTries(3))
}
