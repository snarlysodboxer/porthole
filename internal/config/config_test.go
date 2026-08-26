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

func TestAnnotationAllowed(t *testing.T) {
	cfg := &Config{AllowedAnnotations: []string{
		"example.com/team",
		"kubectl.kubernetes.io/last-applied-configuration",
	}}
	if !cfg.AnnotationAllowed("example.com/team") {
		t.Error("allowlisted key should be allowed")
	}
	if cfg.AnnotationAllowed("example.com/other") {
		t.Error("non-allowlisted key should be denied")
	}
	// The hard denylist wins even over an explicit allowlist entry.
	if cfg.AnnotationAllowed("kubectl.kubernetes.io/last-applied-configuration") {
		t.Error("last-applied must be denied even when allowlisted")
	}
	if cfg.AnnotationAllowed("operator.example.com/last-applied-state") {
		t.Error("any last-applied-style key must be denied")
	}
}

func TestClusterKindAllowed(t *testing.T) {
	cfg := &Config{ClusterKinds: []string{"ClusterIssuer"}}
	if !cfg.ClusterKindAllowed("clusterissuer") {
		t.Error("cluster kind match should be case-insensitive")
	}
	if cfg.ClusterKindAllowed("StorageClass") {
		t.Error("kinds off the allowlist should be denied")
	}
}

func TestViewsFromConfigFile(t *testing.T) {
	path := writeConfig(t, `
views:
  - group: cert-manager.io
    kind: Certificate
    facts:
      - name: notAfter
        path: .status.notAfter
`)
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.ViewFor("cert-manager.io", "certificate")
	if v == nil || len(v.Facts) != 1 || v.Facts[0].Path != ".status.notAfter" {
		t.Fatalf("view = %+v", v)
	}
	if cfg.ViewFor("cert-manager.io", "Issuer") != nil {
		t.Error("no view should match an unknown kind")
	}
}

func TestViewAnyRootAllowed(t *testing.T) {
	// No spec/status gate: field choice is the operator's responsibility;
	// the scalar-only extractor is the structural guarantee.
	path := writeConfig(t, `
views:
  - group: example.com
    kind: Widget
    facts:
      - name: host
        path: .spec.host
      - name: containerNames
        path: .spec.template.spec.containers[*].name
      - name: firstCondition
        path: .status.conditions[0].type
`)
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ViewFor("example.com", "Widget") == nil {
		t.Error("view should load")
	}
}

func TestViewBadPathsRejected(t *testing.T) {
	for _, path := range []string{"status.phase", ".status..phase", "", ".status.conditions[x]", ".status.items[*", ".status.[*]"} {
		yaml := `
views:
  - group: example.com
    kind: Widget
    facts:
      - name: f
        path: "` + path + `"
`
		if _, err := Load([]string{"--config", writeConfig(t, yaml)}); err == nil {
			t.Errorf("path %q should be rejected", path)
		}
	}
}

func TestParseFactPath(t *testing.T) {
	path, err := ParseFactPath(".spec.containers[*].env[0].name")
	if err != nil {
		t.Fatal(err)
	}
	segs := path.Segments
	if len(segs) != 4 || len(path.Projection) != 0 {
		t.Fatalf("path = %+v", path)
	}
	if segs[1].Name != "containers" || len(segs[1].Indexes) != 1 || segs[1].Indexes[0] != WildcardIndex {
		t.Errorf("containers segment = %+v", segs[1])
	}
	if segs[2].Name != "env" || segs[2].Indexes[0] != 0 {
		t.Errorf("env segment = %+v", segs[2])
	}
	if len(segs[3].Indexes) != 0 {
		t.Errorf("name segment = %+v", segs[3])
	}
}

func TestParseFactPathProjection(t *testing.T) {
	path, err := ParseFactPath(".spec.tolerations[*].{key, effect, env[*].name}")
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Segments) != 2 || path.Segments[1].Name != "tolerations" {
		t.Fatalf("segments = %+v", path.Segments)
	}
	if len(path.Projection) != 3 {
		t.Fatalf("projection = %+v", path.Projection)
	}
	if path.Projection[0].Label != "key" || path.Projection[1].Label != "effect" {
		t.Errorf("labels = %+v", path.Projection)
	}
	nested := path.Projection[2]
	if nested.Label != "env[*].name" || len(nested.Segments) != 2 || nested.Segments[0].Indexes[0] != WildcardIndex {
		t.Errorf("nested entry = %+v", nested)
	}

	// A projection on the object root is one element with no walk.
	root, err := ParseFactPath(".{a,b}")
	if err != nil || len(root.Segments) != 0 || len(root.Projection) != 2 {
		t.Errorf("root projection = %+v (%v)", root, err)
	}

	for _, bad := range []string{
		".spec.{a}.b",   // not the final element
		".spec.{a,{b}}", // nested
		".spec.{}",      // empty group
		".spec.{a,,b}",  // empty entry
		".{}",           // empty everything
	} {
		if _, err := ParseFactPath(bad); err == nil {
			t.Errorf("path %q should be rejected", bad)
		}
	}
}

func TestConfigDirectory(t *testing.T) {
	// A mounted ConfigMap presents each data key as a file; the directory
	// composes into one config document.
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("mode", "http")
	write("listenAddr", `":9999"`)
	write("enableLogs", "true")
	write("namespaces", "[prod, dev]")
	write("views", `
- group: cert-manager.io
  kind: Certificate
  facts:
    - name: notAfter
      path: .status.notAfter
`)
	// views-<suffix> keys concatenate into the views list, so overlays can
	// append views without overriding the base's key.
	write("views-argocd", `
- group: argoproj.io
  kind: Application
  facts:
    - name: syncStatus
      path: .status.sync.status
`)
	// ConfigMap volume machinery must be ignored.
	if err := os.Mkdir(filepath.Join(dir, "..2026_08_25"), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{"--config", dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "http" || cfg.ListenAddr != ":9999" || !cfg.EnableLogs {
		t.Errorf("cfg = %+v", cfg)
	}
	if len(cfg.Namespaces) != 2 || cfg.Namespaces[0] != "prod" {
		t.Errorf("namespaces = %v", cfg.Namespaces)
	}
	if len(cfg.Views) != 2 {
		t.Fatalf("views = %+v, want the views and views-argocd lists concatenated", cfg.Views)
	}
	if cfg.ViewFor("cert-manager.io", "Certificate") == nil || cfg.ViewFor("argoproj.io", "Application") == nil {
		t.Error("both views files should load")
	}

	// A views-* file must hold a list.
	write("views-bad", "not: a list")
	if _, err := Load([]string{"--config", dir}); err == nil {
		t.Error("non-list views-* key should be rejected")
	}
	if err := os.Remove(filepath.Join(dir, "views-bad")); err != nil {
		t.Fatal(err)
	}

	// A key that cannot be read must fail the load, not silently drop
	// config (a dangling symlink makes os.Stat fail).
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "maxEvents")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--config", dir}); err == nil {
		t.Error("an unreadable config key must fail closed")
	}
	if err := os.Remove(filepath.Join(dir, "maxEvents")); err != nil {
		t.Fatal(err)
	}

	// A typo'd filename is an unknown config key and must be rejected.
	write("enableLogz", "true")
	if _, err := Load([]string{"--config", dir}); err == nil {
		t.Error("unknown config key file should be rejected")
	}
}

func TestViewsMergeSameGroupKind(t *testing.T) {
	// Two views for the same group+kind merge their facts (in order), so
	// an overlay's views-<suffix> key can extend a kind the base already
	// covers instead of silently shadowing it.
	path := writeConfig(t, `
views:
  - group: cert-manager.io
    kind: Certificate
    facts:
      - name: notAfter
        path: .status.notAfter
  - group: cert-manager.io
    kind: certificate
    facts:
      - name: renewalTime
        path: .status.renewalTime
`)
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Views) != 1 {
		t.Fatalf("views = %+v, want merged into one", cfg.Views)
	}
	v := cfg.ViewFor("cert-manager.io", "Certificate")
	if v == nil || len(v.Facts) != 2 || v.Facts[0].Name != "notAfter" || v.Facts[1].Name != "renewalTime" {
		t.Errorf("merged view = %+v", v)
	}
}

func TestViewsMergeFactOverride(t *testing.T) {
	// A later fact reusing a name overrides the earlier one (last wins),
	// so an overlay can redefine a base fact, not just add new ones.
	path := writeConfig(t, `
views:
  - group: cert-manager.io
    kind: Certificate
    facts:
      - name: notAfter
        path: .status.notAfter
      - name: renewalTime
        path: .status.renewalTime
  - group: cert-manager.io
    kind: Certificate
    facts:
      - name: notAfter
        path: .status.conditions[0].lastTransitionTime
`)
	cfg, err := Load([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	v := cfg.ViewFor("cert-manager.io", "Certificate")
	if v == nil || len(v.Facts) != 2 {
		t.Fatalf("merged view = %+v, want 2 facts with no duplicate", v)
	}
	if v.Facts[0].Name != "notAfter" || v.Facts[0].Path != ".status.conditions[0].lastTransitionTime" {
		t.Errorf("overridden fact = %+v, want the later path in the original position", v.Facts[0])
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
