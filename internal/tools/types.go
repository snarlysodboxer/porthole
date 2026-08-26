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
	SecretMetadata     bool `json:"secret_metadata"`
	ConfigMapMetadata  bool `json:"configmap_metadata"`
}

// NamespaceInfo is one Namespace discovered from the API when no allowlist
// is configured.
type NamespaceInfo struct {
	Name      string            `json:"name"`
	Phase     string            `json:"phase,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Age       string            `json:"age,omitempty"`
	CreatedAt string            `json:"createdAt,omitempty"`
}

// ListNamespacesOutput answers "what can I look at?".
type ListNamespacesOutput struct {
	// Namespaces is the configured allowlist. Empty means the server
	// itself does not restrict namespaces; RBAC is the boundary.
	Namespaces []string     `json:"namespaces"`
	Features   FeatureFlags `json:"features"`
	// ClusterNamespaces lists real Namespaces from the API when no
	// allowlist is configured (needs a cluster-scoped grant; omitted
	// without one).
	ClusterNamespaces []NamespaceInfo `json:"clusterNamespaces,omitempty"`
	Note              string          `json:"note,omitempty"`
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
	// deployment.kubernetes.io/revision annotation (Deployments only) -
	// a hardcoded allowlisted annotation.
	RolloutRevision string `json:"rolloutRevision,omitempty"`
	// Annotations carries only operator-allowlisted annotation keys.
	Annotations map[string]string `json:"annotations,omitempty"`
	Pods        []PodSummary      `json:"pods,omitempty"`
	// PodDisruptionBudgets are the PDBs whose selectors match this
	// workload's pods - answers "why won't this drain?".
	PodDisruptionBudgets []PDBInfo `json:"podDisruptionBudgets,omitempty"`
}

// PDBInfo is one PodDisruptionBudget's live status.
type PDBInfo struct {
	Name               string      `json:"name"`
	MinAvailable       string      `json:"minAvailable,omitempty"`
	MaxUnavailable     string      `json:"maxUnavailable,omitempty"`
	DisruptionsAllowed int32       `json:"disruptionsAllowed"`
	CurrentHealthy     int32       `json:"currentHealthy"`
	DesiredHealthy     int32       `json:"desiredHealthy"`
	ExpectedPods       int32       `json:"expectedPods"`
	Conditions         []Condition `json:"conditions,omitempty"`
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

// EnvVarRef names an environment variable and where it comes from -
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

// VolumeInfo names a pod volume and its type - never its contents.
type VolumeInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// SourceName is the referenced object's name for persistentVolumeClaim,
	// configMap, and secret volumes.
	SourceName string `json:"sourceName,omitempty"`
}

// PodStatusOutput is a safe "kubectl describe pod".
type PodStatusOutput struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	// Annotations carries only operator-allowlisted annotation keys.
	Annotations     map[string]string `json:"annotations,omitempty"`
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
	// (ebs.csi.aws.com)" or "nfs" - never its parameters or attributes.
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

// Fact is one view-extracted name/value pair. Values are always scalar
// strings - the extractor refuses maps and objects by construction.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ResourceConditionsOutput is the generic CRD escape hatch.
type ResourceConditionsOutput struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
	// Annotations carries only operator-allowlisted annotation keys.
	Annotations map[string]string `json:"annotations,omitempty"`
	Conditions  []Condition       `json:"conditions,omitempty"`
	// Details are facts extracted by the operator-configured view matching
	// this kind, e.g. a Certificate's notAfter.
	Details         []Fact      `json:"details,omitempty"`
	Events          []EventInfo `json:"events,omitempty"`
	TruncatedEvents int         `json:"truncatedEvents,omitempty"`
}

// ResourceStatusOutput carries the full .status of an arbitrary kind. The
// Status field is the single sanctioned unstructured passthrough in this
// package.
type ResourceStatusOutput struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	// Annotations carries only operator-allowlisted annotation keys.
	Annotations     map[string]string `json:"annotations,omitempty"`
	CreatedAt       string            `json:"createdAt,omitempty"`
	Status          map[string]any    `json:"status,omitempty"`
	Events          []EventInfo       `json:"events,omitempty"`
	TruncatedEvents int               `json:"truncatedEvents,omitempty"`
}

// NetworkPolicyRule summarizes one ingress or egress rule. Empty Peers or
// Ports means the rule does not restrict that dimension.
type NetworkPolicyRule struct {
	// Peers renders each from/to peer, e.g. "pods(app=web)",
	// "namespaces(team=a)", "pods(app=web) in namespaces(team=a)",
	// "ipBlock(10.0.0.0/8 except 10.0.1.0/24)".
	Peers []string `json:"peers,omitempty"`
	// Ports renders as "8080/TCP", "http/TCP", or "8080-8090/TCP".
	Ports []string `json:"ports,omitempty"`
}

// NetworkPolicyInfo is one NetworkPolicy.
type NetworkPolicyInfo struct {
	Name string `json:"name"`
	// PodSelector selects the pods the policy applies to; "(all pods)"
	// when empty.
	PodSelector string              `json:"podSelector"`
	PolicyTypes []string            `json:"policyTypes,omitempty"`
	Ingress     []NetworkPolicyRule `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRule `json:"egress,omitempty"`
}

// NetworkPoliciesOutput answers "why can't X reach Y?".
type NetworkPoliciesOutput struct {
	Namespace string              `json:"namespace"`
	Policies  []NetworkPolicyInfo `json:"policies"`
}

// ServiceAccountInfo is one ServiceAccount; secret references are names
// only.
type ServiceAccountInfo struct {
	Name string `json:"name"`
	// Automount reflects automountServiceAccountToken (absent = default).
	Automount        *bool    `json:"automountServiceAccountToken,omitempty"`
	Secrets          []string `json:"secrets,omitempty"`
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
	CreatedAt        string   `json:"createdAt,omitempty"`
}

// PolicyRuleInfo is one shaped RBAC rule.
type PolicyRuleInfo struct {
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
	Verbs           []string `json:"verbs"`
}

// RoleInfo is one Role with its rules.
type RoleInfo struct {
	Name  string           `json:"name"`
	Rules []PolicyRuleInfo `json:"rules,omitempty"`
}

// BindingInfo is one RoleBinding or ClusterRoleBinding.
type BindingInfo struct {
	Name string `json:"name"`
	// RoleRef is "Role/<name>" or "ClusterRole/<name>".
	RoleRef string `json:"roleRef"`
	// Subjects render as "ServiceAccount:<ns>/<name>", "User:<name>", or
	// "Group:<name>".
	Subjects []string `json:"subjects,omitempty"`
	// RoleRefRules resolves a referenced ClusterRole's rules when the
	// server has the cluster-scoped grant (Role refs: see the roles list).
	RoleRefRules []PolicyRuleInfo `json:"roleRefRules,omitempty"`
}

// RBACSummaryOutput answers "what can this ServiceAccount do / why 403?".
type RBACSummaryOutput struct {
	Namespace       string               `json:"namespace"`
	ServiceAccounts []ServiceAccountInfo `json:"serviceAccounts"`
	Roles           []RoleInfo           `json:"roles"`
	RoleBindings    []BindingInfo        `json:"roleBindings"`
	// ClusterRoleBindings lists cluster-wide bindings whose subjects
	// include a ServiceAccount in this namespace or a group covering it.
	// Needs the cluster-scoped grant; omitted without it.
	ClusterRoleBindings []BindingInfo `json:"clusterRoleBindings,omitempty"`
	Errors              []string      `json:"errors,omitempty"`
}

// AccessRule is one binding's contribution to a ServiceAccount's effective
// access.
type AccessRule struct {
	// Source is the provenance, e.g. "RoleBinding/ci -> Role/deployer" or
	// "ClusterRoleBinding/view-all -> ClusterRole/view (via Group:system:serviceaccounts)".
	Source string           `json:"source"`
	Rules  []PolicyRuleInfo `json:"rules,omitempty"`
	// Unresolved notes a roleRef whose rules the server lacked RBAC to read.
	Unresolved string `json:"unresolved,omitempty"`
}

// ServiceAccountAccessOutput is the effective rule list for one
// ServiceAccount.
type ServiceAccountAccessOutput struct {
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Access    []AccessRule `json:"access"`
	Errors    []string     `json:"errors,omitempty"`
}

// StorageClassInfo is one StorageClass. Parameters are deliberately
// omitted (they can reference endpoints and secret names).
type StorageClassInfo struct {
	Name                 string `json:"name"`
	Provisioner          string `json:"provisioner"`
	ReclaimPolicy        string `json:"reclaimPolicy,omitempty"`
	VolumeBindingMode    string `json:"volumeBindingMode,omitempty"`
	AllowVolumeExpansion bool   `json:"allowVolumeExpansion"`
	// IsDefault comes from the storageclass.kubernetes.io/is-default-class
	// annotation - a hardcoded allowlisted annotation.
	IsDefault bool `json:"isDefault"`
}

// StorageClassesOutput lists the cluster's StorageClasses.
type StorageClassesOutput struct {
	StorageClasses []StorageClassInfo `json:"storageClasses"`
}

// WebhookInfo is one webhook within a configuration. The caBundle is
// omitted by construction.
type WebhookInfo struct {
	Name           string `json:"name"`
	FailurePolicy  string `json:"failurePolicy,omitempty"`
	TimeoutSeconds int32  `json:"timeoutSeconds,omitempty"`
	SideEffects    string `json:"sideEffects,omitempty"`
	// Service is the backing service as "<ns>/<name>:<port><path>", or
	// "url (value omitted)" for URL-based webhooks.
	Service string `json:"service,omitempty"`
	// Rules render as "CREATE,UPDATE apps/deployments".
	Rules             []string `json:"rules,omitempty"`
	NamespaceSelector string   `json:"namespaceSelector,omitempty"`
	ObjectSelector    string   `json:"objectSelector,omitempty"`
}

// WebhookConfigInfo is one Mutating/ValidatingWebhookConfiguration.
type WebhookConfigInfo struct {
	Name     string        `json:"name"`
	Webhooks []WebhookInfo `json:"webhooks,omitempty"`
}

// WebhookConfigsOutput answers "why does every apply hang or fail?".
type WebhookConfigsOutput struct {
	Mutating   []WebhookConfigInfo `json:"mutating"`
	Validating []WebhookConfigInfo `json:"validating"`
	Errors     []string            `json:"errors,omitempty"`
}

// ContainerUsage is one container's point-in-time usage, alongside its
// configured requests and limits so the AI can compare them in one call.
type ContainerUsage struct {
	Name string `json:"name"`
	// CPU is millicores, e.g. "125m".
	CPU string `json:"cpu"`
	// Memory is a human quantity, e.g. "256Mi"; MemoryBytes the raw value.
	Memory      string `json:"memory"`
	MemoryBytes int64  `json:"memoryBytes"`
	// Requests and Limits come from the pod spec (needs pod read RBAC;
	// omitted without it).
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// PodUsage is one pod's point-in-time usage from metrics.k8s.io.
type PodUsage struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	CPU         string           `json:"cpu"`
	Memory      string           `json:"memory"`
	MemoryBytes int64            `json:"memoryBytes"`
	Containers  []ContainerUsage `json:"containers,omitempty"`
	Window      string           `json:"window,omitempty"`
	Timestamp   string           `json:"timestamp,omitempty"`
}

// TopPodsOutput is the "kubectl top pods" equivalent - the right-now
// numbers, complementing TSDB history.
type TopPodsOutput struct {
	Pods   []PodUsage `json:"pods"`
	Errors []string   `json:"errors,omitempty"`
}

// NodeInfo is one node's conditions, capacity, and point-in-time usage.
type NodeInfo struct {
	Name string `json:"name"`
	// Ready is the Ready condition's status: True, False, or Unknown.
	Ready          string      `json:"ready"`
	Unschedulable  bool        `json:"unschedulable,omitempty"`
	KubeletVersion string      `json:"kubeletVersion,omitempty"`
	Taints         []string    `json:"taints,omitempty"` // "key=value:Effect"
	Conditions     []Condition `json:"conditions,omitempty"`
	// Allocatable and Capacity are resource quantities by name.
	Allocatable map[string]string `json:"allocatable,omitempty"`
	Capacity    map[string]string `json:"capacity,omitempty"`
	// Usage comes from metrics.k8s.io; percentages are of allocatable.
	UsageCPU           string `json:"usageCPU,omitempty"`
	UsageCPUPercent    int    `json:"usageCPUPercent,omitempty"`
	UsageMemory        string `json:"usageMemory,omitempty"`
	UsageMemoryPercent int    `json:"usageMemoryPercent,omitempty"`
}

// NodeStatusOutput answers "are the nodes healthy / where is headroom?".
type NodeStatusOutput struct {
	Nodes  []NodeInfo `json:"nodes"`
	Errors []string   `json:"errors,omitempty"`
}

// APIGroupInfo is one served API group and its versions.
type APIGroupInfo struct {
	Group            string   `json:"group"`
	PreferredVersion string   `json:"preferredVersion,omitempty"`
	Versions         []string `json:"versions,omitempty"`
}

// APIResourceInfo is one discovered resource at its preferred version.
type APIResourceInfo struct {
	Group      string `json:"group"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Namespaced bool   `json:"namespaced"`
}

// ListAPIResourcesOutput answers "is CRD X installed, at what version?".
type ListAPIResourcesOutput struct {
	Groups    []APIGroupInfo    `json:"groups,omitempty"`
	Resources []APIResourceInfo `json:"resources"`
}

// SecretMeta is one Secret's existence, type, and key names - values never
// have a field to land in.
type SecretMeta struct {
	Name      string   `json:"name"`
	Type      string   `json:"type,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	KeyCount  int      `json:"keyCount"`
	CreatedAt string   `json:"createdAt,omitempty"`
	Age       string   `json:"age,omitempty"`
	OwnedBy   []string `json:"ownedBy,omitempty"` // "Kind/name"
}

// SecretMetadataOutput answers "does the Secret exist, which keys, how old?".
type SecretMetadataOutput struct {
	Namespace string       `json:"namespace"`
	Secrets   []SecretMeta `json:"secrets"`
}

// ConfigMapMeta is one ConfigMap's existence and key names - never values.
type ConfigMapMeta struct {
	Name      string   `json:"name"`
	Keys      []string `json:"keys,omitempty"`
	KeyCount  int      `json:"keyCount"`
	CreatedAt string   `json:"createdAt,omitempty"`
	Age       string   `json:"age,omitempty"`
	OwnedBy   []string `json:"ownedBy,omitempty"` // "Kind/name"
}

// ConfigMapMetadataOutput answers "does the ConfigMap exist, which keys?".
type ConfigMapMetadataOutput struct {
	Namespace  string          `json:"namespace"`
	ConfigMaps []ConfigMapMeta `json:"configMaps"`
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
