// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig

import (
	"context"

	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	vmopv1util "github.com/vmware-tanzu/vm-operator/pkg/util/vmopv1"
)

// NICDeviceMatcher returns the hardware device that corresponds to a spec NIC
// entry. Returns nil when no match exists (device not yet provisioned, or
// excluded type). Implementations may be stateful (e.g. positional cursor
// advancing per call).
type NICDeviceMatcher func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, specIdx int) vimtypes.BaseVirtualDevice

// NICMatcherFromContext returns the NICDeviceMatcher for the current pass.
// When the context carries a network.ReconcileState with an observation that
// covers spec.network.interfaces, it returns SharedNICMatcher for that
// observation and the state's plan, if any, so that NIC tuning targets the
// same adapter as status, boot options, and the network planner. Otherwise,
// e.g. for callers outside updateVirtualMachine, it returns
// DefaultNICMatcher.
func NICMatcherFromContext(
	ctx context.Context,
	hwDevs []vimtypes.BaseVirtualDevice) NICDeviceMatcher {

	if st, ok := network.FromContext(ctx); ok && st.Observed != nil && st.Observed.Enabled {
		return SharedNICMatcher(st.Observed, st.Plan, hwDevs)
	}
	return DefaultNICMatcher(hwDevs)
}

// SharedNICMatcher returns a NICDeviceMatcher that maps each spec interface
// through the shared network match in info instead of zipping by position.
// The spec interface is found in info by name. The matcher is stateless.
//
//   - An interface whose match claims an adapter (unit number, record,
//     identity, orphan, or positional) maps to the hardware device with the
//     claimed key. An interface with no claim, or an ambiguous one, maps to
//     nil.
//   - When plan is not nil and adds a new adapter for the interface (Add or
//     Replace), the interface maps to the plan's new device, identified by
//     its temporary key, so its tuning is folded into the Add. A Replace
//     never maps to the adapter it removes.
//   - A claimed adapter that plan removes maps to nil.
//
// SR-IOV interfaces and SR-IOV adapters never take part, as with
// DefaultNICMatcher. The shared match, unlike DefaultNICMatcher, includes
// SR-IOV adapters in its inventory, so a positional claim can pair an
// ethernet interface with an SR-IOV adapter when the spec and hardware orders
// of the two kinds differ. Such an interface maps to nil rather than to
// another ethernet adapter, because every other adapter belongs to a
// different interface's claim; tuning therefore never targets an SR-IOV
// adapter and never tunes one adapter for two interfaces.
func SharedNICMatcher(
	info *network.Info,
	plan *network.Plan,
	hwDevs []vimtypes.BaseVirtualDevice) NICDeviceMatcher {

	byKey := make(map[int32]vimtypes.BaseVirtualDevice, len(hwDevs))
	for _, dev := range hwDevs {
		if dev != nil {
			byKey[dev.GetVirtualDevice().Key] = dev
		}
	}

	var changes *network.DeviceChanges
	if plan != nil {
		changes = plan.DeviceChanges()
	}

	return func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, specIdx int) vimtypes.BaseVirtualDevice {
		if !isEthernetInterfaceType(iface.Type) {
			return nil
		}

		state, ok := interfaceState(info, iface.Name, specIdx)
		if !ok {
			return nil
		}

		if plan != nil {
			if action, ok := plan.InterfaceAction(iface.Name); ok {
				switch action.Kind {
				case network.ActionAdd, network.ActionReplace:
					if action.DeviceKey == 0 {
						return nil
					}
					if dev, ok := changes.Lookup(action.DeviceKey); ok && isEthernetDevice(dev) {
						return dev
					}
					return nil
				case network.ActionNoOp, network.ActionEdit, network.ActionDisconnect,
					network.ActionRemove, network.ActionDeferred:
				}
			}
		}

		if !isClaim(state.Match.Kind) {
			return nil
		}
		key := state.Match.AdapterKey
		if changes != nil && changes.Removed(key) {
			return nil
		}
		dev := byKey[key]
		if !isEthernetDevice(dev) {
			return nil
		}
		return dev
	}
}

// interfaceState returns the observed state of the named spec interface.
// specIdx is tried first because the observation holds the interfaces in
// spec order.
func interfaceState(
	info *network.Info,
	name string,
	specIdx int) (network.InterfaceState, bool) {

	if specIdx >= 0 && specIdx < len(info.Interfaces) &&
		info.Interfaces[specIdx].Spec.Name == name {
		return info.Interfaces[specIdx], true
	}
	for _, s := range info.Interfaces {
		if s.Spec.Name == name {
			return s, true
		}
	}
	return network.InterfaceState{}, false
}

// isClaim reports whether a match of the given kind claims an adapter.
func isClaim(kind network.MatchKind) bool {
	switch kind {
	case network.MatchUnitNumber, network.MatchRecord, network.MatchIdentity,
		network.MatchOrphan, network.MatchPositional:
		return true
	case network.MatchNone, network.MatchAmbiguous:
	}
	return false
}

// DefaultNICMatcher returns a NICDeviceMatcher that positionally zips each
// managed ethernet spec interface to the corresponding ethernet device in the
// hardware list. SR-IOV and other non-VMX-namespace adapters are excluded from
// both sides. It is used only when no shared network match is available; see
// NICMatcherFromContext.
//
// The device list is filtered once (O(n) per Reconcile call), not on every match.
//
// TODO: Replace positional zip with identity-based matching once device-order
// semantics are fully understood.
func DefaultNICMatcher(hwDevs []vimtypes.BaseVirtualDevice) NICDeviceMatcher {
	managed := collectManagedEthernetDevices(hwDevs)

	pos := 0
	return func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, _ int) vimtypes.BaseVirtualDevice {
		if !isEthernetInterfaceType(iface.Type) {
			return nil
		}
		if pos >= len(managed) {
			return nil
		}
		dev := managed[pos]
		pos++
		return dev
	}
}

// EthernetDeviceIndex returns the ethernetN namespace index for an ethernet
// device (N = deviceKey - EthernetDeviceKeyBase). Returns (0, false) for
// non-ethernet devices and for a device that is being added and so has only
// a temporary key (see IsAddedDevice).
func EthernetDeviceIndex(dev vimtypes.BaseVirtualDevice) (int32, bool) {
	if !isEthernetDevice(dev) {
		return 0, false
	}
	if IsAddedDevice(dev) {
		return 0, false
	}
	return dev.GetVirtualDevice().Key - vmopv1util.EthernetDeviceKeyBase, true
}

// IsAddedDevice reports whether dev is an ethernet card that the current
// reconfigure adds. Such a card has a temporary, negative key, so it has no
// ethernetN ExtraConfig namespace until vSphere assigns its real key; only
// its device-spec tuning fields can be applied in the same reconfigure.
func IsAddedDevice(dev vimtypes.BaseVirtualDevice) bool {
	return isEthernetDevice(dev) && dev.GetVirtualDevice().Key < 0
}

// collectManagedEthernetDevices returns all managed ethernet devices from the
// hardware list — those where isEthernetDevice returns true.
func collectManagedEthernetDevices(hwDevs []vimtypes.BaseVirtualDevice) []vimtypes.BaseVirtualDevice {
	var out []vimtypes.BaseVirtualDevice
	for _, dev := range hwDevs {
		if isEthernetDevice(dev) {
			out = append(out, dev)
		}
	}
	return out
}

// isEthernetDevice reports whether dev is a VirtualEthernetCard that
// participates in the ethernetX VMX namespace. SR-IOV adapters implement
// BaseVirtualEthernetCard but use a separate vSphere mechanism and are excluded.
func isEthernetDevice(dev vimtypes.BaseVirtualDevice) bool {
	if dev == nil {
		return false
	}
	if _, ok := dev.(vimtypes.BaseVirtualEthernetCard); !ok {
		return false
	}
	_, isSRIOV := dev.(*vimtypes.VirtualSriovEthernetCard)
	return !isSRIOV
}

// isEthernetInterfaceType reports whether a spec interface type participates
// in the ethernetX VMX namespace. SR-IOV interfaces use a separate mechanism
// and are excluded.
func isEthernetInterfaceType(t vmopv1.VirtualMachineNetworkInterfaceType) bool {
	switch t {
	case vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV:
		return false
	default:
		return true
	}
}
