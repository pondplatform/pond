package helmgen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pondplatform/pond/internal/common/domain"
	"gopkg.in/yaml.v3"
)

type generator struct{}

func NewGenerator() HelmValuesGenerator {
	return &generator{}
}

func (g *generator) Generate(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]domain.ResolvedContext) (*HelmValues, error) {
	vals := &HelmValues{
		ReplicaCount: int(cfg.Service.Replicas),
		Image: Image{
			Repository: cfg.Image,
			Tag:        "",
			PullPolicy: "IfNotPresent",
		},
		NameOverride:     cfg.Name,
		FullnameOverride: cfg.Name,
		Service: HelmService{
			Type: "ClusterIP",
			Port: int(cfg.Service.Port),
		},
		Env: cfg.Env,
	}

	// Ingress
	vals.Ingress = HelmIngress{
		Enabled:   cfg.Ingress.Enabled,
		ClassName: "nginx",
	}
	if cfg.Ingress.Enabled && env != nil {
		vals.Ingress.Annotations = map[string]string{
			"cert-manager.io/cluster-issuer":           "letsencrypt-prod",
			"nginx.ingress.kubernetes.io/ssl-redirect": "true",
		}

		host := fmt.Sprintf("%s.%s", cfg.Name, env.DefaultIngressBaseHost)
		vals.Ingress.Hosts = []HelmIngressHost{
			{
				Host: host,
				Paths: []HelmIngressPath{
					{Path: "/", PathType: "Prefix"},
				},
			},
		}
		vals.Ingress.TLS = []HelmIngressTLS{
			{
				Hosts:      []string{host},
				SecretName: fmt.Sprintf("%s-tls", cfg.Name),
			},
		}
	}

	// Health probes
	if cfg.Manage.Health.Endpoint != "" {
		port := int(cfg.Service.Port)
		if cfg.Manage.Health.Port != 0 {
			port = cfg.Manage.Health.Port
		}

		probe := &HelmProbe{
			HTTPGet: HelmHTTPGet{
				Path: cfg.Manage.Health.Endpoint,
				Port: port,
			},
		}
		vals.LivenessProbe = probe
		vals.ReadinessProbe = probe
	}

	// Metrics annotations
	if cfg.Manage.Metrics.Endpoint != "" {
		if vals.PodAnnotations == nil {
			vals.PodAnnotations = make(map[string]string)
		}
		vals.PodAnnotations["prometheus.io/scrape"] = "true"
		vals.PodAnnotations["prometheus.io/port"] = fmt.Sprintf("%d", cfg.Manage.Metrics.Port)
		vals.PodAnnotations["prometheus.io/path"] = cfg.Manage.Metrics.Endpoint
	}

	// Build variable context for config substitution
	variables := make(map[string]string)
	for depName, res := range contexts {
		for k, v := range res.Values {
			variables[fmt.Sprintf("%s.%s", depName, k)] = fmt.Sprintf("%v", v)
		}
	}

	// Config files
	for name, cfgFile := range cfg.Configs {
		// 1. Substitute variables in the Values map
		renderedValues, err := renderValues(cfgFile.Values, variables)
		if err != nil {
			return nil, fmt.Errorf("render config %q: %w", name, err)
		}

		// 2. Encode to base64
		encoded, err := encodeConfigFile(cfgFile.Format, renderedValues)
		if err != nil {
			return nil, fmt.Errorf("encode config %q: %w", name, err)
		}

		vals.Configs = append(vals.Configs, HelmConfig{
			Enabled:       true,
			MountLocation: strings.TrimSuffix(cfgFile.MountDir, "/") + "/" + name,
			Data:          encoded,
		})
	}

	return vals, nil
}

func renderValues(values map[string]any, variables map[string]string) (map[string]any, error) {
	if values == nil {
		return nil, nil
	}

	result := make(map[string]any)
	for k, v := range values {
		rendered, err := renderValue(v, variables)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		result[k] = rendered
	}
	return result, nil
}

func renderValue(v any, variables map[string]string) (any, error) {
	switch val := v.(type) {
	case string:
		return renderString(val, variables)
	case map[string]any:
		return renderValues(val, variables)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			rendered, err := renderValue(item, variables)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			result[i] = rendered
		}
		return result, nil
	default:
		return val, nil
	}
}

func renderString(str string, variables map[string]string) (string, error) {
	if !strings.Contains(str, "{{") {
		return str, nil
	}

	result := str
	for key, value := range variables {
		placeholder := "{{" + key + "}}"
		result = strings.ReplaceAll(result, placeholder, value)
	}

	// Check for unreplaced variables
	if strings.Contains(result, "{{") && strings.Contains(result, "}}") {
		start := strings.Index(result, "{{")
		end := strings.Index(result[start:], "}}") + start + 2
		if end > start {
			unreplaced := result[start:end]
			return "", fmt.Errorf("variable not found: %s", unreplaced)
		}
	}

	return result, nil
}

func encodeConfigFile(format string, values map[string]any) (string, error) {
	var data []byte
	var err error

	switch format {
	case "yaml", "yml":
		data, err = yaml.Marshal(values)
	case "json":
		data, err = json.Marshal(values)
	case "env":
		data, err = marshalEnv(values)
	default:
		return "", fmt.Errorf("unsupported config format: %s", format)
	}
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

func marshalEnv(values map[string]any) ([]byte, error) {
	var result []byte
	for k, v := range values {
		line := fmt.Sprintf("%s=%v\n", k, v)
		result = append(result, []byte(line)...)
	}
	return result, nil
}

