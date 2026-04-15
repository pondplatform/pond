package config

import "github.com/pondplatform/pond/internal/common/domain"

type resolver struct{}

func NewResolver() ConfigResolver {
	return &resolver{}
}

func (r *resolver) Resolve(base *domain.OverridableConfig, envName string) (*domain.ServiceConfig, error) {
	// Start with a copy of the base config.
	cfg := base.ServiceConfig

	// Initialize maps if nil.
	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	if cfg.Dependencies == nil {
		cfg.Dependencies = make(map[string]domain.DependencyDeclaration)
	}
	if cfg.Configs == nil {
		cfg.Configs = make(map[string]domain.ConfigFileSpec)
	}

	override, ok := base.Overrides[envName]
	if !ok {
		return &cfg, nil
	}

	// Apply scalar overrides.
	if override.Ingress != nil && override.Ingress.Enabled != nil {
		cfg.Ingress.Enabled = *override.Ingress.Enabled
	}
	if override.Service != nil && override.Service.Replicas != nil {
		cfg.Service.Replicas = *override.Service.Replicas
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
