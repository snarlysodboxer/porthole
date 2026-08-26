package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultClassAnnotation marks the cluster's default StorageClass - a
// hardcoded allowlisted annotation, read by exact key like the rollout
// revision.
const defaultClassAnnotation = "storageclass.kubernetes.io/is-default-class"

// StorageClassesInput has no parameters.
type StorageClassesInput struct{}

// StorageClasses implements the storage_classes tool. StorageClasses are
// cluster-scoped, so this requires the optional cluster-scoped grant. The
// parameters map is deliberately never copied: it can reference endpoints
// and secret names.
func (t *Toolset) StorageClasses(ctx context.Context, req *mcp.CallToolRequest, in StorageClassesInput) (*mcp.CallToolResult, StorageClassesOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	list, err := t.clients.Typed.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, StorageClassesOutput{}, err
	}

	out := StorageClassesOutput{StorageClasses: []StorageClassInfo{}}
	for i := range list.Items {
		sc := &list.Items[i]
		info := StorageClassInfo{
			Name:        sc.Name,
			Provisioner: sc.Provisioner,
			IsDefault:   sc.Annotations[defaultClassAnnotation] == "true",
		}
		if sc.ReclaimPolicy != nil {
			info.ReclaimPolicy = string(*sc.ReclaimPolicy)
		}
		if sc.VolumeBindingMode != nil {
			info.VolumeBindingMode = string(*sc.VolumeBindingMode)
		}
		if sc.AllowVolumeExpansion != nil {
			info.AllowVolumeExpansion = *sc.AllowVolumeExpansion
		}
		out.StorageClasses = append(out.StorageClasses, info)
	}

	return nil, out, nil
}
