// Package crypto provides envelope encryption for provider secrets at rest
// (PRD §7.1.1, §10.2): AES-256-GCM with a key supplied via environment
// config. Plaintext and keys never appear in errors or logs.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultKeyEnvVar is the environment variable holding the encryption key.
const DefaultKeyEnvVar = "VAULTIX_CRYPTO_KEY"

// envelopeVersion prefixes every ciphertext produced by Box.
const envelopeVersion = "v1"

var (
	// ErrInvalidKey is returned when the configured key is unusable.
	ErrInvalidKey = errors.New("crypto: invalid key")
	// ErrDecrypt is returned when a ciphertext cannot be decrypted.
	// It carries no detail about the key or plaintext.
	ErrDecrypt = errors.New("crypto: decrypt failed")
)

// Box encrypts and decrypts small secrets (OIDC client secrets) with
// AES-256-GCM. The zero value is unusable; construct via NewBox.
type Box struct {
	aead cipher.AEAD
}

// NewBox builds a Box from a raw 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: need 32 bytes, got %d", ErrInvalidKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, "cipher init failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, "gcm init failed")
	}
	return &Box{aead: aead}, nil
}

// NewBoxFromEnv builds a Box from an environment variable. The value may be
// raw 32 bytes, base64 (standard or URL-safe, padded or not), or hex.
func NewBoxFromEnv(envVar string) (*Box, error) {
	if envVar == "" {
		envVar = DefaultKeyEnvVar
	}
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return nil, fmt.Errorf("%w: env %s is empty or unset", ErrInvalidKey, envVar)
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: env %s is not a valid 32-byte key", ErrInvalidKey, envVar)
	}
	return NewBox(key)
}

func decodeKey(s string) ([]byte, error) {
	if len(s) == 32 {
		return []byte(s), nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(strings.TrimPrefix(s, "0x")); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, ErrInvalidKey
}

// GenerateKey returns 32 random bytes suitable for NewBox. Operators store
// the base64 encoding in VAULTIX_CRYPTO_KEY.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto: key generation failed: %w", err)
	}
	return key, nil
}

// Encrypt seals plaintext and returns a versioned envelope string
// ("v1:<base64 nonce|ciphertext>") for storage.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("crypto: box not initialized")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: encrypt failed: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)
	var sb strings.Builder
	sb.WriteString(envelopeVersion)
	sb.WriteString(":")
	sb.WriteString(base64.StdEncoding.EncodeToString(sealed))
	return sb.String(), nil
}

// Decrypt opens an envelope produced by Encrypt.
func (b *Box) Decrypt(envelope string) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("crypto: box not initialized")
	}
	ver, payload, ok := strings.Cut(envelope, ":")
	if !ok || ver != envelopeVersion {
		return nil, ErrDecrypt
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
