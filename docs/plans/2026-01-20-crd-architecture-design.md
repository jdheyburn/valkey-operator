# Valkey Operator CRD Architecture Design

**Date:** 2026-01-20
**Status:** Draft
**Authors:** Design session via brainstorming

## Overview

This document describes the CRD architecture for the Valkey Kubernetes Operator, covering all supported topologies (Cluster, Standalone, Replication, Sentinel) and their configuration options.

## Design Principles

- **"A little duplication is better than a deep dependency"** - Separate CRDs with copied fields rather than shared Go types
- **GitOps-friendly** - Use `+listType=map` with `+listMapKey` for lists
- **No external dependencies** - TLS via Secret references, not cert-manager
- **Explicit over implicit** - Clear configuration, avoid magic behavior
- **Copy and adapt** - Own types for persistence/TLS, not embedded K8s types
- **Sensible defaults with override** - Operator defaults, inline config overrides

---

## CRD Architecture

### User-Facing CRDs

| CRD | Purpose |
|-----|---------|
| `ValkeyCluster` | Cluster mode with shards and replicas per shard. Built-in slot management, rebalancing, and cluster discovery. |
| `Valkey` | Standalone and Replication modes. `replicas=0` is standalone, `replicas>0` enables replication. Seamless scaling from standalone to replication. |
| `ValkeySentinel` | Selector-based monitoring of `Valkey` instances. Provides quorum-based failover for replicated Valkey deployments. |
| `ValkeyPool` | Horizontal scaling via client-side sharding. Creates multiple `Valkey` instances with AZ-aware primary placement. Optionally embeds Sentinel. |

### Internal CRD

| CRD | Purpose |
|-----|---------|
| `ValkeyNode` | Abstracts Deployment vs StatefulSet. Created automatically by `ValkeyCluster` and `Valkey`. Users can inspect but should not modify directly. |

### Key Decisions

- No migration path between `ValkeyCluster` ↔ `Valkey` (architecturally different)
- Seamless scaling within `Valkey` (standalone → replication by increasing replicas)
- `ValkeySentinel` uses label selectors (like prometheus-operator's `ServiceMonitor`), keeping `Valkey` CRD focused
- Common fields duplicated across CRDs rather than shared Go types

---

## Common Spec Fields

All user-facing CRDs share similar top-level fields (duplicated, not shared types):

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster  # or Valkey, ValkeySentinel
metadata:
  name: my-valkey
spec:
  # Image configuration
  image: valkey/valkey:8.0

  # Resource requirements
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi

  # Scheduling
  tolerations: [...]
  nodeSelector: {...}
  affinity: {...}

  # Topology - simple presets for now
  topologyMode: balanced|highAvailability|costOptimized

  # Node abstraction settings
  node:
    workloadType: Deployment|StatefulSet
    servicePerNode: true|false

  # Metrics exporter sidecar
  exporter:
    enabled: true
    image: oliver006/redis_exporter:latest
    resources: {...}
```

### Topology Mode Behavior

- **Operator always enforces:** Primary and replica of the same shard never on the same node
- **Presets:**
  - `balanced` (default): Spread primaries and replicas across zones
  - `highAvailability`: Primary and replicas in different zones per shard
  - `costOptimized`: Replicas co-located with primary (same zone)

---

## Topology-Specific Fields

### ValkeyCluster

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
spec:
  # ... common fields ...

  shards: 3              # Number of shard groups (primaries)
  replicas: 1            # Replicas per shard
```

### Valkey

```yaml
apiVersion: valkey.io/v1alpha1
kind: Valkey
spec:
  # ... common fields ...

  replicas: 0            # 0 = standalone, >0 = replication
```

### ValkeySentinel

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeySentinel
metadata:
  name: my-sentinel
spec:
  replicas: 3            # Sentinel quorum size (odd number recommended)

  # Selector for which Valkey instances to monitor
  selector:
    matchLabels:
      tier: critical
      environment: production

  image: valkey/valkey:8.0
  resources: {...}
```

**Sentinel behavior:**
- Discovers `Valkey` resources matching the selector
- Configures Sentinel to monitor each as a master group
- Handles failover when a primary becomes unavailable
- One `ValkeySentinel` can monitor multiple `Valkey` instances

#### Sentinel Configuration Options

Sentinel settings (quorum, downAfterMilliseconds, failoverTimeout, parallelSyncs) are **per-monitored-master**, not global. Two design options are under consideration:

**Option A: Config on Valkey CRD with ValkeySentinel defaults**

Valkey instances specify their own sentinel config. ValkeySentinel provides org-wide defaults.

```yaml
# ValkeySentinel with defaults
apiVersion: valkey.io/v1alpha1
kind: ValkeySentinel
metadata:
  name: sentinel
spec:
  replicas: 3
  selector:
    matchLabels:
      tier: critical
  defaults:
    quorum: 2
    downAfterMilliseconds: 30000
    failoverTimeout: 180000
    parallelSyncs: 1
---
# Valkey overrides specific settings
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: latency-sensitive
  labels:
    tier: critical
spec:
  replicas: 2
  sentinel:
    downAfterMilliseconds: 5000    # Override - fail faster
```

Merge order: Operator defaults → ValkeySentinel defaults → Valkey spec.sentinel

*Pros:* Self-describing resources, simple mental model
*Cons:* Valkey has sentinel-specific config even if not monitored

**Option B: SentinelMonitor CRD (ServiceMonitor pattern)**

Separate CRD binds Valkey instances to a Sentinel with specific config. Follows prometheus-operator pattern exactly.

```yaml
# ValkeySentinel - just infrastructure
apiVersion: valkey.io/v1alpha1
kind: ValkeySentinel
metadata:
  name: shared-sentinel
spec:
  replicas: 3
---
# SentinelMonitor - binding + config
apiVersion: valkey.io/v1alpha1
kind: SentinelMonitor
metadata:
  name: critical-monitoring
spec:
  sentinelRef:
    name: shared-sentinel
  valkeySelector:
    matchLabels:
      tier: critical
  config:
    quorum: 2
    downAfterMilliseconds: 10000
    failoverTimeout: 60000
    parallelSyncs: 2
---
# Different config for different Valkey instances, same Sentinel
apiVersion: valkey.io/v1alpha1
kind: SentinelMonitor
metadata:
  name: background-monitoring
spec:
  sentinelRef:
    name: shared-sentinel
  valkeySelector:
    matchLabels:
      tier: background
  config:
    downAfterMilliseconds: 60000
```

*Pros:* Clean separation of concerns, proven Kubernetes pattern, Valkey stays clean
*Cons:* Additional CRD, more verbose for simple cases

**Decision:** TBD - both options are valid, choice depends on user preference for simplicity vs flexibility

### ValkeyPool

For horizontal scaling via client-side sharding. Creates multiple Sentinel-managed `Valkey` instances with AZ-aware primary placement.

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyPool
metadata:
  name: cache
spec:
  shards: 4                      # Creates cache-0, cache-1, cache-2, cache-3
  replicasPerShard: 2            # Each shard: 1 primary + 2 replicas

  # AZ distribution for primaries (round-robin, continues on scale-up)
  availabilityZones:
    - us-east-1a
    - us-east-1b
    - us-east-1c

  # Optional embedded Sentinel (disabled by default)
  sentinel:
    enabled: false               # Set to true to create Sentinel for this pool
    replicas: 3
    image: valkey/valkey:8.0
    resources: {...}

  # Template applied to each Valkey shard
  template:
    metadata:
      labels:
        pool: cache
        tier: critical
    spec:
      image: valkey/valkey:8.0
      resources: {...}
      persistence: {...}
      auth: {...}
      tls: {...}
      config: {...}
```

**Resource hierarchy:**

```
ValkeyPool (cache)
├── Valkey (cache-0)
│   ├── ValkeyNode (cache-0-primary)
│   ├── ValkeyNode (cache-0-replica-0)
│   └── ValkeyNode (cache-0-replica-1)
├── Valkey (cache-1)
│   └── ...
├── Valkey (cache-2)
│   └── ...
├── Valkey (cache-3)
│   └── ...
└── ValkeySentinel (cache-sentinel)  # if sentinel.enabled: true
```

**Behaviors:**
- Creates `Valkey` resources named `{pool-name}-{shard-index}` (e.g., cache-0, cache-1, ...)
- Primaries pinned to AZs in round-robin order based on `availabilityZones`
- Scaling up (`shards: 4` → `shards: 6`) adds new shards, continues AZ round-robin pattern
- Data rebalancing is the client's responsibility (client-side sharding)
- If `sentinel.enabled: true`, creates embedded `ValkeySentinel` monitoring all shards in pool
- If `sentinel.enabled: false`, user can deploy external `ValkeySentinel` using label selectors

**Use case:** Companies running Valkey for different workflows (cache, locks, sidekiq) who need to horizontally scale each workflow independently with AZ-aware placement for cost optimization.

---

## Persistence Configuration

```yaml
spec:
  persistence:
    # Valkey RDB snapshots
    rdb:
      enabled: true
      saveIntervals:
        - seconds: 900
          changes: 1
        - seconds: 300
          changes: 10
        - seconds: 60
          changes: 10000
      compression: true

    # Valkey AOF (append-only file)
    aof:
      enabled: false
      fsync: everysec           # always|everysec|no
      rewritePercentage: 100
      rewriteMinSize: 64mb

    # Kubernetes volume configuration
    volume:
      enabled: true
      size: 10Gi
      storageClassName: fast-ssd
      accessModes:
        - ReadWriteOnce
```

**Behavior:**
- `rdb.enabled` / `aof.enabled` control Valkey's persistence mechanism
- `volume.enabled` controls whether a PVC is created
- Operator validates: warns if `rdb.enabled` or `aof.enabled` but `volume.enabled: false`
- Volume expansion supported if StorageClass allows it
- PVCs created separately by operator (not via StatefulSet's `volumeClaimTemplate`) for mutability
- Fields are copied, not embedded - own `PersistenceVolumeSpec` with just needed fields

---

## TLS Configuration

Follows prometheus-operator pattern - Secret references with no cert-manager dependency:

```yaml
spec:
  tls:
    enabled: true

    # Server certificate
    cert:
      secret:
        name: valkey-cert
        key: tls.crt

    # Private key
    key:
      secret:
        name: valkey-cert
        key: tls.key

    # CA certificate (for client verification)
    ca:
      secret:
        name: valkey-ca
        key: ca.crt

    # Valkey-specific TLS options
    clientAuth: required|optional|none   # mTLS for clients
    replication: true                     # TLS for primary-replica traffic
    clusterBus: true                      # TLS for cluster bus (ValkeyCluster only)

    # Protocol settings
    minVersion: TLS12
    protocols:
      - TLSv1.2
      - TLSv1.3
```

**Design decisions:**
- Uses `SecretKeySelector` pattern (native Kubernetes type)
- No cert-manager dependency - works with any Secret source
- `clientAuth` controls whether clients must present certificates (mTLS)
- `replication` and `clusterBus` allow granular control over internal traffic encryption
- `clusterBus` only relevant for `ValkeyCluster`, ignored on `Valkey`

---

## Authentication Configuration

Supports both legacy password auth and modern ACL system. Both can coexist for migration:

```yaml
spec:
  auth:
    # Legacy mode - simple shared password (requirepass)
    password:
      secret:
        name: valkey-password
        key: password

    # ACL mode - multiple users with permissions
    acl:
      enabled: true
      # +listType=map
      # +listMapKey=name
      users:
        - name: default
          passwordSecret:
            name: valkey-default-password
            key: password
          permissions: "+@all ~*"

        - name: readonly
          passwordSecret:
            name: valkey-readonly-password
            key: password
          permissions: "+@read ~*"

        - name: app
          passwordSecret:
            name: app-credentials
            key: valkey-password
          permissions: "+@all -@admin ~app:*"
```

**Behavior:**
- `auth.password` sets `requirepass` for legacy clients
- `auth.acl.enabled: true` enables ACL system
- Both can be active simultaneously for migration
- All passwords via Secret references (no inline passwords)
- Uses list with `+listMapKey=name` for GitOps compatibility

**Migration workflow:**
1. Start with `auth.password` only (legacy clients work)
2. Enable `auth.acl` and add users (new clients use ACL, old clients still use legacy)
3. Once all clients migrated, remove `auth.password`

**Permissions format:** Standard Valkey ACL syntax - categories (`+@read`), commands (`+GET -DEBUG`), and key patterns (`~app:*`).

---

## Custom Configuration

For Valkey settings not explicitly modeled:

```yaml
spec:
  # Untyped config map - any valid Valkey configuration
  # +kubebuilder:pruning:PreserveUnknownFields
  config:
    maxmemory: 2gb
    maxmemory-policy: allkeys-lru
    timeout: 300
    tcp-keepalive: 60
    slowlog-log-slower-than: 10000
    slowlog-max-len: 128
```

**Merge order (later wins):**
1. Operator defaults (sensible baseline)
2. Explicitly modeled fields (`persistence.rdb.*`, `tls.*`, `auth.*`) translate to config
3. `spec.config` inline map

**Design decisions:**
- Uses `map[string]interface{}` with `+kubebuilder:pruning:PreserveUnknownFields`
- Follows MongoDB operator pattern - inline only
- Explicitly modeled fields take precedence to avoid conflicts

---

## ValkeyNode (Internal CRD)

Managed automatically by the operator:

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyNode
metadata:
  name: my-cluster-shard-0-primary
  ownerReferences:
    - apiVersion: valkey.io/v1alpha1
      kind: ValkeyCluster
      name: my-cluster
spec:
  image: valkey/valkey:8.0
  resources: {...}

  workloadType: Deployment|StatefulSet
  servicePerNode: true

  persistence:
    enabled: true
    size: 10Gi
    storageClassName: fast-ssd

  role: primary|replica
  shardIndex: 0
```

**Lifecycle:**
- Created by `ValkeyCluster` or `Valkey` controller
- Each `ValkeyNode` creates exactly one Pod (via Deployment or StatefulSet with replicas=1)
- Optionally creates a ClusterIP Service for stable DNS
- Optionally creates a PVC (managed separately for mutability)

**Why singleton workloads:**
- Maximum control over individual pod scheduling
- Can apply different affinity rules per node
- Avoids StatefulSet's ordered rollout when not needed

---

## Complete Examples

### ValkeyCluster (Production)

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyCluster
metadata:
  name: production-cache
spec:
  image: valkey/valkey:8.0
  shards: 3
  replicas: 1

  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 2
      memory: 4Gi

  topologyMode: highAvailability

  node:
    workloadType: StatefulSet
    servicePerNode: true

  persistence:
    rdb:
      enabled: true
      saveIntervals:
        - seconds: 900
          changes: 1
    aof:
      enabled: false
    volume:
      enabled: true
      size: 20Gi
      storageClassName: fast-ssd

  tls:
    enabled: true
    cert:
      secret:
        name: valkey-cert
        key: tls.crt
    key:
      secret:
        name: valkey-cert
        key: tls.key
    ca:
      secret:
        name: valkey-ca
        key: ca.crt
    clientAuth: optional
    replication: true
    clusterBus: true
    minVersion: TLS12

  auth:
    acl:
      enabled: true
      users:
        - name: default
          passwordSecret:
            name: valkey-default-pw
            key: password
          permissions: "+@all ~*"

  config:
    maxmemory: 3gb
    maxmemory-policy: volatile-lru

  exporter:
    enabled: true
```

### Valkey (Simple Replicated)

```yaml
apiVersion: valkey.io/v1alpha1
kind: Valkey
metadata:
  name: session-store
  labels:
    tier: critical
spec:
  image: valkey/valkey:8.0
  replicas: 2

  resources:
    requests:
      cpu: 100m
      memory: 256Mi

  persistence:
    rdb:
      enabled: true
    volume:
      enabled: true
      size: 5Gi

  auth:
    password:
      secret:
        name: session-store-password
        key: password
```

### ValkeySentinel

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeySentinel
metadata:
  name: sentinel
spec:
  replicas: 3
  image: valkey/valkey:8.0

  selector:
    matchLabels:
      tier: critical
```

### ValkeyPool (Horizontal Scaling with Client-Side Sharding)

```yaml
apiVersion: valkey.io/v1alpha1
kind: ValkeyPool
metadata:
  name: cache
spec:
  shards: 4
  replicasPerShard: 2

  availabilityZones:
    - us-east-1a
    - us-east-1b
    - us-east-1c

  sentinel:
    enabled: true
    replicas: 3

  template:
    metadata:
      labels:
        pool: cache
        tier: critical
    spec:
      image: valkey/valkey:8.0
      resources:
        requests:
          cpu: 250m
          memory: 512Mi
        limits:
          cpu: 1
          memory: 2Gi

      persistence:
        rdb:
          enabled: true
        volume:
          enabled: true
          size: 10Gi
          storageClassName: fast-ssd

      auth:
        password:
          secret:
            name: cache-password
            key: password

      config:
        maxmemory: 1500mb
        maxmemory-policy: allkeys-lru
```

This creates:
- 4 Valkey shards: `cache-0`, `cache-1`, `cache-2`, `cache-3`
- Each shard with 1 primary + 2 replicas
- Primaries in AZs: cache-0→us-east-1a, cache-1→us-east-1b, cache-2→us-east-1c, cache-3→us-east-1a
- 1 Sentinel deployment (`cache-sentinel`) monitoring all 4 shards

---

## Open Items and TODOs

| Item | Notes |
|------|-------|
| Topology configuration | Currently using presets (`topologyMode`). May need explicit `spreadAcross` keys or full affinity override for advanced use cases. |
| External config reference | Started with inline `config` only. Add `configRef` (Secret reference) if users need shared/external configuration. |
| Ingress/LoadBalancer | RFC mentions option for external access. Not yet designed. |
| Backup/Restore | RFC mentions this. Not yet designed. |

---

## Glossary

| Term | Definition |
|------|------------|
| Shard (ValkeyCluster) | A primary + its replicas, responsible for a subset of hash slots. Server-side sharding with automatic slot management. |
| Shard (ValkeyPool) | An independent Valkey instance (primary + replicas) in a pool. Client-side sharding where the application determines which shard to use. |
| Primary | The writable node in a shard or replication group |
| Replica | A read-only copy of a primary |
| Node | A single Valkey instance (represented by ValkeyNode internally) |
| Pool | A collection of shards for horizontal scaling via client-side sharding (ValkeyPool) |

---

## References

- [Valkey Operator RFC](https://github.com/valkey-io/valkey-rfc/pull/28)
- [Kubernetes CRD Design for the Long Haul (KubeCon talk)](notes from ClusterAPI maintainers)
- [prometheus-operator](https://github.com/prometheus-operator/prometheus-operator) - TLS and selector patterns
- [MongoDB Kubernetes Operator](https://github.com/mongodb/mongodb-kubernetes-operator) - Configuration patterns
- [Elastic Cloud on Kubernetes](https://github.com/elastic/cloud-on-k8s) - Configuration patterns
