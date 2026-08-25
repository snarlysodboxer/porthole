package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ListPodsInput selects pods by namespace and optional label selector.
type ListPodsInput struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"namespace to list; omit to scan every allowed namespace"`
	LabelSelector string `json:"label_selector,omitempty" jsonschema:"Kubernetes label selector, e.g. app=matomo"`
}

// ListPods implements the list_pods tool.
func (t *Toolset) ListPods(ctx context.Context, req *mcp.CallToolRequest, in ListPodsInput) (*mcp.CallToolResult, ListPodsOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if in.LabelSelector != "" {
		if _, err := labels.Parse(in.LabelSelector); err != nil {
			return nil, ListPodsOutput{}, fmt.Errorf("invalid label_selector: %w", err)
		}
	}
	namespaces, err := t.namespacesFor(in.Namespace)
	if err != nil {
		return nil, ListPodsOutput{}, err
	}

	out := ListPodsOutput{Pods: []PodSummary{}}
	for _, ns := range namespaces {
		pods, err := t.clients.Typed.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: in.LabelSelector})
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("pods in %q: %v", ns, err))
			continue
		}
		for i := range pods.Items {
			out.Pods = append(out.Pods, t.summarizePod(&pods.Items[i]))
		}
	}

	return nil, out, nil
}

// PodStatusInput identifies one pod.
type PodStatusInput struct {
	Namespace string `json:"namespace" jsonschema:"the pod's namespace"`
	Name      string `json:"name" jsonschema:"the pod's name"`
}

// PodStatus implements the pod_status tool.
func (t *Toolset) PodStatus(ctx context.Context, req *mcp.CallToolRequest, in PodStatusInput) (*mcp.CallToolResult, PodStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, PodStatusOutput{}, err
	}
	pod, err := t.clients.Typed.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, PodStatusOutput{}, err
	}

	out := PodStatusOutput{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Labels:    pod.Labels,
		Phase:     string(pod.Status.Phase),
		Reason:    pod.Status.Reason,
		Message:   pod.Status.Message,
		QOSClass:  string(pod.Status.QOSClass),
		Node:      pod.Spec.NodeName,
		CreatedAt: fmtTime(pod.CreationTimestamp),
		StartedAt: fmtTimePtr(pod.Status.StartTime),
	}
	for _, c := range pod.Status.Conditions {
		out.Conditions = append(out.Conditions, Condition{
			Type:               string(c.Type),
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: fmtTime(c.LastTransitionTime),
		})
	}
	out.InitContainers = detailContainers(pod.Spec.InitContainers, pod.Status.InitContainerStatuses)
	out.Containers = detailContainers(pod.Spec.Containers, pod.Status.ContainerStatuses)
	for _, v := range pod.Spec.Volumes {
		out.Volumes = append(out.Volumes, shapeVolume(v))
	}

	events, truncated, err := t.fetchEvents(ctx, in.Namespace, "Pod", in.Name, 0)
	if err == nil {
		out.Events = events
		out.TruncatedEvents = truncated
	}

	return nil, out, nil
}

// summarizePod builds the listing shape shared by list_pods and
// workload_status.
func (t *Toolset) summarizePod(pod *corev1.Pod) PodSummary {
	total := len(pod.Spec.Containers)
	ready := 0
	var restarts int32
	var briefs []ContainerBrief
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
		briefs = append(briefs, ContainerBrief{
			Name:         cs.Name,
			Ready:        cs.Ready,
			RestartCount: cs.RestartCount,
			State:        shapeContainerState(cs.State),
			LastState:    shapeLastState(cs.LastTerminationState),
		})
	}

	return PodSummary{
		Name:       pod.Name,
		Namespace:  pod.Namespace,
		Phase:      string(pod.Status.Phase),
		Ready:      fmt.Sprintf("%d/%d", ready, total),
		Restarts:   restarts,
		Age:        age(pod.CreationTimestamp.Time, t.now()),
		CreatedAt:  fmtTime(pod.CreationTimestamp),
		Node:       pod.Spec.NodeName,
		Containers: briefs,
	}
}

// detailContainers joins spec containers (for the allowlisted spec facts)
// with their statuses.
func detailContainers(specs []corev1.Container, statuses []corev1.ContainerStatus) []ContainerDetail {
	byName := make(map[string]*corev1.ContainerStatus, len(statuses))
	for i := range statuses {
		byName[statuses[i].Name] = &statuses[i]
	}
	var out []ContainerDetail
	for _, c := range specs {
		d := ContainerDetail{
			Name:     c.Name,
			Image:    c.Image,
			Env:      shapeEnv(c.Env),
			EnvFrom:  shapeEnvFrom(c.EnvFrom),
			Ports:    shapePorts(c.Ports),
			Requests: shapeResourceList(c.Resources.Requests),
			Limits:   shapeResourceList(c.Resources.Limits),
		}
		if cs, ok := byName[c.Name]; ok {
			d.Ready = cs.Ready
			d.RestartCount = cs.RestartCount
			d.State = shapeContainerState(cs.State)
			d.LastState = shapeLastState(cs.LastTerminationState)
		}
		out = append(out, d)
	}

	return out
}

// shapeEnv reduces env vars to names and reference sources. Values are
// never copied: a literal is reported as "literal (value omitted)".
func shapeEnv(env []corev1.EnvVar) []EnvVarRef {
	var out []EnvVarRef
	for _, e := range env {
		ref := EnvVarRef{Name: e.Name, Source: "literal (value omitted)"}
		if vf := e.ValueFrom; vf != nil {
			switch {
			case vf.SecretKeyRef != nil:
				ref.Source = fmt.Sprintf("secretKeyRef:%s/%s", vf.SecretKeyRef.Name, vf.SecretKeyRef.Key)
			case vf.ConfigMapKeyRef != nil:
				ref.Source = fmt.Sprintf("configMapKeyRef:%s/%s", vf.ConfigMapKeyRef.Name, vf.ConfigMapKeyRef.Key)
			case vf.FieldRef != nil:
				ref.Source = fmt.Sprintf("fieldRef:%s", vf.FieldRef.FieldPath)
			case vf.ResourceFieldRef != nil:
				ref.Source = fmt.Sprintf("resourceFieldRef:%s", vf.ResourceFieldRef.Resource)
			}
		}
		out = append(out, ref)
	}

	return out
}

func shapeEnvFrom(envFrom []corev1.EnvFromSource) []string {
	var out []string
	for _, e := range envFrom {
		switch {
		case e.SecretRef != nil:
			out = append(out, "secretRef:"+e.SecretRef.Name)
		case e.ConfigMapRef != nil:
			out = append(out, "configMapRef:"+e.ConfigMapRef.Name)
		}
	}

	return out
}

func shapePorts(ports []corev1.ContainerPort) []string {
	var out []string
	for _, p := range ports {
		s := fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol)
		if p.Name != "" {
			s += " (" + p.Name + ")"
		}
		out = append(out, s)
	}

	return out
}

// shapeVolume reports the volume's name and which VolumeSource field is
// set — via reflection so new source types are named automatically — plus
// the referenced object's name for claim/configMap/secret sources. Volume
// contents are never touched.
func shapeVolume(v corev1.Volume) VolumeInfo {
	info := VolumeInfo{
		Name: v.Name,
		Type: volumeSourceName(reflect.ValueOf(v.VolumeSource)),
	}
	switch {
	case v.PersistentVolumeClaim != nil:
		info.SourceName = v.PersistentVolumeClaim.ClaimName
	case v.ConfigMap != nil:
		info.SourceName = v.ConfigMap.Name
	case v.Secret != nil:
		info.SourceName = v.Secret.SecretName
	}

	return info
}

// volumeSourceName returns the canonical camelCase name (the field's JSON
// tag, e.g. "nfs", "persistentVolumeClaim") of the first set pointer field
// in a volume-source union struct.
func volumeSourceName(val reflect.Value) string {
	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		if val.Field(i).Kind() == reflect.Pointer && !val.Field(i).IsNil() {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if tag != "" {
				return tag
			}
			return typ.Field(i).Name
		}
	}

	return "unknown"
}
