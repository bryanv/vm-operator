// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine/networkextraconfig"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

func newVMXNet3Dev(key int32) *vimtypes.VirtualVmxnet3 {
	d := &vimtypes.VirtualVmxnet3{}
	d.Key = key
	return d
}

func getVal(ov pkgutil.OptionValues, key string) (string, bool) {
	return ov.GetString(key)
}

var _ = Describe("DefaultNICMatcher / EthernetDeviceIndex", func() {
	It("positionally zips ethernet spec interfaces to ethernet hardware devices", func() {
		matcher := networkextraconfig.DefaultNICMatcher([]vimtypes.BaseVirtualDevice{
			newVMXNet3Dev(4000),
			newVMXNet3Dev(4001),
		})

		dev0 := matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"}, 0)
		Expect(dev0).ToNot(BeNil())
		idx0, ok := networkextraconfig.EthernetDeviceIndex(dev0)
		Expect(ok).To(BeTrue())
		Expect(idx0).To(Equal(int32(0)))

		dev1 := matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"}, 1)
		Expect(dev1).ToNot(BeNil())
		idx1, ok := networkextraconfig.EthernetDeviceIndex(dev1)
		Expect(ok).To(BeTrue())
		Expect(idx1).To(Equal(int32(1)))
	})

	It("returns nil for SR-IOV interface types", func() {
		matcher := networkextraconfig.DefaultNICMatcher([]vimtypes.BaseVirtualDevice{newVMXNet3Dev(4000)})
		dev := matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name: "eth0",
			Type: vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV,
		}, 0)
		Expect(dev).To(BeNil())
	})

	It("returns nil when no more hardware devices are available", func() {
		matcher := networkextraconfig.DefaultNICMatcher(nil)
		Expect(matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"}, 0)).To(BeNil())
	})

	// Characterization test: pins today's known limitation that positional
	// matching has no notion of interface identity, so reordering
	// spec.network.interfaces (same Names/props, swapped slice positions)
	// cross-wires each interface's ExtraConfig onto the wrong physical
	// device — no hardware change required. See the TODO on
	// DefaultNICMatcher: this must be revisited (and this test rewritten
	// to assert the opposite) once matching moves to a stable per-NIC
	// identity (e.g. the NIC unit-number feature).
	It("cross-wires ExtraConfig when spec interfaces are reordered (documents current positional-matching limitation)", func() {
		hwDevs := []vimtypes.BaseVirtualDevice{newVMXNet3Dev(4000), newVMXNet3Dev(4001)}
		ctx := context.Background()

		eth1 := vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:    "eth1",
			VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{RSSOffloadEnabled: ptr.To(true)},
		}
		eth2 := vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:    "eth2",
			VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{RSSOffloadEnabled: ptr.To(false)},
		}

		// Before: spec call order [eth1, eth2] lines up with hardware order
		// [4000, 4001] — device 4000 (ethernet0) gets eth1's value.
		before := networkextraconfig.DefaultNICMatcher(hwDevs)
		dev := before(eth1, 0)
		ec := networkextraconfig.DesiredNICExtraConfig(ctx, eth1, dev.GetVirtualDevice().Key, nil)
		v, _ := getVal(ec, "ethernet0.rssoffload")
		Expect(v).To(Equal("TRUE"), "eth1 (RSSOffloadEnabled=true) lands on device 4000")

		dev = before(eth2, 1)
		ec = networkextraconfig.DesiredNICExtraConfig(ctx, eth2, dev.GetVirtualDevice().Key, nil)
		v, _ = getVal(ec, "ethernet1.rssoffload")
		Expect(v).To(Equal("FALSE"), "eth2 (RSSOffloadEnabled=false) lands on device 4001")

		// After: the spec array is reordered — eth2 called first, eth1
		// second — while the hardware device list is untouched. Matching
		// is purely call-order based, so it silently swaps which
		// interface's properties reach which physical device.
		after := networkextraconfig.DefaultNICMatcher(hwDevs)
		dev = after(eth2, 0)
		ec = networkextraconfig.DesiredNICExtraConfig(ctx, eth2, dev.GetVirtualDevice().Key, nil)
		v, _ = getVal(ec, "ethernet0.rssoffload")
		Expect(v).To(Equal("FALSE"), "device 4000 (ethernet0) now gets eth2's value instead of eth1's")

		dev = after(eth1, 1)
		ec = networkextraconfig.DesiredNICExtraConfig(ctx, eth1, dev.GetVirtualDevice().Key, nil)
		v, _ = getVal(ec, "ethernet1.rssoffload")
		Expect(v).To(Equal("TRUE"), "device 4001 (ethernet1) now gets eth1's value instead of eth2's")
	})
})

var _ = Describe("DesiredNICExtraConfig", func() {
	ctx := context.Background()

	It("translates first-class VMXNet3 fields with the device prefix", func() {
		iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
			VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{RSSOffloadEnabled: ptr.To(true)},
		}
		got := networkextraconfig.DesiredNICExtraConfig(ctx, iface, 4000, nil)
		v, ok := getVal(got, "ethernet0.rssoffload")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("TRUE"))
	})

	It("writes bag keys with the device prefix", func() {
		iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
			AdvancedProperties: []vmopv1common.KeyValuePair{{Key: "latencySensitivity.level", Value: "high"}},
		}
		got := networkextraconfig.DesiredNICExtraConfig(ctx, iface, 4000, nil)
		v, ok := getVal(got, "ethernet0.latencySensitivity.level")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("high"))
	})

	It("skips a first-class key if present in advancedProperties", func() {
		iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
			AdvancedProperties: []vmopv1common.KeyValuePair{{Key: "ethernet0.rssoffload", Value: "bogus"}},
		}
		got := networkextraconfig.DesiredNICExtraConfig(ctx, iface, 4000, nil)
		Expect(got).To(BeEmpty())
	})

	It("clears a previously-managed bag key no longer requested", func() {
		iface := vmopv1.VirtualMachineNetworkInterfaceSpec{}
		got := networkextraconfig.DesiredNICExtraConfig(ctx, iface, 4000, []string{"foo"})
		v, ok := getVal(got, "ethernet0.foo")
		Expect(ok).To(BeTrue())
		Expect(v).To(BeEmpty())
	})

	It("does not clear a managed key that is still requested", func() {
		iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
			AdvancedProperties: []vmopv1common.KeyValuePair{{Key: "foo", Value: "bar"}},
		}
		got := networkextraconfig.DesiredNICExtraConfig(ctx, iface, 4000, []string{"foo"})
		v, ok := getVal(got, "ethernet0.foo")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("bar"))
	})
})

var _ = Describe("NICExtraConfigDiff", func() {
	ctx := context.Background()

	It("returns nothing when overlay matches observed", func() {
		observed := pkgutil.OptionValues{&vimtypes.OptionValue{Key: "ethernet0.rssoffload", Value: "TRUE"}}
		overlay := pkgutil.OptionValues{&vimtypes.OptionValue{Key: "ethernet0.rssoffload", Value: "TRUE"}}
		applied, deferred, powerCyclePending := networkextraconfig.NICExtraConfigDiff(ctx, observed, overlay, nil, false)
		Expect(applied).To(BeEmpty())
		Expect(deferred).To(BeEmpty())
		Expect(powerCyclePending).To(BeFalse())
	})

	It("scopes the diff to overlay's own keys when existingEC is nil", func() {
		// An unrelated reconciler's pending change (in existingEC, here omitted)
		// must not surface as this NIC's mismatch.
		observed := pkgutil.OptionValues{}
		overlay := pkgutil.OptionValues{&vimtypes.OptionValue{Key: "ethernet0.foo", Value: "bar"}}
		applied, _, _ := networkextraconfig.NICExtraConfigDiff(ctx, observed, overlay, nil, false)
		Expect(applied).To(HaveLen(1))
	})

	It("merges overlay onto existingEC before diffing", func() {
		observed := pkgutil.OptionValues{}
		existingEC := pkgutil.OptionValues{&vimtypes.OptionValue{Key: "ethernet0.other", Value: "baz"}}
		overlay := pkgutil.OptionValues{&vimtypes.OptionValue{Key: "ethernet0.foo", Value: "bar"}}
		applied, _, _ := networkextraconfig.NICExtraConfigDiff(ctx, observed, overlay, existingEC, false)
		Expect(applied).To(HaveLen(2))
	})
})

var _ = Describe("ReconcileNICFields", func() {
	var (
		vm  vmopv1.VirtualMachine
		ci  vimtypes.VirtualMachineConfigInfo
		dev *vimtypes.VirtualVmxnet3
	)

	BeforeEach(func() {
		vm = vmopv1.VirtualMachine{}
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff
		dev = newVMXNet3Dev(4000)
		ci = vimtypes.VirtualMachineConfigInfo{Version: "vmx-21"}
	})

	Context("with dryRun=true", func() {
		It("reports the field as needing a change but never mutates dev", func() {
			iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
				VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)},
			}
			vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{ReservationLockedToMax: ptr.To(true)}

			cs := &vimtypes.VirtualMachineConfigSpec{}
			blocked, blockedPowerOff := networkextraconfig.ReconcileNICFields(vm, iface, dev, ci, cs, true)

			Expect(blocked).To(BeEmpty())
			Expect(blockedPowerOff).To(BeEmpty())
			Expect(cs.DeviceChange).To(BeEmpty(), "dryRun must not add DeviceChange entries")
			Expect(dev.Uptv2Enabled).To(BeNil(), "dryRun must never mutate the source device")
		})

		It("still reports blocked (prerequisite) fields", func() {
			iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
				VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)},
			}
			// No memoryAdvanced set → prerequisite blocked.
			cs := &vimtypes.VirtualMachineConfigSpec{}
			blocked, _ := networkextraconfig.ReconcileNICFields(vm, iface, dev, ci, cs, true)
			Expect(blocked).To(HaveLen(1))
			Expect(dev.Uptv2Enabled).To(BeNil())
		})
	})

	Context("with dryRun=false", func() {
		It("mutates dev and adds a DeviceChange entry", func() {
			iface := vmopv1.VirtualMachineNetworkInterfaceSpec{
				VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)},
			}
			vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{ReservationLockedToMax: ptr.To(true)}

			cs := &vimtypes.VirtualMachineConfigSpec{}
			blocked, blockedPowerOff := networkextraconfig.ReconcileNICFields(vm, iface, dev, ci, cs, false)

			Expect(blocked).To(BeEmpty())
			Expect(blockedPowerOff).To(BeEmpty())
			Expect(cs.DeviceChange).To(HaveLen(1))
			Expect(dev.Uptv2Enabled).To(Equal(ptr.To(true)))
		})
	})
})

// newCard returns a VMXNET3 card with the given key and unit number, backed by
// the named standard network.
func newCard(key, unitNumber int32, networkName string) *vimtypes.VirtualVmxnet3 {
	d := newVMXNet3Dev(key)
	d.UnitNumber = ptr.To(unitNumber)
	d.Backing = &vimtypes.VirtualEthernetCardNetworkBackingInfo{
		VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
			DeviceName: networkName,
		},
	}
	return d
}

// newSRIOVCard returns an SR-IOV card with the given key and unit number,
// backed by the named standard network.
func newSRIOVCard(key, unitNumber int32, networkName string) *vimtypes.VirtualSriovEthernetCard {
	d := &vimtypes.VirtualSriovEthernetCard{}
	d.Key = key
	d.UnitNumber = ptr.To(unitNumber)
	d.Backing = &vimtypes.VirtualEthernetCardNetworkBackingInfo{
		VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
			DeviceName: networkName,
		},
	}
	return d
}

// newIface returns a spec interface on the named network.
func newIface(name, networkName string) vmopv1.VirtualMachineNetworkInterfaceSpec {
	return vmopv1.VirtualMachineNetworkInterfaceSpec{
		Name:    name,
		Network: &vmopv1common.PartialObjectRef{Name: networkName},
	}
}

// namedNetworkContext returns a context for the named-network provider, whose
// observation makes no Kubernetes calls and resolves every interface Ready.
func namedNetworkContext(unitNumbers bool) context.Context {
	ctx := pkgcfg.NewContext()
	return pkgcfg.UpdateContext(ctx, func(cfg *pkgcfg.Config) {
		cfg.NetworkProviderType = pkgcfg.NetworkProviderTypeNamed
		cfg.Features.VMNetworkUnitNumbers = unitNumbers
	})
}

// observe returns the shared network observation of a VM with the given
// interfaces and devices.
func observe(
	ctx context.Context,
	ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
	devs []vimtypes.BaseVirtualDevice) *network.Info {

	vm := &vmopv1.VirtualMachine{}
	vm.Name, vm.Namespace = "vm", "ns"
	vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{Interfaces: ifaces}
	moVM := mo.VirtualMachine{
		Config: &vimtypes.VirtualMachineConfigInfo{
			Hardware: vimtypes.VirtualHardware{Device: devs},
		},
	}
	info := network.Observe(ctx, network.ObserveInput{VM: vm, MoVM: moVM})
	Expect(info.Complete).To(BeTrue())
	return info
}

// matchKeys returns the device key each interface maps to, or nil for an
// interface the matcher maps to no device.
func matchKeys(
	matcher networkextraconfig.NICDeviceMatcher,
	ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []*int32 {

	out := make([]*int32, len(ifaces))
	for i, iface := range ifaces {
		if dev := matcher(iface, i); dev != nil {
			out[i] = ptr.To(dev.GetVirtualDevice().Key)
		}
	}
	return out
}

// expectOneChangePerKey asserts that cs carries at most one Edit or Add per
// device key and never both an Edit and a Remove for the same key.
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

var _ = Describe("NICMatcherFromContext", Label(testlabels.Update), func() {
	It("falls back to DefaultNICMatcher when the context has no network state", func() {
		ctx := namedNetworkContext(true)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			newIface("eth0", "net-b"),
			newIface("eth1", "net-a"),
		}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a"), newCard(4001, 8, "net-b")}

		got := matchKeys(networkextraconfig.NICMatcherFromContext(ctx, devs), ifaces)
		want := matchKeys(networkextraconfig.DefaultNICMatcher(devs), ifaces)
		Expect(got).To(Equal(want))
		Expect(got).To(Equal([]*int32{ptr.To(int32(4000)), ptr.To(int32(4001))}))
	})

	It("falls back to DefaultNICMatcher when the state has no observation", func() {
		ctx := network.WithContext(namedNetworkContext(true), &network.ReconcileState{})
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{newIface("eth0", "net-b")}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a")}

		Expect(matchKeys(networkextraconfig.NICMatcherFromContext(ctx, devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4000))}))
	})

	It("uses the shared match when the context carries an observation", func() {
		ctx := namedNetworkContext(false)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			newIface("eth0", "net-b"),
			newIface("eth1", "net-a"),
		}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a"), newCard(4001, 8, "net-b")}
		ctx = network.WithContext(ctx, &network.ReconcileState{Observed: observe(ctx, ifaces, devs)})

		Expect(matchKeys(networkextraconfig.NICMatcherFromContext(ctx, devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4001)), ptr.To(int32(4000))}))
	})
})

var _ = Describe("SharedNICMatcher", Label(testlabels.Update), func() {
	It("maps unnumbered interfaces by attachment identity rather than by position", func() {
		ctx := namedNetworkContext(false)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			newIface("eth0", "net-b"),
			newIface("eth1", "net-a"),
		}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a"), newCard(4001, 8, "net-b")}
		info := observe(ctx, ifaces, devs)

		Expect(matchKeys(networkextraconfig.SharedNICMatcher(info, nil, devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4001)), ptr.To(int32(4000))}))
	})

	It("maps reordered spec interfaces to the same adapters", func() {
		ctx := namedNetworkContext(false)
		eth0, eth1 := newIface("eth0", "net-a"), newIface("eth1", "net-b")
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a"), newCard(4001, 8, "net-b")}

		before := []vmopv1.VirtualMachineNetworkInterfaceSpec{eth0, eth1}
		after := []vmopv1.VirtualMachineNetworkInterfaceSpec{eth1, eth0}

		Expect(matchKeys(networkextraconfig.SharedNICMatcher(observe(ctx, before, devs), nil, devs), before)).
			To(Equal([]*int32{ptr.To(int32(4000)), ptr.To(int32(4001))}))
		Expect(matchKeys(networkextraconfig.SharedNICMatcher(observe(ctx, after, devs), nil, devs), after)).
			To(Equal([]*int32{ptr.To(int32(4001)), ptr.To(int32(4000))}))
	})

	It("maps numbered interfaces by unit number and never falls back for a miss", func() {
		ctx := namedNetworkContext(true)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			newIface("eth0", "net-x"),
			newIface("eth1", "net-x"),
			newIface("eth2", "net-x"),
		}
		ifaces[0].UnitNumber = ptr.To(int32(8))
		ifaces[1].UnitNumber = ptr.To(int32(7))
		ifaces[2].UnitNumber = ptr.To(int32(9))
		devs := []vimtypes.BaseVirtualDevice{
			newCard(4000, 7, "net-x"),
			newCard(4001, 8, "net-x"),
			newCard(4002, 10, "net-x"),
		}
		info := observe(ctx, ifaces, devs)

		Expect(matchKeys(networkextraconfig.SharedNICMatcher(info, nil, devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4001)), ptr.To(int32(4000)), nil}))
		// DefaultNICMatcher zips eth2 to 4002 even though its unit number is
		// 10, not 9.
		Expect(matchKeys(networkextraconfig.DefaultNICMatcher(devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4000)), ptr.To(int32(4001)), ptr.To(int32(4002))}))
	})

	It("maps mixed numbered and unnumbered interfaces", func() {
		ctx := namedNetworkContext(true)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			newIface("eth0", "net-q"),
			newIface("eth1", "net-z"),
		}
		ifaces[0].UnitNumber = ptr.To(int32(8))
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-q"), newCard(4001, 8, "net-q")}
		info := observe(ctx, ifaces, devs)

		// eth0 claims unit 8 exactly; eth1 has no identity match and takes the
		// remaining adapter positionally.
		Expect(matchKeys(networkextraconfig.SharedNICMatcher(info, nil, devs), ifaces)).
			To(Equal([]*int32{ptr.To(int32(4001)), ptr.To(int32(4000))}))
	})

	It("never maps an interface to an SR-IOV adapter", func() {
		ctx := namedNetworkContext(false)
		sriov := newIface("sriov0", "net-s")
		sriov.Type = vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{sriov, newIface("eth0", "net-q")}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-z"), newSRIOVCard(4001, 8, "net-z")}
		info := observe(ctx, ifaces, devs)

		// The positional claims pair sriov0 with 4000 and eth0 with the SR-IOV
		// adapter 4001. Neither interface is tuned.
		Expect(info.Interfaces[1].Match.AdapterKey).To(Equal(int32(4001)))
		Expect(matchKeys(networkextraconfig.SharedNICMatcher(info, nil, devs), ifaces)).
			To(Equal([]*int32{nil, nil}))
	})

	It("finds an interface by name when the spec index differs", func() {
		ctx := namedNetworkContext(false)
		ifaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{newIface("eth0", "net-a"), newIface("eth1", "net-b")}
		devs := []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a"), newCard(4001, 8, "net-b")}
		matcher := networkextraconfig.SharedNICMatcher(observe(ctx, ifaces, devs), nil, devs)

		Expect(matcher(ifaces[1], 0).GetVirtualDevice().Key).To(Equal(int32(4001)))
		Expect(matcher(newIface("eth9", "net-a"), 0)).To(BeNil())
	})

	Context("with a plan", func() {
		var (
			ctx    context.Context
			ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec
			devs   []vimtypes.BaseVirtualDevice
		)

		BeforeEach(func() {
			ctx = namedNetworkContext(false)
		})

		buildPlan := func() (*network.Info, *network.Plan) {
			info := observe(ctx, ifaces, devs)
			backings := map[string]vimtypes.BaseVirtualDeviceBackingInfo{}
			for _, iface := range ifaces {
				backings[iface.Name] = &vimtypes.VirtualEthernetCardNetworkBackingInfo{
					VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
						DeviceName: iface.Network.Name,
					},
				}
			}
			return info, network.BuildPlan(info, network.PlanInput{
				PowerState:   vimtypes.VirtualMachinePowerStatePoweredOff,
				ApplyChanges: true,
				CardBackings: backings,
			})
		}

		It("maps an interface the plan adds to the plan's new device", func() {
			ifaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{newIface("eth0", "net-a"), newIface("eth1", "net-b")}
			devs = []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a")}
			info, plan := buildPlan()

			action, ok := plan.InterfaceAction("eth1")
			Expect(ok).To(BeTrue())
			Expect(action.Kind).To(Equal(network.ActionAdd))

			matcher := networkextraconfig.SharedNICMatcher(info, plan, devs)
			Expect(matcher(ifaces[0], 0).GetVirtualDevice().Key).To(Equal(int32(4000)))
			added := matcher(ifaces[1], 1)
			Expect(added).ToNot(BeNil())
			Expect(added.GetVirtualDevice().Key).To(Equal(action.DeviceKey))
			planned, _ := plan.DeviceChanges().Lookup(action.DeviceKey)
			Expect(added).To(BeIdenticalTo(planned))
			Expect(networkextraconfig.IsAddedDevice(added)).To(BeTrue())
			_, hasIdx := networkextraconfig.EthernetDeviceIndex(added)
			Expect(hasIdx).To(BeFalse())
		})

		It("maps a replaced interface to the new device and never to the removed adapter", func() {
			ifaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{newIface("eth0", "net-b")}
			devs = []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a")}
			info, plan := buildPlan()

			action, _ := plan.InterfaceAction("eth0")
			Expect(action.Kind).To(Equal(network.ActionReplace))
			Expect(plan.DeviceChanges().Removed(4000)).To(BeTrue())

			dev := networkextraconfig.SharedNICMatcher(info, plan, devs)(ifaces[0], 0)
			Expect(dev).ToNot(BeNil())
			Expect(dev.GetVirtualDevice().Key).To(Equal(action.DeviceKey))
		})

		It("maps a claimed adapter the plan removes to nil", func() {
			ifaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{newIface("eth0", "net-a")}
			devs = []vimtypes.BaseVirtualDevice{newCard(4000, 7, "net-a")}
			info, plan := buildPlan()
			Expect(plan.DeviceChanges().Remove(info.Adapters[0])).To(Succeed())

			Expect(networkextraconfig.SharedNICMatcher(info, plan, devs)(ifaces[0], 0)).To(BeNil())
		})
	})
})

var _ = Describe("ReconcileNICFieldsWithChanges", Label(testlabels.Update), func() {
	var (
		ctx   context.Context
		vm    vmopv1.VirtualMachine
		ci    vimtypes.VirtualMachineConfigInfo
		iface vmopv1.VirtualMachineNetworkInterfaceSpec
		dev   *vimtypes.VirtualVmxnet3
		info  *network.Info
		plan  *network.Plan
		cs    *vimtypes.VirtualMachineConfigSpec
	)

	BeforeEach(func() {
		ctx = namedNetworkContext(false)
		vm = vmopv1.VirtualMachine{}
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff
		vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{ReservationLockedToMax: ptr.To(true)}
		ci = vimtypes.VirtualMachineConfigInfo{Version: "vmx-21"}

		iface = newIface("eth0", "net-a")
		iface.VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)}
		dev = newCard(4000, 7, "net-a")

		info = observe(ctx, []vmopv1.VirtualMachineNetworkInterfaceSpec{iface}, []vimtypes.BaseVirtualDevice{dev})
		plan = network.BuildPlan(info, network.PlanInput{
			PowerState:   vimtypes.VirtualMachinePowerStatePoweredOff,
			ApplyChanges: true,
		})
		cs = &vimtypes.VirtualMachineConfigSpec{}
	})

	It("edits the plan's copy already in the ConfigSpec instead of adding a second Edit", func() {
		planned, err := plan.DeviceChanges().Edit(info.Adapters[0])
		Expect(err).ToNot(HaveOccurred())
		cs.DeviceChange = plan.DeviceChanges().Specs()

		networkextraconfig.ReconcileNICFieldsWithChanges(vm, iface, dev, ci, cs, plan.DeviceChanges(), false)

		Expect(cs.DeviceChange).To(HaveLen(1))
		Expect(cs.DeviceChange[0].GetVirtualDeviceConfigSpec().Device).To(BeIdenticalTo(planned))
		Expect(planned.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(true)))
		Expect(dev.Uptv2Enabled).To(BeNil(), "the observed device must not be mutated")
		expectOneChangePerKey(cs)
	})

	It("edits the plan's copy when the plan owns one", func() {
		planned, err := plan.DeviceChanges().Edit(info.Adapters[0])
		Expect(err).ToNot(HaveOccurred())

		networkextraconfig.ReconcileNICFieldsWithChanges(vm, iface, dev, ci, cs, plan.DeviceChanges(), false)

		Expect(cs.DeviceChange).To(BeEmpty(), "the plan's Edit carries the change")
		Expect(planned.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(true)))
		Expect(dev.Uptv2Enabled).To(BeNil())
	})

	It("appends an Edit of a copy when the plan does not touch the device", func() {
		networkextraconfig.ReconcileNICFieldsWithChanges(vm, iface, dev, ci, cs, plan.DeviceChanges(), false)

		Expect(cs.DeviceChange).To(HaveLen(1))
		spec := cs.DeviceChange[0].GetVirtualDeviceConfigSpec()
		Expect(spec.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationEdit))
		Expect(spec.Device).ToNot(BeIdenticalTo(dev))
		Expect(spec.Device.GetVirtualDevice().Key).To(Equal(int32(4000)))
		Expect(spec.Device.(*vimtypes.VirtualVmxnet3).Uptv2Enabled).To(Equal(ptr.To(true)))
		Expect(spec.Device.GetVirtualDevice().Backing).To(Equal(dev.Backing))
		Expect(dev.Uptv2Enabled).To(BeNil(), "the observed device must not be mutated")
		Expect(plan.DeviceChanges().Len()).To(BeZero(), "tuning is not recorded in the plan")
	})

	It("does not edit a device the plan removes", func() {
		Expect(plan.DeviceChanges().Remove(info.Adapters[0])).To(Succeed())
		cs.DeviceChange = plan.DeviceChanges().Specs()

		networkextraconfig.ReconcileNICFieldsWithChanges(vm, iface, dev, ci, cs, plan.DeviceChanges(), false)

		Expect(cs.DeviceChange).To(HaveLen(1))
		Expect(cs.DeviceChange[0].GetVirtualDeviceConfigSpec().Operation).
			To(Equal(vimtypes.VirtualDeviceConfigSpecOperationRemove))
		Expect(dev.Uptv2Enabled).To(BeNil())
		expectOneChangePerKey(cs)
	})

	It("does not edit a device the ConfigSpec removes when there is no plan", func() {
		cs.DeviceChange = []vimtypes.BaseVirtualDeviceConfigSpec{
			&vimtypes.VirtualDeviceConfigSpec{
				Device:    newVMXNet3Dev(4000),
				Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
			},
		}

		networkextraconfig.ReconcileNICFields(vm, iface, dev, ci, cs, false)

		Expect(cs.DeviceChange).To(HaveLen(1))
		Expect(dev.Uptv2Enabled).To(BeNil())
		expectOneChangePerKey(cs)
	})
})
