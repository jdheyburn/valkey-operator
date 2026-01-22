# Valkey Operator Examples

This directory contains example YAML manifests demonstrating various deployment patterns for the Valkey Operator.

## Quick Start Examples

### Standalone Valkey

**File:** `valkey_standalone.yaml`
**Use case:** Development, testing, simple cache
**Total pods:** 1

```bash
kubectl apply -f valkey_standalone.yaml
```

Simple single-pod Valkey instance with persistence and metrics.

### Replicated Valkey (Operator-Managed Failover)

**File:** `valkey_replicated.yaml`
**Use case:** Basic HA without Sentinel
**Total pods:** 3 (1 primary + 2 replicas)

```bash
kubectl apply -f valkey_replicated.yaml
```

High-availability setup where the operator manages failover if the primary fails.

### Valkey with Sentinel (Automatic Failover)

**File:** `valkey_with_sentinel.yaml`
**Use case:** Production HA with automatic failover
**Total pods:** 8 (5 Valkey + 3 Sentinel)

```bash
kubectl apply -f valkey_with_sentinel.yaml
```

Production-ready setup with Sentinel managing automatic failover.

## Horizontal Scaling Examples

### Basic ValkeyPool

**File:** `valkeypool_basic.yaml`
**Use case:** Client-side sharding without Valkey Cluster protocol
**Total pods:** 15 (5 shards × 3 pods per shard)

```bash
kubectl apply -f valkeypool_basic.yaml
```

Multiple independent Valkey instances for horizontal scaling with client-side sharding.

### ValkeyPool with Multi-AZ

**File:** `valkeypool_with_az.yaml`
**Use case:** Multi-AZ deployment for HA and cost optimization
**Total pods:** 32 (9 shards × 3 pods per shard + 5 Sentinel)

```bash
kubectl apply -f valkeypool_with_az.yaml
```

Pool distributed across availability zones with Sentinel for HA. Reduces cross-AZ data transfer costs.

## Valkey Cluster Examples

### Basic ValkeyCluster

**File:** `valkeycluster_basic.yaml`
**Use case:** Production sharding with automatic slot distribution
**Total pods:** 9 (3 shards × 3 pods per shard)
**Requires:** Cluster-aware client

```bash
kubectl apply -f valkeycluster_basic.yaml
```

Server-side sharding using Valkey Cluster protocol with automatic hash slot distribution.

### ValkeyCluster with Multi-AZ

**File:** `valkeycluster_with_az.yaml`
**Use case:** Production cluster with HA across availability zones
**Total pods:** 15 (5 shards × 3 pods per shard)

```bash
kubectl apply -f valkeycluster_with_az.yaml
```

Multi-AZ ValkeyCluster with primaries distributed across zones and replicas in different zones for HA.

## Advanced Examples

### Shared Sentinel

**File:** `sentinel_shared.yaml`
**Use case:** Cost-efficient HA across multiple environments
**Total pods:** 13 (3 Sentinel + 5+3+2 Valkey across prod/staging/dev)

```bash
kubectl apply -f sentinel_shared.yaml
```

One Sentinel monitoring multiple Valkey instances with different failover SLAs per environment.

### Complete Production Setup

**File:** `production_complete.yaml`
**Use case:** Enterprise production deployment with all features
**Total pods:** 53

```bash
kubectl apply -f production_complete.yaml
```

Comprehensive production setup including:
- ValkeyCluster for application data (15 pods)
- ValkeyPool for session cache (30 pods)
- Standalone Valkey for rate limiting (3 pods)
- Sentinel for HA (5 pods)
- Multi-AZ distribution
- TLS enabled
- Prometheus monitoring

## Upgrade Path

Follow this progression to learn the operator:

1. **Start simple:** `valkey_standalone.yaml` (1 pod)
2. **Add replication:** `valkey_replicated.yaml` (3 pods)
3. **Add HA:** `valkey_with_sentinel.yaml` (8 pods)
4. **Scale horizontally:** `valkeypool_basic.yaml` (15 pods)
5. **Multi-AZ:** `valkeypool_with_az.yaml` (32 pods)
6. **Server-side sharding:** `valkeycluster_basic.yaml` (9 pods)
7. **Production:** `production_complete.yaml` (53 pods)

## Architecture Comparison

| Example | Topology | Sharding | Failover | Client Type |
|---------|----------|----------|----------|-------------|
| `valkey_standalone.yaml` | Single instance | None | Manual | Standard |
| `valkey_replicated.yaml` | Replication | None | Operator | Standard |
| `valkey_with_sentinel.yaml` | Replication | None | Sentinel | Sentinel-aware |
| `valkeypool_basic.yaml` | Multiple instances | Client-side | Operator | Standard |
| `valkeypool_with_az.yaml` | Multiple instances | Client-side | Sentinel | Sentinel-aware |
| `valkeycluster_basic.yaml` | Cluster | Server-side | Built-in | Cluster-aware |
| `valkeycluster_with_az.yaml` | Cluster | Server-side | Built-in | Cluster-aware |

## Customization

All examples can be customized with:

- **Resources:** Adjust `resources.requests` and `resources.limits`
- **Storage:** Change `persistence.size` and `persistence.storageClassName`
- **Scaling:** Modify `replicas` or `shards`
- **AZ Distribution:** Add `azDistribution` with your zones
- **Configuration:** Add Valkey config parameters in `config` section
- **TLS:** Enable with `tls.enabled: true` and certificate references
- **Node Selection:** Use `nodeSelector`, `affinity`, `tolerations`

## Connecting to Valkey

### Standalone or Replication

```bash
# Connect to primary
kubectl exec -it <valkey-name>-0 -- valkey-cli

# Get connection info
kubectl get svc <valkey-name>-0-svc
```

### ValkeyPool

```bash
# List all shards
kubectl get valkey -l app.kubernetes.io/managed-by=valkeypool

# Connect to specific shard
kubectl exec -it <pool-name>-0-0 -- valkey-cli
```

### ValkeyCluster

```bash
# Connect to any node (cluster-aware client handles routing)
kubectl exec -it <cluster-name>-0-0 -- valkey-cli -c

# Check cluster status
kubectl exec -it <cluster-name>-0-0 -- valkey-cli cluster info
```

## Monitoring

All examples have metrics exporter enabled by default. If using prometheus-operator:

```bash
# Apply ServiceMonitor
kubectl apply -f production_complete.yaml

# Access Grafana and import Valkey dashboard
# Dashboard ID: 763 (Redis/Valkey dashboard)
```

## Troubleshooting

### Check resource status

```bash
kubectl get valkey,valkeypool,valkeycluster,sentinel
kubectl describe valkey <name>
```

### View logs

```bash
# Valkey logs
kubectl logs <valkey-pod-name> -c valkey

# Operator logs
kubectl logs -n valkey-operator-system deployment/valkey-operator-controller-manager
```

### Debug replication

```bash
kubectl exec -it <valkey-name>-0 -- valkey-cli INFO replication
```

### Debug cluster

```bash
kubectl exec -it <cluster-name>-0-0 -- valkey-cli cluster nodes
kubectl exec -it <cluster-name>-0-0 -- valkey-cli cluster info
```

## Further Reading

- [Design Document](../../docs/design/crd-architecture-proposal-3.md)
- [Valkey Documentation](https://valkey.io/documentation/)
- [Kubernetes Documentation](https://kubernetes.io/docs/home/)
