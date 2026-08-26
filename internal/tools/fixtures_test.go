package tools

// Adversarial fixtures: every object carries sentinel strings in fields the
// server must never serialize (env values, annotations, ConfigMap/Secret
// data, CR specs, managedFields). The leak test asserts no sentinel ever
// appears in any tool response.

import (
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	eventsv1 "k8s.io/api/events/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/snarlysodboxer/porthole/internal/config"
	"github.com/snarlysodboxer/porthole/internal/kube"
)

const (
	leakEnvValue      = "SENTINEL_ENV_VALUE_LEAK"
	leakLastApplied   = "SENTINEL_LAST_APPLIED_LEAK"
	leakAnnotation    = "SENTINEL_ANNOTATION_LEAK"
	leakConfigMapData = "SENTINEL_CONFIGMAP_DATA_LEAK"
	leakSecretData    = "SENTINEL_SECRET_DATA_LEAK"
	leakCRSpec        = "SENTINEL_CR_SPEC_LEAK"
	leakManagedFields = "SENTINEL_MANAGED_FIELDS_LEAK"
	leakCommandArg    = "SENTINEL_COMMAND_ARG_LEAK"
	leakVolumeAttr    = "SENTINEL_VOLUME_ATTRIBUTE_LEAK"
	leakSCParameter   = "SENTINEL_STORAGECLASS_PARAM_LEAK"
	leakWebhookURL    = "SENTINEL_WEBHOOK_URL_LEAK"
	// leakStatusSecret sits in a CR's .status under a secret-looking key:
	// the resource_status redaction pass must catch it.
	leakStatusSecret = "SENTINEL_STATUS_SECRET_LEAK"
)

var sentinels = []string{
	leakEnvValue, leakLastApplied, leakAnnotation, leakConfigMapData,
	leakSecretData, leakCRSpec, leakManagedFields, leakCommandArg,
	leakVolumeAttr, leakSCParameter, leakWebhookURL, leakStatusSecret,
}

// allowedAnnotationKey is benign and allowlisted in testConfig, proving the
// allowlist lets exactly the listed keys through.
const allowedAnnotationKey = "team.example.com/oncall"

// fixedNow keeps age strings deterministic.
var fixedNow = time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

func mustQuantity(s string) resource.Quantity {
	return resource.MustParse(s)
}

func adversarialAnnotations() map[string]string {
	return map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": leakLastApplied,
		"internal.example.com/note":                        leakAnnotation,
		"deployment.kubernetes.io/revision":                "7",
		allowedAnnotationKey:                               "team-alpha",
	}
}

func adversarialPodSpec() corev1.PodSpec {
	return corev1.PodSpec{
		NodeName: "node-1",
		Containers: []corev1.Container{{
			Name:    "app",
			Image:   "registry.example.com/app:v1",
			Command: []string{"/bin/app", "--password=" + leakCommandArg},
			Args:    []string{leakCommandArg},
			Env: []corev1.EnvVar{
				{Name: "PLAIN", Value: leakEnvValue},
				{Name: "FROM_SECRET", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "db-creds"},
						Key:                  "password",
					},
				}},
				{Name: "FROM_CM", ValueFrom: &corev1.EnvVarSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
						Key:                  "mode",
					},
				}},
			},
			EnvFrom: []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "bulk-secret"}}},
			},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: mustQuantity("100m"), corev1.ResourceMemory: mustQuantity("128Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: mustQuantity("256Mi")},
			},
		}},
		Volumes: []corev1.Volume{
			{Name: "data", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-pvc"},
			}},
			{Name: "creds", VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "db-creds"},
			}},
			{Name: "conf", VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}},
			}},
			{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		},
	}
}

func fixtureObjects() []runtime.Object {
	created := metav1.NewTime(fixedNow.Add(-26 * time.Hour))
	replicas := int32(3)
	exitCode137 := int32(137)
	suspend := false

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "prod",
			Labels:            map[string]string{"app": "web"},
			Annotations:       adversarialAnnotations(),
			CreationTimestamp: created,
			Generation:        4,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}, Annotations: adversarialAnnotations()},
				Spec:       adversarialPodSpec(),
			},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 3,
			Replicas:           3, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2,
			Conditions: []appsv1.DeploymentCondition{{
				Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
				Reason: "ProgressDeadlineExceeded", Message: `ReplicaSet "web-6b9" has timed out progressing.`,
				LastTransitionTime: created,
			}},
		},
	}

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db", Namespace: "prod", Annotations: adversarialAnnotations(), CreationTimestamp: created,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{Spec: adversarialPodSpec()},
		},
		Status: appsv1.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3},
	}

	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "agent", Namespace: "dev", Annotations: adversarialAnnotations(), CreationTimestamp: created,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "agent"}},
			Template: corev1.PodTemplateSpec{Spec: adversarialPodSpec()},
		},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 5, NumberReady: 4, UpdatedNumberScheduled: 5, NumberAvailable: 4},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-1", Namespace: "prod",
			Labels:            map[string]string{"app": "web"},
			Annotations:       adversarialAnnotations(),
			CreationTimestamp: metav1.NewTime(fixedNow.Add(-90 * time.Minute)),
		},
		Spec: adversarialPodSpec(),
		Status: corev1.PodStatus{
			Phase:    corev1.PodRunning,
			QOSClass: corev1.PodQOSBurstable,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionFalse,
				Reason: "ContainersNotReady", Message: "containers with unready status: [app]",
			}},
			StartTime: &created,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: false, RestartCount: 12,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CrashLoopBackOff",
					Message: "back-off 5m0s restarting failed container=app",
				}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					ExitCode: exitCode137, Reason: "OOMKilled",
					FinishedAt: metav1.NewTime(fixedNow.Add(-10 * time.Minute)),
				}},
			}},
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-config", Namespace: "prod",
			Annotations:       adversarialAnnotations(),
			CreationTimestamp: created,
			OwnerReferences:   []metav1.OwnerReference{{Kind: "HelmRelease", Name: "app"}},
		},
		Data:       map[string]string{"config.ini": leakConfigMapData},
		BinaryData: map[string][]byte{"cert.der": []byte(leakConfigMapData)},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "db-creds", Namespace: "prod",
			Annotations:       adversarialAnnotations(),
			CreationTimestamp: created,
			OwnerReferences:   []metav1.OwnerReference{{Kind: "ExternalSecret", Name: "db-creds"}},
		},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte(leakSecretData)},
		StringData: map[string]string{"token": leakSecretData},
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", Annotations: adversarialAnnotations()},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "web"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	ready, notReady := true, false
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "prod",
			Labels:      map[string]string{discoveryv1.LabelServiceName: "web"},
			Annotations: adversarialAnnotations(),
		},
		Endpoints: []discoveryv1.Endpoint{
			{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{Conditions: discoveryv1.EndpointConditions{Ready: &notReady}},
		},
	}

	storageClass := "gp3"
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-pvc", Namespace: "prod", Annotations: adversarialAnnotations()},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClass,
			VolumeName:       "pv-123",
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: mustQuantity("10Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    corev1.ClaimBound,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: mustQuantity("10Gi")},
		},
	}

	volumeModeFS := corev1.PersistentVolumeFilesystem
	boundPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-123", Annotations: adversarialAnnotations()},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: mustQuantity("10Gi")},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "gp3",
			VolumeMode:                    &volumeModeFS,
			ClaimRef:                      &corev1.ObjectReference{Namespace: "prod", Name: "data-pvc"},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "ebs.csi.aws.com",
					VolumeHandle: "vol-0abc123",
					VolumeAttributes: map[string]string{
						"secretParam": leakVolumeAttr,
					},
					NodeStageSecretRef: &corev1.SecretReference{Name: "csi-creds", Namespace: "kube-system"},
				},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	releasedPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-old", Annotations: adversarialAnnotations()},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: mustQuantity("5Gi")},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			StorageClassName:              "gp3",
			ClaimRef:                      &corev1.ObjectReference{Namespace: "prod", Name: "old-pvc"},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				NFS: &corev1.NFSVolumeSource{Server: "nfs.internal.example.com", Path: "/exports/old"},
			},
		},
		Status: corev1.PersistentVolumeStatus{
			Phase:  corev1.VolumeReleased,
			Reason: "Released",
		},
	}

	startTime := metav1.NewTime(fixedNow.Add(-2 * time.Hour))
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backup-29100", Namespace: "prod",
			Annotations:     adversarialAnnotations(),
			OwnerReferences: []metav1.OwnerReference{{Kind: "CronJob", Name: "backup"}},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{Spec: adversarialPodSpec()},
		},
		Status: batchv1.JobStatus{
			Failed: 2, StartTime: &startTime,
			Conditions: []batchv1.JobCondition{{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
				Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit",
			}},
		},
	}
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "prod", Annotations: adversarialAnnotations()},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 3 * * *",
			Suspend:  &suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: adversarialPodSpec()}},
			},
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &startTime,
			Active:           []corev1.ObjectReference{{Kind: "Job", Name: "backup-29100"}},
		},
	}

	podEvent := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1.ev1", Namespace: "prod", Annotations: adversarialAnnotations()},
		Regarding:  corev1.ObjectReference{Kind: "Pod", Name: "web-1", Namespace: "prod"},
		Type:       "Warning", Reason: "BackOff",
		Note:                     `Back-off pulling image "registry.example.com/app:v1"`,
		ReportingController:      "kubelet",
		DeprecatedCount:          14,
		DeprecatedFirstTimestamp: metav1.NewTime(fixedNow.Add(-50 * time.Minute)),
		DeprecatedLastTimestamp:  metav1.NewTime(fixedNow.Add(-2 * time.Minute)),
	}
	oldEvent := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1.ev2", Namespace: "prod"},
		Regarding:  corev1.ObjectReference{Kind: "Pod", Name: "web-1", Namespace: "prod"},
		Type:       "Normal", Reason: "Scheduled",
		Note:                    "Successfully assigned prod/web-1 to node-1",
		ReportingController:     "default-scheduler",
		DeprecatedLastTimestamp: metav1.NewTime(fixedNow.Add(-49 * time.Minute)),
	}
	deployEvent := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "web.ev1", Namespace: "prod"},
		Regarding:  corev1.ObjectReference{Kind: "Deployment", Name: "web", Namespace: "prod"},
		Type:       "Normal", Reason: "ScalingReplicaSet",
		Note:                    "Scaled up replica set web-6b9 to 3",
		ReportingController:     "deployment-controller",
		DeprecatedLastTimestamp: metav1.NewTime(fixedNow.Add(-30 * time.Minute)),
	}
	widgetEvent := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "widget-1.ev1", Namespace: "prod"},
		Regarding:  corev1.ObjectReference{Kind: "Widget", Name: "widget-1", Namespace: "prod"},
		Type:       "Normal", Reason: "Reconciled",
		Note:                    "Widget reconciled",
		ReportingController:     "widget-controller",
		DeprecatedLastTimestamp: metav1.NewTime(fixedNow.Add(-1 * time.Minute)),
	}
	// Events about cluster-scoped objects land in a namespace the
	// controller chose; the fan-out lookup must find this one in prod.
	clusterWidgetEvent := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "cw-1.ev1", Namespace: "prod"},
		Regarding:  corev1.ObjectReference{Kind: "ClusterWidget", Name: "cw-1"},
		Type:       "Warning", Reason: "ReconcileFailed",
		Note:                    "cluster widget reconcile failed",
		ReportingController:     "widget-controller",
		DeprecatedLastTimestamp: metav1.NewTime(fixedNow.Add(-3 * time.Minute)),
	}

	protocolTCP := corev1.ProtocolTCP
	port8080 := intstr.FromInt32(8080)
	endPort := int32(8090)
	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-web", Namespace: "prod", Annotations: adversarialAnnotations()},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app": "frontend"}},
						NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}},
					},
					{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.0.1.0/24"}}},
				},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &protocolTCP, Port: &port8080, EndPort: &endPort}},
			}},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{}}},
			}},
		},
	}

	automount := false
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-sa", Namespace: "prod",
			Annotations: adversarialAnnotations(), CreationTimestamp: created,
		},
		AutomountServiceAccountToken: &automount,
		Secrets:                      []corev1.ObjectReference{{Name: "db-creds"}},
		ImagePullSecrets:             []corev1.LocalObjectReference{{Name: "regcred"}},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "prod", Annotations: adversarialAnnotations()},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""}, Resources: []string{"pods"},
			Verbs: []string{"get", "list"}, ResourceNames: []string{"web-1"},
		}},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "reader-binding", Namespace: "prod", Annotations: adversarialAnnotations()},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "app-sa", Namespace: "prod"}},
	}
	clusterRoleRefBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "widget-view-binding", Namespace: "prod"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "widget-viewer"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice"}},
	}
	// A dangling roleRef: the referenced ClusterRole does not exist, which
	// rbac_summary must surface rather than silently omit.
	danglingBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "dangling-binding", Namespace: "prod"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "deleted-role"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "bob"}},
	}
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "widget-viewer", Annotations: adversarialAnnotations()},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"example.com"}, Resources: []string{"widgets"}, Verbs: []string{"get", "list"},
		}},
	}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "all-prod-sa-widget-view", Annotations: adversarialAnnotations()},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "widget-viewer"},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "system:serviceaccounts:prod"},
			{Kind: "ServiceAccount", Name: "other", Namespace: "elsewhere"},
		},
	}

	minAvailable := intstr.FromInt32(2)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "web-pdb", Namespace: "prod", Annotations: adversarialAnnotations()},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 3,
			Conditions: []metav1.Condition{{
				Type: "DisruptionAllowed", Status: metav1.ConditionFalse,
				Reason: "InsufficientPods", LastTransitionTime: created,
			}},
		},
	}

	reclaimDelete := corev1.PersistentVolumeReclaimDelete
	bindingMode := storagev1.VolumeBindingWaitForFirstConsumer
	expansion := true
	scAnnotations := adversarialAnnotations()
	scAnnotations[defaultClassAnnotation] = "true"
	gp3StorageClass := &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: "gp3", Annotations: scAnnotations},
		Provisioner:          "ebs.csi.aws.com",
		ReclaimPolicy:        &reclaimDelete,
		VolumeBindingMode:    &bindingMode,
		AllowVolumeExpansion: &expansion,
		Parameters:           map[string]string{"csi.storage.k8s.io/node-stage-secret-name": leakSCParameter},
	}

	failurePolicy := admissionv1.Fail
	sideEffects := admissionv1.SideEffectClassNone
	webhookTimeout := int32(5)
	webhookPort := int32(443)
	webhookPath := "/mutate"
	mutatingWebhook := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "sidecar-injector", Annotations: adversarialAnnotations()},
		Webhooks: []admissionv1.MutatingWebhook{{
			Name: "inject.example.com",
			ClientConfig: admissionv1.WebhookClientConfig{
				Service:  &admissionv1.ServiceReference{Namespace: "mesh-system", Name: "injector", Port: &webhookPort, Path: &webhookPath},
				CABundle: []byte("ca-bundle-bytes-never-shown"),
			},
			FailurePolicy: &failurePolicy, TimeoutSeconds: &webhookTimeout, SideEffects: &sideEffects,
			Rules: []admissionv1.RuleWithOperations{{
				Operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update},
				Rule:       admissionv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"}},
			}},
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"mesh-injection": "enabled"}},
		}},
	}
	webhookURL := "https://" + leakWebhookURL + "/validate"
	validatingWebhook := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "external-validator", Annotations: adversarialAnnotations()},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name:          "check.example.com",
			ClientConfig:  admissionv1.WebhookClientConfig{URL: &webhookURL, CABundle: []byte("ca")},
			FailurePolicy: &failurePolicy, SideEffects: &sideEffects,
		}},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1", Annotations: adversarialAnnotations(), CreationTimestamp: created},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: "dedicated", Value: "db", Effect: corev1.TaintEffectNoSchedule}},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "KubeletReady"},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse, Reason: "KubeletHasSufficientMemory"},
			},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: mustQuantity("2"), corev1.ResourceMemory: mustQuantity("4Gi")},
			Capacity:    corev1.ResourceList{corev1.ResourceCPU: mustQuantity("2"), corev1.ResourceMemory: mustQuantity("8Gi")},
			NodeInfo:    corev1.NodeSystemInfo{KubeletVersion: "v1.36.0"},
		},
	}

	prodNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod",
			Labels:            map[string]string{"env": "prod"},
			Annotations:       adversarialAnnotations(),
			CreationTimestamp: created,
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	devNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "dev", CreationTimestamp: created},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}

	return []runtime.Object{
		deployment, statefulSet, daemonSet, pod, configMap, secret,
		service, endpointSlice, pvc, boundPV, releasedPV, job, cronJob,
		podEvent, oldEvent, deployEvent, widgetEvent, clusterWidgetEvent,
		networkPolicy, serviceAccount, role, roleBinding, clusterRoleRefBinding,
		danglingBinding, clusterRole, clusterRoleBinding, pdb, gp3StorageClass,
		mutatingWebhook, validatingWebhook, node, prodNamespace, devNamespace,
	}
}

// fixtureMetrics returns metrics.k8s.io objects for the metrics fake.
func fixtureMetrics() []runtime.Object {
	return []runtime.Object{
		&metricsv1beta1.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "prod", Annotations: adversarialAnnotations()},
			Timestamp:  metav1.NewTime(fixedNow.Add(-30 * time.Second)),
			Window:     metav1.Duration{Duration: 30 * time.Second},
			Containers: []metricsv1beta1.ContainerMetrics{{
				Name: "app",
				Usage: corev1.ResourceList{
					corev1.ResourceCPU:    mustQuantity("125m"),
					corev1.ResourceMemory: mustQuantity("256Mi"),
				},
			}},
		},
		&metricsv1beta1.NodeMetrics{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1", Annotations: adversarialAnnotations()},
			Timestamp:  metav1.NewTime(fixedNow.Add(-30 * time.Second)),
			Window:     metav1.Duration{Duration: 30 * time.Second},
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    mustQuantity("500m"),
				corev1.ResourceMemory: mustQuantity("2Gi"),
			},
		},
	}
}

var (
	widgetGVR        = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
	clusterWidgetGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "clusterwidgets"}
)

// fixtureClusterWidget is a cluster-scoped CR, allowlisted in testConfig's
// clusterKinds.
func fixtureClusterWidget() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "ClusterWidget",
		"metadata": map[string]any{
			"name":              "cw-1",
			"creationTimestamp": "2026-08-20T00:00:00Z",
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": leakLastApplied,
				"internal.example.com/note":                        leakAnnotation,
				allowedAnnotationKey:                               "team-alpha",
			},
		},
		"spec": map[string]any{
			"password": leakCRSpec,
		},
		"status": map[string]any{
			"phase": "Ready",
			"conditions": []any{
				map[string]any{
					"type": "Ready", "status": "True",
					"reason": "ReconcileSuccess", "message": "cluster widget is ready",
				},
			},
		},
	}}
}

func fixtureWidget() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name":              "widget-1",
			"namespace":         "prod",
			"labels":            map[string]any{"app": "widget"},
			"creationTimestamp": "2026-08-20T00:00:00Z",
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": leakLastApplied,
				"internal.example.com/note":                        leakAnnotation,
			},
			"managedFields": []any{
				map[string]any{"manager": leakManagedFields},
			},
		},
		"spec": map[string]any{
			"password": leakCRSpec,
		},
		"status": map[string]any{
			"phase": "Ready",
			// Some operators stash secret-looking material in status; the
			// resource_status redaction must catch it.
			"rootPasswordHash": leakStatusSecret,
			"keyCount":         int64(2),
			"conditions": []any{
				map[string]any{
					"type": "Ready", "status": "True",
					"reason": "ReconcileSuccess", "message": "widget is ready",
					"lastTransitionTime": "2026-08-21T10:00:00Z",
				},
			},
		},
	}}
}

// cachedFakeDiscovery adapts the fake to CachedDiscoveryInterface.
type cachedFakeDiscovery struct {
	*discoveryfake.FakeDiscovery
}

func (c *cachedFakeDiscovery) Fresh() bool { return true }
func (c *cachedFakeDiscovery) Invalidate() {}

func fixtureAPIResources() []*metav1.APIResourceList {
	return []*metav1.APIResourceList{
		{
			GroupVersion: "example.com/v1",
			APIResources: []metav1.APIResource{
				{Name: "widgets", Kind: "Widget", Namespaced: true},
				{Name: "widgets/status", Kind: "Widget", Namespaced: true},
				{Name: "clusterwidgets", Kind: "ClusterWidget", Namespaced: false},
				{Name: "clustergizmos", Kind: "ClusterGizmo", Namespaced: false},
			},
		},
		{
			GroupVersion: "gateway.networking.k8s.io/v1",
			APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway", Namespaced: true}},
		},
		{
			GroupVersion: "networking.istio.io/v1",
			APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway", Namespaced: true}},
		},
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Mode:                    "stdio",
		Namespaces:              []string{"prod", "dev"},
		EnableCRDStatus:         true,
		EnableLogs:              true,
		EnableSecretMetadata:    true,
		EnableConfigMapMetadata: true,
		MaxLogBytes:             1024,
		MaxEvents:               50,
		// last-applied is deliberately allowlisted here: the hard denylist
		// must win anyway, and the leak test proves it.
		AllowedAnnotations: []string{allowedAnnotationKey, "kubectl.kubernetes.io/last-applied-configuration"},
		ClusterKinds:       []string{"ClusterWidget"},
		Views: []config.View{{
			Group: "example.com",
			Kind:  "Widget",
			Facts: []config.ViewFact{
				{Name: "phase", Path: ".status.phase"},
				// [*] fans out over a list and pulls one scalar per element.
				{Name: "conditionTypes", Path: ".status.conditions[*].type"},
				// A projection keeps each element's fields associated.
				{Name: "conditionSummary", Path: ".status.conditions[*].{type,status}"},
				// These two land on a map and a list of maps: the extractor
				// must refuse both.
				{Name: "wholeStatus", Path: ".status"},
				{Name: "conditions", Path: ".status.conditions"},
			},
		}},
	}
}

// newTestToolset wires the fake clients into a Toolset with a fixed clock.
func newTestToolset(cfg *config.Config) *Toolset {
	clientset := fake.NewClientset(fixtureObjects()...)
	fakeDisco := &cachedFakeDiscovery{FakeDiscovery: &discoveryfake.FakeDiscovery{
		Fake: &clienttesting.Fake{Resources: fixtureAPIResources()},
	}}

	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			widgetGVR:        "WidgetList",
			clusterWidgetGVR: "ClusterWidgetList",
		},
		fixtureWidget(), fixtureClusterWidget(),
	)

	// The metrics fake's tracker guesses the resource "podmetricses" from
	// the kind, but the typed fake serves "pods"/"nodes" - register the
	// fixtures under the real GVRs explicitly.
	metricsClient := metricsfake.NewSimpleClientset()
	for _, obj := range fixtureMetrics() {
		switch m := obj.(type) {
		case *metricsv1beta1.PodMetrics:
			if err := metricsClient.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("pods"), m, m.Namespace); err != nil {
				panic(err)
			}
		case *metricsv1beta1.NodeMetrics:
			if err := metricsClient.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("nodes"), m, ""); err != nil {
				panic(err)
			}
		}
	}

	ts := New(cfg, &kube.Clients{
		Typed:   clientset,
		Dynamic: dynamicClient,
		Mapper:  kube.NewMapper(fakeDisco),
		Metrics: metricsClient,
	})
	ts.now = func() time.Time { return fixedNow }

	return ts
}
