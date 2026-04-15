package config

import (
	"io"

	"github.com/pondplatform/pond/internal/common/domain"
)

// ConfigParser reads and parses a pond.yml file.
type ConfigParser interface {
	Parse(r io.Reader) (*domain.OverridableConfig, error)
	ParseFile(path string) (*domain.OverridableConfig, error)
}

// ConfigResolver applies environment overrides to produce a final ServiceConfig.
type ConfigResolver interface {
	Resolve(base *domain.OverridableConfig, envName string) (*domain.ServiceConfig, error)
}

// TemplateRenderer interpolates {{var}} placeholders in config file values
// using resolved dependency contexts (dependency name → output values).
type TemplateRenderer interface {
	Render(values map[string]any, contexts map[string]map[string]any, svcConfig *domain.ServiceConfig) (map[string]any, error)
}
