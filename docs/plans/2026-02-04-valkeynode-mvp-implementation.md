# ValkeyNode MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement the ValkeyNode CRD - an internal abstraction for single-pod Valkey deployments with StatefulSet and headless Service.

**Architecture:** ValkeyNode controller creates a singleton StatefulSet (replicas=1) and headless Service for stable DNS identity. Parent controllers (Valkey, ValkeyCluster) will create ValkeyNodes; users don't create them directly.

**Tech Stack:** Go 1.24, Kubebuilder, controller-runtime 0.22.4, Ginkgo/Gomega for tests

**Working Directory:** `/Users/joseph.heyburn/code/valkey-operator_jdheyburn/.worktrees/valkeynode-mvp`

---

## Task 1: Define ValkeyNode Types

**Files:**
- Create: `api/v1alpha1/valkeynode_types.go`

**Step 1: Create the ValkeyNode type definitions**

```go
/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValkeyNodeSpec defines the desired state of ValkeyNode.
type ValkeyNodeSpec struct {
	// Image is the Valkey container image to use.
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// Resources defines the resource requirements for the Valkey container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector defines the node selection constraints.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Affinity defines the pod affinity/anti-affinity rules.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines the pod tolerations.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// ValkeyNodeStatus defines the observed state of ValkeyNode.
type ValkeyNodeStatus struct {
	// Ready indicates whether the ValkeyNode is ready to serve traffic.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// PodName is the name of the pod created by the StatefulSet.
	// +optional
	PodName string `json:"podName,omitempty"`

	// PodIP is the IP address of the pod.
	// +optional
	PodIP string `json:"podIP,omitempty"`

	// ServiceName is the name of the headless service.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// Conditions represent the current state of the ValkeyNode.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// ValkeyNodeConditionReady indicates the ValkeyNode is ready.
	ValkeyNodeConditionReady = "Ready"
	// ValkeyNodeConditionStatefulSetReady indicates the StatefulSet is ready.
	ValkeyNodeConditionStatefulSetReady = "StatefulSetReady"
)

const (
	// ValkeyNodeReasonPodRunning indicates the pod is running.
	ValkeyNodeReasonPodRunning = "PodRunning"
	// ValkeyNodeReasonPodNotReady indicates the pod is not ready.
	ValkeyNodeReasonPodNotReady = "PodNotReady"
	// ValkeyNodeReasonStatefulSetNotReady indicates the StatefulSet is not ready.
	ValkeyNodeReasonStatefulSetNotReady = "StatefulSetNotReady"
	// ValkeyNodeReasonReplicaAvailable indicates the replica is available.
	ValkeyNodeReasonReplicaAvailable = "ReplicaAvailable"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vkn

// ValkeyNode is the Schema for the valkeynodes API.
// ValkeyNode is an internal CRD managed by the operator.
// Users should not create ValkeyNodes directly.
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Whether the node is ready"
// +kubebuilder:printcolumn:name="Pod",type="string",JSONPath=".status.podName",description="Pod name"
// +kubebuilder:printcolumn:name="IP",type="string",JSONPath=".status.podIP",description="Pod IP",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time since creation"
type ValkeyNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec ValkeyNodeSpec `json:"spec"`

	// +optional
	Status ValkeyNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ValkeyNodeList contains a list of ValkeyNode.
type ValkeyNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ValkeyNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ValkeyNode{}, &ValkeyNodeList{})
}
```

**Step 2: Run code generation**

Run: `make generate`
Expected: Generates `zz_generated.deepcopy.go` with DeepCopy methods

**Step 3: Generate CRD manifests**

Run: `make manifests`
Expected: Creates `config/crd/bases/valkey.io_valkeynodes.yaml`

**Step 4: Verify generation succeeded**

Run: `ls config/crd/bases/valkey.io_valkeynodes.yaml`
Expected: File exists

**Step 5: Commit**

```bash
git add api/v1alpha1/valkeynode_types.go api/v1alpha1/zz_generated.deepcopy.go config/crd/bases/valkey.io_valkeynodes.yaml config/rbac/role.yaml
git commit -m "feat(valkeynode): add ValkeyNode CRD type definitions

Define ValkeyNodeSpec with image, resources, and scheduling fields.
Define ValkeyNodeStatus with ready, podName, podIP, serviceName, conditions.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 2: Implement Resource Builders

**Files:**
- Create: `internal/controller/valkeynode_resources.go`

**Step 1: Create the resource builder file with label helpers**

```go
/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

// valkeyNodeLabels returns the standard labels for ValkeyNode resources.
func valkeyNodeLabels(node *valkeyiov1alpha1.ValkeyNode) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "valkey",
		"app.kubernetes.io/instance":   node.Name,
		"app.kubernetes.io/managed-by": "valkey-operator",
		"app.kubernetes.io/component":  "valkeynode",
	}
}

// buildHeadlessService creates a headless Service for the ValkeyNode.
func buildHeadlessService(node *valkeyiov1alpha1.ValkeyNode) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: node.Namespace,
			Labels:    valkeyNodeLabels(node),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  valkeyNodeLabels(node),
			Ports: []corev1.ServicePort{
				{
					Name:       "valkey",
					Port:       DefaultPort,
					TargetPort: intstr.FromInt(DefaultPort),
				},
			},
		},
	}
}

// buildStatefulSet creates a StatefulSet for the ValkeyNode.
func buildStatefulSet(node *valkeyiov1alpha1.ValkeyNode) *appsv1.StatefulSet {
	replicas := int32(1)
	labels := valkeyNodeLabels(node)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: node.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: node.Name,
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:      "valkey",
							Image:     node.Spec.Image,
							Resources: node.Spec.Resources,
							Ports: []corev1.ContainerPort{
								{
									Name:          "valkey",
									ContainerPort: DefaultPort,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt(DefaultPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt(DefaultPort),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       10,
							},
						},
					},
					NodeSelector: node.Spec.NodeSelector,
					Affinity:     node.Spec.Affinity,
					Tolerations:  node.Spec.Tolerations,
				},
			},
		},
	}
}
```

**Step 2: Verify file compiles**

Run: `go build ./...`
Expected: No errors

**Step 3: Commit**

```bash
git add internal/controller/valkeynode_resources.go
git commit -m "feat(valkeynode): add resource builders for StatefulSet and Service

Add valkeyNodeLabels(), buildHeadlessService(), and buildStatefulSet()
helper functions to construct Kubernetes resources from ValkeyNode spec.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 3: Implement ValkeyNode Controller

**Files:**
- Create: `internal/controller/valkeynode_controller.go`

**Step 1: Create the controller file**

```go
/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

// ValkeyNodeReconciler reconciles a ValkeyNode object.
type ValkeyNodeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=valkey.io,resources=valkeynodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=valkey.io,resources=valkeynodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=valkey.io,resources=valkeynodes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="apps",resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile moves the current state of the ValkeyNode closer to the desired state.
func (r *ValkeyNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.V(1).Info("reconciling ValkeyNode")

	// Fetch the ValkeyNode instance
	node := &valkeyiov1alpha1.ValkeyNode{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Ensure headless Service exists
	if err := r.ensureService(ctx, node); err != nil {
		return ctrl.Result{}, err
	}

	// Ensure StatefulSet exists
	if err := r.ensureStatefulSet(ctx, node); err != nil {
		return ctrl.Result{}, err
	}

	// Update status from StatefulSet and Pod
	if err := r.updateStatus(ctx, node); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue if not ready
	if !node.Status.Ready {
		log.V(1).Info("ValkeyNode not ready, requeuing")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	log.V(1).Info("ValkeyNode reconciliation complete")
	return ctrl.Result{}, nil
}

// ensureService creates or updates the headless Service for the ValkeyNode.
func (r *ValkeyNodeReconciler) ensureService(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	log := logf.FromContext(ctx)

	desired := buildHeadlessService(node)
	if err := controllerutil.SetControllerReference(node, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating headless Service", "service", desired.Name)
			if err := r.Create(ctx, desired); err != nil {
				r.Recorder.Eventf(node, corev1.EventTypeWarning, "ServiceCreateFailed", "Failed to create Service: %v", err)
				return err
			}
			r.Recorder.Event(node, corev1.EventTypeNormal, "ServiceCreated", "Created headless Service")
			return nil
		}
		return err
	}

	// Update if needed (selector or ports changed)
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(node, corev1.EventTypeWarning, "ServiceUpdateFailed", "Failed to update Service: %v", err)
		return err
	}

	return nil
}

// ensureStatefulSet creates or updates the StatefulSet for the ValkeyNode.
func (r *ValkeyNodeReconciler) ensureStatefulSet(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	log := logf.FromContext(ctx)

	desired := buildStatefulSet(node)
	if err := controllerutil.SetControllerReference(node, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.StatefulSet{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating StatefulSet", "statefulset", desired.Name)
			if err := r.Create(ctx, desired); err != nil {
				r.Recorder.Eventf(node, corev1.EventTypeWarning, "StatefulSetCreateFailed", "Failed to create StatefulSet: %v", err)
				return err
			}
			r.Recorder.Event(node, corev1.EventTypeNormal, "StatefulSetCreated", "Created StatefulSet")
			return nil
		}
		return err
	}

	// Update mutable fields if needed
	existing.Spec.Template.Spec.Containers[0].Image = desired.Spec.Template.Spec.Containers[0].Image
	existing.Spec.Template.Spec.Containers[0].Resources = desired.Spec.Template.Spec.Containers[0].Resources
	existing.Spec.Template.Spec.NodeSelector = desired.Spec.Template.Spec.NodeSelector
	existing.Spec.Template.Spec.Affinity = desired.Spec.Template.Spec.Affinity
	existing.Spec.Template.Spec.Tolerations = desired.Spec.Template.Spec.Tolerations

	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(node, corev1.EventTypeWarning, "StatefulSetUpdateFailed", "Failed to update StatefulSet: %v", err)
		return err
	}

	return nil
}

// updateStatus updates the ValkeyNode status based on StatefulSet and Pod state.
func (r *ValkeyNodeReconciler) updateStatus(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	log := logf.FromContext(ctx)

	// Get StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: node.Name}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			// StatefulSet not yet created
			return nil
		}
		return err
	}

	// Update service name
	node.Status.ServiceName = node.Name

	// Check StatefulSet readiness
	stsReady := sts.Status.ReadyReplicas >= 1
	if stsReady {
		meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
			Type:               valkeyiov1alpha1.ValkeyNodeConditionStatefulSetReady,
			Status:             metav1.ConditionTrue,
			Reason:             valkeyiov1alpha1.ValkeyNodeReasonReplicaAvailable,
			Message:            "StatefulSet has 1/1 ready replicas",
			ObservedGeneration: node.Generation,
		})
	} else {
		meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
			Type:               valkeyiov1alpha1.ValkeyNodeConditionStatefulSetReady,
			Status:             metav1.ConditionFalse,
			Reason:             valkeyiov1alpha1.ValkeyNodeReasonStatefulSetNotReady,
			Message:            "StatefulSet does not have ready replicas",
			ObservedGeneration: node.Generation,
		})
	}

	// Get Pod info
	podName := node.Name + "-0"
	pod := &corev1.Pod{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: podName}, pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		// Pod not yet created
		node.Status.Ready = false
		meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
			Type:               valkeyiov1alpha1.ValkeyNodeConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             valkeyiov1alpha1.ValkeyNodeReasonPodNotReady,
			Message:            "Pod does not exist yet",
			ObservedGeneration: node.Generation,
		})
	} else {
		node.Status.PodName = pod.Name
		node.Status.PodIP = pod.Status.PodIP

		// Check pod readiness
		podReady := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				podReady = true
				break
			}
		}

		node.Status.Ready = podReady && stsReady
		if node.Status.Ready {
			meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
				Type:               valkeyiov1alpha1.ValkeyNodeConditionReady,
				Status:             metav1.ConditionTrue,
				Reason:             valkeyiov1alpha1.ValkeyNodeReasonPodRunning,
				Message:            "StatefulSet pod is running and ready",
				ObservedGeneration: node.Generation,
			})
		} else {
			meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
				Type:               valkeyiov1alpha1.ValkeyNodeConditionReady,
				Status:             metav1.ConditionFalse,
				Reason:             valkeyiov1alpha1.ValkeyNodeReasonPodNotReady,
				Message:            "Pod is not ready",
				ObservedGeneration: node.Generation,
			})
		}
	}

	// Update status
	if err := r.Status().Update(ctx, node); err != nil {
		log.Error(err, "failed to update ValkeyNode status")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&valkeyiov1alpha1.ValkeyNode{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Named("valkeynode").
		Complete(r)
}
```

**Step 2: Verify file compiles**

Run: `go build ./...`
Expected: No errors

**Step 3: Commit**

```bash
git add internal/controller/valkeynode_controller.go
git commit -m "feat(valkeynode): implement ValkeyNode controller

Reconciler creates headless Service and StatefulSet from ValkeyNode spec.
Updates status with pod name, IP, and conditions based on observed state.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 4: Register Controller in main.go

**Files:**
- Modify: `cmd/main.go:183-191`

**Step 1: Add ValkeyNode controller registration after ValkeyCluster**

Find this block in `cmd/main.go`:
```go
	if err := (&controller.ValkeyClusterReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("valkeycluster-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ValkeyCluster")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder
```

Replace with:
```go
	if err := (&controller.ValkeyClusterReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("valkeycluster-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ValkeyCluster")
		os.Exit(1)
	}

	if err := (&controller.ValkeyNodeReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("valkeynode-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ValkeyNode")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder
```

**Step 2: Regenerate RBAC manifests**

Run: `make manifests`
Expected: Updates `config/rbac/role.yaml` with StatefulSet permissions

**Step 3: Verify build succeeds**

Run: `go build ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add cmd/main.go config/rbac/role.yaml
git commit -m "feat(valkeynode): register ValkeyNode controller in manager

Add ValkeyNodeReconciler to controller manager setup.
Regenerate RBAC role with StatefulSet permissions.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 5: Write Integration Tests

**Files:**
- Create: `internal/controller/valkeynode_controller_test.go`

**Step 1: Create integration test file**

```go
/*
Copyright 2025 Valkey Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

var _ = Describe("ValkeyNode Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		ctx        context.Context
		reconciler *ValkeyNodeReconciler
		namespace  string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "default"
		reconciler = &ValkeyNodeReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	Context("When creating a ValkeyNode", func() {
		It("should create StatefulSet and Service", func() {
			nodeName := "test-valkeynode-create"

			// Create ValkeyNode
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			// Trigger reconciliation
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify Service was created
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/instance", nodeName))

			// Verify StatefulSet was created
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.ServiceName).To(Equal(nodeName))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:8.0"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		})

		It("should update StatefulSet when spec.image changes", func() {
			nodeName := "test-valkeynode-update"

			// Create ValkeyNode
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:7.0",
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			// Initial reconciliation
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify initial image
			sts := &appsv1.StatefulSet{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
				if err != nil {
					return ""
				}
				return sts.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:7.0"))

			// Update image
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      nodeName,
				Namespace: namespace,
			}, node)).To(Succeed())
			node.Spec.Image = "valkey/valkey:8.0"
			Expect(k8sClient.Update(ctx, node)).To(Succeed())

			// Reconcile again
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify updated image
			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
				if err != nil {
					return ""
				}
				return sts.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:8.0"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		})

		It("should handle ValkeyNode deletion via garbage collection", func() {
			nodeName := "test-valkeynode-delete"

			// Create ValkeyNode
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify resources exist
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
			}, timeout, interval).Should(Succeed())

			// Delete ValkeyNode
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())

			// Reconcile should handle not found
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify ValkeyNode is gone
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, node)
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})

		It("should set correct labels on resources", func() {
			nodeName := "test-valkeynode-labels"

			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Check Service labels
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "valkey"))
			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/instance", nodeName))
			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "valkey-operator"))
			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/component", "valkeynode"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		})

		It("should apply scheduling constraints from spec", func() {
			nodeName := "test-valkeynode-scheduling"

			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					NodeSelector: map[string]string{
						"node-type": "valkey",
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "dedicated",
							Operator: corev1.TolerationOpEqual,
							Value:    "valkey",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("node-type", "valkey"))
			Expect(sts.Spec.Template.Spec.Tolerations).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Tolerations[0].Key).To(Equal("dedicated"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		})
	})

	Context("When reconciling an externally modified StatefulSet", func() {
		It("should restore the image to match ValkeyNode spec", func() {
			nodeName := "test-valkeynode-restore"

			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodeName,
					Namespace: namespace,
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Externally modify StatefulSet
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
			}, timeout, interval).Should(Succeed())

			sts.Spec.Template.Spec.Containers[0].Image = "valkey/valkey:7.0"
			Expect(k8sClient.Update(ctx, sts)).To(Succeed())

			// Reconcile should restore
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nodeName,
					Namespace: namespace,
				}, sts)
				if err != nil {
					return ""
				}
				return sts.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:8.0"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, node)).To(Succeed())
		})
	})
})

var _ = Describe("ValkeyNode Resource Builders", func() {
	Context("valkeyNodeLabels", func() {
		It("should return correct labels", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
			}

			labels := valkeyNodeLabels(node)

			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", "valkey"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/instance", "test-node"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "valkey-operator"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "valkeynode"))
		})
	})

	Context("buildHeadlessService", func() {
		It("should create headless service with correct spec", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			svc := buildHeadlessService(node)

			Expect(svc.Name).To(Equal("test-node"))
			Expect(svc.Namespace).To(Equal("default"))
			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(6379)))
		})
	})

	Context("buildStatefulSet", func() {
		It("should create StatefulSet with correct spec", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					NodeSelector: map[string]string{
						"zone": "us-east-1a",
					},
				},
			}

			sts := buildStatefulSet(node)

			Expect(sts.Name).To(Equal("test-node"))
			Expect(sts.Namespace).To(Equal("default"))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.ServiceName).To(Equal("test-node"))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:8.0"))
			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("zone", "us-east-1a"))
		})

		It("should include readiness and liveness probes", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			sts := buildStatefulSet(node)

			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.TCPSocket).NotTo(BeNil())
			Expect(container.LivenessProbe).NotTo(BeNil())
			Expect(container.LivenessProbe.TCPSocket).NotTo(BeNil())
		})
	})
})
```

**Step 2: Run tests**

Run: `go test ./internal/controller/... -v -run ValkeyNode`
Expected: All tests pass

**Step 3: Commit**

```bash
git add internal/controller/valkeynode_controller_test.go
git commit -m "test(valkeynode): add integration tests for ValkeyNode controller

Tests cover:
- Creating StatefulSet and Service
- Updating StatefulSet when image changes
- Garbage collection on deletion
- Label correctness
- Scheduling constraints
- Restoring externally modified StatefulSet
- Resource builder unit tests

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 6: Add Sample CR

**Files:**
- Create: `config/samples/v1alpha1_valkeynode.yaml`

**Step 1: Create sample ValkeyNode manifest**

```yaml
# ValkeyNode is an internal CRD - users should not create these directly.
# This sample is for testing and development purposes only.
apiVersion: valkey.io/v1alpha1
kind: ValkeyNode
metadata:
  name: sample-valkeynode
  namespace: default
spec:
  # Required: Valkey container image
  image: valkey/valkey:8.0

  # Optional: Resource requirements
  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"
    limits:
      memory: "1Gi"
      cpu: "500m"

  # Optional: Node selector for scheduling
  # nodeSelector:
  #   node-type: valkey

  # Optional: Tolerations
  # tolerations:
  #   - key: "dedicated"
  #     operator: "Equal"
  #     value: "valkey"
  #     effect: "NoSchedule"
```

**Step 2: Verify sample is valid**

Run: `kubectl apply --dry-run=client -f config/samples/v1alpha1_valkeynode.yaml`
Expected: valkeynode.valkey.io/sample-valkeynode created (dry run)

**Step 3: Commit**

```bash
git add config/samples/v1alpha1_valkeynode.yaml
git commit -m "docs(valkeynode): add sample ValkeyNode manifest

Example ValkeyNode CR for testing and development.
Note: ValkeyNode is internal, users should not create directly.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Task 7: Final Verification

**Step 1: Run all tests**

Run: `make test`
Expected: All tests pass including new ValkeyNode tests

**Step 2: Build the operator**

Run: `make build`
Expected: Binary builds successfully

**Step 3: Verify CRD generation**

Run: `cat config/crd/bases/valkey.io_valkeynodes.yaml | head -50`
Expected: Shows ValkeyNode CRD with correct spec/status fields

**Step 4: Final commit (if any uncommitted changes)**

```bash
git status
# If clean, skip. Otherwise commit any remaining changes.
```

---

## Summary

After completing all tasks, you will have:

1. **ValkeyNode CRD** (`api/v1alpha1/valkeynode_types.go`) - Type definitions with spec and status
2. **Resource Builders** (`internal/controller/valkeynode_resources.go`) - Helpers to build Service/StatefulSet
3. **Controller** (`internal/controller/valkeynode_controller.go`) - Reconciliation logic
4. **Tests** (`internal/controller/valkeynode_controller_test.go`) - Integration tests with envtest
5. **Sample CR** (`config/samples/v1alpha1_valkeynode.yaml`) - Example manifest

Total commits: 7
Estimated lines of code: ~600
