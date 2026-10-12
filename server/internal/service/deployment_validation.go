package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

// validateSubmission checks everything about a submit request that can be
// checked before anything is written, and reports every problem at once.
func (s *deploymentService) validateSubmission(ctx context.Context, req api.SubmitRequest) (*db.Environment, *serviceconfig.ServiceConfig, error) {
	env, err := s.lookupEnvironment(ctx, req.ProjectID, req.EnvironmentName)
	if err != nil {
		return nil, nil, err
	}
	svcConfig, err := s.resolver.Resolve(&req.OverridableConfig, env.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve config for environment %q: %w", env.Name, err)
	}

	var errs api.ValidationErrors
	// Reject a malformed config here rather than letting it reach the cluster as
	// a failed rollout the user has to read helm output to understand.
	if err := config.Validate(svcConfig); err != nil {
		var invalid *api.ValidationErrors
		if !errors.As(err, &invalid) {
			return nil, nil, err
		}
		errs.Errors = append(errs.Errors, invalid.Errors...)
	}
	for _, name := range slices.Sorted(maps.Keys(svcConfig.Dependencies)) {
		if dependencyType := svcConfig.Dependencies[name].Type; !s.specs.Exists(dependencyType) {
			errs.Add("dependency", name, fmt.Sprintf("unknown dependency type %q", dependencyType))
		}
	}
	addUndeclaredInputErrors(&errs, svcConfig.Dependencies, req.Dependencies)
	addTemplateReferenceErrors(&errs, svcConfig)

	if errs.HasErrors() {
		return nil, nil, &errs
	}
	return env, svcConfig, nil
}

func (s *deploymentService) lookupEnvironment(ctx context.Context, projectID uuid.UUID, name string) (*db.Environment, error) {
	env, err := s.repos.Envs.GetByName(ctx, projectID, name)
	if errors.Is(err, api.ErrNotFound) {
		return nil, s.environmentNotFound(ctx, projectID, name)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup environment %q: %w", name, err)
	}
	return env, nil
}

// environmentNotFound lists the project's environments, since a typo in the
// environment name is the usual cause. Listing is best effort: the not-found
// error stands on its own.
func (s *deploymentService) environmentNotFound(ctx context.Context, projectID uuid.UUID, name string) error {
	envs, err := s.repos.Envs.ListByProject(ctx, projectID)
	if err != nil || len(envs) == 0 {
		return api.NotFound("environment %q not found in project %s", name, projectID)
	}
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	slices.Sort(names)
	return api.NotFound("environment %q not found in project %s (available: %s)", name, projectID, strings.Join(names, ", "))
}

func addUndeclaredInputErrors(errs *api.ValidationErrors, declared map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) {
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		if _, ok := declared[name]; !ok {
			errs.Add("dependency", name, "not declared in service config")
		}
	}
}

// addTemplateReferenceErrors rejects a {{var}} in a config file that could
// never resolve: one that names neither a declared dependency nor a value the
// service config itself provides. A dependency's outputs are only known once
// it is provisioned, so references into them are checked at rollout.
func addTemplateReferenceErrors(errs *api.ValidationErrors, cfg *serviceconfig.ServiceConfig) {
	own := buildLookupTable(nil, cfg)
	for _, file := range slices.Sorted(maps.Keys(cfg.Configs)) {
		for _, ref := range templateReferences(cfg.Configs[file].Values) {
			if _, ok := own[ref]; ok {
				continue
			}
			if _, ok := cfg.Dependencies[strings.SplitN(ref, ".", 2)[0]]; ok && strings.Contains(ref, ".") {
				continue
			}
			errs.Add("config", file, fmt.Sprintf(
				"template variable {{%s}} names no declared dependency and no known value (service.port, deployment.replicas, env.<NAME>)", ref))
		}
	}
}

// verifyUnmanagedReferences rejects a {{dep.key}} on an unmanaged dependency
// whose input has no such key: its values are known now, so the rollout could
// never render.
func verifyUnmanagedReferences(cfg *serviceconfig.ServiceConfig, inputs map[string]api.DependencyInput) error {
	var errs api.ValidationErrors
	for _, file := range slices.Sorted(maps.Keys(cfg.Configs)) {
		for _, ref := range templateReferences(cfg.Configs[file].Values) {
			name, key, ok := strings.Cut(ref, ".")
			input, declared := inputs[name]
			if !ok || !declared || input.Managed {
				continue
			}
			if _, has := input.Values[key]; !has {
				errs.Add("config", file, fmt.Sprintf("template variable {{%s}}: the input of unmanaged dependency %q has no %q", ref, name, key))
			}
		}
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}
