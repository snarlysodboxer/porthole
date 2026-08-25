package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "stdio" || cfg.ListenAddr != ":8080" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !cfg.CRDConditionsEnabled() {
		t.Error("resource_conditions should default to enabled")
	}
	if cfg.EnableCRDStatus || cfg.EnableLogs {
		t.Error("resource_status and pod_logs should default to disabled")
	}
	if cfg.MaxLogBytes != DefaultMaxLogBytes || cfg.MaxEvents != DefaultMaxEvents {
		t.Errorf("ceilings = %d/%d", cfg.MaxLogBytes, cfg.MaxEvents)
	}
}

func TestConfigFile(t *testing.T) {
	path := writeConfig(t, `
mode: http
listenAddr: ":9999"
namespaces: [prod, dev]
enableCRDConditions: false
enableCRDStatus: true
enableLogs: true
maxLogBytes: 1000
maxEvents: 10
`)
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "http" || cfg.ListenAddr != ":9999" {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Namespaces) != 2 {
		t.Errorf("namespaces = %v", cfg.Namespaces)
	}
	if cfg.CRDConditionsEnabled() || !cfg.EnableCRDStatus || !cfg.EnableLogs {
		t.Errorf("features = %+v", cfg)
	}
}

func TestFlagsOverrideFile(t *testing.T) {
	path := writeConfig(t, `
mode: http
namespaces: [prod]
maxEvents: 10
`)
	cfg, err := Load([]string{
		"--config", path,
		"--mode", "stdio",
		"--namespaces", "a, b,c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "stdio" {
		t.Errorf("mode = %q, want flag to win", cfg.Mode)
	}
	if len(cfg.Namespaces) != 3 || cfg.Namespaces[1] != "b" {
		t.Errorf("namespaces = %v", cfg.Namespaces)
	}
	if cfg.MaxEvents != 10 {
		t.Errorf("maxEvents = %d, want file value kept when flag unset", cfg.MaxEvents)
	}
}

func TestInvalidMode(t *testing.T) {
	if _, err := Load([]string{"--mode", "carrier-pigeon"}); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestUnknownConfigKeyRejected(t *testing.T) {
	path := writeConfig(t, "modee: http\n")
	if _, err := Load([]string{"--config", path}); err == nil {
		t.Fatal("expected strict YAML parsing to reject unknown keys")
	}
}

func TestNamespaceAllowed(t *testing.T) {
	cfg := &Config{Namespaces: []string{"prod"}}
	if !cfg.NamespaceAllowed("prod") || cfg.NamespaceAllowed("dev") {
		t.Error("allowlist not enforced")
	}
	open := &Config{}
	if !open.NamespaceAllowed("anything") {
		t.Error("empty allowlist should allow everything (RBAC is the boundary)")
	}
}
