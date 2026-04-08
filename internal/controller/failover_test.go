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
	"testing"

	"github.com/stretchr/testify/assert"
	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
	"valkey.io/valkey-operator/internal/valkey"
)

func makeNodeList(clusterName string, shardIndex int, ips ...string) *valkeyiov1alpha1.ValkeyNodeList {
	list := &valkeyiov1alpha1.ValkeyNodeList{}
	for nodeIndex, ip := range ips {
		node := valkeyiov1alpha1.ValkeyNode{}
		node.Name = valkeyNodeName(clusterName, shardIndex, nodeIndex)
		node.Status.PodIP = ip
		list.Items = append(list.Items, node)
	}
	return list
}

func TestShardNodeOrder(t *testing.T) {
	t.Run("nil cluster state falls back to reverse index order", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1", "10.0.0.2", "10.0.0.3")
		order := shardNodeOrder("test", 0, 3, nil, nodes)
		assert.Equal(t, []int{2, 1, 0}, order)
	})

	t.Run("node-index 0 is primary (default topology)", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1", "10.0.0.2", "10.0.0.3")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-0",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}},
						{Address: "10.0.0.3", Id: "node-3", Flags: []string{"slave"}},
					},
				},
			},
		}
		order := shardNodeOrder("test", 0, 3, state, nodes)
		// Replicas (2, 1) first, then primary (0) last
		assert.Equal(t, []int{2, 1, 0}, order)
	})

	t.Run("node-index 1 is primary after failover", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1", "10.0.0.2", "10.0.0.3")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-0",
					PrimaryId: "node-2",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"slave"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"master"}},
						{Address: "10.0.0.3", Id: "node-3", Flags: []string{"slave"}},
					},
				},
			},
		}
		order := shardNodeOrder("test", 0, 3, state, nodes)
		// Replicas (2, 0) first, then primary (1) last
		assert.Equal(t, []int{2, 0, 1}, order)
	})

	t.Run("last node-index is primary after failover", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1", "10.0.0.2", "10.0.0.3")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-0",
					PrimaryId: "node-3",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"slave"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}},
						{Address: "10.0.0.3", Id: "node-3", Flags: []string{"master"}},
					},
				},
			},
		}
		order := shardNodeOrder("test", 0, 3, state, nodes)
		// Replicas (1, 0) first, then primary (2) last
		assert.Equal(t, []int{1, 0, 2}, order)
	})

	t.Run("single node shard returns just that node", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-0",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
					},
				},
			},
		}
		order := shardNodeOrder("test", 0, 1, state, nodes)
		assert.Equal(t, []int{0}, order)
	})

	t.Run("node IP not found in cluster state falls back to reverse order", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "10.0.0.1", "10.0.0.2")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{},
		}
		order := shardNodeOrder("test", 0, 2, state, nodes)
		assert.Equal(t, []int{1, 0}, order)
	})

	t.Run("node without PodIP falls back to reverse order", func(t *testing.T) {
		nodes := makeNodeList("test", 0, "", "", "")
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-0",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
					},
				},
			},
		}
		order := shardNodeOrder("test", 0, 3, state, nodes)
		assert.Equal(t, []int{2, 1, 0}, order)
	})
}

func TestFindFailoverShard(t *testing.T) {
	t.Run("primary with synced replica returns shard", func(t *testing.T) {
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-1",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}, Info: map[string]string{"master_link_status": "up"}},
					},
				},
			},
		}
		assert.NotNil(t, findFailoverShard(state, "10.0.0.1"))
	})

	t.Run("replica address returns nil", func(t *testing.T) {
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-1",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}, Info: map[string]string{"master_link_status": "up"}},
					},
				},
			},
		}
		assert.Nil(t, findFailoverShard(state, "10.0.0.2"))
	})

	t.Run("primary with no replicas returns nil", func(t *testing.T) {
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-1",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
					},
				},
			},
		}
		assert.Nil(t, findFailoverShard(state, "10.0.0.1"))
	})

	t.Run("primary with unsynced replica returns nil", func(t *testing.T) {
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-1",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
						{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}, Info: map[string]string{"master_link_status": "down"}},
					},
				},
			},
		}
		assert.Nil(t, findFailoverShard(state, "10.0.0.1"))
	})

	t.Run("unknown address returns nil", func(t *testing.T) {
		state := &valkey.ClusterState{
			Shards: []*valkey.ShardState{
				{
					Id:        "shard-1",
					PrimaryId: "node-1",
					Nodes: []*valkey.NodeState{
						{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master"}},
					},
				},
			},
		}
		assert.Nil(t, findFailoverShard(state, "10.0.0.99"))
	})
}

func TestNodeRequiresRoll(t *testing.T) {
	t.Run("different spec with pod IP requires roll", func(t *testing.T) {
		current := &valkeyiov1alpha1.ValkeyNode{}
		current.Spec.Image = "valkey:8.1"
		current.Status.PodIP = "10.0.0.1"
		desired := &valkeyiov1alpha1.ValkeyNode{}
		desired.Spec.Image = "valkey:8.2"
		assert.True(t, nodeRequiresRoll(current, desired))
	})

	t.Run("same spec does not require roll", func(t *testing.T) {
		current := &valkeyiov1alpha1.ValkeyNode{}
		current.Spec.Image = "valkey:8.1"
		current.Status.PodIP = "10.0.0.1"
		desired := &valkeyiov1alpha1.ValkeyNode{}
		desired.Spec.Image = "valkey:8.1"
		assert.False(t, nodeRequiresRoll(current, desired))
	})

	t.Run("different spec but no pod IP does not require roll", func(t *testing.T) {
		current := &valkeyiov1alpha1.ValkeyNode{}
		current.Spec.Image = "valkey:8.1"
		desired := &valkeyiov1alpha1.ValkeyNode{}
		desired.Spec.Image = "valkey:8.2"
		assert.False(t, nodeRequiresRoll(current, desired))
	})
}
