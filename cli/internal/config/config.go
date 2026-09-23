package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Environment variables the CLI reads. Every field storable in a context has one,
// named POND_ plus the screaming-snake form of its flag.
const (
	EnvServerURL     = "POND_SERVER_URL"
	EnvToken         = "POND_TOKEN"
	EnvProject       = "POND_PROJECT"
	EnvProjectID     = "POND_PROJECT_ID"
	EnvEnvironment   = "POND_ENV"
	EnvContext       = "POND_CONTEXT"
	EnvConfig        = "POND_CONFIG"
	EnvKubeContext   = "POND_KUBE_CONTEXT"
	EnvKubeNamespace = "POND_KUBE_NAMESPACE"
	EnvKubeService   = "POND_KUBE_SERVICE"
	EnvForwardPort   = "POND_KUBECTL_FORWARD_PORT"
)

const defaultServerURL = "http://localhost:8080"

type Context struct {
	Server      string      `yaml:"server,omitempty"`
	Token       string      `yaml:"token,omitempty"`
	Project     string      `yaml:"project,omitempty"`
	Env         string      `yaml:"env,omitempty"`
	PortForward PortForward `yaml:"port-forward,omitempty"`
}

// PortForward describes the kubectl port-forward the CLI starts on demand when the
// server is only reachable from inside the cluster. Port is the local port kubectl
// binds; it is independent of Server, which is what the CLI actually dials.
type PortForward struct {
	KubeContext   string `yaml:"kube-context,omitempty"`
	KubeNamespace string `yaml:"kube-namespace,omitempty"`
	KubeService   string `yaml:"kube-service,omitempty"`
	Port          int    `yaml:"kubectl-forward-port,omitempty"`
}

// Configured reports whether the context has enough information to start a forward.
func (p PortForward) Configured() bool {
	return p.KubeContext != "" && p.KubeNamespace != "" && p.Port != 0
}

type Config struct {
	CurrentContext string             `yaml:"current-context,omitempty"`
	Contexts       map[string]Context `yaml:"contexts,omitempty"`
}

type Flags struct {
	Server      string
	Token       string
	Project     string
	Env         string
	Context     string
	PortForward PortForward
}

type ResolvedConfig struct {
	Server      string
	Token       string
	Project     string
	Env         string
	PortForward PortForward
}

// ErrProjectRequired is shared by every command that needs a project, so the list of
// ways to supply one is stated in exactly one place.
var ErrProjectRequired = fmt.Errorf(
	"project is required: use --project, %s or %s, or configure a context",
	EnvProject, EnvProjectID)

// ClientKey is used to store the server client in cobra's context.
type ClientKey struct{}

// ResolvedConfigKey is used to store the resolved config in cobra's context.
type ResolvedConfigKey struct{}

// ConfigPath returns POND_CONFIG when set, else ~/.pond/config. The value is a single
// path: unlike KUBECONFIG there is no ':'-separated merge list and no tilde expansion,
// and a relative path resolves against the current working directory.
func ConfigPath() (string, error) {
	if p := env(EnvConfig); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".pond", "config"), nil
}

// env returns the value of the first variable set to a non-empty value.
func env(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// itoa renders a port for the resolution chain, mapping the unset zero value to the
// empty string so that "unset" stays distinguishable from an explicit 0.
func itoa(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func parsePort(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("must be a port number between 1 and 65535")
	}
	return port, nil
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
		contextName = env(EnvContext)
	}
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

	pick := func(flag, envVal, ctxVal, def string) string {
		if flag != "" {
			return flag
		}
		if envVal != "" {
			return envVal
		}
		if ctxVal != "" {
			return ctxVal
		}
		return def
	}

	pf := activeCtx.PortForward

	// Only the env var can carry a non-numeric port: the flag is int-typed by cobra and
	// the config file by yaml, so those fail before Resolve runs. Either can still be
	// out of range, hence the second message.
	portEnv := env(EnvForwardPort)
	portStr := pick(itoa(flags.PortForward.Port), portEnv, itoa(pf.Port), "")
	port, err := parsePort(portStr)
	if err != nil {
		if portStr == portEnv {
			return ResolvedConfig{}, fmt.Errorf("%s=%q: %w", EnvForwardPort, portStr, err)
		}
		return ResolvedConfig{}, fmt.Errorf("port-forward port %q: %w", portStr, err)
	}

	return ResolvedConfig{
		Server:  pick(flags.Server, env(EnvServerURL), activeCtx.Server, defaultServerURL),
		Token:   pick(flags.Token, env(EnvToken), activeCtx.Token, ""),
		Project: pick(flags.Project, env(EnvProject, EnvProjectID), activeCtx.Project, ""),
		Env:     pick(flags.Env, env(EnvEnvironment), activeCtx.Env, ""),
		PortForward: PortForward{
			KubeContext:   pick(flags.PortForward.KubeContext, env(EnvKubeContext), pf.KubeContext, ""),
			KubeNamespace: pick(flags.PortForward.KubeNamespace, env(EnvKubeNamespace), pf.KubeNamespace, ""),
			KubeService:   pick(flags.PortForward.KubeService, env(EnvKubeService), pf.KubeService, ""),
			Port:          port,
		},
	}, nil
}
