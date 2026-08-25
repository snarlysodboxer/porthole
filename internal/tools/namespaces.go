package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		},
	}
	if len(out.Namespaces) == 0 {
		out.Namespaces = []string{}
		out.Note = "No namespace allowlist is configured on this server; RBAC bounds what is visible."
	}

	return nil, out, nil
}
