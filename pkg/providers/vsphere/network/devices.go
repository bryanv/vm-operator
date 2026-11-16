// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// CreateVirtualEthernetCard creates a new VirtualEthernetCard based on the Device
// and the InterfaceSpec. It resolves the Device's Ethernet card backing and
// builds the card with BuildAdd, so the create path and the update path's
// planner build an added card the same way.
func CreateVirtualEthernetCard(
	ctx context.Context,
	dev Device,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) (vimtypes.BaseVirtualDevice, error) {

	backing, err := dev.Backing.EthernetCardBackingInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get Ethernet card backing info for network ID %s (%s): %w",
			dev.NetworkID, dev.Backing.Reference().Value, err)
	}

	// The MAC address is passed through as given rather than normalized, so
	// the card carries exactly the provider's value, as it always has.
	ethDev, err := BuildAdd(AddInput{
		Interface: interfaceSpec.Name,
		Target: &Attachment{
			ExternalID: externalIDIntent(dev.ExternalID),
			MAC:        MACPolicy{Pinned: dev.MacAddress},
		},
		Backing: backing,
	})
	if err != nil {
		return nil, err
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

// MapEthernetDevicesToSpecIdx maps the device keys of the VM's Ethernet cards
// to the index of the spec.network.interfaces entry that claims them. It is
// derived from the shared hardware match (MatchInterfaces), so status, boot
// options, and device reconciliation agree about which card belongs to each
// interface. It reuses the pass's observation from the context when that
// observation was computed from the same VM and vSphere VM, and otherwise
// observes and matches anew. A card that no interface claims, such as the
// card left behind by a numbered interface whose unit number has no device
// yet, has no entry.
func MapEthernetDevicesToSpecIdx(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	vmMO mo.VirtualMachine) map[int32]int {

	if vmCtx.VM.Spec.Network == nil || vmMO.Config == nil || vmMO.Config.Hardware.Device == nil {
		return nil
	}

	return DeviceKeyToSpecIdx(ObservedInfo(vmCtx, client, vmCtx.VM, vmMO))
}

// ObservedInfo returns a matched network observation of vm and moVM. It
// returns the Observed snapshot of the ReconcileState in ctx when that
// snapshot was computed from the same inputs, i.e. its ObservationIdentity
// equals the identity of vm and moVM. Otherwise, for example when a later
// step changed spec.network.interfaces or there is no ReconcileState in ctx,
// it observes and matches anew with Observe. The returned snapshot must be
// treated as read-only. It returns nil when moVM has no config.
func ObservedInfo(
	ctx context.Context,
	client ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine) *Info {

	if moVM.Config == nil {
		return nil
	}

	if st, ok := FromContext(ctx); ok && st.Observed != nil &&
		st.Observed.Identity == observationIdentity(vm, moVM) {

		return st.Observed
	}

	return Observe(ctx, ObserveInput{
		Client: client,
		VM:     vm,
		MoVM:   moVM,
	})
}

// IsClaim returns true when the match kind claims an adapter, i.e. the
// Match's AdapterKey identifies the interface's adapter.
func IsClaim(kind MatchKind) bool {
	switch kind {
	case MatchUnitNumber, MatchRecord, MatchIdentity, MatchOrphan, MatchPositional:
		return true
	case MatchNone, MatchAmbiguous:
	}
	return false
}

// DeviceKeyToSpecIdx returns the device key of each claimed adapter mapped to
// the index of the interface that claims it. Interfaces whose match does not
// claim an adapter, i.e. MatchNone and MatchAmbiguous, have no entry. It
// returns nil when info is nil.
func DeviceKeyToSpecIdx(info *Info) map[int32]int {
	if info == nil {
		return nil
	}

	out := make(map[int32]int, len(info.Interfaces))
	for i, iface := range info.Interfaces {
		if IsClaim(iface.Match.Kind) {
			out[iface.Match.AdapterKey] = i
		}
	}
	return out
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

		// TODO: For network migration when the NetworkID is a LogicalSwitchUUID,
		// we'll need to match this like we do for NSXT/VPC.

		ethCard := bEthCard.GetVirtualEthernetCard()
		if id := netIf.Status.ExternalID; id != "" {
			if ethCard.ExternalId != id {
				continue
			}
		} else if ethCard.ExternalId != "" {
			// This Ethernet card has an external ID but the network interface CR does
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
