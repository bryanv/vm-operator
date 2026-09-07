// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"slices"
	"strings"

	govmomifault "github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgptr "github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/pkg/util/resize"
)

func ReconcileNetworkInterfaces(
	ctx context.Context,
	results *NetworkInterfaceResults,
	currentEthCards object.VirtualDeviceList,
) ([]vimtypes.BaseVirtualDeviceConfigSpec, error) {
	var deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec

	// Removes of replaced devices must land in the removes-first portion of
	// the returned change list: vSphere frees and reuses a unit number within
	// one Reconfigure, but only if the Remove is processed before the Add.
	var replaceRemoves []vimtypes.BaseVirtualDeviceConfigSpec

	// Devices are only populated with a unit number when the
	// VMNetworkUnitNumbers feature is enabled, so a non-nil dev.UnitNumber
	// already implies the feature is on; the explicit flag check here keeps
	// that invariant local (and makes a directly-constructed Device with a
	// unit number behave as today when the feature is off).
	unitNumbersEnabled := pkgcfg.FromContext(ctx).Features.VMNetworkUnitNumbers

	// Two passes, in order (plan.md Design point 1): pass 1 claims devices
	// for every numbered result first — exact-slot lookup only; pass 2 then
	// runs the MAC/ExternalID/backing fallback for un-numbered results
	// against whatever pass 1 left unclaimed. Running these in a single
	// greedy loop would let an un-numbered result claim — by backing — the
	// device a later numbered result declares as its identity.
	handled := make([]bool, len(results.Devices))

	for idx := range results.Devices {
		dev := &results.Devices[idx]
		if !(unitNumbersEnabled && dev.UnitNumber != nil) {
			continue
		}
		handled[idx] = true
		ethCard := dev.EthCard.(vimtypes.BaseVirtualEthernetCard)

		// Stamp the declared unit number onto the desired device up front
		// (T001 confirmed vSphere honours an explicit UnitNumber on an Add):
		// every device change this result can emit below — a replace Add, or
		// a miss Add — must carry it, while the full-match branch emits no
		// device change at all. Stamp once here so the branches stay stamp
		// sites free.
		ethCard.GetVirtualEthernetCard().GetVirtualDevice().UnitNumber = pkgptr.To(*dev.UnitNumber)

		// Pass 1: exact unit-number claim. A declared unit number identifies
		// specific hardware or nothing: on a miss the result must not fall
		// back to MAC/ExternalID/backing matching, nor to the orphaned-CR
		// Edit below — it routes straight to the ordinary Add path.
		matchingIdx := FindMatchingEthCard(currentEthCards, ethCard, dev.UnitNumber)
		if matchingIdx >= 0 {
			matchDev := currentEthCards[matchingIdx].(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()

			if ethCardMatchesDesired(ethCard.GetVirtualEthernetCard(), matchDev) {
				// The device at the declared slot agrees with everything the
				// desired state specifies: no device change. Adopt its
				// Key/MAC — this is how a Generated MAC is learned.
				results.Devices[idx].EthCardKey = matchDev.Key
				results.Devices[idx].MacAddress = matchDev.MacAddress
				ethCard.GetVirtualEthernetCard().MacAddress = matchDev.MacAddress
			} else {
				// The device at the declared slot disagrees with the desired
				// state: replace it by removing it and adding the desired
				// device at the same unit number in the same ReconfigVM_Task
				// — deliberately not an Edit. The Add is built fresh from
				// the interface's own desired state, so backing, MAC, and
				// ExternalID are coherent by construction.
				//
				// Do NOT adopt the removed device's Key/MacAddress: leave
				// EthCardKey zero and mark the results so the
				// post-reconfigure MAC fixup re-identifies the new device by
				// its unit number. On at least one VC build a replacement
				// reissues the SAME Key (derived from the unit number), so
				// an unchanged Key does not imply that nothing changed.
				replaceRemoves = append(replaceRemoves, &vimtypes.VirtualDeviceConfigSpec{
					Device:    currentEthCards[matchingIdx],
					Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
				})
				deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
					Device:    dev.EthCard,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
				})
				results.UpdatedEthCards = true
			}

			// Claim the located device either way so the trailing
			// unmatched-device removal and the fallback pass cannot see it.
			currentEthCards = slices.Delete(currentEthCards, matchingIdx, matchingIdx+1)
			continue
		}

		// Miss at the declared slot: a plain Add of the desired device,
		// already stamped with the declared unit number above. The
		// interface's old device — if unclaimed by any other result — is
		// removed by the trailing unmatched-device pass.
		deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
			Device:    dev.EthCard,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
		})
		results.UpdatedEthCards = true
	}

	// Pass 2: un-numbered results use the existing MAC/ExternalID/backing
	// matching against whatever pass 1 left unclaimed. Results here carry
	// no unit number by construction (pass 1 handled and marked every
	// numbered result), so their Adds are emitted without a UnitNumber
	// stamp — if pass 2 ever handles numbered results, its Add branch
	// must stamp them the way pass 1 does.
	for idx, dev := range results.Devices {
		if handled[idx] {
			continue
		}

		matchingIdx := FindMatchingEthCard(currentEthCards, dev.EthCard.(vimtypes.BaseVirtualEthernetCard), nil)
		if matchingIdx >= 0 {
			// Exact match. Claim it by removing the device from the current ethernet cards.
			matchDev := currentEthCards[matchingIdx].(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			results.Devices[idx].EthCardKey = matchDev.Key
			results.Devices[idx].MacAddress = matchDev.MacAddress
			dev.EthCard.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().MacAddress = matchDev.MacAddress
			currentEthCards = slices.Delete(currentEthCards, matchingIdx, matchingIdx+1)
		} else {
			existingIdx := findExistingEthCardForOrphanedCR(ctx, dev.InterfaceName, results.OrphanedNetworkInterfaces, currentEthCards)
			if existingIdx >= 0 {
				// As best we can, we determined that one of the VM's current ethernet card corresponds to a now
				// unreferenced (orphaned) network interface CR with the same interface name. To keep the device
				// type the same, do an edit on the existing device.
				editDev := currentEthCards[existingIdx]

				ethDev := editDev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
				ethDev.Backing = dev.EthCard.GetVirtualDevice().Backing
				ethDev.AddressType = dev.EthCard.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().AddressType
				ethDev.MacAddress = dev.EthCard.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().MacAddress
				ethDev.ExternalId = dev.EthCard.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().ExternalId

				// Clear SubnetID when the backing changes to ensure we don't have a
				// mismatch between the subnet ID and the port group specified in the
				// backing. The subnetID is populated by the platform automatically when
				// the network adapter is connected to a subnet.
				ethDev.SubnetId = ""

				deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
					Device:    editDev,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
				})

				currentEthCards = slices.Delete(currentEthCards, existingIdx, existingIdx+1)
			} else {
				deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
					Device:    dev.EthCard,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
				})
			}

			results.UpdatedEthCards = true
		}
	}

	// Remove any unmatched existing interfaces.
	removeDeviceChanges := make([]vimtypes.BaseVirtualDeviceConfigSpec, 0, len(currentEthCards))
	for _, dev := range currentEthCards {
		removeDeviceChanges = append(removeDeviceChanges, &vimtypes.VirtualDeviceConfigSpec{
			Device:    dev,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
		})
	}

	// Process any removes first: replaced devices, then unmatched ones.
	return append(append(replaceRemoves, removeDeviceChanges...), deviceChanges...), nil
}

// findExistingEthCardForOrphanedCR tries to an orphaned interface and if it has
// a matching current ethernet card, so the existing device can be edited as to
// keep the device type (ie, E1000) the same.
func findExistingEthCardForOrphanedCR(
	_ context.Context,
	interfaceName string,
	orphanedObjects []ctrlclient.Object,
	currentEthCards object.VirtualDeviceList,
) int {
	findMatchFn := func(obj ctrlclient.Object) int {
		switch netIf := obj.(type) {
		case *netopv1alpha1.NetworkInterface:
			// Since this is orphaned we do not know what the MAC could have been.
			return findMatchingEthCardNetOpNetIf(netIf, currentEthCards, "")
		case *ncpv1alpha1.VirtualNetworkInterface:
			return findMatchingEthCardNCPNetIf(netIf, currentEthCards)
		case *vpcv1alpha1.SubnetPort:
			return findMatchingEthCardVPCSubnetPort(netIf, currentEthCards)
		}
		return -1
	}

	var objIdxWithoutLabel []int
	suffix := "-" + interfaceName

	for idx, obj := range orphanedObjects {
		if v, ok := obj.GetLabels()[VMInterfaceNameLabel]; !ok {
			// Favor interfaces that we had labeled with the interface spec name first. Otherwise,
			// fallback to just trying to match by the suffix which isn't perfect.
			if strings.HasSuffix(obj.GetName(), suffix) {
				objIdxWithoutLabel = append(objIdxWithoutLabel, idx)
			}
			continue
		} else if v != interfaceName {
			continue
		}

		if matchingIdx := findMatchFn(obj); matchingIdx >= 0 {
			return matchingIdx
		}
	}

	for _, idx := range objIdxWithoutLabel {
		obj := orphanedObjects[idx]

		if matchingIdx := findMatchFn(obj); matchingIdx >= 0 {
			return matchingIdx
		}
	}

	return -1
}

// FindMatchingEthCard returns the index of the first ethernet card in
// currentEthCards matching the desired ethCard. When unitNumber is non-nil,
// it performs an exact-slot scan only: a declared unit number identifies
// specific hardware or nothing at all, so the MAC/ExternalID/backing
// comparison is never consulted (a miss must not match "whatever
// backing-matches instead"). Call sites enable that path only when the
// VMNetworkUnitNumbers feature is on; the flag is not checked here because
// the function takes no context.
func FindMatchingEthCard(
	currentEthCards object.VirtualDeviceList,
	ethCard vimtypes.BaseVirtualEthernetCard,
	unitNumber *int32,
) int {
	if unitNumber != nil {
		for idx := range currentEthCards {
			if u := currentEthCards[idx].GetVirtualDevice().UnitNumber; u != nil && *u == *unitNumber {
				return idx
			}
		}
		return -1
	}

	ethDev := ethCard.GetVirtualEthernetCard()

	for idx := range currentEthCards {
		curDev := currentEthCards[idx].(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
		if ethCardMatchesDesired(ethDev, curDev) {
			return idx
		}
	}

	return -1
}

// ethCardMatchesDesired compares a desired ethernet card against a located
// (current) one on backing, MAC — only when the desired state specifies one
// via AddressType Manual; a Generated MAC is not compared — and ExternalID —
// only when the desired state specifies one (non-empty). Fields the desired
// state does not specify are not compared and must not trigger a change.
//
// Device type is deliberately excluded: the desired device is always built
// as the default type (vmxnet3), so comparing type would churn every
// class-ConfigSpec E1000/SR-IOV device on the first reconcile after the
// feature is enabled. A located device of a differing type is left alone.
func ethCardMatchesDesired(ethDev, curDev *vimtypes.VirtualEthernetCard) bool {
	if ethDev.AddressType == string(vimtypes.VirtualEthernetCardMacTypeManual) {
		if !strings.EqualFold(ethDev.MacAddress, curDev.MacAddress) {
			return false
		}
	}

	if ethDev.ExternalId != "" {
		if ethDev.ExternalId != curDev.ExternalId {
			return false
		}
	}

	if curDev.Backing == nil {
		return false
	}

	switch a := ethDev.Backing.(type) {
	case *vimtypes.VirtualEthernetCardNetworkBackingInfo:
		return resize.MatchVirtualEthernetCardNetworkBackingInfo(a, curDev.Backing)
	case *vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo:
		return resize.MatchVirtualEthernetCardDistributedVirtualPortBackingInfo(a, curDev.Backing)
	case *vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo:
		return resize.MatchVirtualEthernetCardOpaqueNetworkBackingInfo(a, curDev.Backing)
	}

	return false
}

// nicUnitNumberCollisionProperty is the InvalidDeviceSpec Property value
// vSphere reports when an explicit NIC unit number collides with an occupied
// slot (T001 Q4/E08).
const nicUnitNumberCollisionProperty = "unitNumber"

// IsNICUnitNumberCollisionFault reports whether err is the fault vSphere
// returns when an explicit NIC unit number collides with an already-occupied
// slot: InvalidDeviceSpec with Property "unitNumber" (T001 Q4/E08, recorded
// against a real vCenter and matching vcsim's simulated fault type).
//
// Such a collision is a PERMANENT error, not a retryable one: retrying an
// unchanged colliding payload faults identically forever. Callers should
// surface it as pkgerr.NoRequeueError. The classification is wired into the
// session package's doReconfigure (session_vm_update.go), the shared
// reconfigure entry that executes the ConfigSpec carrying the ethernet device
// changes computed here.
//
// Note the deliberately narrow shape: an out-of-range unit number below the
// ethernet band may be silently renumbered by vSphere rather than faulted,
// and above the band it faults with InvalidArgument (T001 E19) — neither is
// a collision, and neither matches here.
func IsNICUnitNumberCollisionFault(err error) bool {
	if err == nil {
		return false
	}

	var spec *vimtypes.InvalidDeviceSpec
	if _, ok := govmomifault.As(err, &spec); !ok {
		return false
	}

	// vSphere reports the property exactly as "unitNumber"; compare exactly.
	return spec.Property == nicUnitNumberCollisionProperty
}
