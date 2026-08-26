package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// TopPodsInput selects the namespace scope.
type TopPodsInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to report; omit to scan every allowed namespace"`
}

// TopPods implements the top_pods tool: the "kubectl top pods" equivalent
// from metrics.k8s.io. Purely numeric - no leak surface beyond names.
func (t *Toolset) TopPods(ctx context.Context, req *mcp.CallToolRequest, in TopPodsInput) (*mcp.CallToolResult, TopPodsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	namespaces, err := t.namespacesFor(in.Namespace)
	if err != nil {
		return nil, TopPodsOutput{}, err
	}

	out := TopPodsOutput{Pods: []PodUsage{}}
	for _, ns := range namespaces {
		list, err := t.clients.Metrics.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("pod metrics in %q (is metrics.k8s.io served, e.g. by metrics-server?): %v", ns, err))
			continue
		}
		// Join usage with the pod specs' requests/limits so they can be
		// compared in one call; degrade to usage-only without pod read.
		// Keys are namespace-qualified because ns may be "" (no allowlist),
		// where same-named pods in different namespaces would collide.
		// Init containers are included: restartable sidecars report usage.
		resources := map[string]corev1.ResourceRequirements{}
		pods, err := t.clients.Typed.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("pods in %q not readable (usage shown without requests/limits): %v", ns, err))
		} else {
			for i := range pods.Items {
				pod := &pods.Items[i]
				for _, c := range pod.Spec.Containers {
					resources[pod.Namespace+"/"+pod.Name+"/"+c.Name] = c.Resources
				}
				for _, c := range pod.Spec.InitContainers {
					resources[pod.Namespace+"/"+pod.Name+"/"+c.Name] = c.Resources
				}
			}
		}
		for i := range list.Items {
			out.Pods = append(out.Pods, shapePodMetrics(&list.Items[i], resources))
		}
	}

	return nil, out, nil
}

func shapePodMetrics(pm *metricsv1beta1.PodMetrics, resources map[string]corev1.ResourceRequirements) PodUsage {
	u := PodUsage{
		Name:      pm.Name,
		Namespace: pm.Namespace,
		Window:    pm.Window.Duration.String(),
		Timestamp: fmtTime(pm.Timestamp),
	}
	var cpuTotal, memTotal int64
	for _, c := range pm.Containers {
		cpu := c.Usage.Cpu().MilliValue()
		mem := c.Usage.Memory().Value()
		cpuTotal += cpu
		memTotal += mem
		usage := ContainerUsage{
			Name:        c.Name,
			CPU:         fmt.Sprintf("%dm", cpu),
			Memory:      humanBytes(mem),
			MemoryBytes: mem,
		}
		if r, ok := resources[pm.Namespace+"/"+pm.Name+"/"+c.Name]; ok {
			usage.Requests = shapeResourceList(r.Requests)
			usage.Limits = shapeResourceList(r.Limits)
		}
		u.Containers = append(u.Containers, usage)
	}
	u.CPU = fmt.Sprintf("%dm", cpuTotal)
	u.Memory = humanBytes(memTotal)
	u.MemoryBytes = memTotal

	return u
}

// NodeStatusInput has no parameters.
type NodeStatusInput struct{}

// NodeStatus implements the node_status tool: node conditions, taints, and
// allocatable vs capacity from Node objects, combined with point-in-time
// usage from metrics.k8s.io. Both reads are cluster-scoped and degrade
// independently, so partial grants still produce partial answers.
func (t *Toolset) NodeStatus(ctx context.Context, req *mcp.CallToolRequest, in NodeStatusInput) (*mcp.CallToolResult, NodeStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	out := NodeStatusOutput{Nodes: []NodeInfo{}}

	infoByName := map[string]*NodeInfo{}
	var order []string
	nodes, err := t.clients.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("nodes not readable: %v", err))
	} else {
		for i := range nodes.Items {
			info := shapeNode(&nodes.Items[i])
			infoByName[info.Name] = info
			order = append(order, info.Name)
		}
	}

	metrics, err := t.clients.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("node metrics not readable (is metrics.k8s.io served, e.g. by metrics-server?): %v", err))
	} else {
		var allocByName map[string]corev1.ResourceList
		if nodes != nil {
			allocByName = make(map[string]corev1.ResourceList, len(nodes.Items))
			for i := range nodes.Items {
				allocByName[nodes.Items[i].Name] = nodes.Items[i].Status.Allocatable
			}
		}
		for i := range metrics.Items {
			nm := &metrics.Items[i]
			info, ok := infoByName[nm.Name]
			if !ok {
				// Metrics-only degradation (Node objects unreadable):
				// readiness genuinely is unknown here.
				info = &NodeInfo{Name: nm.Name, Ready: "Unknown"}
				infoByName[nm.Name] = info
				order = append(order, nm.Name)
			}
			attachNodeUsage(info, nm, allocByName[nm.Name])
		}
	}

	for _, name := range order {
		out.Nodes = append(out.Nodes, *infoByName[name])
	}

	return nil, out, nil
}

func shapeNode(node *corev1.Node) *NodeInfo {
	info := &NodeInfo{
		Name:           node.Name,
		Ready:          "Unknown",
		Unschedulable:  node.Spec.Unschedulable,
		KubeletVersion: node.Status.NodeInfo.KubeletVersion,
		Allocatable:    shapeResourceList(node.Status.Allocatable),
		Capacity:       shapeResourceList(node.Status.Capacity),
	}
	for _, taint := range node.Spec.Taints {
		s := taint.Key
		if taint.Value != "" {
			s += "=" + taint.Value
		}
		info.Taints = append(info.Taints, s+":"+string(taint.Effect))
	}
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			info.Ready = string(c.Status)
		}
		info.Conditions = append(info.Conditions, Condition{
			Type:               string(c.Type),
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: fmtTime(c.LastTransitionTime),
		})
	}

	return info
}

func attachNodeUsage(info *NodeInfo, nm *metricsv1beta1.NodeMetrics, allocatable corev1.ResourceList) {
	cpu := nm.Usage.Cpu().MilliValue()
	mem := nm.Usage.Memory().Value()
	info.UsageCPU = fmt.Sprintf("%dm", cpu)
	info.UsageMemory = humanBytes(mem)
	if allocCPU := allocatable.Cpu().MilliValue(); allocCPU > 0 {
		info.UsageCPUPercent = int(cpu * 100 / allocCPU)
	}
	if allocMem := allocatable.Memory().Value(); allocMem > 0 {
		info.UsageMemoryPercent = int(mem * 100 / allocMem)
	}
}
