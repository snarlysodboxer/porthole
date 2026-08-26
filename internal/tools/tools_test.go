package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/snarlysodboxer/porthole/internal/config"
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
	if len(out.Events) != 3 { // ReconcileFailed (-3m), BackOff (-2m), Reconciled (-1m)
		t.Errorf("events = %+v, want 3 recent", out.Events)
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
	if len(out.Events) != 1 || out.TruncatedCount != 4 {
		t.Fatalf("got %d events, truncated %d; want 1 kept (most recent), 4 truncated", len(out.Events), out.TruncatedCount)
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

func TestResourceConditionsClusterScopedNotAllowlisted(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Kind: "ClusterGizmo", Name: "cg",
	})
	if err == nil || !strings.Contains(err.Error(), "clusterKinds allowlist") {
		t.Fatalf("err = %v, want clusterKinds allowlist rejection", err)
	}
}

func TestResourceConditionsClusterScopedAllowlisted(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Kind: "ClusterWidget", Name: "cw-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "ClusterWidget" || out.Namespace != "" {
		t.Errorf("identity = %s ns %q, want ClusterWidget with empty namespace", out.Kind, out.Namespace)
	}
	if len(out.Conditions) != 1 || out.Conditions[0].Message != "cluster widget is ready" {
		t.Errorf("conditions = %+v", out.Conditions)
	}
	// Events about the cluster-scoped object live in a controller-chosen
	// namespace (prod here); the allowlist fan-out must find them.
	if len(out.Events) != 1 || out.Events[0].Reason != "ReconcileFailed" {
		t.Errorf("events = %+v, want the cluster widget's event found via fan-out", out.Events)
	}
}

func TestResourceConditionsViewDetails(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ResourceConditions(context.Background(), nil, ResourceInput{
		Namespace: "prod", Kind: "Widget", Name: "widget-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The view declares five facts; the two whose paths land on a map and
	// a list of maps must be refused by the scalar-only extractor.
	if len(out.Details) != 3 {
		t.Fatalf("details = %+v, want only the scalar facts", out.Details)
	}
	if out.Details[0].Name != "phase" || out.Details[0].Value != "Ready" {
		t.Errorf("phase fact = %+v", out.Details[0])
	}
	if out.Details[1].Name != "conditionTypes" || out.Details[1].Value != "Ready" {
		t.Errorf("wildcard fact = %+v", out.Details[1])
	}
	if out.Details[2].Name != "conditionSummary" || out.Details[2].Value != "{type: Ready, status: True}" {
		t.Errorf("projection fact = %+v", out.Details[2])
	}
}

func TestLeavesAtWildcards(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "app", "env": []any{
					map[string]any{"name": "A"}, map[string]any{"name": "B"},
				}},
				map[string]any{"name": "sidecar", "env": []any{
					map[string]any{"name": "C"},
				}},
			},
		},
	}
	extract := func(path string) ([]any, error) {
		parsed, err := config.ParseFactPath(path)
		if err != nil {
			return nil, err
		}
		return leavesAt(obj, parsed.Segments), nil
	}

	names, err := extract(".spec.containers[*].name")
	if err != nil || len(names) != 2 || names[0] != "app" || names[1] != "sidecar" {
		t.Errorf("container names = %v (%v)", names, err)
	}
	envs, err := extract(".spec.containers[*].env[*].name")
	if err != nil || len(envs) != 3 || envs[2] != "C" {
		t.Errorf("env names = %v (%v)", envs, err)
	}
	first, err := extract(".spec.containers[0].name")
	if err != nil || len(first) != 1 || first[0] != "app" {
		t.Errorf("indexed name = %v (%v)", first, err)
	}
	if out, err := extract(".spec.containers[9].name"); err != nil || len(out) != 0 {
		t.Errorf("out-of-range index = %v (%v), want empty", out, err)
	}
	if out, err := extract(".spec.missing[*].name"); err != nil || len(out) != 0 {
		t.Errorf("missing field = %v (%v), want empty", out, err)
	}
	// [*] on a non-list contributes nothing rather than erroring.
	if out, err := extract(".spec.containers[0].name[*]"); err != nil || len(out) != 0 {
		t.Errorf("wildcard on scalar = %v (%v), want empty", out, err)
	}
}

func TestAnnotationsAllowlistOnResponses(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, pod, err := ts.PodStatus(context.Background(), nil, PodStatusInput{Namespace: "prod", Name: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the allowlisted key appears - not the sentinel-carrying key, and
	// not last-applied even though testConfig explicitly allowlists it.
	if len(pod.Annotations) != 1 || pod.Annotations[allowedAnnotationKey] != "team-alpha" {
		t.Errorf("pod annotations = %v, want only %s", pod.Annotations, allowedAnnotationKey)
	}
	_, wl, err := ts.WorkloadStatus(context.Background(), nil, WorkloadStatusInput{Namespace: "prod", Kind: "Deployment", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(wl.Annotations) != 1 || wl.Annotations[allowedAnnotationKey] != "team-alpha" {
		t.Errorf("workload annotations = %v", wl.Annotations)
	}
}

func TestAnnotationsEmptyAllowlistOmitsAll(t *testing.T) {
	cfg := testConfig()
	cfg.AllowedAnnotations = nil
	ts := newTestToolset(cfg)
	_, pod, err := ts.PodStatus(context.Background(), nil, PodStatusInput{Namespace: "prod", Name: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Annotations) != 0 {
		t.Errorf("annotations = %v, want none with an empty allowlist", pod.Annotations)
	}
}

func TestWorkloadStatusPDBEnrichment(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.WorkloadStatus(context.Background(), nil, WorkloadStatusInput{
		Namespace: "prod", Kind: "Deployment", Name: "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PodDisruptionBudgets) != 1 {
		t.Fatalf("pdbs = %+v, want the matching web-pdb", out.PodDisruptionBudgets)
	}
	pdb := out.PodDisruptionBudgets[0]
	if pdb.Name != "web-pdb" || pdb.MinAvailable != "2" || pdb.DisruptionsAllowed != 0 || pdb.ExpectedPods != 3 {
		t.Errorf("pdb = %+v", pdb)
	}
	if len(pdb.Conditions) != 1 || pdb.Conditions[0].Reason != "InsufficientPods" {
		t.Errorf("pdb conditions = %+v", pdb.Conditions)
	}
	// The db StatefulSet's pods don't match web-pdb's selector.
	_, sts, err := ts.WorkloadStatus(context.Background(), nil, WorkloadStatusInput{
		Namespace: "prod", Kind: "StatefulSet", Name: "db",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sts.PodDisruptionBudgets) != 0 {
		t.Errorf("db pdbs = %+v, want none", sts.PodDisruptionBudgets)
	}
}

func TestNetworkPolicies(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.NetworkPolicies(context.Background(), nil, NetworkPoliciesInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Policies) != 1 {
		t.Fatalf("policies = %+v", out.Policies)
	}
	p := out.Policies[0]
	if p.Name != "allow-web" || p.PodSelector != "app=web" {
		t.Errorf("policy = %+v", p)
	}
	if len(p.Ingress) != 1 || len(p.Ingress[0].Peers) != 2 {
		t.Fatalf("ingress = %+v", p.Ingress)
	}
	if p.Ingress[0].Peers[0] != "pods(app=frontend) in namespaces(team=a)" {
		t.Errorf("peer 0 = %q", p.Ingress[0].Peers[0])
	}
	if p.Ingress[0].Peers[1] != "ipBlock(10.0.0.0/8 except 10.0.1.0/24)" {
		t.Errorf("peer 1 = %q", p.Ingress[0].Peers[1])
	}
	if len(p.Ingress[0].Ports) != 1 || p.Ingress[0].Ports[0] != "8080-8090/TCP" {
		t.Errorf("ports = %v", p.Ingress[0].Ports)
	}
	if len(p.Egress) != 1 || p.Egress[0].Peers[0] != "namespaces((all))" {
		t.Errorf("egress = %+v", p.Egress)
	}
}

func TestRBACSummary(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.RBACSummary(context.Background(), nil, RBACSummaryInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ServiceAccounts) != 1 || out.ServiceAccounts[0].Name != "app-sa" {
		t.Fatalf("serviceAccounts = %+v", out.ServiceAccounts)
	}
	sa := out.ServiceAccounts[0]
	if sa.Automount == nil || *sa.Automount || len(sa.Secrets) != 1 || sa.Secrets[0] != "db-creds" {
		t.Errorf("sa = %+v", sa)
	}
	if len(out.Roles) != 1 || out.Roles[0].Name != "reader" {
		t.Fatalf("roles = %+v", out.Roles)
	}
	if out.Roles[0].Rules[0].ResourceNames[0] != "web-1" || out.Roles[0].Rules[0].Verbs[1] != "list" {
		t.Errorf("role rules = %+v", out.Roles[0].Rules)
	}
	if len(out.RoleBindings) != 3 {
		t.Fatalf("roleBindings = %+v", out.RoleBindings)
	}
	byName := map[string]BindingInfo{}
	for _, b := range out.RoleBindings {
		byName[b.Name] = b
	}
	direct := byName["reader-binding"]
	if direct.RoleRef != "Role/reader" || direct.Subjects[0] != "ServiceAccount:prod/app-sa" {
		t.Errorf("reader-binding = %+v", direct)
	}
	if len(direct.RoleRefRules) != 0 {
		t.Errorf("Role refs should not duplicate rules inline: %+v", direct)
	}
	viaCluster := byName["widget-view-binding"]
	if viaCluster.RoleRef != "ClusterRole/widget-viewer" || len(viaCluster.RoleRefRules) != 1 {
		t.Errorf("widget-view-binding should resolve ClusterRole rules: %+v", viaCluster)
	}
	// The dangling roleRef must be reported, not silently unresolved.
	if len(byName["dangling-binding"].RoleRefRules) != 0 {
		t.Errorf("dangling-binding should have no rules: %+v", byName["dangling-binding"])
	}
	foundDanglingError := false
	for _, e := range out.Errors {
		if strings.Contains(e, "deleted-role") && strings.Contains(e, "dangling-binding") {
			foundDanglingError = true
		}
	}
	if !foundDanglingError {
		t.Errorf("errors = %v, want one naming the dangling roleRef", out.Errors)
	}
	// The CRB has a prod group subject, so it touches this namespace.
	if len(out.ClusterRoleBindings) != 1 || out.ClusterRoleBindings[0].Name != "all-prod-sa-widget-view" {
		t.Fatalf("clusterRoleBindings = %+v", out.ClusterRoleBindings)
	}
	// And it must not appear for dev.
	_, dev, err := ts.RBACSummary(context.Background(), nil, RBACSummaryInput{Namespace: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.ClusterRoleBindings) != 0 {
		t.Errorf("dev clusterRoleBindings = %+v, want none", dev.ClusterRoleBindings)
	}
}

func TestServiceAccountAccess(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ServiceAccountAccess(context.Background(), nil, ServiceAccountAccessInput{
		Namespace: "prod", Name: "app-sa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Access) != 2 {
		t.Fatalf("access = %+v, want direct RoleBinding + group CRB", out.Access)
	}
	bySource := map[string][]PolicyRuleInfo{}
	for _, a := range out.Access {
		bySource[a.Source] = a.Rules
	}
	direct := bySource["RoleBinding/reader-binding -> Role/reader"]
	if len(direct) != 1 || direct[0].Resources[0] != "pods" {
		t.Errorf("direct access = %+v (sources: %v)", direct, bySource)
	}
	group := bySource["ClusterRoleBinding/all-prod-sa-widget-view -> ClusterRole/widget-viewer (via Group:system:serviceaccounts:prod)"]
	if len(group) != 1 || group[0].Resources[0] != "widgets" {
		t.Errorf("group access = %+v (sources: %v)", group, bySource)
	}
}

func TestServiceAccountAccessUnknownSA(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, _, err := ts.ServiceAccountAccess(context.Background(), nil, ServiceAccountAccessInput{
		Namespace: "prod", Name: "nope",
	})
	if err == nil {
		t.Fatal("expected an error for a missing ServiceAccount")
	}
}

func TestStorageClasses(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.StorageClasses(context.Background(), nil, StorageClassesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.StorageClasses) != 1 {
		t.Fatalf("storageClasses = %+v", out.StorageClasses)
	}
	sc := out.StorageClasses[0]
	if sc.Name != "gp3" || sc.Provisioner != "ebs.csi.aws.com" || !sc.IsDefault ||
		sc.ReclaimPolicy != "Delete" || sc.VolumeBindingMode != "WaitForFirstConsumer" || !sc.AllowVolumeExpansion {
		t.Errorf("sc = %+v", sc)
	}
}

func TestWebhookConfigs(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.WebhookConfigs(context.Background(), nil, WebhookConfigsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Mutating) != 1 || len(out.Validating) != 1 {
		t.Fatalf("configs = %d/%d mutating/validating, want 1/1", len(out.Mutating), len(out.Validating))
	}
	m := out.Mutating[0].Webhooks[0]
	if m.FailurePolicy != "Fail" || m.TimeoutSeconds != 5 || m.SideEffects != "None" {
		t.Errorf("mutating webhook = %+v", m)
	}
	if m.Service != "mesh-system/injector:443/mutate" {
		t.Errorf("service = %q", m.Service)
	}
	if len(m.Rules) != 1 || m.Rules[0] != "CREATE,UPDATE core/pods" {
		t.Errorf("rules = %v", m.Rules)
	}
	if m.NamespaceSelector != "mesh-injection=enabled" {
		t.Errorf("namespaceSelector = %q", m.NamespaceSelector)
	}
	v := out.Validating[0].Webhooks[0]
	if v.Service != "url (value omitted)" {
		t.Errorf("url-based webhook service = %q, want the URL omitted", v.Service)
	}
}

func TestTopPods(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.TopPods(context.Background(), nil, TopPodsInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pods) != 1 {
		t.Fatalf("pods = %+v", out.Pods)
	}
	p := out.Pods[0]
	if p.Name != "web-1" || p.CPU != "125m" || p.MemoryBytes != 256*1024*1024 {
		t.Errorf("usage = %+v", p)
	}
	if len(p.Containers) != 1 || p.Containers[0].CPU != "125m" {
		t.Errorf("containers = %+v", p.Containers)
	}
	// Configured requests/limits are joined in from the pod spec so usage
	// and configuration compare in one call.
	c := p.Containers[0]
	if c.Requests["cpu"] != "100m" || c.Requests["memory"] != "128Mi" || c.Limits["memory"] != "256Mi" {
		t.Errorf("requests/limits = %v / %v", c.Requests, c.Limits)
	}
	if p.Window != "30s" {
		t.Errorf("window = %q", p.Window)
	}
}

func TestNodeStatus(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.NodeStatus(context.Background(), nil, NodeStatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 {
		t.Fatalf("nodes = %+v", out.Nodes)
	}
	n := out.Nodes[0]
	if n.Name != "node-1" || n.Ready != "True" || n.KubeletVersion != "v1.36.0" {
		t.Errorf("node = %+v", n)
	}
	if len(n.Taints) != 1 || n.Taints[0] != "dedicated=db:NoSchedule" {
		t.Errorf("taints = %v", n.Taints)
	}
	// Usage 500m of 2 CPU = 25%; 2Gi of 4Gi allocatable = 50%.
	if n.UsageCPU != "500m" || n.UsageCPUPercent != 25 || n.UsageMemoryPercent != 50 {
		t.Errorf("usage = %s/%d%% cpu, %s/%d%% memory", n.UsageCPU, n.UsageCPUPercent, n.UsageMemory, n.UsageMemoryPercent)
	}
	if n.Allocatable["memory"] != "4Gi" || n.Capacity["memory"] != "8Gi" {
		t.Errorf("allocatable/capacity = %v / %v", n.Allocatable, n.Capacity)
	}
}

func TestListAPIResources(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ListAPIResources(context.Background(), nil, ListAPIResourcesInput{Group: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// widgets, clusterwidgets, clustergizmos - widgets/status is a
	// subresource and must be skipped.
	if len(out.Resources) != 3 {
		t.Fatalf("resources = %+v, want 3", out.Resources)
	}
	for _, r := range out.Resources {
		if r.Group != "example.com" {
			t.Errorf("group filter leaked %+v", r)
		}
		if r.Kind == "Widget" && (!r.Namespaced || r.Resource != "widgets") {
			t.Errorf("widget = %+v", r)
		}
		if r.Kind == "ClusterWidget" && r.Namespaced {
			t.Errorf("clusterwidget = %+v", r)
		}
	}
	// Unfiltered must include multiple groups.
	_, all, err := ts.ListAPIResources(context.Background(), nil, ListAPIResourcesInput{})
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, r := range all.Resources {
		groups[r.Group] = true
	}
	if !groups["example.com"] || !groups["gateway.networking.k8s.io"] {
		t.Errorf("groups = %v", groups)
	}
}

func TestSecretMetadata(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.SecretMetadata(context.Background(), nil, SecretMetadataInput{Namespace: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Secrets) != 1 {
		t.Fatalf("secrets = %+v", out.Secrets)
	}
	s := out.Secrets[0]
	if s.Name != "db-creds" || s.Type != "Opaque" || s.KeyCount != 2 {
		t.Errorf("secret = %+v", s)
	}
	// Keys sorted; values never present (the leak test proves it globally).
	if len(s.Keys) != 2 || s.Keys[0] != "password" || s.Keys[1] != "token" {
		t.Errorf("keys = %v", s.Keys)
	}
	if s.Age != "1d2h" || len(s.OwnedBy) != 1 || s.OwnedBy[0] != "ExternalSecret/db-creds" {
		t.Errorf("age/owners = %s %v", s.Age, s.OwnedBy)
	}
	// Named lookup for a missing Secret must error (the "does it exist" ask).
	if _, _, err := ts.SecretMetadata(context.Background(), nil, SecretMetadataInput{Namespace: "prod", Name: "nope"}); err == nil {
		t.Error("expected an error for a missing Secret")
	}
}

func TestConfigMapMetadata(t *testing.T) {
	ts := newTestToolset(testConfig())
	_, out, err := ts.ConfigMapMetadata(context.Background(), nil, ConfigMapMetadataInput{Namespace: "prod", Name: "app-config"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ConfigMaps) != 1 {
		t.Fatalf("configMaps = %+v", out.ConfigMaps)
	}
	cm := out.ConfigMaps[0]
	if cm.KeyCount != 2 || cm.Keys[0] != "cert.der" || cm.Keys[1] != "config.ini" {
		t.Errorf("keys = %v", cm.Keys)
	}
}

func TestListNamespacesFallback(t *testing.T) {
	cfg := testConfig()
	cfg.Namespaces = nil
	ts := newTestToolset(cfg)
	_, out, err := ts.ListNamespaces(context.Background(), nil, ListNamespacesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Namespaces) != 0 {
		t.Errorf("namespaces = %v, want empty allowlist echoed", out.Namespaces)
	}
	if len(out.ClusterNamespaces) != 2 {
		t.Fatalf("clusterNamespaces = %+v, want prod and dev", out.ClusterNamespaces)
	}
	byName := map[string]NamespaceInfo{}
	for _, ns := range out.ClusterNamespaces {
		byName[ns.Name] = ns
	}
	prod := byName["prod"]
	if prod.Phase != "Active" || prod.Labels["env"] != "prod" || prod.Age != "1d2h" {
		t.Errorf("prod = %+v", prod)
	}
	if out.Note == "" {
		t.Error("expected an explanatory note")
	}
}

func TestProjectionKeepsElementFieldsAssociated(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"tolerations": []any{
				map[string]any{
					"key": "node.kubernetes.io/not-ready", "operator": "Exists",
					"effect": "NoExecute", "tolerationSeconds": int64(300),
				},
				// No tolerationSeconds: the pair is skipped for this
				// element, not misaligned across elements.
				map[string]any{
					"key": "dedicated", "operator": "Equal", "effect": "NoSchedule",
				},
			},
		},
	}
	extract := func(pathText string) (string, bool) {
		t.Helper()
		path, err := config.ParseFactPath(pathText)
		if err != nil {
			t.Fatal(err)
		}
		if len(path.Projection) > 0 {
			return renderProjection(leavesAt(obj, path.Segments), path.Projection)
		}
		return renderLeaves(leavesAt(obj, path.Segments))
	}

	got, ok := extract(".spec.tolerations[*].{key,effect,tolerationSeconds}")
	want := "{key: node.kubernetes.io/not-ready, effect: NoExecute, tolerationSeconds: 300}, " +
		"{key: dedicated, effect: NoSchedule}"
	if !ok || got != want {
		t.Errorf("projection = %q, want %q", got, want)
	}

	// An entry landing on a non-scalar refuses the whole fact.
	if _, ok := extract(".{spec}"); ok {
		t.Error("projection entry reaching a map must refuse")
	}

	// Projection on a single (non-list) element renders one group.
	single, ok := extract(".spec.tolerations[0].{key,operator}")
	if !ok || single != "{key: node.kubernetes.io/not-ready, operator: Exists}" {
		t.Errorf("single-element projection = %q", single)
	}

	// A missing MIDDLE segment (not just a missing leaf) yields nothing at
	// every level: in the main walk before the projection, deeper in the
	// main walk, and inside a projection entry's own path.
	for _, path := range []string{
		".spec.containers[*].{name,imagePullPolicy}",      // .spec.containers absent
		".spec.nope.tolerations[*].{key,effect}",          // intermediate map absent
		".status.tolerations[*].{key,effect}",             // root branch absent
		".spec.tolerations[*].{nope.deeper,also[*].gone}", // entries' middles absent
	} {
		if got, ok := extract(path); !ok || got != "" {
			t.Errorf("path %s = %q ok=%v, want empty (fact omitted)", path, got, ok)
		}
	}
}

func TestRenderScalarRefusesObjects(t *testing.T) {
	if _, ok := renderScalar(map[string]any{"a": "b"}); ok {
		t.Error("maps must be refused")
	}
	if _, ok := renderScalar([]any{map[string]any{"a": "b"}}); ok {
		t.Error("lists containing maps must be refused")
	}
	if v, ok := renderScalar([]any{"a", int64(2), true}); !ok || v != "a, 2, true" {
		t.Errorf("scalar list = %q ok=%v", v, ok)
	}
	if _, ok := renderScalar(nil); ok {
		t.Error("nil must be refused")
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
