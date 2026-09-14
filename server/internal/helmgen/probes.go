package helmgen

import (
	"fmt"
	"strings"

	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

const managementComponent = "deployment.management"

// managementOf returns the management block, or nil when the config declares none.
func managementOf(cfg *serviceconfig.ServiceConfig) *serviceconfig.ManagementConfig {
	if cfg.Deployment == nil {
		return nil
	}
	return cfg.Deployment.Management
}

// resolveProbePort falls back to the service port when no explicit port is
// given. ok is false when neither is set, which leaves the caller no valid
// port to probe.
func resolveProbePort(explicit *int32, servicePort *int) (int, bool) {
	if explicit != nil {
		return int(*explicit), true
	}
	if servicePort != nil {
		return *servicePort, true
	}
	return 0, false
}

// probeValues maps a user-declared probe onto Helm values. A nil probe yields
// nil so the values file omits the key entirely and no probe is configured.
// A probe without httpGet is rejected by ValidateManagement at submit time; it
// is treated as absent here rather than panicking on an older stored snapshot.
func probeValues(p *serviceconfig.Probe, servicePort *int) *HelmProbe {
	if p == nil || p.HTTPGet == nil {
		return nil
	}

	port, _ := resolveProbePort(p.HTTPGet.Port, servicePort)

	return &HelmProbe{
		HTTPGet: HelmHTTPGet{
			Path:        p.HTTPGet.Path,
			Port:        port,
			Scheme:      p.HTTPGet.Scheme,
			HTTPHeaders: p.HTTPGet.HTTPHeaders,
		},
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
		TimeoutSeconds:      p.TimeoutSeconds,
		SuccessThreshold:    p.SuccessThreshold,
		FailureThreshold:    p.FailureThreshold,
	}
}

// ValidateManagement checks the probe and metrics configuration. It returns a
// *api.ValidationErrors so the caller can reject a submission with a per-field
// 422 rather than letting a malformed probe surface later as a failed rollout.
func ValidateManagement(cfg *serviceconfig.ServiceConfig) error {
	mgmt := managementOf(cfg)
	if mgmt == nil {
		return nil
	}

	var servicePort *int
	if cfg.Service != nil && cfg.Service.Port != nil {
		servicePort = serviceconfig.Ptr(int(*cfg.Service.Port))
	}

	var errs api.ValidationErrors
	validateProbe(&errs, "livenessProbe", mgmt.LivenessProbe, servicePort)
	validateProbe(&errs, "readinessProbe", mgmt.ReadinessProbe, servicePort)

	// Kubernetes rejects a liveness probe that needs more than one success:
	// there is nothing to recover to, so the field is meaningless there.
	if p := mgmt.LivenessProbe; p != nil && p.SuccessThreshold != nil && *p.SuccessThreshold != 1 {
		errs.Add(managementComponent, "livenessProbe.successThreshold", "must be 1 for a liveness probe")
	}

	validateMetrics(&errs, mgmt.Metrics, servicePort)

	if errs.HasErrors() {
		return &errs
	}
	return nil
}

func validateProbe(errs *api.ValidationErrors, name string, p *serviceconfig.Probe, servicePort *int) {
	if p == nil {
		return
	}

	if p.HTTPGet == nil {
		errs.Add(managementComponent, name+".httpGet", "is required: only HTTP probes are supported")
		return
	}

	validatePath(errs, name+".httpGet.path", p.HTTPGet.Path)

	if _, ok := resolveProbePort(p.HTTPGet.Port, servicePort); !ok {
		errs.Add(managementComponent, name+".httpGet.port", "is required when service.port is not set")
	} else if p.HTTPGet.Port != nil {
		validatePort(errs, name+".httpGet.port", *p.HTTPGet.Port)
	}

	if s := p.HTTPGet.Scheme; s != nil && *s != "HTTP" && *s != "HTTPS" {
		errs.Add(managementComponent, name+".httpGet.scheme", fmt.Sprintf("must be HTTP or HTTPS, got %q", *s))
	}

	for i, h := range p.HTTPGet.HTTPHeaders {
		if h.Name == "" {
			errs.Add(managementComponent, fmt.Sprintf("%s.httpGet.httpHeaders[%d].name", name, i), "must not be empty")
		}
	}

	if v := p.InitialDelaySeconds; v != nil && *v < 0 {
		errs.Add(managementComponent, name+".initialDelaySeconds", "must not be negative")
	}
	atLeastOne(errs, name+".periodSeconds", p.PeriodSeconds)
	atLeastOne(errs, name+".timeoutSeconds", p.TimeoutSeconds)
	atLeastOne(errs, name+".successThreshold", p.SuccessThreshold)
	atLeastOne(errs, name+".failureThreshold", p.FailureThreshold)
}

func validateMetrics(errs *api.ValidationErrors, m *serviceconfig.MetricsConfig, servicePort *int) {
	if m == nil || m.Endpoint == "" {
		return
	}

	validatePath(errs, "metrics.endpoint", m.Endpoint)

	if _, ok := resolveProbePort(m.Port, servicePort); !ok {
		errs.Add(managementComponent, "metrics.port", "is required when service.port is not set")
	} else if m.Port != nil {
		validatePort(errs, "metrics.port", *m.Port)
	}
}

func validatePath(errs *api.ValidationErrors, field, path string) {
	switch {
	case path == "":
		errs.Add(managementComponent, field, "must not be empty")
	case !strings.HasPrefix(path, "/"):
		errs.Add(managementComponent, field, fmt.Sprintf("must start with %q, got %q", "/", path))
	}
}

func validatePort(errs *api.ValidationErrors, field string, port int32) {
	if port < 1 || port > 65535 {
		errs.Add(managementComponent, field, fmt.Sprintf("must be between 1 and 65535, got %d", port))
	}
}

func atLeastOne(errs *api.ValidationErrors, field string, v *int32) {
	if v != nil && *v < 1 {
		errs.Add(managementComponent, field, fmt.Sprintf("must be at least 1, got %d", *v))
	}
}
