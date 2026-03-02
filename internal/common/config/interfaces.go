package config

import (
	"io"

	"github.com/pondplatform/pond/internal/common/domain"
)

// ConfigParser reads and parses a pond.yml file.
type ConfigParser interface {
	Parse(r io.Reader) (*OverridableConfig, error)
	ParseFile(path string) (*OverridableConfig, error)
}

// ConfigResolver applies environment overrides to produce a final ServiceConfig.
type ConfigResolver interface {
	Resolve(base *OverridableConfig, envName string) (*domain.ServiceConfig, error)
}

// TemplateRenderer interpolates {{var}} placeholders in config file values
// using resolved dependency contexts.
type TemplateRenderer interface {
	Render(values map[string]any, contexts map[string]domain.ResolvedContext, svcConfig *domain.ServiceConfig) (map[string]any, error)
}
