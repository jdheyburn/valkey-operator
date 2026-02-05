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
// +kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch;create;update;patch;delete
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

	// Handle workload type change - delete old workload and wait for pods to be removed
	if requeue, err := r.cleanupOldWorkload(ctx, node); err != nil {
		return ctrl.Result{}, err
	} else if requeue {
		log.V(1).Info("waiting for old workload cleanup")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Ensure workload exists (StatefulSet or Deployment)
	if node.Spec.WorkloadType == valkeyiov1alpha1.WorkloadTypeDeployment {
		if err := r.ensureDeployment(ctx, node); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err := r.ensureStatefulSet(ctx, node); err != nil {
			return ctrl.Result{}, err
		}
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

// ensureService creates or updates the headless Service for the ValkeyNode using server-side apply.
func (r *ValkeyNodeReconciler) ensureService(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	desired := buildHeadlessService(node)
	if err := controllerutil.SetControllerReference(node, desired, r.Scheme); err != nil {
		return err
	}

	return r.Patch(ctx, desired, client.Apply, client.FieldOwner("valkeynode-controller"), client.ForceOwnership)
}

// ensureStatefulSet creates or updates the StatefulSet for the ValkeyNode using server-side apply.
func (r *ValkeyNodeReconciler) ensureStatefulSet(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	desired := buildStatefulSet(node)
	if err := controllerutil.SetControllerReference(node, desired, r.Scheme); err != nil {
		return err
	}

	return r.Patch(ctx, desired, client.Apply, client.FieldOwner("valkeynode-controller"), client.ForceOwnership)
}

// ensureDeployment creates or updates the Deployment for the ValkeyNode using server-side apply.
func (r *ValkeyNodeReconciler) ensureDeployment(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	desired := buildDeployment(node)
	if err := controllerutil.SetControllerReference(node, desired, r.Scheme); err != nil {
		return err
	}

	return r.Patch(ctx, desired, client.Apply, client.FieldOwner("valkeynode-controller"), client.ForceOwnership)
}

// cleanupOldWorkload removes the old workload type if workloadType was changed.
// Returns true if cleanup is in progress and reconciliation should requeue.
func (r *ValkeyNodeReconciler) cleanupOldWorkload(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) (bool, error) {
	log := logf.FromContext(ctx)
	resourceName := valkeyNodeResourceName(node)

	var wrongWorkloadExists bool

	if node.Spec.WorkloadType == valkeyiov1alpha1.WorkloadTypeDeployment {
		// Check if StatefulSet exists (wrong workload type)
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: resourceName}, sts); err == nil {
			// StatefulSet exists, delete it
			log.Info("deleting StatefulSet due to workloadType change", "statefulset", resourceName)
			if err := r.Delete(ctx, sts); err != nil {
				return false, err
			}
			wrongWorkloadExists = true
		}
	} else {
		// Check if Deployment exists (wrong workload type)
		deploy := &appsv1.Deployment{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: resourceName}, deploy); err == nil {
			// Deployment exists, delete it
			log.Info("deleting Deployment due to workloadType change", "deployment", resourceName)
			if err := r.Delete(ctx, deploy); err != nil {
				return false, err
			}
			wrongWorkloadExists = true
		}
	}

	// If we just deleted the wrong workload, wait for pods to be removed
	if wrongWorkloadExists {
		return true, nil // Requeue to wait for deletion
	}

	// Check if correct workload exists - if so, no cleanup needed
	var correctWorkloadExists bool
	if node.Spec.WorkloadType == valkeyiov1alpha1.WorkloadTypeDeployment {
		deploy := &appsv1.Deployment{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: resourceName}, deploy); err == nil {
			correctWorkloadExists = true
		}
	} else {
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: resourceName}, sts); err == nil {
			correctWorkloadExists = true
		}
	}

	// If correct workload exists, cleanup is complete
	if correctWorkloadExists {
		return false, nil
	}

	// Neither workload exists - check if pods are still terminating
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(node.Namespace),
		client.MatchingLabels(valkeyNodeLabels(node))); err != nil {
		return false, err
	}
	if len(podList.Items) > 0 {
		log.V(1).Info("waiting for pods to be removed", "count", len(podList.Items))
		return true, nil // Pods still exist, requeue
	}

	return false, nil // Cleanup complete, ready to create new workload
}

// updateStatus updates the ValkeyNode status based on Pod state.
func (r *ValkeyNodeReconciler) updateStatus(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) error {
	log := logf.FromContext(ctx)
	resourceName := valkeyNodeResourceName(node)

	// Update service name
	node.Status.ServiceName = resourceName

	// Get pod by label selector (works for both StatefulSet and Deployment)
	pod := r.getPod(ctx, node)

	// Update pod info and ready condition
	if pod == nil {
		node.Status.Ready = false
		node.Status.PodName = ""
		node.Status.PodIP = ""
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

		// Check pod readiness from its conditions
		podReady := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				podReady = true
				break
			}
		}

		node.Status.Ready = podReady
		if podReady {
			meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
				Type:               valkeyiov1alpha1.ValkeyNodeConditionReady,
				Status:             metav1.ConditionTrue,
				Reason:             valkeyiov1alpha1.ValkeyNodeReasonPodRunning,
				Message:            "Pod is running and ready",
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

// getPod returns the pod for a ValkeyNode by listing with label selector.
func (r *ValkeyNodeReconciler) getPod(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) *corev1.Pod {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(node.Namespace),
		client.MatchingLabels(valkeyNodeLabels(node))); err != nil {
		return nil
	}
	if len(podList.Items) > 0 {
		return &podList.Items[0]
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&valkeyiov1alpha1.ValkeyNode{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Named("valkeynode").
		Complete(r)
}
