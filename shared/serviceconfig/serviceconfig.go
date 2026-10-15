package serviceconfig

import "regexp"

// Ptr returns a pointer to v. Useful when constructing ServiceConfig literals.
func Ptr[T any](v T) *T { return &v }

// TemplateVar matches a {{var}} reference and captures the variable's name.
var TemplateVar = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

// ServiceConfig is the fully-resolved configuration for deploying a service
// to a specific environment (overrides already applied).
type ServiceConfig struct {
	Version int    `json:"version,omitempty" yaml:"version"`
	Name    string `json:"name"              yaml:"name"`
	Image   string `json:"image,omitempty"   yaml:"image"`
	Build   string `json:"build,omitempty"   yaml:"build"`

	// Command replaces the image's CMD and keeps its ENTRYPOINT. omitzero rather
	// than omitempty, so an empty list reaches the server and is rejected
	// instead of arriving as unset.
	Command []string `json:"command,omitzero" yaml:"command"`

	Deployment *DeploymentSpec `json:"deployment,omitempty" yaml:"deployment"`
	Service    *ServiceSpec    `json:"service,omitempty"    yaml:"service"`
	Gateway    *GatewayConfig  `json:"gateway,omitempty"    yaml:"gateway"`

	Dependencies map[string]DependencyDeclaration `json:"dependencies,omitempty" yaml:"dependencies"`
	Env          map[string]string                `json:"env,omitempty"          yaml:"env"`
	Configs      map[string]ConfigFileSpec        `json:"configs,omitempty"      yaml:"configs"`

	// Volumes has no counterpart in Override: a service needs the same storage
	// in every environment.
	Volumes map[string]VolumeSpec `json:"volumes,omitempty" yaml:"volumes"`

	// Variables has no counterpart in Override: the input file is already per environment.
	Variables map[string]VariableDeclaration `json:"variables,omitempty" yaml:"variables"`
}

// VariableDeclaration is a {{variables.<name>}} whose value comes from the deployment input.
type VariableDeclaration struct {
	// Secret keeps the value out of command and the gateway, which anyone who
	// can read the workload sees.
	Secret bool `json:"secret,omitempty" yaml:"secret"`
}

// VolumeSpec is a directory that survives restarts and redeploys. Its map key
// identifies the data, so renaming a volume gives the service a new, empty one.
type VolumeSpec struct {
	// Type says which copies of the service mount the volume, and from where.
	// It is required, so pond.yml states that behaviour rather than inheriting
	// it. "shared-single-node" is the only type: one volume that every copy of
	// the service mounts, attachable to one node at a time.
	Type      string `json:"type"      yaml:"type"`
	MountPath string `json:"mountPath" yaml:"mountPath"`
	// Size is a whole number of Mi, Gi or Ti.
	Size string `json:"size" yaml:"size"`
}

type DeploymentSpec struct {
	Replicas   *int32            `json:"replicas,omitempty"   yaml:"replicas,omitempty"`
	Management *ManagementConfig `json:"management,omitempty" yaml:"management,omitempty"`
}

type ServiceSpec struct {
	Port *int32 `json:"port,omitempty" yaml:"port"`
}

type GatewayConfig struct {
	HTTP *GatewayHTTPConfig `json:"http,omitempty" yaml:"http,omitempty"`
}

type GatewayHTTPConfig struct {
	Enabled  *bool           `json:"enabled,omitempty"  yaml:"enabled,omitempty"`
	Hostname *string         `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Rules    []HTTPRouteRule `json:"rules,omitempty"    yaml:"rules,omitempty"`
}

// The HTTPRoute types below mirror gateway-api v1 HTTPRouteRule minus
// backendRefs and parentRefs, which Pond owns and the chart supplies.
// They are marshalled back out into the Helm values file, so the yaml tags
// need omitempty to keep nulls and empty lists out of the rendered HTTPRoute.

type HTTPRouteRule struct {
	Matches []HTTPRouteMatch  `json:"matches,omitempty" yaml:"matches,omitempty"`
	Filters []HTTPRouteFilter `json:"filters,omitempty" yaml:"filters,omitempty"`
}

type HTTPRouteMatch struct {
	Path        *HTTPPathMatch    `json:"path,omitempty"        yaml:"path,omitempty"`
	Headers     []HTTPHeaderMatch `json:"headers,omitempty"     yaml:"headers,omitempty"`
	QueryParams []HTTPHeaderMatch `json:"queryParams,omitempty" yaml:"queryParams,omitempty"`
	Method      *string           `json:"method,omitempty"      yaml:"method,omitempty"`
}

type HTTPPathMatch struct {
	// Type is Exact, PathPrefix or RegularExpression.
	Type  *string `json:"type,omitempty"  yaml:"type,omitempty"`
	Value *string `json:"value,omitempty" yaml:"value,omitempty"`
}

type HTTPHeaderMatch struct {
	// Type is Exact or RegularExpression.
	Type  *string `json:"type,omitempty" yaml:"type,omitempty"`
	Name  string  `json:"name"           yaml:"name"`
	Value string  `json:"value"          yaml:"value"`
}

type HTTPRouteFilter struct {
	Type                   string                     `json:"type"                             yaml:"type"`
	RequestHeaderModifier  *HTTPHeaderFilter          `json:"requestHeaderModifier,omitempty"  yaml:"requestHeaderModifier,omitempty"`
	ResponseHeaderModifier *HTTPHeaderFilter          `json:"responseHeaderModifier,omitempty" yaml:"responseHeaderModifier,omitempty"`
	RequestRedirect        *HTTPRequestRedirectFilter `json:"requestRedirect,omitempty"        yaml:"requestRedirect,omitempty"`
	URLRewrite             *HTTPURLRewriteFilter      `json:"urlRewrite,omitempty"             yaml:"urlRewrite,omitempty"`
	RequestMirror          *HTTPRequestMirrorFilter   `json:"requestMirror,omitempty"          yaml:"requestMirror,omitempty"`
	ExtensionRef           *LocalObjectReference      `json:"extensionRef,omitempty"           yaml:"extensionRef,omitempty"`
}

type HTTPHeaderFilter struct {
	Set    []HTTPHeader `json:"set,omitempty"    yaml:"set,omitempty"`
	Add    []HTTPHeader `json:"add,omitempty"    yaml:"add,omitempty"`
	Remove []string     `json:"remove,omitempty" yaml:"remove,omitempty"`
}

type HTTPHeader struct {
	Name  string `json:"name"  yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

type HTTPRequestRedirectFilter struct {
	Scheme     *string           `json:"scheme,omitempty"     yaml:"scheme,omitempty"`
	Hostname   *string           `json:"hostname,omitempty"   yaml:"hostname,omitempty"`
	Path       *HTTPPathModifier `json:"path,omitempty"       yaml:"path,omitempty"`
	Port       *int32            `json:"port,omitempty"       yaml:"port,omitempty"`
	StatusCode *int              `json:"statusCode,omitempty" yaml:"statusCode,omitempty"`
}

type HTTPURLRewriteFilter struct {
	Hostname *string           `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Path     *HTTPPathModifier `json:"path,omitempty"     yaml:"path,omitempty"`
}

type HTTPPathModifier struct {
	// Type is ReplaceFullPath or ReplacePrefixMatch.
	Type               string  `json:"type"                         yaml:"type"`
	ReplaceFullPath    *string `json:"replaceFullPath,omitempty"    yaml:"replaceFullPath,omitempty"`
	ReplacePrefixMatch *string `json:"replacePrefixMatch,omitempty" yaml:"replacePrefixMatch,omitempty"`
}

type HTTPRequestMirrorFilter struct {
	BackendRef LocalObjectReference `json:"backendRef" yaml:"backendRef"`
}

type LocalObjectReference struct {
	Group *string `json:"group,omitempty" yaml:"group,omitempty"`
	Kind  *string `json:"kind,omitempty"  yaml:"kind,omitempty"`
	Name  string  `json:"name"            yaml:"name"`
	Port  *int32  `json:"port,omitempty"  yaml:"port,omitempty"`
}

type ManagementConfig struct {
	LivenessProbe  *Probe         `json:"livenessProbe,omitempty"  yaml:"livenessProbe,omitempty"`
	ReadinessProbe *Probe         `json:"readinessProbe,omitempty" yaml:"readinessProbe,omitempty"`
	Metrics        *MetricsConfig `json:"metrics,omitempty"        yaml:"metrics,omitempty"`
}

// Probe mirrors core/v1 Probe restricted to the httpGet handler: Pond only
// supports HTTP checks, so exec, tcpSocket and grpc are deliberately absent.
// An omitted probe means no probe is configured on the container at all.
type Probe struct {
	HTTPGet             *HTTPGetAction `json:"httpGet,omitempty"             yaml:"httpGet,omitempty"`
	InitialDelaySeconds *int32         `json:"initialDelaySeconds,omitempty" yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       *int32         `json:"periodSeconds,omitempty"       yaml:"periodSeconds,omitempty"`
	TimeoutSeconds      *int32         `json:"timeoutSeconds,omitempty"      yaml:"timeoutSeconds,omitempty"`
	SuccessThreshold    *int32         `json:"successThreshold,omitempty"    yaml:"successThreshold,omitempty"`
	FailureThreshold    *int32         `json:"failureThreshold,omitempty"    yaml:"failureThreshold,omitempty"`
}

type HTTPGetAction struct {
	Path string `json:"path" yaml:"path"`
	// Port defaults to service.port when unset.
	Port *int32 `json:"port,omitempty" yaml:"port,omitempty"`
	// Scheme is HTTP or HTTPS.
	Scheme      *string      `json:"scheme,omitempty"      yaml:"scheme,omitempty"`
	HTTPHeaders []HTTPHeader `json:"httpHeaders,omitempty" yaml:"httpHeaders,omitempty"`
}

type MetricsConfig struct {
	// Port defaults to service.port when unset.
	Port     *int32 `json:"port,omitempty"     yaml:"port,omitempty"`
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
}

type ConfigFileSpec struct {
	Format    string         `json:"format,omitempty"   yaml:"format"`
	MountPath string         `json:"mountPath,omitempty" yaml:"mountPath"`
	Values    map[string]any `json:"values,omitempty"   yaml:"values"`
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
	Command      []string                         `yaml:"command"      json:"command,omitzero"`
	Deployment   *DeploymentSpec                  `yaml:"deployment"   json:"deployment,omitempty"`
	Service      *ServiceSpec                     `yaml:"service"      json:"service,omitempty"`
	Gateway      *GatewayConfig                   `yaml:"gateway"      json:"gateway,omitempty"`
	Env          map[string]string                `yaml:"env"          json:"env,omitempty"`
	Dependencies map[string]DependencyDeclaration `yaml:"dependencies" json:"dependencies,omitempty"`
	Configs      map[string]ConfigFileSpec        `yaml:"configs"      json:"configs,omitempty"`
}
