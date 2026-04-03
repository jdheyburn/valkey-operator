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

func TestFindShardForAddress(t *testing.T) {
	state := &valkey.ClusterState{
		Shards: []*valkey.ShardState{
			{
				Id:        "shard-1",
				PrimaryId: "node-1",
				Nodes: []*valkey.NodeState{
					{Address: "10.0.0.1", Id: "node-1", Flags: []string{"master", "myself"}},
					{Address: "10.0.0.2", Id: "node-2", Flags: []string{"slave"}},
				},
			},
			{
				Id:        "shard-2",
				PrimaryId: "node-3",
				Nodes: []*valkey.NodeState{
					{Address: "10.0.0.3", Id: "node-3", Flags: []string{"master"}},
				},
			},
		},
	}

	t.Run("found in first shard", func(t *testing.T) {
		shard := findShardForAddress(state, "10.0.0.1")
		assert.NotNil(t, shard)
		assert.Equal(t, "shard-1", shard.Id)
	})

	t.Run("found replica in first shard", func(t *testing.T) {
		shard := findShardForAddress(state, "10.0.0.2")
		assert.NotNil(t, shard)
		assert.Equal(t, "shard-1", shard.Id)
	})

	t.Run("found in second shard", func(t *testing.T) {
		shard := findShardForAddress(state, "10.0.0.3")
		assert.NotNil(t, shard)
		assert.Equal(t, "shard-2", shard.Id)
	})

	t.Run("not found", func(t *testing.T) {
		shard := findShardForAddress(state, "10.0.0.99")
		assert.Nil(t, shard)
	})
}

func TestShouldFailoverBeforeUpdate(t *testing.T) {
	t.Run("primary with synced replica", func(t *testing.T) {
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
		assert.True(t, shouldFailoverBeforeUpdate(state, "10.0.0.1"))
	})

	t.Run("replica address - should not failover", func(t *testing.T) {
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
		assert.False(t, shouldFailoverBeforeUpdate(state, "10.0.0.2"))
	})

	t.Run("primary with no replicas", func(t *testing.T) {
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
		assert.False(t, shouldFailoverBeforeUpdate(state, "10.0.0.1"))
	})

	t.Run("primary with unsynced replica", func(t *testing.T) {
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
		assert.False(t, shouldFailoverBeforeUpdate(state, "10.0.0.1"))
	})

	t.Run("unknown address", func(t *testing.T) {
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
		assert.False(t, shouldFailoverBeforeUpdate(state, "10.0.0.99"))
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
