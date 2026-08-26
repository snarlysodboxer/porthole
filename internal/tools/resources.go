package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/snarlysodboxer/porthole/internal/config"
)

// ResourceInput identifies one resource of any kind.
type ResourceInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the resource's namespace; omit only for allowlisted cluster-scoped kinds"`
	Kind      string `json:"kind" jsonschema:"the resource kind, e.g. Certificate, Application, Gateway (case-insensitive)"`
	Name      string `json:"name" jsonschema:"the resource's name"`
	Group     string `json:"group,omitempty" jsonschema:"API group to disambiguate kinds that exist in multiple groups, e.g. gateway.networking.k8s.io"`
}

// ResourceConditions implements the resource_conditions tool: the generic
// escape hatch returning .status.conditions plus events for any kind.
func (t *Toolset) ResourceConditions(ctx context.Context, req *mcp.CallToolRequest, in ResourceInput) (*mcp.CallToolResult, ResourceConditionsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	obj, res, err := t.getResource(ctx, in)
	if err != nil {
		return nil, ResourceConditionsOutput{}, err
	}
	out := ResourceConditionsOutput{
		APIVersion:  obj.GetAPIVersion(),
		Kind:        obj.GetKind(),
		Name:        obj.GetName(),
		Namespace:   obj.GetNamespace(),
		Annotations: t.shapeAnnotations(obj.GetAnnotations()),
		Conditions:  extractConditions(obj),
	}
	if view := t.cfg.ViewFor(res.Group, obj.GetKind()); view != nil {
		out.Details = extractFacts(view, obj)
	}
	events, truncated := t.fetchEventsForResource(ctx, res, in)
	out.Events = events
	out.TruncatedEvents = truncated

	return nil, out, nil
}

// ResourceStatus implements the flag-gated resource_status tool. The output
// is built from scratch: only .status and allowlisted metadata are copied
// out of the fetched object, so spec, annotations, and managedFields are
// omitted by construction.
func (t *Toolset) ResourceStatus(ctx context.Context, req *mcp.CallToolRequest, in ResourceInput) (*mcp.CallToolResult, ResourceStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	obj, res, err := t.getResource(ctx, in)
	if err != nil {
		return nil, ResourceStatusOutput{}, err
	}
	out := shapeResourceStatus(obj)
	out.Annotations = t.shapeAnnotations(obj.GetAnnotations())
	events, truncated := t.fetchEventsForResource(ctx, res, in)
	out.Events = events
	out.TruncatedEvents = truncated

	return nil, out, nil
}

// fetchEventsForResource looks up a resource's events, best effort. For a
// namespaced resource that is its own namespace. Events about a
// cluster-scoped resource land in whatever namespace its controller chose,
// so the lookup fans out over the allowlist namespaces (staying inside the
// allowlist boundary) - or queries cluster-wide when no allowlist is
// configured, which works only with cluster-wide event RBAC.
func (t *Toolset) fetchEventsForResource(ctx context.Context, res *resolution, in ResourceInput) ([]EventInfo, int) {
	if res.Namespaced {
		events, truncated, err := t.fetchEvents(ctx, in.Namespace, res.Kind, in.Name, 0)
		if err != nil {
			return nil, 0
		}
		return events, truncated
	}

	namespaces, err := t.namespacesFor("")
	if err != nil {
		return nil, 0
	}
	// Events match on kind + name only (shaped events don't retain the
	// group), so a same-named object of a same-named kind in another group
	// could contribute stray events - cosmetic noise at worst.
	var all []shapedEvent
	for _, ns := range namespaces {
		events, err := t.listEvents(ctx, ns, res.Kind, in.Name, 0)
		if err != nil {
			continue
		}
		all = append(all, events...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].lastSeen.Before(all[j].lastSeen) })
	truncated := 0
	if len(all) > t.cfg.MaxEvents {
		truncated = len(all) - t.cfg.MaxEvents
		all = all[len(all)-t.cfg.MaxEvents:]
	}
	var infos []EventInfo
	for _, e := range all {
		infos = append(infos, e.info)
	}

	return infos, truncated
}

// shapeResourceStatus copies only .status and allowlisted metadata into a
// fresh output. This is the strip step the leak tests target.
func shapeResourceStatus(obj *unstructured.Unstructured) ResourceStatusOutput {
	out := ResourceStatusOutput{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		Labels:     obj.GetLabels(),
		CreatedAt:  fmtTime(obj.GetCreationTimestamp()),
	}
	if status, found, err := unstructured.NestedMap(obj.Object, "status"); err == nil && found {
		if redacted, ok := redactStatus(status).(map[string]any); ok {
			out.Status = redacted
		}
	}

	return out
}

// redactedStatusKey matches status field names that smell like secret
// material. Status is supposed to be observation, not secrets, but some
// operators stash things like password hashes there - cheap insurance for
// the one unstructured passthrough.
var redactedStatusKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|key|hash)`)

// redactStatus replaces values under suspicious keys with "(redacted)".
// Numbers and booleans are kept (counts like keyCount aren't secrets);
// strings, lists, and objects under a matching key are dropped wholesale.
func redactStatus(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			if redactedStatusKey.MatchString(k) {
				switch value.(type) {
				case bool, float64, int64, nil:
					out[k] = value
				default:
					out[k] = "(redacted)"
				}
				continue
			}
			out[k] = redactStatus(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = redactStatus(item)
		}
		return out
	default:
		return v
	}
}

func (t *Toolset) getResource(ctx context.Context, in ResourceInput) (*unstructured.Unstructured, *resolution, error) {
	res, err := t.clients.Mapper.Resolve(in.Kind, in.Group)
	if err != nil {
		return nil, nil, err
	}

	if !res.Namespaced {
		// Cluster-scoped kinds are served only from the operator's
		// explicit allowlist (reading them needs a cluster-scoped grant).
		if !t.cfg.ClusterKindAllowed(res.Kind) {
			return nil, nil, fmt.Errorf("kind %q is cluster-scoped and not in this server's clusterKinds allowlist", res.Kind)
		}
		obj, err := t.clients.Dynamic.Resource(res.GVR).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
		return obj, &resolution{Kind: res.Kind, Group: res.GVR.Group, Namespaced: false}, nil
	}

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, nil, err
	}
	obj, err := t.clients.Dynamic.Resource(res.GVR).Namespace(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	return obj, &resolution{Kind: res.Kind, Group: res.GVR.Group, Namespaced: true}, nil
}

// resolution mirrors the fields of kube.Resolution used here, avoiding a
// wider dependency in signatures.
type resolution struct {
	Kind       string
	Group      string
	Namespaced bool
}

// ListResourcesInput selects a kind, optionally scoped to one namespace.
type ListResourcesInput struct {
	Kind      string `json:"kind" jsonschema:"the resource kind to list, e.g. Application, Certificate (case-insensitive)"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to list; omit to scan every allowed namespace (ignored for cluster-scoped kinds)"`
	Group     string `json:"group,omitempty" jsonschema:"API group to disambiguate kinds that exist in multiple groups"`
}

// ListResources implements the list_resources tool: enumerate resources of
// any kind with a compact condition summary and view facts per item - the
// discovery step before resource_conditions' exact-name get.
func (t *Toolset) ListResources(ctx context.Context, req *mcp.CallToolRequest, in ListResourcesInput) (*mcp.CallToolResult, ListResourcesOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	res, err := t.clients.Mapper.Resolve(in.Kind, in.Group)
	if err != nil {
		return nil, ListResourcesOutput{}, err
	}
	out := ListResourcesOutput{
		APIVersion: res.GVR.GroupVersion().String(),
		Kind:       res.Kind,
		Resources:  []ResourceSummary{},
	}
	view := t.cfg.ViewFor(res.GVR.Group, res.Kind)

	if !res.Namespaced {
		if !t.cfg.ClusterKindAllowed(res.Kind) {
			return nil, out, fmt.Errorf("kind %q is cluster-scoped and not in this server's clusterKinds allowlist", res.Kind)
		}
		list, err := t.clients.Dynamic.Resource(res.GVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, out, err
		}
		for i := range list.Items {
			out.Resources = append(out.Resources, t.summarizeResource(&list.Items[i], view))
		}
		return nil, out, nil
	}

	namespaces, err := t.namespacesFor(in.Namespace)
	if err != nil {
		return nil, out, err
	}
	for _, ns := range namespaces {
		list, err := t.clients.Dynamic.Resource(res.GVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s in %q: %v", res.GVR.Resource, ns, err))
			continue
		}
		for i := range list.Items {
			out.Resources = append(out.Resources, t.summarizeResource(&list.Items[i], view))
		}
	}

	return nil, out, nil
}

func (t *Toolset) summarizeResource(obj *unstructured.Unstructured, view *config.View) ResourceSummary {
	summary := ResourceSummary{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Age:       age(obj.GetCreationTimestamp().Time, t.now()),
		CreatedAt: fmtTime(obj.GetCreationTimestamp()),
		Status:    summarizeConditions(extractConditions(obj)),
	}
	if view != nil {
		summary.Details = extractFacts(view, obj)
	}

	return summary
}

// summarizeConditions renders a one-line health hint: the Ready condition
// when present, else the first non-True condition (likely the problem),
// else the first condition.
func summarizeConditions(conditions []Condition) string {
	render := func(c Condition) string {
		s := c.Type + "=" + c.Status
		if c.Status != "True" && c.Reason != "" {
			s += " (" + c.Reason + ")"
		}
		return s
	}
	for _, c := range conditions {
		if c.Type == "Ready" {
			return render(c)
		}
	}
	for _, c := range conditions {
		if c.Status != "True" {
			return render(c)
		}
	}
	if len(conditions) > 0 {
		return render(conditions[0])
	}

	return ""
}

// extractConditions pulls .status.conditions out of an unstructured object
// into typed Conditions; unknown fields are dropped.
func extractConditions(obj *unstructured.Unstructured) []Condition {
	items, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}
	var out []Condition
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		str := func(key string) string {
			s, _ := m[key].(string)
			return s
		}
		out = append(out, Condition{
			Type:               str("type"),
			Status:             str("status"),
			Reason:             str("reason"),
			Message:            str("message"),
			LastTransitionTime: str("lastTransitionTime"),
		})
	}

	return out
}
