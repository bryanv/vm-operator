// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package manager

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
)

// vmicOVFConfigMapExcludeSelector returns a label selector that matches all
// ConfigMaps except the VirtualMachineImageCache OVF ConfigMaps.
func vmicOVFConfigMapExcludeSelector() labels.Selector {
	r, err := labels.NewRequirement(
		pkgconst.VMICacheOVFConfigMapLabelKey,
		selection.DoesNotExist,
		nil)
	if err != nil {
		panic(err)
	}
	return labels.NewSelector().Add(*r)
}

// NewPodNamespaceCachedClientFunc returns a function suitable for the
// controller manager's NewClient option. The returned client serves reads of
// ConfigMaps and Secrets in the pod namespace from the manager's cache and
// reads of those types in all other namespaces directly from the API server.
//
// The manager's cache MUST be configured to only cache ConfigMaps and Secrets
// in the pod namespace, otherwise the cache will grow unbounded.
func NewPodNamespaceCachedClientFunc(
	podNamespace string) func(*rest.Config, ctrlclient.Options) (ctrlclient.Client, error) {

	return func(
		config *rest.Config,
		options ctrlclient.Options) (ctrlclient.Client, error) {

		var cached ctrlclient.Reader
		if options.Cache != nil {
			cached = options.Cache.Reader
		}

		// The base client reads ConfigMaps and Secrets directly because they
		// are in the DisableFor list.
		direct, err := ctrlclient.New(config, options)
		if err != nil {
			return nil, err
		}

		if cached == nil {
			return direct, nil
		}

		return NewPodNamespaceCachedClient(podNamespace, cached, direct), nil
	}
}

// NewPodNamespaceCachedClient returns a client that reads ConfigMaps and
// Secrets in the pod namespace from cached, and all other reads and writes
// from direct.
func NewPodNamespaceCachedClient(
	podNamespace string,
	cached ctrlclient.Reader,
	direct ctrlclient.Client) ctrlclient.Client {

	return &podNamespaceCachedClient{
		Client:       direct,
		cached:       cached,
		podNamespace: podNamespace,
	}
}

type podNamespaceCachedClient struct {
	ctrlclient.Client
	cached       ctrlclient.Reader
	podNamespace string
}

func isConfigMapOrSecret(obj any) bool {
	switch obj.(type) {
	case *corev1.ConfigMap, *corev1.ConfigMapList,
		*corev1.Secret, *corev1.SecretList:
		return true
	}
	return false
}

func (c *podNamespaceCachedClient) Get(
	ctx context.Context,
	key ctrlclient.ObjectKey,
	obj ctrlclient.Object,
	opts ...ctrlclient.GetOption) error {

	if key.Namespace == c.podNamespace && isConfigMapOrSecret(obj) {
		err := c.cached.Get(ctx, key, obj, opts...)
		if _, ok := obj.(*corev1.ConfigMap); !ok || !apierrors.IsNotFound(err) {
			return err
		}
		// Some ConfigMaps in the pod namespace, such as the OVF ConfigMaps
		// for VirtualMachineImageCache resources, are excluded from the cache
		// by a label selector, so fall back to reading from the API server.
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *podNamespaceCachedClient) List(
	ctx context.Context,
	list ctrlclient.ObjectList,
	opts ...ctrlclient.ListOption) error {

	if isConfigMapOrSecret(list) {
		var lo ctrlclient.ListOptions
		lo.ApplyOptions(opts)
		if lo.Namespace == c.podNamespace {
			return c.cached.List(ctx, list, opts...)
		}
	}
	return c.Client.List(ctx, list, opts...)
}
