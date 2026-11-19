package storage

import "context"

// SecretBox seals and unseals secret payloads for storage. It is the narrow
// local seam for envelope encryption; the production implementation is the
// auth track's crypto service (AES-256-GCM), wired in cmd.
//
// INTEGRATION NOTE: auth track provides the real SecretBox. Until then,
// secret create/update cannot run against production; use a test fake (see
// service_test.go) for storage-local verification.
type SecretBox interface {
	// Seal encrypts plaintext, returning the ciphertext and the key
	// reference (key slot) needed to open it later.
	Seal(ctx context.Context, plaintext []byte) (ciphertext []byte, keyRef string, err error)
	// Open decrypts ciphertext sealed under keyRef. It is used by the
	// destination resolver (cmd wiring) to obtain a destination's own
	// configured keys; plaintext is never persisted or returned by reads.
	Open(ctx context.Context, ciphertext []byte, keyRef string) (plaintext []byte, err error)
}
