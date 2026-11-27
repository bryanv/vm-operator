// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package backfill

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkglog "github.com/vmware-tanzu/vm-operator/pkg/log"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	pkgrecord "github.com/vmware-tanzu/vm-operator/pkg/record"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	vmopv1util "github.com/vmware-tanzu/vm-operator/pkg/util/vmopv1"
)

// NICConfigFromMoVM populates per-NIC spec fields from the live vSphere
// VM configuration during schema upgrade. For each (spec interface,
// ethernet device) pair:
//
//   - spec.network.interfaces[i].type: set from the device type when empty,
//     defaulting to VMXNet3 for unrecognised device types.
//   - spec.network.interfaces[i].vmxnet3.*: backfilled from moVM.Config.ExtraConfig
//     using the device-key-derived ethernetX prefix.
//   - spec.network.interfaces[i].vnumaNodeID: filled from VirtualDevice.NumaNode.
//   - spec.network.interfaces[i].vmxnet3.UPTv2Enabled: filled from
//     VirtualVmxnet3.Uptv2Enabled.
//
// With unitNumbersEnabled false, interfaces are paired positionally with the
// ethernet devices and any unit numbers are ignored. Otherwise they are
// paired in two passes:
//
//  0. A numbered interface (see vmopv1util.NICInterfaceUnitNumber; an
//     SRIOV-typed interface counts as un-numbered) is matched by its unit
//     number to the unclaimed device at exactly that slot
//     (claimByUnitNumber, the same pass 0 NICUnitNumbersFromMoVM runs).
//
//  1. The remaining interfaces are zipped, in spec order, against the
//     devices pass 0 left unclaimed. An un-numbered interface takes the next
//     unclaimed device. How a numbered interface that missed its slot takes
//     part depends on whether every remaining interface can have a device
//     (at least as many unclaimed devices as remaining interfaces).
//
//     If every remaining interface can have a device, the numbered miss
//     holds its positional slot. It is paired with the device there only if
//     that is an SR-IOV card: SR-IOV cards sit outside the
//     NICUnitNumberFirst..NICUnitNumberMax band, so a numbered interface
//     backed by one always misses in pass 0 (for example a unit number
//     pinned at create on an interface mapped to a VM class SR-IOV NIC).
//     Any other device there is consumed but not paired: it is most likely
//     the interface's old device, awaiting a power-off to move to the
//     declared slot, and must not be handed to a later interface.
//
//     Otherwise some numbered interface has no device yet (added while the
//     VM was powered on), and which one cannot be told positionally. The
//     numbered misses then sit out the zip entirely, so they cannot take a
//     later interface's device.
//
//     For an all-un-numbered VM this is the previous all-positional pairing.
//
// A paired interface gets the full backfill above. An un-numbered interface
// left unpaired (no unclaimed device left) only has its Type defaulted to
// VMXNet3 when unset. A numbered interface left unpaired is not modified:
// its device is unknown, so defaulting its Type could record the wrong
// device model permanently.
//
// Spec wins: only nil/zero fields are written.
// Returns true if any field was mutated.
func NICConfigFromMoVM(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine,
	unitNumbersEnabled bool) bool {

	if moVM.Config == nil {
		return false
	}
	if vm.Spec.Network == nil || len(vm.Spec.Network.Interfaces) == 0 {
		return false
	}

	ethDevs := collectEthernetDevicesFromMoVM(moVM)
	mutated := false

	ifaces := vm.Spec.Network.Interfaces
	isNumbered := func(i int) bool {
		return vmopv1util.NICInterfaceUnitNumber(&ifaces[i], unitNumbersEnabled) != nil
	}

	// Pass 0: numbered interfaces claim their exact device by unit number.
	claimed := make([]bool, len(ethDevs))
	devIdx := claimByUnitNumber(ifaces, ethDevs, claimed, unitNumbersEnabled)

	// Pass 1: the remaining interfaces zip, in spec order, against the
	// devices pass 0 left unclaimed. Numbered misses hold their positional
	// slot only when every remaining interface can have a device.
	remainingIfaces, remainingDevs := 0, 0
	for i := range ifaces {
		if devIdx[i] < 0 {
			remainingIfaces++
		}
	}
	for j := range ethDevs {
		if !claimed[j] {
			remainingDevs++
		}
	}
	numberedHoldSlot := remainingDevs >= remainingIfaces

	next := 0
	for i := range ifaces {
		if devIdx[i] >= 0 {
			continue
		}
		numbered := isNumbered(i)
		if numbered && !numberedHoldSlot {
			continue
		}
		for next < len(ethDevs) && claimed[next] {
			next++
		}
		if next >= len(ethDevs) {
			break
		}
		claimed[next] = true
		if numbered {
			if _, ok := ethDevs[next].(*vimtypes.VirtualSriovEthernetCard); !ok {
				// Consumed, not paired: see the doc comment.
				continue
			}
		}
		devIdx[i] = next
	}

	for i := range ifaces {
		iface := &ifaces[i]

		if devIdx[i] < 0 {
			// No matching hardware device. Default Type to VMXNet3 if unset,
			// but only for an un-numbered interface: a numbered one's device
			// is unknown (see above).
			if iface.Type == "" && !isNumbered(i) {
				iface.Type = vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3
				mutated = true
			}
			continue
		}

		dev := ethDevs[devIdx[i]]

		if iface.Type == "" {
			t := mapVimEthernetToNetworkInterfaceType(dev)
			if t == "" {
				t = vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3
			}
			iface.Type = t
			mutated = true
		}

		if backfillNICSpec(
			ctx,
			vmopv1util.EthernetExtraConfigPrefix(dev.GetVirtualDevice().Key),
			iface,
			moVM.Config.ExtraConfig) {
			mutated = true
		}

		if backfillVNUMANodeID(iface, dev) {
			mutated = true
		}
		if backfillUPTv2Enabled(iface, dev) {
			mutated = true
		}
	}

	return mutated
}

func mapVimEthernetToNetworkInterfaceType(
	dev vimtypes.BaseVirtualDevice) vmopv1.VirtualMachineNetworkInterfaceType {

	switch dev.(type) {
	case *vimtypes.VirtualVmxnet3:
		return vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3
	case *vimtypes.VirtualSriovEthernetCard:
		return vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV
	case *vimtypes.VirtualE1000:
		return vmopv1.VirtualMachineNetworkInterfaceTypeE1000
	case *vimtypes.VirtualE1000e:
		return vmopv1.VirtualMachineNetworkInterfaceTypeE1000e
	case *vimtypes.VirtualVmxnet2:
		return vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet2
	case *vimtypes.VirtualPCNet32:
		return vmopv1.VirtualMachineNetworkInterfaceTypePCNet32
	default:
		return ""
	}
}

// backfillNICSpec backfills vmxnet3.* spec fields from ExtraConfig using the
// given prefix (e.g. "ethernet0."). Only applies to VMXNet3 NICs.
func backfillNICSpec(
	ctx context.Context,
	prefix string,
	iface *vmopv1.VirtualMachineNetworkInterfaceSpec,
	extraConfig []vimtypes.BaseOptionValue) bool {

	// Only backfill vmxnet3 fields for VMXNet3 NICs.
	if iface.Type != vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3 {
		return false
	}
	log := pkglog.FromContextOrDefault(ctx)
	mutated := false

	for _, bov := range extraConfig {
		ov, ok := bov.(*vimtypes.OptionValue)
		if !ok {
			continue
		}

		if !strings.HasPrefix(ov.Key, prefix) {
			continue
		}

		raw, ok := ov.Value.(string)
		if !ok {
			continue
		}

		fieldIdx, exists := vmopv1util.VMXNet3NICKeyMap()[vmopv1util.NormalizeEthernetDeviceKey(ov.Key)]
		if !exists {
			continue
		}

		// Spec wins: skip if the field is already non-zero.
		if iface.VMXNet3 != nil {
			rv := reflect.ValueOf(iface.VMXNet3).Elem().Field(fieldIdx)
			if !rv.IsZero() {
				continue
			}
		}

		// Decode into a nicSpec struct to avoid initialising iface.VMXNet3
		// prematurely. If the raw value is a host-default sentinel (auto,
		// default, dontcare) the decoded field stays zero and we skip without
		// touching spec — preserving the nil=auto convention for *bool fields.
		var nicSpec vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec
		nicSpecFieldValue := reflect.ValueOf(&nicSpec).Elem().Field(fieldIdx)
		if err := vmopv1util.DecodeVMXFieldValue(ctx, nicSpecFieldValue, raw); err != nil {
			log.V(1).Error(err, "cannot decode vmx nic field; skipping", "key", ov.Key)
			continue
		}
		if nicSpecFieldValue.IsZero() {
			continue
		}

		if iface.VMXNet3 == nil {
			iface.VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{}
		}
		reflect.ValueOf(iface.VMXNet3).Elem().Field(fieldIdx).Set(nicSpecFieldValue)
		mutated = true
	}
	return mutated
}

// backfillVNUMANodeID populates iface.VNUMANodeID from dev.NumaNode when the
// device reports a NUMA node assignment (>= 0) and the spec field is nil.
func backfillVNUMANodeID(
	iface *vmopv1.VirtualMachineNetworkInterfaceSpec,
	dev vimtypes.BaseVirtualDevice) bool {

	if iface.VNUMANodeID != nil {
		return false // spec wins
	}

	numaNode := dev.GetVirtualDevice().NumaNode
	if numaNode == nil || *numaNode < 0 {
		// nil → unset (no NUMA assignment, or cleared and read back as absent).
		// Negative → explicitly no affinity.
		return false
	}

	iface.VNUMANodeID = numaNode
	return true
}

// backfillUPTv2Enabled populates iface.vmxnet3.UPTv2Enabled from
// VirtualVmxnet3.Uptv2Enabled when the device has it set and the spec
// field is nil. Only applies to VMXNet3 (or type-unset) interfaces.
func backfillUPTv2Enabled(
	iface *vmopv1.VirtualMachineNetworkInterfaceSpec,
	dev vimtypes.BaseVirtualDevice) bool {

	vmxnet3Dev, ok := dev.(*vimtypes.VirtualVmxnet3)
	if !ok || vmxnet3Dev.Uptv2Enabled == nil {
		return false
	}

	// UPTv2 is a VMXNet3-only feature.
	if iface.Type != "" &&
		iface.Type != vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3 {
		return false
	}

	if iface.VMXNet3 != nil && iface.VMXNet3.UPTv2Enabled != nil {
		return false // spec wins
	}

	if iface.VMXNet3 == nil {
		iface.VMXNet3 = &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{}
	}

	v := *vmxnet3Dev.Uptv2Enabled
	iface.VMXNet3.UPTv2Enabled = &v
	return true
}

// collectEthernetDevicesFromMoVM returns the ethernet devices from moVM in
// the order they appear in Config.Hardware.Device.
func collectEthernetDevicesFromMoVM(
	moVM mo.VirtualMachine) []vimtypes.BaseVirtualDevice {

	if moVM.Config == nil {
		return nil
	}

	devs := make([]vimtypes.BaseVirtualDevice, 0,
		len(moVM.Config.Hardware.Device))

	for _, dev := range moVM.Config.Hardware.Device {
		if pkgutil.IsEthernetCard(dev) {
			devs = append(devs, dev)
		}
	}
	return devs
}

// NICUnitNumberBackfillAmbiguous is the warning-event reason emitted when an
// interface's observed unit number was recorded by positional matching rather
// than a unique match.
const NICUnitNumberBackfillAmbiguous = "NICUnitNumberBackfillAmbiguous"

// NICUnitNumbersFromMoVM records each spec.network.interfaces entry's observed
// PCI unit number from the VM's live vSphere ethernet devices during schema
// upgrade. Once recorded, the unit number is the interface's identifier for
// its hardware, so the value recorded here must be the one vSphere actually
// observed. Spec wins: an interface that already carries a unit number is
// never overwritten, including when the observed device sits at a different
// slot — that disagreement is reported by the steady-state hardware condition,
// not by this one-shot backfill.
//
// It also records type SRIOV on an untyped interface that is uniquely matched
// to an SR-IOV card (see SR-IOV detection under step 1). SR-IOV cards sit outside the unit-number
// band, so such an interface gets no unit number, and the type is what keeps
// it that way: the mutation webhook does not number SRIOV-typed interfaces,
// and reconcile-time matching treats them as un-numbered even when they
// carry a unit number recorded before their type was known. Without it, the
// webhook would number an SR-IOV interface whose type is unset, and its
// declared unit would never match its card. SRIOV-typed interfaces never
// have a unit number recorded.
//
// Interfaces are matched to devices in these steps:
//
//  0. A numbered interface (spec unit number set) is matched only by its unit
//     number, to the unclaimed device at exactly that slot. If no device sits
//     there, the interface is not matched and does not fall back to hodgepodge
//     matching. The device is claimed either way, so the zip below cannot
//     hand it to a different interface.
//
//  1. Hodgepodge matching for unnumbered interfaces, via the network package's
//     provider-dispatched matcher (network.FindMatchingEthCardForInterfaceSpec),
//     the same MAC / ExternalID / backing criteria the reconcile-time matching
//     uses (FindMatchingEthCard / MapEthernetDevicesToSpecIdx). A returned index
//     is a unique, provider-approved match among the unclaimed devices. When the
//     client is nil, this step is skipped entirely. SRIOV-typed interfaces
//     are skipped: they never have a unit number recorded.
//
//     Step 1 is followed by SR-IOV detection (recordSRIOVTypes), for each
//     untyped interface still unmatched (a numbered interface that missed
//     its slot, or an un-numbered interface step 1 did not match). The same provider-dispatched matcher runs over
//     every unclaimed ethernet device, SR-IOV cards included. Only if its
//     match is an SR-IOV card AND no other unclaimed device also matches is
//     type SRIOV recorded and the card claimed. The uniqueness check matters
//     because the write is one-shot and permanent, and the Named provider
//     matches by backing alone: two same-network candidates record nothing.
//     SRIOV-typed interfaces claim their SR-IOV card first, so that card is
//     not a candidate for an untyped interface. When the client is nil,
//     this step is skipped.
//
//  2. A positional zip of the remaining unmatched spec interfaces against
//     the remaining unclaimed devices (with an observed unit number), as a
//     strictly last resort so every interface receives its observed unit
//     number. A single warning event (NICUnitNumberBackfillAmbiguous) names
//     every interface whose value came from the zip: nothing in the
//     resulting spec distinguishes a zipped value from a matched one, yet a
//     wrong zip assignment is one-shot and feeds unit-number-first reconcile
//     matching, status, and boot-order device selection. No other case
//     emits an event here.
//
// Per interface, the write is skipped (the unit number stays nil) when the
// observed value would make the spec inadmissible (G7): already claimed by
// another interface's spec value or by a value recorded earlier in this same
// pass, or — defensively, since ethernet cards are allocated units 7-16 by
// the platform — outside that range. Note what the skip does and does not
// buy: it keeps the spec admissible, but it does not leave the interface
// un-numbered — the mutation webhook runs on this very patch and assigns the
// interface a free slot, exactly as AddControllersForVolumes numbers a volume
// the disk backfill skipped (and that invented slot is acted on under the
// unit-number identity model). This is accepted; see the spec plan's
// "Consistency with the disk placement model".
//
// The backfill is driven by the spec's interface list and runs even when
// spec.network.disabled is true (the devices may still exist in vSphere); a
// VM with no interfaces records nothing.
//
// Returns true if any spec field was mutated.
func NICUnitNumbersFromMoVM(
	ctx context.Context,
	client ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine) bool {

	if moVM.Config == nil {
		return false
	}
	if vm.Spec.Network == nil || len(vm.Spec.Network.Interfaces) == 0 {
		return false
	}

	// The ethernet device list the provider-dispatched matcher consumes; its
	// indices are the ones claimedDevs tracks. SR-IOV cards are excluded:
	// they sit outside the NICUnitNumberFirst..NICUnitNumberMax range, so no
	// unit number is recorded for them and they must not claim a device in
	// pass 1 or a slot in the zip. A single list guarantees index alignment
	// between pass 1 and the zip.
	ethDevs := object.VirtualDeviceList(moVM.Config.Hardware.Device).
		Select(func(dev vimtypes.BaseVirtualDevice) bool {
			if _, ok := dev.(*vimtypes.VirtualSriovEthernetCard); ok {
				return false
			}
			return pkgutil.IsEthernetCard(dev)
		})
	// SR-IOV cards are kept in their own list, used only by SR-IOV detection.
	sriovDevs := object.VirtualDeviceList(moVM.Config.Hardware.Device).
		Select(func(dev vimtypes.BaseVirtualDevice) bool {
			_, ok := dev.(*vimtypes.VirtualSriovEthernetCard)
			return ok
		})
	if len(ethDevs) == 0 && len(sriovDevs) == 0 {
		return false
	}

	// G7 admissibility guard: unit numbers already claimed in the spec, plus
	// any recorded by this same pass, are unavailable for recording.
	occupiedUnits := sets.New[int32]()
	for i := range vm.Spec.Network.Interfaces {
		if u := vm.Spec.Network.Interfaces[i].UnitNumber; u != nil {
			occupiedUnits.Insert(*u)
		}
	}

	var (
		claimedDevs   = make([]bool, len(ethDevs))
		hodgepodgeIdx = make([]bool, len(vm.Spec.Network.Interfaces))
		mutated       bool
		zipMatched    []string
	)

	// Passes 1 and 2 skip numbered interfaces when recording, so pass 0 only
	// needs to report which numbered interfaces missed (for SR-IOV detection)
	// and claim the devices of the rest. This backfill only runs with
	// VMNetworkUnitNumbers enabled.
	unitDevIdx := claimByUnitNumber(vm.Spec.Network.Interfaces, ethDevs, claimedDevs, true)

	// Pass 1: provider-dispatched hodgepodge matching for unnumbered
	// interfaces. A nil client cannot resolve provider CRs; skip the pass
	// rather than panic (the zip below still records observed slots, with
	// its ambiguity event).
	if client != nil {
		vmCtx := pkgctx.NewVirtualMachineContext(ctx, vm)

		// Match against only the devices not yet claimed by an earlier
		// interface, mirroring MapEthernetDevicesToSpecIdx's consumption of
		// each matched card before the next lookup: without this, two
		// interfaces whose criteria both match the same card (e.g. two
		// same-network Named interfaces without MACs) would both match the
		// first device. unclaimedIdx maps positions in unclaimedDevs back to
		// their indices in ethDevs.
		unclaimedIdx := make([]int, 0, len(ethDevs))
		unclaimedDevs := make([]vimtypes.BaseVirtualDevice, 0, len(ethDevs))
		rebuildUnclaimed := func() {
			unclaimedIdx = unclaimedIdx[:0]
			unclaimedDevs = unclaimedDevs[:0]
			for j := range ethDevs {
				if !claimedDevs[j] {
					unclaimedIdx = append(unclaimedIdx, j)
					unclaimedDevs = append(unclaimedDevs, ethDevs[j])
				}
			}
		}
		rebuildUnclaimed()

		for i := range vm.Spec.Network.Interfaces {
			iface := &vm.Spec.Network.Interfaces[i]

			// Numbered interfaces were matched in pass 0 and never fall back.
			// SRIOV-typed interfaces never have a unit number recorded, and
			// must not claim a non-SR-IOV device.
			if iface.UnitNumber != nil ||
				iface.Type == vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV {
				continue
			}

			if len(unclaimedDevs) == 0 {
				break
			}

			matchingIdx := network.FindMatchingEthCardForInterfaceSpec(
				vmCtx, client, *iface, unclaimedDevs)
			if matchingIdx < 0 {
				continue
			}

			devIdx := unclaimedIdx[matchingIdx]
			claimedDevs[devIdx] = true
			hodgepodgeIdx[i] = true
			rebuildUnclaimed()

			if recordEthDeviceUnitNumber(
				ctx, iface, ethDevs[devIdx], occupiedUnits) {
				mutated = true
			}
		}

		// SR-IOV detection: record type SRIOV on untyped interfaces uniquely
		// matched to an SR-IOV card.
		if recordSRIOVTypes(vmCtx, client, vm.Spec.Network.Interfaces,
			unitDevIdx, hodgepodgeIdx, ethDevs, claimedDevs, sriovDevs) {
			mutated = true
		}
	} else {
		pkglog.FromContextOrDefault(ctx).V(4).Info(
			"Skipping NIC unit number hodgepodge matching: no client")
	}

	// Reserve every unclaimed device whose observed slot is already declared
	// by a numbered spec interface (or recorded this pass): the zip must only
	// pair leftover interfaces with devices that are not already some other
	// interface's identity, otherwise it G7-skips the write and consumes the
	// pairing, costing a later valid device its record. Pass 0 and pass 1
	// claims are kept even when the spec disagrees with the device: a device
	// claimed by a match must be reserved, not handed to the zip.
	for j := range ethDevs {
		if claimedDevs[j] {
			continue
		}
		if u := ethDevs[j].GetVirtualDevice().UnitNumber; u != nil && occupiedUnits.Has(*u) {
			claimedDevs[j] = true
		}
	}

	// Pass 2: positional zip (zipRecordUnitNumbers).
	if zipMutated, names := zipRecordUnitNumbers(ctx, vm.Spec.Network.Interfaces,
		hodgepodgeIdx, ethDevs, claimedDevs, occupiedUnits); zipMutated {
		mutated = true
		zipMatched = names
	}

	if len(zipMatched) > 0 {
		// G8.2: this is the only case a one-shot event is the right mechanism
		// for — nothing in the resulting spec records that the value came from
		// the zip. The other divergence cases (an explicit value disagreeing
		// with the observed slot, a G7-skipped interface) are steady-state
		// observable and belong to the hardware condition, not here.
		pkgrecord.FromContext(ctx).Warnf(
			vm,
			NICUnitNumberBackfillAmbiguous,
			"Observed unit numbers were recorded for the following network interfaces by positional matching rather than a unique match, and may be assigned incorrectly: %s",
			strings.Join(zipMatched, ", "))
	}

	return mutated
}

// zipRecordUnitNumbers is pass 2 of NICUnitNumbersFromMoVM: a positional zip
// of the interfaces still without a unit number (not matched in pass 1, and
// not SRIOV-typed) against the remaining unclaimed devices with an observed
// unit number. Devices without an observed unit number have nothing to
// record and are not zip candidates. It returns whether any unit number was
// recorded, and the names of the interfaces it was recorded for.
func zipRecordUnitNumbers(
	ctx context.Context,
	ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
	hodgepodgeIdx []bool,
	ethDevs object.VirtualDeviceList,
	claimedDevs []bool,
	occupiedUnits sets.Set[int32]) (bool, []string) {

	var (
		mutated    bool
		zipMatched []string
	)
	for i := range ifaces {
		iface := &ifaces[i]
		if hodgepodgeIdx[i] || iface.UnitNumber != nil ||
			iface.Type == vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV {
			continue
		}

		for j := range ethDevs {
			dev := ethDevs[j]
			if claimedDevs[j] || dev.GetVirtualDevice().UnitNumber == nil {
				continue
			}

			claimedDevs[j] = true
			if recordEthDeviceUnitNumber(ctx, iface, dev, occupiedUnits) {
				mutated = true
				zipMatched = append(zipMatched, iface.Name)
			}
			break
		}
	}
	return mutated, zipMatched
}

// recordSRIOVTypes is the SR-IOV detection step of NICUnitNumbersFromMoVM.
// SRIOV-typed interfaces first claim their matching SR-IOV card, so it
// cannot be a candidate below. Then each untyped interface still unmatched
// (a numbered interface whose unitDevIdx is -1, or an un-numbered interface
// with no hodgepodge match) is matched against every unclaimed device,
// SR-IOV cards included. If the match is an SR-IOV card and is the only unclaimed device
// that matches, the interface's type is set to SRIOV and the card claimed.
// ethDevs and claimedDevs are the non-SR-IOV devices and their claims, which
// are read but not modified. Returns true if any interface type was set.
func recordSRIOVTypes(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
	unitDevIdx []int,
	hodgepodgeIdx []bool,
	ethDevs object.VirtualDeviceList,
	claimedDevs []bool,
	sriovDevs object.VirtualDeviceList) bool {

	if len(sriovDevs) == 0 {
		return false
	}

	sriovClaimed := make([]bool, len(sriovDevs))
	claimSRIOV := func(dev vimtypes.BaseVirtualDevice) {
		for j := range sriovDevs {
			if sriovDevs[j] == dev {
				sriovClaimed[j] = true
				return
			}
		}
	}
	unclaimedSRIOV := func() object.VirtualDeviceList {
		var out object.VirtualDeviceList
		for j := range sriovDevs {
			if !sriovClaimed[j] {
				out = append(out, sriovDevs[j])
			}
		}
		return out
	}

	for i := range ifaces {
		if ifaces[i].Type != vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV {
			continue
		}
		cands := unclaimedSRIOV()
		if m := network.FindMatchingEthCardForInterfaceSpec(vmCtx, client, ifaces[i], cands); m >= 0 {
			claimSRIOV(cands[m])
		}
	}

	var mutated bool
	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Type != "" || hodgepodgeIdx[i] ||
			(iface.UnitNumber != nil && unitDevIdx[i] >= 0) {
			continue
		}

		var cands object.VirtualDeviceList
		for j := range ethDevs {
			if !claimedDevs[j] {
				cands = append(cands, ethDevs[j])
			}
		}
		cands = append(cands, unclaimedSRIOV()...)

		m := network.FindMatchingEthCardForInterfaceSpec(vmCtx, client, *iface, cands)
		if m < 0 {
			continue
		}
		match := cands[m]
		if _, ok := match.(*vimtypes.VirtualSriovEthernetCard); !ok {
			continue
		}
		rest := slices.Delete(slices.Clone(cands), m, m+1)
		if network.FindMatchingEthCardForInterfaceSpec(vmCtx, client, *iface, rest) >= 0 {
			pkglog.FromContextOrDefault(vmCtx).V(4).Info(
				"Skipping NIC SR-IOV type backfill: more than one device matches",
				"interface", iface.Name)
			continue
		}

		claimSRIOV(match)
		iface.Type = vmopv1.VirtualMachineNetworkInterfaceTypeSRIOV
		mutated = true
	}
	return mutated
}

// claimByUnitNumber is pass 0 of NICUnitNumbersFromMoVM and NICConfigFromMoVM.
// Each numbered interface (see vmopv1util.NICInterfaceUnitNumber) claims the
// unclaimed device at exactly its spec unit number. It returns, per
// interface, the index into ethDevs of the claimed device, or -1 when the
// interface is un-numbered or no device sits at its unit. Callers decide how
// a numbered interface with no device at its unit is handled.
func claimByUnitNumber(
	ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethDevs object.VirtualDeviceList,
	claimedDevs []bool,
	unitNumbersEnabled bool) []int {

	devIdx := make([]int, len(ifaces))
	for i := range ifaces {
		devIdx[i] = -1
		unit := vmopv1util.NICInterfaceUnitNumber(&ifaces[i], unitNumbersEnabled)
		if unit == nil {
			continue
		}
		for j := range ethDevs {
			if claimedDevs[j] {
				continue
			}
			if u := ethDevs[j].GetVirtualDevice().UnitNumber; u != nil && *u == *unit {
				claimedDevs[j] = true
				devIdx[i] = j
				break
			}
		}
	}
	return devIdx
}

// recordEthDeviceUnitNumber records the device's observed unit number onto
// iface unless doing so would make the spec inadmissible (G7): the value is
// outside the valid ethernet-card range, or already claimed by another
// interface's spec value or by a value recorded earlier in this same pass
// (occupiedUnits). The device's UnitNumber is already *int32 — copy the
// pointed-to value with a nil guard; never re-take its address, which would
// be a **int32 and would alias a field inside moVM.
func recordEthDeviceUnitNumber(
	ctx context.Context,
	iface *vmopv1.VirtualMachineNetworkInterfaceSpec,
	dev vimtypes.BaseVirtualDevice,
	occupiedUnits sets.Set[int32]) bool {

	observed := dev.GetVirtualDevice().UnitNumber
	if observed == nil {
		// The device reports no unit number: nothing to record.
		return false
	}

	unit := *observed

	if unit < vmopv1util.NICUnitNumberFirst || unit > vmopv1util.NICUnitNumberMax {
		pkglog.FromContextOrDefault(ctx).V(4).Info(
			"Skipping NIC unit number backfill: observed value is out of the valid range",
			"interface", iface.Name,
			"unitNumber", unit)
		return false
	}

	if occupiedUnits.Has(unit) {
		pkglog.FromContextOrDefault(ctx).V(4).Info(
			"Skipping NIC unit number backfill: observed value is already claimed by another interface",
			"interface", iface.Name,
			"unitNumber", unit)
		return false
	}

	occupiedUnits.Insert(unit)
	iface.UnitNumber = ptr.To(unit)
	return true
}
