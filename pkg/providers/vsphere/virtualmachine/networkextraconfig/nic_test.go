// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine/networkextraconfig"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

func newVMXNet3Dev(key int32) *vimtypes.VirtualVmxnet3 {
	d := &vimtypes.VirtualVmxnet3{}
	d.Key = key
	return d
}

func newVMXNet3DevWithUnit(key, unit int32) *vimtypes.VirtualVmxnet3 {
	d := newVMXNet3Dev(key)
	d.UnitNumber = ptr.To(unit)
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
		}, nil, false)

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
		matcher := networkextraconfig.DefaultNICMatcher([]vimtypes.BaseVirtualDevice{newVMXNet3Dev(4000)}, nil, false)
		dev := matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name: "eth0",
			Type: vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV,
		}, 0)
		Expect(dev).To(BeNil())
	})

	It("returns nil when no more hardware devices are available", func() {
		matcher := networkextraconfig.DefaultNICMatcher(nil, nil, false)
		Expect(matcher(vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"}, 0)).To(BeNil())
	})

	// Characterization test: pins today's known limitation that positional
	// matching has no notion of interface identity, so reordering
	// spec.network.interfaces (same Names/props, swapped slice positions)
	// cross-wires each interface's ExtraConfig onto the wrong physical
	// device — no hardware change required. This now pins the zip for
	// UN-NUMBERED interfaces (and the flag-off path); numbered interfaces
	// resolve by unit number instead, so this limitation applies only to
	// interfaces the feature cannot identify.
	It("cross-wires ExtraConfig when un-numbered spec interfaces are reordered (documents positional-matching limitation)", func() {
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
		before := networkextraconfig.DefaultNICMatcher(hwDevs, nil, false)
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
		after := networkextraconfig.DefaultNICMatcher(hwDevs, nil, false)
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

var _ = Describe("DefaultNICMatcher unit numbers", func() {

	It("matches numbered interfaces by unit number when device order differs from spec order", func() {
		hwDevs := []vimtypes.BaseVirtualDevice{
			newVMXNet3DevWithUnit(4000, 8),
			newVMXNet3DevWithUnit(4001, 9),
		}
		interfaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0", UnitNumber: ptr.To(int32(9))},
			{Name: "eth1", UnitNumber: ptr.To(int32(8))},
		}
		matcher := networkextraconfig.DefaultNICMatcher(hwDevs, interfaces, true)

		// Spec order deliberately differs from device order.
		dev0 := matcher(interfaces[0], 0)
		Expect(dev0).ToNot(BeNil())
		Expect(dev0.GetVirtualDevice().Key).To(Equal(int32(4001)))

		dev1 := matcher(interfaces[1], 1)
		Expect(dev1).ToNot(BeNil())
		Expect(dev1.GetVirtualDevice().Key).To(Equal(int32(4000)))
	})

	It("returns nil for a numbered interface with no device at its slot", func() {
		hwDevs := []vimtypes.BaseVirtualDevice{newVMXNet3DevWithUnit(4000, 8)}
		interfaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0", UnitNumber: ptr.To(int32(9))},
		}
		matcher := networkextraconfig.DefaultNICMatcher(hwDevs, interfaces, true)

		// Exact-only: no device at the declared slot means no match — never
		// a zip onto some other device's slot.
		Expect(matcher(interfaces[0], 0)).To(BeNil())
	})

	It("zips un-numbered interfaces positionally against unclaimed devices", func() {
		hwDevs := []vimtypes.BaseVirtualDevice{
			newVMXNet3DevWithUnit(4000, 8),
			newVMXNet3DevWithUnit(4001, 9),
		}
		interfaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0"},
			{Name: "eth1", UnitNumber: ptr.To(int32(9))},
		}
		matcher := networkextraconfig.DefaultNICMatcher(hwDevs, interfaces, true)

		// The numbered interface claims 4001 by unit, even when consulted
		// out of spec order.
		dev1 := matcher(interfaces[1], 1)
		Expect(dev1).ToNot(BeNil())
		Expect(dev1.GetVirtualDevice().Key).To(Equal(int32(4001)))

		// The un-numbered interface zips onto the remaining unclaimed device.
		dev0 := matcher(interfaces[0], 0)
		Expect(dev0).ToNot(BeNil())
		Expect(dev0.GetVirtualDevice().Key).To(Equal(int32(4000)))
	})

	It("does not let an un-numbered interface steal a numbered interface's device (pre-claim)", func() {
		// Device 4000 sits at the unit eth1 declares; without the pre-claim,
		// the un-numbered eth0 consulted first would zip onto 4000.
		hwDevs := []vimtypes.BaseVirtualDevice{
			newVMXNet3DevWithUnit(4000, 9),
			newVMXNet3DevWithUnit(4001, 8),
		}
		interfaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0"},
			{Name: "eth1", UnitNumber: ptr.To(int32(9))},
		}
		matcher := networkextraconfig.DefaultNICMatcher(hwDevs, interfaces, true)

		dev0 := matcher(interfaces[0], 0)
		Expect(dev0).ToNot(BeNil())
		Expect(dev0.GetVirtualDevice().Key).To(Equal(int32(4001)), "un-numbered zip must skip the pre-claimed device")

		dev1 := matcher(interfaces[1], 1)
		Expect(dev1).ToNot(BeNil())
		Expect(dev1.GetVirtualDevice().Key).To(Equal(int32(4000)))
	})

	It("ignores unit numbers entirely when the flag is off", func() {
		hwDevs := []vimtypes.BaseVirtualDevice{
			newVMXNet3DevWithUnit(4000, 8),
			newVMXNet3DevWithUnit(4001, 9),
		}
		interfaces := []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0", UnitNumber: ptr.To(int32(9))},
			{Name: "eth1"},
		}
		matcher := networkextraconfig.DefaultNICMatcher(hwDevs, interfaces, false)

		// Pure positional zip: eth0 gets device 4000 despite declaring 9.
		dev0 := matcher(interfaces[0], 0)
		Expect(dev0.GetVirtualDevice().Key).To(Equal(int32(4000)))
		dev1 := matcher(interfaces[1], 1)
		Expect(dev1.GetVirtualDevice().Key).To(Equal(int32(4001)))
	})
})

var _ = Describe("findOrCreateDeviceEdit via ReconcileNICFields", func() {
	var (
		vm vmopv1.VirtualMachine
		ci vimtypes.VirtualMachineConfigInfo
	)

	BeforeEach(func() {
		vm = vmopv1.VirtualMachine{}
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOff
		vm.Spec.MemoryAdvanced = &vmopv1.VirtualMachineMemoryAdvancedSpec{ReservationLockedToMax: ptr.To(true)}
		ci = vimtypes.VirtualMachineConfigInfo{Version: "vmx-21"}
	})

	ifaceWithUPTv2 := func() vmopv1.VirtualMachineNetworkInterfaceSpec {
		return vmopv1.VirtualMachineNetworkInterfaceSpec{
			VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{UPTv2Enabled: ptr.To(true)},
		}
	}

	It("does not Edit a device the same ConfigSpec Removes", func() {
		dev := newVMXNet3Dev(4000)
		removed := newVMXNet3Dev(4000)
		cs := &vimtypes.VirtualMachineConfigSpec{
			DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
				&vimtypes.VirtualDeviceConfigSpec{
					Device:    removed,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
				},
			},
		}

		blocked, _ := networkextraconfig.ReconcileNICFields(vm, ifaceWithUPTv2(), dev, ci, cs, false)
		Expect(blocked).To(BeEmpty())
		// No Edit appended for the removed key: Remove + Add + Edit of the
		// same key must not land in one ReconfigVM_Task.
		Expect(cs.DeviceChange).To(HaveLen(1))
		Expect(cs.DeviceChange[0].GetVirtualDeviceConfigSpec().Operation).
			To(Equal(vimtypes.VirtualDeviceConfigSpecOperationRemove))
		// The field was not written onto the device being replaced.
		Expect(dev.Uptv2Enabled).To(BeNil())
	})

	It("reuses an existing Edit entry for the same key (dedupe preserved)", func() {
		dev := newVMXNet3Dev(4000)
		existing := newVMXNet3Dev(4000)
		cs := &vimtypes.VirtualMachineConfigSpec{
			DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
				&vimtypes.VirtualDeviceConfigSpec{
					Device:    existing,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
				},
			},
		}

		_, _ = networkextraconfig.ReconcileNICFields(vm, ifaceWithUPTv2(), dev, ci, cs, false)
		// No second Edit entry; the pre-existing entry's device is mutated.
		Expect(cs.DeviceChange).To(HaveLen(1))
		Expect(existing.Uptv2Enabled).To(Equal(ptr.To(true)))
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
