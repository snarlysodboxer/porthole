package tools

import (
	"context"
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PVCStatusInput selects a namespace.
type PVCStatusInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose PersistentVolumeClaims to list"`
}

// PVCStatus implements the pvc_status tool.
func (t *Toolset) PVCStatus(ctx context.Context, req *mcp.CallToolRequest, in PVCStatusInput) (*mcp.CallToolResult, PVCStatusOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, PVCStatusOutput{}, err
	}
	list, err := t.clients.Typed.CoreV1().PersistentVolumeClaims(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, PVCStatusOutput{}, err
	}

	out := PVCStatusOutput{Namespace: in.Namespace, PVCs: []PVCInfo{}}

	// PersistentVolumes are cluster-scoped, so this needs a
	// ClusterRoleBinding; without one the tool degrades to PVC-only info.
	pvByName := map[string]*corev1.PersistentVolume{}
	pvs, err := t.clients.Typed.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("persistentvolumes not readable (PVC info is still complete): %v", err))
	} else {
		for i := range pvs.Items {
			pv := &pvs.Items[i]
			pvByName[pv.Name] = pv
			// Surface volumes that reference this namespace but aren't
			// serving a bound claim — Released/Failed orphans and
			// Available volumes reserved for a claim.
			if pv.Status.Phase != corev1.VolumeBound &&
				pv.Spec.ClaimRef != nil && pv.Spec.ClaimRef.Namespace == in.Namespace {
				out.UnboundVolumes = append(out.UnboundVolumes, shapePV(pv))
			}
		}
	}

	for i := range list.Items {
		pvc := &list.Items[i]
		info := PVCInfo{
			Name:          pvc.Name,
			Phase:         string(pvc.Status.Phase),
			RequestedSize: quantityString(pvc.Spec.Resources.Requests, corev1.ResourceStorage),
			Capacity:      quantityString(pvc.Status.Capacity, corev1.ResourceStorage),
			VolumeName:    pvc.Spec.VolumeName,
		}
		if pvc.Spec.StorageClassName != nil {
			info.StorageClass = *pvc.Spec.StorageClassName
		}
		for _, m := range pvc.Spec.AccessModes {
			info.AccessModes = append(info.AccessModes, string(m))
		}
		if pv, ok := pvByName[pvc.Spec.VolumeName]; ok {
			shaped := shapePV(pv)
			info.Volume = &shaped
		}
		out.PVCs = append(out.PVCs, info)
	}

	return nil, out, nil
}

// shapePV reduces a PersistentVolume to status plus allowlisted spec facts.
// Volume source parameters (CSI volumeAttributes, server addresses, secret
// refs) are never copied — only which source type backs the volume and the
// CSI volume handle.
func shapePV(pv *corev1.PersistentVolume) PVInfo {
	info := PVInfo{
		Name:          pv.Name,
		Phase:         string(pv.Status.Phase),
		Capacity:      quantityString(pv.Spec.Capacity, corev1.ResourceStorage),
		StorageClass:  pv.Spec.StorageClassName,
		ReclaimPolicy: string(pv.Spec.PersistentVolumeReclaimPolicy),
		Source:        pvSource(pv),
		Reason:        pv.Status.Reason,
		Message:       pv.Status.Message,
	}
	if pv.Spec.VolumeMode != nil {
		info.VolumeMode = string(*pv.Spec.VolumeMode)
	}
	for _, m := range pv.Spec.AccessModes {
		info.AccessModes = append(info.AccessModes, string(m))
	}
	if ref := pv.Spec.ClaimRef; ref != nil {
		info.ClaimRef = ref.Namespace + "/" + ref.Name
	}
	if pv.Spec.CSI != nil {
		info.VolumeHandle = pv.Spec.CSI.VolumeHandle
	}

	return info
}

// pvSource names the PV's backing source type, plus the driver for CSI —
// mirroring shapeVolume's approach for pod volumes.
func pvSource(pv *corev1.PersistentVolume) string {
	if pv.Spec.CSI != nil {
		return fmt.Sprintf("csi (%s)", pv.Spec.CSI.Driver)
	}

	return volumeSourceName(reflect.ValueOf(pv.Spec.PersistentVolumeSource))
}
