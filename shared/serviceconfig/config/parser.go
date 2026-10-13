package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// yaml.v3 reports each field problem as "line N: <message>", and an unknown
// key as "field <key> not found in type <Go type>".
var (
	yamlErrorLine    = regexp.MustCompile(`^line (\d+): (.*)$`)
	yamlUnknownField = regexp.MustCompile(`^field (.+) not found in type \S+$`)
)

type parser struct{}

func NewParser() ConfigParser {
	return &parser{}
}

func (p *parser) Parse(r io.Reader) (*serviceconfig.OverridableConfig, error) {
	return parse(r, "pond.yml")
}

func (p *parser) ParseFile(path string) (*serviceconfig.OverridableConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return parse(f, path)
}

func parse(r io.Reader, name string) (*serviceconfig.OverridableConfig, error) {
	var cfg serviceconfig.OverridableConfig
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return nil, describeTypeError(name, typeErr)
		}
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return &cfg, nil
}

func describeTypeError(name string, typeErr *yaml.TypeError) error {
	msgs := make([]string, len(typeErr.Errors))
	for i, msg := range typeErr.Errors {
		m := yamlErrorLine.FindStringSubmatch(msg)
		if m == nil {
			msgs[i] = fmt.Sprintf("%s: %s", name, msg)
			continue
		}
		line, detail := m[1], m[2]
		if field := yamlUnknownField.FindStringSubmatch(detail); field != nil {
			detail = fmt.Sprintf("unknown field %q", field[1])
		}
		msgs[i] = fmt.Sprintf("%s:%s: %s", name, line, detail)
	}
	return errors.New(strings.Join(msgs, "; "))
}
