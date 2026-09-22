package helmgen

import (
	"github.com/pondplatform/pond/shared/serviceconfig"
)

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
// A probe without httpGet is rejected by config.Validate at submit time; it
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
