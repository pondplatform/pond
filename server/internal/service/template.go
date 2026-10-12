package service

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/pondplatform/pond/shared/serviceconfig"
)

var templateVarRegex = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

// renderTemplate interpolates {{var}} placeholders in config file values using
// the dependency contexts (dependency name → output values) and the service
// config's own values.
func renderTemplate(values map[string]any, contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) (map[string]any, error) {
	return renderMap(values, buildLookupTable(contexts, svcConfig))
}

func buildLookupTable(contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) map[string]string {
	table := make(map[string]string)

	for name, vals := range contexts {
		for k, v := range vals {
			table[name+"."+k] = fmt.Sprintf("%v", v)
		}
	}

	if svcConfig.Service != nil && svcConfig.Service.Port != nil {
		table["service.port"] = fmt.Sprintf("%d", *svcConfig.Service.Port)
	}
	if svcConfig.Deployment != nil && svcConfig.Deployment.Replicas != nil {
		table["deployment.replicas"] = fmt.Sprintf("%d", *svcConfig.Deployment.Replicas)
	}

	for k, v := range svcConfig.Env {
		table["env."+k] = v
	}

	return table
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
func templateReferences(values map[string]any) []string {
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
		}
	}
	walk(values)
	return slices.Sorted(maps.Keys(refs))
}
