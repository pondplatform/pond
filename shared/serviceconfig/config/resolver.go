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

	// Apply ingress override: copy struct before mutating to avoid sharing with base.
	if override.Ingress != nil {
		ingressCopy := serviceconfig.IngressConfig{}
		if cfg.Ingress != nil {
			ingressCopy = *cfg.Ingress
		}
		if override.Ingress.Enabled != nil {
			ingressCopy.Enabled = override.Ingress.Enabled
		}
		cfg.Ingress = &ingressCopy
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
		if override.Service.Replicas != nil {
			serviceCopy.Replicas = override.Service.Replicas
		}
		cfg.Service = &serviceCopy
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
