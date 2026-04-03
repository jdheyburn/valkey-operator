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

// findShardForAddress returns the shard containing a node with the given IP address,
// or nil if no shard contains that address.
func findShardForAddress(state *valkey.ClusterState, address string) *valkey.ShardState {
	for _, shard := range state.Shards {
		for _, node := range shard.Nodes {
			if node.Address == address {
				return shard
			}
		}
	}
	return nil
}

// shouldFailoverBeforeUpdate returns true if the node at the given address is a
// primary with at least one synced replica, meaning we should perform a graceful
// failover before updating it.
func shouldFailoverBeforeUpdate(state *valkey.ClusterState, address string) bool {
	shard := findShardForAddress(state, address)
	if shard == nil {
		return false
	}
	// Check if the node at this address is the primary.
	primary := shard.GetPrimaryNode()
	if primary == nil || primary.Address != address {
		return false
	}
	// Only failover if there is at least one synced replica to promote.
	return len(shard.GetSyncedReplicas()) > 0
}

// proactiveFailover issues CLUSTER FAILOVER to the best synced replica in the
// shard containing the given address, then polls until the replica reports
// role:master or the timeout is reached.
func proactiveFailover(ctx context.Context, recorder events.EventRecorder, cluster *valkeyiov1alpha1.ValkeyCluster, state *valkey.ClusterState, address string) error {
	log := logf.FromContext(ctx)

	shard := findShardForAddress(state, address)
	if shard == nil {
		recorder.Eventf(cluster, nil, corev1.EventTypeWarning, "FailoverSkipped", "ProactiveFailover", "No shard found for address %s", address)
		return fmt.Errorf("no shard found for address %s", address)
	}

	replicas := shard.GetSyncedReplicas()
	if len(replicas) == 0 {
		recorder.Eventf(cluster, nil, corev1.EventTypeWarning, "FailoverSkipped", "ProactiveFailover", "No synced replicas available for failover in shard %s", shard.Id)
		return fmt.Errorf("no synced replicas in shard %s", shard.Id)
	}

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
	deadline := time.After(proactiveFailoverTimeout)
	ticker := time.NewTicker(proactiveFailoverPoll)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
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

// specEqual returns true if two ValkeyNodeSpec values are deeply equal.
func specEqual(a, b valkeyiov1alpha1.ValkeyNodeSpec) bool {
	return reflect.DeepEqual(a, b)
}
