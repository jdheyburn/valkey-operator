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
	"maps"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
	valkeyv1 "valkey.io/valkey-operator/api/v1alpha1"
)

const appName = "valkey"

// Labels returns a copy of user defined labels including recommended:
// https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labels(cluster *valkeyv1.ValkeyCluster) map[string]string {
	if cluster.Labels == nil {
		cluster.Labels = make(map[string]string)
	}
	l := maps.Clone(cluster.Labels)
	l["app.kubernetes.io/name"] = appName
	l["app.kubernetes.io/instance"] = cluster.Name
	l["app.kubernetes.io/component"] = "valkey-cluster"
	l["app.kubernetes.io/part-of"] = appName
	l["app.kubernetes.io/managed-by"] = "valkey-operator"
	return l
}

// valkeyNodeLabels returns the standard labels for ValkeyNode resources.
func valkeyNodeLabels(node *valkeyiov1alpha1.ValkeyNode) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "valkey",
		"app.kubernetes.io/instance":   node.Name,
		"app.kubernetes.io/managed-by": "valkey-operator",
		"app.kubernetes.io/component":  "valkey-node",
	}
}

// Annotations returns a copy of user defined annotations.
func annotations(cluster *valkeyv1.ValkeyCluster) map[string]string {
	return maps.Clone(cluster.Annotations)
}
