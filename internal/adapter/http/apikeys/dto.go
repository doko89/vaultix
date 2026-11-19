package apikeys

import (
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// DTOs: requests are validated with validator/v10; responses never carry
// hashes. The full secret appears only in issueResponse.key, exactly once.

// issueRequest mints a key. ttl_hours==0 (or omitted) means never expires;
// otherwise it must fit the service bounds (1m..8760h).
type issueRequest struct {
	Name     string   `json:"name" validate:"required,min=1,max=128"`
	Scopes   []string `json:"scopes" validate:"required,min=1,dive,min=1,max=32"`
	TTlHours float64  `json:"ttl_hours" validate:"gte=0,lte=8760"`
}

type keyJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toKeyJSON(k domain.ApiKey) keyJSON {
	return keyJSON{
		ID: k.ID, Name: k.Name, KeyPrefix: k.KeyPrefix, Scopes: k.Scopes,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
		RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt,
	}
}

type issueResponse struct {
	keyJSON
	// Key is the full `vxk_...` secret, returned exactly once. It is
	// unrecoverable afterwards: list responses never include it.
	Key string `json:"key"`
}
