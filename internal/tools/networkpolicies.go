package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkPoliciesInput selects a namespace.
type NetworkPoliciesInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose NetworkPolicies to list"`
}

// NetworkPolicies implements the network_policies tool. Policies are
// spec-only (selectors, ports, peer rules) and structurally safe.
func (t *Toolset) NetworkPolicies(ctx context.Context, req *mcp.CallToolRequest, in NetworkPoliciesInput) (*mcp.CallToolResult, NetworkPoliciesOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, NetworkPoliciesOutput{}, err
	}
	list, err := t.clients.Typed.NetworkingV1().NetworkPolicies(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, NetworkPoliciesOutput{}, err
	}

	out := NetworkPoliciesOutput{Namespace: in.Namespace, Policies: []NetworkPolicyInfo{}}
	for i := range list.Items {
		out.Policies = append(out.Policies, shapeNetworkPolicy(&list.Items[i]))
	}

	return nil, out, nil
}

func shapeNetworkPolicy(np *networkingv1.NetworkPolicy) NetworkPolicyInfo {
	info := NetworkPolicyInfo{
		Name:        np.Name,
		PodSelector: formatSelector(&np.Spec.PodSelector),
	}
	for _, pt := range np.Spec.PolicyTypes {
		info.PolicyTypes = append(info.PolicyTypes, string(pt))
	}
	for _, r := range np.Spec.Ingress {
		info.Ingress = append(info.Ingress, NetworkPolicyRule{
			Peers: shapePeers(r.From),
			Ports: shapeNetPorts(r.Ports),
		})
	}
	for _, r := range np.Spec.Egress {
		info.Egress = append(info.Egress, NetworkPolicyRule{
			Peers: shapePeers(r.To),
			Ports: shapeNetPorts(r.Ports),
		})
	}

	return info
}

func shapePeers(peers []networkingv1.NetworkPolicyPeer) []string {
	var out []string
	for _, p := range peers {
		var parts []string
		if p.PodSelector != nil {
			parts = append(parts, fmt.Sprintf("pods(%s)", formatSelector(p.PodSelector)))
		}
		if p.NamespaceSelector != nil {
			parts = append(parts, fmt.Sprintf("namespaces(%s)", formatSelector(p.NamespaceSelector)))
		}
		if p.IPBlock != nil {
			s := "ipBlock(" + p.IPBlock.CIDR
			if len(p.IPBlock.Except) > 0 {
				s += " except " + strings.Join(p.IPBlock.Except, ", ")
			}
			parts = append(parts, s+")")
		}
		// A peer with no selector or ipBlock is invalid API-side; if one
		// ever appears, skip it rather than emit a misleading blank entry.
		if len(parts) == 0 {
			continue
		}
		out = append(out, strings.Join(parts, " in "))
	}

	return out
}

func shapeNetPorts(ports []networkingv1.NetworkPolicyPort) []string {
	var out []string
	for _, p := range ports {
		proto := "TCP" // the API's default when protocol is unset
		if p.Protocol != nil {
			proto = string(*p.Protocol)
		}
		port := ""
		if p.Port != nil {
			port = p.Port.String()
		}
		if p.EndPort != nil {
			port = fmt.Sprintf("%s-%d", port, *p.EndPort)
		}
		if port == "" {
			port = "all"
		}
		out = append(out, port+"/"+proto)
	}

	return out
}
