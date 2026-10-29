package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// resolveServiceDependencies returns requested with an unmanaged input for
// every dependency of type service: the exports of the named service's latest
// succeeded deployment in env. Only those exports are imported, never what the
// service itself depends on.
func resolveServiceDependencies(ctx context.Context, repos store.Repos, projectID uuid.UUID, env *db.Environment, cfg *serviceconfig.ServiceConfig, requested map[string]api.DependencyInput) (map[string]api.DependencyInput, error) {
	resolved := maps.Clone(requested)
	if resolved == nil {
		resolved = map[string]api.DependencyInput{}
	}
	var errs api.ValidationErrors
	for _, name := range slices.Sorted(maps.Keys(cfg.Dependencies)) {
		dependency := cfg.Dependencies[name]
		if dependency.Type != serviceconfig.DependencyTypeService {
			continue
		}
		if _, given := requested[name]; given {
			errs.Add("dependency", name, "takes no input: a service dependency imports its values from the service")
			continue
		}
		target := dependency.ServiceName()
		if target == cfg.Name {
			errs.Add("dependency", name, fmt.Sprintf("names service %q itself", target))
			continue
		}
		exports, problem, err := latestExports(ctx, repos, projectID, env, target)
		if err != nil {
			return nil, err
		}
		if problem != "" {
			errs.Add("dependency", name, problem)
			continue
		}
		values := make(map[string]any, len(exports))
		for key, value := range exports {
			values[key] = value
		}
		resolved[name] = api.DependencyInput{Managed: false, Values: values}
	}
	if errs.HasErrors() {
		return nil, &errs
	}
	return resolved, nil
}

// latestExports are the exports of target's latest succeeded deployment in
// env, or a problem saying why there are none.
func latestExports(ctx context.Context, repos store.Repos, projectID uuid.UUID, env *db.Environment, target string) (map[string]string, string, error) {
	svc, err := repos.Services.GetByName(ctx, projectID, target)
	if errors.Is(err, api.ErrNotFound) {
		return nil, fmt.Sprintf("service %q not found in the project", target), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("lookup service %q: %w", target, err)
	}
	latest, err := repos.Deployments.ListByServiceFiltered(ctx, svc.ID, &env.ID, nil, 1, "")
	if err != nil {
		return nil, "", fmt.Errorf("lookup deployments of service %q: %w", target, err)
	}
	if len(latest) > 0 && latest[0].Kind == db.DeploymentKindDelete {
		return nil, deletedProblem(latest[0], target, env.Name), nil
	}
	succeeded := api.DeploymentStatusSucceeded
	deployments, err := repos.Deployments.ListByServiceFiltered(ctx, svc.ID, &env.ID, &succeeded, 1, "")
	if err != nil {
		return nil, "", fmt.Errorf("lookup deployments of service %q: %w", target, err)
	}
	switch {
	// A succeeded delete, then a deploy that has not succeeded yet.
	case len(deployments) == 0, deployments[0].Kind == db.DeploymentKindDelete:
		return nil, fmt.Sprintf("service %q has no successful deployment in environment %q: deploy it first", target, env.Name), nil
	case deployments[0].Exports == nil:
		return nil, fmt.Sprintf("service %q was last deployed before services could depend on it: redeploy it first", target), nil
	}
	return deployments[0].Exports, "", nil
}

// deletedProblem says why target can't be depended on when its latest
// deployment in the environment, d, is a delete.
func deletedProblem(d db.Deployment, target, env string) string {
	switch d.Status {
	case db.DeploymentInProgress:
		return fmt.Sprintf("service %q is being deleted from environment %q", target, env)
	case db.DeploymentSucceeded:
		return fmt.Sprintf("service %q was deleted from environment %q: deploy it again first", target, env)
	}
	return fmt.Sprintf("the last delete of service %q in environment %q %s: deploy it again first", target, env, d.Status)
}
