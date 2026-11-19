package storage

import (
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// DTOs. Every request is validated at the boundary; every secret response
// carries metadata only (never plaintext or ciphertext).

// DestinationRequest creates or fully replaces a destination.
type DestinationRequest struct {
	Name          string `json:"name" validate:"required,min=1,max=128"`
	Endpoint      string `json:"endpoint" validate:"omitempty,http_url,max=512"`
	Region        string `json:"region" validate:"omitempty,max=64"`
	Bucket        string `json:"bucket" validate:"required,min=3,max=63"`
	Prefix        string `json:"prefix" validate:"omitempty,max=256"`
	CredentialRef string `json:"credential_ref" validate:"omitempty,max=128"`
	UsePathStyle  bool   `json:"use_path_style"`
	Default       bool   `json:"default"`
}

func (r DestinationRequest) toCreateInput() ustorage.CreateDestinationInput {
	return ustorage.CreateDestinationInput{
		Name:          r.Name,
		Endpoint:      r.Endpoint,
		Region:        r.Region,
		Bucket:        r.Bucket,
		Prefix:        r.Prefix,
		CredentialRef: r.CredentialRef,
		UsePathStyle:  r.UsePathStyle,
		Default:       r.Default,
	}
}

func (r DestinationRequest) toUpdateInput(id string) ustorage.UpdateDestinationInput {
	return ustorage.UpdateDestinationInput{
		ID:            id,
		Name:          r.Name,
		Endpoint:      r.Endpoint,
		Region:        r.Region,
		Bucket:        r.Bucket,
		Prefix:        r.Prefix,
		CredentialRef: r.CredentialRef,
		UsePathStyle:  r.UsePathStyle,
		Default:       r.Default,
	}
}

// DestinationResponse renders a destination. Long-lived credentials are
// referenced, never embedded.
type DestinationResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Endpoint      string    `json:"endpoint"`
	Region        string    `json:"region"`
	Bucket        string    `json:"bucket"`
	Prefix        string    `json:"prefix"`
	CredentialRef string    `json:"credential_ref"`
	UsePathStyle  bool      `json:"use_path_style"`
	Default       bool      `json:"default"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toDestinationResponse(d *domain.StorageDestination) DestinationResponse {
	return DestinationResponse{
		ID:            d.ID,
		Name:          d.Name,
		Endpoint:      d.Endpoint,
		Region:        d.Region,
		Bucket:        d.Bucket,
		Prefix:        d.Prefix,
		CredentialRef: d.CredentialRef,
		UsePathStyle:  d.UsePathStyle,
		Default:       d.Default,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

// TestConnectionResponse reports a connection-test outcome. A failed probe
// is a 200 with OK=false (the test ran; the storage did not answer).
type TestConnectionResponse struct {
	DestinationID string    `json:"destination_id"`
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

// SecretRequest creates or rotates a secret profile. Plaintext is an opaque
// UTF-8 string (base64-encode binary material); it is write-only and never
// echoed back.
type SecretRequest struct {
	Name      string `json:"name" validate:"required,min=1,max=128"`
	Kind      string `json:"kind" validate:"required,oneof=database storage encryption generic"`
	Plaintext string `json:"plaintext" validate:"required,min=1,max=1048576"`
}

func (r SecretRequest) toCreateInput(actor string) ustorage.CreateSecretInput {
	return ustorage.CreateSecretInput{
		Name:      r.Name,
		Kind:      domain.SecretKind(r.Kind),
		Plaintext: []byte(r.Plaintext),
		CreatedBy: actor,
	}
}

func (r SecretRequest) toUpdateInput(id string) ustorage.UpdateSecretInput {
	return ustorage.UpdateSecretInput{
		ID:        id,
		Name:      r.Name,
		Kind:      domain.SecretKind(r.Kind),
		Plaintext: []byte(r.Plaintext),
	}
}

// SecretMetaResponse renders a profile's metadata references only.
type SecretMetaResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	KeyRef    string    `json:"key_ref"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toSecretMetaResponse(m *ustorage.SecretMeta) SecretMetaResponse {
	return SecretMetaResponse{
		ID:        m.ID,
		Name:      m.Name,
		Kind:      string(m.Kind),
		KeyRef:    m.KeyRef,
		CreatedBy: m.CreatedBy,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// ErrorResponse renders API failures. Details name fields and rules only,
// never submitted values.
type ErrorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}
