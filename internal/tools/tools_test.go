package tools

import (
	"context"
	"strings"
	"testing"
)

func TestListNamespaces(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ListNamespaces(context.Background(), nil, ListNamespacesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Namespaces) != 2 || out.Namespaces[0] != "prod" {
		t.Errorf("namespaces = %v, want [prod dev]", out.Namespaces)
	}
	if !out.Features.ResourceConditions || !out.Features.ResourceStatus || !out.Features.PodLogs {
		t.Errorf("features = %+v, want all enabled", out.Features)
	}
}

func TestListWorkloadsFanOut(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ListWorkloads(context.Background(), nil, ListWorkloadsInput{})
	if err != nil {
		t.Fatal(err)
	}
	// Fan-out over [prod, dev] must find web+db (prod) and agent (dev).
	if len(out.Workloads) != 3 {
		t.Fatalf("got %d workloads, want 3: %+v", len(out.Workloads), out.Workloads)
	}
	byName := map[string]WorkloadSummary{}
	for _, w := range out.Workloads {
		byName[w.Name] = w
	}
	web := byName["web"]
	if web.Kind != "Deployment" || web.DesiredReplicas != 3 || web.ReadyReplicas != 2 {
		t.Errorf("web = %+v, want Deployment desired=3 ready=2", web)
	}
	if len(web.Conditions) != 1 || web.Conditions[0].Reason != "ProgressDeadlineExceeded" {
		t.Errorf("web conditions = %+v", web.Conditions)
	}
	agent := byName["agent"]
	if agent.Kind != "DaemonSet" || agent.DesiredReplicas != 5 || agent.ReadyReplicas != 4 {
		t.Errorf("agent = %+v, want DaemonSet desired=5 ready=4", agent)
	}
}

func TestListWorkloadsNamespaceDenied(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ListWorkloads(context.Background(), nil, ListWorkloadsInput{Namespace: "kube-system"})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("err = %v, want allowlist rejection", err)
	}
}

func TestWorkloadStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.WorkloadStatus(context.Background(), nil, WorkloadStatusInput{
		Namespace: "prod", Kind: "deployment", Name: "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Generation != 4 || out.ObservedGeneration != 3 {
		t.Errorf("generation = %d/%d, want 4/3", out.Generation, out.ObservedGeneration)
	}
	if out.RolloutRevision != "7" {
		t.Errorf("rolloutRevision = %q, want 7", out.RolloutRevision)
	}
	if len(out.Pods) != 1 || out.Pods[0].Name != "web-1" {
		t.Fatalf("pods = %+v, want [web-1]", out.Pods)
	}
	if out.Pods[0].Ready != "0/1" || out.Pods[0].Restarts != 12 {
		t.Errorf("pod summary = %+v, want ready 0/1 restarts 12", out.Pods[0])
	}
}

func TestWorkloadStatusUnsupportedKind(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.WorkloadStatus(context.Background(), nil, WorkloadStatusInput{
		Namespace: "prod", Kind: "ReplicaSet", Name: "web",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("err = %v, want unsupported kind", err)
	}
}

func TestListPodsContainerStates(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ListPods(context.Background(), nil, ListPodsInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pods) != 1 {
		t.Fatalf("got %d pods, want 1", len(out.Pods))
	}
	pod := out.Pods[0]
	if pod.Age != "1h30m" {
		t.Errorf("age = %q, want 1h30m", pod.Age)
	}
	c := pod.Containers[0]
	if c.State.State != "waiting" || c.State.Reason != "CrashLoopBackOff" {
		t.Errorf("state = %+v, want waiting/CrashLoopBackOff", c.State)
	}
	if c.LastState == nil || c.LastState.Reason != "OOMKilled" || c.LastState.ExitCode == nil || *c.LastState.ExitCode != 137 {
		t.Errorf("lastState = %+v, want OOMKilled exit 137", c.LastState)
	}
}

func TestListPodsInvalidSelector(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ListPods(context.Background(), nil, ListPodsInput{Namespace: "prod", LabelSelector: "a=b=c"})
	if err == nil || !strings.Contains(err.Error(), "label_selector") {
		t.Fatalf("err = %v, want invalid selector", err)
	}
}

func TestPodStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.PodStatus(context.Background(), nil, PodStatusInput{Namespace: "prod", Name: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.QOSClass != "Burstable" || out.Node != "node-1" {
		t.Errorf("qos/node = %s/%s", out.QOSClass, out.Node)
	}
	if len(out.Containers) != 1 {
		t.Fatalf("containers = %+v", out.Containers)
	}
	c := out.Containers[0]
	wantEnv := map[string]string{
		"PLAIN":       "literal (value omitted)",
		"FROM_SECRET": "secretKeyRef:db-creds/password",
		"FROM_CM":     "configMapKeyRef:app-config/mode",
	}
	if len(c.Env) != len(wantEnv) {
		t.Fatalf("env = %+v", c.Env)
	}
	for _, e := range c.Env {
		if wantEnv[e.Name] != e.Source {
			t.Errorf("env %s source = %q, want %q", e.Name, e.Source, wantEnv[e.Name])
		}
	}
	if len(c.EnvFrom) != 1 || c.EnvFrom[0] != "secretRef:bulk-secret" {
		t.Errorf("envFrom = %v", c.EnvFrom)
	}
	if c.Requests["cpu"] != "100m" || c.Limits["memory"] != "256Mi" {
		t.Errorf("resources = %v / %v", c.Requests, c.Limits)
	}
	wantVolumes := map[string][2]string{
		"data":  {"persistentVolumeClaim", "data-pvc"},
		"creds": {"secret", "db-creds"},
		"conf":  {"configMap", "app-config"},
		"tmp":   {"emptyDir", ""},
	}
	if len(out.Volumes) != len(wantVolumes) {
		t.Fatalf("volumes = %+v", out.Volumes)
	}
	for _, v := range out.Volumes {
		want := wantVolumes[v.Name]
		if v.Type != want[0] || v.SourceName != want[1] {
			t.Errorf("volume %s = %s/%s, want %s/%s", v.Name, v.Type, v.SourceName, want[0], want[1])
		}
	}
	// The pod's events must be inlined, and only its own.
	if len(out.Events) != 2 {
		t.Fatalf("events = %+v, want the pod's 2 events", out.Events)
	}
	for _, e := range out.Events {
		if e.Object != "Pod/web-1" {
			t.Errorf("event object = %q, want Pod/web-1", e.Object)
		}
	}
}

func TestEventsFilterAndOrder(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.Events(context.Background(), nil, EventsInput{Namespace: "prod", Kind: "Pod", Name: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 2 {
		t.Fatalf("events = %+v, want 2", out.Events)
	}
	// Ascending by lastSeen: Scheduled (-49m) before BackOff (-2m).
	if out.Events[0].Reason != "Scheduled" || out.Events[1].Reason != "BackOff" {
		t.Errorf("order = %s, %s; want Scheduled, BackOff", out.Events[0].Reason, out.Events[1].Reason)
	}
	if out.Events[1].Count != 14 || !strings.Contains(out.Events[1].Message, "Back-off pulling image") {
		t.Errorf("backoff event = %+v", out.Events[1])
	}
}

func TestEventsSince(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.Events(context.Background(), nil, EventsInput{Namespace: "prod", Since: "10m"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range out.Events {
		if e.Reason == "Scheduled" || e.Reason == "ScalingReplicaSet" {
			t.Errorf("event %s should be filtered by since=10m", e.Reason)
		}
	}
	if len(out.Events) != 2 { // BackOff (-2m) and Reconciled (-1m)
		t.Errorf("events = %+v, want 2 recent", out.Events)
	}
}

func TestEventsTruncation(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEvents = 1
	ts := newTestToolset(cfg)
	_, out, err := ts.Events(context.Background(), nil, EventsInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 1 || out.TruncatedCount != 3 {
		t.Fatalf("got %d events, truncated %d; want 1 kept (most recent), 3 truncated", len(out.Events), out.TruncatedCount)
	}
	if out.Events[0].Reason != "Reconciled" {
		t.Errorf("kept event = %+v, want the most recent (Reconciled)", out.Events[0])
	}
	if out.Note == "" {
		t.Error("expected an explicit truncation note")
	}
}

func TestEventsInvalidSince(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.Events(context.Background(), nil, EventsInput{Since: "yesterday"})
	if err == nil || !strings.Contains(err.Error(), "since") {
		t.Fatalf("err = %v, want invalid since", err)
	}
}

func TestServiceEndpoints(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ServiceEndpoints(context.Background(), nil, ServiceEndpointsInput{Namespace: "prod", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ReadyEndpoints != 2 || out.NotReadyEndpoints != 1 {
		t.Errorf("endpoints = %d/%d ready/notReady, want 2/1", out.ReadyEndpoints, out.NotReadyEndpoints)
	}
	if out.Selector["app"] != "web" || len(out.Ports) != 1 || out.Ports[0].Port != 80 {
		t.Errorf("selector/ports = %v %v", out.Selector, out.Ports)
	}
}

func TestPVCStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.PVCStatus(context.Background(), nil, PVCStatusInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PVCs) != 1 {
		t.Fatalf("pvcs = %+v", out.PVCs)
	}
	pvc := out.PVCs[0]
	if pvc.Phase != "Bound" || pvc.RequestedSize != "10Gi" || pvc.StorageClass != "gp3" || pvc.VolumeName != "pv-123" {
		t.Errorf("pvc = %+v", pvc)
	}
	if pvc.Volume == nil {
		t.Fatal("expected bound PV detail on the PVC")
	}
	if pvc.Volume.ReclaimPolicy != "Delete" || pvc.Volume.Source != "csi (ebs.csi.aws.com)" ||
		pvc.Volume.VolumeHandle != "vol-0abc123" || pvc.Volume.ClaimRef != "prod/data-pvc" {
		t.Errorf("volume = %+v", pvc.Volume)
	}
	if len(out.UnboundVolumes) != 1 {
		t.Fatalf("unboundVolumes = %+v, want the Released pv-old", out.UnboundVolumes)
	}
	orphan := out.UnboundVolumes[0]
	if orphan.Name != "pv-old" || orphan.Phase != "Released" || orphan.Source != "nfs" {
		t.Errorf("orphan = %+v", orphan)
	}
	if len(out.Errors) != 0 {
		t.Errorf("errors = %v, want none when PVs are readable", out.Errors)
	}
}

func TestJobStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.JobStatus(context.Background(), nil, JobStatusInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 1 || len(out.CronJobs) != 1 {
		t.Fatalf("jobs/cronjobs = %d/%d, want 1/1", len(out.Jobs), len(out.CronJobs))
	}
	job := out.Jobs[0]
	if job.Failed != 2 || job.OwnedBy != "CronJob/backup" {
		t.Errorf("job = %+v", job)
	}
	if len(job.Conditions) != 1 || job.Conditions[0].Reason != "BackoffLimitExceeded" {
		t.Errorf("job conditions = %+v", job.Conditions)
	}
	cron := out.CronJobs[0]
	if cron.Schedule != "0 3 * * *" || cron.ActiveCount != 1 || cron.Suspend {
		t.Errorf("cron = %+v", cron)
	}
}

func TestJobStatusRequiresNamespace(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.JobStatus(context.Background(), nil, JobStatusInput{})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("err = %v, want namespace required", err)
	}
}

func TestResourceConditions(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Namespace: "prod", Kind: "widget", Name: "widget-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.APIVersion != "example.com/v1" || out.Kind != "Widget" {
		t.Errorf("identity = %s %s", out.APIVersion, out.Kind)
	}
	if len(out.Conditions) != 1 || out.Conditions[0].Reason != "ReconcileSuccess" || out.Conditions[0].Message != "widget is ready" {
		t.Fatalf("conditions = %+v", out.Conditions)
	}
	if len(out.Events) != 1 || out.Events[0].Reason != "Reconciled" {
		t.Errorf("events = %+v, want the widget's event", out.Events)
	}
}

func TestResourceConditionsAmbiguousKind(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Namespace: "prod", Kind: "Gateway", Name: "gw",
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
	if !strings.Contains(err.Error(), "gateway.networking.k8s.io") || !strings.Contains(err.Error(), "networking.istio.io") {
		t.Errorf("ambiguity error should list candidate groups: %v", err)
	}
}

func TestResourceConditionsClusterScopedRejected(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Namespace: "prod", Kind: "ClusterWidget", Name: "cw",
	})
	if err == nil || !strings.Contains(err.Error(), "cluster-scoped") {
		t.Fatalf("err = %v, want cluster-scoped rejection", err)
	}
}

func TestResourceStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ResourceStatus(context.Background(), nil, ResourceInput{
		Namespace: "prod", Kind: "Widget", Name: "widget-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status["phase"] != "Ready" {
		t.Errorf("status = %+v, want phase Ready", out.Status)
	}
	if out.Labels["app"] != "widget" {
		t.Errorf("labels = %v", out.Labels)
	}
}

func TestInstructionsAdvertiseScope(t *testing.T) {
	s := Instructions(testConfig())
	for _, want := range []string{"prod", "dev", "resource_conditions", "resource_status", "pod_logs"} {
		if !strings.Contains(s, want) {
			t.Errorf("instructions missing %q: %s", want, s)
		}
	}
}
