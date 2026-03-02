package dependency

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type dependencyResolver struct {
	configs    domain.DependencyConfigRepository
	contexts   domain.ResolvedContextRepository
	specs      SpecRegistry
	providers  ProviderRegistry
}

func NewDependencyResolver(
	configs domain.DependencyConfigRepository,
	contexts domain.ResolvedContextRepository,
	specs SpecRegistry,
	providers ProviderRegistry,
) DependencyResolver {
	return &dependencyResolver{
		configs:   configs,
		contexts:  contexts,
		specs:     specs,
		providers: providers,
	}
}

func (r *dependencyResolver) ResolveAll(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) (map[string]domain.ResolvedContext, error) {
	result := make(map[string]domain.ResolvedContext, len(deps))

	for name, decl := range deps {
		resolved, err := r.resolveOne(ctx, serviceID, envID, name, decl)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency %q: %w", name, err)
		}
		result[name] = *resolved
	}

	return result, nil
}

func (r *dependencyResolver) resolveOne(ctx context.Context, serviceID, envID uuid.UUID, name string, decl domain.DependencyDeclaration) (*domain.ResolvedContext, error) {
	cfg, err := r.configs.Get(ctx, serviceID, envID, name)
	if err != nil {
		return nil, fmt.Errorf("get dependency config: %w", err)
	}

	// For non-managed dependencies, use user config as resolved context.
	if !cfg.Managed {
		rc := &domain.ResolvedContext{
			ID:             uuid.New(),
			ServiceID:      serviceID,
			EnvironmentID:  envID,
			DependencyName: name,
			Values:         cfg.UserConfig,
			ResolvedAt:     time.Now(),
		}
		return rc, nil
	}

	// For managed dependencies, check for existing resolved context first.
	existing, err := r.contexts.Get(ctx, serviceID, envID, name)
	if err == nil && existing != nil {
		return existing, nil
	}

	return nil, fmt.Errorf("no resolved context for managed dependency %q; provider must be run first", name)
}

func (r *dependencyResolver) Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error {
	var errs domain.ValidationErrors

	for name, decl := range deps {
		if !r.specs.Exists(decl.Type) {
			errs.Add("dependency", name, fmt.Sprintf("unknown dependency type %q", decl.Type))
			continue
		}

		_, err := r.configs.Get(ctx, serviceID, envID, name)
		if err != nil {
			errs.Add("dependency", name, "dependency not configured for this environment")
		}
	}

	if errs.HasErrors() {
		return &errs
	}
	return nil
}
