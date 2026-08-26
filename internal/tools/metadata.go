package tools

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The metadata tools answer "does the Secret/ConfigMap exist, which keys,
// how old" - the missing-key crash-loop question - without values ever
// having a field to land in. Kubernetes RBAC has no field-level reads, so
// serving key names requires get/list on the whole objects: that grant is
// the one real tradeoff in this server, which is why these tools sit behind
// their own flags and their own RBAC objects.

// SecretMetadataInput selects a namespace, optionally one Secret.
type SecretMetadataInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose Secrets to inspect"`
	Name      string `json:"name,omitempty" jsonschema:"limit to this Secret; errors if it does not exist"`
}

// SecretMetadata implements the flag-gated secret_metadata tool.
func (t *Toolset) SecretMetadata(ctx context.Context, req *mcp.CallToolRequest, in SecretMetadataInput) (*mcp.CallToolResult, SecretMetadataOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, SecretMetadataOutput{}, err
	}
	out := SecretMetadataOutput{Namespace: in.Namespace, Secrets: []SecretMeta{}}

	if in.Name != "" {
		secret, err := t.clients.Typed.CoreV1().Secrets(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, SecretMetadataOutput{}, err
		}
		out.Secrets = append(out.Secrets, t.shapeSecretMeta(secret))
		return nil, out, nil
	}

	list, err := t.clients.Typed.CoreV1().Secrets(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, SecretMetadataOutput{}, err
	}
	for i := range list.Items {
		out.Secrets = append(out.Secrets, t.shapeSecretMeta(&list.Items[i]))
	}

	return nil, out, nil
}

// shapeSecretMeta copies only key names out of a Secret; data values never
// touch the response struct.
func (t *Toolset) shapeSecretMeta(secret *corev1.Secret) SecretMeta {
	keys := make([]string, 0, len(secret.Data)+len(secret.StringData))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	for k := range secret.StringData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return SecretMeta{
		Name:      secret.Name,
		Type:      string(secret.Type),
		Keys:      keys,
		KeyCount:  len(keys),
		CreatedAt: fmtTime(secret.CreationTimestamp),
		Age:       age(secret.CreationTimestamp.Time, t.now()),
		OwnedBy:   ownerStrings(secret.OwnerReferences),
	}
}

// ConfigMapMetadataInput selects a namespace, optionally one ConfigMap.
type ConfigMapMetadataInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace whose ConfigMaps to inspect"`
	Name      string `json:"name,omitempty" jsonschema:"limit to this ConfigMap; errors if it does not exist"`
}

// ConfigMapMetadata implements the flag-gated configmap_metadata tool.
func (t *Toolset) ConfigMapMetadata(ctx context.Context, req *mcp.CallToolRequest, in ConfigMapMetadataInput) (*mcp.CallToolResult, ConfigMapMetadataOutput, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	if err := t.checkNamespace(in.Namespace); err != nil {
		return nil, ConfigMapMetadataOutput{}, err
	}
	out := ConfigMapMetadataOutput{Namespace: in.Namespace, ConfigMaps: []ConfigMapMeta{}}

	if in.Name != "" {
		cm, err := t.clients.Typed.CoreV1().ConfigMaps(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		if err != nil {
			return nil, ConfigMapMetadataOutput{}, err
		}
		out.ConfigMaps = append(out.ConfigMaps, t.shapeConfigMapMeta(cm))
		return nil, out, nil
	}

	list, err := t.clients.Typed.CoreV1().ConfigMaps(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, ConfigMapMetadataOutput{}, err
	}
	for i := range list.Items {
		out.ConfigMaps = append(out.ConfigMaps, t.shapeConfigMapMeta(&list.Items[i]))
	}

	return nil, out, nil
}

// shapeConfigMapMeta copies only key names out of a ConfigMap.
func (t *Toolset) shapeConfigMapMeta(cm *corev1.ConfigMap) ConfigMapMeta {
	keys := make([]string, 0, len(cm.Data)+len(cm.BinaryData))
	for k := range cm.Data {
		keys = append(keys, k)
	}
	for k := range cm.BinaryData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return ConfigMapMeta{
		Name:      cm.Name,
		Keys:      keys,
		KeyCount:  len(keys),
		CreatedAt: fmtTime(cm.CreationTimestamp),
		Age:       age(cm.CreationTimestamp.Time, t.now()),
		OwnedBy:   ownerStrings(cm.OwnerReferences),
	}
}
