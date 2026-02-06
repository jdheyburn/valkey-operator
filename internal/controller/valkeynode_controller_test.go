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
	"k8s.io/client-go/tools/events"
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
		fakeRecorder *events.FakeRecorder
		testCtx      context.Context
	)

	BeforeEach(func() {
		testCtx = context.Background()
		fakeRecorder = events.NewFakeRecorder(100)
		reconciler = &ValkeyNodeReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: fakeRecorder,
		}
	})

	Context("When reconciling a new ValkeyNode", func() {
		const resourceName = "test-valkeynode"
		const managedResourceName = "valkey-" + resourceName

		var (
			typeNamespacedName        types.NamespacedName
			managedResourceNamespaced types.NamespacedName
			valkeyNode                *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}
			managedResourceNamespaced = types.NamespacedName{
				Name:      managedResourceName,
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
				return k8sClient.Get(testCtx, managedResourceNamespaced, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(DefaultPort)))

			By("verifying the StatefulSet was created")
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, managedResourceNamespaced, sts)
			}, timeout, interval).Should(Succeed())

			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:8.0"))
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
				"app.kubernetes.io/component":  "valkey-node",
			}

			By("verifying labels on Service")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, svc)).To(Succeed())
			for key, value := range expectedLabels {
				Expect(svc.Labels).To(HaveKeyWithValue(key, value))
			}
			for key, value := range expectedLabels {
				Expect(svc.Spec.Selector).To(HaveKeyWithValue(key, value))
			}

			By("verifying labels on StatefulSet")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())
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
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, svc)).To(Succeed())
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())

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
		const managedResourceName = "valkey-" + resourceName

		var (
			typeNamespacedName        types.NamespacedName
			managedResourceNamespaced types.NamespacedName
			valkeyNode                *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}
			managedResourceNamespaced = types.NamespacedName{
				Name:      managedResourceName,
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
				if err := k8sClient.Get(testCtx, managedResourceNamespaced, sts); err != nil {
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
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())
			sts.Spec.Template.Spec.Containers[0].Image = "modified/image:latest"
			Expect(k8sClient.Update(testCtx, sts)).To(Succeed())

			By("verifying the image was modified")
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("modified/image:latest"))

			By("reconciling to restore the correct image")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the image was restored")
			Eventually(func() string {
				if err := k8sClient.Get(testCtx, managedResourceNamespaced, sts); err != nil {
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
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())

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

	// TODO: Add tests for switching workload types (StatefulSet <-> Deployment)
	// This functionality will be implemented later

	Context("When reconciling a ValkeyNode with Deployment workloadType", func() {
		const resourceName = "test-deployment-valkeynode"
		const managedResourceName = "valkey-" + resourceName

		var (
			typeNamespacedName        types.NamespacedName
			managedResourceNamespaced types.NamespacedName
			valkeyNode                *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}
			managedResourceNamespaced = types.NamespacedName{
				Name:      managedResourceName,
				Namespace: "default",
			}

			By("creating the ValkeyNode CR with Deployment workloadType")
			valkeyNode = &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image:        "valkey/valkey:8.0",
					WorkloadType: valkeyiov1alpha1.WorkloadTypeDeployment,
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

		It("should create Deployment and Service (not StatefulSet)", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the headless Service was created")
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, managedResourceNamespaced, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))

			By("verifying the Deployment was created")
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, managedResourceNamespaced, deploy)
			}, timeout, interval).Should(Succeed())

			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("valkey/valkey:8.0"))

			By("verifying no StatefulSet was created")
			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(testCtx, managedResourceNamespaced, sts)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should set correct labels on Deployment", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			expectedLabels := map[string]string{
				"app.kubernetes.io/name":       "valkey",
				"app.kubernetes.io/instance":   resourceName,
				"app.kubernetes.io/managed-by": "valkey-operator",
				"app.kubernetes.io/component":  "valkey-node",
			}

			By("verifying labels on Deployment")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, deploy)).To(Succeed())
			for key, value := range expectedLabels {
				Expect(deploy.Labels).To(HaveKeyWithValue(key, value))
			}
			for key, value := range expectedLabels {
				Expect(deploy.Spec.Template.Labels).To(HaveKeyWithValue(key, value))
			}
		})

		It("should update Deployment when spec.image changes", func() {
			By("reconciling to create initial resources")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("updating the ValkeyNode image")
			Expect(k8sClient.Get(testCtx, typeNamespacedName, valkeyNode)).To(Succeed())
			valkeyNode.Spec.Image = "valkey/valkey:8.1"
			Expect(k8sClient.Update(testCtx, valkeyNode)).To(Succeed())

			By("reconciling the updated ValkeyNode")
			_, err = reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Deployment image was updated")
			deploy := &appsv1.Deployment{}
			Eventually(func() string {
				if err := k8sClient.Get(testCtx, managedResourceNamespaced, deploy); err != nil {
					return ""
				}
				if len(deploy.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return deploy.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("valkey/valkey:8.1"))
		})

		It("should set owner reference on Deployment", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying owner reference on Deployment")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, deploy)).To(Succeed())

			Expect(deploy.OwnerReferences).To(HaveLen(1))
			Expect(deploy.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(deploy.OwnerReferences[0].Kind).To(Equal("ValkeyNode"))
		})
	})

	Context("When managing ConfigMaps", func() {
		const resourceName = "test-configmap-valkeynode"
		const managedResourceName = "valkey-" + resourceName

		var (
			typeNamespacedName        types.NamespacedName
			managedResourceNamespaced types.NamespacedName
			valkeyNode                *valkeyiov1alpha1.ValkeyNode
		)

		BeforeEach(func() {
			typeNamespacedName = types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}
			managedResourceNamespaced = types.NamespacedName{
				Name:      managedResourceName,
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

		It("should create ConfigMap when ValkeyNode is created", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the ConfigMap was created")
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, managedResourceNamespaced, cm)
			}, timeout, interval).Should(Succeed())

			Expect(cm.Data).To(HaveKey("valkey.conf"))
			configContent := cm.Data["valkey.conf"]
			Expect(configContent).To(ContainSubstring("bind 0.0.0.0"))
			Expect(configContent).To(ContainSubstring("protected-mode no"))
			Expect(configContent).To(ContainSubstring("replica-announce-ip"))
			Expect(configContent).To(ContainSubstring("replica-announce-port 6379"))
		})

		It("should update ConfigMap when spec.config changes", func() {
			By("reconciling to create initial resources")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("updating the ValkeyNode config")
			Expect(k8sClient.Get(testCtx, typeNamespacedName, valkeyNode)).To(Succeed())
			valkeyNode.Spec.Config = map[string]string{
				"maxmemory":        "2gb",
				"maxmemory-policy": "volatile-lru",
			}
			Expect(k8sClient.Update(testCtx, valkeyNode)).To(Succeed())

			By("reconciling the updated ValkeyNode")
			_, err = reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the ConfigMap was updated")
			cm := &corev1.ConfigMap{}
			Eventually(func() bool {
				if err := k8sClient.Get(testCtx, managedResourceNamespaced, cm); err != nil {
					return false
				}
				return cm.Data["valkey.conf"] != ""
			}, timeout, interval).Should(BeTrue())

			configContent := cm.Data["valkey.conf"]
			Expect(configContent).To(ContainSubstring("maxmemory 2gb"))
			Expect(configContent).To(ContainSubstring("maxmemory-policy volatile-lru"))
		})

		It("should set owner reference on ConfigMap", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying owner reference on ConfigMap")
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, cm)).To(Succeed())

			Expect(cm.OwnerReferences).To(HaveLen(1))
			Expect(cm.OwnerReferences[0].Name).To(Equal(resourceName))
			Expect(cm.OwnerReferences[0].Kind).To(Equal("ValkeyNode"))
		})

		It("should create StatefulSet with config volume mounted", func() {
			By("reconciling the ValkeyNode")
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet has config volume")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(testCtx, managedResourceNamespaced, sts)).To(Succeed())

			// Verify volume exists
			var configVolumeFound bool
			for _, vol := range sts.Spec.Template.Spec.Volumes {
				if vol.Name == "config" && vol.ConfigMap != nil {
					Expect(vol.ConfigMap.Name).To(Equal(managedResourceName))
					configVolumeFound = true
					break
				}
			}
			Expect(configVolumeFound).To(BeTrue(), "config volume should exist")

			// Verify volume mount exists
			container := sts.Spec.Template.Spec.Containers[0]
			var mountFound bool
			for _, mount := range container.VolumeMounts {
				if mount.Name == "config" {
					Expect(mount.MountPath).To(Equal("/etc/valkey"))
					Expect(mount.ReadOnly).To(BeTrue())
					mountFound = true
					break
				}
			}
			Expect(mountFound).To(BeTrue(), "config volume mount should exist")

			// Verify command uses config file
			Expect(container.Command).To(Equal([]string{"valkey-server", "/etc/valkey/valkey.conf"}))
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
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "valkey-node"))
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

			Expect(svc.Name).To(Equal("valkey-test-node"))
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
			Expect(sts.Name).To(Equal("valkey-test-node"))
			Expect(sts.Namespace).To(Equal("test-ns"))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			Expect(sts.Spec.ServiceName).To(Equal("valkey-test-node"))

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

	Describe("buildDeployment", func() {
		It("creates correct deployment spec with probes", func() {
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

			deploy := buildDeployment(node)

			By("verifying basic properties")
			Expect(deploy.Name).To(Equal("valkey-test-node"))
			Expect(deploy.Namespace).To(Equal("test-ns"))
			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))

			By("verifying container spec")
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := deploy.Spec.Template.Spec.Containers[0]
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

			By("verifying liveness probe")
			Expect(container.LivenessProbe).NotTo(BeNil())
			Expect(container.LivenessProbe.TCPSocket).NotTo(BeNil())
			Expect(container.LivenessProbe.TCPSocket.Port.IntValue()).To(Equal(DefaultPort))
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
				},
			}

			deploy := buildDeployment(node)

			Expect(deploy.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("disktype", "ssd"))
			Expect(deploy.Spec.Template.Spec.Tolerations).To(HaveLen(1))
			Expect(deploy.Spec.Template.Spec.Tolerations[0].Key).To(Equal("dedicated"))
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

			deploy := buildDeployment(node)
			expectedLabels := valkeyNodeLabels(node)

			Expect(deploy.Labels).To(Equal(expectedLabels))
			Expect(deploy.Spec.Selector.MatchLabels).To(Equal(expectedLabels))
			Expect(deploy.Spec.Template.Labels).To(Equal(expectedLabels))
		})
	})

	Describe("buildConfigMap", func() {
		It("generates correct defaults", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "test-ns",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			cm := buildConfigMap(node)

			Expect(cm.Name).To(Equal("valkey-test-node"))
			Expect(cm.Namespace).To(Equal("test-ns"))
			Expect(cm.Data).To(HaveKey("valkey.conf"))

			configContent := cm.Data["valkey.conf"]
			Expect(configContent).To(ContainSubstring("bind 0.0.0.0"))
			Expect(configContent).To(ContainSubstring("protected-mode no"))
		})

		It("merges user config from spec.config", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					Config: map[string]string{
						"maxmemory":        "1gb",
						"maxmemory-policy": "allkeys-lru",
					},
				},
			}

			cm := buildConfigMap(node)
			configContent := cm.Data["valkey.conf"]

			Expect(configContent).To(ContainSubstring("maxmemory 1gb"))
			Expect(configContent).To(ContainSubstring("maxmemory-policy allkeys-lru"))
			// Defaults should still be present
			Expect(configContent).To(ContainSubstring("bind 0.0.0.0"))
		})

		It("always includes replica-announce-ip and replica-announce-port", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cache-0",
					Namespace: "prod",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			cm := buildConfigMap(node)
			configContent := cm.Data["valkey.conf"]

			Expect(configContent).To(ContainSubstring("replica-announce-ip valkey-cache-0.prod.svc.cluster.local"))
			Expect(configContent).To(ContainSubstring("replica-announce-port 6379"))
		})

		It("controller-managed settings cannot be overridden by user config", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					Config: map[string]string{
						"replica-announce-ip":   "malicious.example.com",
						"replica-announce-port": "9999",
					},
				},
			}

			cm := buildConfigMap(node)
			configContent := cm.Data["valkey.conf"]

			// Controller-managed settings should win
			Expect(configContent).To(ContainSubstring("replica-announce-ip valkey-test-node.default.svc.cluster.local"))
			Expect(configContent).To(ContainSubstring("replica-announce-port 6379"))
			// User values should NOT be present
			Expect(configContent).NotTo(ContainSubstring("malicious.example.com"))
			Expect(configContent).NotTo(ContainSubstring("9999"))
		})

		It("user config can override defaults", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-node",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
					Config: map[string]string{
						"protected-mode": "yes",
						"bind":           "127.0.0.1",
					},
				},
			}

			cm := buildConfigMap(node)
			configContent := cm.Data["valkey.conf"]

			// User overrides should win over defaults
			Expect(configContent).To(ContainSubstring("protected-mode yes"))
			Expect(configContent).To(ContainSubstring("bind 127.0.0.1"))
		})

		It("sets correct labels", func() {
			node := &valkeyiov1alpha1.ValkeyNode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "label-test",
					Namespace: "default",
				},
				Spec: valkeyiov1alpha1.ValkeyNodeSpec{
					Image: "valkey/valkey:8.0",
				},
			}

			cm := buildConfigMap(node)
			expectedLabels := valkeyNodeLabels(node)

			Expect(cm.Labels).To(Equal(expectedLabels))
		})
	})
})
