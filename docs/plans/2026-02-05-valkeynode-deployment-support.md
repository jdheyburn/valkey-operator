# ValkeyNode Deployment Support

**Date:** 2026-02-05
**Status:** Ready for Implementation
**Working Directory:** `/Users/joseph.heyburn/code/valkey-operator_jdheyburn/.worktrees/valkeynode-mvp`
**Branch:** `feature/valkeynode-mvp`

---

## Background

ValkeyNode is an internal CRD that creates single-pod Valkey deployments. Currently it only supports StatefulSet. Organizations may have requirements to use Deployments over StatefulSets, so we need to add support for both workload types.

---

## Key Files

| File | Purpose |
|------|---------|
| `api/v1alpha1/valkeynode_types.go` | CRD type definitions (ValkeyNodeSpec, ValkeyNodeStatus) |
| `internal/controller/valkeynode_resources.go` | Resource builders (`buildStatefulSet`, `buildHeadlessService`, `valkeyNodeLabels`, `valkeyNodeResourceName`) |
| `internal/controller/valkeynode_controller.go` | Reconciler (`ensureService`, `ensureStatefulSet`, `updateStatus`) |
| `internal/controller/valkeynode_controller_test.go` | Integration and unit tests |

---

## Design Decisions

1. **Workload type selection:** Add `spec.workloadType` enum field with values:
   - `StatefulSet` (default)
   - `Deployment`

2. **Service behavior:** Keep creating headless Service regardless of workload type (consistent abstraction)

3. **Naming convention:** Resources use `valkey-{node.Name}` prefix (e.g., `valkey-sample-0` for pod)

---

## Implementation Tasks

### Task 1: Update Types (`api/v1alpha1/valkeynode_types.go`)

Add the WorkloadType enum and field to ValkeyNodeSpec:

```go
// WorkloadType specifies the type of workload to create for the ValkeyNode.
// +kubebuilder:validation:Enum=StatefulSet;Deployment
type WorkloadType string

const (
    // WorkloadTypeStatefulSet creates a StatefulSet with stable network identity.
    WorkloadTypeStatefulSet WorkloadType = "StatefulSet"
    // WorkloadTypeDeployment creates a Deployment for simpler, stateless workloads.
    WorkloadTypeDeployment WorkloadType = "Deployment"
)
```

Add to ValkeyNodeSpec struct:

```go
// WorkloadType specifies whether to create a StatefulSet or Deployment.
// StatefulSet provides stable network identity, Deployment is simpler.
// +kubebuilder:default=StatefulSet
// +optional
WorkloadType WorkloadType `json:"workloadType,omitempty"`
```

Run `make generate && make manifests` after changes.

### Task 2: Add Deployment Builder (`internal/controller/valkeynode_resources.go`)

Create `buildDeployment(node)` function similar to `buildStatefulSet`:

```go
// buildDeployment creates a Deployment for the ValkeyNode.
func buildDeployment(node *valkeyiov1alpha1.ValkeyNode) *appsv1.Deployment {
    replicas := int32(1)
    labels := valkeyNodeLabels(node)
    resourceName := valkeyNodeResourceName(node)

    return &appsv1.Deployment{
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

### Task 3: Update Controller (`internal/controller/valkeynode_controller.go`)

#### 3a: Add RBAC marker for Deployments

```go
// +kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch;create;update;patch;delete
```

#### 3b: Add `ensureDeployment` method

Similar to `ensureStatefulSet`, but for Deployments.

#### 3c: Update `Reconcile` method

Check `spec.workloadType` and call appropriate ensure method:

```go
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
```

#### 3d: Update `updateStatus` method

For Deployment, pod names are generated (not predictable). List pods by label selector:

```go
if node.Spec.WorkloadType == valkeyiov1alpha1.WorkloadTypeDeployment {
    // Get Deployment
    deploy := &appsv1.Deployment{}
    if err := r.Get(ctx, client.ObjectKey{Namespace: node.Namespace, Name: resourceName}, deploy); err != nil {
        // handle error
    }
    // Check deployment readiness
    deployReady := deploy.Status.ReadyReplicas >= 1

    // List pods by label to find the pod name/IP
    podList := &corev1.PodList{}
    if err := r.List(ctx, podList,
        client.InNamespace(node.Namespace),
        client.MatchingLabels(valkeyNodeLabels(node))); err != nil {
        // handle error
    }
    if len(podList.Items) > 0 {
        pod := &podList.Items[0]
        node.Status.PodName = pod.Name
        node.Status.PodIP = pod.Status.PodIP
        // check pod readiness...
    }
} else {
    // existing StatefulSet logic
}
```

#### 3e: Update `SetupWithManager`

Add Deployment to owned resources:

```go
func (r *ValkeyNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&valkeyiov1alpha1.ValkeyNode{}).
        Owns(&corev1.Service{}).
        Owns(&appsv1.StatefulSet{}).
        Owns(&appsv1.Deployment{}).
        Named("valkeynode").
        Complete(r)
}
```

### Task 4: Update Tests (`internal/controller/valkeynode_controller_test.go`)

Add tests for:
- Creating Deployment when `workloadType: Deployment`
- Verifying Deployment spec matches ValkeyNode spec
- Status updates work correctly for Deployment
- Image updates propagate to Deployment

### Task 5: Update Sample CR

Add a Deployment example to `config/samples/v1alpha1_valkeynode.yaml` or create a separate sample.

---

## Commands

```bash
# After changing types
make generate && make manifests

# Run tests
go test ./internal/controller/... -count=1

# Build
make build

# Install CRDs
make install
```

---

## Notes

- Deployment pod names are non-deterministic (e.g., `valkey-sample-7d8f9c6b4-x2k9p`), unlike StatefulSet (`valkey-sample-0`)
- The headless Service still works for both - for Deployment it resolves to the pod IP
- Consider whether switching workload types on an existing ValkeyNode should be allowed (probably not - would require delete/recreate)
