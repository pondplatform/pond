package dependency

import (
	"fmt"

	"github.com/pondplatform/pond/internal/common/domain"
)

// specRegistry is an in-memory registry of built-in dependency type schemas.
type specRegistry struct {
	specs map[string]domain.DependencySpec
}

func NewSpecRegistry() SpecRegistry {
	r := &specRegistry{specs: make(map[string]domain.DependencySpec)}
	registerBuiltinSpecs(r)
	return r
}

func (r *specRegistry) Get(depType string) (domain.DependencySpec, error) {
	spec, ok := r.specs[depType]
	if !ok {
		return domain.DependencySpec{}, fmt.Errorf("dependency type %q: %w", depType, domain.ErrNotFound)
	}
	return spec, nil
}

func (r *specRegistry) List() []domain.DependencySpec {
	result := make([]domain.DependencySpec, 0, len(r.specs))
	for _, s := range r.specs {
		result = append(result, s)
	}
	return result
}

func (r *specRegistry) Exists(depType string) bool {
	_, ok := r.specs[depType]
	return ok
}

func (r *specRegistry) register(spec domain.DependencySpec) {
	r.specs[spec.Type] = spec
}

// providerRegistry maps dependency types to their ManagedProvider implementations.
type providerRegistry struct {
	providers map[string]ManagedProvider
}

func NewProviderRegistry() ProviderRegistry {
	return &providerRegistry{providers: make(map[string]ManagedProvider)}
}

func (r *providerRegistry) Get(depType string) (ManagedProvider, error) {
	p, ok := r.providers[depType]
	if !ok {
		return nil, fmt.Errorf("no provider for type %q: %w", depType, domain.ErrNotFound)
	}
	return p, nil
}

func (r *providerRegistry) Register(depType string, provider ManagedProvider) error {
	if _, exists := r.providers[depType]; exists {
		return fmt.Errorf("provider for type %q: %w", depType, domain.ErrAlreadyExists)
	}
	r.providers[depType] = provider
	return nil
}
