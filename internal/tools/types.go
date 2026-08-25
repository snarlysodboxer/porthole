package tools

// Response types are the security policy: every field an AI client can see
// is enumerated here.
//
// All timestamps are RFC 3339 strings.

// Condition is a normalized status condition from any resource.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// EventInfo is a shaped Kubernetes event.
type EventInfo struct {
	Type      string `json:"type,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Count     int32  `json:"count,omitempty"`
	FirstSeen string `json:"firstSeen,omitempty"`
	LastSeen  string `json:"lastSeen,omitempty"`
	// Object is the involved object as "Kind/name".
	Object string `json:"object,omitempty"`
	// Source is the reporting controller.
	Source string `json:"source,omitempty"`
}

// FeatureFlags reports which optional tools this server has enabled.
type FeatureFlags struct {
	ResourceConditions bool `json:"resource_conditions"`
	ResourceStatus     bool `json:"resource_status"`
	PodLogs            bool `json:"pod_logs"`
}

// ListNamespacesOutput answers "what can I look at?".
type ListNamespacesOutput struct {
	// Namespaces is the configured allowlist. Empty means the server
	// itself does not restrict namespaces; RBAC is the boundary.
	Namespaces []string     `json:"namespaces"`
	Features   FeatureFlags `json:"features"`
	Note       string       `json:"note,omitempty"`
}

// WorkloadSummary is one Deployment/StatefulSet/DaemonSet.
type WorkloadSummary struct {
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	Kind              string      `json:"kind"`
	DesiredReplicas   int32       `json:"desiredReplicas"`
	ReadyReplicas     int32       `json:"readyReplicas"`
	UpdatedReplicas   int32       `json:"updatedReplicas"`
	AvailableReplicas int32       `json:"availableReplicas"`
	Conditions        []Condition `json:"conditions,omitempty"`
}

// ListWorkloadsOutput lists workloads with rollout health at a glance.
type ListWorkloadsOutput struct {
	Workloads []WorkloadSummary `json:"workloads"`
	// Errors carries per-namespace failures (e.g. RBAC denials) when
	// fanning out over the allowlist, without failing the whole call.
	Errors []string `json:"errors,omitempty"`
}

// WorkloadStatusOutput is a deep look at one workload's rollout.
type WorkloadStatusOutput struct {
	WorkloadSummary
	Generation         int64 `json:"generation"`
	ObservedGeneration int64 `json:"observedGeneration"`
	// RolloutRevision comes from the controller-written
	// deployment.kubernetes.io/revision annotation (Deployments only) —
	// the single allowlisted annotation.
	RolloutRevision string       `json:"rolloutRevision,omitempty"`
	Pods            []PodSummary `json:"pods,omitempty"`
}

// ContainerState is one container's current or last state.
type ContainerState struct {
	// State is running, waiting, or terminated.
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message,omitempty"`
	ExitCode   *int32 `json:"exitCode,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// ContainerBrief is per-container state for pod listings.
type ContainerBrief struct {
	Name         string          `json:"name"`
	Ready        bool            `json:"ready"`
	RestartCount int32           `json:"restartCount"`
	State        ContainerState  `json:"state"`
	LastState    *ContainerState `json:"lastState,omitempty"`
}

// PodSummary is one pod in a listing.
type PodSummary struct {
	Name       string           `json:"name"`
	Namespace  string           `json:"namespace"`
	Phase      string           `json:"phase"`
	Ready      string           `json:"ready"` // "readyContainers/totalContainers"
	Restarts   int32            `json:"restarts"`
	Age        string           `json:"age,omitempty"`
	CreatedAt  string           `json:"createdAt,omitempty"`
	Node       string           `json:"node,omitempty"`
	Containers []ContainerBrief `json:"containers,omitempty"`
}

// ListPodsOutput lists pods.
type ListPodsOutput struct {
	Pods   []PodSummary `json:"pods"`
	Errors []string     `json:"errors,omitempty"`
}

// EnvVarRef names an environment variable and where it comes from —
// never its value.
type EnvVarRef struct {
	Name string `json:"name"`
	// Source is "literal (value omitted)", "secretKeyRef:<name>/<key>",
	// "configMapKeyRef:<name>/<key>", "fieldRef:<path>", or
	// "resourceFieldRef:<resource>".
	Source string `json:"source"`
}

// ContainerDetail is a container in pod_status.
type ContainerDetail struct {
	Name         string          `json:"name"`
	Image        string          `json:"image"`
	Ready        bool            `json:"ready"`
	RestartCount int32           `json:"restartCount"`
	State        ContainerState  `json:"state"`
	LastState    *ContainerState `json:"lastState,omitempty"`
	// Env lists variable names and reference sources only, never values.
	Env []EnvVarRef `json:"env,omitempty"`
	// EnvFrom lists bulk sources as "secretRef:<name>" /
	// "configMapRef:<name>" (contents never shown).
	EnvFrom  []string          `json:"envFrom,omitempty"`
	Ports    []string          `json:"ports,omitempty"` // "8080/TCP (name)"
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// VolumeInfo names a pod volume and its type — never its contents.
type VolumeInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// SourceName is the referenced object's name for persistentVolumeClaim,
	// configMap, and secret volumes.
	SourceName string `json:"sourceName,omitempty"`
}

// PodStatusOutput is a safe "kubectl describe pod".
type PodStatusOutput struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Labels          map[string]string `json:"labels,omitempty"`
	Phase           string            `json:"phase"`
	Reason          string            `json:"reason,omitempty"`
	Message         string            `json:"message,omitempty"`
	QOSClass        string            `json:"qosClass,omitempty"`
	Node            string            `json:"node,omitempty"`
	CreatedAt       string            `json:"createdAt,omitempty"`
	StartedAt       string            `json:"startedAt,omitempty"`
	Conditions      []Condition       `json:"conditions,omitempty"`
	InitContainers  []ContainerDetail `json:"initContainers,omitempty"`
	Containers      []ContainerDetail `json:"containers,omitempty"`
	Volumes         []VolumeInfo      `json:"volumes,omitempty"`
	Events          []EventInfo       `json:"events,omitempty"`
	TruncatedEvents int               `json:"truncatedEvents,omitempty"`
}

// EventsOutput lists events, most recent last.
type EventsOutput struct {
	Events []EventInfo `json:"events"`
	// TruncatedCount is how many older events were dropped to fit the
	// server's ceiling.
	TruncatedCount int      `json:"truncatedCount,omitempty"`
	Note           string   `json:"note,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// ServicePortInfo is one service port.
type ServicePortInfo struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
	NodePort   int32  `json:"nodePort,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// ServiceEndpointsOutput answers "why is nothing answering?".
type ServiceEndpointsOutput struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Type              string            `json:"type"`
	Selector          map[string]string `json:"selector,omitempty"`
	Ports             []ServicePortInfo `json:"ports,omitempty"`
	ReadyEndpoints    int               `json:"readyEndpoints"`
	NotReadyEndpoints int               `json:"notReadyEndpoints"`
}

// PVCInfo is one PersistentVolumeClaim.
type PVCInfo struct {
	Name          string   `json:"name"`
	Phase         string   `json:"phase"`
	RequestedSize string   `json:"requestedSize,omitempty"`
	Capacity      string   `json:"capacity,omitempty"`
	StorageClass  string   `json:"storageClass,omitempty"`
	VolumeName    string   `json:"volumeName,omitempty"`
	AccessModes   []string `json:"accessModes,omitempty"`
	// Volume is the bound PersistentVolume's detail, when the server has
	// RBAC to read PersistentVolumes (cluster-scoped).
	Volume *PVInfo `json:"volume,omitempty"`
}

// PVInfo is a bound or namespace-referencing PersistentVolume.
type PVInfo struct {
	Name          string   `json:"name"`
	Phase         string   `json:"phase"`
	Capacity      string   `json:"capacity,omitempty"`
	StorageClass  string   `json:"storageClass,omitempty"`
	ReclaimPolicy string   `json:"reclaimPolicy,omitempty"`
	VolumeMode    string   `json:"volumeMode,omitempty"`
	AccessModes   []string `json:"accessModes,omitempty"`
	// ClaimRef is the claiming PVC as "namespace/name".
	ClaimRef string `json:"claimRef,omitempty"`
	// Source names which volume source backs the PV, e.g. "csi
	// (ebs.csi.aws.com)" or "nfs" — never its parameters or attributes.
	Source string `json:"source,omitempty"`
	// VolumeHandle is the CSI backend volume ID (e.g. an EBS volume id).
	VolumeHandle string `json:"volumeHandle,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
}

// PVCStatusOutput lists PVCs in a namespace, enriched with their bound
// PersistentVolumes.
type PVCStatusOutput struct {
	Namespace string    `json:"namespace"`
	PVCs      []PVCInfo `json:"pvcs"`
	// UnboundVolumes are PersistentVolumes whose claimRef points into this
	// namespace but are not Bound (Released/Failed orphans, or Available
	// volumes reserved for a claim).
	UnboundVolumes []PVInfo `json:"unboundVolumes,omitempty"`
	// Errors notes partial failures, e.g. missing RBAC to read
	// PersistentVolumes.
	Errors []string `json:"errors,omitempty"`
}

// JobInfo is one Job.
type JobInfo struct {
	Name           string      `json:"name"`
	OwnedBy        string      `json:"ownedBy,omitempty"` // "CronJob/<name>"
	Active         int32       `json:"active"`
	Succeeded      int32       `json:"succeeded"`
	Failed         int32       `json:"failed"`
	StartTime      string      `json:"startTime,omitempty"`
	CompletionTime string      `json:"completionTime,omitempty"`
	Conditions     []Condition `json:"conditions,omitempty"`
}

// CronJobInfo is one CronJob.
type CronJobInfo struct {
	Name               string `json:"name"`
	Schedule           string `json:"schedule"`
	Suspend            bool   `json:"suspend"`
	LastScheduleTime   string `json:"lastScheduleTime,omitempty"`
	LastSuccessfulTime string `json:"lastSuccessfulTime,omitempty"`
	ActiveCount        int    `json:"activeCount"`
}

// JobStatusOutput answers "did the job/cron run?".
type JobStatusOutput struct {
	Namespace string        `json:"namespace"`
	Jobs      []JobInfo     `json:"jobs"`
	CronJobs  []CronJobInfo `json:"cronJobs"`
}

// ResourceConditionsOutput is the generic CRD escape hatch.
type ResourceConditionsOutput struct {
	APIVersion      string      `json:"apiVersion"`
	Kind            string      `json:"kind"`
	Name            string      `json:"name"`
	Namespace       string      `json:"namespace"`
	Conditions      []Condition `json:"conditions,omitempty"`
	Events          []EventInfo `json:"events,omitempty"`
	TruncatedEvents int         `json:"truncatedEvents,omitempty"`
}

// ResourceStatusOutput carries the full .status of an arbitrary kind. The
// Status field is the single sanctioned unstructured passthrough in this
// package.
type ResourceStatusOutput struct {
	APIVersion      string            `json:"apiVersion"`
	Kind            string            `json:"kind"`
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Labels          map[string]string `json:"labels,omitempty"`
	CreatedAt       string            `json:"createdAt,omitempty"`
	Status          map[string]any    `json:"status,omitempty"`
	Events          []EventInfo       `json:"events,omitempty"`
	TruncatedEvents int               `json:"truncatedEvents,omitempty"`
}

// PodLogsOutput returns (optionally grep-filtered) log lines.
type PodLogsOutput struct {
	Lines []string `json:"lines"`
	// MatchedLines is how many lines matched the grep filter (may exceed
	// len(Lines) if output was truncated).
	MatchedLines int    `json:"matchedLines,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	Note         string `json:"note,omitempty"`
}
