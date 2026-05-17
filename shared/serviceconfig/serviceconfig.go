package serviceconfig

// Ptr returns a pointer to v. Useful when constructing ServiceConfig literals.
func Ptr[T any](v T) *T { return &v }

// ServiceConfig is the fully-resolved configuration for deploying a service
// to a specific environment (overrides already applied).
type ServiceConfig struct {
	Version int    `json:"version,omitempty" yaml:"version"`
	Name    string `json:"name"              yaml:"name"`
	Image   string `json:"image,omitempty"   yaml:"image"`
	Build   string `json:"build,omitempty"   yaml:"build"`

	Ingress *IngressConfig    `json:"ingress,omitempty"    yaml:"ingress"`
	Service *ServiceSpec      `json:"service,omitempty"    yaml:"service"`
	Manage  *ManagementConfig `json:"management,omitempty" yaml:"management"`

	Dependencies map[string]DependencyDeclaration `json:"dependencies,omitempty" yaml:"dependencies"`
	Env          map[string]string                `json:"env,omitempty"          yaml:"env"`
	Configs      map[string]ConfigFileSpec        `json:"configs,omitempty"      yaml:"configs"`
}

type IngressConfig struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled"`
}

type ServiceSpec struct {
	Port     *int32 `json:"port,omitempty"     yaml:"port"`
	Replicas *int32 `json:"replicas,omitempty" yaml:"replicas"`
}

type ManagementConfig struct {
	Metrics *MetricsConfig `json:"metrics,omitempty" yaml:"metrics"`
	Health  *HealthConfig  `json:"health,omitempty"  yaml:"health"`
}

type MetricsConfig struct {
	Port     *int   `json:"port,omitempty"     yaml:"port"`
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint"`
}

type HealthConfig struct {
	Port     *int   `json:"port,omitempty"     yaml:"port"`
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint"`
}

type ConfigFileSpec struct {
	Format   string         `json:"format,omitempty"   yaml:"format"`
	MountDir string         `json:"mountDir,omitempty" yaml:"mountDir"`
	Values   map[string]any `json:"values,omitempty"   yaml:"values"`
}

type DependencyDeclaration struct {
	Type   string         `json:"type,omitempty"   yaml:"type"`
	Config map[string]any `json:"config,omitempty" yaml:"config"`
}

// OverridableConfig is the raw form of a pond.yml: a base ServiceConfig
// plus a map of per-environment overrides.
type OverridableConfig struct {
	ServiceConfig `yaml:",inline" json:",inline"`
	Overrides     map[string]Override `yaml:"overrides" json:"overrides,omitempty"`
}

type Override struct {
	Ingress      *IngressConfig                   `yaml:"ingress"      json:"ingress,omitempty"`
	Service      *ServiceSpec                     `yaml:"service"      json:"service,omitempty"`
	Env          map[string]string                `yaml:"env"          json:"env,omitempty"`
	Dependencies map[string]DependencyDeclaration `yaml:"dependencies" json:"dependencies,omitempty"`
	Configs      map[string]ConfigFileSpec        `yaml:"configs"      json:"configs,omitempty"`
}
