package config

import (
	"io"

	"github.com/pondplatform/pond/shared/serviceconfig"
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
