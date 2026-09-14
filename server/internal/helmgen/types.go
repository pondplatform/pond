package helmgen

import "github.com/pondplatform/pond/shared/serviceconfig"

type HelmValues struct {
	// ReplicaCount is a pointer so an unset value is omitted and the chart
	// default applies, rather than being pinned to zero replicas.
	ReplicaCount     *int              `yaml:"replicaCount,omitempty"`
	Image            Image             `yaml:"image"`
	NameOverride     string            `yaml:"nameOverride"`
	FullnameOverride string            `yaml:"fullnameOverride"`
	Service          HelmService       `yaml:"service"`
	HTTPRoute        HelmHTTPRoute     `yaml:"httpRoute"`
	LivenessProbe    *HelmProbe        `yaml:"livenessProbe,omitempty"`
	ReadinessProbe   *HelmProbe        `yaml:"readinessProbe,omitempty"`
	Env              map[string]string `yaml:"env"`
	Configs          []HelmConfig      `yaml:"configs"`
	PodAnnotations   map[string]string `yaml:"podAnnotations,omitempty"`
	PodLabels        map[string]string `yaml:"podLabels,omitempty"`
}

type Image struct {
	Repository string `yaml:"repository"`
	Tag        string `yaml:"tag"`
	PullPolicy string `yaml:"pullPolicy"`
}

type HelmService struct {
	Type string `yaml:"type"`
	Port *int   `yaml:"port,omitempty"`
}

// HelmHTTPRoute reuses the serviceconfig rule types directly: the shapes are
// identical and the chart renders them with toYaml, so a parallel tree would
// only be a drift risk. parentRefs and backendRefs are supplied by the chart.
type HelmHTTPRoute struct {
	Enabled   bool                          `yaml:"enabled"`
	Hostnames []string                      `yaml:"hostnames,omitempty"`
	Rules     []serviceconfig.HTTPRouteRule `yaml:"rules,omitempty"`
}

type HelmProbe struct {
	HTTPGet HelmHTTPGet `yaml:"httpGet"`
}

type HelmHTTPGet struct {
	Path string `yaml:"path"`
	Port int    `yaml:"port"`
}

type HelmConfig struct {
	Enabled       bool   `yaml:"enabled"`
	MountLocation string `yaml:"mountLocation"`
	Data          string `yaml:"data"`
}
