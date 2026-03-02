package config

import "github.com/pondplatform/pond/internal/common/domain"

type OverridableConfig struct {
	domain.ServiceConfig `yaml:",inline"`
	Overrides            map[string]Override `yaml:"overrides"`
}

type Override struct {
	Ingress      *IngressOverride                      `yaml:"ingress"`
	Service      *ServiceOverride                      `yaml:"service"`
	Env          map[string]string                     `yaml:"env"`
	Dependencies map[string]domain.DependencyDeclaration `yaml:"dependencies"`
	Configs      map[string]domain.ConfigFileSpec       `yaml:"configs"`
}

type IngressOverride struct {
	Enabled *bool `yaml:"enabled"`
}

type ServiceOverride struct {
	Replicas *int32 `yaml:"replicas"`
}
