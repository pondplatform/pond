package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

type parser struct{}

func NewParser() ConfigParser {
	return &parser{}
}

func (p *parser) Parse(r io.Reader) (*OverridableConfig, error) {
	var cfg OverridableConfig
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode pond.yml: %w", err)
	}
	return &cfg, nil
}

func (p *parser) ParseFile(path string) (*OverridableConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return p.Parse(f)
}
