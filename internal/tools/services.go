package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceEndpointsInput identifies one Service.
type ServiceEndpointsInput struct {
	Namespace string `json:"namespace" jsonschema:"the service's namespace"`
	Name      string `json:"name" jsonschema:"the service's name"`
}

// ServiceEndpoints implements the service_endpoints tool.
func (t *Toolset) ServiceEndpoints(ctx context.Context, req *mcp.CallToolRequest, in ServiceEndpointsInput) (*mcp.CallToolResult, ServiceEndpointsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, ServiceEndpointsOutput{}, err
	}
	svc, err := t.clients.Typed.CoreV1().Services(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, ServiceEndpointsOutput{}, err
	}

	out := ServiceEndpointsOutput{
		Name:      svc.Name,
		Namespace: svc.Namespace,
		Type:      string(svc.Spec.Type),
		Selector:  svc.Spec.Selector,
	}
	for _, p := range svc.Spec.Ports {
		out.Ports = append(out.Ports, ServicePortInfo{
			Name:       p.Name,
			Port:       p.Port,
			TargetPort: p.TargetPort.String(),
			NodePort:   p.NodePort,
			Protocol:   string(p.Protocol),
		})
	}

	slices, err := t.clients.Typed.DiscoveryV1().EndpointSlices(in.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + in.Name,
	})
	if err != nil {
		return nil, out, err
	}
	for _, slice := range slices.Items {
		for _, ep := range slice.Endpoints {
			// A nil Ready condition means unknown; the API instructs
			// consumers to treat it as ready.
			if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
				out.ReadyEndpoints++
			} else {
				out.NotReadyEndpoints++
			}
		}
	}

	return nil, out, nil
}
