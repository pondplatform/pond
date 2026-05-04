package config

import (
	"io"

	"github.com/pondplatform/pond/internal/common/serviceconfig"
)

// ConfigParser reads and parses a pond.yml file.
type ConfigParser interface {
	Parse(r io.Reader) (*serviceconfig.OverridableConfig, error)
	ParseFile(path string) (*serviceconfig.OverridableConfig, error)
}

// ConfigResolver applies environment overrides to produce a final ServiceConfig.
type ConfigResolver interface {
	Resolve(base *serviceconfig.OverridableConfig, envName string) (*serviceconfig.ServiceConfig, error)
}

// TemplateRenderer interpolates {{var}} placeholders in config file values
// using resolved dependency contexts (dependency name → output values).
type TemplateRenderer interface {
	Render(values map[string]any, contexts map[string]map[string]any, svcConfig *serviceconfig.ServiceConfig) (map[string]any, error)
}
