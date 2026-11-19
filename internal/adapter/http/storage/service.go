package storage

import (
	"context"

	"github.com/vaultix/vaultix/internal/domain"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// Service is the narrow storage use-case seam this package serves. It is
// satisfied by *ustorage.Service with zero glue (see the assertion below);
// tests substitute fakes.
type Service interface {
	CreateDestination(ctx context.Context, in ustorage.CreateDestinationInput) (*domain.StorageDestination, error)
	GetDestination(ctx context.Context, id string) (*domain.StorageDestination, error)
	UpdateDestination(ctx context.Context, in ustorage.UpdateDestinationInput) (*domain.StorageDestination, error)
	DeleteDestination(ctx context.Context, id string) error
	ListDestinations(ctx context.Context) ([]domain.StorageDestination, error)
	TestDestination(ctx context.Context, id string) error

	CreateSecret(ctx context.Context, in ustorage.CreateSecretInput) (*ustorage.SecretMeta, error)
	GetSecret(ctx context.Context, id string) (*ustorage.SecretMeta, error)
	UpdateSecret(ctx context.Context, in ustorage.UpdateSecretInput) (*ustorage.SecretMeta, error)
	DeleteSecret(ctx context.Context, id string) error
	ListSecrets(ctx context.Context) ([]ustorage.SecretMeta, error)
}

var _ Service = (*ustorage.Service)(nil)

// Deps carries the handler dependencies. Service is required.
type Deps struct {
	Service Service
	// Actor names the authenticated caller recorded as secret creator; it
	// is empty until the auth track wires identity into this package.
	Actor string
}
