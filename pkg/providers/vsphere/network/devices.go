// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// CreateVirtualEthernetCard creates a new VirtualEthernetCard based on the Device
// and the InterfaceSpec.
func CreateVirtualEthernetCard(
	ctx context.Context,
	dev Device,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) (vimtypes.BaseVirtualDevice, error) {

	backing, err := dev.Backing.EthernetCardBackingInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get Ethernet card backing info for network ID %s (%s): %w",
			dev.NetworkID, dev.Backing.Reference().Value, err)
	}

	var cardType string
	// TODO: Honor interfaceSpec.Type if set.
	switch interfaceSpec.Type { //nolint:gocritic
	default:
		cardType = "vmxnet3"
	}

	ethDev, err := object.EthernetCardTypes().CreateEthernetCard(cardType, backing)
	if err != nil {
		return nil, fmt.Errorf("unable to create Ethernet card for backing %s: %w",
			dev.Backing.Reference().Value, err)
	}

	ethCard := ethDev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	ethCard.ExternalId = dev.ExternalID
	if dev.MacAddress != "" {
		ethCard.MacAddress = dev.MacAddress
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
	} else {
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeGenerated)
	}

	// TODO: interfaceSpec.VNUMANodeID, VMXNet3. We need to sort out how to deal
	// with the NetworkExtraConfig reconciler. First, during VM create, it is being
	// called before vmCreateGenConfigSpecZipNetworkInterfaces, so it won't see these
	// devices, and second it look like it depends on the DeviceKey being set, which
	// it won't be until after the device is added to the VM. For VM create, my pref
	// is to create the VM as-is instead of fixup after create. Similarly, when a NIC
	// is added to a VM, the all this should land in the same Reconfigure.

	return ethDev, nil
}

// UpdateVMClassEthCardFromDevice applies a Device to an existing Ethernet card
// from the class ConfigSpec. This is used only during VM create.
func UpdateVMClassEthCardFromDevice(
	ctx context.Context,
	dev Device,
	ethCard *vimtypes.VirtualEthernetCard) error {

	backing, err := dev.Backing.EthernetCardBackingInfo(ctx)
	if err != nil {
		return fmt.Errorf("unable to get Ethernet card backing info for network ID %s (%s): %w",
			dev.NetworkID, dev.Backing.Reference().Value, err)
	}

	ethCard.Backing = backing
	ethCard.SubnetId = ""
	ethCard.ExternalId = dev.ExternalID

	if dev.MacAddress != "" {
		ethCard.MacAddress = dev.MacAddress
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
	} else { //nolint:staticcheck,revive
		// BMV: IMO this must be Generated/TypeAssigned to avoid major foot gun, but we have tests assuming
		// this is left as-is.
		// ethCard.MacAddress = ""
		// ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeGenerated)
	}

	return nil
}

// MapEthernetDevicesToSpecIdx maps the VM's ethernet devices to the
// corresponding entry in the VM's Spec. It returns two maps:
//
//   - The first (authoritative) map is the exact-only mapping: an interface
//     carrying a unit number (when VMNetworkUnitNumbers is enabled) resolves
//     only to the device at its declared slot, and gets NO entry on a miss.
//     Anything that moves hardware or selects boot devices must use this map
//     exclusively (the boot-options reconciler does).
//
//   - The second (name-resolution) map is the authoritative map plus entries
//     for numbered interfaces that missed their exact slot, resolved through
//     the ordinary CR-based/zip fallback against leftover devices. It exists
//     only so the status path can keep labeling a Tools-reported interface
//     entry with its spec name (and keep the vnumaNodeID/vmxnet3 status
//     joins via that name; the networkextraconfig status path matches with
//     its own DefaultNICMatcher and is unaffected). A wrong entry here can
//     mislabel a status entry but can never move hardware, which is why the
//     relaxed fallback is acceptable for this map only (spec.md G13, I15).
func MapEthernetDevicesToSpecIdx(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	vmMO mo.VirtualMachine) (map[int32]int, map[int32]int) {

	if vmCtx.VM.Spec.Network == nil || vmMO.Config == nil || vmMO.Config.Hardware.Device == nil {
		return nil, nil
	}

	var (
		devices         = object.VirtualDeviceList(vmMO.Config.Hardware.Device)
		ethCards        = devices.SelectByType((*vimtypes.VirtualEthernetCard)(nil))
		interfaces      = vmCtx.VM.Spec.Network.Interfaces
		devKeyToSpecIdx = make(map[int32]int)
	)

	unitNumbersEnabled := pkgcfg.FromContext(vmCtx).Features.VMNetworkUnitNumbers
	claimed := make([]bool, len(ethCards))

	// Track which spec interfaces the authoritative pass failed to map, so
	// the name-resolution pass can run the fallback for exactly those. Only
	// meaningful when unitNumbersEnabled; with the flag off both maps are
	// identical.
	authoritativeMiss := make([]bool, len(interfaces))

	// Pass 1: exact unit-number claim for numbered interfaces. A declared
	// unit number is the interface's identity for its device (spec.md G11):
	// on a hit the card is claimed so the fallback passes cannot reuse it;
	// on a miss the interface gets NO entry at all — it must not fall
	// through to CR-based/positional matching, because a wrong match here
	// feeds boot-order device selection (bootoptions reconciler) and could
	// point network boot at the wrong physical NIC. The miss is not
	// transient (G13): a unitNumber change is admitted on a powered-on VM
	// but not applied until the next power-off, so callers tolerate the
	// absence rather than this mapping relaxing.
	if unitNumbersEnabled {
		unitToIdx := make(map[int32]int, len(ethCards))
		for i, dev := range ethCards {
			if u := dev.GetVirtualDevice().UnitNumber; u != nil {
				unitToIdx[*u] = i
			}
		}

		for i := range interfaces {
			if interfaces[i].UnitNumber == nil {
				continue
			}
			if j, ok := unitToIdx[*interfaces[i].UnitNumber]; ok && !claimed[j] {
				claimed[j] = true
				devKeyToSpecIdx[ethCards[j].GetVirtualDevice().Key] = i
			} else {
				authoritativeMiss[i] = true
			}
		}
	}

	if !pkgcfg.FromContext(vmCtx).Features.MutableNetworks {
		if !unitNumbersEnabled {
			// For immutable, just zip these lists together. This assumes that the
			// devices are in the same order as the VM Spec.Network.Interfaces.
			for i := range min(len(ethCards), len(interfaces)) {
				devKeyToSpecIdx[ethCards[i].GetVirtualDevice().Key] = i
			}
			return devKeyToSpecIdx, devKeyToSpecIdx
		}

		// Zip the un-numbered interfaces against the remaining unclaimed
		// devices, in device order. Numbered interfaces are skipped: they
		// resolve exclusively by their declared unit number above.
		next := 0
		for i := range interfaces {
			if interfaces[i].UnitNumber != nil {
				continue
			}
			for next < len(ethCards) && claimed[next] {
				next++
			}
			if next >= len(ethCards) {
				break
			}
			devKeyToSpecIdx[ethCards[next].GetVirtualDevice().Key] = i
			claimed[next] = true
			next++
		}

		// Name-resolution fallback: numbered misses zip against whatever the
		// authoritative passes left unclaimed. Labels status entries only.
		devKeyToSpecIdxForNaming := maps.Clone(devKeyToSpecIdx)
		for i := range interfaces {
			if !authoritativeMiss[i] {
				continue
			}
			for next < len(ethCards) && claimed[next] {
				next++
			}
			if next >= len(ethCards) {
				break
			}
			devKeyToSpecIdxForNaming[ethCards[next].GetVirtualDevice().Key] = i
			claimed[next] = true
			next++
		}
		return devKeyToSpecIdx, devKeyToSpecIdxForNaming
	}

	// Mutable: per-interface provider-dispatched matching for the un-numbered
	// interfaces only, consuming each matched card (numbered interfaces'
	// claims are already carved out below so the matcher cannot reuse them).
	if unitNumbersEnabled {
		remaining := make(object.VirtualDeviceList, 0, len(ethCards))
		for i, dev := range ethCards {
			if !claimed[i] {
				remaining = append(remaining, dev)
			}
		}
		ethCards = remaining
	}

	for i, interfaceSpec := range interfaces {
		if unitNumbersEnabled && interfaceSpec.UnitNumber != nil {
			// Exact-only: claimed above, or deliberately unmapped on a miss —
			// never eligible for CR-based matching.
			continue
		}
		matchingIdx := FindMatchingEthCardForInterfaceSpec(vmCtx, client, interfaceSpec, ethCards)
		if matchingIdx >= 0 {
			devKeyToSpecIdx[ethCards[matchingIdx].GetVirtualDevice().Key] = i
			ethCards = slices.Delete(ethCards, matchingIdx, matchingIdx+1)
		}
	}

	if !unitNumbersEnabled {
		return devKeyToSpecIdx, devKeyToSpecIdx
	}

	// Name-resolution fallback: numbered misses CR-match against the devices
	// the authoritative pass left unclaimed. Labels status entries only.
	devKeyToSpecIdxForNaming := maps.Clone(devKeyToSpecIdx)
	for i, interfaceSpec := range interfaces {
		if !authoritativeMiss[i] {
			continue
		}
		matchingIdx := FindMatchingEthCardForInterfaceSpec(vmCtx, client, interfaceSpec, ethCards)
		if matchingIdx >= 0 {
			devKeyToSpecIdxForNaming[ethCards[matchingIdx].GetVirtualDevice().Key] = i
			ethCards = slices.Delete(ethCards, matchingIdx, matchingIdx+1)
		}
	}

	return devKeyToSpecIdx, devKeyToSpecIdxForNaming
}

// FindMatchingEthCardForInterfaceSpec resolves a spec interface to the index
// of its matching ethernet device in ethCards via provider-dispatched
// MAC/ExternalID/backing matching, returning -1 when no device satisfies the
// network provider's criteria. It dispatches on the configured
// NetworkProviderType: VDS, NSXT, VPC, or Named. The matcher compares the MAC
// only when the interface specifies one, the ExternalID only when non-empty,
// and the backing per provider — mirroring FindMatchingEthCard's predicate.
// Shared by the reconcile-time matching and the schema-upgrade backfill.
func FindMatchingEthCardForInterfaceSpec(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethCards object.VirtualDeviceList) int {

	matchingIdx := -1

	switch pkgcfg.FromContext(vmCtx).NetworkProviderType {
	case pkgcfg.NetworkProviderTypeVDS:
		matchingIdx = findMatchingEthCardVDS(vmCtx, client, interfaceSpec, ethCards)
	case pkgcfg.NetworkProviderTypeNSXT:
		matchingIdx = findMatchingEthCardNSXT(vmCtx, client, interfaceSpec, ethCards)
	case pkgcfg.NetworkProviderTypeVPC:
		matchingIdx = findMatchingEthCardVPC(vmCtx, client, interfaceSpec, ethCards)
	case pkgcfg.NetworkProviderTypeNamed:
		matchingIdx = findMatchingEthCardNamed(vmCtx, client, interfaceSpec, ethCards)
	}

	return matchingIdx
}

func findMatchingEthCardVDS(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethCards object.VirtualDeviceList) int {

	var (
		networkRefName string
		networkRefType metav1.TypeMeta
	)

	if netRef := interfaceSpec.Network; netRef != nil {
		// If Name is empty, NetOP will try to select the namespace default.
		networkRefName = netRef.Name
		networkRefType = netRef.TypeMeta
	}

	if kind := networkRefType.Kind; kind != "" && kind != "Network" {
		return -1
	}

	netIf := &netopv1alpha1.NetworkInterface{}
	netIfKey := types.NamespacedName{
		Namespace: vmCtx.VM.Namespace,
		Name:      NetOPCRName(vmCtx.VM.Name, networkRefName, interfaceSpec.Name, true),
	}

	// Check if a networkIf object exists with the older (v1a1) naming convention.
	if err := client.Get(vmCtx, netIfKey, netIf); err != nil {
		if !apierrors.IsNotFound(err) {
			return -1
		}

		// If NotFound set the netIf to the new v1a2 naming convention.
		netIf.ObjectMeta = metav1.ObjectMeta{
			Name:      NetOPCRName(vmCtx.VM.Name, networkRefName, interfaceSpec.Name, false),
			Namespace: vmCtx.VM.Namespace,
		}

		if err := client.Get(vmCtx, ctrlclient.ObjectKeyFromObject(netIf), netIf); err != nil {
			return -1
		}
	}

	// The NetworkInterface does not have a Spec field for a MAC address, but if our
	// interface spec has one, then we would have used that to create the EthCard so
	// match on that too.
	macAddress := interfaceSpec.MACAddr

	return findMatchingEthCardNetOpNetIf(netIf, ethCards, macAddress)
}

func findMatchingEthCardNetOpNetIf(
	netIf *netopv1alpha1.NetworkInterface,
	ethCards object.VirtualDeviceList,
	macAddress string) int {

	for i, dev := range ethCards {
		bEthCard, ok := dev.(vimtypes.BaseVirtualEthernetCard)
		if !ok {
			continue
		}

		ethCard := bEthCard.GetVirtualEthernetCard()
		if id := netIf.Status.ExternalID; id != "" {
			if ethCard.ExternalId != id {
				continue
			}
		} else if ethCard.ExternalId != "" {
			// This ethernet device has an external ID but the network interface CR does
			// not. In VDS, the external ID is only set on newly created interface CRs so
			// don't continue trying to match.
			continue
		}

		if macAddress != "" {
			if !strings.EqualFold(macAddress, ethCard.MacAddress) {
				continue
			}
		}

		switch b := ethCard.Backing.(type) {
		case *vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo:
			if b.Port.PortgroupKey == netIf.Status.NetworkID {
				return i
			}
		case *vimtypes.VirtualEthernetCardNetworkBackingInfo:
			// The server resolves and reports back the Network MoRef for a
			// standard portgroup backing even though it is not required
			// when configuring the device.
			if b.Network != nil && b.Network.Value == netIf.Status.NetworkID {
				return i
			}
		}
	}

	return -1
}

func findMatchingEthCardNSXT(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethCards object.VirtualDeviceList) int {

	var (
		networkRefName string
		networkRefType metav1.TypeMeta
	)

	if netRef := interfaceSpec.Network; netRef != nil {
		// If Name is empty, NCP will use the namespace default.
		networkRefName = netRef.Name
		networkRefType = netRef.TypeMeta
	}

	if kind := networkRefType.Kind; kind != "" && kind != "VirtualNetwork" {
		return -1
	}

	vnetIf := &ncpv1alpha1.VirtualNetworkInterface{}
	vnetIfKey := types.NamespacedName{
		Namespace: vmCtx.VM.Namespace,
		Name:      NCPCRName(vmCtx.VM.Name, networkRefName, interfaceSpec.Name, true),
	}

	// check if a networkIf object exists with the older (v1a1) naming convention
	if err := client.Get(vmCtx, vnetIfKey, vnetIf); err != nil {
		if !apierrors.IsNotFound(err) {
			return -1
		}

		// if notFound set the vnetIf to use the new v1a2 naming convention
		vnetIf.ObjectMeta = metav1.ObjectMeta{
			Name:      NCPCRName(vmCtx.VM.Name, networkRefName, interfaceSpec.Name, false),
			Namespace: vmCtx.VM.Namespace,
		}

		if err := client.Get(vmCtx, ctrlclient.ObjectKeyFromObject(vnetIf), vnetIf); err != nil {
			return -1
		}
	}

	return findMatchingEthCardNCPNetIf(vnetIf, ethCards)
}

func findMatchingEthCardNCPNetIf(
	netIf *ncpv1alpha1.VirtualNetworkInterface,
	ethCards object.VirtualDeviceList) int {

	for i, dev := range ethCards {
		bEthCard, ok := dev.(vimtypes.BaseVirtualEthernetCard)
		if !ok {
			continue
		}

		ethCard := bEthCard.GetVirtualEthernetCard()
		if ethCard.ExternalId == netIf.Status.InterfaceID &&
			strings.EqualFold(ethCard.MacAddress, netIf.Status.MacAddress) {
			return i
		}
	}

	return -1
}

func findMatchingEthCardVPC(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethCards object.VirtualDeviceList) int {

	var networkRefName string
	if netRef := interfaceSpec.Network; netRef != nil {
		networkRefName = netRef.Name
	}

	subnetPort := &vpcv1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      VPCCRName(vmCtx.VM.Name, networkRefName, interfaceSpec.Name),
			Namespace: vmCtx.VM.Namespace,
		},
	}

	if err := client.Get(vmCtx, ctrlclient.ObjectKeyFromObject(subnetPort), subnetPort); err != nil {
		return -1
	}

	return findMatchingEthCardVPCSubnetPort(subnetPort, ethCards)
}

func findMatchingEthCardVPCSubnetPort(
	subnetPort *vpcv1alpha1.SubnetPort,
	ethCards object.VirtualDeviceList) int {

	for i, dev := range ethCards {
		bEthCard, ok := dev.(vimtypes.BaseVirtualEthernetCard)
		if !ok {
			continue
		}

		ethCard := bEthCard.GetVirtualEthernetCard()

		macAddress := subnetPort.Status.NetworkInterfaceConfig.MACAddress
		if macAddress == vpcIgnoreMacAddr {
			macAddress = ""
		}

		if macAddress != "" {
			if !strings.EqualFold(macAddress, ethCard.MacAddress) {
				continue
			}
		}

		// TODO: Relax this to check during VPC backup/restore.
		if ethCard.ExternalId == subnetPort.Status.Attachment.ID {
			return i
		}
	}

	return -1
}

func findMatchingEthCardNamed(
	_ pkgctx.VirtualMachineContext,
	_ ctrlclient.Client,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	ethCards object.VirtualDeviceList) int {

	if interfaceSpec.Network == nil || interfaceSpec.Network.Name == "" {
		return -1
	}

	for i, dev := range ethCards {
		bEthCard, ok := dev.(vimtypes.BaseVirtualEthernetCard)
		if !ok {
			continue
		}

		if mac := interfaceSpec.MACAddr; mac != "" {
			if !strings.EqualFold(mac, bEthCard.GetVirtualEthernetCard().MacAddress) {
				continue
			}
		}

		network, ok := dev.GetVirtualDevice().Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
		if ok && network.DeviceName == interfaceSpec.Network.Name {
			return i
		}
	}

	return -1
}
