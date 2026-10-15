package helmgen

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
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

// deploymentIDAnnotation differs on every deployment, so each one changes the
// pod template and rolls the pods even when nothing else changed.
const deploymentIDAnnotation = "pond/deployment-id"

func (g *generator) Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment, imageTag string, deploymentID uuid.UUID) (*HelmValues, error) {
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
		Strategy:     HelmStrategy{Type: "RollingUpdate"},
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
		Env:            cfg.Env,
		PodAnnotations: map[string]string{deploymentIDAnnotation: deploymentID.String()},
	}

	for _, arg := range cfg.Command {
		vals.Args = append(vals.Args, argEscaper.Replace(arg))
	}

	vals.HTTPRoute = HelmHTTPRoute{Enabled: gatewayEnabled(cfg)}
	if vals.HTTPRoute.Enabled {
		if hostname := GatewayHostname(cfg, env); hostname != "" {
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
			metricsPort, _ := resolveProbePort(mgmt.Metrics.Port, servicePort)
			vals.PodAnnotations["prometheus.io/scrape"] = "true"
			vals.PodAnnotations["prometheus.io/port"] = fmt.Sprintf("%d", metricsPort)
			vals.PodAnnotations["prometheus.io/path"] = mgmt.Metrics.Endpoint
		}
	}

	// Config files (values are already rendered by the deployment service).
	// The chart names each one's Secret and pod volume by its list index, so
	// map order would rename them on every deploy and roll the pod for nothing.
	for _, name := range slices.Sorted(maps.Keys(cfg.Configs)) {
		cfgFile := cfg.Configs[name]
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

	addVolumes(vals, cfg)
	addConfigChecksum(vals)

	return vals, nil
}

func gatewayEnabled(cfg *serviceconfig.ServiceConfig) bool {
	return cfg.Gateway != nil && cfg.Gateway.HTTP != nil &&
		cfg.Gateway.HTTP.Enabled != nil && *cfg.Gateway.HTTP.Enabled
}

// GatewayHostname is the hostname the service's route matches: the one
// pond.yml sets, else <name>.<environment base host>. It is "" when the
// gateway is disabled or neither is set.
func GatewayHostname(cfg *serviceconfig.ServiceConfig, env *domain.Environment) string {
	switch {
	case !gatewayEnabled(cfg):
		return ""
	case cfg.Gateway.HTTP.Hostname != nil:
		return *cfg.Gateway.HTTP.Hostname
	case env != nil && env.DefaultIngressBaseHost != "":
		return cfg.Name + "." + env.DefaultIngressBaseHost
	}
	return ""
}

// addConfigChecksum rolls the pods when env or a config file changes: a
// changed Secret alone never reaches a running pod, least of all via subPath.
func addConfigChecksum(vals *HelmValues) {
	if len(vals.Env) == 0 && len(vals.Configs) == 0 {
		return
	}
	// Map keys encode sorted and Configs is in name order, so the sum is stable.
	data, _ := json.Marshal(struct {
		Env     map[string]string
		Configs []HelmConfig
	}{vals.Env, vals.Configs})
	vals.PodAnnotations["checksum/config"] = fmt.Sprintf("%x", sha256.Sum256(data))
}

// argEscaper doubles every $ so arguments reach the app verbatim: Kubernetes
// expands $(VAR) in container args and reduces $$ to $.
var argEscaper = strings.NewReplacer("$", "$$")

// volumeGroupID owns new volume files so images that run as a non-root user
// can write to them. 1000 is the first user most base images create.
const volumeGroupID = 1000

func addVolumes(vals *HelmValues, cfg *serviceconfig.ServiceConfig) {
	if len(cfg.Volumes) == 0 {
		return
	}

	// Every old pod stops before a new one starts, so the old and the new
	// version never write the same files at once, and a ReadWriteOnce claim is
	// free to follow the pods to another node.
	vals.Strategy.Type = "Recreate"
	vals.PodSecurityContext = &HelmPodSecurityContext{
		FSGroup: serviceconfig.Ptr(int64(volumeGroupID)),
		// Only chown when the volume's root doesn't match, rather than walking
		// every file on each start.
		FSGroupChangePolicy: "OnRootMismatch",
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Volumes)) {
		volume := cfg.Volumes[name]
		podVolume := "vol-" + name
		vals.PersistentVolumeClaims = append(vals.PersistentVolumeClaims, HelmPersistentVolumeClaim{Name: name, Size: volume.Size})
		vals.Volumes = append(vals.Volumes, HelmVolume{
			Name:                  podVolume,
			PersistentVolumeClaim: HelmPersistentVolumeClaimRef{ClaimName: vals.FullnameOverride + "-" + name},
		})
		vals.VolumeMounts = append(vals.VolumeMounts, HelmVolumeMount{Name: podVolume, MountPath: volume.MountPath})
	}
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

// envDoubleQuoteEscaper leaves $ alone: Node's dotenv and python-dotenv keep
// a \$ verbatim rather than unescaping it.
var envDoubleQuoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// marshalEnv single-quotes values because dotenv readers take single-quoted
// text literally: no escapes and no $ interpolation. A value that contains a
// single quote or a line break cannot be single-quoted, so it falls back to
// double quotes and the common \\ \" \n \r escapes.
func marshalEnv(values map[string]any) ([]byte, error) {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(values)) {
		v := fmt.Sprintf("%v", values[k])
		if strings.ContainsAny(v, "'\n\r") {
			fmt.Fprintf(&b, "%s=\"%s\"\n", k, envDoubleQuoteEscaper.Replace(v))
		} else {
			fmt.Fprintf(&b, "%s='%s'\n", k, v)
		}
	}
	return []byte(b.String()), nil
}
