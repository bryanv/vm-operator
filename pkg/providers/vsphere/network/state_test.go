// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("ReconcileState", Label(testlabels.API), func() {

	Context("WithContext and FromContext", func() {
		It("returns false when no state is present", func() {
			st, ok := network.FromContext(context.Background())
			Expect(ok).To(BeFalse())
			Expect(st).To(BeNil())
		})

		It("returns false for a nil state", func() {
			ctx := network.WithContext(context.Background(), nil)
			st, ok := network.FromContext(ctx)
			Expect(ok).To(BeFalse())
			Expect(st).To(BeNil())
		})

		It("returns the same state pointer", func() {
			in := &network.ReconcileState{}
			ctx := network.WithContext(context.Background(), in)

			out, ok := network.FromContext(ctx)
			Expect(ok).To(BeTrue())
			Expect(out).To(BeIdenticalTo(in))

			// Replacing a snapshot through the pointer is visible to every
			// holder of the context.
			in.Observed = &network.Info{Complete: true}
			out, _ = network.FromContext(ctx)
			Expect(out.Observed).To(BeIdenticalTo(in.Observed))
		})
	})

	Context("PolicyFromContext", func() {
		It("returns every field false with the default config", func() {
			Expect(network.PolicyFromContext(pkgcfg.NewContextWithDefaultConfig())).
				To(Equal(network.Policy{}))
		})

		It("returns every field false even when VMNetworkHotPlug is enabled", func() {
			ctx := pkgcfg.UpdateContext(
				pkgcfg.NewContextWithDefaultConfig(),
				func(config *pkgcfg.Config) {
					config.Features.VMNetworkHotPlug = true
				})
			Expect(pkgcfg.FromContext(ctx).Features.VMNetworkHotPlug).To(BeTrue())
			Expect(network.PolicyFromContext(ctx)).To(Equal(network.Policy{}))
		})
	})

	Context("InterfaceState records", func() {
		const (
			vmName = "state-test-vm"
			vmNS   = "state-test-ns"
		)

		var (
			ctx         context.Context
			vm          *vmopv1.VirtualMachine
			initObjects []ctrlclient.Object
			funcs       interceptor.Funcs
			info        *network.Info
		)

		netOPRef := func(name string) *common.PartialObjectRef {
			return &common.PartialObjectRef{
				TypeMeta: metav1.TypeMeta{
					APIVersion: netopv1alpha1.SchemeGroupVersion.String(),
					Kind:       "Network",
				},
				Name: name,
			}
		}

		BeforeEach(func() {
			ctx = pkgcfg.UpdateContext(
				pkgcfg.NewContextWithDefaultConfig(),
				func(config *pkgcfg.Config) {
					config.NetworkProviderType = pkgcfg.NetworkProviderTypeVDS
					config.Features.VMNetworkUnitNumbers = true
				})

			vm = &vmopv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vmName,
					Namespace: vmNS,
					UID:       "state-test-uid",
				},
				Spec: vmopv1.VirtualMachineSpec{
					Network: &vmopv1.VirtualMachineNetworkSpec{
						Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
							{
								// Ready.
								Name:       "eth0",
								Network:    netOPRef("net-ready"),
								UnitNumber: ptr.To[int32](7),
							},
							{
								// Pending: the CR does not exist yet.
								Name:       "eth1",
								Network:    netOPRef("net-pending"),
								UnitNumber: ptr.To[int32](8),
								DHCP4:      ptr.To(true),
							},
							{
								// Failed: no network apiVersion.
								Name:       "eth2",
								Network:    &common.PartialObjectRef{Name: "net-failed"},
								UnitNumber: ptr.To[int32](9),
							},
							{
								// Unknown: the CR read fails.
								Name:       "eth3",
								Network:    netOPRef("net-unknown"),
								UnitNumber: ptr.To[int32](10),
							},
						},
					},
				},
			}

			readyNetIf := &netopv1alpha1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{
					Name:      network.NetOPCRName(vmName, "net-ready", "eth0", false),
					Namespace: vmNS,
				},
				Status: netopv1alpha1.NetworkInterfaceStatus{
					NetworkID: "dvportgroup-42",
					Conditions: []netopv1alpha1.NetworkInterfaceCondition{
						{
							Type:   netopv1alpha1.NetworkInterfaceReady,
							Status: "True",
						},
					},
				},
			}
			initObjects = []ctrlclient.Object{readyNetIf}

			unknownName := network.NetOPCRName(vmName, "net-unknown", "eth3", false)
			funcs = interceptor.Funcs{
				Get: func(
					ctx context.Context,
					c ctrlclient.WithWatch,
					key ctrlclient.ObjectKey,
					obj ctrlclient.Object,
					opts ...ctrlclient.GetOption) error {

					if key.Name == unknownName {
						return errors.New("fake get error")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}
		})

		JustBeforeEach(func() {
			client := builder.NewFakeClientWithInterceptors(funcs, initObjects...)
			info = network.Observe(ctx, network.ObserveInput{
				Client: client,
				VM:     vm,
				MoVM: mo.VirtualMachine{
					Config: &vimtypes.VirtualMachineConfigInfo{},
				},
			})
		})

		It("keeps one record per spec entry, in spec order, with identity and claim fields", func() {
			Expect(info.Enabled).To(BeTrue())
			Expect(info.Complete).To(BeFalse())
			Expect(info.Interfaces).To(HaveLen(4))

			for i, iface := range info.Interfaces {
				By(iface.Spec.Name)
				Expect(iface.Spec).To(Equal(vm.Spec.Network.Interfaces[i]))
				Expect(iface.Spec.UnitNumber).To(Equal(vm.Spec.Network.Interfaces[i].UnitNumber))
				Expect(iface.Match).To(Equal(network.Match{Kind: network.MatchNone}))
				Expect(iface.AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			}

			ready := info.Interfaces[0].Provider
			Expect(ready.Phase).To(Equal(network.ResolutionReady))
			Expect(ready.Ref.Type).To(Equal(pkgcfg.NetworkProviderTypeVDS))
			Expect(ready.Ref.Name).To(Equal(network.NetOPCRName(vmName, "net-ready", "eth0", false)))
			Expect(ready.Ref.UID).ToNot(BeEmpty())
			Expect(ready.Target).ToNot(BeNil())
			Expect(ready.Err).ToNot(HaveOccurred())

			pending := info.Interfaces[1].Provider
			Expect(pending.Phase).To(Equal(network.ResolutionPending))
			Expect(pending.Reason).To(Equal(network.ReasonProviderResourceNotFound))
			Expect(pending.Ref).To(Equal(network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: vmNS,
				Name:      network.NetOPCRName(vmName, "net-pending", "eth1", false),
			}))
			Expect(pending.Target).To(BeNil())
			Expect(pending.Err).ToNot(HaveOccurred())

			failed := info.Interfaces[2].Provider
			Expect(failed.Phase).To(Equal(network.ResolutionFailed))
			Expect(failed.Reason).To(Equal(network.ReasonInvalidInterfaceSpec))
			Expect(failed.Ref.Namespace).To(Equal(vmNS))
			Expect(failed.Err).To(HaveOccurred())
			Expect(failed.Target).To(BeNil())

			unknown := info.Interfaces[3].Provider
			Expect(unknown.Phase).To(Equal(network.ResolutionUnknown))
			Expect(unknown.Reason).To(Equal(network.ReasonReadFailed))
			Expect(unknown.Ref).To(Equal(network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: vmNS,
				Name:      network.NetOPCRName(vmName, "net-unknown", "eth3", false),
			}))
			Expect(unknown.Err).To(MatchError(ContainSubstring("fake get error")))
			Expect(unknown.Target).To(BeNil())
		})

		It("owns its copy of each spec", func() {
			vm.Spec.Network.Interfaces[1].Name = "changed"
			*vm.Spec.Network.Interfaces[1].DHCP4 = false
			*vm.Spec.Network.Interfaces[1].UnitNumber = 99

			spec := info.Interfaces[1].Spec
			Expect(spec.Name).To(Equal("eth1"))
			Expect(*spec.DHCP4).To(BeTrue())
			Expect(*spec.UnitNumber).To(Equal(int32(8)))
		})
	})
})
