package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

// Key presentation: `vxk_` + 32 base64url chars (192 bits of entropy).
const (
	// KeyScheme is the secret prefix identifying user API keys in an
	// Authorization bearer (the HTTP gate only routes vxk_ bearers here).
	KeyScheme = "vxk_"
	// randomChars is the secret length after the scheme prefix.
	randomChars = 32
	// prefixChars is the KeyPrefix length (first chars of the secret).
	prefixChars = 8
)

// GenerateSecret returns a fresh API key secret (`vxk_` + 32 random
// chars). It is shown to the owner exactly once and never stored.
func GenerateSecret() (string, error) {
	// 24 random bytes encode to exactly 32 base64url chars (no padding).
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("apikey: secret generation failed: %w", err)
	}
	return KeyScheme + base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashSecret hashes a presented secret for storage/lookup (raw SHA-256).
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// EqualHash compares hashes in constant time.
func EqualHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// DisplayPrefix returns the first 8 chars of the secret for lookup
// display (e.g. `vxk_a1b2c`). It carries no authentication value.
func DisplayPrefix(secret string) string {
	if len(secret) < prefixChars {
		return secret
	}
	return secret[:prefixChars]
}

// ValidSecretShape validates a presented secret's shape before any store
// lookup (cheap rejection of garbage, wrong schemes, truncation).
func ValidSecretShape(secret string) bool {
	if !strings.HasPrefix(secret, KeyScheme) {
		return false
	}
	if len(secret) != len(KeyScheme)+randomChars {
		return false
	}
	for _, c := range secret[len(KeyScheme):] {
		isB64URL := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' ||
			c >= '0' && c <= '9' || c == '-' || c == '_'
		if !isB64URL {
			return false
		}
	}
	return true
}
