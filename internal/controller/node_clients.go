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
	"crypto/tls"
	"fmt"

	vclient "github.com/valkey-io/valkey-go"
	"sigs.k8s.io/controller-runtime/pkg/client"

	valkeyiov1alpha1 "valkey.io/valkey-operator/api/v1alpha1"
	"valkey.io/valkey-operator/internal/valkey"
)

// NodeClients is the single front door for Valkey connections in the controller
// layer. It wraps a ClientRegistry and resolves k8s credentials and TLS so
// callers only need to supply a *ValkeyNode — no manual option building.
type NodeClients struct {
	registry  *valkey.ClientRegistry
	k8sClient client.Client
}

// NewNodeClients wraps registry with k8s-aware credential and TLS resolution.
func NewNodeClients(registry *valkey.ClientRegistry, k8sClient client.Client) *NodeClients {
	return &NodeClients{registry: registry, k8sClient: k8sClient}
}

// GetClient resolves credentials and TLS for node, then borrows a client from
// the registry. The caller MUST call the returned release func when done
// (typically via defer).
func (n *NodeClients) GetClient(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) (vclient.Client, func(), error) {
	opt, tlsVersion := n.buildOption(ctx, node)
	key := nodeRegistryKey(node.Namespace, node.Name)
	c, err := n.registry.GetClient(ctx, key, opt, tlsVersion)
	if err != nil {
		return nil, nil, err
	}
	return c, func() { n.registry.Release(key) }, nil
}

// GetExistingClient borrows a client that was already established by a prior
// GetClient call. Returns (nil, nil, false) if the key is not in the registry.
// The caller MUST call the returned release func when done.
func (n *NodeClients) GetExistingClient(key string) (vclient.Client, func(), bool) {
	c, ok := n.registry.GetExistingClient(key)
	if !ok {
		return nil, nil, false
	}
	return c, func() { n.registry.Release(key) }, true
}

// Evict closes and removes the client for key. Delegates to the registry.
func (n *NodeClients) Evict(key string) {
	n.registry.Evict(key)
}

// buildOption derives the valkey-go ClientOption from a ValkeyNode spec.
// Credentials and TLS are resolved from the k8s API on a best-effort basis;
// errors are silently ignored so connection attempts are made with whatever is
// available.
func (n *NodeClients) buildOption(ctx context.Context, node *valkeyiov1alpha1.ValkeyNode) (vclient.ClientOption, string) {
	var tlsConfig *tls.Config
	tlsVersion := ""
	if node.Spec.TLS != nil && node.Spec.TLS.Certificate.SecretName != "" {
		secretName := node.Spec.TLS.Certificate.SecretName
		serverName := ""
		if clusterName, ok := node.Labels[LabelCluster]; ok {
			serverName = fmt.Sprintf("%s.%s.svc.cluster.local", headlessServiceName(clusterName), node.Namespace)
		}
		if cfg, rv, err := getTLSConfig(ctx, n.k8sClient, secretName, serverName, node.Namespace); err == nil {
			tlsConfig = cfg
			tlsVersion = rv
		}
	}

	var username, password string
	if clusterName, ok := node.Labels[LabelCluster]; ok {
		if pwd, _ := fetchSystemUserPassword(ctx, operatorUser, n.k8sClient, clusterName, node.Namespace); pwd != "" {
			username = operatorUser
			password = pwd
		}
	}

	return vclient.ClientOption{
		InitAddress:       []string{fmt.Sprintf("%s:%d", node.Status.PodIP, DefaultPort)},
		ForceSingleClient: true,
		TLSConfig:         tlsConfig,
		Username:          username,
		Password:          password,
	}, tlsVersion
}
