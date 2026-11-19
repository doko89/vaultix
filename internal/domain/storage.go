package domain

import (
	"fmt"
	"strings"
	"time"
)

// StorageDestination configures one S3-compatible object storage target
// (PRD §7.12). Long-lived credentials are referenced, never embedded:
// agents receive only short-lived presigned URLs / scoped credentials.
type StorageDestination struct {
	ID       string
	Name     string
	Endpoint string // e.g. https://s3.example.com (empty = AWS default)
	Region   string
	Bucket   string
	Prefix   string
	// CredentialRef references the encrypted access-key profile.
	CredentialRef string
	// UsePathStyle forces path-style addressing (MinIO etc.).
	UsePathStyle bool
	// Default marks the destination used when a policy names none.
	Default   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks destination invariants.
func (d StorageDestination) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if strings.TrimSpace(d.Bucket) == "" {
		return fmt.Errorf("%w: bucket is required", ErrValidation)
	}
	return nil
}

// SecretKind classifies a protected credential profile.
type SecretKind string

const (
	SecretKindDatabase   SecretKind = "database"
	SecretKindStorage    SecretKind = "storage"
	SecretKindEncryption SecretKind = "encryption"
	SecretKindGeneric    SecretKind = "generic"
)

// SecretProfile is an encrypted-at-rest credential bundle (PRD
// `secret_profiles`). Plaintext values never appear on this entity.
type SecretProfile struct {
	ID   string
	Name string
	Kind SecretKind
	// Ciphertext is the encrypted payload; adapters own envelope encryption.
	Ciphertext []byte
	// KeyRef identifies the data-encryption key slot for rotation support.
	KeyRef    string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks secret profile invariants.
func (s SecretProfile) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if s.Kind == "" {
		return fmt.Errorf("%w: kind is required", ErrValidation)
	}
	if len(s.Ciphertext) == 0 {
		return fmt.Errorf("%w: ciphertext is required", ErrValidation)
	}
	return nil
}
