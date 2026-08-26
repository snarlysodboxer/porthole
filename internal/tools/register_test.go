package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/snarlysodboxer/porthole/internal/config"
)

// connect registers the toolset on a real MCP server and returns a client
// session over an in-memory transport. This exercises AddTool's schema
// inference (which panics on invalid input/output types) and the full
// call path.
func connect(t *testing.T, cfg *config.Config) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "porthole", Version: "test"}, &mcp.ServerOptions{
		Instructions: Instructions(cfg),
	})
	newTestToolset(cfg).Register(server)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(context.Background(), serverTransport)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	return session
}

func listToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	return names
}

func TestRegisterAllFeaturesEnabled(t *testing.T) {
	session := connect(t, testConfig())
	names := listToolNames(t, session)
	want := []string{
		"list_namespaces", "list_workloads", "workload_status", "list_pods",
		"pod_status", "events", "service_endpoints", "pvc_status", "job_status",
		"network_policies", "rbac_summary", "service_account_access",
		"storage_classes", "webhook_configs", "top_pods", "node_status",
		"list_api_resources", "secret_metadata", "configmap_metadata",
		"list_resources", "resource_conditions", "resource_status", "pod_logs",
	}
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("tool %s not registered; got %v", name, names)
		}
	}
	if len(names) != len(want) {
		t.Errorf("got %d tools, want %d: %v", len(names), len(want), names)
	}
}

func TestRegisterFeatureGating(t *testing.T) {
	cfg := testConfig()
	disabled := false
	cfg.EnableCRDConditions = &disabled
	cfg.EnableCRDStatus = false
	cfg.EnableLogs = false
	cfg.EnableSecretMetadata = false
	cfg.EnableConfigMapMetadata = false

	session := connect(t, cfg)
	names := listToolNames(t, session)
	gatedOff := []string{
		"resource_conditions", "resource_status", "pod_logs",
		"secret_metadata", "configmap_metadata", "list_resources",
	}
	for _, gated := range gatedOff {
		if slices.Contains(names, gated) {
			t.Errorf("tool %s should be gated off; got %v", gated, names)
		}
	}
}

func TestCallToolOverProtocol(t *testing.T) {
	session := connect(t, testConfig())
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "workload_status",
		Arguments: map[string]any{"namespace": "prod", "kind": "Deployment", "name": "web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	serialized := ""
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			serialized += text.Text
		}
	}
	if !strings.Contains(serialized, "ProgressDeadlineExceeded") {
		t.Errorf("response missing condition reason: %s", serialized)
	}
	for _, sentinel := range sentinels {
		if strings.Contains(serialized, sentinel) {
			t.Errorf("protocol-level response leaked %s", sentinel)
		}
	}
}

func TestCallToolMissingRequiredParamRejected(t *testing.T) {
	session := connect(t, testConfig())
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "pod_status",
		Arguments: map[string]any{"namespace": "prod"},
	})
	if err == nil && !res.IsError {
		t.Fatal("expected schema validation to reject a call missing required 'name'")
	}
}
