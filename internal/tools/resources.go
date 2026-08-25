package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ResourceInput identifies one namespaced resource of any kind.
type ResourceInput struct {
	Namespace string `json:"namespace" jsonschema:"the resource's namespace"`
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
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		Conditions: extractConditions(obj),
	}
	events, truncated, err := t.fetchEvents(ctx, in.Namespace, res.Kind, in.Name, 0)
	if err == nil {
		out.Events = events
		out.TruncatedEvents = truncated
	}

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
	events, truncated, err := t.fetchEvents(ctx, in.Namespace, res.Kind, in.Name, 0)
	if err == nil {
		out.Events = events
		out.TruncatedEvents = truncated
	}

	return nil, out, nil
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
		out.Status = status
	}

	return out
}

func (t *Toolset) getResource(ctx context.Context, in ResourceInput) (*unstructured.Unstructured, *resolution, error) {
	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, nil, err
	}
	res, err := t.clients.Mapper.Resolve(in.Kind, in.Group)
	if err != nil {
		return nil, nil, err
	}
	if !res.Namespaced {
		return nil, nil, fmt.Errorf("kind %q is cluster-scoped; this server only serves namespaced resources", res.Kind)
	}
	obj, err := t.clients.Dynamic.Resource(res.GVR).Namespace(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	return obj, &resolution{Kind: res.Kind}, nil
}

// resolution mirrors the fields of kube.Resolution used here, avoiding a
// wider dependency in signatures.
type resolution struct {
	Kind string
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
