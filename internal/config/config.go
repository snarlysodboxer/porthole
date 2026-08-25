// Package config loads porthole's configuration from an optional YAML file
// and command-line flags. Flags override file values.
package config

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// Defaults for server-side response ceilings.
const (
	DefaultMaxLogBytes = 256 * 1024
	DefaultMaxEvents   = 100
)

// Config is the effective server configuration.
type Config struct {
	// Mode is "stdio" (local) or "http" (in-cluster streamable HTTP).
	Mode string `json:"mode"`
	// ListenAddr is the bind address in http mode.
	ListenAddr string `json:"listenAddr"`
	// Kubeconfig is a path to a kubeconfig file. Empty means in-cluster
	// config when available, falling back to client-go's default loading
	// rules ($KUBECONFIG, ~/.kube/config).
	Kubeconfig string `json:"kubeconfig"`
	// Namespaces is the namespace allowlist. Empty means no app-level
	// restriction: whatever RBAC allows.
	Namespaces []string `json:"namespaces"`
	// EnableCRDConditions gates the resource_conditions tool.
	EnableCRDConditions *bool `json:"enableCRDConditions"`
	// EnableCRDStatus gates the resource_status tool (full .status of
	// arbitrary kinds).
	EnableCRDStatus bool `json:"enableCRDStatus"`
	// EnableLogs gates the pod_logs tool.
	EnableLogs bool `json:"enableLogs"`
	// MaxLogBytes is the server-side ceiling on log response size;
	// per-request max_bytes is clamped to it.
	MaxLogBytes int64 `json:"maxLogBytes"`
	// MaxEvents is the server-side ceiling on returned events per call.
	MaxEvents int `json:"maxEvents"`
	// AuthTokenFile, if set in http mode, requires requests to carry
	// "Authorization: Bearer <token>" matching the file's contents.
	AuthTokenFile string `json:"authTokenFile"`
}

// CRDConditionsEnabled returns the effective value of EnableCRDConditions
// (default true).
func (c *Config) CRDConditionsEnabled() bool {
	return c.EnableCRDConditions == nil || *c.EnableCRDConditions
}

// NamespaceAllowed reports whether a namespace passes the allowlist. An
// empty allowlist allows everything (RBAC is then the only boundary).
func (c *Config) NamespaceAllowed(ns string) bool {
	return len(c.Namespaces) == 0 || slices.Contains(c.Namespaces, ns)
}

func defaults() *Config {
	return &Config{
		Mode:        "stdio",
		ListenAddr:  ":8080",
		MaxLogBytes: DefaultMaxLogBytes,
		MaxEvents:   DefaultMaxEvents,
	}
}

// Load parses args (excluding the program name), reads the config file if
// given, and applies flags on top. It returns the effective config.
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("porthole", flag.ContinueOnError)
	var (
		configPath    = fs.String("config", "", "path to YAML config file")
		mode          = fs.String("mode", "stdio", "transport mode: http or stdio")
		listenAddr    = fs.String("listen-addr", ":8080", "bind address in http mode")
		kubeconfig    = fs.String("kubeconfig", "", "path to kubeconfig (default: in-cluster config, then client-go loading rules)")
		namespaces    = fs.String("namespaces", "", "comma-separated namespace allowlist (empty: whatever RBAC allows)")
		crdConditions = fs.Bool("enable-crd-conditions", true, "enable the resource_conditions tool")
		crdStatus     = fs.Bool("enable-crd-status", false, "enable the resource_status tool (full .status of arbitrary kinds)")
		logs          = fs.Bool("enable-logs", false, "enable the pod_logs tool")
		maxLogBytes   = fs.Int64("max-log-bytes", DefaultMaxLogBytes, "server-side ceiling on log response bytes")
		maxEvents     = fs.Int("max-events", DefaultMaxEvents, "server-side ceiling on events returned per call")
		authTokenFile = fs.String("auth-token-file", "", "file with a static bearer token required in http mode")
	)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := defaults()
	if *configPath != "" {
		data, err := os.ReadFile(*configPath)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.UnmarshalStrict(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", *configPath, err)
		}
	}

	// Flags override file values, but only flags the user actually set.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["mode"] {
		cfg.Mode = *mode
	}
	if set["listen-addr"] {
		cfg.ListenAddr = *listenAddr
	}
	if set["kubeconfig"] {
		cfg.Kubeconfig = *kubeconfig
	}
	if set["namespaces"] {
		cfg.Namespaces = splitNonEmpty(*namespaces)
	}
	if set["enable-crd-conditions"] {
		cfg.EnableCRDConditions = crdConditions
	}
	if set["enable-crd-status"] {
		cfg.EnableCRDStatus = *crdStatus
	}
	if set["enable-logs"] {
		cfg.EnableLogs = *logs
	}
	if set["max-log-bytes"] {
		cfg.MaxLogBytes = *maxLogBytes
	}
	if set["max-events"] {
		cfg.MaxEvents = *maxEvents
	}
	if set["auth-token-file"] {
		cfg.AuthTokenFile = *authTokenFile
	}

	if cfg.Mode != "http" && cfg.Mode != "stdio" {
		return nil, fmt.Errorf("invalid mode %q: must be http or stdio", cfg.Mode)
	}
	if cfg.MaxLogBytes <= 0 {
		cfg.MaxLogBytes = DefaultMaxLogBytes
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = DefaultMaxEvents
	}

	return cfg, nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}

	return out
}
