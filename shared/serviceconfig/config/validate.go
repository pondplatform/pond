package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// schemaVersion is the only pond.yml schema version Pond understands.
const schemaVersion = 1

// validationComponent tags every error with the artifact the user has to edit
// to fix it, so a client can group config problems apart from server-side ones.
const validationComponent = "config"

// maxServiceNameLen is Helm's limit on a release name, and the service name
// becomes the release name.
const maxServiceNameLen = 53

const maxHostnameLen = 253

var (
	dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

	// Gateway API hostnames are lowercase DNS names, optionally prefixed with a
	// single wildcard label.
	hostnamePattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

	// envVarNamePattern mirrors the Kubernetes env var name rule; anything else
	// is rejected when the pod is created.
	envVarNamePattern = regexp.MustCompile(`^[-._a-zA-Z][-._a-zA-Z0-9]*$`)

	// Config templates reference a dependency as {{name.field}} and the
	// renderer's pattern only matches word characters, so a name outside that
	// set can never be referenced.
	dependencyNamePattern = regexp.MustCompile(`^\w+$`)
)

// configFormats are the formats config files can be rendered as. Kept in step
// with the encoder in the Helm values generator.
var configFormats = []string{".env", "json", "yaml", "yml"}

var httpMethods = []string{"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"}

var filterTypes = []string{
	"RequestHeaderModifier",
	"ResponseHeaderModifier",
	"RequestRedirect",
	"URLRewrite",
	"RequestMirror",
	"ExtensionRef",
}

// Validate checks a resolved ServiceConfig against what Pond, Helm and
// Kubernetes accept. It reports every problem it finds as a
// *api.ValidationErrors so a bad pond.yml is rejected up front with per-field
// errors, rather than surfacing later as a failed rollout the user has to read
// Helm output to understand.
func Validate(cfg *serviceconfig.ServiceConfig) error {
	var errs api.ValidationErrors

	switch {
	case cfg.Version == 0:
		errs.Add(validationComponent, "version", fmt.Sprintf("is required and must be %d", schemaVersion))
	case cfg.Version != schemaVersion:
		errs.Add(validationComponent, "version", fmt.Sprintf("must be %d, got %d", schemaVersion, cfg.Version))
	}

	switch {
	case cfg.Name == "":
		errs.Add(validationComponent, "name", "must not be empty")
	case len(cfg.Name) > maxServiceNameLen:
		errs.Add(validationComponent, "name", fmt.Sprintf("must be at most %d characters, got %d", maxServiceNameLen, len(cfg.Name)))
	case !dns1123Label.MatchString(cfg.Name):
		errs.Add(validationComponent, "name", fmt.Sprintf("must be lowercase alphanumeric characters or %q, starting and ending with an alphanumeric character, got %q", "-", cfg.Name))
	}

	if cfg.Image == "" && cfg.Build == "" {
		errs.Add(validationComponent, "image", "is required unless build is set")
	}

	var servicePort *int32
	if cfg.Service != nil {
		servicePort = cfg.Service.Port
		if cfg.Service.Port != nil {
			validatePort(&errs, "service.port", *cfg.Service.Port)
		}
	}

	validateDeployment(&errs, cfg.Deployment, servicePort)
	validateGateway(&errs, cfg.Gateway)
	validateEnv(&errs, cfg.Env)
	validateConfigs(&errs, cfg.Configs)
	validateDependencies(&errs, cfg.Dependencies)

	if errs.HasErrors() {
		return &errs
	}
	return nil
}

func validateDeployment(errs *api.ValidationErrors, dep *serviceconfig.DeploymentSpec, servicePort *int32) {
	if dep == nil {
		return
	}

	if r := dep.Replicas; r != nil && *r < 0 {
		errs.Add(validationComponent, "deployment.replicas", fmt.Sprintf("must not be negative, got %d", *r))
	}

	mgmt := dep.Management
	if mgmt == nil {
		return
	}

	const prefix = "deployment.management."
	validateProbe(errs, prefix+"livenessProbe", mgmt.LivenessProbe, servicePort)
	validateProbe(errs, prefix+"readinessProbe", mgmt.ReadinessProbe, servicePort)

	// Kubernetes rejects a liveness probe that needs more than one success:
	// there is nothing to recover to, so the field is meaningless there.
	if p := mgmt.LivenessProbe; p != nil && p.SuccessThreshold != nil && *p.SuccessThreshold != 1 {
		errs.Add(validationComponent, prefix+"livenessProbe.successThreshold", "must be 1 for a liveness probe")
	}

	if m := mgmt.Metrics; m != nil && m.Endpoint != "" {
		validatePath(errs, prefix+"metrics.endpoint", m.Endpoint)
		validateResolvedPort(errs, prefix+"metrics.port", m.Port, servicePort)
	}
}

func validateProbe(errs *api.ValidationErrors, field string, p *serviceconfig.Probe, servicePort *int32) {
	if p == nil {
		return
	}

	if p.HTTPGet == nil {
		errs.Add(validationComponent, field+".httpGet", "is required: only HTTP probes are supported")
		return
	}

	validatePath(errs, field+".httpGet.path", p.HTTPGet.Path)
	validateResolvedPort(errs, field+".httpGet.port", p.HTTPGet.Port, servicePort)

	if s := p.HTTPGet.Scheme; s != nil && *s != "HTTP" && *s != "HTTPS" {
		errs.Add(validationComponent, field+".httpGet.scheme", fmt.Sprintf("must be HTTP or HTTPS, got %q", *s))
	}

	for i, h := range p.HTTPGet.HTTPHeaders {
		if h.Name == "" {
			errs.Add(validationComponent, fmt.Sprintf("%s.httpGet.httpHeaders[%d].name", field, i), "must not be empty")
		}
	}

	if v := p.InitialDelaySeconds; v != nil && *v < 0 {
		errs.Add(validationComponent, field+".initialDelaySeconds", "must not be negative")
	}
	atLeastOne(errs, field+".periodSeconds", p.PeriodSeconds)
	atLeastOne(errs, field+".timeoutSeconds", p.TimeoutSeconds)
	atLeastOne(errs, field+".successThreshold", p.SuccessThreshold)
	atLeastOne(errs, field+".failureThreshold", p.FailureThreshold)
}

func validateGateway(errs *api.ValidationErrors, gw *serviceconfig.GatewayConfig) {
	if gw == nil || gw.HTTP == nil {
		return
	}

	if h := gw.HTTP.Hostname; h != nil {
		validateHostname(errs, "gateway.http.hostname", *h)
	}

	for i, rule := range gw.HTTP.Rules {
		field := fmt.Sprintf("gateway.http.rules[%d]", i)
		for j, match := range rule.Matches {
			validateMatch(errs, fmt.Sprintf("%s.matches[%d]", field, j), match)
		}
		for j, filter := range rule.Filters {
			validateFilter(errs, fmt.Sprintf("%s.filters[%d]", field, j), filter)
		}
	}
}

func validateMatch(errs *api.ValidationErrors, field string, m serviceconfig.HTTPRouteMatch) {
	if p := m.Path; p != nil {
		// Gateway API defaults an omitted match type to PathPrefix.
		matchType := "PathPrefix"
		if p.Type != nil {
			matchType = *p.Type
		}
		switch matchType {
		case "Exact", "PathPrefix":
			validatePath(errs, field+".path.value", deref(p.Value))
		case "RegularExpression":
			if deref(p.Value) == "" {
				errs.Add(validationComponent, field+".path.value", "must not be empty")
			}
		default:
			errs.Add(validationComponent, field+".path.type", fmt.Sprintf("must be Exact, PathPrefix or RegularExpression, got %q", matchType))
		}
	}

	for i, h := range m.Headers {
		validateHeaderMatch(errs, fmt.Sprintf("%s.headers[%d]", field, i), h)
	}
	for i, q := range m.QueryParams {
		validateHeaderMatch(errs, fmt.Sprintf("%s.queryParams[%d]", field, i), q)
	}

	if method := m.Method; method != nil && !slices.Contains(httpMethods, *method) {
		errs.Add(validationComponent, field+".method", fmt.Sprintf("must be an uppercase HTTP method, got %q", *method))
	}
}

func validateHeaderMatch(errs *api.ValidationErrors, field string, h serviceconfig.HTTPHeaderMatch) {
	if h.Name == "" {
		errs.Add(validationComponent, field+".name", "must not be empty")
	}
	if h.Value == "" {
		errs.Add(validationComponent, field+".value", "must not be empty")
	}
	if t := h.Type; t != nil && *t != "Exact" && *t != "RegularExpression" {
		errs.Add(validationComponent, field+".type", fmt.Sprintf("must be Exact or RegularExpression, got %q", *t))
	}
}

func validateFilter(errs *api.ValidationErrors, field string, f serviceconfig.HTTPRouteFilter) {
	if !slices.Contains(filterTypes, f.Type) {
		errs.Add(validationComponent, field+".type", fmt.Sprintf("must be one of %s, got %q", strings.Join(filterTypes, ", "), f.Type))
		return
	}

	// Gateway API allows only the block matching type, so a leftover block from
	// another filter type is a copy-paste error rather than a harmless extra.
	blocks := []struct {
		filterType string
		field      string
		present    bool
	}{
		{"RequestHeaderModifier", "requestHeaderModifier", f.RequestHeaderModifier != nil},
		{"ResponseHeaderModifier", "responseHeaderModifier", f.ResponseHeaderModifier != nil},
		{"RequestRedirect", "requestRedirect", f.RequestRedirect != nil},
		{"URLRewrite", "urlRewrite", f.URLRewrite != nil},
		{"RequestMirror", "requestMirror", f.RequestMirror != nil},
		{"ExtensionRef", "extensionRef", f.ExtensionRef != nil},
	}
	for _, b := range blocks {
		switch {
		case b.filterType == f.Type && !b.present:
			errs.Add(validationComponent, field+"."+b.field, fmt.Sprintf("is required when type is %s", f.Type))
		case b.filterType != f.Type && b.present:
			errs.Add(validationComponent, field+"."+b.field, fmt.Sprintf("must not be set when type is %s", f.Type))
		}
	}

	switch f.Type {
	case "RequestHeaderModifier":
		validateHeaderFilter(errs, field+".requestHeaderModifier", f.RequestHeaderModifier)
	case "ResponseHeaderModifier":
		validateHeaderFilter(errs, field+".responseHeaderModifier", f.ResponseHeaderModifier)
	case "RequestRedirect":
		validateRedirect(errs, field+".requestRedirect", f.RequestRedirect)
	case "URLRewrite":
		validateURLRewrite(errs, field+".urlRewrite", f.URLRewrite)
	case "RequestMirror":
		if f.RequestMirror != nil && f.RequestMirror.BackendRef.Name == "" {
			errs.Add(validationComponent, field+".requestMirror.backendRef.name", "must not be empty")
		}
	case "ExtensionRef":
		if f.ExtensionRef != nil && f.ExtensionRef.Name == "" {
			errs.Add(validationComponent, field+".extensionRef.name", "must not be empty")
		}
	}
}

func validateHeaderFilter(errs *api.ValidationErrors, field string, f *serviceconfig.HTTPHeaderFilter) {
	if f == nil {
		return
	}

	if len(f.Set) == 0 && len(f.Add) == 0 && len(f.Remove) == 0 {
		errs.Add(validationComponent, field, "must set at least one of set, add or remove")
	}

	for i, h := range f.Set {
		if h.Name == "" {
			errs.Add(validationComponent, fmt.Sprintf("%s.set[%d].name", field, i), "must not be empty")
		}
	}
	for i, h := range f.Add {
		if h.Name == "" {
			errs.Add(validationComponent, fmt.Sprintf("%s.add[%d].name", field, i), "must not be empty")
		}
	}
	for i, name := range f.Remove {
		if name == "" {
			errs.Add(validationComponent, fmt.Sprintf("%s.remove[%d]", field, i), "must not be empty")
		}
	}
}

func validateRedirect(errs *api.ValidationErrors, field string, r *serviceconfig.HTTPRequestRedirectFilter) {
	if r == nil {
		return
	}

	if s := r.Scheme; s != nil && *s != "http" && *s != "https" {
		errs.Add(validationComponent, field+".scheme", fmt.Sprintf("must be http or https, got %q", *s))
	}
	if h := r.Hostname; h != nil {
		validateHostname(errs, field+".hostname", *h)
	}
	if p := r.Port; p != nil {
		validatePort(errs, field+".port", *p)
	}
	// Gateway API only defines the two redirect codes a Gateway can answer with.
	if c := r.StatusCode; c != nil && *c != 301 && *c != 302 {
		errs.Add(validationComponent, field+".statusCode", fmt.Sprintf("must be 301 or 302, got %d", *c))
	}
	validatePathModifier(errs, field+".path", r.Path)
}

func validateURLRewrite(errs *api.ValidationErrors, field string, r *serviceconfig.HTTPURLRewriteFilter) {
	if r == nil {
		return
	}

	if r.Hostname == nil && r.Path == nil {
		errs.Add(validationComponent, field, "must set hostname, path or both")
	}
	if h := r.Hostname; h != nil {
		validateHostname(errs, field+".hostname", *h)
	}
	validatePathModifier(errs, field+".path", r.Path)
}

func validatePathModifier(errs *api.ValidationErrors, field string, m *serviceconfig.HTTPPathModifier) {
	if m == nil {
		return
	}

	switch m.Type {
	case "ReplaceFullPath":
		validatePath(errs, field+".replaceFullPath", deref(m.ReplaceFullPath))
		if m.ReplacePrefixMatch != nil {
			errs.Add(validationComponent, field+".replacePrefixMatch", "must not be set when type is ReplaceFullPath")
		}
	case "ReplacePrefixMatch":
		validatePath(errs, field+".replacePrefixMatch", deref(m.ReplacePrefixMatch))
		if m.ReplaceFullPath != nil {
			errs.Add(validationComponent, field+".replaceFullPath", "must not be set when type is ReplacePrefixMatch")
		}
	default:
		errs.Add(validationComponent, field+".type", fmt.Sprintf("must be ReplaceFullPath or ReplacePrefixMatch, got %q", m.Type))
	}
}

func validateEnv(errs *api.ValidationErrors, env map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(env)) {
		if !envVarNamePattern.MatchString(name) {
			errs.Add(validationComponent, fmt.Sprintf("env[%q]", name), "must be a valid environment variable name")
		}
	}
}

func validateConfigs(errs *api.ValidationErrors, configs map[string]serviceconfig.ConfigFileSpec) {
	mountedBy := make(map[string]string, len(configs))
	for _, name := range slices.Sorted(maps.Keys(configs)) {
		spec := configs[name]
		field := fmt.Sprintf("configs[%q]", name)

		if name == "" {
			errs.Add(validationComponent, field, "must not be empty")
		}

		if !slices.Contains(configFormats, spec.Format) {
			errs.Add(validationComponent, field+".format", fmt.Sprintf("must be one of %s, got %q", strings.Join(configFormats, ", "), spec.Format))
		}

		switch {
		case spec.MountPath == "":
			errs.Add(validationComponent, field+".mountPath", "must not be empty")
		case !strings.HasPrefix(spec.MountPath, "/"):
			errs.Add(validationComponent, field+".mountPath", fmt.Sprintf("must be an absolute path, got %q", spec.MountPath))
		case strings.HasSuffix(spec.MountPath, "/"):
			errs.Add(validationComponent, field+".mountPath", fmt.Sprintf("must be the full path of the file, not a directory, got %q", spec.MountPath))
		case mountedBy[spec.MountPath] != "":
			errs.Add(validationComponent, field+".mountPath", fmt.Sprintf("%q is already mounted by configs[%q]", spec.MountPath, mountedBy[spec.MountPath]))
		default:
			mountedBy[spec.MountPath] = name
		}
	}
}

func validateDependencies(errs *api.ValidationErrors, deps map[string]serviceconfig.DependencyDeclaration) {
	for _, name := range slices.Sorted(maps.Keys(deps)) {
		field := fmt.Sprintf("dependencies[%q]", name)

		if !dependencyNamePattern.MatchString(name) {
			errs.Add(validationComponent, field, "must be letters, digits or underscores so config templates can reference it as {{name.field}}")
		}
		if deps[name].Type == "" {
			errs.Add(validationComponent, field+".type", "must not be empty")
		}
	}
}

func validateHostname(errs *api.ValidationErrors, field, hostname string) {
	switch {
	case hostname == "":
		errs.Add(validationComponent, field, "must not be empty")
	case len(hostname) > maxHostnameLen:
		errs.Add(validationComponent, field, fmt.Sprintf("must be at most %d characters, got %d", maxHostnameLen, len(hostname)))
	case !hostnamePattern.MatchString(hostname):
		errs.Add(validationComponent, field, fmt.Sprintf("must be a lowercase DNS hostname without scheme, port or path, got %q", hostname))
	}
}

func validatePath(errs *api.ValidationErrors, field, path string) {
	switch {
	case path == "":
		errs.Add(validationComponent, field, "must not be empty")
	case !strings.HasPrefix(path, "/"):
		errs.Add(validationComponent, field, fmt.Sprintf("must start with %q, got %q", "/", path))
	}
}

func validatePort(errs *api.ValidationErrors, field string, port int32) {
	if port < 1 || port > 65535 {
		errs.Add(validationComponent, field, fmt.Sprintf("must be between 1 and 65535, got %d", port))
	}
}

// validateResolvedPort checks a port that falls back to service.port when unset.
func validateResolvedPort(errs *api.ValidationErrors, field string, explicit, servicePort *int32) {
	switch {
	case explicit != nil:
		validatePort(errs, field, *explicit)
	case servicePort == nil:
		errs.Add(validationComponent, field, "is required when service.port is not set")
	}
}

func atLeastOne(errs *api.ValidationErrors, field string, v *int32) {
	if v != nil && *v < 1 {
		errs.Add(validationComponent, field, fmt.Sprintf("must be at least 1, got %d", *v))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
