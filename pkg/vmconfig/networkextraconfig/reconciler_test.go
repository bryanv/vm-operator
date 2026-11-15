// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/pkg/vmconfig/networkextraconfig"
)

var errFake = errors.New("fake error")

func newVMXNet3(key int32) vimtypes.BaseVirtualDevice {
	d := &vimtypes.VirtualVmxnet3{}
	d.Key = key
	return d
}

// collectEditChanges returns all Edit DeviceChange entries from cs.
func collectEditChanges(cs *vimtypes.VirtualMachineConfigSpec) []*vimtypes.VirtualDeviceConfigSpec {
	var out []*vimtypes.VirtualDeviceConfigSpec
	for _, dc := range cs.DeviceChange {
		if spec, ok := dc.(*vimtypes.VirtualDeviceConfigSpec); ok {
			if spec.Operation == vimtypes.VirtualDeviceConfigSpecOperationEdit {
				out = append(out, spec)
			}
		}
	}
	return out
}

func findCondition(vm *vmopv1.VirtualMachine) *metav1.Condition {
	for i := range vm.Status.Conditions {
		if vm.Status.Conditions[i].Type == vmopv1.VirtualMachineNetworkConfigSynced {
			c := vm.Status.Conditions[i]
			return &c
		}
	}
	return nil
}

var _ = Describe("networkextraconfig.Reconcile", func() {
	var (
		ctx        context.Context
		vm         *vmopv1.VirtualMachine
		moVM       mo.VirtualMachine
		configSpec *vimtypes.VirtualMachineConfigSpec
		r          = networkextraconfig.New()
	)

	BeforeEach(func() {
		ctx = pkgcfg.NewContext()
		ctx = pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
			cfg.Features.VMServiceClasslessVM = true
		})

		vm = &vmopv1.VirtualMachine{}
		vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}

		moVM = mo.VirtualMachine{}
		moVM.Config = &vimtypes.VirtualMachineConfigInfo{
			Version: "vmx-21",
			Hardware: vimtypes.VirtualHardware{
				Device: []vimtypes.BaseVirtualDevice{
					newVMXNet3(4000),
				},
			},
		}

		configSpec = &vimtypes.VirtualMachineConfigSpec{}
	})

	Context("when VMServiceClasslessVM is disabled", func() {
		It("returns nil and makes no changes", func() {
			ctx2 := pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
				cfg.Features.VMServiceClasslessVM = false
			})
			Expect(r.Reconcile(ctx2, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(configSpec.ExtraConfig).To(BeEmpty())
		})
	})

	Context("when moVM.Config is nil", func() {
		It("returns nil and makes no changes", func() {
			moVM.Config = nil
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(configSpec.ExtraConfig).To(BeEmpty())
		})
	})

	Context("when no network interfaces are configured", func() {
		It("returns nil and makes no changes", func() {
			vm.Spec.Network = nil
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(configSpec.ExtraConfig).To(BeEmpty())
		})
	})

	Context("with one VMXNet3 interface", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{Name: "eth0"},
			}
		})

		It("emits no ExtraConfig when spec has no VMXNet3 or advancedProperties", func() {
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(configSpec.ExtraConfig).To(BeEmpty())
		})

		It("translates VMXNet3 first-class fields to ExtraConfig when present", func() {
			vm.Spec.Network.Interfaces[0].VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
				RSSOffloadEnabled: ptr.To(true),
			}
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			found := false
			for _, ec := range configSpec.ExtraConfig {
				kv := ec.GetOptionValue()
				if kv.Key == "ethernet0.rssoffload" {
					Expect(kv.Value).To(Equal("TRUE"))
					found = true
				}
			}
			Expect(found).To(BeTrue(), "expected ethernet0.rssoffload in ExtraConfig")
		})

		It("writes advancedProperties bag keys with the device prefix", func() {
			vm.Spec.Network.Interfaces[0].AdvancedProperties = []vmopv1common.KeyValuePair{
				{Key: "latencySensitivity.level", Value: "high"},
			}
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			found := false
			for _, ec := range configSpec.ExtraConfig {
				kv := ec.GetOptionValue()
				if kv.Key == "ethernet0.latencySensitivity.level" {
					Expect(kv.Value).To(Equal("high"))
					found = true
				}
			}
			Expect(found).To(BeTrue(), "expected bag key in ExtraConfig")
		})
	})

	Context("when both UPTv2Enabled and VNUMANodeID differ for the same NIC", func() {
		BeforeEach(func() {
			// EFI firmware and vNUMA topology are prerequisites for VNUMANodeID.
			vm.Spec.BootOptions = &vmopv1.VirtualMachineBootOptions{
				Firmware: vmopv1.VirtualMachineBootOptionsFirmwareTypeEFI,
			}
			vm.Spec.CPUAdvanced = &vmopv1.VirtualMachineCPUAdvancedSpec{
				Topology: &vmopv1.VirtualMachineCPUTopologySpec{
					VNUMANodeCount: ptr.To(int32(2)),
				},
			}
			// UPTv2 requires full memory reservation.
			vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{
				ReservationLockedToMax: ptr.To(true),
			}
			// Powered off so the non-hot-pluggable VNUMANodeID field can apply.
			vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:        "eth0",
					VNUMANodeID: ptr.To(int32(1)),
					VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
						UPTv2Enabled: ptr.To(true),
					},
				},
			}
		})

		It("produces exactly one DeviceChange entry with both values set", func() {
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			editChanges := collectEditChanges(configSpec)
			Expect(editChanges).To(HaveLen(1), "expected exactly one DeviceChange edit, got %d", len(editChanges))

			dev := editChanges[0].Device.(*vimtypes.VirtualVmxnet3)
			Expect(dev.Uptv2Enabled).NotTo(BeNil())
			Expect(*dev.Uptv2Enabled).To(BeTrue())
			Expect(dev.NumaNode).To(Equal(ptr.To(int32(1))))
		})

		It("augments a pre-existing DeviceChange entry rather than adding a duplicate", func() {
			// Simulate another reconciler (e.g. UpdateEthCardDeviceChanges) having
			// already added an Edit for this NIC before we run.
			existing := &vimtypes.VirtualVmxnet3{}
			existing.Key = 4000
			configSpec.DeviceChange = append(configSpec.DeviceChange, &vimtypes.VirtualDeviceConfigSpec{
				Device:    existing,
				Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
			})

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			editChanges := collectEditChanges(configSpec)
			Expect(editChanges).To(HaveLen(1), "expected exactly one DeviceChange edit after augmenting pre-existing entry")

			dev := editChanges[0].Device.(*vimtypes.VirtualVmxnet3)
			Expect(dev.Uptv2Enabled).NotTo(BeNil())
			Expect(*dev.Uptv2Enabled).To(BeTrue())
			Expect(dev.NumaNode).To(Equal(ptr.To(int32(1))))
		})
	})

	Context("when UPTv2Enabled=true but memory reservation is not locked to max", func() {
		BeforeEach(func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name: "eth0",
					VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
						UPTv2Enabled: ptr.To(true),
					},
				},
			}
			// No memoryAdvanced set → prerequisite blocked.
		})

		It("emits no DeviceChange when memory reservation prerequisite is unmet", func() {
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(collectEditChanges(configSpec)).To(BeEmpty(),
				"expected no DeviceChange when memory reservation prerequisite is unmet")
		})

		It("unblocks when spec.resources.requests.memory equals spec.resources.size.memory", func() {
			mem := resource.MustParse("4Gi")
			vm.Spec.Resources = &vmopv1.VirtualMachineResourcesSpec{
				Requests: &vmopv1.VirtualMachineResourceQuantity{Memory: &mem},
				Size:     &vmopv1.VirtualMachineResourceQuantity{Memory: &mem},
			}
			vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOn

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			edits := collectEditChanges(configSpec)
			Expect(edits).To(HaveLen(1))
			Expect(edits[0].Device.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(true)))
		})

		It("still blocks when spec requests.memory != size.memory (partial reservation)", func() {
			req := resource.MustParse("2Gi")
			size := resource.MustParse("4Gi")
			vm.Spec.Resources = &vmopv1.VirtualMachineResourcesSpec{
				Requests: &vmopv1.VirtualMachineResourceQuantity{Memory: &req},
				Size:     &vmopv1.VirtualMachineResourceQuantity{Memory: &size},
			}

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
			Expect(collectEditChanges(configSpec)).To(BeEmpty())
		})
	})

	Context("when UPTv2Enabled=false and memory reservation is not set", func() {
		BeforeEach(func() {
			// Simulate a NIC that has Uptv2Enabled=true in vSphere being cleared.
			existing := &vimtypes.VirtualVmxnet3{}
			existing.Key = 4000
			existing.Uptv2Enabled = ptr.To(true)
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{existing}

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name: "eth0",
					VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
						UPTv2Enabled: ptr.To(false),
					},
				},
			}
		})

		It("applies the DeviceChange without a memory reservation prerequisite", func() {
			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			edits := collectEditChanges(configSpec)
			Expect(edits).To(HaveLen(1))
			Expect(edits[0].Device.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(false)))
		})
	})

	Context("when spec.VNUMANodeID is -1 (explicit clear)", func() {
		BeforeEach(func() {
			// EFI firmware and vNUMA topology are prerequisites for VNUMANodeID.
			vm.Spec.BootOptions = &vmopv1.VirtualMachineBootOptions{
				Firmware: vmopv1.VirtualMachineBootOptionsFirmwareTypeEFI,
			}
			vm.Spec.CPUAdvanced = &vmopv1.VirtualMachineCPUAdvancedSpec{
				Topology: &vmopv1.VirtualMachineCPUTopologySpec{
					VNUMANodeCount: ptr.To(int32(2)),
				},
			}
			vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:        "eth0",
					VNUMANodeID: ptr.To(int32(-1)),
				},
			}
		})

		When("device has an existing NUMA assignment", func() {
			BeforeEach(func() {
				dev := &vimtypes.VirtualVmxnet3{}
				dev.Key = 4000
				dev.NumaNode = ptr.To(int32(2))
				moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{dev}
			})

			It("sends NumaNode = -1 in DeviceChange to clear the NUMA assignment", func() {
				Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

				editChanges := collectEditChanges(configSpec)
				Expect(editChanges).To(HaveLen(1))
				dev := editChanges[0].Device.(*vimtypes.VirtualVmxnet3)
				Expect(dev.NumaNode).To(Equal(ptr.To(int32(-1))))
			})
		})

		When("device already has no NUMA assignment", func() {
			It("emits no DeviceChange (already no NUMA)", func() {
				Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())
				Expect(collectEditChanges(configSpec)).To(BeEmpty())
			})
		})
	})
})

// newNetworkCard returns a VMXNET3 card with the given key and unit number,
// backed by the named standard network.
func newNetworkCard(key, unitNumber int32, networkName string) *vimtypes.VirtualVmxnet3 {
	d := &vimtypes.VirtualVmxnet3{}
	d.Key = key
	d.UnitNumber = ptr.To(unitNumber)
	d.Backing = networkBacking(networkName)
	return d
}

func networkBacking(networkName string) *vimtypes.VirtualEthernetCardNetworkBackingInfo {
	return &vimtypes.VirtualEthernetCardNetworkBackingInfo{
		VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
			DeviceName: networkName,
		},
	}
}

// extraConfigValue returns the value of key in cs.ExtraConfig.
func extraConfigValue(cs *vimtypes.VirtualMachineConfigSpec, key string) (string, bool) {
	for _, ec := range cs.ExtraConfig {
		if kv := ec.GetOptionValue(); kv != nil && kv.Key == key {
			v, _ := kv.Value.(string)
			return v, true
		}
	}
	return "", false
}

// expectOneChangePerKey asserts that cs carries at most one device change per
// device key, so never two Edits and never an Edit and a Remove for one key.
func expectOneChangePerKey(cs *vimtypes.VirtualMachineConfigSpec) {
	GinkgoHelper()
	ops := map[int32][]vimtypes.VirtualDeviceConfigSpecOperation{}
	for _, dc := range cs.DeviceChange {
		spec := dc.GetVirtualDeviceConfigSpec()
		key := spec.Device.GetVirtualDevice().Key
		ops[key] = append(ops[key], spec.Operation)
	}
	for key, o := range ops {
		Expect(o).To(HaveLen(1), "device %d has device changes %v", key, o)
	}
}

var _ = Describe("networkextraconfig.Reconcile with network reconcile state", Label(testlabels.Update), func() {
	var (
		ctx        context.Context
		vm         *vmopv1.VirtualMachine
		moVM       mo.VirtualMachine
		configSpec *vimtypes.VirtualMachineConfigSpec
		st         *network.ReconcileState
		r          = networkextraconfig.New()
	)

	BeforeEach(func() {
		ctx = pkgcfg.NewContext()
		ctx = pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
			cfg.Features.VMServiceClasslessVM = true
			// The named-network provider resolves every interface Ready
			// without any Kubernetes calls.
			cfg.NetworkProviderType = pkgcfg.NetworkProviderTypeNamed
		})

		vm = &vmopv1.VirtualMachine{}
		vm.Name, vm.Namespace = "vm", "ns"
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff
		vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{
			ReservationLockedToMax: ptr.To(true),
		}
		vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}

		moVM = mo.VirtualMachine{
			Config: &vimtypes.VirtualMachineConfigInfo{Version: "vmx-21"},
		}
		configSpec = &vimtypes.VirtualMachineConfigSpec{}
		st = nil
	})

	// observe stores the shared observation of vm and moVM in the context.
	observe := func() {
		st = &network.ReconcileState{
			Observed: network.Observe(ctx, network.ObserveInput{VM: vm, MoVM: moVM}),
		}
		Expect(st.Observed.Complete).To(BeTrue())
		ctx = network.WithContext(ctx, st)
	}

	// plan builds the plan for the observation and puts its device changes
	// in configSpec, as the powered-off dispatch sites do before
	// doReconfigure runs the vmconfig reconcilers.
	plan := func() {
		backings := map[string]vimtypes.BaseVirtualDeviceBackingInfo{}
		for _, iface := range vm.Spec.Network.Interfaces {
			backings[iface.Name] = networkBacking(iface.Network.Name)
		}
		st.Plan = network.BuildPlan(st.Observed, network.PlanInput{
			PowerState:   vimtypes.VirtualMachinePowerStatePoweredOff,
			ApplyChanges: true,
			CardBackings: backings,
		})
		configSpec.DeviceChange = append(configSpec.DeviceChange, st.Plan.DeviceChanges().Specs()...)
	}

	iface := func(name, networkName string) vmopv1.VirtualMachineNetworkInterfaceSpec {
		return vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:    name,
			Network: &vmopv1common.PartialObjectRef{Name: networkName},
		}
	}

	It("tunes the adapters the shared match claims rather than zipping by position", func() {
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			iface("eth0", "net-b"),
			iface("eth1", "net-a"),
		}
		vm.Spec.Network.Interfaces[0].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth0"}}
		vm.Spec.Network.Interfaces[1].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth1"}}
		moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{
			newNetworkCard(4000, 7, "net-a"),
			newNetworkCard(4001, 8, "net-b"),
		}
		observe()

		Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

		v, ok := extraConfigValue(configSpec, "ethernet1.foo")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("eth0"))
		v, ok = extraConfigValue(configSpec, "ethernet0.foo")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("eth1"))
	})

	It("tunes a numbered interface's adapter and skips a numbered interface with no adapter", func() {
		ctx = pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
			cfg.Features.VMNetworkUnitNumbers = true
		})
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			iface("eth0", "net-x"),
			iface("eth1", "net-x"),
		}
		vm.Spec.Network.Interfaces[0].UnitNumber = ptr.To(int32(9))
		vm.Spec.Network.Interfaces[0].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth0"}}
		vm.Spec.Network.Interfaces[1].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth1"}}
		moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{newNetworkCard(4000, 7, "net-y")}
		observe()

		Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

		// eth0 has no adapter at unit 9; eth1 takes 4000 positionally.
		v, ok := extraConfigValue(configSpec, "ethernet0.foo")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("eth1"))
	})

	Context("with a plan", func() {
		BeforeEach(func() {
			vm.Spec.BootOptions = &vmopv1.VirtualMachineBootOptions{
				Firmware: vmopv1.VirtualMachineBootOptionsFirmwareTypeEFI,
			}
			vm.Spec.CPUAdvanced = &vmopv1.VirtualMachineCPUAdvancedSpec{
				Topology: &vmopv1.VirtualMachineCPUTopologySpec{
					VNUMANodeCount: ptr.To(int32(2)),
				},
			}
		})

		It("folds the tuning of an added adapter into the plan's Add", func() {
			eth0 := iface("eth0", "net-a")
			eth0.AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth0"}}
			eth1 := iface("eth1", "net-b")
			eth1.VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)}
			eth1.VNUMANodeID = ptr.To(int32(1))
			eth1.AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth1"}}
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0, eth1}
			observed := newNetworkCard(4000, 7, "net-a")
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{observed}
			observe()
			plan()

			action, _ := st.Plan.InterfaceAction("eth1")
			Expect(action.Kind).To(Equal(network.ActionAdd))

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			Expect(configSpec.DeviceChange).To(HaveLen(1), "tuning adds no device change of its own")
			spec := configSpec.DeviceChange[0].GetVirtualDeviceConfigSpec()
			Expect(spec.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationAdd))
			Expect(spec.Device.GetVirtualDevice().Key).To(Equal(action.DeviceKey))
			added := spec.Device.(*vimtypes.VirtualVmxnet3)
			Expect(added.Uptv2Enabled).To(Equal(ptr.To(true)))
			Expect(added.NumaNode).To(Equal(ptr.To(int32(1))))

			// The added adapter's ethernetN ExtraConfig waits for its real
			// key; eth0's is applied.
			v, ok := extraConfigValue(configSpec, "ethernet0.foo")
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("eth0"))
			for _, ec := range configSpec.ExtraConfig {
				Expect(ec.GetOptionValue().Value).ToNot(Equal("eth1"))
			}
			Expect(observed.Uptv2Enabled).To(BeNil())
			expectOneChangePerKey(configSpec)
		})

		It("folds the tuning of a replaced adapter into the Add and does not edit the removed adapter", func() {
			eth0 := iface("eth0", "net-b")
			eth0.VNUMANodeID = ptr.To(int32(1))
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0}
			observed := newNetworkCard(4000, 7, "net-a")
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{observed}
			observe()
			plan()

			action, _ := st.Plan.InterfaceAction("eth0")
			Expect(action.Kind).To(Equal(network.ActionReplace))

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			Expect(configSpec.DeviceChange).To(HaveLen(2))
			for _, dc := range configSpec.DeviceChange {
				spec := dc.GetVirtualDeviceConfigSpec()
				switch spec.Operation {
				case vimtypes.VirtualDeviceConfigSpecOperationRemove:
					Expect(spec.Device.GetVirtualDevice().Key).To(Equal(int32(4000)))
					Expect(spec.Device.GetVirtualDevice().NumaNode).To(BeNil())
				case vimtypes.VirtualDeviceConfigSpecOperationAdd:
					Expect(spec.Device.GetVirtualDevice().NumaNode).To(Equal(ptr.To(int32(1))))
				default:
					Fail("unexpected device change " + string(spec.Operation))
				}
			}
			Expect(observed.NumaNode).To(BeNil())
			expectOneChangePerKey(configSpec)
		})

		It("edits the plan's copy instead of adding a duplicate Edit", func() {
			eth0 := iface("eth0", "net-a")
			eth0.VNUMANodeID = ptr.To(int32(1))
			eth0.VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)}
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0}
			observed := newNetworkCard(4000, 7, "net-a")
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{observed}
			observe()
			plan()
			// Stand in for a planned attachment Edit of the claimed adapter.
			planned, err := st.Plan.DeviceChanges().Edit(st.Observed.Adapters[0])
			Expect(err).ToNot(HaveOccurred())
			configSpec.DeviceChange = st.Plan.DeviceChanges().Specs()

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			edits := collectEditChanges(configSpec)
			Expect(edits).To(HaveLen(1))
			Expect(edits[0].Device).To(BeIdenticalTo(planned))
			Expect(planned.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(true)))
			Expect(planned.GetVirtualDevice().NumaNode).To(Equal(ptr.To(int32(1))))
			Expect(observed.Uptv2Enabled).To(BeNil())
			Expect(observed.NumaNode).To(BeNil())
			expectOneChangePerKey(configSpec)
		})

		It("edits a copy of an adapter the plan does not change", func() {
			eth0 := iface("eth0", "net-a")
			eth0.VNUMANodeID = ptr.To(int32(1))
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0}
			observed := newNetworkCard(4000, 7, "net-a")
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{observed}
			observe()
			plan()
			Expect(configSpec.DeviceChange).To(BeEmpty())

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			edits := collectEditChanges(configSpec)
			Expect(edits).To(HaveLen(1))
			Expect(edits[0].Device).ToNot(BeIdenticalTo(observed))
			Expect(edits[0].Device.GetVirtualDevice().NumaNode).To(Equal(ptr.To(int32(1))))
			Expect(observed.NumaNode).To(BeNil())
			Expect(st.Plan.DeviceChanges().Len()).To(BeZero())
		})

		It("does not tune an adapter the plan removes", func() {
			eth0 := iface("eth0", "net-a")
			eth0.VNUMANodeID = ptr.To(int32(1))
			eth0.AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth0"}}
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0}
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{newNetworkCard(4000, 7, "net-a")}
			observe()
			plan()
			Expect(st.Plan.DeviceChanges().Remove(st.Observed.Adapters[0])).To(Succeed())
			configSpec.DeviceChange = st.Plan.DeviceChanges().Specs()

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			Expect(collectEditChanges(configSpec)).To(BeEmpty())
			_, ok := extraConfigValue(configSpec, "ethernet0.foo")
			Expect(ok).To(BeFalse())
			expectOneChangePerKey(configSpec)
		})
	})

	When("there is no network reconcile state", func() {
		It("zips spec interfaces to adapters by position as before", func() {
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				iface("eth0", "net-b"),
				iface("eth1", "net-a"),
			}
			vm.Spec.Network.Interfaces[0].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth0"}}
			vm.Spec.Network.Interfaces[1].AdvancedProperties = []vmopv1common.KeyValuePair{{Key: "foo", Value: "eth1"}}
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{
				newNetworkCard(4000, 7, "net-a"),
				newNetworkCard(4001, 8, "net-b"),
			}

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			v, _ := extraConfigValue(configSpec, "ethernet0.foo")
			Expect(v).To(Equal("eth0"))
			v, _ = extraConfigValue(configSpec, "ethernet1.foo")
			Expect(v).To(Equal("eth1"))
		})

		It("does not edit an adapter the ConfigSpec already removes", func() {
			eth0 := iface("eth0", "net-a")
			eth0.VNUMANodeID = ptr.To(int32(1))
			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0}
			observed := newNetworkCard(4000, 7, "net-a")
			moVM.Config.Hardware.Device = []vimtypes.BaseVirtualDevice{observed}
			configSpec.DeviceChange = []vimtypes.BaseVirtualDeviceConfigSpec{
				&vimtypes.VirtualDeviceConfigSpec{
					Device:    newNetworkCard(4000, 7, "net-a"),
					Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
				},
			}

			Expect(r.Reconcile(ctx, nil, nil, vm, moVM, configSpec)).To(Succeed())

			Expect(collectEditChanges(configSpec)).To(BeEmpty())
			Expect(observed.NumaNode).To(BeNil())
			expectOneChangePerKey(configSpec)
		})
	})
})

// OnResult now only ever marks NetworkConfigSynced=False, and only for a real
// Reconfigure task failure. Everything else — True, PrerequisiteNotMet,
// PowerOffRequired, PowerCyclePending — is decided by
// reconcileStatusNetworkExtraConfig (pkg/providers/vsphere/vmlifecycle),
// computed fresh from moVM and spec.network every reconcile. See that
// package's "NetworkExtraConfig status" tests for those cases.
var _ = Describe("networkextraconfig.OnResult", func() {
	var (
		ctx  context.Context
		vm   *vmopv1.VirtualMachine
		moVM mo.VirtualMachine
		r    = networkextraconfig.New()
	)

	BeforeEach(func() {
		ctx = pkgcfg.NewContext()
		ctx = pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
			cfg.Features.VMServiceClasslessVM = true
		})

		vm = &vmopv1.VirtualMachine{}
		vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{}

		moVM = mo.VirtualMachine{}
	})

	Context("when VMServiceClasslessVM is disabled", func() {
		It("returns nil without setting condition", func() {
			ctx2 := pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
				cfg.Features.VMServiceClasslessVM = false
			})
			Expect(r.OnResult(ctx2, vm, moVM, nil)).To(Succeed())
			Expect(vm.Status.Conditions).To(BeEmpty())
		})
	})

	Context("with a task error", func() {
		It("marks condition false with NetworkConfigError", func() {
			Expect(r.OnResult(ctx, vm, moVM, errFake)).To(Succeed())
			cond := findCondition(vm)
			Expect(cond).NotTo(BeNil())
			Expect(string(cond.Status)).To(Equal("False"))
			Expect(cond.Reason).To(Equal(vmopv1.VirtualMachineNetworkConfigErrorReason))
		})
	})

	Context("with a NoRequeueNoErr sentinel (e.g. ErrCreate, ErrHasTask)", func() {
		It("does not mark the condition", func() {
			Expect(r.OnResult(ctx, vm, moVM, pkgerr.NoRequeueNoErr("created vm"))).To(Succeed())
			Expect(findCondition(vm)).To(BeNil())
		})
	})

	Context("on success", func() {
		It("leaves the condition untouched", func() {
			Expect(r.OnResult(ctx, vm, moVM, nil)).To(Succeed())
			Expect(findCondition(vm)).To(BeNil())
		})
	})
})
