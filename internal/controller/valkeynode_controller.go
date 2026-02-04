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
