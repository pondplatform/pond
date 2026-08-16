package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Context struct {
	Server  string `yaml:"server,omitempty"`
	Token   string `yaml:"token,omitempty"`
	Project string `yaml:"project,omitempty"`
	Env     string `yaml:"env,omitempty"`
}

type Config struct {
	CurrentContext string             `yaml:"current-context,omitempty"`
	Contexts       map[string]Context `yaml:"contexts,omitempty"`
}

type Flags struct {
	Server  string
	Token   string
	Project string
	Env     string
	Context string
}

type ResolvedConfig struct {
	Server  string
	Token   string
	Project string
	Env     string
}

// ClientKey is used to store the server client in cobra's context.
type ClientKey struct{}

// ResolvedConfigKey is used to store the resolved config in cobra's context.
type ResolvedConfigKey struct{}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".pond", "config"), nil
}

// Load reads the config file at path. A missing file is not an error — an empty config is returned.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{Contexts: map[string]Context{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return &cfg, nil
}

// Save writes cfg to path atomically. It creates the parent directory if needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// Resolve applies the four-tier priority chain: flags > env vars > active context > defaults.
func Resolve(flags Flags, cfg *Config) (ResolvedConfig, error) {
	contextName := flags.Context
	if contextName == "" {
		contextName = cfg.CurrentContext
	}

	var activeCtx Context
	if contextName != "" {
		ctx, ok := cfg.Contexts[contextName]
		if !ok {
			return ResolvedConfig{}, fmt.Errorf("context %q not found", contextName)
		}
		activeCtx = ctx
	}

	pick := func(flag, envVar, ctxVal, def string) string {
		if flag != "" {
			return flag
		}
		if v := os.Getenv(envVar); v != "" {
			return v
		}
		if ctxVal != "" {
			return ctxVal
		}
		return def
	}

	return ResolvedConfig{
		Server:  pick(flags.Server, "POND_SERVER_URL", activeCtx.Server, "http://localhost:8080"),
		Token:   pick(flags.Token, "POND_TOKEN", activeCtx.Token, ""),
		Project: pick(flags.Project, "POND_PROJECT", activeCtx.Project, ""),
		Env:     pick(flags.Env, "POND_ENV", activeCtx.Env, ""),
	}, nil
}
