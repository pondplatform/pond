package config

import (
	"fmt"
	"io"
	"os"

	"github.com/pondplatform/pond/internal/common/serviceconfig"
	"gopkg.in/yaml.v3"
)

type parser struct{}

func NewParser() ConfigParser {
	return &parser{}
}

func (p *parser) Parse(r io.Reader) (*serviceconfig.OverridableConfig, error) {
	var cfg serviceconfig.OverridableConfig
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode pond.yml: %w", err)
	}
	return &cfg, nil
}

func (p *parser) ParseFile(path string) (*serviceconfig.OverridableConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return p.Parse(f)
}
