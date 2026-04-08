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
	"fmt"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
	"valkey.io/valkey-operator/internal/valkey"
)

const (
	proactiveFailoverTimeout = 10 * time.Second
	proactiveFailoverPoll    = 1 * time.Second
)

// findFailoverShard returns the shard if the node at address is a primary with
// at least one synced replica (meaning a graceful failover should be attempted
// before updating it), or nil if no failover is needed.
func findFailoverShard(state *valkey.ClusterState, address string) *valkey.ShardState {
	shard := state.FindShardForAddress(address)
	if shard == nil {
		return nil
	}
	primary := shard.GetPrimaryNode()
	if primary == nil || primary.Address != address {
		return nil
	}
	if len(shard.GetSyncedReplicas()) == 0 {
		return nil
	}
	return shard
}

// proactiveFailover issues CLUSTER FAILOVER to the best synced replica in shard,
// then polls until the replica reports role:master or the timeout is reached.
// shard must be non-nil and have at least one synced replica.
func proactiveFailover(ctx context.Context, recorder events.EventRecorder, cluster *valkeyiov1alpha1.ValkeyCluster, shard *valkey.ShardState, address string) error {
	log := logf.FromContext(ctx)

	replicas := shard.GetSyncedReplicas()

	// Pick the first synced replica as the failover target. The ordering is
	// determined by node discovery order — no priority scheme is applied yet.
	target := replicas[0]
	log.Info("initiating proactive failover", "shard", shard.Id, "target", target.Address)

	// Emit FailoverInitiated before the command so observers see the event at
	// the moment the failover begins, not after.
	recorder.Eventf(cluster, nil, corev1.EventTypeNormal, "FailoverInitiated", "ProactiveFailover", "Initiated failover from %s to %s in shard %s", address, target.Address, shard.Id)

	// Issue CLUSTER FAILOVER on the replica.
	err := target.Client.Do(ctx, target.Client.B().ClusterFailover().Build()).Error()
	if err != nil {
		recorder.Eventf(cluster, nil, corev1.EventTypeWarning, "FailoverFailed", "ProactiveFailover", "CLUSTER FAILOVER command failed on %s: %v", target.Address, err)
		return fmt.Errorf("CLUSTER FAILOVER failed on %s: %w", target.Address, err)
	}

	// Poll until the replica reports role:master or timeout.
	timer := time.NewTimer(proactiveFailoverTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(proactiveFailoverPoll)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			recorder.Eventf(cluster, nil, corev1.EventTypeWarning, "FailoverTimeout", "ProactiveFailover", "Failover to %s in shard %s did not complete within %s", target.Address, shard.Id, proactiveFailoverTimeout)
			return fmt.Errorf("failover to %s timed out after %s", target.Address, proactiveFailoverTimeout)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := target.Client.Do(ctx, target.Client.B().Info().Section("replication").Build()).ToString()
			if err != nil {
				log.V(1).Info("failed to query INFO replication during failover poll", "target", target.Address, "err", err)
				continue
			}
			role := parseValkeyRole(info)
			if role == RolePrimary {
				recorder.Eventf(cluster, nil, corev1.EventTypeNormal, "FailoverCompleted", "ProactiveFailover", "Failover completed: %s is now primary in shard %s", target.Address, shard.Id)
				log.Info("proactive failover completed", "newPrimary", target.Address, "shard", shard.Id)
				return nil
			}
		}
	}
}

// nodeRequiresRoll returns true if the current ValkeyNode has a running pod
// whose spec differs from the desired spec, meaning CreateOrUpdate will
// trigger a pod roll.
func nodeRequiresRoll(current *valkeyiov1alpha1.ValkeyNode, desired *valkeyiov1alpha1.ValkeyNode) bool {
	return current.Status.PodIP != "" && !reflect.DeepEqual(current.Spec, desired.Spec)
}

// shardNodeOrder returns nodeIndices for a shard ordered replicas-first,
// primary-last. When clusterState is available, the actual primary is
// determined from live cluster topology (handles post-failover scenarios where
// a replica has been promoted). Otherwise falls back to reverse-index order
// (assumes node-index 0 is primary).
func shardNodeOrder(clusterName string, shardIndex, nodesPerShard int, clusterState *valkey.ClusterState, nodeList *valkeyiov1alpha1.ValkeyNodeList) []int {
	// Build lookup from name to PodIP for this shard's nodes.
	byName := make(map[string]string, nodesPerShard)
	for i := range nodeList.Items {
		byName[nodeList.Items[i].Name] = nodeList.Items[i].Status.PodIP
	}

	primaryIdx := -1
	if clusterState != nil {
		for nodeIndex := range nodesPerShard {
			ip := byName[valkeyNodeName(clusterName, shardIndex, nodeIndex)]
			if ip == "" {
				continue
			}
			if shard := clusterState.FindShardForAddress(ip); shard != nil {
				if primary := shard.GetPrimaryNode(); primary != nil && primary.Address == ip {
					primaryIdx = nodeIndex
					break
				}
			}
		}
	}

	// Default reverse order when cluster state is unavailable or primary
	// could not be identified.
	if primaryIdx == -1 {
		indices := make([]int, nodesPerShard)
		for i := range nodesPerShard {
			indices[i] = nodesPerShard - 1 - i
		}
		return indices
	}

	// Replicas first (reverse order among non-primary), then primary last.
	indices := make([]int, 0, nodesPerShard)
	for nodeIndex := nodesPerShard - 1; nodeIndex >= 0; nodeIndex-- {
		if nodeIndex != primaryIdx {
			indices = append(indices, nodeIndex)
		}
	}
	indices = append(indices, primaryIdx)
	return indices
}

// anyNodeRequiresRoll returns true if any existing ValkeyNode in the list has
// a spec diff against what the cluster would build for it. Used as a cheap
// pre-flight check to avoid opening Valkey connections on steady-state reconciles.
func anyNodeRequiresRoll(cluster *valkeyiov1alpha1.ValkeyCluster, nodeList *valkeyiov1alpha1.ValkeyNodeList) bool {
	byName := make(map[string]*valkeyiov1alpha1.ValkeyNode, len(nodeList.Items))
	for i := range nodeList.Items {
		byName[nodeList.Items[i].Name] = &nodeList.Items[i]
	}
	nodesPerShard := 1 + int(cluster.Spec.Replicas)
	for shardIndex := range int(cluster.Spec.Shards) {
		for nodeIndex := range nodesPerShard {
			desired := buildClusterValkeyNode(cluster, shardIndex, nodeIndex)
			if current, ok := byName[desired.Name]; ok && nodeRequiresRoll(current, desired) {
				return true
			}
		}
	}
	return false
}
