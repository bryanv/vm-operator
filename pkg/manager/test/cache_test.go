// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package manager_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkgmgr "github.com/vmware-tanzu/vm-operator/pkg/manager"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func cacheTests() {
	var (
		ctx    *builder.IntegrationTestContext
		client ctrlclient.Client
		cache  *cacheProxy
	)

	BeforeEach(func() {
		ctx = suite.NewIntegrationTestContext()
		client = suite.GetManager().GetClient()
		Expect(suite.GetManager().GetCache()).To(BeAssignableToTypeOf(&cacheProxy{}))
		cache = suite.GetManager().GetCache().(*cacheProxy)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	Context("Getting objects", func() {
		var (
			obj    ctrlclient.Object
			objKey ctrlclient.ObjectKey
		)
		JustBeforeEach(func() {
			obj.SetGenerateName("my-object-")
			obj.SetNamespace(ctx.Namespace)
			Expect(client.Create(ctx, obj)).To(Succeed())
			objKey = ctrlclient.ObjectKeyFromObject(obj)
		})

		Context("ConfigMap", func() {
			BeforeEach(func() {
				obj = &corev1.ConfigMap{}
			})
			It("should return the object with a live read", func() {
				Expect(client.Get(ctx, objKey, obj)).To(Succeed())
				Expect(cache.getCallCountFor(obj)).To(Equal(0))
			})
		})
		Context("Node", func() {
			BeforeEach(func() {
				obj = &corev1.Node{}
			})
			It("should return the object with a live read", func() {
				Expect(client.Get(ctx, objKey, obj)).To(Succeed())
				Expect(cache.getCallCountFor(obj)).To(Equal(1))
			})
		})
		Context("Secret", func() {
			BeforeEach(func() {
				obj = &corev1.Secret{}
			})
			It("should return the object with a live read", func() {
				Expect(client.Get(ctx, objKey, obj)).To(Succeed())
				Expect(cache.getCallCountFor(obj)).To(Equal(0))
			})
		})
	})
}

type cacheProxy struct {
	sync.Mutex
	ctrlcache.Cache
	getCalls map[ctrlclient.Object]int
}

func newCacheProxy(
	config *rest.Config,
	opts ctrlcache.Options) (ctrlcache.Cache, error) {

	cache, err := ctrlcache.New(config, opts)
	if err != nil {
		return nil, err
	}

	return &cacheProxy{
		Cache:    cache,
		getCalls: map[ctrlclient.Object]int{},
	}, nil
}

func (c *cacheProxy) getCallCountFor(obj ctrlclient.Object) int {
	c.Lock()
	defer c.Unlock()
	return c.getCalls[obj]
}

func (c *cacheProxy) Get(
	ctx context.Context,
	key ctrlclient.ObjectKey,
	obj ctrlclient.Object,
	opts ...ctrlclient.GetOption) error {

	c.Lock()
	defer c.Unlock()

	if i, ok := c.getCalls[obj]; ok {
		c.getCalls[obj] = i + 1
	} else {
		c.getCalls[obj] = 1
	}

	return c.Cache.Get(ctx, key, obj, opts...)
}

func clientTests() {
	const podNS = "pod-ns"

	var (
		ctx    context.Context
		client ctrlclient.Client
	)

	newObjs := func(name, source string) []ctrlclient.Object {
		meta := func(ns string) metav1.ObjectMeta {
			return metav1.ObjectMeta{Namespace: ns, Name: name}
		}
		data := map[string]string{"source": source}
		return []ctrlclient.Object{
			&corev1.ConfigMap{ObjectMeta: meta(podNS), Data: data},
			&corev1.ConfigMap{ObjectMeta: meta("other-ns"), Data: data},
			&corev1.Secret{ObjectMeta: meta(podNS), StringData: data},
			&corev1.Secret{ObjectMeta: meta("other-ns"), StringData: data},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		cached := ctrlfake.NewClientBuilder().
			WithObjects(newObjs("obj", "cached")...).Build()
		direct := ctrlfake.NewClientBuilder().
			WithObjects(newObjs("obj", "direct")...).Build()
		client = pkgmgr.NewPodNamespaceCachedClient(podNS, cached, direct)
	})

	DescribeTable("Get",
		func(ns string, obj ctrlclient.Object, expected string) {
			key := ctrlclient.ObjectKey{Namespace: ns, Name: "obj"}
			Expect(client.Get(ctx, key, obj)).To(Succeed())
			switch o := obj.(type) {
			case *corev1.ConfigMap:
				Expect(o.Data).To(HaveKeyWithValue("source", expected))
			case *corev1.Secret:
				Expect(o.StringData).To(HaveKeyWithValue("source", expected))
			}
		},
		Entry("ConfigMap in pod namespace is cached", podNS, &corev1.ConfigMap{}, "cached"),
		Entry("ConfigMap in other namespace is direct", "other-ns", &corev1.ConfigMap{}, "direct"),
		Entry("Secret in pod namespace is cached", podNS, &corev1.Secret{}, "cached"),
		Entry("Secret in other namespace is direct", "other-ns", &corev1.Secret{}, "direct"),
	)

	Context("List", func() {
		It("ConfigMaps in pod namespace are direct", func() {
			var list corev1.ConfigMapList
			Expect(client.List(ctx, &list, ctrlclient.InNamespace(podNS))).To(Succeed())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Data).To(HaveKeyWithValue("source", "direct"))
		})
		It("Secrets in pod namespace with a limit are direct", func() {
			var list corev1.SecretList
			Expect(client.List(ctx, &list, ctrlclient.InNamespace(podNS), ctrlclient.Limit(5))).To(Succeed())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].StringData).To(HaveKeyWithValue("source", "direct"))
		})
		It("Secrets in pod namespace with a live read context are direct", func() {
			var list corev1.SecretList
			Expect(client.List(pkgctx.WithLiveRead(ctx), &list, ctrlclient.InNamespace(podNS))).To(Succeed())
			Expect(list.Items[0].StringData).To(HaveKeyWithValue("source", "direct"))
		})
		It("Secrets in pod namespace are cached", func() {
			var list corev1.SecretList
			Expect(client.List(ctx, &list, ctrlclient.InNamespace(podNS))).To(Succeed())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].StringData).To(HaveKeyWithValue("source", "cached"))
		})
		It("ConfigMaps in other namespace are direct", func() {
			var list corev1.ConfigMapList
			Expect(client.List(ctx, &list, ctrlclient.InNamespace("other-ns"))).To(Succeed())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Data).To(HaveKeyWithValue("source", "direct"))
		})
		It("ConfigMaps in all namespaces are direct", func() {
			var list corev1.ConfigMapList
			Expect(client.List(ctx, &list)).To(Succeed())
			Expect(list.Items).To(HaveLen(2))
			for _, i := range list.Items {
				Expect(i.Data).To(HaveKeyWithValue("source", "direct"))
			}
		})
	})

	Context("Other types", func() {
		It("Get is direct", func() {
			Expect(client.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: podNS},
			})).To(Succeed())
			Expect(client.Get(ctx, ctrlclient.ObjectKey{Name: podNS}, &corev1.Namespace{})).To(Succeed())
		})
	})

	Context("Writes", func() {
		It("go to the direct client", func() {
			Expect(client.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: podNS, Name: "new"},
			})).To(Succeed())
			// Not in the cache yet, so the Get falls back to the API server.
			Expect(client.Get(ctx, ctrlclient.ObjectKey{Namespace: podNS, Name: "new"}, &corev1.Secret{})).To(Succeed())
		})
	})

	Context("ConfigMap in pod namespace not in the cache", func() {
		It("Get falls back to the direct client", func() {
			Expect(client.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: podNS, Name: "new"},
				Data:       map[string]string{"source": "direct"},
			})).To(Succeed())
			var obj corev1.ConfigMap
			Expect(client.Get(ctx, ctrlclient.ObjectKey{Namespace: podNS, Name: "new"}, &obj)).To(Succeed())
			Expect(obj.Data).To(HaveKeyWithValue("source", "direct"))
		})
	})

	Context("Live read", func() {
		It("Get is direct", func() {
			var obj corev1.ConfigMap
			key := ctrlclient.ObjectKey{Namespace: podNS, Name: "obj"}
			Expect(client.Get(pkgctx.WithLiveRead(ctx), key, &obj)).To(Succeed())
			Expect(obj.Data).To(HaveKeyWithValue("source", "direct"))
		})
	})

	Context("Cached ConfigMap with stripped data", func() {
		It("Get falls back to the direct client", func() {
			cached := ctrlfake.NewClientBuilder().WithObjects(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   podNS,
					Name:        "obj",
					Annotations: map[string]string{pkgconst.CacheStrippedAnnotationKey: ""},
				},
			}).Build()
			direct := ctrlfake.NewClientBuilder().
				WithObjects(newObjs("obj", "direct")...).Build()
			c := pkgmgr.NewPodNamespaceCachedClient(podNS, cached, direct)
			var obj corev1.ConfigMap
			Expect(c.Get(ctx, ctrlclient.ObjectKey{Namespace: podNS, Name: "obj"}, &obj)).To(Succeed())
			Expect(obj.Data).To(HaveKeyWithValue("source", "direct"))
		})
	})

	Context("Empty pod namespace", func() {
		It("does not wrap the direct client", func() {
			direct := ctrlfake.NewClientBuilder().Build()
			Expect(pkgmgr.NewPodNamespaceCachedClient("", nil, direct)).To(BeIdenticalTo(direct))
		})
	})
}
