package dependency

import (
	"context"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

// DependencyResolver coordinates resolving all dependencies for a service
// deployment in a given environment.
type DependencyResolver interface {
	ResolveAll(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) (map[string]domain.ResolvedContext, error)
	Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error
}

// ManagedProvider is implemented by each infrastructure provider (e.g. OpenTofu).
type ManagedProvider interface {
	InputFields() []domain.FieldSpec
	Apply(ctx context.Context, req ProviderRequest) (map[string]any, error)
	Destroy(ctx context.Context, req ProviderRequest) error
}

type ProviderRequest struct {
	ServiceName    string
	DependencyName string
	DependencyType string
	Inputs         map[string]any
	Environment    domain.Environment
}

// SpecRegistry is a read-only registry of built-in dependency type schemas.
type SpecRegistry interface {
	Get(depType string) (domain.DependencySpec, error)
	List() []domain.DependencySpec
	Exists(depType string) bool
}

// ProviderRegistry maps dependency types to their ManagedProvider implementations.
type ProviderRegistry interface {
	Get(depType string) (ManagedProvider, error)
	Register(depType string, provider ManagedProvider) error
}
