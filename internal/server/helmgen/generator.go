package helmgen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/common/serviceconfig"
	"gopkg.in/yaml.v3"
)

type generator struct{}

func NewGenerator() HelmValuesGenerator {
	return &generator{}
}

func splitRepositoryTag(image string) (string,string) {
	split := strings.SplitN(image, ":", 2)

	tag := "latest"
	if len(split) > 1 {
		tag = split[1]
	}
	return split[0], tag
}

func (g *generator) Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment, contexts map[string]map[string]any) (*HelmValues, error) {
	repository,tag := splitRepositoryTag(cfg.Image)
	
	vals := &HelmValues{
		ReplicaCount: int(cfg.Service.Replicas),
		Image: Image{
			Repository: repository,
			Tag:        tag,
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

	// Config files (values are already rendered by the deployment service)
	for name, cfgFile := range cfg.Configs {
		encoded, err := encodeConfigFile(cfgFile.Format, cfgFile.Values)
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

