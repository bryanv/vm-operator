// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/vim25/mo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	netsetutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube/networksettings"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("RetireProviderResources", Label(testlabels.API), func() {

	const (
		namespace  = "retire-ns"
		adapterKey = int32(4000)
		otherKey   = int32(4001)
		oldPG      = "dvportgroup-11"
		newPG      = "dvportgroup-12"
		oldExtID   = "ext-old"
		newExtID   = "ext-new"
		oldName    = "retire-vm-old-eth0"
		newName    = "retire-vm-new-eth0"
	)

	var (
		ctx       context.Context
		vm        *vmopv1.VirtualMachine
		funcs     interceptor.Funcs
		client    ctrlclient.Client
		objs      []ctrlclient.Object
		info      *network.Info
		submitted bool

		deleteCalls []ctrlclient.DeleteOptions
	)

	ownedBy := func(obj ctrlclient.Object) ctrlclient.Object {
		obj.SetNamespace(namespace)
		obj.SetLabels(map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name})
		obj.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: vmopv1.GroupVersion.String(),
			Kind:       "VirtualMachine",
			Name:       vm.Name,
			UID:        vm.UID,
		}})
		return obj
	}

	netOP := func(name string, status netopv1alpha1.NetworkInterfaceStatus) ctrlclient.Object {
		return ownedBy(&netopv1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status:     status,
		})
	}

	adapter := func(key int32, pg, extID, mac string) network.ObservedAdapter {
		a := network.ObservedAdapter{
			Key: key,
			MAC: mac,
			Attachment: network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: pg},
				ExternalID: network.FieldIntent{Op: network.IntentClear},
			},
		}
		if extID != "" {
			a.Attachment.ExternalID = network.FieldIntent{Op: network.IntentSet, Value: extID}
		}
		return a
	}

	desired := func(name string, phase network.ResolutionPhase, ref network.ProviderRef) network.InterfaceState {
		return network.InterfaceState{
			Spec:     vmopv1.VirtualMachineNetworkInterfaceSpec{Name: name},
			Provider: network.Resolution{Phase: phase, Ref: ref},
			Match:    network.Match{Kind: network.MatchIdentity, AdapterKey: adapterKey},
		}
	}

	vdsRef := func(name string) network.ProviderRef {
		return network.ProviderRef{
			Type:      pkgcfg.NetworkProviderTypeVDS,
			Namespace: namespace,
			Name:      name,
		}
	}

	// start creates the client with the VM and objs, and points the
	// observation at the stored VM. Specs mutate objs and info first.
	start := func() {
		GinkgoHelper()
		if client != nil {
			return
		}
		client = builder.NewFakeClientWithInterceptors(funcs, append([]ctrlclient.Object{vm}, objs...)...)

		stored := &vmopv1.VirtualMachine{}
		Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), stored)).To(Succeed())
		vm = stored
		info.Identity = network.ObservationIdentityForTest(vm, mo.VirtualMachine{})
	}

	exists := func(obj ctrlclient.Object) bool {
		GinkgoHelper()
		start()
		err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj.DeepCopyObject().(ctrlclient.Object))
		if apierrors.IsNotFound(err) {
			return false
		}
		Expect(err).ToNot(HaveOccurred())
		return true
	}

	retire := func() []network.RetireResult {
		start()

		// The observation lists the owned objects as stored, with their UID
		// and resource version.
		var owned []ctrlclient.Object
		for _, obj := range objs {
			stored := obj.DeepCopyObject().(ctrlclient.Object)
			Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), stored)).To(Succeed())
			owned = append(owned, stored)
		}
		network.SetInfoOwnedObjectsForTest(info, owned)

		return network.RetireProviderResources(ctx, network.RetireInput{
			Client:    client,
			VM:        vm,
			Observed:  info,
			Submitted: submitted,
		})
	}

	BeforeEach(func() {
		ctx = context.Background()
		client = nil
		submitted = false
		deleteCalls = nil
		funcs = interceptor.Funcs{
			Delete: func(
				ctx context.Context,
				c ctrlclient.WithWatch,
				obj ctrlclient.Object,
				opts ...ctrlclient.DeleteOption) error {

				o := ctrlclient.DeleteOptions{}
				o.ApplyOptions(opts)
				deleteCalls = append(deleteCalls, o)
				return c.Delete(ctx, obj, opts...)
			},
		}

		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "retire-vm",
				Namespace:  namespace,
				UID:        types.UID(uuid.NewString()),
				Generation: 2,
			},
		}

		objs = []ctrlclient.Object{
			netOP(newName, netopv1alpha1.NetworkInterfaceStatus{
				NetworkID:  newPG,
				ExternalID: newExtID,
			}),
			netOP(oldName, netopv1alpha1.NetworkInterfaceStatus{
				NetworkID:  oldPG,
				ExternalID: oldExtID,
			}),
		}

		info = &network.Info{
			Enabled:  true,
			Complete: true,
			Interfaces: []network.InterfaceState{
				desired("eth0", network.ResolutionReady, vdsRef(newName)),
			},
			Adapters: []network.ObservedAdapter{
				adapter(adapterKey, newPG, newExtID, "00:50:56:00:00:01"),
			},
			Disposition: map[int32]network.AdapterDisposition{
				adapterKey: network.AdapterDispositionClaimed,
			},
		}
	})

	It("returns nothing without an observation", func() {
		start()
		Expect(network.RetireProviderResources(ctx, network.RetireInput{
			Client: client,
			VM:     vm,
		})).To(BeEmpty())
	})

	It("deletes an unreferenced resource no adapter uses, with UID and resource version preconditions", func() {
		results := retire()
		Expect(results).To(HaveLen(1))
		Expect(results[0].Ref.Name).To(Equal(oldName))
		Expect(results[0].Outcome).To(Equal(network.RetireOutcomeDeleted))
		Expect(results[0].Reason).To(Equal(network.RetireReasonDeleted))
		Expect(network.RetireRequeueAfter(results)).To(BeZero())

		Expect(exists(objs[1])).To(BeFalse())
		Expect(exists(objs[0])).To(BeTrue())

		Expect(deleteCalls).To(HaveLen(1))
		pre := deleteCalls[0].Preconditions
		Expect(pre).ToNot(BeNil())
		Expect(pre.UID).ToNot(BeNil())
		Expect(*pre.UID).To(Equal(results[0].Ref.UID))
		Expect(pre.ResourceVersion).ToNot(BeNil())
		Expect(*pre.ResourceVersion).ToNot(BeEmpty())
	})

	It("returns nothing when every owned resource is desired", func() {
		objs = objs[:1]
		Expect(retire()).To(BeEmpty())
		Expect(deleteCalls).To(BeEmpty())
	})

	DescribeTable("retains a resource whose attachment an adapter still uses, whatever the adapter's disposition",
		func(d network.AdapterDisposition) {
			info.Adapters = append(info.Adapters, adapter(otherKey, oldPG, oldExtID, "00:50:56:00:00:02"))
			info.Disposition[otherKey] = d

			results := retire()
			Expect(results).To(ConsistOf(And(
				HaveField("Ref.Name", oldName),
				HaveField("Outcome", network.RetireOutcomeRetained),
				HaveField("Reason", network.RetireReasonAttachmentInUse),
				HaveField("Adapter", otherKey),
			)))
			Expect(network.RetireRequeueAfter(results)).To(BeZero())
			Expect(exists(objs[1])).To(BeTrue())
			Expect(deleteCalls).To(BeEmpty())
		},
		Entry("claimed, pending replacement", network.AdapterDispositionClaimed),
		Entry("protected", network.AdapterDispositionProtected),
		Entry("retiring", network.AdapterDispositionRetiring),
		Entry("unclaimed, removal deferred", network.AdapterDispositionUnclaimed),
	)

	It("deletes the resource once the hardware no longer uses its attachment", func() {
		info.Adapters = append(info.Adapters, adapter(otherKey, oldPG, oldExtID, "00:50:56:00:00:02"))
		info.Disposition[otherKey] = network.AdapterDispositionRetiring
		Expect(retire()).To(ConsistOf(HaveField("Outcome", network.RetireOutcomeRetained)))

		// The next pass observes that the adapter was removed.
		info.Adapters = info.Adapters[:1]
		delete(info.Disposition, otherKey)
		Expect(retire()).To(ConsistOf(HaveField("Outcome", network.RetireOutcomeDeleted)))
		Expect(exists(objs[1])).To(BeFalse())
	})

	Context("a NetOP NetworkInterface without an external ID", func() {
		BeforeEach(func() {
			objs[1] = netOP(oldName, netopv1alpha1.NetworkInterfaceStatus{NetworkID: oldPG})
		})

		DescribeTable("compares the backing and the MAC address",
			func(a network.ObservedAdapter, mac string, reason string, outcome network.RetireOutcome) {
				if mac != "" {
					objs[1] = netOP(oldName, netopv1alpha1.NetworkInterfaceStatus{
						NetworkID:  oldPG,
						MacAddress: mac,
					})
				}
				info.Adapters = append(info.Adapters, a)
				info.Disposition[a.Key] = network.AdapterDispositionUnclaimed

				Expect(retire()).To(ConsistOf(And(
					HaveField("Outcome", outcome),
					HaveField("Reason", reason),
				)))
			},
			Entry("retains as ambiguous when only the backing is shared",
				adapter(otherKey, oldPG, "", "00:50:56:00:00:02"), "",
				network.RetireReasonAttachmentAmbiguous, network.RetireOutcomeRetained),
			Entry("retains when the backing and the MAC address match",
				adapter(otherKey, oldPG, "", "00:50:56:00:00:02"), "00:50:56:00:00:02",
				network.RetireReasonAttachmentInUse, network.RetireOutcomeRetained),
			Entry("deletes when the MAC address differs",
				adapter(otherKey, oldPG, "", "00:50:56:00:00:02"), "00:50:56:00:00:03",
				network.RetireReasonDeleted, network.RetireOutcomeDeleted),
			Entry("deletes when the backing differs",
				adapter(otherKey, "dvportgroup-99", "", "00:50:56:00:00:02"), "",
				network.RetireReasonDeleted, network.RetireOutcomeDeleted),
			Entry("deletes when the adapter has another resource's external ID",
				adapter(otherKey, oldPG, "ext-other", "00:50:56:00:00:02"), "",
				network.RetireReasonDeleted, network.RetireOutcomeDeleted),
		)
	})

	DescribeTable("compares NSX resources by external ID",
		func(obj ctrlclient.Object, adapterExtID string, outcome network.RetireOutcome) {
			objs[1] = ownedBy(obj)
			info.Adapters = append(info.Adapters, adapter(otherKey, "", adapterExtID, "00:50:56:00:00:02"))
			info.Disposition[otherKey] = network.AdapterDispositionUnclaimed

			Expect(retire()).To(ConsistOf(HaveField("Outcome", outcome)))
		},
		Entry("NCP in use",
			&ncpv1alpha1.VirtualNetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: oldName},
				Status:     ncpv1alpha1.VirtualNetworkInterfaceStatus{InterfaceID: oldExtID},
			}, oldExtID, network.RetireOutcomeRetained),
		Entry("NCP unused",
			&ncpv1alpha1.VirtualNetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: oldName},
				Status:     ncpv1alpha1.VirtualNetworkInterfaceStatus{InterfaceID: oldExtID},
			}, "ext-other", network.RetireOutcomeDeleted),
		Entry("VPC in use",
			&vpcv1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: oldName},
				Status: vpcv1alpha1.SubnetPortStatus{
					Attachment: vpcv1alpha1.PortAttachment{ID: oldExtID},
				},
			}, oldExtID, network.RetireOutcomeRetained),
		Entry("VPC unused",
			&vpcv1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: oldName},
				Status: vpcv1alpha1.SubnetPortStatus{
					Attachment: vpcv1alpha1.PortAttachment{ID: oldExtID},
				},
			}, "ext-other", network.RetireOutcomeDeleted),
	)

	Context("a resource that reports no attachment", func() {
		BeforeEach(func() {
			objs[1] = netOP(oldName, netopv1alpha1.NetworkInterfaceStatus{})
		})

		It("is retained while an adapter is not claimed", func() {
			info.Adapters = append(info.Adapters, adapter(otherKey, oldPG, "", ""))
			info.Disposition[otherKey] = network.AdapterDispositionProtected

			Expect(retire()).To(ConsistOf(And(
				HaveField("Outcome", network.RetireOutcomeRetained),
				HaveField("Reason", network.RetireReasonAttachmentUnknown),
				HaveField("Adapter", otherKey),
			)))
		})

		It("is deleted when every adapter is claimed", func() {
			Expect(retire()).To(ConsistOf(HaveField("Outcome", network.RetireOutcomeDeleted)))
		})
	})

	It("keeps the resource of a desired interface that is not ready", func() {
		pendingName := "retire-vm-pending-eth1"
		objs = append(objs, netOP(pendingName, netopv1alpha1.NetworkInterfaceStatus{}))
		info.Interfaces = append(info.Interfaces,
			desired("eth1", network.ResolutionPending, vdsRef(pendingName)))

		results := retire()
		Expect(results).To(ConsistOf(HaveField("Ref.Name", oldName)))
		Expect(exists(objs[2])).To(BeTrue())
	})

	It("keys the desired set by provider type and name", func() {
		sameName := ownedBy(&vpcv1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{Name: newName},
		})
		objs = append(objs[:1], sameName)

		results := retire()
		Expect(results).To(ConsistOf(And(
			HaveField("Ref.Type", pkgcfg.NetworkProviderTypeVPC),
			HaveField("Ref.Name", newName),
			HaveField("Outcome", network.RetireOutcomeDeleted),
		)))
		Expect(exists(objs[0])).To(BeTrue())
	})

	DescribeTable("retains every candidate",
		func(mutate func(), reason string) {
			info.Interfaces = append(info.Interfaces,
				desired("eth1", network.ResolutionReady, vdsRef("retire-vm-x-eth1")))
			objs = append(objs, netOP("retire-vm-other-eth2", netopv1alpha1.NetworkInterfaceStatus{}))
			mutate()

			results := retire()
			Expect(results).To(HaveLen(2))
			Expect(results).To(HaveEach(And(
				HaveField("Outcome", network.RetireOutcomeRetained),
				HaveField("Reason", reason),
			)))
			Expect(network.RetireRequeueAfter(results)).To(BeZero())
			Expect(deleteCalls).To(BeEmpty())
		},
		Entry("when the observation is incomplete",
			func() { info.Complete = false },
			network.RetireReasonObservationIncomplete),
		Entry("when network device changes were submitted on this pass",
			func() { submitted = true },
			network.RetireReasonHardwareChangeSubmitted),
		Entry("when a desired interface has no provider resource name",
			func() { info.Interfaces[1].Provider.Ref.Name = "" },
			network.RetireReasonDesiredUnknown),
	)

	Context("re-checking the desired identity before the delete", func() {

		It("cancels retirement when the spec changed after the observation", func() {
			// The observation was made from generation N; the user changed
			// the spec, e.g. back to the old network, before the delete.
			start()
			info.Identity.Generation--

			Expect(retire()).To(ConsistOf(And(
				HaveField("Outcome", network.RetireOutcomeRetained),
				HaveField("Reason", network.RetireReasonDesiredChanged),
			)))
			Expect(exists(objs[1])).To(BeTrue())
			Expect(deleteCalls).To(BeEmpty())
		})

		It("cancels retirement when the VirtualMachine was recreated", func() {
			start()
			info.Identity.VMUID = types.UID(uuid.NewString())

			Expect(retire()).To(ConsistOf(HaveField("Reason", network.RetireReasonDesiredChanged)))
			Expect(exists(objs[1])).To(BeTrue())
		})

		It("retains without a retry when the VirtualMachine is gone", func() {
			start()
			Expect(client.Delete(ctx, vm)).To(Succeed())

			results := retire()
			Expect(results).To(ConsistOf(HaveField("Reason", network.RetireReasonDesiredChanged)))
			Expect(network.RetireRequeueAfter(results)).To(BeZero())
			Expect(exists(objs[1])).To(BeTrue())
		})

		When("the VirtualMachine cannot be read", func() {
			var failVMGet bool

			BeforeEach(func() {
				failVMGet = false
				funcs.Get = func(
					ctx context.Context,
					c ctrlclient.WithWatch,
					key ctrlclient.ObjectKey,
					obj ctrlclient.Object,
					opts ...ctrlclient.GetOption) error {

					if _, ok := obj.(*vmopv1.VirtualMachine); ok && failVMGet {
						return errors.New("fake get error")
					}
					return c.Get(ctx, key, obj, opts...)
				}
			})

			It("records a failure that asks for a retry", func() {
				start()
				failVMGet = true
				results := retire()
				Expect(results).To(ConsistOf(And(
					HaveField("Outcome", network.RetireOutcomeFailed),
					HaveField("Reason", network.RetireReasonRecheckFailed),
					HaveField("Err", MatchError(ContainSubstring("fake get error"))),
				)))
				Expect(network.RetireRequeueAfter(results)).To(Equal(network.PendingRequeueAfter))
				Expect(exists(objs[1])).To(BeTrue())
			})
		})
	})

	When("the delete fails", func() {
		var failDelete bool

		BeforeEach(func() {
			failDelete = true
			next := funcs.Delete
			funcs.Delete = func(
				ctx context.Context,
				c ctrlclient.WithWatch,
				obj ctrlclient.Object,
				opts ...ctrlclient.DeleteOption) error {

				if failDelete {
					return errors.New("fake delete error")
				}
				return next(ctx, c, obj, opts...)
			}
		})

		It("records the failure, asks for a retry, and deletes on a later pass", func() {
			results := retire()
			Expect(results).To(ConsistOf(And(
				HaveField("Ref.Name", oldName),
				HaveField("Outcome", network.RetireOutcomeFailed),
				HaveField("Reason", network.RetireReasonDeleteFailed),
				HaveField("Err", MatchError(ContainSubstring("fake delete error"))),
			)))
			Expect(network.RetireRequeueAfter(results)).To(Equal(network.PendingRequeueAfter))
			Expect(exists(objs[1])).To(BeTrue())

			failDelete = false
			Expect(retire()).To(ConsistOf(HaveField("Outcome", network.RetireOutcomeDeleted)))
			Expect(exists(objs[1])).To(BeFalse())
		})
	})

	It("counts a resource that is already gone as deleted", func() {
		start()
		stale := objs[1].DeepCopyObject().(ctrlclient.Object)
		Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(stale), stale)).To(Succeed())
		Expect(client.Delete(ctx, stale)).To(Succeed())
		network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{stale})

		results := network.RetireProviderResources(ctx, network.RetireInput{
			Client: client, VM: vm, Observed: info,
		})
		Expect(results).To(ConsistOf(HaveField("Outcome", network.RetireOutcomeDeleted)))
	})

	When("a newer resource reused the name", func() {
		BeforeEach(func() {
			// The fake client does not enforce a UID precondition, so enforce
			// it the way the API server does.
			funcs.Delete = func(
				ctx context.Context,
				c ctrlclient.WithWatch,
				obj ctrlclient.Object,
				opts ...ctrlclient.DeleteOption) error {

				o := ctrlclient.DeleteOptions{}
				o.ApplyOptions(opts)
				deleteCalls = append(deleteCalls, o)
				if o.Preconditions != nil && o.Preconditions.UID != nil {
					current := obj.DeepCopyObject().(ctrlclient.Object)
					if err := c.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), current); err == nil &&
						current.GetUID() != *o.Preconditions.UID {

						return apierrors.NewConflict(schema.GroupResource{}, obj.GetName(),
							fmt.Errorf("precondition failed: UID in precondition: %s, UID in object meta: %s",
								*o.Preconditions.UID, current.GetUID()))
					}
				}
				return c.Delete(ctx, obj, opts...)
			}
		})

		It("does not delete the newer resource", func() {
			start()

			// Observe the old resource.
			stale := objs[1].DeepCopyObject().(ctrlclient.Object)
			Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(stale), stale)).To(Succeed())
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{stale})

			// Recreate it with the same name before the retirement runs.
			Expect(client.Delete(ctx, stale.DeepCopyObject().(ctrlclient.Object))).To(Succeed())
			recreated := netOP(oldName, netopv1alpha1.NetworkInterfaceStatus{})
			Expect(client.Create(ctx, recreated)).To(Succeed())
			Expect(recreated.GetUID()).ToNot(Equal(stale.GetUID()))
			deleteCalls = nil

			results := network.RetireProviderResources(ctx, network.RetireInput{
				Client: client, VM: vm, Observed: info,
			})
			Expect(results).To(ConsistOf(And(
				HaveField("Ref.UID", stale.GetUID()),
				HaveField("Outcome", network.RetireOutcomeFailed),
				HaveField("Reason", network.RetireReasonDeleteFailed),
			)))
			Expect(network.RetireRequeueAfter(results)).To(Equal(network.PendingRequeueAfter))

			Expect(deleteCalls).To(HaveLen(1))
			Expect(*deleteCalls[0].Preconditions.UID).To(Equal(stale.GetUID()))

			current := &netopv1alpha1.NetworkInterface{}
			Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(recreated), current)).To(Succeed())
			Expect(current.UID).To(Equal(recreated.GetUID()))
		})
	})
})

var _ = Describe("ListNetworkInterfaces", func() {

	var (
		testConfig builder.VCSimTestConfig
		ctx        *builder.TestContextForVCSim

		vmCtx pkgctx.VirtualMachineContext
		vm    *vmopv1.VirtualMachine

		initObjects []ctrlclient.Object
	)

	BeforeEach(func() {
		testConfig = builder.VCSimTestConfig{}

		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "list-net-if-test-vm",
				Namespace: "list-net-if-test-ns",
				UID:       types.UID(uuid.NewString()),
			},
		}
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(testConfig, initObjects...)

		vmCtx = pkgctx.VirtualMachineContext{
			Context: ctx,
			Logger:  suite.GetLogger().WithName("list_interfaces_test"),
			VM:      vm,
		}
	})

	AfterEach(func() {
		initObjects = nil
	})

	newNetworkSettings := func(provider, legacyProvider netopv1alpha1.NetworkProvider) *netopv1alpha1.NetworkSettings {
		return &netopv1alpha1.NetworkSettings{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: vm.Namespace,
			},
			Provider:       provider,
			LegacyProvider: legacyProvider,
		}
	}

	makeNetOPIf := func(name string, makeOwner bool) *netopv1alpha1.NetworkInterface {
		obj := &netopv1alpha1.NetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: vm.Namespace,
				Labels:    map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name},
			},
		}
		if makeOwner {
			Expect(ctrlutil.SetOwnerReference(vm, obj, ctx.Client.Scheme())).To(Succeed())
		}
		return obj
	}

	makeNCPIf := func(name string, makeOwner bool) *ncpv1alpha1.VirtualNetworkInterface {
		obj := &ncpv1alpha1.VirtualNetworkInterface{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: vm.Namespace,
				Labels:    map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name},
			},
		}
		if makeOwner {
			Expect(ctrlutil.SetOwnerReference(vm, obj, ctx.Client.Scheme())).To(Succeed())
		}
		return obj
	}

	makeVPCIf := func(name string, makeOwner bool) *vpcv1alpha1.SubnetPort {
		obj := &vpcv1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: vm.Namespace,
				Labels:    map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name},
			},
		}
		if makeOwner {
			Expect(ctrlutil.SetOwnerReference(vm, obj, ctx.Client.Scheme())).To(Succeed())
		}
		return obj
	}

	Context("PerNamespaceNetworkProvider", func() {

		JustBeforeEach(func() {
			pkgcfg.SetContext(ctx, func(cfg *pkgcfg.Config) {
				cfg.Features.PerNamespaceNetworkProvider = true
			})
			// Re-wrap so vmCtx picks up the updated feature flag.
			vmCtx = pkgctx.VirtualMachineContext{
				Context: ctx,
				Logger:  suite.GetLogger().WithName("list_interfaces_test"),
				VM:      vm,
			}
		})

		It("returns an error when no NetworkSettings/default exists", func() {
			_, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(netsetutil.ErrNetworkSettingsNotFound))
		})

		Context("with VDS NetworkSettings", func() {

			JustBeforeEach(func() {
				ns := newNetworkSettings(netopv1alpha1.NetworkProviderVSphereDistributed, "")
				Expect(ctx.Client.Create(ctx, ns)).To(Succeed())
			})

			It("returns empty list when no interface CRs exist", func() {
				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(BeEmpty())
			})

			It("lists owned NetOP CRs", func() {
				owned := makeNetOPIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})

			It("excludes unowned NetOP CRs", func() {
				owned := makeNetOPIf(vm.Name+"-owned", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())
				unowned := makeNetOPIf(vm.Name+"-unowned", false)
				Expect(ctx.Client.Create(ctx, unowned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})
		})

		Context("with NSXT NetworkSettings", func() {

			JustBeforeEach(func() {
				ns := newNetworkSettings(netopv1alpha1.NetworkProviderNSXTier1, "")
				Expect(ctx.Client.Create(ctx, ns)).To(Succeed())
			})

			It("returns empty list when no interface CRs exist", func() {
				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(BeEmpty())
			})

			It("lists owned NCP VirtualNetworkInterface CRs", func() {
				owned := makeNCPIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})

			It("excludes unowned NCP VirtualNetworkInterface CRs", func() {
				owned := makeNCPIf(vm.Name+"-owned", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())
				unowned := makeNCPIf(vm.Name+"-unowned", false)
				Expect(ctx.Client.Create(ctx, unowned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})

			It("does not list NetOP or VPC CRs even if they exist", func() {
				ncpOwned := makeNCPIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, ncpOwned)).To(Succeed())
				// A NetOP CR with the same VM-name label should be invisible.
				netopIf := makeNetOPIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, netopIf)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(ncpOwned.GetName()))
			})
		})

		Context("with VPC NetworkSettings", func() {

			JustBeforeEach(func() {
				ns := newNetworkSettings(netopv1alpha1.NetworkProviderVPC, "")
				Expect(ctx.Client.Create(ctx, ns)).To(Succeed())
			})

			It("returns empty list when no interface CRs exist", func() {
				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(BeEmpty())
			})

			It("lists owned SubnetPort CRs", func() {
				owned := makeVPCIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})

			It("excludes unowned SubnetPort CRs", func() {
				owned := makeVPCIf(vm.Name+"-owned", true)
				Expect(ctx.Client.Create(ctx, owned)).To(Succeed())
				unowned := makeVPCIf(vm.Name+"-unowned", false)
				Expect(ctx.Client.Create(ctx, unowned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(owned.GetName()))
			})

			It("does not list NetOP or NCP CRs even if they exist", func() {
				vpcOwned := makeVPCIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, vpcOwned)).To(Succeed())
				// A NetOP CR with the same VM-name label should be invisible.
				netopIf := makeNetOPIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, netopIf)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(vpcOwned.GetName()))
			})
		})

		Context("with VPC NetworkSettings and a legacy VDS provider", func() {

			JustBeforeEach(func() {
				ns := newNetworkSettings(netopv1alpha1.NetworkProviderVPC, netopv1alpha1.NetworkProviderVSphereDistributed)
				Expect(ctx.Client.Create(ctx, ns)).To(Succeed())
			})

			It("lists owned CRs from both the current and the legacy provider", func() {
				vpcOwned := makeVPCIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, vpcOwned)).To(Succeed())
				netopOwned := makeNetOPIf(vm.Name+"-eth1", true)
				Expect(ctx.Client.Create(ctx, netopOwned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				names := make([]string, 0, len(ifaces))
				for _, obj := range ifaces {
					names = append(names, obj.GetName())
				}
				Expect(names).To(ConsistOf(vpcOwned.GetName(), netopOwned.GetName()))
			})

			It("excludes unowned CRs from either the current or the legacy provider", func() {
				vpcOwned := makeVPCIf(vm.Name+"-owned", true)
				Expect(ctx.Client.Create(ctx, vpcOwned)).To(Succeed())
				vpcUnowned := makeVPCIf(vm.Name+"-vpc-unowned", false)
				Expect(ctx.Client.Create(ctx, vpcUnowned)).To(Succeed())
				netopOwned := makeNetOPIf(vm.Name+"-owned-legacy", true)
				Expect(ctx.Client.Create(ctx, netopOwned)).To(Succeed())
				netopUnowned := makeNetOPIf(vm.Name+"-netop-unowned", false)
				Expect(ctx.Client.Create(ctx, netopUnowned)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				names := make([]string, 0, len(ifaces))
				for _, obj := range ifaces {
					names = append(names, obj.GetName())
				}
				Expect(names).To(ConsistOf(vpcOwned.GetName(), netopOwned.GetName()))
			})

			It("does not list NCP CRs, which are neither the current nor the legacy provider", func() {
				vpcOwned := makeVPCIf(vm.Name+"-eth0", true)
				Expect(ctx.Client.Create(ctx, vpcOwned)).To(Succeed())
				ncpIf := makeNCPIf(vm.Name+"-eth1", true)
				Expect(ctx.Client.Create(ctx, ncpIf)).To(Succeed())

				ifaces, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
				Expect(err).ToNot(HaveOccurred())
				Expect(ifaces).To(HaveLen(1))
				Expect(ifaces[0].GetName()).To(Equal(vpcOwned.GetName()))
			})
		})
	})
})

var _ = Describe("ListNetworkInterfaces with external network interfaces", func() {

	var (
		ctx   *builder.TestContextForVCSim
		vmCtx pkgctx.VirtualMachineContext
		vm    *vmopv1.VirtualMachine
	)

	BeforeEach(func() {
		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "list-external-test-vm",
				Namespace: "list-external-test-ns",
				UID:       types.UID(uuid.NewString()),
			},
		}
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{
			WithNetworkConfig: []builder.VCSimNetworkConfig{{Provider: pkgcfg.NetworkProviderTypeVPC}},
		})

		pkgcfg.SetContext(ctx, func(config *pkgcfg.Config) {
			config.Features.BringYourOwnNetworkInterface = true
		})

		vmCtx = pkgctx.VirtualMachineContext{
			Context: ctx,
			Logger:  suite.GetLogger().WithName("list_external_test"),
			VM:      vm,
		}
	})

	newSubnetPort := func(name string, external, owned bool) *vpcv1alpha1.SubnetPort {
		obj := &vpcv1alpha1.SubnetPort{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   vm.Namespace,
				Labels:      map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name},
				Annotations: map[string]string{},
			},
		}
		if external {
			obj.Annotations[pkgconst.ExternalNetworkInterfaceAnnotation] = "true"
		}
		if owned {
			Expect(ctrlutil.SetOwnerReference(vm, obj, ctx.Client.Scheme())).To(Succeed())
		}
		return obj
	}

	It("never returns external SubnetPorts, regardless of owner references", func() {
		external := newSubnetPort(vm.Name+"-external", true, false)
		externalOwned := newSubnetPort(vm.Name+"-external-owned", true, true)
		managed := newSubnetPort(vm.Name+"-managed", false, true)
		for _, o := range []*vpcv1alpha1.SubnetPort{external, externalOwned, managed} {
			Expect(ctx.Client.Create(ctx, o)).To(Succeed())
		}

		objs, err := network.ListNetworkInterfaces(vmCtx, ctx.Client)
		Expect(err).ToNot(HaveOccurred())
		Expect(objs).To(HaveLen(1))
		Expect(objs[0].GetName()).To(Equal(managed.Name))
	})
})
