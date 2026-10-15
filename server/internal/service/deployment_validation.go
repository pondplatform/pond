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

	// Only declared variables can be referenced; values for others are rejected below.
	values := maps.Clone(req.Variables)
	maps.DeleteFunc(values, func(name, _ string) bool {
		_, declared := svcConfig.Variables[name]
		return !declared
	})
	known := newTemplateContext(svcConfig, env, values)
	// The gateway is checked as rendered. What a dependency's outputs fill in is
	// only known at rollout, which checks the gateway again.
	rendered := *svcConfig
	rendered.Gateway = renderGatewayKnown(svcConfig.Gateway, known)
	known = known.withGateway(&rendered, env)

	var errs api.ValidationErrors
	// Reject a malformed config here rather than letting it reach the cluster as
	// a failed rollout the user has to read helm output to understand.
	if err := config.Validate(&rendered); err != nil {
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
	addVariableInputErrors(&errs, svcConfig.Variables, req.Variables)
	addTemplateReferenceErrors(&errs, svcConfig, known)
	s.addVisibleReferenceErrors(&errs, svcConfig)

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

func addVariableInputErrors(errs *api.ValidationErrors, declared map[string]serviceconfig.VariableDeclaration, inputs map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		if _, ok := declared[name]; !ok {
			errs.Add("variable", name, "not declared in pond.yml")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if _, ok := inputs[name]; !ok {
			errs.Add("variable", name, "missing value: the input gives none")
		}
	}
}

// templateSite is a field the renderer substitutes into, named as validation
// errors report it, with the {{var}}s it references.
type templateSite struct {
	field string
	refs  []string
	// inGateway marks the gateway's fields, which {{gateway.*}} are rendered from.
	inGateway bool
}

func templateSites(cfg *serviceconfig.ServiceConfig) []templateSite {
	var sites []templateSite
	for _, file := range slices.Sorted(maps.Keys(cfg.Configs)) {
		sites = append(sites, templateSite{field: file, refs: templateReferences(cfg.Configs[file].Values)})
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Env)) {
		sites = append(sites, templateSite{field: fmt.Sprintf("env[%q]", name), refs: templateReferences(cfg.Env[name])})
	}
	sites = append(sites, commandSites(cfg)...)
	return append(sites, gatewaySites(cfg)...)
}

func commandSites(cfg *serviceconfig.ServiceConfig) []templateSite {
	sites := make([]templateSite, len(cfg.Command))
	for i, arg := range cfg.Command {
		sites[i] = templateSite{field: commandField(i), refs: templateReferences(arg)}
	}
	return sites
}

func gatewaySites(cfg *serviceconfig.ServiceConfig) []templateSite {
	var sites []templateSite
	_, _ = mapGatewayStrings(cfg.Gateway, func(field, s string) (string, error) {
		sites = append(sites, templateSite{field: field, refs: templateReferences(s), inGateway: true})
		return s, nil
	})
	return sites
}

func commandField(i int) string { return fmt.Sprintf("command[%d]", i) }

// addTemplateReferenceErrors rejects a {{var}} that could never resolve
// against known. A dependency's outputs are only known once it is
// provisioned, so references into them are checked at rollout.
func addTemplateReferenceErrors(errs *api.ValidationErrors, cfg *serviceconfig.ServiceConfig, known templateContext) {
	for _, site := range templateSites(cfg) {
		for _, ref := range site.refs {
			problem := referenceProblem(cfg, known, ref)
			if site.inGateway && strings.HasPrefix(ref, "gateway.") {
				problem = "comes from the gateway, so the gateway can't use it"
			}
			if problem != "" {
				errs.Add("config", site.field, fmt.Sprintf("template variable {{%s}} %s", ref, problem))
			}
		}
	}
}

// referenceProblem says why ref can't resolve, or returns "" if it can.
func referenceProblem(cfg *serviceconfig.ServiceConfig, known templateContext, ref string) string {
	if _, ok := known[ref]; ok {
		return ""
	}
	namespace, rest, _ := strings.Cut(ref, ".")
	switch namespace {
	case "dependencies":
		name, _, hasKey := strings.Cut(rest, ".")
		if _, declared := cfg.Dependencies[name]; !declared {
			return fmt.Sprintf("names no declared dependency: %q is not under dependencies in pond.yml", name)
		}
		if !hasKey {
			return fmt.Sprintf("names no output: write {{dependencies.%s.<output>}}", name)
		}
		return ""
	case "variables":
		// A declared variable without a value is reported as missing from the input.
		if _, declared := cfg.Variables[rest]; declared {
			return ""
		}
		return fmt.Sprintf("names no declared variable: %q is not under variables in pond.yml", rest)
	case "env":
		return "is no longer available: declare a variable and use {{variables.<name>}} wherever the value is needed, env included"
	case "gateway":
		if problem := gatewayProblem(cfg, known); problem != "" {
			return problem
		}
	}
	if _, ok := cfg.Dependencies[namespace]; ok && rest != "" {
		return fmt.Sprintf("names no known value: did you mean {{%s%s}}?", dependencyRefPrefix, ref)
	}
	return "names no known value (service.name, service.port, deployment.replicas, environment.name, gateway.hostname, gateway.url, variables.<name>, dependencies.<name>.<output>)"
}

// gatewayProblem says why the service has no gateway values, or returns "" if it has them.
func gatewayProblem(cfg *serviceconfig.ServiceConfig, known templateContext) string {
	if _, ok := known["gateway.hostname"]; ok {
		return ""
	}
	var route *serviceconfig.GatewayHTTPConfig
	if cfg.Gateway != nil {
		route = cfg.Gateway.HTTP
	}
	switch {
	case route == nil || route.Enabled == nil || !*route.Enabled:
		return "needs the gateway: set gateway.http.enabled"
	case route.Hostname != nil:
		return fmt.Sprintf("needs a single hostname, and gateway.http.hostname %q is a wildcard", *route.Hostname)
	}
	return fmt.Sprintf("needs a hostname: set gateway.http.hostname, or a base host on environment %q", known["environment.name"])
}

// addVisibleReferenceErrors rejects a reference that may carry a secret in
// command or the gateway, which anyone who can read the workload sees.
func (s *deploymentService) addVisibleReferenceErrors(errs *api.ValidationErrors, cfg *serviceconfig.ServiceConfig) {
	visible := []struct {
		sites []templateSite
		why   string
	}{
		{commandSites(cfg), "arguments are visible to anyone who can read the workload; pass it in env or a config file instead"},
		{gatewaySites(cfg), "the gateway's settings are visible to anyone who can read the HTTPRoute"},
	}
	for _, v := range visible {
		for _, site := range v.sites {
			for _, ref := range site.refs {
				if reason := s.refSensitive(cfg, ref); reason != "" {
					errs.Add("config", site.field, fmt.Sprintf("template variable {{%s}} %s, and %s", ref, reason, v.why))
				}
			}
		}
	}
}

// refSensitive says why ref may carry a secret, or returns "" if it can't. A
// dependency output its type doesn't declare, such as an extra value in an
// unmanaged input, may well be one.
func (s *deploymentService) refSensitive(cfg *serviceconfig.ServiceConfig, ref string) string {
	if name, ok := strings.CutPrefix(ref, "variables."); ok {
		if cfg.Variables[name].Secret {
			return "is a secret variable"
		}
		return ""
	}
	name, key, ok := parseDependencyRef(ref)
	dependency, declared := cfg.Dependencies[name]
	if !ok || !declared {
		return ""
	}
	spec, err := s.specs.Get(dependency.Type)
	if err != nil {
		return ""
	}
	output := slices.IndexFunc(spec.OutputFields, func(f db.FieldSpec) bool { return f.Name == key })
	switch {
	case output < 0:
		return fmt.Sprintf("may be sensitive: %s declares no %q output", dependency.Type, key)
	case spec.OutputFields[output].Sensitive:
		return "is sensitive"
	}
	return ""
}

// verifyUnmanagedReferences rejects a {{dependencies.<name>.<key>}} on an
// unmanaged dependency whose input has no such key: its values are known now,
// so the rollout could never render.
func verifyUnmanagedReferences(cfg *serviceconfig.ServiceConfig, inputs map[string]api.DependencyInput) error {
	var errs api.ValidationErrors
	for _, site := range templateSites(cfg) {
		for _, ref := range site.refs {
			name, key, ok := parseDependencyRef(ref)
			input, declared := inputs[name]
			if !ok || !declared || input.Managed {
				continue
			}
			if _, has := input.Values[key]; !has {
				errs.Add("config", site.field, fmt.Sprintf("template variable {{%s}}: the input of unmanaged dependency %q has no %q", ref, name, key))
			}
		}
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}
