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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
)

var _ = Describe("ValkeyNode Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		reconciler   *ValkeyNodeReconciler
		fakeRecorder *record.FakeRecorder
		testCtx      context.Context
	)

	BeforeEach(func() {
		testCtx = context.Background()
		fakeRecorder = record.NewFakeRecorder(100)
		reconciler = &ValkeyNodeReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: fakeRecorder,
		}
	})

	Context("When reconciling a new ValkeyNode", func() {
		const resourceName = "test-valkeynode"

		var (
			typeNamespacedName types.NamespacedName
			valkeyNode         *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("creating the ValkeyNode CR")
			valkeyNode = &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(testCtx, valkeyNode)).To(Succeed())
		})

		AfterEach(func() {
			By("cleaning up the ValkeyNode CR")
			resource := &valkeyiov1alpha1.ValkeyNode{}
			err := k8sClient.Get(testCtx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(testCtx, resource)).To(Succeed())
			}
		})

		It("should create StatefulSet and Service", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the headless Service was created")
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, typeNamespacedName, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(DefaultPort)))

			By("verifying the StatefulSet was created")
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, typeNamespacedName, sts)
			}, timeout, interval).Should(Succeed())

			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:8.0"))

			By("verifying events were recorded")
			events := collectEvents(fakeRecorder)
			Expect(events).To(ContainElement(ContainSubstring("ServiceCreated")))
			Expect(events).To(ContainElement(ContainSubstring("StatefulSetCreated")))
		})

		It("should set correct labels on resources", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			expectedLabels := map[string]string{
				"app.kubernetes.io/name":       "valkey",
				"app.kubernetes.io/instance":   resourceName,
				"app.kubernetes.io/managed-by": "valkey-operator",
				"app.kubernetes.io/component":  "valkeynode",
			}

			By("verifying labels on Service")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, svc)).To(Succeed())
			for key, value := range expectedLabels {
				Expect(svc.Labels).To(HaveKeyWithValue(key, value))
			}
			for key, value := range expectedLabels {
				Expect(svc.Spec.Selector).To(HaveKeyWithValue(key, value))
			}

			By("verifying labels on StatefulSet")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, sts)).To(Succeed())
			for key, value := range expectedLabels {
				Expect(sts.Labels).To(HaveKeyWithValue(key, value))
			}
			for key, value := range expectedLabels {
				Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue(key, value))
			}
		})

		It("should handle ValkeyNode deletion via garbage collection", func() {
			By("reconciling the ValkeyNode to create resources")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying resources exist")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, svc)).To(Succeed())
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, sts)).To(Succeed())

			By("verifying owner references are set")
			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(svc.OwnerReferences[0].Kind).To(Equal("ValkeyNode"))

			Expect(sts.OwnerReferences).To(HaveLen(1))
			Expect(sts.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(sts.OwnerReferences[0].Kind).To(Equal("ValkeyNode"))

			By("deleting the ValkeyNode")
			Expect(k8sClient.Delete(testCtx, valkeyNode)).To(Succeed())

			By("verifying ValkeyNode is deleted")
			Eventually(func() bool {
				err := k8sClient.Get(testCtx, typeNamespacedName, &valkeyiov1alpha1.ValkeyNode{})
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When updating a ValkeyNode", func() {
		const resourceName = "test-update-valkeynode"

		var (
			typeNamespacedName types.NamespacedName
			valkeyNode         *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("creating the ValkeyNode CR")
			valkeyNode = &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}
			Expect(k8sClient.Create(testCtx, valkeyNode)).To(Succeed())

			By("reconciling to create initial resources")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			By("cleaning up the ValkeyNode CR")
			resource := &valkeyiov1alpha1.ValkeyNode{}
			err := k8sClient.Get(testCtx, typeNamespacedName, resource)
			if err == nil {
				Expect(k8sClient.Delete(testCtx, resource)).To(Succeed())
			}
		})

		It("should update StatefulSet when spec.image changes", func() {
			By("updating the ValkeyNode image")
			Expect(k8sClient.Get(testCtx, typeNamespacedName, valkeyNode)).To(Succeed())
			valkeyNode.Spec.Image = "valkey/valkey:8.1"
			Expect(k8sClient.Update(testCtx, valkeyNode)).To(Succeed())

			By("reconciling the updated ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet image was updated")
			sts := &appsv1.StatefulSet{}
			Eventually(func() string {
				if err := k8sClient.Get(testCtx, typeNamespacedName, sts); err != nil {
					return ""
				}
				if len(sts.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return sts.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:8.1"))
		})

		It("should restore the image to match ValkeyNode spec when externally modified", func() {
			By("externally modifying the StatefulSet image")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, sts)).To(Succeed())
			sts.Spec.Template.Spec.Containers[0].Image = "modified/image:latest"
			Expect(k8sClient.Update(testCtx, sts)).To(Succeed())

			By("verifying the image was modified")
			Expect(k8sClient.Get(testCtx, typeNamespacedName, sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("modified/image:latest"))

			By("reconciling to restore the correct image")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the image was restored")
			Eventually(func() string {
				if err := k8sClient.Get(testCtx, typeNamespacedName, sts); err != nil {
					return ""
				}
				return sts.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:8.0"))
		})

		It("should apply scheduling constraints from spec", func() {
			By("updating ValkeyNode with scheduling constraints")
			Expect(k8sClient.Get(testCtx, typeNamespacedName, valkeyNode)).To(Succeed())
			valkeyNode.Spec.NodeSelector = map[string]string{
				"disktype": "ssd",
				"env":      "production",
			}
			valkeyNode.Spec.Tolerations = []corev1.Toleration{
				{
					Key:      "dedicated",
					Operator: corev1.TolerationOpEqual,
					Value:    "valkey",
					Effect:   corev1.TaintEffectNoSchedule,
				},
			}
			Expect(k8sClient.Update(testCtx, valkeyNode)).To(Succeed())

			By("reconciling the updated ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet has scheduling constraints")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, sts)).To(Succeed())

			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("disktype", "ssd"))
			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("env", "production"))
			Expect(sts.Spec.Template.Spec.Tolerations).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Tolerations[0].Key).To(Equal("dedicated"))
			Expect(sts.Spec.Template.Spec.Tolerations[0].Value).To(Equal("valkey"))
		})
	})

	Context("When ValkeyNode does not exist", func() {
		It("should not return an error for missing resource", func() {
			By("reconciling a non-existent ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("ValkeyNode Resource Builders", func() {
	Describe("valkeyNodeLabels", func() {
		It("returns correct labels", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
			}

			labels := valkeyNodeLabels(node)

			Expect(labels).To(HaveLen(4))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", "valkey"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/instance", "test-node"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "valkey-operator"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "valkeynode"))
		})

		It("uses the node name for instance label", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-custom-node",
					Namespace: "my-namespace",
				},
			}

			labels := valkeyNodeLabels(node)

			Expect(labels["app.kubernetes.io/instance"]).To(Equal("my-custom-node"))
		})
	})

	Describe("buildHeadlessService", func() {
		It("creates correct service spec", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "test-ns",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			svc := buildHeadlessService(node)

			Expect(svc.Name).To(Equal("test-node"))
			Expect(svc.Namespace).To(Equal("test-ns"))
			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Name).To(Equal("valkey"))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(DefaultPort)))
			Expect(svc.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(DefaultPort)))
		})

		It("uses standard labels as selector", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "selector-test",
					Namespace: "default",
				},
			}

			svc := buildHeadlessService(node)

			expectedLabels := valkeyNodeLabels(node)
			Expect(svc.Spec.Selector).To(Equal(expectedLabels))
			Expect(svc.Labels).To(Equal(expectedLabels))
		})
	})

	Describe("buildStatefulSet", func() {
		It("creates correct statefulset spec with probes", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "test-ns",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			}

			sts := buildStatefulSet(node)

			By("verifying basic properties")
			Expect(sts.Name).To(Equal("test-node"))
			Expect(sts.Namespace).To(Equal("test-ns"))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.ServiceName).To(Equal("test-node"))

			By("verifying container spec")
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("valkey"))
			Expect(container.Image).To(Equal("valkey/valkey:8.0"))

			By("verifying resource requirements")
			Expect(container.Resources.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("100m")))
			Expect(container.Resources.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("128Mi")))
			Expect(container.Resources.Limits[corev1.ResourceCPU]).To(Equal(resource.MustParse("500m")))
			Expect(container.Resources.Limits[corev1.ResourceMemory]).To(Equal(resource.MustParse("512Mi")))

			By("verifying readiness probe")
			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.TCPSocket).NotTo(BeNil())
			Expect(container.ReadinessProbe.TCPSocket.Port.IntValue()).To(Equal(DefaultPort))
			Expect(container.ReadinessProbe.InitialDelaySeconds).To(Equal(int32(5)))
			Expect(container.ReadinessProbe.PeriodSeconds).To(Equal(int32(5)))

			By("verifying liveness probe")
			Expect(container.LivenessProbe).NotTo(BeNil())
			Expect(container.LivenessProbe.TCPSocket).NotTo(BeNil())
			Expect(container.LivenessProbe.TCPSocket.Port.IntValue()).To(Equal(DefaultPort))
			Expect(container.LivenessProbe.InitialDelaySeconds).To(Equal(int32(15)))
			Expect(container.LivenessProbe.PeriodSeconds).To(Equal(int32(10)))
		})

		It("applies scheduling constraints", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					NodeSelector: map[string]string{
						"disktype": "ssd",
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "dedicated",
							Operator: corev1.TolerationOpEqual,
							Value:    "valkey",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "topology.kubernetes.io/zone",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"us-west-2a"},
											},
										},
									},
								},
							},
						},
					},
				},
			}

			sts := buildStatefulSet(node)

			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("disktype", "ssd"))
			Expect(sts.Spec.Template.Spec.Tolerations).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Tolerations[0].Key).To(Equal("dedicated"))
			Expect(sts.Spec.Template.Spec.Affinity).NotTo(BeNil())
			Expect(sts.Spec.Template.Spec.Affinity.NodeAffinity).NotTo(BeNil())
		})

		It("sets correct labels and selectors", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "label-test",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			sts := buildStatefulSet(node)
			expectedLabels := valkeyNodeLabels(node)

			Expect(sts.Labels).To(Equal(expectedLabels))
			Expect(sts.Spec.Selector.MatchLabels).To(Equal(expectedLabels))
			Expect(sts.Spec.Template.Labels).To(Equal(expectedLabels))
		})

		It("configures container port correctly", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "port-test",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			sts := buildStatefulSet(node)

			Expect(sts.Spec.Template.Spec.Containers[0].Ports).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Containers[0].Ports[0].Name).To(Equal("valkey"))
			Expect(sts.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(DefaultPort)))
		})
	})
})
