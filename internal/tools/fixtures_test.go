package tools

// Adversarial fixtures: every object carries sentinel strings in fields the
// server must never serialize (env values, annotations, ConfigMap/Secret
// data, CR specs, managedFields). The leak test asserts no sentinel ever
// appears in any tool response.

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

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
)

var sentinels = []string{
	leakEnvValue, leakLastApplied, leakAnnotation, leakConfigMapData,
	leakSecretData, leakCRSpec, leakManagedFields, leakCommandArg,
	leakVolumeAttr,
}

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
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "prod", Annotations: adversarialAnnotations()},
		Data:       map[string]string{"config.ini": leakConfigMapData},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "prod", Annotations: adversarialAnnotations()},
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

	return []runtime.Object{
		deployment, statefulSet, daemonSet, pod, configMap, secret,
		service, endpointSlice, pvc, boundPV, releasedPV, job, cronJob,
		podEvent, oldEvent, deployEvent, widgetEvent,
	}
}

var widgetGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

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
		Mode:            "stdio",
		Namespaces:      []string{"prod", "dev"},
		EnableCRDStatus: true,
		EnableLogs:      true,
		MaxLogBytes:     1024,
		MaxEvents:       50,
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
		map[schema.GroupVersionResource]string{widgetGVR: "WidgetList"},
		fixtureWidget(),
	)

	ts := New(cfg, &kube.Clients{
		Typed:   clientset,
		Dynamic: dynamicClient,
		Mapper:  kube.NewMapper(fakeDisco),
	})
	ts.now = func() time.Time { return fixedNow }

	return ts
}
