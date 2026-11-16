// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
	"errors"
	"strings"
	"time"

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
	pkgcond "github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
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

		It("returns every field false except NICsOutOfGOSCHash when VMNetworkHotPlug is enabled", func() {
			ctx := pkgcfg.UpdateContext(
				pkgcfg.NewContextWithDefaultConfig(),
				func(config *pkgcfg.Config) {
					config.Features.VMNetworkHotPlug = true
				})
			Expect(pkgcfg.FromContext(ctx).Features.VMNetworkHotPlug).To(BeTrue())
			Expect(network.PolicyFromContext(ctx)).To(Equal(network.Policy{
				NICsOutOfGOSCHash: true,
			}))
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

var _ = Describe("ReconcileState Readiness", Label(testlabels.API), func() {

	const (
		adapterKey = int32(4000)
		adapterMAC = "00:50:56:aa:bb:cc"
	)

	var (
		vm    *vmopv1.VirtualMachine
		moVM  mo.VirtualMachine
		vmCtx pkgctx.VirtualMachineContext
		info  *network.Info
		plan  *network.Plan
		st    *network.ReconcileState
	)

	readyInterface := func(name string, key int32) network.InterfaceState {
		return network.InterfaceState{
			Spec: vmopv1.VirtualMachineNetworkInterfaceSpec{Name: name},
			Provider: network.Resolution{
				Phase:  network.ResolutionReady,
				Target: &network.Attachment{},
			},
			Match:            network.Match{Kind: network.MatchIdentity, AdapterKey: key},
			AttachmentChange: network.AttachmentChangeUnchanged,
		}
	}

	noOp := func(name string, key int32) network.Action {
		return network.Action{
			Kind:      network.ActionNoOp,
			Interface: name,
			Adapter:   key,
			Reason:    network.PlanReasonAttachmentUnchanged,
		}
	}

	BeforeEach(func() {
		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "readiness-vm",
				Namespace:  "readiness-ns",
				UID:        "readiness-uid",
				Generation: 3,
			},
			Spec: vmopv1.VirtualMachineSpec{
				Network: &vmopv1.VirtualMachineNetworkSpec{
					Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{Name: "eth0"},
					},
				},
			},
		}
		moVM = mo.VirtualMachine{
			Config: &vimtypes.VirtualMachineConfigInfo{ChangeVersion: "cv-1"},
		}
		vmCtx = pkgctx.VirtualMachineContext{
			Context: context.Background(),
			VM:      vm,
			MoVM:    moVM,
		}

		identity := network.ObservationIdentityForTest(vm, moVM)
		info = &network.Info{
			Identity:   identity,
			Enabled:    true,
			Complete:   true,
			Interfaces: []network.InterfaceState{readyInterface("eth0", adapterKey)},
			Adapters: []network.ObservedAdapter{
				{Key: adapterKey, MAC: adapterMAC},
			},
			Disposition: map[int32]network.AdapterDisposition{
				adapterKey: network.AdapterDispositionClaimed,
			},
		}
		plan = &network.Plan{
			Identity: identity,
			Actions:  []network.Action{noOp("eth0", adapterKey)},
		}
		st = &network.ReconcileState{Observed: info, Plan: plan}
	})

	It("is ready when every predicate holds", func() {
		r := st.Readiness(vmCtx)
		Expect(r.Known).To(BeTrue())
		Expect(r.ProviderReady).To(BeTrue())
		Expect(r.HardwareConverged).To(BeTrue())
		Expect(r.MACsResolved).To(BeTrue())
		Expect(r.Pending).To(BeEmpty())
		Expect(r.Ready()).To(BeTrue())
	})

	It("is not ready without a state", func() {
		var nilState *network.ReconcileState
		r := nilState.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Known).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(HaveField("Reason", network.ReadinessReasonNotEvaluated)))
	})

	It("is not ready without a plan", func() {
		st.Plan = nil
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(HaveField("Reason", network.ReadinessReasonNotEvaluated)))
	})

	It("is not ready without an observation", func() {
		st.Observed = nil
		Expect(st.Readiness(vmCtx).Ready()).To(BeFalse())
	})

	It("is not ready with an empty plan that describes no observation", func() {
		st.Plan = &network.Plan{}
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Known).To(BeFalse())
		Expect(r.Pending[0].Reason).To(Equal(network.ReadinessReasonObservationStale))
	})

	It("is not ready when the plan was built from another observation", func() {
		plan.Identity.ChangeVersion = "cv-0"
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Pending[0].Reason).To(Equal(network.ReadinessReasonObservationStale))
	})

	It("is not ready when the spec changed after the observation", func() {
		vm.Spec.Network.Interfaces[0].Name = "eth9"
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Pending[0].Reason).To(Equal(network.ReadinessReasonObservationStale))
	})

	It("is not ready when the hardware changed after the observation", func() {
		vmCtx.MoVM.Config = &vimtypes.VirtualMachineConfigInfo{ChangeVersion: "cv-2"}
		Expect(st.Readiness(vmCtx).Ready()).To(BeFalse())
	})

	It("is not ready when the observation is incomplete", func() {
		info.Complete = false
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Pending[0].Reason).To(Equal(network.ReadinessReasonObservationIncomplete))
	})

	It("is not ready once network device changes were submitted", func() {
		st.Submitted = true
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Known).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(network.PendingInterface{
			Reason:  network.ReadinessReasonHardwarePending,
			Message: "network device changes were submitted and are not observed yet",
		}))
	})

	It("is not ready for a Pending interface whose adapter is kept by a NoOp", func() {
		info.Interfaces[0].Provider = network.Resolution{
			Phase:   network.ResolutionPending,
			Message: "NetworkInterface ns/eth0 is not ready",
		}
		plan.Actions[0] = network.Action{
			Kind:      network.ActionNoOp,
			Interface: "eth0",
			Adapter:   adapterKey,
			Reason:    network.PlanReasonProviderPending,
			Message:   "NetworkInterface ns/eth0 is not ready",
		}
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Known).To(BeTrue())
		Expect(r.ProviderReady).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(network.PendingInterface{
			Name:    "eth0",
			Reason:  network.PlanReasonProviderPending,
			Message: "NetworkInterface ns/eth0 is not ready",
		}))
		Expect(r.PendingNames()).To(Equal([]string{"eth0"}))
	})

	It("is not ready for a Failed interface", func() {
		info.Interfaces[0].Provider = network.Resolution{Phase: network.ResolutionFailed}
		plan.Actions[0].Reason = network.PlanReasonProviderFailed
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.ProviderReady).To(BeFalse())
	})

	It("is not ready for a NoOp that keeps an adapter whose attachment is being replaced", func() {
		info.Interfaces[0].AttachmentChange = network.AttachmentChangeReplacement
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.ProviderReady).To(BeTrue())
		Expect(r.HardwareConverged).To(BeFalse())
	})

	It("is not ready for a deferred change", func() {
		plan.Actions[0] = network.Action{
			Kind:      network.ActionDeferred,
			Proposed:  network.ActionReplace,
			Interface: "eth0",
			Adapter:   adapterKey,
			Reason:    network.PlanReasonPowerOffRequired,
			Message:   "replace requires the VM to be powered off",
		}
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.HardwareConverged).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(network.PendingInterface{
			Name:    "eth0",
			Reason:  network.PlanReasonPowerOffRequired,
			Message: "replace requires the VM to be powered off",
		}))
	})

	It("is not ready for an interface without an action", func() {
		plan.Actions = nil
		Expect(st.Readiness(vmCtx).Ready()).To(BeFalse())
	})

	It("is not ready for an interface that claims no adapter", func() {
		info.Interfaces[0].Match = network.Match{Kind: network.MatchNone}
		Expect(st.Readiness(vmCtx).Ready()).To(BeFalse())
	})

	It("is not ready while a disconnected adapter that must be removed is still present", func() {
		const retiringKey = int32(4001)
		info.Adapters = append(info.Adapters, network.ObservedAdapter{
			Key: retiringKey,
			MAC: "00:50:56:aa:bb:dd",
			Connect: network.ConnectionState{
				Present: true,
			},
		})
		info.Disposition[retiringKey] = network.AdapterDispositionRetiring
		plan.Actions = append(plan.Actions, network.Action{
			Kind:     network.ActionDeferred,
			Proposed: network.ActionRemove,
			Adapter:  retiringKey,
			Reason:   network.PlanReasonPowerOffRequired,
			Message:  "remove requires the VM to be powered off",
		})

		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.HardwareConverged).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(network.PendingInterface{
			Adapter: retiringKey,
			Reason:  network.PlanReasonPowerOffRequired,
			Message: "remove requires the VM to be powered off",
		}))
	})

	It("is not ready for an unclaimed adapter even when the plan omits its removal", func() {
		info.Adapters = append(info.Adapters, network.ObservedAdapter{Key: 4001, MAC: "x"})
		info.Disposition[4001] = network.AdapterDispositionUnclaimed
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(HaveField("Reason", network.PlanReasonRemovalPending)))
	})

	It("is not ready while the adapter has no MAC address", func() {
		info.Adapters[0].MAC = ""
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.MACsResolved).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(HaveField("Reason", network.ReadinessReasonMACAddressPending)))
	})

	It("is not ready when the adapter's MAC address is not the pinned one", func() {
		info.Interfaces[0].Provider.Target.MAC.Pinned = "00:50:56:00:00:01"
		r := st.Readiness(vmCtx)
		Expect(r.Ready()).To(BeFalse())
		Expect(r.MACsResolved).To(BeFalse())
		Expect(r.Pending).To(ConsistOf(HaveField("Reason", network.ReadinessReasonMACMismatch)))
	})

	It("accepts a pinned MAC address that differs only in case", func() {
		info.Interfaces[0].Provider.Target.MAC.Pinned = strings.ToUpper(adapterMAC)
		Expect(st.Readiness(vmCtx).Ready()).To(BeTrue())
	})

	It("is ready for an evaluated network without interfaces and adapters", func() {
		vm.Spec.Network = nil
		identity := network.ObservationIdentityForTest(vm, moVM)
		st.Observed = &network.Info{Identity: identity, Complete: true}
		st.Plan = &network.Plan{Identity: identity}
		Expect(st.Readiness(vmCtx).Ready()).To(BeTrue())
	})

	It("orders the pending reasons", func() {
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth2"}, {Name: "eth0"}, {Name: "eth1"},
		}
		identity := network.ObservationIdentityForTest(vm, moVM)
		info.Identity, plan.Identity = identity, identity
		info.Interfaces = []network.InterfaceState{
			readyInterface("eth2", 0), readyInterface("eth0", adapterKey), readyInterface("eth1", 0),
		}
		info.Interfaces[0].Match = network.Match{Kind: network.MatchNone}
		info.Interfaces[2].Match = network.Match{Kind: network.MatchNone}
		info.Adapters = append(info.Adapters,
			network.ObservedAdapter{Key: 4002, MAC: "b"},
			network.ObservedAdapter{Key: 4001, MAC: "a"})
		info.Disposition[4001] = network.AdapterDispositionUnclaimed
		info.Disposition[4002] = network.AdapterDispositionUnclaimed
		plan.Actions = []network.Action{
			{Kind: network.ActionDeferred, Interface: "eth2", Reason: network.PlanReasonPowerOffRequired},
			noOp("eth0", adapterKey),
			{Kind: network.ActionDeferred, Interface: "eth1", Reason: network.PlanReasonPowerOffRequired},
		}
		st.Submitted = true

		r := st.Readiness(vmCtx)
		Expect(r.Pending).To(HaveLen(5))
		Expect(r.Pending[0].Reason).To(Equal(network.ReadinessReasonHardwarePending))
		Expect(r.Pending[1].Name).To(Equal("eth1"))
		Expect(r.Pending[2].Name).To(Equal("eth2"))
		Expect(r.Pending[3].Adapter).To(Equal(int32(4001)))
		Expect(r.Pending[4].Adapter).To(Equal(int32(4002)))
		Expect(r.PendingNames()).To(Equal([]string{"eth1", "eth2"}))
	})
})

var _ = Describe("SetReadyCondition", Label(testlabels.API), func() {
	var vm *vmopv1.VirtualMachine

	BeforeEach(func() {
		vm = &vmopv1.VirtualMachine{}
	})

	It("marks the condition False with the error", func() {
		network.SetReadyCondition(vm, true, nil, errors.New("boom"))
		c := pkgcond.Get(vm, vmopv1.VirtualMachineConditionNetworkReady)
		Expect(c).ToNot(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal(network.ReadyConditionNotReadyReason))
		Expect(c.Message).To(ContainSubstring("boom"))
	})

	It("deletes the condition without network interfaces", func() {
		pkgcond.MarkTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)
		network.SetReadyCondition(vm, false, []network.PendingInterface{{Reason: "X"}}, nil)
		Expect(pkgcond.Has(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeFalse())
	})

	It("marks the condition True without pending reasons", func() {
		network.SetReadyCondition(vm, true, nil, nil)
		Expect(pkgcond.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
	})

	It("uses the shared reason and lists every pending reason in order", func() {
		network.SetReadyCondition(vm, true, []network.PendingInterface{
			{Name: "eth1", Reason: network.PlanReasonProviderPending, Message: "a"},
			{Name: "eth2", Reason: network.PlanReasonProviderPending},
		}, nil)
		c := pkgcond.Get(vm, vmopv1.VirtualMachineConditionNetworkReady)
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal(network.PlanReasonProviderPending))
		Expect(c.Message).To(Equal("eth1: ProviderPending (a); eth2: ProviderPending"))
	})

	It("uses NotReady for several reasons", func() {
		network.SetReadyCondition(vm, true, []network.PendingInterface{
			{Name: "eth1", Reason: network.PlanReasonPowerOffRequired},
			{Adapter: 4001, Reason: network.PlanReasonRemovalPending},
		}, nil)
		c := pkgcond.Get(vm, vmopv1.VirtualMachineConditionNetworkReady)
		Expect(c.Reason).To(Equal(network.ReadyConditionNotReadyReason))
		Expect(c.Message).To(Equal("eth1: PowerOffRequired; adapter 4001: RemovalPending"))
	})
})

var _ = Describe("PendingRetryAfter", Label(testlabels.API), func() {
	It("is zero without an observation", func() {
		Expect(network.PendingRetryAfter(nil, nil)).To(BeZero())
	})

	DescribeTable("retries only waits without a dependable wake-up",
		func(complete bool, phase network.ResolutionPhase, action network.Action, expected time.Duration) {
			info := &network.Info{
				Complete: complete,
				Interfaces: []network.InterfaceState{{
					Spec:     vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"},
					Provider: network.Resolution{Phase: phase},
				}},
			}
			plan := &network.Plan{Actions: []network.Action{action}}
			Expect(network.PendingRetryAfter(info, plan)).To(Equal(expected))
		},
		Entry("ready and unchanged", true, network.ResolutionReady,
			network.Action{Kind: network.ActionNoOp}, time.Duration(0)),
		Entry("incomplete observation", false, network.ResolutionReady,
			network.Action{Kind: network.ActionNoOp}, network.PendingRequeueAfter),
		Entry("pending provider", true, network.ResolutionPending,
			network.Action{Kind: network.ActionDeferred}, network.PendingRequeueAfter),
		Entry("unknown provider", true, network.ResolutionUnknown,
			network.Action{Kind: network.ActionDeferred}, network.PendingRequeueAfter),
		Entry("failed provider", true, network.ResolutionFailed,
			network.Action{Kind: network.ActionDeferred}, time.Duration(0)),
		Entry("change deferred until power-off", true, network.ResolutionReady,
			network.Action{Kind: network.ActionDeferred, Reason: network.PlanReasonPowerOffRequired}, time.Duration(0)),
		Entry("backing not resolved", true, network.ResolutionReady,
			network.Action{Kind: network.ActionDeferred, Reason: network.PlanReasonBackingUnavailable}, network.PendingRequeueAfter),
	)
})
