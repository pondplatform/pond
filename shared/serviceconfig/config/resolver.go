package config

import "github.com/pondplatform/pond/shared/serviceconfig"

type resolver struct{}

func NewResolver() ConfigResolver {
	return &resolver{}
}

func (r *resolver) Resolve(base *serviceconfig.OverridableConfig, envName string) (*serviceconfig.ServiceConfig, error) {
	cfg := base.ServiceConfig

	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	if cfg.Dependencies == nil {
		cfg.Dependencies = make(map[string]serviceconfig.DependencyDeclaration)
	}
	if cfg.Configs == nil {
		cfg.Configs = make(map[string]serviceconfig.ConfigFileSpec)
	}

	override, ok := base.Overrides[envName]
	if !ok {
		return &cfg, nil
	}

	// Apply deployment override: copy struct before mutating to avoid sharing with base.
	if override.Deployment != nil {
		deploymentCopy := serviceconfig.DeploymentSpec{}
		if cfg.Deployment != nil {
			deploymentCopy = *cfg.Deployment
		}
		if override.Deployment.Replicas != nil {
			deploymentCopy.Replicas = override.Deployment.Replicas
		}
		if override.Deployment.Management != nil {
			deploymentCopy.Management = mergeManagement(deploymentCopy.Management, override.Deployment.Management)
		}
		cfg.Deployment = &deploymentCopy
	}

	// Apply service override: copy struct before mutating to avoid sharing with base.
	if override.Service != nil {
		serviceCopy := serviceconfig.ServiceSpec{}
		if cfg.Service != nil {
			serviceCopy = *cfg.Service
		}
		if override.Service.Port != nil {
			serviceCopy.Port = override.Service.Port
		}
		cfg.Service = &serviceCopy
	}

	// Apply gateway override: copy struct before mutating to avoid sharing with base.
	if override.Gateway != nil && override.Gateway.HTTP != nil {
		httpCopy := serviceconfig.GatewayHTTPConfig{}
		if cfg.Gateway != nil && cfg.Gateway.HTTP != nil {
			httpCopy = *cfg.Gateway.HTTP
		}
		if override.Gateway.HTTP.Enabled != nil {
			httpCopy.Enabled = override.Gateway.HTTP.Enabled
		}
		if override.Gateway.HTTP.Hostname != nil {
			httpCopy.Hostname = override.Gateway.HTTP.Hostname
		}
		// Routing rules replace rather than append: they are order-sensitive, so
		// keeping the base rules first would let them shadow the override.
		if override.Gateway.HTTP.Rules != nil {
			httpCopy.Rules = override.Gateway.HTTP.Rules
		}
		cfg.Gateway = &serviceconfig.GatewayConfig{HTTP: &httpCopy}
	}

	// Deep merge maps: override keys win.
	for k, v := range override.Env {
		cfg.Env[k] = v
	}
	for k, v := range override.Dependencies {
		cfg.Dependencies[k] = v
	}
	for k, v := range override.Configs {
		cfg.Configs[k] = v
	}

	return &cfg, nil
}

// mergeManagement replaces each present sub-block wholesale rather than merging
// field by field: a probe assembled half from the base and half from the
// override (override path, base timings) is rarely what anyone means.
func mergeManagement(base, override *serviceconfig.ManagementConfig) *serviceconfig.ManagementConfig {
	merged := serviceconfig.ManagementConfig{}
	if base != nil {
		merged = *base
	}
	if override.LivenessProbe != nil {
		merged.LivenessProbe = override.LivenessProbe
	}
	if override.ReadinessProbe != nil {
		merged.ReadinessProbe = override.ReadinessProbe
	}
	if override.Metrics != nil {
		merged.Metrics = override.Metrics
	}
	return &merged
}
