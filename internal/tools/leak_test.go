package tools

// The leak test is the teeth behind the "omission by construction" claim:
// every tool runs against adversarial fixtures whose denied fields (env
// values, command/args, annotations including last-applied, ConfigMap and
// Secret data, CR specs, managedFields) all carry sentinel strings, and no
// sentinel may appear in any serialized response. resource_status is
// covered by its own strip test (strip_test.go) since its .status
// passthrough is unstructured by design.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNoDeniedFieldLeaks(t *testing.T) {
	ts := newTestToolset(testConfig())
	ctx := context.Background()

	responses := map[string]any{}
	collect := func(name string, out any, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		responses[name] = out
	}

	{
		_, out, err := ts.ListNamespaces(ctx, nil, ListNamespacesInput{})
		collect("list_namespaces", out, err)
	}
	{
		_, out, err := ts.ListWorkloads(ctx, nil, ListWorkloadsInput{})
		collect("list_workloads", out, err)
	}
	{
		_, out, err := ts.WorkloadStatus(ctx, nil, WorkloadStatusInput{Namespace: "prod", Kind: "Deployment", Name: "web"})
		collect("workload_status deployment", out, err)
	}
	{
		_, out, err := ts.WorkloadStatus(ctx, nil, WorkloadStatusInput{Namespace: "prod", Kind: "StatefulSet", Name: "db"})
		collect("workload_status statefulset", out, err)
	}
	{
		_, out, err := ts.WorkloadStatus(ctx, nil, WorkloadStatusInput{Namespace: "dev", Kind: "DaemonSet", Name: "agent"})
		collect("workload_status daemonset", out, err)
	}
	{
		_, out, err := ts.ListPods(ctx, nil, ListPodsInput{})
		collect("list_pods", out, err)
	}
	{
		_, out, err := ts.PodStatus(ctx, nil, PodStatusInput{Namespace: "prod", Name: "web-1"})
		collect("pod_status", out, err)
	}
	{
		_, out, err := ts.Events(ctx, nil, EventsInput{})
		collect("events", out, err)
	}
	{
		_, out, err := ts.ServiceEndpoints(ctx, nil, ServiceEndpointsInput{Namespace: "prod", Name: "web"})
		collect("service_endpoints", out, err)
	}
	{
		_, out, err := ts.PVCStatus(ctx, nil, PVCStatusInput{Namespace: "prod"})
		collect("pvc_status", out, err)
	}
	{
		_, out, err := ts.JobStatus(ctx, nil, JobStatusInput{Namespace: "prod"})
		collect("job_status", out, err)
	}
	{
		_, out, err := ts.ResourceConditions(ctx, nil, ResourceInput{Namespace: "prod", Kind: "Widget", Name: "widget-1"})
		collect("resource_conditions", out, err)
	}
	{
		_, out, err := ts.ResourceStatus(ctx, nil, ResourceInput{Namespace: "prod", Kind: "Widget", Name: "widget-1"})
		collect("resource_status", out, err)
	}

	for name, out := range responses {
		data, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		serialized := string(data)
		for _, sentinel := range sentinels {
			if strings.Contains(serialized, sentinel) {
				t.Errorf("%s leaked %s:\n%s", name, sentinel, serialized)
			}
		}
		// The last-applied annotation key must never appear either.
		if strings.Contains(serialized, "last-applied-configuration") {
			t.Errorf("%s serialized the last-applied annotation:\n%s", name, serialized)
		}
	}
}

// TestLeakSentinelsArePlanted guards the leak test itself: if the fixtures
// stop carrying a sentinel, the leak test would pass vacuously.
func TestLeakSentinelsArePlanted(t *testing.T) {
	planted := fmt.Sprintf("%+v %+v", fixtureObjects(), fixtureWidget())
	for _, sentinel := range sentinels {
		if !strings.Contains(planted, sentinel) {
			t.Errorf("fixture objects do not carry sentinel %s", sentinel)
		}
	}
}
