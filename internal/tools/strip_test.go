package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestResourceStatusStripsEverythingButStatus is the dedicated test for the
// one unstructured passthrough: given an adversarial object with spec,
// annotations, managedFields, and last-applied all populated, the shaped
// output may contain only .status plus allowlisted metadata.
func TestResourceStatusStripsEverythingButStatus(t *testing.T) {
	out := shapeResourceStatus(fixtureWidget())

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)

	for _, denied := range []string{
		leakCRSpec, leakAnnotation, leakLastApplied, leakManagedFields,
		`"spec"`, "annotations", "managedFields", "last-applied",
	} {
		if strings.Contains(serialized, denied) {
			t.Errorf("resource_status output contains denied content %q:\n%s", denied, serialized)
		}
	}

	// Sanity: the allowed content is present.
	if out.Status["phase"] != "Ready" {
		t.Errorf("status not preserved: %+v", out.Status)
	}
	// Secret-looking status keys are redacted; numeric ones are kept.
	if out.Status["rootPasswordHash"] != "(redacted)" {
		t.Errorf("rootPasswordHash = %v, want (redacted)", out.Status["rootPasswordHash"])
	}
	if out.Status["keyCount"] != int64(2) {
		t.Errorf("keyCount = %v (%T), want the number kept", out.Status["keyCount"], out.Status["keyCount"])
	}
	if out.Name != "widget-1" || out.Namespace != "prod" || out.Labels["app"] != "widget" {
		t.Errorf("allowlisted metadata missing: %+v", out)
	}
	if out.CreatedAt == "" {
		t.Error("creationTimestamp missing")
	}
}

// TestResponseTypesAreClosed enforces the struct-level rule that keeps the
// leak test strong: no response type may contain an open-ended container
// (map with non-string values, or any interface type) that could smuggle
// arbitrary object fields - except the single sanctioned passthrough,
// ResourceStatusOutput.Status.
func TestResponseTypesAreClosed(t *testing.T) {
	allowed := map[string]bool{
		"ResourceStatusOutput.Status": true,
	}
	responseTypes := []reflect.Type{
		reflect.TypeFor[ListNamespacesOutput](),
		reflect.TypeFor[ListWorkloadsOutput](),
		reflect.TypeFor[WorkloadStatusOutput](),
		reflect.TypeFor[ListPodsOutput](),
		reflect.TypeFor[PodStatusOutput](),
		reflect.TypeFor[EventsOutput](),
		reflect.TypeFor[ServiceEndpointsOutput](),
		reflect.TypeFor[PVCStatusOutput](),
		reflect.TypeFor[JobStatusOutput](),
		reflect.TypeFor[ResourceConditionsOutput](),
		reflect.TypeFor[ResourceStatusOutput](),
		reflect.TypeFor[PodLogsOutput](),
		reflect.TypeFor[NetworkPoliciesOutput](),
		reflect.TypeFor[RBACSummaryOutput](),
		reflect.TypeFor[ServiceAccountAccessOutput](),
		reflect.TypeFor[StorageClassesOutput](),
		reflect.TypeFor[WebhookConfigsOutput](),
		reflect.TypeFor[TopPodsOutput](),
		reflect.TypeFor[NodeStatusOutput](),
		reflect.TypeFor[ListAPIResourcesOutput](),
		reflect.TypeFor[SecretMetadataOutput](),
		reflect.TypeFor[ConfigMapMetadataOutput](),
		reflect.TypeFor[ListResourcesOutput](),
	}
	for _, typ := range responseTypes {
		checkClosed(t, typ, typ.Name(), allowed, map[reflect.Type]bool{})
	}
}

func checkClosed(t *testing.T, typ reflect.Type, path string, allowed map[string]bool, visited map[reflect.Type]bool) {
	t.Helper()
	if allowed[path] {
		return
	}
	switch typ.Kind() {
	case reflect.Interface:
		t.Errorf("%s: interface type %v is an open container", path, typ)
	case reflect.Map:
		if typ.Key().Kind() != reflect.String || typ.Elem().Kind() != reflect.String {
			t.Errorf("%s: map type %v is an open container (only map[string]string is allowed)", path, typ)
		}
	case reflect.Pointer, reflect.Slice, reflect.Array:
		checkClosed(t, typ.Elem(), path, allowed, visited)
	case reflect.Struct:
		if visited[typ] {
			return
		}
		visited[typ] = true
		if typ.PkgPath() != "" && !strings.Contains(typ.PkgPath(), "snarlysodboxer/porthole") {
			t.Errorf("%s: foreign struct type %v in a response - responses must be hand-shaped", path, typ)
			return
		}
		for f := range typ.Fields() {
			fieldPath := typ.Name() + "." + f.Name
			if f.Anonymous {
				fieldPath = path
			}
			checkClosed(t, f.Type, fieldPath, allowed, visited)
		}
	}
}
