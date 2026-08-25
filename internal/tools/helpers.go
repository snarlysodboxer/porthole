package tools

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fmtTime(t metav1.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}

func fmtTimePtr(t *metav1.Time) string {
	if t == nil {
		return ""
	}

	return fmtTime(*t)
}

// age renders a duration compactly, e.g. "3d4h", "5m12s".
func age(from time.Time, now time.Time) string {
	if from.IsZero() {
		return ""
	}
	d := max(now.Sub(from), 0)
	switch {
	case d >= 24*time.Hour:
		days := d / (24 * time.Hour)
		hours := (d % (24 * time.Hour)) / time.Hour
		return fmt.Sprintf("%dd%dh", days, hours)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", d/time.Hour, (d%time.Hour)/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", d/time.Minute, (d%time.Minute)/time.Second)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

func shapeContainerState(s corev1.ContainerState) ContainerState {
	switch {
	case s.Running != nil:
		return ContainerState{State: "running", StartedAt: fmtTime(s.Running.StartedAt)}
	case s.Terminated != nil:
		ec := s.Terminated.ExitCode
		return ContainerState{
			State:      "terminated",
			Reason:     s.Terminated.Reason,
			Message:    s.Terminated.Message,
			ExitCode:   &ec,
			StartedAt:  fmtTime(s.Terminated.StartedAt),
			FinishedAt: fmtTime(s.Terminated.FinishedAt),
		}
	case s.Waiting != nil:
		return ContainerState{State: "waiting", Reason: s.Waiting.Reason, Message: s.Waiting.Message}
	default:
		return ContainerState{State: "unknown"}
	}
}

func shapeLastState(s corev1.ContainerState) *ContainerState {
	if s.Running == nil && s.Terminated == nil && s.Waiting == nil {
		return nil
	}
	shaped := shapeContainerState(s)

	return &shaped
}

func shapeResourceList(rl corev1.ResourceList) map[string]string {
	if len(rl) == 0 {
		return nil
	}
	out := make(map[string]string, len(rl))
	for name, qty := range rl {
		out[string(name)] = qty.String()
	}

	return out
}

func quantityString(rl corev1.ResourceList, name corev1.ResourceName) string {
	qty, ok := rl[name]
	if !ok {
		return ""
	}

	return (&qty).String()
}
