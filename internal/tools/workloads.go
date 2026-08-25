package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// revisionAnnotation is the single allowlisted annotation: a
// controller-written integer identifying the current rollout revision.
const revisionAnnotation = "deployment.kubernetes.io/revision"

// ListWorkloadsInput selects the namespace scope.
type ListWorkloadsInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to list; omit to scan every allowed namespace"`
}

// ListWorkloads implements the list_workloads tool.
func (t *Toolset) ListWorkloads(ctx context.Context, req *mcp.CallToolRequest, in ListWorkloadsInput) (*mcp.CallToolResult, ListWorkloadsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	namespaces, err := t.namespacesFor(in.Namespace)
	if err != nil {
		return nil, ListWorkloadsOutput{}, err
	}

	out := ListWorkloadsOutput{Workloads: []WorkloadSummary{}}
	for _, ns := range namespaces {
		deps, err := t.clients.Typed.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("deployments in %q: %v", ns, err))
		} else {
			for i := range deps.Items {
				out.Workloads = append(out.Workloads, summarizeDeployment(&deps.Items[i]))
			}
		}
		stss, err := t.clients.Typed.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("statefulsets in %q: %v", ns, err))
		} else {
			for i := range stss.Items {
				out.Workloads = append(out.Workloads, summarizeStatefulSet(&stss.Items[i]))
			}
		}
		dss, err := t.clients.Typed.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("daemonsets in %q: %v", ns, err))
		} else {
			for i := range dss.Items {
				out.Workloads = append(out.Workloads, summarizeDaemonSet(&dss.Items[i]))
			}
		}
	}

	return nil, out, nil
}

// WorkloadStatusInput identifies one workload.
type WorkloadStatusInput struct {
	Namespace string `json:"namespace" jsonschema:"the workload's namespace"`
	Kind      string `json:"kind" jsonschema:"Deployment, StatefulSet, or DaemonSet (case-insensitive)"`
	Name      string `json:"name" jsonschema:"the workload's name"`
}

// WorkloadStatus implements the workload_status tool.
func (t *Toolset) WorkloadStatus(ctx context.Context, req *mcp.CallToolRequest, in WorkloadStatusInput) (*mcp.CallToolResult, WorkloadStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, WorkloadStatusOutput{}, err
	}

	var out WorkloadStatusOutput
	var selector *metav1.LabelSelector

	switch strings.ToLower(in.Kind) {
	case "deployment":
		dep, err := t.clients.Typed.AppsV1().Deployments(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, out, err
		}
		out.WorkloadSummary = summarizeDeployment(dep)
		out.Generation = dep.Generation
		out.ObservedGeneration = dep.Status.ObservedGeneration
		out.RolloutRevision = dep.Annotations[revisionAnnotation]
		selector = dep.Spec.Selector
	case "statefulset":
		sts, err := t.clients.Typed.AppsV1().StatefulSets(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, out, err
		}
		out.WorkloadSummary = summarizeStatefulSet(sts)
		out.Generation = sts.Generation
		out.ObservedGeneration = sts.Status.ObservedGeneration
		selector = sts.Spec.Selector
	case "daemonset":
		ds, err := t.clients.Typed.AppsV1().DaemonSets(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, out, err
		}
		out.WorkloadSummary = summarizeDaemonSet(ds)
		out.Generation = ds.Generation
		out.ObservedGeneration = ds.Status.ObservedGeneration
		selector = ds.Spec.Selector
	default:
		return nil, out, fmt.Errorf("unsupported kind %q: must be Deployment, StatefulSet, or DaemonSet", in.Kind)
	}

	if selector != nil {
		sel, err := metav1.LabelSelectorAsSelector(selector)
		if err == nil {
			pods, err := t.clients.Typed.CoreV1().Pods(in.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
			if err == nil {
				for i := range pods.Items {
					out.Pods = append(out.Pods, t.summarizePod(&pods.Items[i]))
				}
			}
		}
	}

	return nil, out, nil
}

func summarizeDeployment(d *appsv1.Deployment) WorkloadSummary {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	s := WorkloadSummary{
		Name:              d.Name,
		Namespace:         d.Namespace,
		Kind:              "Deployment",
		DesiredReplicas:   desired,
		ReadyReplicas:     d.Status.ReadyReplicas,
		UpdatedReplicas:   d.Status.UpdatedReplicas,
		AvailableReplicas: d.Status.AvailableReplicas,
	}
	for _, c := range d.Status.Conditions {
		s.Conditions = append(s.Conditions, Condition{
			Type:               string(c.Type),
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: fmtTime(c.LastTransitionTime),
		})
	}

	return s
}

func summarizeStatefulSet(sts *appsv1.StatefulSet) WorkloadSummary {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	s := WorkloadSummary{
		Name:              sts.Name,
		Namespace:         sts.Namespace,
		Kind:              "StatefulSet",
		DesiredReplicas:   desired,
		ReadyReplicas:     sts.Status.ReadyReplicas,
		UpdatedReplicas:   sts.Status.UpdatedReplicas,
		AvailableReplicas: sts.Status.AvailableReplicas,
	}
	for _, c := range sts.Status.Conditions {
		s.Conditions = append(s.Conditions, Condition{
			Type:               string(c.Type),
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: fmtTime(c.LastTransitionTime),
		})
	}

	return s
}

func summarizeDaemonSet(ds *appsv1.DaemonSet) WorkloadSummary {
	s := WorkloadSummary{
		Name:              ds.Name,
		Namespace:         ds.Namespace,
		Kind:              "DaemonSet",
		DesiredReplicas:   ds.Status.DesiredNumberScheduled,
		ReadyReplicas:     ds.Status.NumberReady,
		UpdatedReplicas:   ds.Status.UpdatedNumberScheduled,
		AvailableReplicas: ds.Status.NumberAvailable,
	}
	for _, c := range ds.Status.Conditions {
		s.Conditions = append(s.Conditions, Condition{
			Type:               string(c.Type),
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: fmtTime(c.LastTransitionTime),
		})
	}

	return s
}
