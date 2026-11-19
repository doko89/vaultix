package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// verifyIDToken parses the compact JWS, verifies the signature against the
// provider JWKS (with rotation refresh), and enforces iss/aud/exp/iat/nonce.
func (c *Client) verifyIDToken(ctx context.Context, p *domain.OIDCProvider, ep *Endpoints, raw, expectedNonce string) (*domain.OIDCClaims, error) {
	header, payload, signingInput, signature, err := splitCompact(raw)
	if err != nil {
		return nil, err
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(header, &hdr); err != nil {
		return nil, fmt.Errorf("%w: malformed header", ErrInvalidToken)
	}
	if hdr.Alg == "" || hdr.Alg == "none" {
		return nil, fmt.Errorf("%w: missing or 'none' alg", ErrInvalidToken)
	}
	key, err := c.signingKey(ctx, ep.JWKS, hdr.Kid, hdr.Alg)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(hdr.Alg, key, signingInput, signature); err != nil {
		// Same-kid rotation (or stale cache): refresh once and retry
		// before failing closed.
		set, ferr := c.getJWKS(ctx, ep.JWKS, true)
		if ferr != nil {
			return nil, err
		}
		key2, ferr := set.find(hdr.Kid, hdr.Alg)
		if ferr != nil {
			return nil, err
		}
		if err := verifySignature(hdr.Alg, key2, signingInput, signature); err != nil {
			return nil, err
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: malformed claims", ErrInvalidToken)
	}
	if err := c.validateClaims(p, claims, expectedNonce); err != nil {
		return nil, err
	}
	return extractClaims(p, claims), nil
}

func splitCompact(raw string) (header, payload, signingInput, signature []byte, err error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, nil, nil, nil, fmt.Errorf("%w: malformed compact serialization", ErrInvalidToken)
	}
	header, err = base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: malformed header", ErrInvalidToken)
	}
	payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: malformed claims", ErrInvalidToken)
	}
	signature, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: malformed signature", ErrInvalidToken)
	}
	signingInput = []byte(parts[0] + "." + parts[1])
	return header, payload, signingInput, signature, nil
}

// validateClaims enforces iss/aud/exp/iat/nonce (PRD §7.1.1, §10.2).
func (c *Client) validateClaims(p *domain.OIDCProvider, claims map[string]any, expectedNonce string) error {
	now := c.now()
	iss, _ := claims["iss"].(string)
	if iss == "" || iss != p.Issuer {
		return fmt.Errorf("%w: issuer mismatch", ErrInvalidToken)
	}
	if !audContains(claims["aud"], p.ClientID) {
		return fmt.Errorf("%w: audience mismatch", ErrInvalidToken)
	}
	exp, ok := numericDate(claims["exp"])
	if !ok {
		return fmt.Errorf("%w: missing exp", ErrInvalidToken)
	}
	if now.Add(-c.leeway).After(exp) {
		return fmt.Errorf("%w: token expired", ErrInvalidToken)
	}
	iat, ok := numericDate(claims["iat"])
	if !ok {
		return fmt.Errorf("%w: missing iat", ErrInvalidToken)
	}
	if iat.After(now.Add(c.leeway)) {
		return fmt.Errorf("%w: token issued in the future", ErrInvalidToken)
	}
	nonce, _ := claims["nonce"].(string)
	if expectedNonce == "" || nonce != expectedNonce {
		return fmt.Errorf("%w: nonce mismatch", ErrInvalidToken)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return fmt.Errorf("%w: missing sub", ErrInvalidToken)
	}
	return nil
}

func audContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []any:
		for _, e := range v {
			if s, _ := e.(string); s == clientID {
				return true
			}
		}
	}
	return false
}

func numericDate(v any) (time.Time, bool) {
	// Claims decode via encoding/json, so dates arrive as float64.
	n, ok := v.(float64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(n), 0), true
}

// extractClaims maps verified claims onto domain.OIDCClaims, resolving group
// membership from every configured group-claim name (dot paths allowed,
// e.g. "realm_access.roles").
func extractClaims(p *domain.OIDCProvider, claims map[string]any) *domain.OIDCClaims {
	sub, _ := claims["sub"].(string)
	iss, _ := claims["iss"].(string)
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	if name == "" {
		name, _ = claims["preferred_username"].(string)
	}
	var exp time.Time
	if t, ok := numericDate(claims["exp"]); ok {
		exp = t
	}
	seen := map[string]bool{}
	var groups []string
	for _, claimName := range p.GroupClaimNames {
		for _, g := range stringsFromPath(claims, claimName) {
			if g != "" && !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	return &domain.OIDCClaims{
		Subject: sub, Issuer: iss, Email: email, Name: name,
		Groups: groups, Expiry: exp,
	}
}

// stringsFromPath reads a claim by dot-separated path; the leaf may be a
// string or an array of strings (or stringables).
func stringsFromPath(claims map[string]any, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var cur any = claims
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// signingKey resolves the JWK for kid/alg, refreshing once on unknown kid to
// support key rotation (PRD §7.1.1).
func (c *Client) signingKey(ctx context.Context, jwksURL, kid, alg string) (crypto.PublicKey, error) {
	set, err := c.getJWKS(ctx, jwksURL, false)
	if err != nil {
		return nil, err
	}
	if key, ferr := set.find(kid, alg); ferr == nil {
		return key, nil
	}
	// Unknown kid: refresh once in case of rotation, then fail closed.
	set, err = c.getJWKS(ctx, jwksURL, true)
	if err != nil {
		return nil, err
	}
	if key, ferr := set.find(kid, alg); ferr == nil {
		return key, nil
	}
	return nil, fmt.Errorf("%w: no matching signing key", ErrInvalidToken)
}

func (c *Client) getJWKS(ctx context.Context, jwksURL string, forceRefresh bool) (*jwkSet, error) {
	c.mu.Lock()
	cached, ok := c.jwks[jwksURL]
	fresh := ok && c.now().Sub(cached.at) < jwksCacheTTL
	c.mu.Unlock()
	if fresh && !forceRefresh {
		return cached.set, nil
	}
	set, err := c.fetchJWKS(ctx, jwksURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.jwks[jwksURL] = &cachedJWKS{set: set, at: c.now()}
	c.mu.Unlock()
	return set, nil
}

func (c *Client) fetchJWKS(ctx context.Context, jwksURL string) (*jwkSet, error) {
	body, err := c.getRetry(ctx, jwksURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: jwks fetch failed: %w", err)
	}
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, errors.New("oidc: jwks document is not valid JSON")
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("oidc: jwks document has no keys")
	}
	return &set, nil
}

// jwk is the subset of RFC 7517 we verify with (RSA + EC public keys).
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

func (s *jwkSet) find(kid, alg string) (crypto.PublicKey, error) {
	for i := range s.Keys {
		k := &s.Keys[i]
		if kid != "" && k.Kid != "" && k.Kid != kid {
			continue
		}
		if k.Alg != "" && k.Alg != alg {
			continue
		}
		switch k.Kty {
		case "RSA":
			key, err := k.rsaPublicKey()
			if err != nil {
				continue
			}
			return key, nil
		case "EC":
			key, err := k.ecPublicKey()
			if err != nil {
				continue
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("%w: no matching signing key", ErrInvalidToken)
}

func (k *jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	if k.N == "" || k.E == "" {
		return nil, errors.New("bad rsa jwk")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func (k *jwk) ecPublicKey() (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	default:
		return nil, errors.New("unsupported ec curve")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, err
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, err
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, nil
}

// verifySignature checks the JWS over signingInput for the supported algs.
func verifySignature(alg string, key crypto.PublicKey, signingInput, signature []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key type mismatch", ErrInvalidToken)
		}
		h, hf := hashForAlg(alg)
		h.Write(signingInput)
		if err := rsa.VerifyPKCS1v15(rsaKey, hf, h.Sum(nil), signature); err != nil {
			return fmt.Errorf("%w: bad signature", ErrInvalidToken)
		}
		return nil
	case "PS256", "PS384", "PS512":
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key type mismatch", ErrInvalidToken)
		}
		h, hf := hashForAlg(alg)
		h.Write(signingInput)
		opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: hf}
		if err := rsa.VerifyPSS(rsaKey, hf, h.Sum(nil), signature, opts); err != nil {
			return fmt.Errorf("%w: bad signature", ErrInvalidToken)
		}
		return nil
	case "ES256", "ES384":
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key type mismatch", ErrInvalidToken)
		}
		h, _ := hashForAlg(alg)
		h.Write(signingInput)
		if !ecdsa.VerifyASN1(ecKey, h.Sum(nil), signature) {
			return fmt.Errorf("%w: bad signature", ErrInvalidToken)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported alg %q", ErrInvalidToken, alg)
	}
}

func hashForAlg(alg string) (hash.Hash, crypto.Hash) {
	switch alg {
	case "RS384", "PS384", "ES384":
		return sha512.New384(), crypto.SHA384
	case "RS512", "PS512":
		return sha512.New(), crypto.SHA512
	default:
		return sha256.New(), crypto.SHA256
	}
}
