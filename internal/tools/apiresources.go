package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListAPIResourcesInput optionally filters to one API group.
type ListAPIResourcesInput struct {
	Group string `json:"group,omitempty" jsonschema:"filter to one API group, e.g. cert-manager.io; pass 'core' for the core group; omit for all groups"`
}

// ListAPIResources implements the list_api_resources tool. Discovery
// endpoints are readable by any authenticated ServiceAccount, so this needs
// no RBAC beyond authentication.
func (t *Toolset) ListAPIResources(ctx context.Context, req *mcp.CallToolRequest, in ListAPIResourcesInput) (*mcp.CallToolResult, ListAPIResourcesOutput, error) {
	group := in.Group
	if strings.EqualFold(group, "core") {
		group = ""
	}
	filtered := in.Group != ""

	resources, err := t.clients.Mapper.ListResources("")
	if err != nil {
		return nil, ListAPIResourcesOutput{}, err
	}
	out := ListAPIResourcesOutput{Resources: []APIResourceInfo{}}
	for _, r := range resources {
		if filtered && !strings.EqualFold(r.Group, group) {
			continue
		}
		out.Resources = append(out.Resources, APIResourceInfo{
			Group:      r.Group,
			Version:    r.Version,
			Kind:       r.Kind,
			Resource:   r.Resource,
			Namespaced: r.Namespaced,
		})
	}

	groups, err := t.clients.Mapper.ListGroups()
	if err == nil {
		for _, g := range groups {
			if filtered && !strings.EqualFold(g.Group, group) {
				continue
			}
			out.Groups = append(out.Groups, APIGroupInfo{
				Group:            g.Group,
				PreferredVersion: g.PreferredVersion,
				Versions:         g.Versions,
			})
		}
	}

	return nil, out, nil
}
