package domain

// ServiceConfig is the fully-resolved configuration for deploying a service
// to a specific environment (overrides already applied).
type ServiceConfig struct {
	Version int    `json:"version" yaml:"version"`
	Name    string `json:"name"    yaml:"name"`
	Image   string `json:"image"   yaml:"image"`
	Build   string `json:"build"   yaml:"build"`

	Ingress IngressConfig    `json:"ingress"    yaml:"ingress"`
	Service ServiceSpec      `json:"service"    yaml:"service"`
	Manage  ManagementConfig `json:"management" yaml:"management"`

	Dependencies map[string]DependencyDeclaration `json:"dependencies" yaml:"dependencies"`
	Env          map[string]string                `json:"env"          yaml:"env"`
	Configs      map[string]ConfigFileSpec        `json:"configs"      yaml:"configs"`
}

type IngressConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type ServiceSpec struct {
	Port     int32 `json:"port"     yaml:"port"`
	Replicas int32 `json:"replicas" yaml:"replicas"`
}

type ManagementConfig struct {
	Metrics MetricsConfig `json:"metrics" yaml:"metrics"`
	Health  HealthConfig  `json:"health"  yaml:"health"`
}

type MetricsConfig struct {
	Port     int    `json:"port"     yaml:"port"`
	Endpoint string `json:"endpoint" yaml:"endpoint"`
}

type HealthConfig struct {
	Port     int    `json:"port"     yaml:"port"`
	Endpoint string `json:"endpoint" yaml:"endpoint"`
}

type ConfigFileSpec struct {
	Format   string         `json:"format"   yaml:"format"`
	MountDir string         `json:"mountDir" yaml:"mountDir"`
	Values   map[string]any `json:"values"   yaml:"values"`
}

type DependencyDeclaration struct {
	Type   string         `json:"type"   yaml:"type"`
	Config map[string]any `json:"config" yaml:"config"`
}

// OverridableConfig is the raw form of a pond.yml: a base ServiceConfig
// plus a map of per-environment overrides.
type OverridableConfig struct {
	ServiceConfig `yaml:",inline" json:",inline"`
	Overrides     map[string]Override `yaml:"overrides" json:"overrides,omitempty"`
}

type Override struct {
	Ingress      *IngressOverride              `yaml:"ingress"      json:"ingress,omitempty"`
	Service      *ServiceOverride              `yaml:"service"      json:"service,omitempty"`
	Env          map[string]string             `yaml:"env"          json:"env,omitempty"`
	Dependencies map[string]DependencyDeclaration `yaml:"dependencies" json:"dependencies,omitempty"`
	Configs      map[string]ConfigFileSpec    `yaml:"configs"      json:"configs,omitempty"`
}

type IngressOverride struct {
	Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
}

type ServiceOverride struct {
	Replicas *int32 `yaml:"replicas" json:"replicas,omitempty"`
}
