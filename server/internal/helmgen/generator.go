package helmgen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

type generator struct{}

func NewGenerator() HelmValuesGenerator {
	return &generator{}
}

func splitRepositoryTag(image string) (string, string) {
	split := strings.SplitN(image, ":", 2)

	tag := "latest"
	if len(split) > 1 {
		tag = split[1]
	}
	return split[0], tag
}

func (g *generator) Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment, imageTag string) (*HelmValues, error) {
	repository, tag := splitRepositoryTag(cfg.Image)
	// The tag the deployment was submitted with is the one the user asked to
	// deploy, so it overrides a tag pinned in the image reference.
	if imageTag != "" {
		tag = imageTag
	}

	var replicaCount *int
	if cfg.Deployment != nil && cfg.Deployment.Replicas != nil {
		replicaCount = serviceconfig.Ptr(int(*cfg.Deployment.Replicas))
	}
	var servicePort *int
	if cfg.Service != nil && cfg.Service.Port != nil {
		servicePort = serviceconfig.Ptr(int(*cfg.Service.Port))
	}

	vals := &HelmValues{
		ReplicaCount: replicaCount,
		Image: Image{
			Repository: repository,
			Tag:        tag,
			PullPolicy: "IfNotPresent",
		},
		NameOverride:     cfg.Name,
		FullnameOverride: cfg.Name,
		Service: HelmService{
			Type: "ClusterIP",
			Port: servicePort,
		},
		Env: cfg.Env,
	}

	gatewayEnabled := cfg.Gateway != nil && cfg.Gateway.HTTP != nil &&
		cfg.Gateway.HTTP.Enabled != nil && *cfg.Gateway.HTTP.Enabled

	vals.HTTPRoute = HelmHTTPRoute{Enabled: gatewayEnabled}
	if gatewayEnabled {
		hostname := ""
		if cfg.Gateway.HTTP.Hostname != nil {
			hostname = *cfg.Gateway.HTTP.Hostname
		} else if env != nil && env.DefaultIngressBaseHost != "" {
			hostname = fmt.Sprintf("%s.%s", cfg.Name, env.DefaultIngressBaseHost)
		}
		if hostname != "" {
			vals.HTTPRoute.Hostnames = []string{hostname}
		}

		vals.HTTPRoute.Rules = cfg.Gateway.HTTP.Rules
		if len(vals.HTTPRoute.Rules) == 0 {
			vals.HTTPRoute.Rules = []serviceconfig.HTTPRouteRule{{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Path: &serviceconfig.HTTPPathMatch{
						Type:  serviceconfig.Ptr("PathPrefix"),
						Value: serviceconfig.Ptr("/"),
					},
				}},
			}}
		}
	}

	if mgmt := managementOf(cfg); mgmt != nil {
		// Probes are opt-in: a nil probe stays nil here so omitempty keeps the
		// key out of the values file and the chart configures no probe at all.
		vals.LivenessProbe = probeValues(mgmt.LivenessProbe, servicePort)
		vals.ReadinessProbe = probeValues(mgmt.ReadinessProbe, servicePort)

		if mgmt.Metrics != nil && mgmt.Metrics.Endpoint != "" {
			if vals.PodAnnotations == nil {
				vals.PodAnnotations = make(map[string]string)
			}
			metricsPort, _ := resolveProbePort(mgmt.Metrics.Port, servicePort)
			vals.PodAnnotations["prometheus.io/scrape"] = "true"
			vals.PodAnnotations["prometheus.io/port"] = fmt.Sprintf("%d", metricsPort)
			vals.PodAnnotations["prometheus.io/path"] = mgmt.Metrics.Endpoint
		}
	}

	// Config files (values are already rendered by the deployment service)
	for name, cfgFile := range cfg.Configs {
		encoded, err := encodeConfigFile(cfgFile.Format, cfgFile.Values)
		if err != nil {
			return nil, fmt.Errorf("encode config %q: %w", name, err)
		}

		vals.Configs = append(vals.Configs, HelmConfig{
			Enabled:       true,
			MountLocation: cfgFile.MountPath,
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
	case ".env":
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
		escaped := strings.ReplaceAll(fmt.Sprintf("%v", v), "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "$", "\\$")
		line := fmt.Sprintf("%s=\"%s\"\n", k, escaped)
		result = append(result, []byte(line)...)
	}
	return result, nil
}
