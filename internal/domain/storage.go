package domain

import (
	"fmt"
	"strings"
	"time"
)

// UploadMode selects how agents upload job objects to a destination.
//
//   - UploadModePresigned (default): agents receive short-lived presigned
//     PUT URLs per object and never see credentials.
//   - UploadModeDirect: the manager issues the destination's own S3
//     credential pair to the agent per polled job (TLS only), and the agent
//     PUTs with header auth, building keys itself under the same
//     <prefix>/jobs/<jobID>/<name> rule the grant path uses.
//
// WARNING (PRD §7.12 explicit opt-in exception): PRD §7.12 forbids exposing
// long-lived storage credentials to agents. UploadModeDirect is a deliberate,
// per-destination opt-out of that rule for S3 implementations with broken
// SigV4 query auth (presigned PUTs rejected with 403 while header auth
// works). Use it only for such implementations: every polled job hands the
// destination's full access-key pair to the agent, so anyone who can read a
// job's poll response (or compromise the agent host) owns the bucket.
type UploadMode string

const (
	// UploadModePresigned mints short-lived presigned URLs per object.
	UploadModePresigned UploadMode = "presigned"
	// UploadModeDirect issues per-job S3 credentials to the agent (see the
	// WARNING on UploadMode; TLS-only issuance, refused over plaintext).
	UploadModeDirect UploadMode = "direct"
)

// StorageDestination configures one S3-compatible object storage target
// (PRD §7.12). Long-lived credentials are referenced, never embedded:
// agents receive only short-lived presigned URLs / scoped credentials —
// unless UploadMode is UploadModeDirect (explicit per-destination opt-in,
// see UploadMode).
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
	// UploadMode selects presigned (default) or direct agent uploads. See
	// UploadMode: "direct" hands per-job S3 credentials to the agent and
	// must only be used for S3 implementations with broken query auth.
	UploadMode UploadMode
	// Default marks the destination used when a policy names none.
	Default   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizedUploadMode returns the effective mode: legacy rows stored
// without a mode behave as presigned.
func (d StorageDestination) NormalizedUploadMode() UploadMode {
	if d.UploadMode == "" {
		return UploadModePresigned
	}
	return d.UploadMode
}

// Validate checks destination invariants.
func (d StorageDestination) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if strings.TrimSpace(d.Bucket) == "" {
		return fmt.Errorf("%w: bucket is required", ErrValidation)
	}
	switch d.UploadMode {
	case "", UploadModePresigned, UploadModeDirect:
	default:
		return fmt.Errorf("%w: upload_mode %q is invalid: use %q (default, short-lived presigned URLs) or %q (hands per-job S3 credentials to the agent; only for S3 implementations with broken query auth)",
			ErrValidation, string(d.UploadMode), string(UploadModePresigned), string(UploadModeDirect))
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
