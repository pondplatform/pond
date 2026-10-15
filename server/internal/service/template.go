package service

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

var templateVarRegex = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

const dependencyRefPrefix = "dependencies."

// templateContext maps each {{var}} a service's templates can use to its value.
type templateContext map[string]string

func newTemplateContext(cfg *serviceconfig.ServiceConfig, env *db.Environment, variables map[string]string) templateContext {
	ctx := templateContext{
		"service.name":     cfg.Name,
		"environment.name": env.Name,
	}
	if cfg.Service != nil && cfg.Service.Port != nil {
		ctx["service.port"] = fmt.Sprintf("%d", *cfg.Service.Port)
	}
	if cfg.Deployment != nil && cfg.Deployment.Replicas != nil {
		ctx["deployment.replicas"] = fmt.Sprintf("%d", *cfg.Deployment.Replicas)
	}
	// A wildcard route matches many hostnames, so none of them is the service's.
	if hostname := helmgen.GatewayHostname(cfg, env); hostname != "" && !strings.HasPrefix(hostname, "*.") {
		ctx["gateway.hostname"] = hostname
		ctx["gateway.url"] = "https://" + hostname
	}
	for name, value := range variables {
		ctx["variables."+name] = value
	}
	return ctx
}

// withDependencies adds each dependency's outputs as {{dependencies.<name>.<key>}}.
func (c templateContext) withDependencies(outputs map[string]map[string]any) templateContext {
	merged := maps.Clone(c)
	for name, values := range outputs {
		for key, value := range values {
			merged[dependencyRefPrefix+name+"."+key] = fmt.Sprintf("%v", value)
		}
	}
	return merged
}

// parseDependencyRef splits a dependencies.<name>.<key> reference.
func parseDependencyRef(ref string) (name, key string, ok bool) {
	rest, ok := strings.CutPrefix(ref, dependencyRefPrefix)
	if !ok {
		return "", "", false
	}
	return strings.Cut(rest, ".")
}

// renderServiceConfig substitutes ctx into every field that takes templates.
// It makes one pass, so a value that itself contains {{…}} stays as it is.
func renderServiceConfig(cfg serviceconfig.ServiceConfig, ctx templateContext) (serviceconfig.ServiceConfig, error) {
	if cfg.Env != nil {
		env := make(map[string]string, len(cfg.Env))
		for _, name := range slices.Sorted(maps.Keys(cfg.Env)) {
			rendered, err := renderString(cfg.Env[name], ctx)
			if err != nil {
				return cfg, fmt.Errorf("render env[%q]: %w", name, err)
			}
			env[name] = rendered
		}
		cfg.Env = env
	}

	if cfg.Command != nil {
		command := make([]string, len(cfg.Command))
		for i, arg := range cfg.Command {
			rendered, err := renderString(arg, ctx)
			if err != nil {
				return cfg, fmt.Errorf("render %s: %w", commandField(i), err)
			}
			command[i] = rendered
		}
		cfg.Command = command
	}

	if cfg.Configs != nil {
		configs := make(map[string]serviceconfig.ConfigFileSpec, len(cfg.Configs))
		for _, name := range slices.Sorted(maps.Keys(cfg.Configs)) {
			file := cfg.Configs[name]
			values, err := renderMap(file.Values, ctx)
			if err != nil {
				return cfg, fmt.Errorf("render config %q: %w", name, err)
			}
			file.Values = values
			configs[name] = file
		}
		cfg.Configs = configs
	}
	return cfg, nil
}

func renderMap(values map[string]any, lookup map[string]string) (map[string]any, error) {
	result := make(map[string]any, len(values))
	for k, v := range values {
		rendered, err := renderValue(v, lookup)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		result[k] = rendered
	}
	return result, nil
}

func renderValue(v any, lookup map[string]string) (any, error) {
	switch val := v.(type) {
	case string:
		return renderString(val, lookup)
	case map[string]any:
		return renderMap(val, lookup)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			rendered, err := renderValue(item, lookup)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			result[i] = rendered
		}
		return result, nil
	default:
		return v, nil
	}
}

func renderString(s string, lookup map[string]string) (string, error) {
	var missingVars []string

	result := templateVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-2]
		if val, ok := lookup[varName]; ok {
			return val
		}
		missingVars = append(missingVars, varName)
		return match
	})

	if len(missingVars) > 0 {
		return "", fmt.Errorf("unresolved template variables: %s", strings.Join(missingVars, ", "))
	}
	return result, nil
}

// templateReferences returns every {{var}} the renderer would substitute in
// values, sorted and without duplicates.
func templateReferences(values any) []string {
	refs := make(map[string]struct{})
	var walk func(v any)
	walk = func(v any) {
		switch val := v.(type) {
		case string:
			for _, m := range templateVarRegex.FindAllStringSubmatch(val, -1) {
				refs[m[1]] = struct{}{}
			}
		case map[string]any:
			for _, item := range val {
				walk(item)
			}
		case []any:
			for _, item := range val {
				walk(item)
			}
		}
	}
	walk(values)
	return slices.Sorted(maps.Keys(refs))
}
