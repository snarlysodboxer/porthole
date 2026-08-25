// Package tools implements porthole's MCP tools.
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/snarlysodboxer/porthole/internal/config"
	"github.com/snarlysodboxer/porthole/internal/kube"
)

const requestTimeout = 30 * time.Second

// Toolset holds the clients and config shared by all tool handlers.
type Toolset struct {
	cfg     *config.Config
	clients *kube.Clients
	now     func() time.Time
}

// New builds a Toolset.
func New(cfg *config.Config, clients *kube.Clients) *Toolset {
	return &Toolset{cfg: cfg, clients: clients, now: time.Now}
}

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true}
}

// Register adds all enabled tools to the MCP server.
func (t *Toolset) Register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_namespaces",
		Description: "List the namespaces this server is scoped to and which optional features are enabled. Call this first to learn the terrain.",
		Annotations: readOnly(),
	}, t.ListNamespaces)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_workloads",
		Description: "List Deployments, StatefulSets, and DaemonSets with replica counts and conditions. Omit namespace to scan every allowed namespace — the one-call 'what's unhealthy anywhere?' entry point.",
		Annotations: readOnly(),
	}, t.ListWorkloads)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "workload_status",
		Description: "Deep rollout status for one Deployment, StatefulSet, or DaemonSet: conditions with messages, observedGeneration vs generation, rollout revision, and a summary of its pods. Answers 'why isn't this rolling out?'.",
		Annotations: readOnly(),
	}, t.WorkloadStatus)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_pods",
		Description: "List pods with phase, readiness, restart counts, node, and per-container state reasons (CrashLoopBackOff, ImagePullBackOff, OOMKilled) with exit codes. Omit namespace to scan every allowed namespace.",
		Annotations: readOnly(),
	}, t.ListPods)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pod_status",
		Description: "Safe 'kubectl describe pod': container states including last termination (exit code, reason, message), pod conditions, QoS class, images, node, env variable names (never values), volume names and types (never contents), resources, ports, and the pod's events.",
		Annotations: readOnly(),
	}, t.PodStatus)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "events",
		Description: "List Kubernetes events, optionally filtered to one object (kind + name) or a time window. Answers 'what happened?'. Note: the cluster retains events for about an hour by default, so longer 'since' windows are best-effort.",
		Annotations: readOnly(),
	}, t.Events)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "service_endpoints",
		Description: "Show a Service's type, selector, and ports, plus ready/not-ready endpoint counts from EndpointSlices. Catches 'selector matches no pods'. Answers 'why is nothing answering?'.",
		Annotations: readOnly(),
	}, t.ServiceEndpoints)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pvc_status",
		Description: "List PersistentVolumeClaims in a namespace: phase, requested size, storage class, access modes, plus each bound PersistentVolume's detail (phase, reclaim policy, source driver, volume handle) and any unbound volumes still referencing the namespace. Answers 'why is the pod stuck Pending?'.",
		Annotations: readOnly(),
	}, t.PVCStatus)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_status",
		Description: "List Jobs (active/succeeded/failed, timings, conditions) and CronJobs (schedule, suspend, last schedule/success times) in a namespace. Answers 'did the job run?'.",
		Annotations: readOnly(),
	}, t.JobStatus)

	if t.cfg.CRDConditionsEnabled() {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "resource_conditions",
			Description: "Get .status.conditions and events for any resource kind, including custom resources (Certificates, Argo CD Applications, Gateways, ...). If a bare kind is ambiguous across API groups, pass the group parameter.",
			Annotations: readOnly(),
		}, t.ResourceConditions)
	}

	if t.cfg.EnableCRDStatus {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "resource_status",
			Description: "Get the full .status (never spec) and events of any resource kind, including custom resources. Use when resource_conditions isn't enough.",
			Annotations: readOnly(),
		}, t.ResourceStatus)
	}

	if t.cfg.EnableLogs {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "pod_logs",
			Description: "Fetch container logs. Set previous=true for the last output of a crashed container (crash-loop diagnosis). grep applies an RE2 regex filter server-side. Responses are size-capped; truncation is marked explicitly.",
			Annotations: readOnly(),
		}, t.PodLogs)
	}
}

// Instructions is the MCP server instructions string: it advertises scope
// and enabled features so clients know the terrain without probing.
func Instructions(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("porthole: a curated, read-only Kubernetes troubleshooting window. ")
	b.WriteString("Responses are hand-shaped from live .status fields, events, and safe metadata; ")
	b.WriteString("specs, env values, annotations, and Secret/ConfigMap data are structurally omitted. ")
	if len(cfg.Namespaces) > 0 {
		fmt.Fprintf(&b, "Namespace allowlist: %s. ", strings.Join(cfg.Namespaces, ", "))
	} else {
		b.WriteString("No namespace allowlist is configured; RBAC bounds what is visible. ")
	}
	features := []string{}
	if cfg.CRDConditionsEnabled() {
		features = append(features, "resource_conditions")
	}
	if cfg.EnableCRDStatus {
		features = append(features, "resource_status")
	}
	if cfg.EnableLogs {
		features = append(features, "pod_logs")
	}
	if len(features) > 0 {
		fmt.Fprintf(&b, "Optional tools enabled: %s.", strings.Join(features, ", "))
	} else {
		b.WriteString("No optional tools are enabled.")
	}

	return b.String()
}

// namespacesFor expands a tool's namespace parameter: an explicit namespace
// is checked against the allowlist; an empty one fans out over the
// allowlist, or falls back to a cluster-scoped query ("") when no allowlist
// is configured (which works only if RBAC grants cluster-wide list).
func (t *Toolset) namespacesFor(ns string) ([]string, error) {
	if ns != "" {
		if err := t.checkNamespace(ns); err != nil {
			return nil, err
		}
		return []string{ns}, nil
	}
	if len(t.cfg.Namespaces) > 0 {
		return t.cfg.Namespaces, nil
	}

	return []string{""}, nil
}

func (t *Toolset) checkNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	if !t.cfg.NamespaceAllowed(ns) {
		return fmt.Errorf("namespace %q is not in this server's allowlist (allowed: %s)",
			ns, strings.Join(t.cfg.Namespaces, ", "))
	}

	return nil
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, requestTimeout)
}
