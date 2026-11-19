package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// BcryptHasher is the production Hasher (bcrypt, adaptive cost).
type BcryptHasher struct {
	// Cost overrides bcrypt.DefaultCost when positive (tests use MinCost).
	Cost int
}

func (h BcryptHasher) cost() int {
	if h.Cost > 0 {
		return h.Cost
	}
	return bcrypt.DefaultCost
}

// Hash hashes a password. Empty passwords are rejected.
func (h BcryptHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("%w: empty password", ErrWeakPassword)
	}
	sum, err := bcrypt.GenerateFromPassword([]byte(password), h.cost())
	if err != nil {
		// Never include the password or its length class in the error.
		return "", errors.New("auth: password hashing failed")
	}
	return string(sum), nil
}

// Verify compares a bcrypt hash against a password in constant time
// (bcrypt's own comparison). Unknown hash shapes fail closed.
func (h BcryptHasher) Verify(hash, password string) error {
	if hash == "" || password == "" {
		return ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// dummyHash is a valid bcrypt hash of an unguessable password, used to keep
// unknown-user login timing indistinguishable from real verification.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// GenerateSessionToken returns a 256-bit opaque token (base64url, 43 chars).
func GenerateSessionToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("auth: token generation failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashSessionToken hashes a token for storage/lookup (SHA-256 hex).
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualTokenHash compares hashes in constant time.
func EqualTokenHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// GeneratePKCE returns a code_verifier (43 chars) and its S256 challenge.
func GeneratePKCE() (verifier, challenge string, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("auth: pkce generation failed: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// ChallengeS256 derives the S256 challenge for a verifier (tests/interop).
func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateState returns a CSRF state value (128-bit, base64url).
func GenerateState() (string, error) { return randomBase64(16) }

// GenerateNonce returns an OIDC nonce value (128-bit, base64url).
func GenerateNonce() (string, error) { return randomBase64(16) }

func randomBase64(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: random generation failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// dummyVerify burns ~one bcrypt comparison so unknown-user logins cost the
// same as real ones (enumeration resistance).
func dummyVerify() { _ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte("invalid")) }

// tokenTTL validates a presented token's shape before any store lookup.
func validTokenShape(token string) bool {
	return len(token) >= 20 && len(token) <= 128
}

// pendingLogin tracks an in-flight OIDC authorization request.
type pendingLogin struct {
	providerID  string
	state       string
	nonce       string
	verifier    string
	redirectURI string
	expiresAt   time.Time
}

// expiry reports whether the pending login is unusable at now.
func (p pendingLogin) expired(now time.Time) bool { return !now.Before(p.expiresAt) }

// validRedirect guards the redirect URI round-trip (exact match).
func (p pendingLogin) validRedirect(redirectURI string) bool {
	return subtle.ConstantTimeCompare([]byte(p.redirectURI), []byte(redirectURI)) == 1 && p.redirectURI != ""
}
