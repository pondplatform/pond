package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pondplatform/pond/shared/serviceconfig"
)

var templateVarRegex = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

// TemplateRenderer interpolates {{var}} placeholders in config file values
// using resolved dependency contexts (dependency name → output values).
type TemplateRenderer interface {
	Render(values map[string]any, contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) (map[string]any, error)
}

type templateRenderer struct{}

func NewTemplateRenderer() TemplateRenderer {
	return &templateRenderer{}
}

func (t *templateRenderer) Render(values map[string]any, contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) (map[string]any, error) {
	lookupTable := buildLookupTable(contexts, svcConfig)
	result, err := renderMap(values, lookupTable)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func buildLookupTable(contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) map[string]string {
	table := make(map[string]string)

	for name, vals := range contexts {
		for k, v := range vals {
			table[name+"."+k] = fmt.Sprintf("%v", v)
		}
	}

	if svcConfig.Service != nil {
		if svcConfig.Service.Port != nil {
			table["service.port"] = fmt.Sprintf("%d", *svcConfig.Service.Port)
		}
		if svcConfig.Service.Replicas != nil {
			table["service.replicas"] = fmt.Sprintf("%d", *svcConfig.Service.Replicas)
		}
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
