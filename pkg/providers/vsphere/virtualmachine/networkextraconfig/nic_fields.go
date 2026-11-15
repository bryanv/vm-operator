// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig

import (
	"fmt"

	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	vmopv1util "github.com/vmware-tanzu/vm-operator/pkg/util/vmopv1"
)

// nicFieldDef describes how to observe and reconcile a single NIC-level
// device-spec field (e.g. Uptv2Enabled, NumaNode) via ConfigSpec.DeviceChange.
type nicFieldDef struct {
	// fieldName is the spec path shown in condition messages.
	fieldName string
	// minHWVer is the minimum hardware version required; 0 means no gate.
	minHWVer vimtypes.HardwareVersion
	// prerequisite, if non-nil, checks runtime conditions (EFI firmware,
	// vNUMA topology) beyond the hardware version. Called after differs=true
	// and after the hwVer gate passes. Returns (true, reason) when blocked.
	prerequisite func(
		vm vmopv1.VirtualMachine,
		iface vmopv1.VirtualMachineNetworkInterfaceSpec,
		dev vimtypes.BaseVirtualDevice,
		ci vimtypes.VirtualMachineConfigInfo,
	) (blocked bool, reason string)
	// hotPluggable reports whether the field can be applied while powered on.
	hotPluggable func(
		vm vmopv1.VirtualMachine,
		iface vmopv1.VirtualMachineNetworkInterfaceSpec,
		dev vimtypes.BaseVirtualDevice,
		ci vimtypes.VirtualMachineConfigInfo,
	) bool
	// differs returns true when a DeviceChange Edit is needed for this field.
	differs func(
		vm vmopv1.VirtualMachine,
		iface vmopv1.VirtualMachineNetworkInterfaceSpec,
		dev vimtypes.BaseVirtualDevice,
		ci vimtypes.VirtualMachineConfigInfo,
		cs *vimtypes.VirtualMachineConfigSpec,
	) bool
	// apply writes the desired value to mutable, the single device in the
	// ConfigSpec's device changes that edits or adds the matched device (see
	// findOrCreateDeviceEdit).
	apply func(
		iface vmopv1.VirtualMachineNetworkInterfaceSpec,
		mutable vimtypes.BaseVirtualDevice,
	)
}

// nicFields is the ordered list of NIC device-spec fields managed by
// this reconciler. Adding a new field requires only a new entry here.
var nicFields = []nicFieldDef{
	{
		fieldName: "interfaces[i].vmxnet3.uptv2Enabled",
		minHWVer:  vimtypes.VMX20,
		// UPTv2 requires full memory reservation (reservationLockedToMax or
		// explicit requests.memory == size.memory).  Only block when the
		// desired value is true — disabling UPTv2 never requires reservation.
		prerequisite: func(vm vmopv1.VirtualMachine, iface vmopv1.VirtualMachineNetworkInterfaceSpec, _ vimtypes.BaseVirtualDevice, _ vimtypes.VirtualMachineConfigInfo) (bool, string) {
			if !desiredUPTv2Enabled(iface) {
				return false, ""
			}
			if vmopv1util.FullMemReservationSpecMet(&vm) {
				return false, ""
			}
			return true, "full memory reservation required: " +
				"set spec.memoryAdvanced.reservationLockedToMax=true, " +
				"or set spec.resources.requests.memory equal to spec.resources.size.memory"
		},
		hotPluggable: func(_ vmopv1.VirtualMachine, _ vmopv1.VirtualMachineNetworkInterfaceSpec, _ vimtypes.BaseVirtualDevice, _ vimtypes.VirtualMachineConfigInfo) bool {
			return true
		},
		differs: func(_ vmopv1.VirtualMachine, iface vmopv1.VirtualMachineNetworkInterfaceSpec, dev vimtypes.BaseVirtualDevice, _ vimtypes.VirtualMachineConfigInfo, _ *vimtypes.VirtualMachineConfigSpec) bool {
			vmxnet3, ok := dev.(*vimtypes.VirtualVmxnet3)
			if !ok {
				return false
			}
			return ptr.Deref(vmxnet3.Uptv2Enabled) != desiredUPTv2Enabled(iface)
		},
		apply: func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, mutable vimtypes.BaseVirtualDevice) {
			vmxnet3, ok := mutable.(*vimtypes.VirtualVmxnet3)
			if !ok {
				return
			}
			v := desiredUPTv2Enabled(iface)
			vmxnet3.Uptv2Enabled = &v
		},
	},
	{
		fieldName: "interfaces[i].vnumaNodeID",
		minHWVer:  vimtypes.VMX20,
		hotPluggable: func(_ vmopv1.VirtualMachine, _ vmopv1.VirtualMachineNetworkInterfaceSpec, _ vimtypes.BaseVirtualDevice, _ vimtypes.VirtualMachineConfigInfo) bool {
			return false
		},
		prerequisite: func(vm vmopv1.VirtualMachine, iface vmopv1.VirtualMachineNetworkInterfaceSpec, _ vimtypes.BaseVirtualDevice, ci vimtypes.VirtualMachineConfigInfo) (bool, string) {
			if iface.VNUMANodeID == nil {
				return false, ""
			}
			if !vmopv1util.FirmwareIsEFI(&vm, ci) {
				return true, "EFI firmware required"
			}
			if vmopv1util.GetVNUMANodeCount(&vm) == 0 {
				return true, "vNUMA topology required (spec.cpuAdvanced.topology.vnumaNodeCount must be > 0)"
			}
			return false, ""
		},
		differs: func(_ vmopv1.VirtualMachine, iface vmopv1.VirtualMachineNetworkInterfaceSpec, dev vimtypes.BaseVirtualDevice, _ vimtypes.VirtualMachineConfigInfo, _ *vimtypes.VirtualMachineConfigSpec) bool {
			desired := desiredVNUMANodeID(iface)
			current := dev.GetVirtualDevice().NumaNode
			if *desired < 0 && (current == nil || *current < 0) {
				return false
			}
			if current == nil {
				return true
			}
			return *current != *desired
		},
		apply: func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, mutable vimtypes.BaseVirtualDevice) {
			mutable.GetVirtualDevice().NumaNode = desiredVNUMANodeID(iface)
		},
	},
}

// ReconcileNICFields evaluates all nicFields entries for a single spec
// interface / hardware device pair. When dryRun is false, it applies changes
// to cs (the normal Reconcile path). When dryRun is true, it never calls
// apply — cs is only used to look up a pre-existing DeviceChange entry, and
// dev (and the moVM it's aliased from) is never mutated — for status/condition
// computation, which must not stamp not-yet-confirmed values onto vmCtx.MoVM.
// Returns the names of blocked (prerequisite failure) and blockedPowerOff
// (hot-plug not possible) fields either way.
//
// It is ReconcileNICFieldsWithChanges without a network plan.
func ReconcileNICFields(
	vm vmopv1.VirtualMachine,
	iface vmopv1.VirtualMachineNetworkInterfaceSpec,
	dev vimtypes.BaseVirtualDevice,
	ci vimtypes.VirtualMachineConfigInfo,
	cs *vimtypes.VirtualMachineConfigSpec,
	dryRun bool,
) (blocked, blockedPowerOff []string) {
	return ReconcileNICFieldsWithChanges(vm, iface, dev, ci, cs, nil, dryRun)
}

// ReconcileNICFieldsWithChanges is ReconcileNICFields for a pass whose
// network plan owns changes, the network device changes of the same
// reconfigure. When dryRun is false and changes is not nil, the fields are
// applied to the plan's copy of dev, or to the new device the plan adds when
// dev is that device, rather than to a second Edit of dev; a device the plan
// removes is not edited; and dev itself is never mutated. See
// findOrCreateDeviceEdit.
func ReconcileNICFieldsWithChanges(
	vm vmopv1.VirtualMachine,
	iface vmopv1.VirtualMachineNetworkInterfaceSpec,
	dev vimtypes.BaseVirtualDevice,
	ci vimtypes.VirtualMachineConfigInfo,
	cs *vimtypes.VirtualMachineConfigSpec,
	changes *network.DeviceChanges,
	dryRun bool,
) (blocked, blockedPowerOff []string) {
	hwVer, _ := vimtypes.ParseHardwareVersion(ci.Version)
	poweredOn := vm.Status.PowerState == vmopv1.VirtualMachinePowerStateOn

	var (
		mutable  vimtypes.BaseVirtualDevice
		resolved bool
	)

	for _, f := range nicFields {
		// Skip early if current state already matches desired.
		if !f.differs(vm, iface, dev, ci, cs) {
			continue
		}

		// Hardware version gate.
		if f.minHWVer > 0 && hwVer < f.minHWVer {
			blocked = append(blocked,
				fmt.Sprintf("%s (requires hwVer >= %d)", f.fieldName, f.minHWVer))
			continue
		}

		// Runtime prerequisite check (EFI, vNUMA topology, etc.).
		if f.prerequisite != nil {
			if prereqBlocked, reason := f.prerequisite(vm, iface, dev, ci); prereqBlocked {
				blocked = append(blocked, fmt.Sprintf("%s (%s)", f.fieldName, reason))
				continue
			}
		}

		if poweredOn && !f.hotPluggable(vm, iface, dev, ci) {
			blockedPowerOff = append(blockedPowerOff, f.fieldName)
		} else if !dryRun {
			if !resolved {
				mutable = findOrCreateDeviceEdit(cs, changes, dev)
				resolved = true
			}
			if mutable != nil {
				f.apply(iface, mutable)
			}
		}
	}
	return blocked, blockedPowerOff
}

// desiredUPTv2Enabled returns the desired UPTv2 enablement: nil spec → false.
func desiredUPTv2Enabled(iface vmopv1.VirtualMachineNetworkInterfaceSpec) bool {
	if iface.VMXNet3 == nil || iface.VMXNet3.UPTv2Enabled == nil {
		return false
	}
	return *iface.VMXNet3.UPTv2Enabled
}

// desiredVNUMANodeID maps the spec pointer to the vSphere wire value.
//
// VirtualDevice.NumaNode wire semantics (*int32):
//
//	nil      → field absent from wire → vSphere status-quo (no change)
//	ptr(-1)  → clears affinity        → ConfigInfo omits field → Go reads nil
//	ptr(0)   → assigns NUMA node 0
//	ptr(N>0) → assigns NUMA node N
//
// To clear an existing NUMA assignment send ptr(-1). VNUMANodeID == nil means
// no assignment desired, so we send ptr(-1) to clear. Values >= 0 are sent
// as-is.
func desiredVNUMANodeID(iface vmopv1.VirtualMachineNetworkInterfaceSpec) *int32 {
	if iface.VNUMANodeID == nil {
		return ptr.To(int32(-1))
	}
	return iface.VNUMANodeID
}

// findOrCreateDeviceEdit returns the single mutable device through which dev
// is edited in this reconfigure, so that cs carries at most one Edit per
// device key regardless of how many fields need updating or which other
// contributor edited the device first. It returns nil when dev must not be
// edited. In order:
//
//  1. A device that changes removes, or that cs already removes, is not
//     edited, so cs never carries both an Edit and a Remove for one key.
//  2. An Edit or Add of the same key already in cs is reused. With a plan,
//     this is the plan's own copy, which addNetworkChanges put in cs.
//  3. With a plan, its copy of the device (changes.Lookup) is reused; it is
//     the device the plan's Edit or Add carries.
//  4. Otherwise a new Edit is appended to cs. With a plan, the new Edit
//     carries a deep copy of dev, so the observed device is never mutated.
//     Without a plan, dev itself is appended, as before the network planner
//     existed.
//
// The new Edit is appended to cs rather than recorded in changes: changes
// holds only the plan's own network changes, which is what decides whether
// the reconfigure is guarded by the VM's ChangeVersion.
func findOrCreateDeviceEdit(
	cs *vimtypes.VirtualMachineConfigSpec,
	changes *network.DeviceChanges,
	dev vimtypes.BaseVirtualDevice) vimtypes.BaseVirtualDevice {

	key := dev.GetVirtualDevice().Key
	if changes != nil && changes.Removed(key) {
		return nil
	}

	var existing vimtypes.BaseVirtualDevice
	for _, dc := range cs.DeviceChange {
		if dc == nil {
			continue
		}
		spec := dc.GetVirtualDeviceConfigSpec()
		if spec == nil || spec.Device == nil || spec.Device.GetVirtualDevice().Key != key {
			continue
		}
		switch spec.Operation {
		case vimtypes.VirtualDeviceConfigSpecOperationRemove:
			return nil
		case vimtypes.VirtualDeviceConfigSpecOperationEdit, vimtypes.VirtualDeviceConfigSpecOperationAdd:
			if existing == nil {
				existing = spec.Device
			}
		}
	}
	if existing != nil {
		return existing
	}

	if changes == nil {
		cs.DeviceChange = append(cs.DeviceChange, &vimtypes.VirtualDeviceConfigSpec{
			Device:    dev,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
		})
		return dev
	}

	if planned, ok := changes.Lookup(key); ok {
		return planned
	}

	// A device with a temporary key is one that is being added, and only its
	// Add may carry it.
	if key < 0 {
		return nil
	}

	mutable, err := copyDevice(dev)
	if err != nil {
		return nil
	}
	cs.DeviceChange = append(cs.DeviceChange, &vimtypes.VirtualDeviceConfigSpec{
		Device:    mutable,
		Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
	})
	return mutable
}

// copyDevice returns a deep copy of a device that keeps its concrete type.
// The device is wrapped in a device config spec because the copy facility
// round-trips its argument through JSON, which records the concrete type of
// an interface-typed field but not of the root value.
func copyDevice(dev vimtypes.BaseVirtualDevice) (vimtypes.BaseVirtualDevice, error) {
	spec, err := vimtypes.DeepCopy(vimtypes.VirtualDeviceConfigSpec{Device: dev})
	if err != nil {
		return nil, fmt.Errorf("failed to copy device %d: %w", dev.GetVirtualDevice().Key, err)
	}
	if spec.Device == nil {
		return nil, fmt.Errorf("failed to copy device %d", dev.GetVirtualDevice().Key)
	}
	return spec.Device, nil
}
