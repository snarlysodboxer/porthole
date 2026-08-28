// Package config loads porthole's configuration from an optional YAML file
// (or ConfigMap-style directory of per-key files) and command-line flags.
// Flags override file values.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	// EnableSecretMetadata gates the secret_metadata tool (Secret names,
	// types, and key names - never values). Requires get/list on Secrets,
	// which is a deliberate RBAC escalation: see the README's tradeoff
	// discussion before enabling.
	EnableSecretMetadata bool `json:"enableSecretMetadata"`
	// EnableConfigMapMetadata gates the configmap_metadata tool (ConfigMap
	// names and key names - never values).
	EnableConfigMapMetadata bool `json:"enableConfigMapMetadata"`
	// AllowedAnnotations is an exact-key allowlist of annotations copied
	// into responses. A hard, non-configurable denylist wins over it: any
	// key containing "last-applied" is never served, because such
	// annotations embed whole object specs.
	AllowedAnnotations []string `json:"allowedAnnotations"`
	// ClusterKinds is a kind allowlist (case-insensitive) for which
	// resource_conditions and resource_status accept cluster-scoped
	// resources, e.g. [ClusterIssuer]. Reading them requires a
	// cluster-scoped RBAC grant.
	ClusterKinds []string `json:"clusterKinds"`
	// Views declaratively enrich resource_conditions with per-kind facts
	// pulled from scalar paths. See the View type.
	Views []View `json:"views"`
	// MaxLogBytes is the server-side ceiling on log response size;
	// per-request max_bytes is clamped to it.
	MaxLogBytes int64 `json:"maxLogBytes"`
	// MaxEvents is the server-side ceiling on returned events per call.
	MaxEvents int `json:"maxEvents"`
	// AuthTokenFile, if set in http mode, requires requests to carry
	// "Authorization: Bearer <token>" matching the file's contents.
	AuthTokenFile string `json:"authTokenFile"`
}

// View declares facts to extract for one resource kind, enriching
// resource_conditions responses. Fact paths are dotted scalar paths like
// .status.sync.revision: extraction pulls scalars (or lists of scalars,
// joined) - never maps or objects - so a view cannot smuggle whole object
// bodies. Which fields are safe to expose is a judgment porthole cannot
// make for your CRDs: the onus is on you to reference only fields that
// hold no secret material.
type View struct {
	// Group is the API group the view applies to ("" for the core group).
	Group string `json:"group"`
	// Kind is matched case-insensitively.
	Kind  string     `json:"kind"`
	Facts []ViewFact `json:"facts"`
}

// ViewFact names one fact and the dotted path it is pulled from.
type ViewFact struct {
	Name string `json:"name"`
	// Path is a dotted path rooted at the object, e.g. ".status.phase".
	// Segments may fan out over lists with [*] or pick one element with
	// [N], e.g. ".spec.template.spec.containers[*].name". A trailing
	// {a,b,...} group projects several fields per reached element, keeping
	// them associated, e.g. ".spec.tolerations[*].{key,effect,operator}".
	// Every leaf must still be a scalar.
	Path string `json:"path"`
}

// WildcardIndex marks a [*] list operation in a PathSegment.
const WildcardIndex = -1

// PathSegment is one parsed element of a view fact path: a field name plus
// any list operations applied to it, in order.
type PathSegment struct {
	Name string
	// Indexes are list operations applied after the field lookup:
	// WildcardIndex means every element, otherwise the element at that
	// position.
	Indexes []int
}

// ProjectionEntry is one field of a trailing {a,b,...} projection group: a
// relative path evaluated against each element the main segments reach.
type ProjectionEntry struct {
	// Label is the entry as written, used to label the rendered value.
	Label    string
	Segments []PathSegment
}

// FactPath is a parsed view fact path.
type FactPath struct {
	Segments []PathSegment
	// Projection, when non-empty, renders each reached element as
	// "{label: value, ...}" instead of the element itself.
	Projection []ProjectionEntry
}

var (
	factPathSegment = regexp.MustCompile(`^([^\[\]]+)((?:\[(?:\*|\d+)\])*)$`)
	factPathListOp  = regexp.MustCompile(`\[(\*|\d+)\]`)
)

// ParseFactPath parses and validates a view fact path.
func ParseFactPath(path string) (*FactPath, error) {
	if !strings.HasPrefix(path, ".") {
		return nil, fmt.Errorf("path %q must start with '.', like .status.phase", path)
	}
	body := strings.TrimPrefix(path, ".")

	// Split off a trailing {a,b,...} projection group.
	var projection string
	hasProjection := false
	if i := strings.Index(body, "{"); i >= 0 {
		if !strings.HasSuffix(body, "}") {
			return nil, fmt.Errorf("path %q: a {a,b,...} projection must be the final element, like .spec.tolerations[*].{key,effect}", path)
		}
		if i > 0 && body[i-1] != '.' {
			return nil, fmt.Errorf("path %q: a {a,b,...} projection must follow a dot, like .spec.tolerations[*].{key,effect}", path)
		}
		projection = body[i+1 : len(body)-1]
		hasProjection = true
		if i == 0 {
			body = ""
		} else {
			body = body[:i-1]
		}
		if strings.ContainsAny(projection, "{}") {
			return nil, fmt.Errorf("path %q: projections cannot nest", path)
		}
	}
	if strings.ContainsAny(body, "{}") {
		return nil, fmt.Errorf("path %q: a {a,b,...} projection must be the final element", path)
	}

	out := &FactPath{}
	if body != "" {
		segments, err := parsePathSegments(body, path)
		if err != nil {
			return nil, err
		}
		out.Segments = segments
	}
	if hasProjection {
		for entry := range strings.SplitSeq(projection, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				return nil, fmt.Errorf("path %q: empty projection entry", path)
			}
			segments, err := parsePathSegments(entry, path)
			if err != nil {
				return nil, err
			}
			out.Projection = append(out.Projection, ProjectionEntry{Label: entry, Segments: segments})
		}
	}
	if len(out.Segments) == 0 && len(out.Projection) == 0 {
		return nil, fmt.Errorf("path %q: empty path", path)
	}

	return out, nil
}

func parsePathSegments(body, path string) ([]PathSegment, error) {
	var out []PathSegment
	for raw := range strings.SplitSeq(body, ".") {
		m := factPathSegment.FindStringSubmatch(raw)
		if m == nil {
			return nil, fmt.Errorf("path %q: bad segment %q (want name, name[*], or name[N])", path, raw)
		}
		seg := PathSegment{Name: m[1]}
		for _, op := range factPathListOp.FindAllStringSubmatch(m[2], -1) {
			if op[1] == "*" {
				seg.Indexes = append(seg.Indexes, WildcardIndex)
				continue
			}
			n, err := strconv.Atoi(op[1])
			if err != nil {
				return nil, fmt.Errorf("path %q: bad index in segment %q", path, raw)
			}
			seg.Indexes = append(seg.Indexes, n)
		}
		out = append(out, seg)
	}

	return out, nil
}

// CRDConditionsEnabled returns the effective value of EnableCRDConditions
// (default true).
func (c *Config) CRDConditionsEnabled() bool {
	return c.EnableCRDConditions == nil || *c.EnableCRDConditions
}

// AnnotationAllowed reports whether an annotation key may be served. The
// last-applied denylist is not configurable and wins over the allowlist:
// last-applied-style annotations embed whole object specs.
func (c *Config) AnnotationAllowed(key string) bool {
	if strings.Contains(key, "last-applied") {
		return false
	}

	return slices.Contains(c.AllowedAnnotations, key)
}

// ClusterKindAllowed reports whether a cluster-scoped kind may be served by
// the generic tools (matched case-insensitively).
func (c *Config) ClusterKindAllowed(kind string) bool {
	return slices.ContainsFunc(c.ClusterKinds, func(k string) bool {
		return strings.EqualFold(k, kind)
	})
}

// ViewFor returns the view matching an API group and kind, or nil.
func (c *Config) ViewFor(group, kind string) *View {
	for i := range c.Views {
		v := &c.Views[i]
		if strings.EqualFold(v.Group, group) && strings.EqualFold(v.Kind, kind) {
			return v
		}
	}

	return nil
}

// pruneViews drops malformed views and facts with unparseable paths, returning
// one warning per dropped item. Views only enrich resource_conditions, we warn
// and keep going if when there are invalid views. Choosing safe fields is on
// you; the scalar-only extractor is the structural guarantee that whole object
// bodies cannot leak.
func (c *Config) pruneViews() []string {
	var warnings []string
	views := c.Views[:0]
	for _, v := range c.Views {
		if v.Kind == "" {
			warnings = append(warnings, fmt.Sprintf("dropping view with group %q: kind is required", v.Group))
			continue
		}
		facts := v.Facts[:0]
		for _, f := range v.Facts {
			if f.Name == "" || f.Path == "" {
				warnings = append(warnings, fmt.Sprintf("view for kind %q: dropping fact %q: every fact needs a name and a path", v.Kind, f.Name))
				continue
			}
			if _, err := ParseFactPath(f.Path); err != nil {
				warnings = append(warnings, fmt.Sprintf("view for kind %q: dropping fact %q: %v", v.Kind, f.Name, err))
				continue
			}
			facts = append(facts, f)
		}
		v.Facts = facts
		if len(v.Facts) == 0 {
			warnings = append(warnings, fmt.Sprintf("dropping view for kind %q: no valid facts", v.Kind))
			continue
		}
		views = append(views, v)
	}
	c.Views = views

	return warnings
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

// Load parses args, reads the config file if given, and applies flags on top.
// It returns the effective config plus any warnings about invalid views.
func Load(args []string) (*Config, []string, error) {
	fs := flag.NewFlagSet("porthole", flag.ContinueOnError)
	var (
		configPath    = fs.String("config", "", "path to a YAML config file, or a directory (e.g. a mounted ConfigMap) whose files are top-level config keys")
		mode          = fs.String("mode", "stdio", "transport mode: http or stdio")
		listenAddr    = fs.String("listen-addr", ":8080", "bind address in http mode")
		kubeconfig    = fs.String("kubeconfig", "", "path to kubeconfig (default: in-cluster config, then client-go loading rules)")
		namespaces    = fs.String("namespaces", "", "comma-separated namespace allowlist (empty: whatever RBAC allows)")
		crdConditions = fs.Bool("enable-crd-conditions", true, "enable the resource_conditions tool")
		crdStatus     = fs.Bool("enable-crd-status", false, "enable the resource_status tool (full .status of arbitrary kinds)")
		logs          = fs.Bool("enable-logs", false, "enable the pod_logs tool")
		secretMeta    = fs.Bool("enable-secret-metadata", false, "enable the secret_metadata tool (key names, never values; needs Secret read RBAC)")
		configMapMeta = fs.Bool("enable-configmap-metadata", false, "enable the configmap_metadata tool (key names, never values; needs ConfigMap read RBAC)")
		annotations   = fs.String("allowed-annotations", "", "comma-separated exact annotation keys to include in responses (last-applied is always denied)")
		clusterKinds  = fs.String("cluster-kinds", "", "comma-separated cluster-scoped kinds the generic tools may serve, e.g. ClusterIssuer")
		maxLogBytes   = fs.Int64("max-log-bytes", DefaultMaxLogBytes, "server-side ceiling on log response bytes")
		maxEvents     = fs.Int("max-events", DefaultMaxEvents, "server-side ceiling on events returned per call")
		authTokenFile = fs.String("auth-token-file", "", "file with a static bearer token required in http mode")
	)
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}

	cfg := defaults()
	if *configPath != "" {
		data, err := readConfigPath(*configPath)
		if err != nil {
			return nil, nil, err
		}
		if err := yaml.UnmarshalStrict(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("parsing config %s: %w", *configPath, err)
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
	if set["enable-secret-metadata"] {
		cfg.EnableSecretMetadata = *secretMeta
	}
	if set["enable-configmap-metadata"] {
		cfg.EnableConfigMapMetadata = *configMapMeta
	}
	if set["allowed-annotations"] {
		cfg.AllowedAnnotations = splitNonEmpty(*annotations)
	}
	if set["cluster-kinds"] {
		cfg.ClusterKinds = splitNonEmpty(*clusterKinds)
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
		return nil, nil, fmt.Errorf("invalid mode %q: must be http or stdio", cfg.Mode)
	}
	if cfg.MaxLogBytes <= 0 {
		cfg.MaxLogBytes = DefaultMaxLogBytes
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = DefaultMaxEvents
	}
	cfg.Views = mergeViews(cfg.Views)
	warnings := cfg.pruneViews()

	return cfg, warnings, nil
}

// mergeViews combines views declaring the same group and kind by merging
// their facts, in declaration order. This makes duplicates well-defined:
// an overlay's views-<suffix> file can add facts to a kind the base
// already covers, and a fact reusing an earlier name overrides it
// (last wins) instead of emitting twice.
func mergeViews(views []View) []View {
	var out []View
	index := map[string]int{}
	for _, v := range views {
		key := strings.ToLower(v.Group) + "/" + strings.ToLower(v.Kind)
		if i, ok := index[key]; ok {
			out[i].Facts = mergeFacts(out[i].Facts, v.Facts)
			continue
		}
		index[key] = len(out)
		v.Facts = mergeFacts(nil, v.Facts)
		out = append(out, v)
	}

	return out
}

// mergeFacts appends facts with last-wins-by-name semantics.
func mergeFacts(existing, additions []ViewFact) []ViewFact {
	index := map[string]int{}
	for i, f := range existing {
		index[f.Name] = i
	}
	out := existing
	for _, f := range additions {
		if i, ok := index[f.Name]; ok {
			out[i] = f
			continue
		}
		index[f.Name] = len(out)
		out = append(out, f)
	}

	return out
}

// readConfigPath returns the config as one YAML document. A regular file is
// returned as-is. A directory - the shape of a mounted ConfigMap, where each
// data key becomes a file - is composed into a document with one top-level
// key per file, the file's content parsed as that key's YAML value. This
// keeps the ConfigMap mergeable per-key with kustomize instead of being one
// opaque config.yaml blob.
func readConfigPath(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		return data, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("reading config directory: %w", err)
	}
	merged := map[string]any{}
	// Files named views or views-<suffix> concatenate (in filename order)
	// into the views list, so kustomize overlays can append views in their
	// own key instead of overriding the whole list.
	var views []any
	for _, entry := range entries {
		name := entry.Name()
		// Skip the mount machinery in ConfigMap volumes (..data,
		// ..2026_01_02* dirs) and other hidden files.
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(path, name)
		// Stat (not entry.Type) so ConfigMap key symlinks resolve. A stat
		// failure is an error, not a skip: silently dropping a config key
		// could fail open (e.g. losing the namespaces allowlist). Config
		// loads once at startup, so a transient mount race just means a
		// restart-and-retry; revisit this if config ever hot-reloads.
		st, err := os.Stat(full)
		if err != nil {
			return nil, fmt.Errorf("reading config key %s: %w", name, err)
		}
		if st.IsDir() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("reading config key %s: %w", name, err)
		}
		var value any
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("parsing config key %s: %w", name, err)
		}
		if name == "views" || strings.HasPrefix(name, "views-") {
			list, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("config key %s: must be a list of views", name)
			}
			views = append(views, list...)
			continue
		}
		merged[name] = value
	}
	if len(views) > 0 {
		merged["views"] = views
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("composing config from directory: %w", err)
	}

	return data, nil
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
