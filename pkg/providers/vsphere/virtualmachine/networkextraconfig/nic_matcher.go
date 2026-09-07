// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package networkextraconfig

import (
	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1util "github.com/vmware-tanzu/vm-operator/pkg/util/vmopv1"
)

// NICDeviceMatcher returns the hardware device that corresponds to a spec NIC
// entry. Returns nil when no match exists (device not yet provisioned, or
// excluded type). Implementations may be stateful (e.g. positional cursor
// advancing per call).
type NICDeviceMatcher func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, specIdx int) vimtypes.BaseVirtualDevice

// DefaultNICMatcher returns a NICDeviceMatcher that pairs each managed
// ethernet spec interface to the corresponding ethernet device in the
// hardware list. SR-IOV and other non-VMX-namespace adapters are excluded
// from both sides.
//
// With unitNumbersEnabled, a spec interface carrying a unit number resolves
// by an exact unit lookup over the managed devices — the unit number is the
// interface's identity for its device, so a miss returns nil and NEVER falls
// through to the positional zip (the same exact-only rule as the reconcile
// path's unit-number matching; both callers already skip on nil). The
// devices of numbered interfaces are pre-claimed at construction, so an
// un-numbered interface consulted before a numbered one cannot zip onto a
// device a numbered interface declares as its identity (two-pass claim
// order, as in the reconcile path). Un-numbered interfaces zip positionally
// against the remaining unclaimed devices, in call order.
//
// With the flag disabled the matcher is the historical positional zip,
// byte-for-byte: unit numbers present on devices or interfaces are ignored.
func DefaultNICMatcher(
	hwDevs []vimtypes.BaseVirtualDevice,
	interfaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
	unitNumbersEnabled bool) NICDeviceMatcher {

	managed := collectManagedEthernetDevices(hwDevs)

	pos := 0

	if !unitNumbersEnabled {
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

	// reserved marks devices that must never be handed to the positional
	// zip (numbered interfaces' devices, and devices already zipped).
	reserved := make([]bool, len(managed))
	// handed marks numbered units already matched, so a duplicate declared
	// unit (webhook-forbidden) cannot match the same device twice.
	handed := make(map[int32]bool)

	unitToIdx := make(map[int32]int, len(managed))
	for i, dev := range managed {
		if u := dev.GetVirtualDevice().UnitNumber; u != nil {
			unitToIdx[*u] = i
		}
	}

	// Pre-claim the devices of numbered interfaces so the positional cursor
	// for un-numbered interfaces can never hand out a device a numbered
	// interface declares as its identity. A numbered interface whose unit
	// has no device claims nothing: it resolves to nil at call time, never
	// to a zipped stranger.
	for i := range interfaces {
		if u := interfaces[i].UnitNumber; u != nil {
			if j, ok := unitToIdx[*u]; ok {
				reserved[j] = true
			}
		}
	}

	return func(iface vmopv1.VirtualMachineNetworkInterfaceSpec, _ int) vimtypes.BaseVirtualDevice {
		if !isEthernetInterfaceType(iface.Type) {
			return nil
		}

		if iface.UnitNumber != nil {
			// Exact-only: a declared unit number identifies specific hardware
			// or nothing at all.
			u := *iface.UnitNumber
			if !handed[u] {
				if j, ok := unitToIdx[u]; ok && reserved[j] {
					handed[u] = true
					return managed[j]
				}
			}
			return nil
		}

		for pos < len(managed) && reserved[pos] {
			pos++
		}
		if pos >= len(managed) {
			return nil
		}
		dev := managed[pos]
		reserved[pos] = true
		pos++
		return dev
	}
}

// EthernetDeviceIndex returns the ethernetN namespace index for an ethernet
// device (N = deviceKey - EthernetDeviceKeyBase). Returns (0, false) for
// non-ethernet devices.
func EthernetDeviceIndex(dev vimtypes.BaseVirtualDevice) (int32, bool) {
	if !isEthernetDevice(dev) {
		return 0, false
	}
	return dev.GetVirtualDevice().Key - vmopv1util.EthernetDeviceKeyBase, true
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
