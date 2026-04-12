package helmgen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

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
		probe := &HelmProbe{
			HTTPGet: HelmHTTPGet{
				Path: cfg.Manage.Health.Endpoint,
				Port: cfg.Manage.Health.Port,
			},
		}
		vals.LivenessProbe = probe
		vals.ReadinessProbe = probe
	}

	// Metrics annotations
	if cfg.Manage.Metrics.Endpoint != "" {
		vals.PodAnnotations = map[string]string{
			"prometheus.io/scrape": "true",
			"prometheus.io/port":   fmt.Sprintf("%d", cfg.Manage.Metrics.Port),
			"prometheus.io/path":   cfg.Manage.Metrics.Endpoint,
		}
	}

	// Config files
	for name, cfgFile := range cfg.Configs {
		encoded, err := encodeConfigFile(cfgFile)
		if err != nil {
			return nil, fmt.Errorf("encode config %q: %w", name, err)
		}
		vals.Configs = append(vals.Configs, HelmConfig{
			Enabled:       true,
			MountLocation: cfgFile.MountDir + "/" + name,
			Data:          encoded,
		})
	}

	return vals, nil
}

func encodeConfigFile(spec domain.ConfigFileSpec) (string, error) {
	var data []byte
	var err error

	switch spec.Format {
	case "yaml":
		data, err = yaml.Marshal(spec.Values)
	case "json":
		data, err = json.Marshal(spec.Values)
	case "env":
		data, err = marshalEnv(spec.Values)
	default:
		return "", fmt.Errorf("unsupported config format: %s", spec.Format)
	}
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

func marshalEnv(values map[string]any) ([]byte, error) {
	var result []byte
	for k, v := range values {
		result = append(result, []byte(fmt.Sprintf("%s=%v\n", k, v))...)
	}
	return result, nil
}
