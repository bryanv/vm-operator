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
	toolscache "k8s.io/client-go/tools/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// vmicKind is the kind of the owner of the OVF ConfigMaps.
const vmicKind = "VirtualMachineImageCache"

// vmicOVFConfigMapTransform wraps next with a transform that removes the data
// from ConfigMaps owned by a VirtualMachineImageCache. The label selector keeps
// labeled OVF ConfigMaps out of the cache, but ConfigMaps created before the
// label was introduced are still listed by the informer, and this keeps them
// from consuming memory. The cached copy is annotated so readers know to read
// the real object from the API server.
func vmicOVFConfigMapTransform(next toolscache.TransformFunc) toolscache.TransformFunc {
	return func(in any) (any, error) {
		if cm, ok := in.(*corev1.ConfigMap); ok {
			for _, o := range cm.OwnerReferences {
				if o.Kind == vmicKind {
					cm.Data = nil
					cm.BinaryData = nil
					if cm.Annotations == nil {
						cm.Annotations = map[string]string{}
					}
					cm.Annotations[pkgconst.CacheStrippedAnnotationKey] = ""
					break
				}
			}
		}
		if next != nil {
			return next(in)
		}
		return in, nil
	}
}

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

	if podNamespace == "" {
		return direct
	}

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

	if key.Namespace == c.podNamespace &&
		isConfigMapOrSecret(obj) &&
		!pkgctx.IsLiveRead(ctx) {

		err := c.cached.Get(ctx, key, obj, opts...)
		if err == nil {
			if _, stripped := obj.GetAnnotations()[pkgconst.CacheStrippedAnnotationKey]; !stripped {
				return nil
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		// The object is not in the cache, or its data was stripped from it.
		// Objects such as the OVF ConfigMaps for VirtualMachineImageCache
		// resources are excluded from the cache, and the cache may lag behind
		// recent writes, so fall back to reading from the API server.
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *podNamespaceCachedClient) List(
	ctx context.Context,
	list ctrlclient.ObjectList,
	opts ...ctrlclient.ListOption) error {

	// ConfigMaps are always listed from the API server since the cache omits
	// some of them, ex. the OVF ConfigMaps.
	if _, ok := list.(*corev1.SecretList); ok && !pkgctx.IsLiveRead(ctx) {
		var lo ctrlclient.ListOptions
		lo.ApplyOptions(opts)
		// Field selectors require a cache index, and the cache cannot
		// paginate, so send those requests to the API server.
		if lo.Namespace == c.podNamespace &&
			lo.FieldSelector == nil &&
			lo.Limit == 0 &&
			lo.Continue == "" {

			return c.cached.List(ctx, list, opts...)
		}
	}
	return c.Client.List(ctx, list, opts...)
}
