package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ListNamespacesInput has no parameters.
type ListNamespacesInput struct{}

// ListNamespaces implements the list_namespaces tool.
func (t *Toolset) ListNamespaces(ctx context.Context, req *mcp.CallToolRequest, in ListNamespacesInput) (*mcp.CallToolResult, ListNamespacesOutput, error) {
	out := ListNamespacesOutput{
		Namespaces: t.cfg.Namespaces,
		Features: FeatureFlags{
			ResourceConditions: t.cfg.CRDConditionsEnabled(),
			ResourceStatus:     t.cfg.EnableCRDStatus,
			PodLogs:            t.cfg.EnableLogs,
			SecretMetadata:     t.cfg.EnableSecretMetadata,
			ConfigMapMetadata:  t.cfg.EnableConfigMapMetadata,
		},
	}
	if len(out.Namespaces) == 0 {
		out.Namespaces = []string{}
		// With no allowlist this tool would be an unhelpful first call, so
		// fall back to listing real Namespaces (needs a cluster-scoped
		// grant; degrades to the note without one).
		ctx, cancel := withTimeout(ctx)
		defer cancel()
		list, err := t.clients.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Note = "No namespace allowlist is configured on this server, and Namespaces are not listable with its RBAC; namespaced RoleBindings bound to the server's ServiceAccount determine what is visible."
			return nil, out, nil
		}
		for i := range list.Items {
			ns := &list.Items[i]
			out.ClusterNamespaces = append(out.ClusterNamespaces, NamespaceInfo{
				Name:      ns.Name,
				Phase:     string(ns.Status.Phase),
				Labels:    ns.Labels,
				Age:       age(ns.CreationTimestamp.Time, t.now()),
				CreatedAt: fmtTime(ns.CreationTimestamp),
			})
		}
		out.Note = "No namespace allowlist is configured on this server; RBAC bounds what is visible. clusterNamespaces lists the cluster's actual Namespaces."
	}

	return nil, out, nil
}
