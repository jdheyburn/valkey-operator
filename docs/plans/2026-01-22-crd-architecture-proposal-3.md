# Valkey Operator CRD Architecture - Design Proposal 3

**Date:** 2026-01-22
**Status:** Proposed
**Authors:** Claude Code (with user feedback)

## Table of Contents

1. [Overview](#overview)
2. [CRD Architecture](#crd-architecture)
3. [Design Section 1: Architecture Overview](#design-section-1-architecture-overview)
4. [Design Section 2: ValkeyNode CRD](#design-section-2-valkeynode-crd)
5. [Design Section 3: Valkey CRD](#design-section-3-valkey-crd)
6. [Design Section 4: ValkeyPool CRD](#design-section-4-valkeypool-crd)
7. [Design Section 5: ValkeyCluster CRD](#design-section-5-valkeycluster-crd)
8. [Design Section 6: Sentinel CRD](#design-section-6-sentinel-crd)
9. [Key Design Decisions](#key-design-decisions)
10. [Design Rejections](#design-rejections)
11. [TODOs and Future Enhancements](#todos-and-future-enhancements)
12. [Migration Guide](#migration-guide)

---

## Overview

The Valkey Operator uses a **five-CRD architecture** designed for flexibility, Kubernetes-native conventions, and natural upgrade paths from simple to complex deployments.

### Design Goals

1. **User-first approach**: Deploy functional, highly available Valkey clusters with minimal configuration
2. **Kubernetes-native**: Follow established Kubernetes conventions for consistency
3. **Production focus**: Prioritize cluster and replication modes for production workloads
4. **Composability**: Integrate with cert-manager, Prometheus Operator, and ecosystem tools
5. **Upgrade paths**: Enable seamless progression from standalone → replicated → HA → sharded

### CRD Summary

| CRD | Purpose | User-Facing | Key Fields |
|-----|---------|-------------|------------|
| **ValkeyNode** | Single-pod abstraction | ❌ No (operator-managed) | type, podManagementType, valkeyConfig, sentinelConfig |
| **Valkey** | Standalone/replicated instance | ✅ Yes | replicas, sentinel, azPlacement |
| **ValkeyPool** | Multiple independent instances | ✅ Yes | shards, template, azDistribution |
| **ValkeyCluster** | Sharded cluster mode | ✅ Yes | shards, replicas, azDistribution |
| **Sentinel** | HA monitoring and failover | ✅ Yes | replicas, quorum, defaultConfig |

---

## CRD Architecture

### Resource Hierarchy

```
User Creates:
├─ ValkeyPool
│  └─ Creates: Multiple Valkey resources
│     └─ Creates: ValkeyNode per pod
│        └─ Creates: Deployment/StatefulSet + Service + PVC
│
├─ Valkey
│  └─ Creates: ValkeyNode per pod
│     └─ Creates: Deployment/StatefulSet + Service + PVC
│
├─ ValkeyCluster
│  └─ Creates: ValkeyNode per pod
│     └─ Creates: Deployment/StatefulSet + Service + PVC
│
└─ Sentinel
   └─ Creates: ValkeyNode per Sentinel pod
      └─ Creates: Deployment/StatefulSet + Service
```

### Controller Responsibilities

| Controller | Responsibilities |
|------------|------------------|
| **ValkeyNode** | Create/manage Deployment or StatefulSet (replicas=1), Service, PVC; Configure Valkey/Sentinel; Update status |
| **Valkey** | Create ValkeyNodes based on replicas; Configure replication topology; Register with Sentinel; Handle operator-managed failover |
| **ValkeyPool** | Create child Valkey resources; Inject AZ affinity; Handle shard scaling; Aggregate status |
| **ValkeyCluster** | Create ValkeyNodes for shards; Assign hash slots; Initialize cluster; Handle slot migration; Inject AZ affinity |
| **Sentinel** | Create ValkeyNodes for Sentinels; Watch monitoredInstances; Configure Sentinel monitoring; Handle Sentinel scaling |

---

## Design Section 1: Architecture Overview

The operator uses a **five-CRD architecture** with clear separation of concerns:

### 1. ValkeyCluster

- **Purpose**: Sharded Valkey Cluster mode with distributed hash slots
- **Manages**: Multiple shards, each with primary + replicas
- **Use case**: Production workloads requiring server-side sharding
- **AZ distribution**: Primaries round-robin across zones, replicas in different zones from primary

### 2. ValkeyPool (new)

- **Purpose**: Multiple independent Valkey instances sharing configuration
- **Manages**: N independent Valkey resources (client-side sharding)
- **Key fields**:
  - `shards`: Number of independent Valkey instances to create
  - `template`: Valkey spec template (replicas, resources, sentinelRef, etc.)
  - `azDistribution`: Optional zones list for round-robin primary placement
- **Creates**: N child Valkey resources (e.g., `mypool-0`, `mypool-1`, ...)
- **Use case**: Horizontal scaling with client-side partitioning, simpler than Cluster mode

### 3. Valkey (new)

- **Purpose**: Single Valkey instance (standalone or replicated)
- **Key fields**:
  - `replicas` (default 1): 1 = standalone, ≥2 = replication
  - `sentinelRef` (optional): Reference to Sentinel for HA failover
- **Upgrade path**: standalone → replicated → Sentinel-managed
- **Created by**: Users directly, or by ValkeyPool controller
- **Failover**: Operator-managed (no sentinelRef) or Sentinel-managed (with sentinelRef)

### 4. Sentinel (new)

- **Purpose**: HA monitoring and automatic failover
- **Discovery**: Valkey instances reference Sentinel via `sentinelRef`
- **Can monitor**: Multiple Valkey instances (one-to-many)
- **Deploys**: Typically 3+ Sentinel pods for quorum

### 5. ValkeyNode (new, operator-managed only)

- **Purpose**: Internal single-pod abstraction
- **Not user-facing**: Created by Valkey/ValkeyCluster/Sentinel controllers
- **Abstracts**: Deployment vs StatefulSet, pod template, volume, service
- **Type field**: `valkey` or `sentinel` (generic for both)
- **Manages**: Creates Deployment/StatefulSet + Service + PVC

---

## Design Section 2: ValkeyNode CRD

ValkeyNode is the operator-managed abstraction for single-pod deployments. Users never create these directly.

### Specification

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyNode
metadata:
  name: myvalkey-primary-0
  namespace: default
  ownerReferences: # Set by parent controller (Valkey/ValkeyCluster/Sentinel)
    - apiVersion: valkey.io/v1alpha1
      kind: Valkey
      name: myvalkey
      controller: true
spec:
  # Type of node - determines reconciliation behavior
  # +kubebuilder:validation:Enum=valkey;sentinel
  type: valkey

  # Pod management strategy
  # +kubebuilder:validation:Enum=deployment;statefulset
  # +kubebuilder:default=statefulset
  podManagementType: statefulset

  # Pod template specification
  podTemplate:
    metadata:
      labels: {}
      annotations: {}
    spec:
      # Standard pod spec fields
      nodeSelector: {}
      affinity: {}
      tolerations: []
      securityContext: {}
      containers:
        - name: valkey
          image: valkey/valkey:8.0
          resources: {}
          env: []
          # ... standard container spec
      initContainers: []
      volumes: []

  # Persistent volume configuration (optional)
  persistence:
    enabled: true
    size: 10Gi
    storageClassName: gp3
    # Operator creates PVC separately, references in pod

  # Service configuration (created per pod)
  service:
    type: ClusterIP
    annotations: {}
    # Service name: <valkeynode-name>-svc

  # Valkey-specific configuration (only when type=valkey)
  valkeyConfig:
    # Cluster configuration (only relevant when enabled)
    cluster:
      # Enable cluster mode
      enabled: false

      # Hash slots assigned to this node (only when enabled: true)
      # Empty for non-cluster nodes or cluster nodes awaiting slot assignment
      slots: []  # e.g., ["0-5460"]

    # Replication configuration
    # CRITICAL: Role is NOT specified in spec. Role is derived from replicaOf.
    # - replicaOf: "" (empty) → This node is a PRIMARY
    # - replicaOf: "hostname:port" → This node is a REPLICA of the specified primary
    #
    # This design allows failovers to change topology without spec conflicts.
    # Parent controllers (Valkey/ValkeyCluster) manage topology by updating this field.
    replicaOf: ""  # Empty string = primary, populated = replica

  # Sentinel-specific configuration (only when type=sentinel)
  sentinelConfig:
    quorum: 2
    monitoredInstances: []  # List of Valkey instances to monitor

status:
  # Standard Kubernetes conditions
  conditions: []

  # Pod reference
  podName: myvalkey-primary-0-xyz
  podIP: 10.0.1.5

  # Service reference
  serviceName: myvalkey-primary-0-svc
  serviceIP: 10.0.1.10

  # PVC reference (if persistence enabled)
  pvcName: myvalkey-primary-0-data

  # Deployment/StatefulSet reference
  managedResourceName: myvalkey-primary-0-sts
  managedResourceKind: StatefulSet

  # Observed replication state (queried via INFO replication)
  # This represents reality, which may temporarily differ from spec during failovers
  observedRole: primary  # "primary" or "replica"
  observedReplicaOf: ""  # What Valkey reports it's replicating from

  # Overall ready state
  ready: true
```

### Replication Role Semantics

**Design Principle: Spec describes relationships, not roles**

The `role` field is deliberately **absent from spec** because:
1. **Failovers change roles** - Sentinel or operator promotes replicas to primaries
2. **Spec represents desired state** - Controllers reconcile toward spec
3. **Role conflicts with failover** - If spec says "role: primary" but Sentinel promotes a different node, reconciliation would fight the failover

**Instead, role is derived from the `replicaOf` field:**
- `replicaOf: ""` (empty) → Node acts as a **primary** (accepts writes)
- `replicaOf: "host:port"` → Node acts as a **replica** (replicates from specified host)

**Status tracks observed reality** via `observedRole` and `observedReplicaOf` (queried from `INFO replication` command). During failovers, status may temporarily differ from spec until reconciliation completes.

### Failover Semantics

#### Sentinel-Managed Failover (when Valkey.spec.sentinelRef is set)

1. **Sentinel detects failure**: Primary ValkeyNode becomes unavailable
2. **Sentinel initiates failover**: Promotes a replica (e.g., node-1) to primary
3. **Sentinel reconfigures Valkey**: Runs `REPLICAOF` commands directly on Valkey instances
   - Promoted node: `REPLICAOF NO ONE` (becomes primary)
   - Other replicas: `REPLICAOF node-1-svc 6379` (replicate from new primary)
4. **ValkeyNode controllers observe change**: Query `INFO replication` and update status
   - node-1: `status.observedRole = "primary"`, `status.observedReplicaOf = ""`
   - Other nodes: `status.observedRole = "replica"`, `status.observedReplicaOf = "node-1-svc:6379"`
5. **Parent Valkey controller detects status change**: Sees topology changed
6. **Valkey controller updates ValkeyNode specs to match reality**:
   - node-1: `spec.valkeyConfig.replicaOf = ""`
   - node-0: `spec.valkeyConfig.replicaOf = "node-1-svc:6379"`
   - node-2: `spec.valkeyConfig.replicaOf = "node-1-svc:6379"`
7. **ValkeyNode controllers reconcile**: Likely no-op since Sentinel already reconfigured
8. **Spec now matches reality**: System is in consistent state

**Key insight**: Sentinel makes decisions and reconfigures Valkey directly. Operator **follows** by updating spec to match the new topology. **Spec follows reality**.

#### Operator-Managed Failover (when Valkey.spec.sentinelRef is NOT set)

1. **Valkey controller detects failure**: Primary ValkeyNode shows `ready: false` or pod is unavailable
2. **Valkey controller selects replica to promote**: Picks best candidate (e.g., node-1 with least replication lag)
3. **Valkey controller updates ValkeyNode specs**:
   - node-1: `spec.valkeyConfig.replicaOf = ""`
   - node-0 (failed): `spec.valkeyConfig.replicaOf = "node-1-svc:6379"` (if it recovers)
   - node-2: `spec.valkeyConfig.replicaOf = "node-1-svc:6379"`
4. **ValkeyNode controllers reconcile**:
   - node-1 controller: Runs `REPLICAOF NO ONE` on node-1 pod
   - node-2 controller: Runs `REPLICAOF node-1-svc 6379` on node-2 pod
5. **ValkeyNode controllers update status**: After running commands, query `INFO replication`
   - node-1: `status.observedRole = "primary"`
   - node-2: `status.observedRole = "replica"`, `status.observedReplicaOf = "node-1-svc:6379"`
6. **Spec and status converge**: System reaches consistent state

**Key insight**: Operator makes decisions by updating spec. ValkeyNode controllers **implement** the changes via reconciliation. **Spec leads, reality follows**.

### ValkeyNode Controller Responsibilities

The ValkeyNode controller is **deliberately "dumb"** - it reconciles infrastructure without making topology decisions:

1. **Create/update pod workload**: Deployment or StatefulSet (with replicas=1)
2. **Create Service**: ClusterIP with stable DNS name `<valkeynode-name>-svc`
3. **Create PVC**: If persistence enabled, separate from volumeClaimTemplate
4. **Configure Valkey**: Translate spec fields into Valkey config:
   - `cluster.enabled: true` → `--cluster-enabled yes`
   - `replicaOf: "host:port"` → Run `REPLICAOF host port` command
   - `replicaOf: ""` → Run `REPLICAOF NO ONE` if currently a replica
   - `cluster.slots: ["0-5460"]` → Store in annotation for cluster initialization
5. **Update status**: Query pod state and Valkey `INFO` command
   - Pod ready state → `status.ready`
   - `INFO replication` → `status.observedRole`, `status.observedReplicaOf`
   - Resource names → `status.podName`, `status.serviceName`, etc.
6. **Handle AZ placement**: Apply affinity from parent-provided podTemplate

**ValkeyNode does NOT:**
- Decide which node should be primary (parent controller's job)
- Initiate failovers (Valkey/ValkeyCluster controller or Sentinel's job)
- Manage cluster slot assignment (ValkeyCluster controller's job)
- Create other ValkeyNodes (parent controller's job)

### Configuration-Driven Behavior

ValkeyNode's behavior is determined entirely by configuration fields, not by parent CRD type:

| Configuration | Resulting Behavior |
|---------------|-------------------|
| `cluster.enabled: true, cluster.slots: ["0-5460"]` | Valkey Cluster primary node |
| `cluster.enabled: true, replicaOf: "host:port"` | Valkey Cluster replica node |
| `cluster.enabled: false, replicaOf: ""` | Standalone primary |
| `cluster.enabled: false, replicaOf: "host:port"` | Replication replica |
| `type: sentinel` | Sentinel node |

Parent controller (Valkey/ValkeyCluster) determines appropriate configuration. ValkeyNode simply implements it.

---

## Design Section 3: Valkey CRD

Valkey is the user-facing CRD for standalone and replicated Valkey deployments. It provides a natural upgrade path from simple standalone to HA replicated setups.

### Specification

```yaml
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: myvalkey
  namespace: default
spec:
  # Valkey version/image
  image: valkey/valkey:8.0

  # Total number of pods
  # 0 = suspended (no pods, PVCs retained)
  # 1 = standalone (1 primary, no replication)
  # ≥2 = replication (1 primary + N replicas where N = replicas - 1)
  # +kubebuilder:validation:Minimum=0
  # +kubebuilder:default=1
  replicas: 1

  # Reference to Sentinel for HA failover management
  # If set: Sentinel manages failover (Sentinel promotes replicas)
  # If unset: Operator manages failover (operator promotes replicas)
  # Only valid when replicas ≥ 2 (requires primary + replica to monitor)
  # +optional
  sentinel:
    # Name of Sentinel resource in same namespace
    name: my-sentinel

    # Sentinel monitoring parameters (specific to this Valkey instance)
    # These override Sentinel's default config for this monitored instance
    # +optional
    config:
      # Time in milliseconds before Sentinel considers primary down
      # +kubebuilder:default="30000"
      down-after-milliseconds: "10000"

      # Timeout for failover operation
      # +kubebuilder:default="180000"
      failover-timeout: "180000"

      # Number of replicas that can sync from new primary simultaneously
      # +kubebuilder:default="1"
      parallel-syncs: "1"

  # Resource requirements for Valkey container
  # +optional
  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"
    limits:
      memory: "1Gi"
      cpu: "500m"

  # Persistent storage configuration
  # +optional
  persistence:
    enabled: true
    size: 10Gi
    storageClassName: gp3

  # Pod scheduling configuration
  # +optional
  nodeSelector: {}

  # +optional
  affinity: {}

  # +optional
  tolerations: []

  # Pod management strategy
  # +kubebuilder:validation:Enum=deployment;statefulset
  # +kubebuilder:default=statefulset
  podManagementType: statefulset

  # Metrics exporter configuration
  # +kubebuilder:default={enabled:true}
  # +optional
  exporter:
    enabled: true
    image: oliver006/redis_exporter:latest
    resources: {}

  # TLS configuration
  # +optional
  tls:
    enabled: false
    certificateRef:
      name: valkey-tls

  # Valkey configuration overrides
  # Applied via ConfigMap, live-reloaded when possible
  # +optional
  config:
    maxmemory: "512mb"
    maxmemory-policy: "allkeys-lru"

status:
  # High-level state
  # +kubebuilder:validation:Enum=Initializing;Ready;Degraded;Suspended;Failed
  state: Ready

  # Current topology
  primary: myvalkey-0
  replicas:
    - myvalkey-1
    - myvalkey-2

  # Ready replicas count
  readyReplicas: 3

  # Sentinel association
  sentinelManaged: true
  sentinelName: my-sentinel

  # Conditions
  # +listType=map
  # +listMapKey=type
  conditions: []

  # ValkeyNode references (operator-managed)
  nodes:
    - name: myvalkey-0
      role: primary
      ready: true
    - name: myvalkey-1
      role: replica
      ready: true
    - name: myvalkey-2
      role: replica
      ready: true
```

### Replicas Semantics (Kubernetes-Native)

**Following standard Kubernetes convention where `spec.replicas` = total number of pods:**

| spec.replicas | Behavior | Pods Created | Use Case |
|---------------|----------|--------------|----------|
| 0 | Suspended | 0 (PVCs retained) | Cost savings, temporary pause |
| 1 | Standalone | 1 primary | Development, testing, simple cache |
| 2 | Replication | 1 primary + 1 replica | Basic HA |
| 3 | Replication | 1 primary + 2 replicas | Production HA |

**Pod naming:** `<valkey-name>-<index>` (e.g., `myvalkey-0`, `myvalkey-1`, `myvalkey-2`)
- Pod 0 is always the initial primary
- Pods 1..N are replicas

### Upgrade Path Examples

#### Example 1: Standalone → Replication

```yaml
# Start with standalone
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: myapp-cache
spec:
  replicas: 1  # Standalone
  persistence:
    enabled: true
    size: 10Gi

# Scale to replication (just change replicas)
spec:
  replicas: 3  # 1 primary + 2 replicas
```

#### Example 2: Replication → Sentinel-Managed

```yaml
# Operator-managed replication
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: myapp-cache
spec:
  replicas: 3

# Add Sentinel for HA (just add sentinel field)
spec:
  replicas: 3
  sentinel:
    name: production-sentinel
```

#### Example 3: Suspend for Cost Savings

```yaml
# Running instance
spec:
  replicas: 3

# Suspend (scale to zero)
spec:
  replicas: 0  # All pods removed, PVCs retained

# Resume (same as before)
spec:
  replicas: 3  # Pods recreate with same data
```

### Valkey Controller Responsibilities

1. **Create ValkeyNodes**: Based on replicas count
   - `replicas: 0`: No ValkeyNodes (suspended state)
   - `replicas: 1`: Creates 1 ValkeyNode (primary, no replication)
   - `replicas: 3`: Creates 3 ValkeyNodes (1 primary + 2 replicas)
2. **Configure replication topology**:
   - Node 0: `replicaOf: ""` (primary)
   - Nodes 1..N: `replicaOf: "<name>-0-svc:6379"` (replicas)
3. **Register with Sentinel**: If `sentinel.name` is set, updates Sentinel's monitored instances
4. **Handle operator-managed failover**: If no Sentinel and primary fails:
   - Detects primary ValkeyNode unhealthy
   - Selects best replica to promote (least lag, higher index = lower priority)
   - Updates ValkeyNode specs: new primary gets `replicaOf: ""`, others point to new primary
5. **Manage ConfigMap**: Creates ConfigMap with Valkey config, mounts into pods
6. **Handle scaling**: Add/remove ValkeyNodes one at a time for controlled scaling
7. **Update status**: Tracks primary node, replica nodes, replication health, Sentinel association

### Validation Rules

```go
func (v *Valkey) ValidateCreate() error {
  // Sentinel requires replication
  if v.Spec.Sentinel != nil && v.Spec.Replicas < 2 {
    return errors.New("sentinel requires replicas >= 2 (primary + replica)")
  }

  return nil
}

func (v *Valkey) ValidateUpdate(old *Valkey) error {
  // Replicas can only change by ±1 per update (controlled scaling)
  diff := abs(v.Spec.Replicas - old.Spec.Replicas)
  if diff > 1 {
    return errors.New("replicas can only change by ±1 per update for safe scaling")
  }

  return nil
}
```

---

## Design Section 4: ValkeyPool CRD

ValkeyPool manages multiple independent Valkey instances (shards) sharing the same configuration. This enables client-side sharding for horizontal scalability.

### Specification

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyPool
metadata:
  name: mypool
  namespace: default
spec:
  # Number of independent Valkey instances (shards)
  # Each shard is a separate Valkey resource
  # +kubebuilder:validation:Minimum=0
  # +kubebuilder:default=3
  shards: 3

  # Template for Valkey instances
  # All shards share this configuration
  template:
    # All Valkey spec fields available
    image: valkey/valkey:8.0

    replicas: 3  # Each shard: 1 primary + 2 replicas

    # Optional: Sentinel reference applies to all shards
    sentinel:
      name: pool-sentinel
      config:
        down-after-milliseconds: "30000"

    persistence:
      enabled: true
      size: 10Gi
      storageClassName: gp3

    resources:
      requests:
        memory: "512Mi"
        cpu: "200m"
      limits:
        memory: "2Gi"
        cpu: "1000m"

    nodeSelector: {}
    affinity: {}
    tolerations: []

    podManagementType: statefulset

    exporter:
      enabled: true

    tls:
      enabled: false

    config:
      maxmemory: "1gb"
      maxmemory-policy: "allkeys-lru"

  # Availability zone distribution for primary pods
  # Primaries are distributed round-robin across zones
  # Replicas are placed in different zones from their primary (anti-affinity)
  # +optional
  azDistribution:
    # List of availability zones
    # Shard 0 primary → zones[0]
    # Shard 1 primary → zones[1]
    # Shard 2 primary → zones[2]
    # Shard 3 primary → zones[0] (wraps around)
    zones:
      - us-east-1a
      - us-east-1b
      - us-east-1c

    # Node label key used for zone selection
    # +kubebuilder:default="topology.kubernetes.io/zone"
    nodeLabel: "topology.kubernetes.io/zone"

    # Replica placement strategy
    # +kubebuilder:validation:Enum=anti-affinity;spread
    # +kubebuilder:default=anti-affinity
    # TODO: Support explicit per-replica zone placement
    replicaStrategy: anti-affinity

status:
  # High-level state
  # +kubebuilder:validation:Enum=Initializing;Ready;Degraded;Failed
  state: Ready

  # Shard status
  totalShards: 3
  readyShards: 3

  # Child Valkey resources
  shards:
    - name: mypool-0
      zone: us-east-1a
      state: Ready
      readyReplicas: 3
    - name: mypool-1
      zone: us-east-1b
      state: Ready
      readyReplicas: 3
    - name: mypool-2
      zone: us-east-1c
      state: Ready
      readyReplicas: 3

  # Conditions
  # +listType=map
  # +listMapKey=type
  conditions: []
```

### Child Resource Naming

ValkeyPool creates child Valkey resources with predictable names:
- Pattern: `<pool-name>-<shard-index>`
- Examples: `mypool-0`, `mypool-1`, `mypool-2`

Each child Valkey resource:
- Has `ownerReferences` pointing to ValkeyPool
- Inherits template spec from ValkeyPool
- Gets AZ-specific affinity injected based on `azDistribution`

### AZ Distribution Semantics

When `azDistribution` is specified:

**Primary placement (round-robin):**
```
Shard 0 primary → zones[0 % len(zones)] = zones[0] = us-east-1a
Shard 1 primary → zones[1 % len(zones)] = zones[1] = us-east-1b
Shard 2 primary → zones[2 % len(zones)] = zones[2] = us-east-1c
Shard 3 primary → zones[3 % len(zones)] = zones[0] = us-east-1a
```

**Replica placement (anti-affinity):**
Replicas for each shard are placed in zones OTHER than the primary's zone:
```
Shard 0: primary in us-east-1a → replicas prefer us-east-1b, us-east-1c
Shard 1: primary in us-east-1b → replicas prefer us-east-1a, us-east-1c
Shard 2: primary in us-east-1c → replicas prefer us-east-1a, us-east-1b
```

**Implementation:** ValkeyPool controller injects `azPlacement` field into child Valkey resources. Valkey controller then generates per-node affinity based on this placement.

### Scaling Examples

#### Scale up (add shards)

```yaml
spec:
  shards: 3  # Currently 3 shards

# Scale to 5 shards
spec:
  shards: 5  # Creates mypool-3 and mypool-4
```

Controller creates new Valkey resources with AZ placement.

#### Scale down (remove shards)

```yaml
spec:
  shards: 5  # Currently 5 shards

# Scale to 3 shards
spec:
  shards: 3  # Deletes mypool-4, mypool-3 (reverse order)
```

**⚠️ Data loss warning**: Scaling down permanently deletes data. Users must manually migrate keys before scaling down.

#### Scale to zero (suspend pool)

```yaml
spec:
  shards: 0  # All Valkey resources deleted, PVCs retained
```

### ValkeyPool Controller Responsibilities

1. **Create child Valkey resources**: Based on shards count
2. **Apply template**: Each Valkey gets template spec
3. **Inject AZ affinity**: If `azDistribution` specified, add `azPlacement` to Valkey spec
4. **Handle scaling**: Create/delete Valkey resources sequentially
5. **Aggregate status**: Collect state from all child Valkey resources
6. **Propagate template changes**: Update all child Valkey resources when template changes

### Use Case: Client-Side Sharding

ValkeyPool is ideal for applications using client-side sharding libraries:

```python
from rediscluster import RedisCluster

# Connect to all shards in the pool
startup_nodes = [
    {"host": "mypool-0-0-svc.default.svc.cluster.local", "port": "6379"},
    {"host": "mypool-1-0-svc.default.svc.cluster.local", "port": "6379"},
    {"host": "mypool-2-0-svc.default.svc.cluster.local", "port": "6379"},
]

# Client handles sharding logic
rc = RedisCluster(startup_nodes=startup_nodes)
rc.set("key", "value")  # Client routes to appropriate shard
```

**ValkeyPool vs ValkeyCluster:**
- **ValkeyPool**: Client-side sharding, independent Valkey instances, simpler
- **ValkeyCluster**: Server-side sharding, distributed hash slots, Valkey Cluster protocol

---

## Design Section 5: ValkeyCluster CRD

ValkeyCluster manages sharded Valkey Cluster mode deployments with distributed hash slots. This is server-side sharding where Valkey handles slot distribution and client redirection.

### Breaking Changes from Current Implementation

**Pre-0.1.0 breaking changes for consistency:**

| Old | New | Reason |
|-----|-----|--------|
| `spec.replicas` | `spec.replicas` | Semantic change: total pods per shard (not replicas excluding primary) |
| No AZ config | `spec.azDistribution` | New feature for multi-AZ cost optimization |
| Implicit persistence | `spec.persistence` | Explicit nested config for consistency |
| No TLS config | `spec.tls` | New feature |
| Minimum 1 shard | Minimum 3 shards | Valkey Cluster protocol requirement |

**Migration formula:** `replicas = old_replicas + 1`

### Specification

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: mycluster
  namespace: default
spec:
  # Valkey version/image
  image: valkey/valkey:8.0

  # Number of primary shards (minimum 3 for Valkey Cluster)
  # Each shard manages a portion of 16384 hash slots
  # +kubebuilder:validation:Minimum=3
  # +kubebuilder:default=3
  shards: 3

  # Total pods per shard (primary + replicas)
  # 0 = suspended (no pods, PVCs retained)
  # 1 = no replication (primary only per shard) - NOT RECOMMENDED for production
  # ≥2 = replication (1 primary + N replicas per shard, where N = replicas - 1)
  # +kubebuilder:validation:Minimum=0
  # +kubebuilder:default=3
  replicas: 3

  # Resource requirements for Valkey container
  # +optional
  resources:
    requests:
      memory: "512Mi"
      cpu: "200m"
    limits:
      memory: "2Gi"
      cpu: "1000m"

  # Persistent storage configuration
  # +optional
  persistence:
    enabled: true
    size: 20Gi
    storageClassName: gp3

  # Pod scheduling configuration
  # +optional
  nodeSelector: {}

  # +optional
  affinity: {}

  # +optional
  tolerations: []

  # Pod management strategy
  # +kubebuilder:validation:Enum=deployment;statefulset
  # +kubebuilder:default=statefulset
  podManagementType: statefulset

  # Availability zone distribution
  # Distributes primary pods across zones, replicas in different zones
  # +optional
  azDistribution:
    zones:
      - us-east-1a
      - us-east-1b
      - us-east-1c

    nodeLabel: topology.kubernetes.io/zone

    # +kubebuilder:validation:Enum=anti-affinity;spread
    # +kubebuilder:default=anti-affinity
    # TODO: Support explicit per-replica zone placement
    replicaStrategy: anti-affinity

  # Metrics exporter configuration
  # +kubebuilder:default={enabled:true}
  # +optional
  exporter:
    enabled: true
    image: oliver006/redis_exporter:latest
    resources: {}

  # TLS configuration
  # +optional
  tls:
    enabled: false
    certificateRef:
      name: valkey-cluster-tls

  # Valkey configuration overrides
  # +optional
  config:
    cluster-node-timeout: "15000"
    cluster-replica-validity-factor: "10"

status:
  # High-level state
  # +kubebuilder:validation:Enum=Initializing;Reconciling;Ready;Degraded;Suspended;Failed
  state: Ready

  reason: ClusterHealthy
  message: "Cluster is healthy with all slots assigned"

  # Cluster topology
  totalShards: 3
  readyShards: 3

  # Total pods
  totalReplicas: 9  # shards * replicas
  readyReplicas: 9

  # Slot distribution status
  slotsAssigned: 16384
  slotsUnassigned: 0

  # Shard details
  shards:
    - shardIndex: 0
      primary: mycluster-0-0
      replicas:
        - mycluster-0-1
        - mycluster-0-2
      slots: "0-5461"
      zone: us-east-1a
      ready: true

    - shardIndex: 1
      primary: mycluster-1-0
      replicas:
        - mycluster-1-1
        - mycluster-1-2
      slots: "5462-10922"
      zone: us-east-1b
      ready: true

    - shardIndex: 2
      primary: mycluster-2-0
      replicas:
        - mycluster-2-1
        - mycluster-2-2
      slots: "10923-16383"
      zone: us-east-1c
      ready: true

  # Conditions
  # +listType=map
  # +listMapKey=type
  conditions: []
```

### Naming Convention

**ValkeyNode naming:** `<cluster-name>-<shard-index>-<replica-index>`

Examples:
- `mycluster-0-0`: Shard 0, replica index 0 (primary)
- `mycluster-0-1`: Shard 0, replica index 1 (first replica)
- `mycluster-1-0`: Shard 1, replica index 0 (primary)
- `mycluster-2-2`: Shard 2, replica index 2 (second replica)

**Service naming:** `<valkeynode-name>-svc`

### Slot Distribution

Valkey Cluster uses 16384 hash slots distributed across primaries:

**For 3 shards:**
- Shard 0: slots 0-5461 (5462 slots)
- Shard 1: slots 5462-10922 (5461 slots)
- Shard 2: slots 10923-16383 (5461 slots)

**Formula:**
```go
slotsPerShard := 16384 / shards
for i := 0; i < shards; i++ {
  start := i * slotsPerShard
  end := start + slotsPerShard - 1
  if i == shards-1 {
    end = 16383  // Last shard gets remainder
  }
  assignSlots(shard[i], start, end)
}
```

### ValkeyCluster Controller Responsibilities

1. **Create ValkeyNodes**: For each shard, create `replicas` ValkeyNodes
2. **Configure cluster mode**: Set `cluster.enabled: true` on all ValkeyNodes
3. **Assign slots**: Distribute 16384 slots across primary ValkeyNodes
4. **Initialize cluster**: Run `CLUSTER MEET` commands to form cluster
5. **Configure replication**: Set `replicaOf` on replica ValkeyNodes
6. **Inject AZ affinity**: If `azDistribution` specified, generate per-node affinity
7. **Handle shard scaling**: Add/remove shards with slot migration
8. **Handle replica scaling**: Add/remove replica ValkeyNodes per shard
9. **Monitor cluster health**: Query `CLUSTER INFO` and update status

### Validation Rules

```go
func (v *ValkeyCluster) ValidateCreate() error {
  // Valkey Cluster protocol requires minimum 3 primaries
  if v.Spec.Shards < 3 {
    return errors.New("Valkey Cluster requires at least 3 shards")
  }

  return nil
}

func (v *ValkeyCluster) ValidateUpdate(old *ValkeyCluster) error {
  // Controlled scaling: shards can only change by ±1
  shardDiff := abs(v.Spec.Shards - old.Spec.Shards)
  if shardDiff > 1 {
    return errors.New("shards can only change by ±1 per update for safe slot migration")
  }

  // ReplicasPerShard can only change by ±1
  replicaDiff := abs(v.Spec.ReplicasPerShard - old.Spec.ReplicasPerShard)
  if replicaDiff > 1 {
    return errors.New("replicas can only change by ±1 per update")
  }

  return nil
}
```

---

## Design Section 6: Sentinel CRD

Sentinel provides high-availability monitoring and automatic failover for Valkey replication instances. Sentinel is **only used with Valkey CRD** (not ValkeyCluster, which has built-in cluster failover).

### Specification

```yaml
apiVersion: valkey.io/v1alpha1
kind: Sentinel
metadata:
  name: production-sentinel
  namespace: default
spec:
  # Sentinel image
  # +kubebuilder:default="valkey/valkey:8.0"
  image: valkey/valkey:8.0

  # Number of Sentinel instances
  # Minimum 3 recommended for proper quorum
  # Odd numbers preferred (3, 5, 7) to avoid split-brain
  # +kubebuilder:validation:Minimum=1
  # +kubebuilder:default=3
  replicas: 3

  # Quorum for failover decisions
  # Number of Sentinels that must agree to initiate failover
  # Typically: quorum = (replicas / 2) + 1
  # +kubebuilder:validation:Minimum=1
  quorum: 2

  # Resource requirements for Sentinel container
  # +optional
  resources:
    requests:
      memory: "128Mi"
      cpu: "100m"
    limits:
      memory: "256Mi"
      cpu: "200m"

  # Pod scheduling configuration
  # +optional
  nodeSelector: {}
  affinity: {}
  tolerations: []

  podManagementType: statefulset

  # Default Sentinel config (used as fallback if Valkey doesn't specify)
  # +optional
  defaultConfig:
    down-after-milliseconds: "30000"
    failover-timeout: "180000"
    parallel-syncs: "1"

  # TLS configuration
  # +optional
  tls:
    enabled: false
    certificateRef:
      name: sentinel-tls

status:
  state: Ready

  totalReplicas: 3
  readyReplicas: 3

  # Monitored Valkey instances
  # Populated by Valkey controller when instances set sentinelRef
  monitoredInstances:
    - name: myapp-cache
      namespace: default
      primary: myapp-cache-0
      replicas: 2
      monitoring: true
      # Per-instance Sentinel config applied
      sentinelConfig:
        down-after-milliseconds: "10000"
        failover-timeout: "180000"
        parallel-syncs: "1"
      lastFailover: "2026-01-22T10:30:00Z"

  # Sentinel node details
  sentinels:
    - name: production-sentinel-0
      ready: true
      ip: 10.0.1.10
    - name: production-sentinel-1
      ready: true
      ip: 10.0.1.11
    - name: production-sentinel-2
      ready: true
      ip: 10.0.1.12

  # Conditions
  # +listType=map
  # +listMapKey=type
  conditions: []
```

### Discovery Mechanism: Valkey References Sentinel

Sentinel discovers which Valkey instances to monitor via **push model**:

1. **Valkey specifies sentinelRef** in its spec
2. **Valkey controller registers with Sentinel**: Updates `Sentinel.status.monitoredInstances`
3. **Sentinel controller configures monitoring**: Runs `SENTINEL MONITOR` commands

### Per-Instance Sentinel Configuration

**Key Design Decision:** Sentinel monitoring config lives in `Valkey.spec.sentinel.config`, not in Sentinel CRD.

**Rationale:**
- Different Valkey instances have different SLA requirements (production vs dev)
- One Sentinel can monitor many instances with different configs
- Clear ownership: Valkey spec contains its monitoring requirements
- Flexible: adjust per-instance without affecting others

**Configuration precedence:**
1. `Valkey.spec.sentinel.config` (highest - per-instance override)
2. `Sentinel.spec.defaultConfig` (middle - Sentinel-wide default)
3. Hardcoded Sentinel defaults (lowest - if nothing specified)

**Example:**

```yaml
---
# Shared Sentinel
apiVersion: valkey.io/v1alpha1
kind: Sentinel
metadata:
  name: shared-sentinel
spec:
  replicas: 3
  quorum: 2
  defaultConfig:
    down-after-milliseconds: "30000"

---
# Production: Fast failover
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: prod-cache
spec:
  replicas: 5
  sentinel:
    name: shared-sentinel
    config:
      down-after-milliseconds: "5000"  # Override for fast failover

---
# Dev: Uses defaults
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: dev-cache
spec:
  replicas: 2
  sentinel:
    name: shared-sentinel
    # No config override - uses defaultConfig (30000ms)
```

### Sentinel Controller Responsibilities

1. **Create ValkeyNodes**: Based on replicas count with `type: sentinel`
2. **Watch status.monitoredInstances**: Populated by Valkey controllers
3. **Configure Sentinel monitoring**: Run `SENTINEL MONITOR` commands with per-instance config
4. **Create headless service**: For Sentinel discovery
5. **Monitor Sentinel health**: Check Sentinel pods are healthy
6. **Handle scaling**: Add/remove Sentinel ValkeyNodes
7. **Update status**: Track monitored instances, Sentinel nodes

### Validation Rules

```go
func (s *Sentinel) ValidateCreate() error {
  if s.Spec.Replicas < 1 {
    return errors.New("replicas must be >= 1")
  }

  // Warn if even number
  if s.Spec.Replicas % 2 == 0 {
    // Log warning: "Odd numbers (3, 5, 7) recommended"
  }

  if s.Spec.Quorum < 1 || s.Spec.Quorum > int32(s.Spec.Replicas) {
    return errors.New("quorum must be between 1 and replicas")
  }

  return nil
}
```

---

## Key Design Decisions

### 1. Replicas Semantic: Kubernetes-Native Convention

**Decision:** `spec.replicas` = **total number of pods** (not "replicas of primary")

**Rationale:**
- Follows Kubernetes Deployment/StatefulSet pattern
- Consistent across all CRDs
- Clear scaling: `replicas: 0` = suspended, `replicas: 1` = standalone, `replicas: 3` = 1 primary + 2 replicas
- Intuitive: "I want 3 pods" → `replicas: 3`

**Applied to:**
- `Valkey.spec.replicas` = total pods
- `ValkeyCluster.spec.replicas` = total pods per shard
- `ValkeyPool.spec.template.replicas` = total pods per shard
- `Sentinel.spec.replicas` = total Sentinel pods

### 2. Role is Derived, Not Specified

**Decision:** ValkeyNode spec does **not** have a `role` field. Role is derived from `replicaOf`.

**Rationale:**
- Failovers change roles dynamically
- Spec represents desired state; controllers reconcile toward spec
- Having `role: primary` in spec would conflict with Sentinel/operator failover decisions

**Implementation:**
- `replicaOf: ""` → primary
- `replicaOf: "host:port"` → replica
- Status tracks `observedRole` for monitoring

### 3. ValkeyNode: Generic Pod Abstraction

**Decision:** ValkeyNode is operator-managed, supports both Valkey and Sentinel pods via `type` field

**Rationale:**
- Single pod management abstraction for all components
- Abstracts Deployment vs StatefulSet choice
- Configuration-driven behavior (no coupling to parent CRD type)
- Reusable and maintainable

### 4. Nesting Sub-Problems

**Decision:** Use nested config structures instead of CamelCase concatenation

**Example:** `cluster.enabled`, `cluster.slots` instead of `clusterEnabled`, `clusterSlots`

**Rationale:**
- Follows Kubernetes CRD design best practices
- Clear scope and validation
- Better extensibility
- More readable

### 5. Multiple Focused CRDs vs Unified CRD

**Decision:** Separate CRDs for different use cases

**Rationale:**
- Clear intent from CRD name
- Easier validation (fields relevant only for that use case)
- Better UX
- Evolvable independently

### 6. Sentinel Discovery: Push Model

**Decision:** Valkey instances explicitly reference Sentinel via `spec.sentinel.name`

**Rationale:**
- Clear from Valkey spec which failover mode is active
- Explicit opt-in
- Supports per-instance Sentinel config
- No accidental monitoring

### 7. Singleton Deployments/StatefulSets

**Decision:** Each ValkeyNode creates one Deployment or StatefulSet with `replicas: 1`

**Rationale:**
- Maximum scheduling control (per-pod affinity)
- Enables AZ pinning
- Faster rollouts
- Supports both Deployment and StatefulSet

### 8. Operator Creates PVCs Separately

**Decision:** Operator creates PVC for each ValkeyNode, then references in pod spec

**Rationale:**
- Avoids StatefulSet `volumeClaimTemplate` immutability
- Operator can modify PVC specs
- Clean separation of concerns

### 9. Service Per Pod

**Decision:** Each ValkeyNode gets its own ClusterIP Service

**Rationale:**
- Stable network identity
- Prevents stale replica issues
- Required for proper `replica-announce-ip`
- Clean pod lifecycle

### 10. AZ Distribution: Round-Robin Primaries, Anti-Affinity Replicas

**Decision:** Primaries distributed round-robin across zones, replicas prefer different zones from primary

**Rationale:**
- Reduces cross-AZ data transfer costs
- HA: replicas survive primary zone failure
- Deterministic and predictable

### 11. Per-Instance Sentinel Configuration

**Decision:** Sentinel monitoring config lives in `Valkey.spec.sentinel.config`

**Rationale:**
- Different Valkey instances have different SLA requirements
- One Sentinel can monitor many instances with different configs
- Clear ownership

### 12. ValkeyCluster Breaking Changes (Pre-0.1.0)

**Decision:** Make breaking changes for consistency before first release

**Rationale:**
- Pre-0.1.0: perfect time for breaking changes
- Consistency with Valkey/ValkeyPool semantics
- Follows Kubernetes conventions

---

## Design Rejections

| Rejected Design | Reason |
|-----------------|--------|
| **`spec.role: primary\|replica`** | Conflicts with failover - role changes dynamically |
| **`replicas` = replicas excluding primary** | Confusing semantic, not Kubernetes-native |
| **Single Valkey CRD with mode field** | Complex validation, unclear API surface |
| **Sentinel label selector discovery** | Implicit, prone to accidental monitoring |
| **StatefulSet volumeClaimTemplate** | Immutable, can't change storage specs |
| **Shared headless service** | Stale replica issues when pods are rolled |
| **Direct pod management** | Operator downtime affects pod lifecycle |
| **Global Sentinel config** | Inflexible, different SLAs needed |
| **Explicit `suspend` field** | Unnecessary, `replicas: 0` achieves same goal |

---

## TODOs and Future Enhancements

### 1. Enhanced Replica Placement (v1alpha2+)

Support explicit per-replica zone placement:

```yaml
azPlacement:
  primaryZone: us-east-1a
  replicaZones:  # Explicit zones for each replica
    - us-east-1b
    - us-east-1c
```

**Current approach:** Anti-affinity provides soft preference
**Future:** Required affinity per replica for strict control

### 2. Configuration Live Reload

- Detect which configs require pod restart
- Apply mutable configs live via `CONFIG SET`

### 3. Backup/Restore Integration

- ValkeyBackup CRD
- Cloud storage integration (S3, GCS, Azure)
- Point-in-time recovery

### 4. Advanced Monitoring

- PrometheusRule generation
- Custom dashboards
- SLO/SLI tracking

### 5. Multi-Cluster Federation

- Cross-cluster replication
- Disaster recovery

### 6. External Access

- LoadBalancer/Ingress configuration
- External DNS integration
- Proxy support

---

## Migration Guide

### ValkeyCluster Breaking Changes (Pre-0.1.0)

**Old spec:**
```yaml
spec:
  shards: 3
  replicas: 2  # 2 replicas per shard (not including primary)
```

**New spec:**
```yaml
spec:
  shards: 3
  replicas: 3  # 1 primary + 2 replicas = 3 total pods per shard
```

**Migration formula:** `replicas = old_replicas + 1`

### Upgrade Paths

#### Simple → Complex

```yaml
# 1. Start with standalone
kind: Valkey
spec:
  replicas: 1

# 2. Add replication
spec:
  replicas: 3

# 3. Add Sentinel for HA
spec:
  replicas: 3
  sentinel:
    name: production-sentinel

# 4. Scale horizontally with Pool
kind: ValkeyPool
spec:
  shards: 5
  template:
    replicas: 3
    sentinel:
      name: production-sentinel

# 5. Upgrade to ValkeyCluster
kind: ValkeyCluster
spec:
  shards: 3
  replicas: 3
```

---

## Consistency Across CRDs

All user-facing CRDs share these patterns:

### Common Fields

```yaml
spec:
  # Standard Kubernetes pod configuration
  resources: {}
  nodeSelector: {}
  affinity: {}
  tolerations: []

  # Pod management
  podManagementType: statefulset

  # Storage
  persistence:
    enabled: true
    size: 10Gi
    storageClassName: gp3

  # Observability
  exporter:
    enabled: true

  # Security
  tls:
    enabled: false

  # Config overrides
  config: {}
```

### Validation Patterns

All CRDs enforce controlled scaling:

```go
// Count can only change by ±1 per update
func ValidateUpdate(old, new *Resource) error {
  diff := abs(new.Spec.Count - old.Spec.Count)
  if diff > 1 {
    return errors.New("count can only change by ±1 per update")
  }
  return nil
}
```

### Status Patterns

```yaml
status:
  state: Ready
  reason: ClusterHealthy
  message: "..."
  conditions: []
```

### Total Pod Calculation

- **Valkey**: `replicas`
- **ValkeyPool**: `shards × template.replicas`
- **ValkeyCluster**: `shards × replicas`
- **Sentinel**: `replicas`

All consistent with Kubernetes conventions ✅

---

## Appendix: Use Case Comparison

| Feature | Valkey | ValkeyPool | ValkeyCluster | Sentinel |
|---------|--------|------------|---------------|----------|
| **Topology** | Single instance | Multiple independent | Cluster with slots | HA monitor |
| **Sharding** | None | Client-side | Server-side | N/A |
| **HA Failover** | Operator or Sentinel | Operator or Sentinel | Built-in | Provides failover |
| **Use Case** | Simple cache, dev | Horizontal scaling | Production sharding | HA for Valkey |
| **Min Replicas** | 1 (standalone) | Template-defined | 3 shards × N | 3 recommended |
| **Client Type** | Standard | Standard | Cluster-aware | Sentinel-aware |

---

## References

- [Valkey RFC #28](https://github.com/valkey-io/valkey-rfc/pull/28)
- [Valkey Operator Discussion #19](https://github.com/valkey-io/valkey-operator/discussions/19)
- [Valkey Kubernetes Topologies Gist](https://gist.githubusercontent.com/jdheyburn/88c5c67625d784d52cb1245be68a7429/raw/2a82b71b0357461721db118aa12bcf8c3cb044ec/VALKEY_KUBERNETES_TOPOLOGIES.md)
- KubeCon Talk: "Kubernetes CRD Design for the Long Haul"
- KubeCon Talk: "Simplify Kubernetes Operator Development With a Modular Design Pattern"
- Prometheus Operator: [github.com/prometheus-operator/prometheus-operator](https://github.com/prometheus-operator/prometheus-operator)
- MongoDB Operator: [github.com/mongodb/mongodb-kubernetes](https://github.com/mongodb/mongodb-kubernetes)
- Elastic Cloud on K8s: [github.com/elastic/cloud-on-k8s](https://github.com/elastic/cloud-on-k8s)
- SAP Valkey Operator: [github.com/sap/valkey-operator](https://github.com/sap/valkey-operator)
- Hyperspike Valkey Operator: [github.com/hyperspike/valkey-operator](https://github.com/hyperspike/valkey-operator)
