# Valkey Operator CRD Design

This document outlines the CRD design for the Valkey Operator, supporting three deployment modes: standalone/replicated Valkey, Sentinel-managed failover, and distributed Valkey Cluster.

## Overview

### CRDs

| CRD | Purpose |
|-----|---------|
| `Valkey` | Standalone or replicated instance with progressive enhancement |
| `ValkeyPool` | Manages multiple identical Valkey shards for application-level sharding |
| `Sentinel` | Control plane for monitoring and failover orchestration |
| `ValkeyCluster` | Distributed sharded cluster with hash slots |

### Design Principles

1. **Progressive enhancement** - Users start with a simple Valkey and add replicas or Sentinel management as needs grow, without changing CRD types.

2. **Separation of concerns** - Sentinel is a separate CRD that selects Valkey instances to monitor. Valkey has no knowledge of Sentinel in its spec.

3. **Shared structs for stable fields** - Common Kubernetes primitives (scheduling, resources, image) use shared Go structs. Domain-specific fields are copied per CRD to allow independent evolution.

4. **Nested sub-problems** - Related fields are grouped (e.g., `persistence.rdb`, `tls.clusterBus`) rather than using flat field names.

5. **GitOps-friendly** - Use `+listType=map` markers where applicable to prevent infinite reconciliation loops.

## Glossary

| Term | Meaning |
|------|---------|
| **Primary** | The Valkey instance accepting writes |
| **Replica** | A Valkey instance replicating from a primary |
| **Shard** | A unit of data partitioning. In ValkeyPool: an independent Valkey (application routes). In ValkeyCluster: a hash slot partition (Valkey routes). |
| **Sentinel** | The control plane process that monitors Valkey and orchestrates failover |
| **Node** | A single Valkey process (standalone, primary, replica, or cluster node) |
| **Pool** | A collection of identical Valkey shards for application-level sharding |

## Shared Configuration

The following fields use shared Go structs across all CRDs:

### CommonPodSpec

```go
type CommonPodSpec struct {
    NodeSelector                  map[string]string
    Tolerations                   []corev1.Toleration
    Affinity                      *corev1.Affinity
    TopologySpreadConstraints     []corev1.TopologySpreadConstraint
    SecurityContext               *corev1.PodSecurityContext
    ServiceAccountName            string
    ImagePullSecrets              []corev1.LocalObjectReference
}
```

### CommonContainerSpec

```go
type CommonContainerSpec struct {
    Image           string
    ImagePullPolicy corev1.PullPolicy
    Resources       corev1.ResourceRequirements
}
```

### ExporterSpec

```go
type ExporterSpec struct {
    // +kubebuilder:default=true
    Enabled   bool
    Image     string
    Resources corev1.ResourceRequirements
    Port      int32
}
```

---

## Valkey CRD

Manages standalone or replicated Valkey instances. Supports progressive enhancement from standalone to replicated to Sentinel-managed.

### Spec

```yaml
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: my-cache
spec:
  # Number of replica instances (0 = standalone, N = 1 primary + N replicas)
  # +kubebuilder:validation:Minimum=0
  # +kubebuilder:default=0
  replicas: 2

  image: valkey/valkey:8.0

  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"
    limits:
      memory: "1Gi"

  # Valkey configuration (maps to valkey.conf)
  config:
    maxmemory: "512mb"
    maxmemory-policy: "allkeys-lru"

  persistence:
    enabled: true
    storage:
      size: "10Gi"
      storageClassName: "fast-ssd"
    rdb:
      enabled: true
      savePolicy:
        - seconds: 900
          changes: 1
        - seconds: 300
          changes: 10
    aof:
      enabled: true
      fsync: "everysec"  # always | everysec | no

  scheduling:
    nodeSelector:
      disktype: ssd
    tolerations:
      - key: "dedicated"
        operator: "Equal"
        value: "valkey"
        effect: "NoSchedule"
    affinity: {}
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: topology.kubernetes.io/zone
        whenUnsatisfiable: ScheduleAnyway

  tls:
    enabled: true
    certificateSecret:
      name: valkey-tls-certs
    certKey: tls.crt
    keyKey: tls.key
    caKey: ca.crt
    clientAuth: require  # none | request | require

  exporter:
    enabled: true
    image: oliver006/redis_exporter:v1.62.0
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
    port: 9121
```

### Status

```yaml
status:
  state: Ready  # Initializing | Reconciling | Ready | Degraded | Failed
  reason: ReplicationHealthy
  message: "Primary and 2 replicas are healthy"

  topology:
    primary: my-cache-0
    replicas:
      - my-cache-1
      - my-cache-2

  connection:
    primary:
      host: my-cache-primary.default.svc.cluster.local
      port: 6379
    replicas:
      host: my-cache-replicas.default.svc.cluster.local
      port: 6379
    sentinel:  # Only populated when monitored by Sentinel
      hosts:
        - shared-sentinel-0.sentinel.svc.cluster.local:26379
        - shared-sentinel-1.sentinel.svc.cluster.local:26379
      masterName: my-cache

  monitoredBy:  # Only populated when monitored by Sentinel
    name: shared-sentinel
    namespace: default

  conditions:
    - type: Ready
      status: "True"
      reason: AllNodesHealthy
      message: "All nodes are healthy and replicating"
      lastTransitionTime: "2025-01-19T10:30:00Z"
      observedGeneration: 3
    - type: ReplicationHealthy
      status: "True"
      reason: SyncComplete
```

### Services Created

| Service | Purpose |
|---------|---------|
| `<name>-primary` | Routes to current primary (read-write) |
| `<name>-replicas` | Load-balances across replicas (read-only) |

For standalone (`replicas: 0`), only a single service `<name>` is created.

---

## ValkeyPool CRD

Manages multiple identical Valkey shards for application-level sharding. The application decides which shard to use (e.g., by hashing user ID). Each shard is an independent Valkey with its own primary and replicas.

### Architecture

ValkeyPool creates child `Valkey` CRs as owned resources:
- Pool `cache` with `shards: 3` creates: `cache-0`, `cache-1`, `cache-2` Valkey CRs
- Each child Valkey CR is managed by the Valkey controller
- ValkeyPool status summarizes child states (no duplication)
- Owner references ensure garbage collection when pool is deleted

### Spec

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyPool
metadata:
  name: cache
spec:
  # Number of independent Valkey shards
  # Creates: cache-0, cache-1, cache-2
  # +kubebuilder:validation:Minimum=1
  shards: 3

  # Template applied to each shard
  template:
    metadata:
      labels:
        tier: production
      annotations:
        sentinel.valkey.io/down-after: "10s"

    spec:
      replicas: 2
      image: valkey/valkey:8.0

      resources:
        requests:
          memory: "256Mi"
          cpu: "100m"

      config:
        maxmemory: "1gb"
        maxmemory-policy: "allkeys-lru"

      persistence:
        enabled: true
        storage:
          size: "10Gi"

      scheduling:
        topologySpreadConstraints:
          - maxSkew: 1
            topologyKey: topology.kubernetes.io/zone
            whenUnsatisfiable: ScheduleAnyway

      tls:
        enabled: true
        certificateSecret:
          name: cache-tls

      exporter:
        enabled: true
```

### Status

ValkeyPool status is minimal to avoid duplication. Detailed status lives in child Valkey CRs.

```yaml
status:
  state: Ready  # Initializing | Reconciling | Ready | Degraded | Failed
  reason: AllShardsHealthy
  message: "3/3 shards healthy"

  # Shard counts
  shards: 3
  readyShards: 3

  # Per-shard references (not full status)
  # +listType=map
  # +listMapKey=name
  shardRefs:
    - name: cache-0
      namespace: default
      state: Ready
    - name: cache-1
      namespace: default
      state: Ready
    - name: cache-2
      namespace: default
      state: Degraded
      reason: MissingReplica

  conditions:
    - type: Ready
      status: "False"
      reason: ShardDegraded
      message: "2/3 shards ready, 1 degraded"
      observedGeneration: 2
```

### Scaling

To scale a pool:
1. Update `spec.shards` (e.g., 3 → 6)
2. ValkeyPool controller creates new child Valkey CRs (`cache-3`, `cache-4`, `cache-5`)
3. Sentinel automatically discovers new shards via label selector

To scale down:
1. Update `spec.shards` (e.g., 6 → 3)
2. ValkeyPool controller deletes highest-indexed children (`cache-5`, `cache-4`, `cache-3`)
3. Sentinel stops monitoring deleted shards

### Sentinel Integration

Child Valkey shards inherit labels from `template.metadata.labels`. A Sentinel can monitor all pool members:

```yaml
apiVersion: valkey.io/v1alpha1
kind: Sentinel
metadata:
  name: cache-sentinel
spec:
  replicas: 3
  selector:
    matchLabels:
      tier: production  # Matches labels from ValkeyPool template
```

---

## Sentinel CRD

Manages Sentinel instances that monitor and orchestrate failover for Valkey instances.

### Spec

```yaml
apiVersion: valkey.io/v1alpha1
kind: Sentinel
metadata:
  name: shared-sentinel
spec:
  # Number of Sentinel instances (should be odd for quorum)
  # +kubebuilder:validation:Minimum=3
  replicas: 3

  image: valkey/valkey:8.0

  resources:
    requests:
      memory: "64Mi"
      cpu: "50m"

  # Selector for Valkey instances to monitor
  selector:
    matchLabels:
      tier: production
    matchNamespaces:
      - default
      - production

  # Default monitoring settings
  # Overridable per-Valkey via sentinel.valkey.io/* annotations
  monitoring:
    downAfter: 30s
    failoverTimeout: 180s
    parallelSyncs: 1
    quorum: 2

  scheduling:
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: topology.kubernetes.io/zone
        whenUnsatisfiable: DoNotSchedule

  tls:
    enabled: true
    certificateSecret:
      name: sentinel-tls

  exporter:
    enabled: true
```

### Per-Valkey Monitoring Overrides

Valkey instances can override Sentinel monitoring settings via annotations:

| Annotation | Default | Description |
|------------|---------|-------------|
| `sentinel.valkey.io/down-after` | from Sentinel spec | Time before marking primary as down |
| `sentinel.valkey.io/failover-timeout` | from Sentinel spec | Failover operation timeout |
| `sentinel.valkey.io/parallel-syncs` | from Sentinel spec | Replicas syncing simultaneously |
| `sentinel.valkey.io/quorum` | from Sentinel spec | Sentinels needed to agree on failure |

Example:

```yaml
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: critical-cache
  labels:
    tier: production
  annotations:
    sentinel.valkey.io/down-after: "10s"
    sentinel.valkey.io/failover-timeout: "60s"
spec:
  replicas: 2
```

### Status

```yaml
status:
  state: Ready  # Initializing | Ready | Degraded | Failed

  replicas: 3
  readyReplicas: 3

  monitoredInstances:
    - name: critical-cache
      namespace: default
      state: Healthy
      primary: critical-cache-0
      replicas: 2
      lastFailover: "2025-01-15T08:30:00Z"
    - name: session-store
      namespace: production
      state: Healthy
      primary: session-store-0
      replicas: 1

  conditions:
    - type: Ready
      status: "True"
      reason: QuorumMet
      message: "3/3 Sentinels healthy, monitoring 2 Valkey instances"
    - type: QuorumHealthy
      status: "True"
      reason: AllSentinelsReachable
```

---

## ValkeyCluster CRD

Manages distributed Valkey Cluster with hash slot sharding.

### Spec

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: my-cluster
spec:
  # Number of shards (minimum 3 for Valkey Cluster)
  # +kubebuilder:validation:Minimum=3
  shards: 3

  # Replicas per shard (0 = no HA)
  # +kubebuilder:validation:Minimum=0
  replicasPerShard: 1

  # Total pods = shards × (1 + replicasPerShard)

  image: valkey/valkey:8.0

  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"

  config:
    maxmemory: "512mb"
    maxmemory-policy: "allkeys-lru"

  cluster:
    nodeTimeout: 15s
    replicaReadOnly: true
    allowReadsWhenDown: false
    migrationBarrier: 1

  persistence:
    enabled: true
    storage:
      size: "10Gi"
      storageClassName: "fast-ssd"
    rdb:
      enabled: true
      savePolicy:
        - seconds: 900
          changes: 1
    aof:
      enabled: true
      fsync: "everysec"

  scheduling:
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: topology.kubernetes.io/zone
        whenUnsatisfiable: ScheduleAnyway

  tls:
    enabled: true
    certificateSecret:
      name: cluster-tls
    clusterBus:
      enabled: true

  exporter:
    enabled: true
```

### Status

```yaml
status:
  state: Ready  # Initializing | Reconciling | Ready | Degraded | Failed
  reason: ClusterHealthy
  message: "All shards healthy, all slots assigned"

  shards: 3
  readyShards: 3

  slots:
    assigned: 16384
    total: 16384

  shardDetails:
    - index: 0
      slots: "0-5460"
      primary: my-cluster-0
      replicas:
        - my-cluster-3
      state: Healthy
    - index: 1
      slots: "5461-10922"
      primary: my-cluster-1
      replicas:
        - my-cluster-4
      state: Healthy
    - index: 2
      slots: "10923-16383"
      primary: my-cluster-2
      replicas:
        - my-cluster-5
      state: Healthy

  connection:
    seeds:
      - my-cluster-0.my-cluster.default.svc.cluster.local:6379
      - my-cluster-1.my-cluster.default.svc.cluster.local:6379
      - my-cluster-2.my-cluster.default.svc.cluster.local:6379

  conditions:
    - type: Ready
      status: "True"
    - type: ClusterFormed
      status: "True"
      reason: TopologyComplete
    - type: SlotsAssigned
      status: "True"
      reason: AllSlotsAssigned
```

---

## References

- [Kubernetes CRD Design for the Long Haul](https://kccnceu2025.sched.com/) - ClusterAPI maintainers talk on CRD design best practices
- [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator) - Reference for CRD patterns
- [GitHub Discussion #19](https://github.com/valkey-io/valkey-operator/discussions/19) - Original design considerations
- [Kubebuilder Markers](https://book.kubebuilder.io/reference/markers) - Validation and CRD generation markers
