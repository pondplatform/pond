package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

const dependencyRefPrefix = "dependencies."

// templateContext maps each {{var}} a service's templates can use to its value.
type templateContext map[string]string

// newTemplateContext holds every value but {{gateway.*}}, which come from the
// rendered gateway: see withGateway.
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
	for name, value := range variables {
		ctx["variables."+name] = value
	}
	return ctx
}

// withGateway adds {{gateway.hostname}} and {{gateway.url}} for the hostname
// cfg's route matches, so cfg's gateway must be rendered already.
func (c templateContext) withGateway(cfg *serviceconfig.ServiceConfig, env *db.Environment) templateContext {
	merged := maps.Clone(c)
	// A wildcard route matches many hostnames, so none of them is the service's.
	if hostname := helmgen.GatewayHostname(cfg, env); hostname != "" && !strings.HasPrefix(hostname, "*.") {
		merged["gateway.hostname"] = hostname
		merged["gateway.url"] = "https://" + hostname
	}
	return merged
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

// renderServiceConfig substitutes ctx, and the {{gateway.*}} values the
// rendered gateway gives, into every field that takes templates. It makes one
// pass, so a value that itself contains {{…}} stays as it is.
func renderServiceConfig(cfg serviceconfig.ServiceConfig, env *db.Environment, ctx templateContext) (serviceconfig.ServiceConfig, error) {
	gateway, err := mapGatewayStrings(cfg.Gateway, func(field, s string) (string, error) {
		rendered, err := renderString(s, ctx)
		if err != nil {
			return "", fmt.Errorf("render %s: %w", field, err)
		}
		return rendered, nil
	})
	if err != nil {
		return cfg, err
	}
	cfg.Gateway = gateway
	ctx = ctx.withGateway(&cfg, env)

	if cfg.Env != nil {
		vars := make(map[string]string, len(cfg.Env))
		for _, name := range slices.Sorted(maps.Keys(cfg.Env)) {
			rendered, err := renderString(cfg.Env[name], ctx)
			if err != nil {
				return cfg, fmt.Errorf("render env[%q]: %w", name, err)
			}
			vars[name] = rendered
		}
		cfg.Env = vars
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

// renderGatewayKnown renders what ctx knows of gw, and leaves the templates it
// doesn't know, such as a dependency's outputs, for the rollout to render.
func renderGatewayKnown(gw *serviceconfig.GatewayConfig, ctx templateContext) *serviceconfig.GatewayConfig {
	rendered, _ := mapGatewayStrings(gw, func(_, s string) (string, error) {
		return serviceconfig.TemplateVar.ReplaceAllStringFunc(s, func(match string) string {
			if val, ok := ctx[match[2:len(match)-2]]; ok {
				return val
			}
			return match
		}), nil
	})
	return rendered
}

// mapGatewayStrings returns a copy of gw with each of its strings replaced by
// what fn returns for it. fn gets the string's field named as validation
// errors name it, such as gateway.http.rules[0].matches[0].path.value.
func mapGatewayStrings(gw *serviceconfig.GatewayConfig, fn func(field, s string) (string, error)) (*serviceconfig.GatewayConfig, error) {
	if gw == nil {
		return nil, nil
	}
	// The JSON form names fields as pond.yml does. The gateway types always
	// encode, and a tree of their own shape always decodes back into them.
	data, _ := json.Marshal(gw)
	var tree any
	_ = json.Unmarshal(data, &tree)
	mapped, err := mapStrings(tree, "gateway", fn)
	if err != nil {
		return nil, err
	}
	data, _ = json.Marshal(mapped)
	var out serviceconfig.GatewayConfig
	_ = json.Unmarshal(data, &out)
	return &out, nil
}

func mapStrings(v any, field string, fn func(field, s string) (string, error)) (any, error) {
	switch val := v.(type) {
	case string:
		return fn(field, val)
	case map[string]any:
		mapped := make(map[string]any, len(val))
		for _, key := range slices.Sorted(maps.Keys(val)) {
			item, err := mapStrings(val[key], field+"."+key, fn)
			if err != nil {
				return nil, err
			}
			mapped[key] = item
		}
		return mapped, nil
	case []any:
		mapped := make([]any, len(val))
		for i, item := range val {
			var err error
			if mapped[i], err = mapStrings(item, fmt.Sprintf("%s[%d]", field, i), fn); err != nil {
				return nil, err
			}
		}
		return mapped, nil
	}
	return v, nil
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

	result := serviceconfig.TemplateVar.ReplaceAllStringFunc(s, func(match string) string {
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
			for _, m := range serviceconfig.TemplateVar.FindAllStringSubmatch(val, -1) {
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
