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
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

// valkeyNodeResourceName returns the name for resources created by a ValkeyNode.
func valkeyNodeResourceName(node *valkeyiov1alpha1.ValkeyNode) string {
	return "valkey-" + node.Name
}

// valkeyNodeLabels returns the standard labels for ValkeyNode resources.
func valkeyNodeLabels(node *valkeyiov1alpha1.ValkeyNode) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "valkey",
		"app.kubernetes.io/instance":   node.Name,
		"app.kubernetes.io/managed-by": "valkey-operator",
		"app.kubernetes.io/component":  "valkeynode",
	}
}

// buildConfigMap creates a ConfigMap containing the Valkey configuration for the ValkeyNode.
func buildConfigMap(node *valkeyiov1alpha1.ValkeyNode) *corev1.ConfigMap {
	// Required defaults
	config := map[string]string{
		"bind":           "0.0.0.0",
		"protected-mode": "no",
	}

	// Merge user config (can override defaults)
	for k, v := range node.Spec.Config {
		config[k] = v
	}

	// Controller-managed settings (always applied, cannot be overridden)
	serviceDNS := fmt.Sprintf("%s.%s.svc.cluster.local", valkeyNodeResourceName(node), node.Namespace)
	config["replica-announce-ip"] = serviceDNS
	config["replica-announce-port"] = strconv.Itoa(DefaultPort)

	// Build config file content
	var lines []string
	for k, v := range config {
		lines = append(lines, fmt.Sprintf("%s %s", k, v))
	}
	sort.Strings(lines) // Consistent ordering
	configData := strings.Join(lines, "\n")

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkeyNodeResourceName(node),
			Namespace: node.Namespace,
			Labels:    valkeyNodeLabels(node),
		},
		Data: map[string]string{
			"valkey.conf": configData,
		},
	}
}

// buildHeadlessService creates a headless Service for the ValkeyNode.
func buildHeadlessService(node *valkeyiov1alpha1.ValkeyNode) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkeyNodeResourceName(node),
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

// buildPodTemplateSpec creates the pod template spec for ValkeyNode workloads.
func buildPodTemplateSpec(node *valkeyiov1alpha1.ValkeyNode, labels map[string]string) corev1.PodTemplateSpec {
	resourceName := valkeyNodeResourceName(node)

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: resourceName,
							},
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "valkey",
					Image:   node.Spec.Image,
					Command: []string{"valkey-server", "/etc/valkey/valkey.conf"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "config",
							MountPath: "/etc/valkey",
							ReadOnly:  true,
						},
					},
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
	}
}

// buildDeployment creates a Deployment for the ValkeyNode.
func buildDeployment(node *valkeyiov1alpha1.ValkeyNode) *appsv1.Deployment {
	replicas := int32(1)
	labels := valkeyNodeLabels(node)
	resourceName := valkeyNodeResourceName(node)

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: node.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: buildPodTemplateSpec(node, labels),
		},
	}
}

// buildStatefulSet creates a StatefulSet for the ValkeyNode.
func buildStatefulSet(node *valkeyiov1alpha1.ValkeyNode) *appsv1.StatefulSet {
	replicas := int32(1)
	labels := valkeyNodeLabels(node)
	resourceName := valkeyNodeResourceName(node)

	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: node.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: resourceName,
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: buildPodTemplateSpec(node, labels),
		},
	}
}
