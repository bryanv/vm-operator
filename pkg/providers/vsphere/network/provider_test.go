// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

const (
	providerTestVMName = "provider-test-vm"
	providerTestNS     = "provider-test-ns"
	providerTestLSUUID = "5c2e44a6-4e43-4f0b-8f2f-3b7c9f1d2e10"
)

var errNoWrites = errors.New("observe must not write")

// noWriteFuncs returns interceptor funcs that fail every write and record
// that one was attempted.
func noWriteFuncs(writes *[]string) interceptor.Funcs {
	var mu sync.Mutex
	record := func(op string, obj ctrlclient.Object) error {
		mu.Lock()
		defer mu.Unlock()
		*writes = append(*writes, fmt.Sprintf("%s %T %s", op, obj, obj.GetName()))
		return errNoWrites
	}

	return interceptor.Funcs{
		Create: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.CreateOption) error {
			return record("create", obj)
		},
		Update: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.UpdateOption) error {
			return record("update", obj)
		},
		Patch: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ctrlclient.Patch, _ ...ctrlclient.PatchOption) error {
			return record("patch", obj)
		},
		Delete: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.DeleteOption) error {
			return record("delete", obj)
		},
		DeleteAllOf: func(_ context.Context, _ ctrlclient.WithWatch, obj ctrlclient.Object, _ ...ctrlclient.DeleteAllOfOption) error {
			return record("deleteAllOf", obj)
		},
		SubResourceCreate: func(_ context.Context, _ ctrlclient.Client, _ string, obj ctrlclient.Object, _ ctrlclient.Object, _ ...ctrlclient.SubResourceCreateOption) error {
			return record("subResourceCreate", obj)
		},
		SubResourceUpdate: func(_ context.Context, _ ctrlclient.Client, _ string, obj ctrlclient.Object, _ ...ctrlclient.SubResourceUpdateOption) error {
			return record("subResourceUpdate", obj)
		},
		SubResourcePatch: func(_ context.Context, _ ctrlclient.Client, _ string, obj ctrlclient.Object, _ ctrlclient.Patch, _ ...ctrlclient.SubResourcePatchOption) error {
			return record("subResourcePatch", obj)
		},
	}
}

func providerTestNetworkRef(providerType pkgcfg.NetworkProviderType, name string) *common.PartialObjectRef {
	ref := &common.PartialObjectRef{Name: name}
	switch providerType {
	case pkgcfg.NetworkProviderTypeVDS:
		ref.TypeMeta = metav1.TypeMeta{
			APIVersion: netopv1alpha1.SchemeGroupVersion.String(),
			Kind:       "Network",
		}
	case pkgcfg.NetworkProviderTypeNSXT:
		ref.TypeMeta = metav1.TypeMeta{
			APIVersion: ncpv1alpha1.SchemeGroupVersion.String(),
			Kind:       "VirtualNetwork",
		}
	case pkgcfg.NetworkProviderTypeVPC:
		ref.TypeMeta = metav1.TypeMeta{
			APIVersion: vpcv1alpha1.SchemeGroupVersion.String(),
			Kind:       "SubnetSet",
		}
	case pkgcfg.NetworkProviderTypeNamed:
		// Named networks have no TypeMeta.
	}
	return ref
}

// providerTestCRName returns the name Prepare uses to create the provider
// resource for an interface.
func providerTestCRName(providerType pkgcfg.NetworkProviderType, networkName, ifaceName string) string {
	switch providerType {
	case pkgcfg.NetworkProviderTypeVDS:
		return network.NetOPCRName(providerTestVMName, networkName, ifaceName, false)
	case pkgcfg.NetworkProviderTypeNSXT:
		return network.NCPCRName(providerTestVMName, networkName, ifaceName, false)
	case pkgcfg.NetworkProviderTypeVPC:
		return network.VPCCRName(providerTestVMName, networkName, ifaceName)
	case pkgcfg.NetworkProviderTypeNamed:
		// Named networks have no CR.
	}
	return ""
}

func providerTestCR(providerType pkgcfg.NetworkProviderType, name string) ctrlclient.Object {
	meta := metav1.ObjectMeta{Name: name, Namespace: providerTestNS}
	switch providerType {
	case pkgcfg.NetworkProviderTypeVDS:
		return &netopv1alpha1.NetworkInterface{ObjectMeta: meta}
	case pkgcfg.NetworkProviderTypeNSXT:
		return &ncpv1alpha1.VirtualNetworkInterface{ObjectMeta: meta}
	case pkgcfg.NetworkProviderTypeVPC:
		return &vpcv1alpha1.SubnetPort{ObjectMeta: meta}
	case pkgcfg.NetworkProviderTypeNamed:
		// Named networks have no CR.
	}
	return nil
}

func providerTestContext(providerType pkgcfg.NetworkProviderType) context.Context {
	return pkgcfg.UpdateContext(
		pkgcfg.NewContextWithDefaultConfig(),
		func(config *pkgcfg.Config) {
			config.NetworkProviderType = providerType
		})
}

func providerTestVM() *vmopv1.VirtualMachine {
	return &vmopv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:       providerTestVMName,
			Namespace:  providerTestNS,
			UID:        "provider-test-vm-uid",
			Generation: 3,
		},
		Spec: vmopv1.VirtualMachineSpec{
			Network: &vmopv1.VirtualMachineNetworkSpec{},
		},
	}
}

func providerTestMoVM(devices ...vimtypes.BaseVirtualDevice) mo.VirtualMachine {
	return mo.VirtualMachine{
		Config: &vimtypes.VirtualMachineConfigInfo{
			ChangeVersion: "change-1",
			Hardware: vimtypes.VirtualHardware{
				Device: devices,
			},
		},
	}
}

// getCounter counts the client Get calls per object type and key.
type getCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (g *getCounter) key(obj ctrlclient.Object, key ctrlclient.ObjectKey) string {
	return fmt.Sprintf("%T %s", obj, key)
}

func (g *getCounter) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(
			ctx context.Context,
			c ctrlclient.WithWatch,
			key ctrlclient.ObjectKey,
			obj ctrlclient.Object,
			opts ...ctrlclient.GetOption) error {

			g.mu.Lock()
			g.counts[g.key(obj, key)]++
			g.mu.Unlock()
			return c.Get(ctx, key, obj, opts...)
		},
	}
}

func (g *getCounter) count(obj ctrlclient.Object, name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.counts[g.key(obj, ctrlclient.ObjectKey{Namespace: providerTestNS, Name: name})]
}

var _ = Describe("Observe", Label(testlabels.API), func() {

	var (
		ctx         context.Context
		vm          *vmopv1.VirtualMachine
		moVM        mo.VirtualMachine
		initObjects []ctrlclient.Object
		writes      []string
		funcs       interceptor.Funcs
		client      ctrlclient.Client
		info        *network.Info
	)

	BeforeEach(func() {
		ctx = providerTestContext(pkgcfg.NetworkProviderTypeVDS)
		vm = providerTestVM()
		moVM = providerTestMoVM()
		initObjects = nil
		writes = nil
		funcs = noWriteFuncs(&writes)
	})

	JustBeforeEach(func() {
		client = builder.NewFakeClientWithInterceptors(funcs, initObjects...)
		info = network.Observe(ctx, network.ObserveInput{
			Client: client,
			VM:     vm,
			MoVM:   moVM,
		})
		Expect(info).ToNot(BeNil())
	})

	DescribeTableSubtree("performs no writes",
		func(providerType pkgcfg.NetworkProviderType) {
			var (
				existingName string
				missingName  string
			)

			BeforeEach(func() {
				ctx = providerTestContext(providerType)

				vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: providerTestNetworkRef(providerType, "net-a"),
					},
					{
						Name:    "eth1",
						Network: providerTestNetworkRef(providerType, "net-b"),
					},
				}

				existingName = providerTestCRName(providerType, "net-a", "eth0")
				missingName = providerTestCRName(providerType, "net-b", "eth1")

				existing := providerTestCR(providerType, existingName)
				existing.SetLabels(map[string]string{pkgconst.NetworkInterfaceVMNameLabel: vm.Name})
				existing.SetOwnerReferences([]metav1.OwnerReference{
					{
						APIVersion: vmopv1.GroupVersion.String(),
						Kind:       "VirtualMachine",
						Name:       vm.Name,
						UID:        vm.UID,
					},
				})
				initObjects = append(initObjects, existing)
			})

			It("reads the CRs without creating, patching, or updating them", func() {
				Expect(writes).To(BeEmpty())
				Expect(info.Complete).To(BeTrue())
				Expect(info.Enabled).To(BeTrue())
				Expect(info.Interfaces).To(HaveLen(2))

				existing := info.Interfaces[0].Provider
				Expect(existing.Ref.Type).To(Equal(providerType))
				Expect(existing.Ref.Name).To(Equal(existingName))
				Expect(existing.Ref.UID).ToNot(BeEmpty())
				Expect(existing.Phase).To(Equal(network.ResolutionPending))
				Expect(existing.Reason).To(Equal(network.ReasonProviderNotReady))
				Expect(network.ResolutionObjectForTest(existing)).ToNot(BeNil())

				missing := info.Interfaces[1].Provider
				Expect(missing.Phase).To(Equal(network.ResolutionPending))
				Expect(missing.Reason).To(Equal(network.ReasonProviderResourceNotFound))
				Expect(missing.Ref).To(Equal(network.ProviderRef{
					Type:      providerType,
					Namespace: providerTestNS,
					Name:      missingName,
				}))
				Expect(network.ResolutionObjectForTest(missing)).To(BeNil())

				By("listing the VM-owned CRs", func() {
					Expect(info.Owned).To(HaveLen(1))
					Expect(info.Owned[0].Type).To(Equal(providerType))
					Expect(info.Owned[0].Name).To(Equal(existingName))
					Expect(info.Owned[0].UID).ToNot(BeEmpty())
					Expect(network.InfoOwnedObjectsForTest(info)).To(HaveLen(1))
				})

				By("not creating the missing CR", func() {
					obj := providerTestCR(providerType, missingName)
					err := client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj)
					Expect(err).To(HaveOccurred())
				})
			})
		},
		Entry("VDS", pkgcfg.NetworkProviderTypeVDS),
		Entry("NSX-T", pkgcfg.NetworkProviderTypeNSXT),
		Entry("VPC", pkgcfg.NetworkProviderTypeVPC),
	)

	Context("NetOP CR with the v1a1 name", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
			}
			initObjects = append(initObjects, providerTestCR(
				pkgcfg.NetworkProviderTypeVDS,
				network.NetOPCRName(vm.Name, "net-a", "eth0", true)))
		})

		It("prefers the existing v1a1 CR", func() {
			Expect(info.Interfaces).To(HaveLen(1))
			r := info.Interfaces[0].Provider
			Expect(r.Ref.Name).To(Equal(network.NetOPCRName(vm.Name, "net-a", "eth0", true)))
			Expect(r.Ref.UID).ToNot(BeEmpty())
		})
	})

	Context("read failures", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
				{
					Name:    "eth1",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-b"),
				},
			}

			failName := network.NetOPCRName(vm.Name, "net-a", "eth0", true)
			funcs.Get = func(
				ctx context.Context,
				c ctrlclient.WithWatch,
				key ctrlclient.ObjectKey,
				obj ctrlclient.Object,
				opts ...ctrlclient.GetOption) error {

				if key.Name == failName {
					return errors.New("fake get error")
				}
				return c.Get(ctx, key, obj, opts...)
			}
			funcs.List = func(
				_ context.Context,
				_ ctrlclient.WithWatch,
				_ ctrlclient.ObjectList,
				_ ...ctrlclient.ListOption) error {

				return errors.New("fake list error")
			}
		})

		It("records Unknown and an incomplete snapshot without failing other interfaces", func() {
			Expect(writes).To(BeEmpty())
			Expect(info.Complete).To(BeFalse())
			Expect(info.Owned).To(BeEmpty())
			Expect(info.Interfaces).To(HaveLen(2))

			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionUnknown))
			Expect(r.Reason).To(Equal(network.ReasonReadFailed))
			Expect(r.Err).To(MatchError(ContainSubstring("fake get error")))
			Expect(r.Ref.Name).To(Equal(network.NetOPCRName(vm.Name, "net-a", "eth0", false)))

			Expect(info.Interfaces[1].Provider.Phase).To(Equal(network.ResolutionPending))
		})
	})

	Context("disabled network", func() {
		BeforeEach(func() {
			vm.Spec.Network.Disabled = true
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
			}
		})

		It("is not enabled and has no interfaces", func() {
			Expect(info.Enabled).To(BeFalse())
			Expect(info.Complete).To(BeTrue())
			Expect(info.Interfaces).To(BeEmpty())
		})
	})

	Context("no network provider", func() {
		BeforeEach(func() {
			ctx = providerTestContext("")
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
			}
		})

		It("fails each interface", func() {
			Expect(info.Interfaces).To(HaveLen(1))
			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionFailed))
			Expect(r.Reason).To(Equal(network.ReasonNoNetworkProvider))
			Expect(r.Err).To(HaveOccurred())
		})
	})

	Context("named network test path", func() {
		BeforeEach(func() {
			ctx = providerTestContext(pkgcfg.NetworkProviderTypeNamed)
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &common.PartialObjectRef{Name: "VM Network"},
					MACAddr: "00:50:56:AA:BB:CC",
				},
				{
					Name:    "eth1",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
			}
		})

		It("resolves without reading any CRs or vCenter", func() {
			Expect(writes).To(BeEmpty())
			Expect(info.Owned).To(BeEmpty())
			Expect(info.Interfaces).To(HaveLen(2))

			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionReady))
			Expect(r.Ref).To(Equal(network.ProviderRef{
				Type: pkgcfg.NetworkProviderTypeNamed,
				Name: "VM Network",
			}))
			Expect(r.Target).To(Equal(&network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNetwork, ID: "VM Network"},
				ExternalID: network.FieldIntent{Op: network.IntentKeep},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			}))
			Expect(network.ResolutionBackingForTest(r)).To(BeNil())

			r = info.Interfaces[1].Provider
			Expect(r.Phase).To(Equal(network.ResolutionFailed))
			Expect(r.Reason).To(Equal(network.ReasonInvalidInterfaceSpec))
		})
	})

	Context("identity and adapters", func() {
		var (
			vmxnet3 *vimtypes.VirtualVmxnet3
			e1000e  *vimtypes.VirtualE1000e
			opaque  *vimtypes.VirtualVmxnet3
		)

		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
			}

			vmxnet3 = &vimtypes.VirtualVmxnet3{
				VirtualVmxnet: vimtypes.VirtualVmxnet{
					VirtualEthernetCard: vimtypes.VirtualEthernetCard{
						VirtualDevice: vimtypes.VirtualDevice{
							Key:        4000,
							UnitNumber: ptr.To[int32](7),
							Backing: &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
								Port: vimtypes.DistributedVirtualSwitchPortConnection{
									SwitchUuid:   "switch-uuid",
									PortgroupKey: "dvportgroup-42",
									PortKey:      "17",
								},
							},
							Connectable: &vimtypes.VirtualDeviceConnectInfo{
								Connected:         true,
								StartConnected:    true,
								AllowGuestControl: true,
							},
						},
						AddressType: string(vimtypes.VirtualEthernetCardMacTypeManual),
						MacAddress:  "00:50:56:AA:BB:CC",
						ExternalId:  "ext-1",
					},
				},
			}
			e1000e = &vimtypes.VirtualE1000e{
				VirtualEthernetCard: vimtypes.VirtualEthernetCard{
					VirtualDevice: vimtypes.VirtualDevice{
						Key: 4001,
						Backing: &vimtypes.VirtualEthernetCardNetworkBackingInfo{
							VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
								DeviceName: "VM Network",
							},
							Network: &vimtypes.ManagedObjectReference{
								Type:  "Network",
								Value: "network-7",
							},
						},
					},
					AddressType: string(vimtypes.VirtualEthernetCardMacTypeGenerated),
					MacAddress:  "00:50:56:00:00:01",
				},
			}
			opaque = &vimtypes.VirtualVmxnet3{
				VirtualVmxnet: vimtypes.VirtualVmxnet{
					VirtualEthernetCard: vimtypes.VirtualEthernetCard{
						VirtualDevice: vimtypes.VirtualDevice{
							Key: 4002,
							Backing: &vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo{
								OpaqueNetworkId:   providerTestLSUUID,
								OpaqueNetworkType: "nsx.LogicalSwitch",
							},
						},
						AddressType: string(vimtypes.VirtualEthernetCardMacTypeAssigned),
						MacAddress:  "00:50:56:00:00:02",
					},
				},
			}

			moVM = providerTestMoVM(
				&vimtypes.VirtualDisk{VirtualDevice: vimtypes.VirtualDevice{Key: 2000}},
				vmxnet3, e1000e, opaque)
		})

		It("records the identity of its inputs", func() {
			Expect(info.Identity.VMUID).To(Equal(vm.UID))
			Expect(info.Identity.Generation).To(Equal(int64(3)))
			Expect(info.Identity.ChangeVersion).To(Equal("change-1"))
			Expect(info.Identity.NetworkHash).ToNot(BeEmpty())

			again := network.Observe(ctx, network.ObserveInput{Client: client, VM: vm, MoVM: moVM})
			Expect(again.Identity).To(Equal(info.Identity))

			vm.Spec.Network.Interfaces[0].Name = "eth9"
			changed := network.Observe(ctx, network.ObserveInput{Client: client, VM: vm, MoVM: moVM})
			Expect(changed.Identity.NetworkHash).ToNot(Equal(info.Identity.NetworkHash))
		})

		It("normalizes the ethernet cards in device order", func() {
			Expect(info.Complete).To(BeTrue())
			Expect(info.Adapters).To(HaveLen(3))

			a := info.Adapters[0]
			Expect(a.Key).To(Equal(int32(4000)))
			Expect(a.UnitNumber).To(Equal(ptr.To[int32](7)))
			Expect(a.Type).To(Equal("VirtualVmxnet3"))
			Expect(a.MAC).To(Equal("00:50:56:aa:bb:cc"))
			Expect(a.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
			Expect(a.Attachment).To(Equal(network.Attachment{
				Backing: network.BackingID{
					Kind:       network.BackingKindDVPortgroup,
					ID:         "dvportgroup-42",
					SwitchUUID: "switch-uuid",
				},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "ext-1"},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			}))
			Expect(a.Connect).To(Equal(network.ConnectionState{
				Present:           true,
				Connected:         true,
				StartConnected:    true,
				AllowGuestControl: true,
			}))
			Expect(network.ObservedAdapterDeviceForTest(a)).To(BeIdenticalTo(vmxnet3))

			By("copying the unit number rather than aliasing it", func() {
				*vmxnet3.UnitNumber = 99
				Expect(*a.UnitNumber).To(Equal(int32(7)))
			})

			a = info.Adapters[1]
			Expect(a.Key).To(Equal(int32(4001)))
			Expect(a.UnitNumber).To(BeNil())
			Expect(a.Type).To(Equal("VirtualE1000e"))
			Expect(a.Attachment).To(Equal(network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNetwork, ID: "network-7"},
				ExternalID: network.FieldIntent{Op: network.IntentClear},
			}))
			Expect(a.Connect).To(Equal(network.ConnectionState{}))

			a = info.Adapters[2]
			Expect(a.Key).To(Equal(int32(4002)))
			Expect(a.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeAssigned)))
			Expect(a.Attachment).To(Equal(network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindOpaque, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentClear},
			}))
		})
	})

	Context("VM hardware config is unavailable", func() {
		BeforeEach(func() {
			moVM = mo.VirtualMachine{}
		})

		It("is incomplete", func() {
			Expect(info.Complete).To(BeFalse())
			Expect(info.Adapters).To(BeEmpty())
		})
	})
})

// providerStatusCase is one provider CR status shape.
type providerStatusCase struct {
	providerType pkgcfg.NetworkProviderType
	macAddr      string
	setStatus    func(obj ctrlclient.Object)

	phase  network.ResolutionPhase
	reason string
	target *network.Attachment
}

var _ = Describe("Provider status shapes", Label(testlabels.API), func() {

	const (
		networkName = "my-net"
		ifaceName   = "eth0"
	)

	readyNetOP := func(networkID, mac, externalID string) func(ctrlclient.Object) {
		return func(obj ctrlclient.Object) {
			netIf := obj.(*netopv1alpha1.NetworkInterface)
			netIf.Status.NetworkID = networkID
			netIf.Status.MacAddress = mac
			netIf.Status.ExternalID = externalID
			netIf.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
				{Type: netopv1alpha1.NetworkInterfaceReady, Status: corev1.ConditionTrue},
			}
		}
	}

	readyNCP := func(lsID, mac, interfaceID string) func(ctrlclient.Object) {
		return func(obj ctrlclient.Object) {
			vnetIf := obj.(*ncpv1alpha1.VirtualNetworkInterface)
			vnetIf.Status.MacAddress = mac
			vnetIf.Status.InterfaceID = interfaceID
			vnetIf.Status.ProviderStatus = &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
				NsxLogicalSwitchID: lsID,
			}
			vnetIf.Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
				{Type: "Ready", Status: "True"},
			}
		}
	}

	readyVPC := func(lsID, mac, attachmentID string) func(ctrlclient.Object) {
		return func(obj ctrlclient.Object) {
			sp := obj.(*vpcv1alpha1.SubnetPort)
			sp.Status.NetworkInterfaceConfig.LogicalSwitchUUID = lsID
			sp.Status.NetworkInterfaceConfig.MACAddress = mac
			sp.Status.Attachment.ID = attachmentID
			sp.Status.Conditions = []vpcv1alpha1.Condition{
				{Type: vpcv1alpha1.Ready, Status: corev1.ConditionTrue},
			}
		}
	}

	DescribeTable("Observe and Prepare decode the status like CreateNetworkDevices",
		func(tc providerStatusCase) {
			ctx := pkgcfg.UpdateContext(
				pkgcfg.NewContextWithDefaultConfig(),
				func(config *pkgcfg.Config) {
					config.NetworkProviderType = tc.providerType
				})

			newVM := func() *vmopv1.VirtualMachine {
				vm := providerTestVM()
				vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    ifaceName,
						Network: providerTestNetworkRef(tc.providerType, networkName),
						MACAddr: tc.macAddr,
					},
				}
				return vm
			}

			newCR := func() ctrlclient.Object {
				obj := providerTestCR(tc.providerType,
					providerTestCRName(tc.providerType, networkName, ifaceName))
				if tc.setStatus != nil {
					tc.setStatus(obj)
				}
				return obj
			}

			// Today's code is the parity oracle.
			oracleVM := newVM()
			oracleClient := builder.NewFakeClient(newCR())
			devices, oracleErr := network.CreateNetworkDevices(ctx, oracleVM, oracleClient, nil, nil)

			var writes []string
			observed := network.Observe(ctx, network.ObserveInput{
				Client: builder.NewFakeClientWithInterceptors(noWriteFuncs(&writes), newCR()),
				VM:     newVM(),
				MoVM:   providerTestMoVM(),
			})
			Expect(writes).To(BeEmpty())

			prepared, _ := network.Prepare(ctx, network.PrepareInput{
				Client: builder.NewFakeClient(newCR()),
				VM:     newVM(),
				MoVM:   providerTestMoVM(),
			})

			for _, info := range []*network.Info{observed, prepared} {
				Expect(info.Interfaces).To(HaveLen(1))
				r := info.Interfaces[0].Provider
				Expect(r.Phase).To(Equal(tc.phase))
				Expect(r.Reason).To(Equal(tc.reason))
				Expect(r.Ref.Type).To(Equal(tc.providerType))
				Expect(r.Ref.Name).To(Equal(providerTestCRName(tc.providerType, networkName, ifaceName)))
				Expect(r.Ref.UID).ToNot(BeEmpty())
				Expect(r.Target).To(Equal(tc.target))
				Expect(network.ResolutionObjectForTest(r)).ToNot(BeNil())

				switch r.Phase {
				case network.ResolutionReady, network.ResolutionPending:
					Expect(r.Err).ToNot(HaveOccurred())
				default:
					Expect(r.Err).To(HaveOccurred())
				}
			}

			if tc.phase != network.ResolutionReady {
				Expect(oracleErr).To(HaveOccurred())
				Expect(network.ResolutionBackingForTest(prepared.Interfaces[0].Provider)).To(BeNil())
				return
			}

			Expect(oracleErr).ToNot(HaveOccurred())
			Expect(devices).To(HaveLen(1))
			dev := devices[0]

			r := prepared.Interfaces[0].Provider
			Expect(r.Target.Backing.ID).To(Equal(dev.NetworkID))
			Expect(r.Target.MAC.Pinned).To(Equal(dev.MacAddress))
			Expect(r.Target.ExternalID.Value).To(Equal(dev.ExternalID))
			Expect(r.Ref.Type).To(Equal(dev.ProviderType))
			Expect(network.ResolutionBackingForTest(r)).To(Equal(dev.Backing))

			Expect(network.ResolutionBackingForTest(observed.Interfaces[0].Provider)).To(BeNil())
		},

		// NetOP.
		Entry("NetOP ready on a DVPG", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus:    readyNetOP("dvportgroup-42", "00:50:56:AA:BB:CC", "ext-1"),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: "dvportgroup-42"},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "ext-1"},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			},
		}),
		Entry("NetOP ready without an external ID or MAC", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus:    readyNetOP("dvportgroup-42", "", ""),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: "dvportgroup-42"},
				ExternalID: network.FieldIntent{Op: network.IntentKeep},
			},
		}),
		Entry("NetOP ready with a spec MAC overriding the status MAC", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			macAddr:      "00:50:56:11:22:AA",
			setStatus:    readyNetOP("dvportgroup-42", "00:50:56:AA:BB:CC", ""),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: "dvportgroup-42"},
				ExternalID: network.FieldIntent{Op: network.IntentKeep},
				MAC:        network.MACPolicy{Pinned: "00:50:56:11:22:aa"},
			},
		}),
		Entry("NetOP ready on a standard network without PerNamespaceNetworkProvider", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus:    readyNetOP("network-7", "", ""),
			phase:        network.ResolutionFailed,
			reason:       network.ReasonBackingNotSupported,
		}),
		Entry("NetOP ready on an unsupported network ID", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus:    readyNetOP(providerTestLSUUID, "", ""),
			phase:        network.ResolutionFailed,
			reason:       network.ReasonBackingNotSupported,
		}),
		Entry("NetOP with no conditions", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			phase:        network.ResolutionPending,
			reason:       network.ReasonProviderNotReady,
		}),
		Entry("NetOP not ready with a reason and message", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus: func(obj ctrlclient.Object) {
				obj.(*netopv1alpha1.NetworkInterface).Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
					{
						Type:    netopv1alpha1.NetworkInterfaceReady,
						Status:  corev1.ConditionFalse,
						Reason:  "IPAllocating",
						Message: "allocating an IP",
					},
				}
			},
			phase:  network.ResolutionPending,
			reason: network.ReasonProviderNotReady,
		}),
		Entry("NetOP failure", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVDS,
			setStatus: func(obj ctrlclient.Object) {
				obj.(*netopv1alpha1.NetworkInterface).Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
					{
						Type:    netopv1alpha1.NetworkInterfaceFailure,
						Status:  corev1.ConditionTrue,
						Reason:  "IPPoolExhausted",
						Message: "no IPs left",
					},
				}
			},
			phase:  network.ResolutionFailed,
			reason: network.ReasonProviderFailed,
		}),

		// NCP.
		Entry("NCP ready", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			setStatus:    readyNCP(providerTestLSUUID, "00:50:56:AA:BB:CC", "iface-1"),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "iface-1"},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			},
		}),
		Entry("NCP ready ignores a spec MAC", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			macAddr:      "00:50:56:11:22:33",
			setStatus:    readyNCP(providerTestLSUUID, "00:50:56:AA:BB:CC", "iface-1"),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "iface-1"},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			},
		}),
		Entry("NCP ready matches a condition type containing Ready", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			setStatus: func(obj ctrlclient.Object) {
				readyNCP(providerTestLSUUID, "", "iface-1")(obj)
				obj.(*ncpv1alpha1.VirtualNetworkInterface).Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
					{Type: "Ready", Status: "False", Reason: "Old", Message: "old"},
					{Type: "PortReady", Status: "True"},
				}
			},
			phase: network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "iface-1"},
			},
		}),
		Entry("NCP ready without provider status", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			setStatus: func(obj ctrlclient.Object) {
				readyNCP(providerTestLSUUID, "", "iface-1")(obj)
				obj.(*ncpv1alpha1.VirtualNetworkInterface).Status.ProviderStatus = nil
			},
			phase:  network.ResolutionPending,
			reason: network.ReasonProviderStatusIncomplete,
		}),
		Entry("NCP not ready", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			setStatus: func(obj ctrlclient.Object) {
				obj.(*ncpv1alpha1.VirtualNetworkInterface).Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
					{Type: "Ready", Status: "False", Reason: "Pending", Message: "waiting"},
				}
			},
			phase:  network.ResolutionPending,
			reason: network.ReasonProviderNotReady,
		}),
		Entry("NCP with an invalid logical switch ID", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeNSXT,
			setStatus:    readyNCP("dvportgroup-42", "", "iface-1"),
			phase:        network.ResolutionFailed,
			reason:       network.ReasonBackingNotSupported,
		}),

		// VPC.
		Entry("VPC ready", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVPC,
			setStatus:    readyVPC(providerTestLSUUID, "00:50:56:AA:BB:CC", "attach-1"),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "attach-1"},
				MAC:        network.MACPolicy{Pinned: "00:50:56:aa:bb:cc"},
			},
		}),
		Entry("VPC ready ignores an all zeros MAC", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVPC,
			setStatus:    readyVPC(providerTestLSUUID, "00:00:00:00:00:00", "attach-1"),
			phase:        network.ResolutionReady,
			target: &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: providerTestLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: "attach-1"},
			},
		}),
		Entry("VPC ready without a logical switch ID", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVPC,
			setStatus:    readyVPC("", "", "attach-1"),
			phase:        network.ResolutionPending,
			reason:       network.ReasonProviderStatusIncomplete,
		}),
		Entry("VPC with no Ready condition", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVPC,
			phase:        network.ResolutionPending,
			reason:       network.ReasonProviderNotReady,
		}),
		Entry("VPC not ready", providerStatusCase{
			providerType: pkgcfg.NetworkProviderTypeVPC,
			setStatus: func(obj ctrlclient.Object) {
				obj.(*vpcv1alpha1.SubnetPort).Status.Conditions = []vpcv1alpha1.Condition{
					{Type: vpcv1alpha1.Ready, Status: corev1.ConditionFalse, Reason: "Pending", Message: "waiting"},
				}
			},
			phase:  network.ResolutionPending,
			reason: network.ReasonProviderNotReady,
		}),
	)

	It("includes the provider reason and message on a Pending resolution", func() {
		ctx := providerTestContext(pkgcfg.NetworkProviderTypeVPC)
		vm := providerTestVM()
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: ifaceName, Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVPC, networkName)},
		}
		sp := providerTestCR(pkgcfg.NetworkProviderTypeVPC,
			providerTestCRName(pkgcfg.NetworkProviderTypeVPC, networkName, ifaceName)).(*vpcv1alpha1.SubnetPort)
		sp.Status.Conditions = []vpcv1alpha1.Condition{
			{Type: vpcv1alpha1.Ready, Status: corev1.ConditionFalse, Reason: "Allocating", Message: "allocating a port"},
		}

		info := network.Observe(ctx, network.ObserveInput{
			Client: builder.NewFakeClient(sp),
			VM:     vm,
			MoVM:   providerTestMoVM(),
		})
		Expect(info.Interfaces).To(HaveLen(1))
		msg := info.Interfaces[0].Provider.Message
		Expect(msg).To(ContainSubstring(sp.Name))
		Expect(msg).To(ContainSubstring("Allocating"))
		Expect(msg).To(ContainSubstring("allocating a port"))
	})
})

var _ = Describe("Prepare", Label(testlabels.API), func() {

	var (
		ctx         context.Context
		vm          *vmopv1.VirtualMachine
		initObjects []ctrlclient.Object
		funcs       interceptor.Funcs
		client      ctrlclient.Client
		info        *network.Info
		err         error
	)

	BeforeEach(func() {
		ctx = providerTestContext(pkgcfg.NetworkProviderTypeVDS)
		vm = providerTestVM()
		initObjects = nil
		funcs = interceptor.Funcs{}
	})

	JustBeforeEach(func() {
		client = builder.NewFakeClientWithInterceptors(funcs, initObjects...)
		info, err = network.Prepare(ctx, network.PrepareInput{
			Client: client,
			VM:     vm,
			MoVM:   providerTestMoVM(),
		})
		Expect(info).ToNot(BeNil())
	})

	DescribeTableSubtree("creates missing CRs",
		func(providerType pkgcfg.NetworkProviderType) {
			BeforeEach(func() {
				ctx = providerTestContext(providerType)
				vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: providerTestNetworkRef(providerType, "net-a"),
					},
				}
			})

			It("creates the CR, owned by and labeled for the VM, and reports it Pending", func() {
				Expect(err).ToNot(HaveOccurred())
				Expect(info.Complete).To(BeTrue())
				Expect(info.Interfaces).To(HaveLen(1))

				name := providerTestCRName(providerType, "net-a", "eth0")
				obj := providerTestCR(providerType, name)
				Expect(client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj)).To(Succeed())
				Expect(obj.GetLabels()).To(HaveKeyWithValue(pkgconst.NetworkInterfaceVMNameLabel, vm.Name))
				Expect(obj.GetLabels()).To(HaveKeyWithValue(network.VMInterfaceNameLabel, "eth0"))
				Expect(obj.GetOwnerReferences()).To(HaveLen(1))
				Expect(obj.GetOwnerReferences()[0].UID).To(Equal(vm.UID))

				r := info.Interfaces[0].Provider
				Expect(r.Phase).To(Equal(network.ResolutionPending))
				Expect(r.Reason).To(Equal(network.ReasonProviderNotReady))
				Expect(r.Ref).To(Equal(network.ProviderRef{
					Type:      providerType,
					Namespace: providerTestNS,
					Name:      name,
					UID:       obj.GetUID(),
				}))
				Expect(info.Interfaces[0].Match).To(Equal(network.Match{Kind: network.MatchNone}))
				Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))

				By("listing the created CR as owned", func() {
					Expect(info.Owned).To(ConsistOf(r.Ref))
				})
			})
		},
		Entry("VDS", pkgcfg.NetworkProviderTypeVDS),
		Entry("NSX-T", pkgcfg.NetworkProviderTypeNSXT),
		Entry("VPC", pkgcfg.NetworkProviderTypeVPC),
	)

	Context("per-interface failures", func() {
		var failName string

		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					// Ready.
					Name:    "eth0",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
				},
				{
					// Missing, so created and Pending.
					Name:    "eth1",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-b"),
				},
				{
					// The create fails.
					Name:    "eth2",
					Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-c"),
				},
				{
					// No network apiVersion.
					Name:    "eth3",
					Network: &common.PartialObjectRef{Name: "net-d"},
				},
			}

			ready := providerTestCR(pkgcfg.NetworkProviderTypeVDS,
				providerTestCRName(pkgcfg.NetworkProviderTypeVDS, "net-a", "eth0")).(*netopv1alpha1.NetworkInterface)
			ready.Status.NetworkID = "dvportgroup-42"
			ready.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
				{Type: netopv1alpha1.NetworkInterfaceReady, Status: corev1.ConditionTrue},
			}
			initObjects = append(initObjects, ready)

			failName = providerTestCRName(pkgcfg.NetworkProviderTypeVDS, "net-c", "eth2")
			funcs.Create = func(
				ctx context.Context,
				c ctrlclient.WithWatch,
				obj ctrlclient.Object,
				opts ...ctrlclient.CreateOption) error {

				if obj.GetName() == failName {
					return errors.New("fake create error")
				}
				return c.Create(ctx, obj, opts...)
			}
		})

		It("keeps each outcome on its own record", func() {
			Expect(info.Complete).To(BeFalse())
			Expect(info.Interfaces).To(HaveLen(4))

			names := make([]string, 0, len(info.Interfaces))
			for _, iface := range info.Interfaces {
				names = append(names, iface.Spec.Name)
				Expect(iface.Match).To(Equal(network.Match{Kind: network.MatchNone}))
				Expect(iface.AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			}
			Expect(names).To(Equal([]string{"eth0", "eth1", "eth2", "eth3"}))

			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionReady))
			Expect(r.Target.Backing).To(Equal(network.BackingID{
				Kind: network.BackingKindDVPortgroup,
				ID:   "dvportgroup-42",
			}))
			Expect(network.ResolutionBackingForTest(r)).ToNot(BeNil())
			Expect(network.ResolutionBackingForTest(r).Reference().Value).To(Equal("dvportgroup-42"))

			r = info.Interfaces[1].Provider
			Expect(r.Phase).To(Equal(network.ResolutionPending))
			Expect(r.Ref.UID).ToNot(BeEmpty())

			r = info.Interfaces[2].Provider
			Expect(r.Phase).To(Equal(network.ResolutionUnknown))
			Expect(r.Reason).To(Equal(network.ReasonEnsureFailed))
			Expect(r.Ref.Name).To(Equal(failName))
			Expect(r.Ref.UID).To(BeEmpty())
			Expect(r.Err).To(MatchError(ContainSubstring("fake create error")))

			r = info.Interfaces[3].Provider
			Expect(r.Phase).To(Equal(network.ResolutionFailed))
			Expect(r.Reason).To(Equal(network.ReasonInvalidInterfaceSpec))
			Expect(r.Err).To(HaveOccurred())

			By("returning only the I/O failures", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("fake create error"))
				Expect(err.Error()).To(ContainSubstring(`"eth2"`))
				Expect(err.Error()).ToNot(ContainSubstring(`"eth3"`))
			})
		})
	})

	Context("VPC network kind is not supported", func() {
		BeforeEach(func() {
			ctx = providerTestContext(pkgcfg.NetworkProviderTypeVPC)
			ref := providerTestNetworkRef(pkgcfg.NetworkProviderTypeVPC, "net-a")
			ref.Kind = "Bogus"
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{Name: "eth0", Network: ref},
			}
		})

		It("fails the interface without an I/O error", func() {
			Expect(err).ToNot(HaveOccurred())
			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionFailed))
			Expect(r.Reason).To(Equal(network.ReasonInvalidInterfaceSpec))
			Expect(r.Ref.Name).To(Equal(providerTestCRName(pkgcfg.NetworkProviderTypeVPC, "net-a", "eth0")))
		})
	})

	DescribeTableSubtree("does not refetch the created or patched CR",
		func(providerType pkgcfg.NetworkProviderType, v1a1Name string, ready func(obj ctrlclient.Object)) {
			var (
				counter getCounter
				name    string
			)

			BeforeEach(func() {
				ctx = providerTestContext(providerType)
				vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: providerTestNetworkRef(providerType, "net-a"),
					},
				}

				name = providerTestCRName(providerType, "net-a", "eth0")
				obj := providerTestCR(providerType, name)
				ready(obj)
				initObjects = append(initObjects, obj)

				counter = getCounter{counts: map[string]int{}}
				funcs = counter.funcs()
			})

			It("gets the CR only inside the create-or-patch helper", func() {
				Expect(err).ToNot(HaveOccurred())
				Expect(info.Interfaces[0].Provider.Phase).To(Equal(network.ResolutionReady))

				obj := providerTestCR(providerType, name)
				// The create-or-patch helper does one Get on the CR's key.
				Expect(counter.count(obj, name)).To(Equal(1))
				if v1a1Name != "" {
					// The helper probes the v1a1 name once.
					Expect(counter.count(obj, v1a1Name)).To(Equal(1))
				}
			})
		},
		Entry("VDS", pkgcfg.NetworkProviderTypeVDS,
			network.NetOPCRName(providerTestVMName, "net-a", "eth0", true),
			func(obj ctrlclient.Object) {
				netIf := obj.(*netopv1alpha1.NetworkInterface)
				netIf.Status.NetworkID = "dvportgroup-42"
				netIf.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
					{Type: netopv1alpha1.NetworkInterfaceReady, Status: corev1.ConditionTrue},
				}
			}),
		Entry("NSX-T", pkgcfg.NetworkProviderTypeNSXT,
			network.NCPCRName(providerTestVMName, "net-a", "eth0", true),
			func(obj ctrlclient.Object) {
				vnetIf := obj.(*ncpv1alpha1.VirtualNetworkInterface)
				vnetIf.Status.ProviderStatus = &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
					NsxLogicalSwitchID: providerTestLSUUID,
				}
				vnetIf.Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
					{Type: "Ready", Status: "True"},
				}
			}),
		Entry("VPC", pkgcfg.NetworkProviderTypeVPC, "",
			func(obj ctrlclient.Object) {
				sp := obj.(*vpcv1alpha1.SubnetPort)
				sp.Status.NetworkInterfaceConfig.LogicalSwitchUUID = providerTestLSUUID
				sp.Status.Conditions = []vpcv1alpha1.Condition{
					{Type: vpcv1alpha1.Ready, Status: corev1.ConditionTrue},
				}
			}),
	)
})

var _ = Describe("Prepare named network", Label(testlabels.VCSim), func() {

	var (
		ctx  *builder.TestContextForVCSim
		vm   *vmopv1.VirtualMachine
		info *network.Info
		err  error
	)

	BeforeEach(func() {
		vm = providerTestVM()
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{
			WithNetworkEnv: builder.NetworkEnvNamed,
		})

		info, err = network.Prepare(ctx, network.PrepareInput{
			Client:    ctx.Client,
			VM:        vm,
			MoVM:      providerTestMoVM(),
			VimClient: ctx.VCClient.Client,
			Finder:    ctx.Finder,
		})
		Expect(info).ToNot(BeNil())
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	Context("network exists", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &common.PartialObjectRef{Name: "DC0_DVPG0"},
				},
			}
		})

		It("resolves the backing like CreateNetworkDevices", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Complete).To(BeTrue())
			Expect(info.Owned).To(BeEmpty())

			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionReady))
			Expect(r.Ref.Type).To(Equal(pkgcfg.NetworkProviderTypeNamed))
			Expect(r.Ref.Name).To(Equal("DC0_DVPG0"))

			devices, oracleErr := network.CreateNetworkDevices(ctx, vm, ctx.Client, ctx.VCClient.Client, ctx.Finder)
			Expect(oracleErr).ToNot(HaveOccurred())
			Expect(devices).To(HaveLen(1))

			backing := network.ResolutionBackingForTest(r)
			Expect(backing).ToNot(BeNil())
			Expect(backing.Reference()).To(Equal(devices[0].Backing.Reference()))

			// Spec 017 T020: a named distributed portgroup's target is its
			// portgroup key, which is what its adapter's distributed port
			// backing records, so the adapter matches its interface.
			Expect(r.Target.Backing).To(Equal(network.BackingID{
				Kind: network.BackingKindDVPortgroup,
				ID:   devices[0].Backing.Reference().Value,
			}))
		})
	})

	Context("network does not exist", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &common.PartialObjectRef{Name: "does-not-exist"},
				},
			}
		})

		It("records Unknown with the lookup error", func() {
			Expect(err).To(HaveOccurred())
			Expect(info.Complete).To(BeFalse())

			r := info.Interfaces[0].Provider
			Expect(r.Phase).To(Equal(network.ResolutionUnknown))
			Expect(r.Reason).To(Equal(network.ReasonBackingResolutionFailed))
			Expect(r.Ref.Name).To(Equal("does-not-exist"))
			Expect(strings.Contains(r.Err.Error(), "does-not-exist")).To(BeTrue())
		})
	})
})
