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
