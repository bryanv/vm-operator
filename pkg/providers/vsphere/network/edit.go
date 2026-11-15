// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
)

// ErrClearUnsupported is returned when a device change needs to clear an
// Ethernet card field. The pinned SDK omits an empty string field from the
// request, so assigning an empty value does not send the explicit clear the
// vSphere API requires, and no validated serializer exists yet. A caller
// must defer such a change rather than send an edit that silently keeps the
// old value.
var ErrClearUnsupported = errors.New(
	"clearing an Ethernet card field is not supported because an empty value is omitted from the request")

// AddInput holds what BuildAdd needs to build a new Ethernet card.
type AddInput struct {
	// Interface is the name of the interface the card is for. It is used in
	// error messages.
	Interface string

	// Target is the ready provider attachment of the interface.
	Target *Attachment

	// Backing is the Ethernet card backing of the target network. The card
	// takes ownership of it.
	Backing vimtypes.BaseVirtualDeviceBackingInfo

	// UnitNumber places the card at a unit number when not nil. The card's
	// controller key is left for vSphere to resolve.
	UnitNumber *int32
}

// BuildAdd builds a new Ethernet card for an interface: a VMXNET3 card with
// the target's backing and external ID, and the target's pinned MAC address
// as a manual address or else a generated address that vSphere assigns. The
// MAC address is written as given. The card carries no key and no
// connection information, so vSphere applies its connection defaults.
func BuildAdd(in AddInput) (vimtypes.BaseVirtualDevice, error) {
	if in.Target == nil {
		return nil, fmt.Errorf("network interface %q has no target attachment", in.Interface)
	}
	if in.Backing == nil {
		return nil, fmt.Errorf("network interface %q has no Ethernet card backing", in.Interface)
	}

	// TODO(T037): With Policy.AdapterTypes, honor an explicit spec.type, add
	// an explicit default Connectable, and fold in compatible NIC tuning.
	ethDev, err := object.EthernetCardTypes().CreateEthernetCard("vmxnet3", in.Backing)
	if err != nil {
		return nil, fmt.Errorf("unable to create Ethernet card for network interface %q: %w",
			in.Interface, err)
	}

	ethCard := ethDev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	if in.Target.ExternalID.Op == IntentSet {
		ethCard.ExternalId = in.Target.ExternalID.Value
	}
	if mac := in.Target.MAC.Pinned; mac != "" {
		ethCard.MacAddress = mac
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
	} else {
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeGenerated)
	}

	if in.UnitNumber != nil {
		unitNumber := *in.UnitNumber
		ethCard.UnitNumber = &unitNumber
	}

	return ethDev, nil
}

// AttachmentEditInput holds what BuildAttachmentEdit needs to edit an
// adapter's attachment.
type AttachmentEditInput struct {
	// Target is the ready provider attachment to apply.
	Target *Attachment

	// Backing is a complete, fresh Ethernet card backing of the target
	// network. It is required only when the adapter's backing changes, and
	// the edited card takes ownership of it.
	Backing vimtypes.BaseVirtualDeviceBackingInfo

	// PowerState is the VM's current power state. The runtime connection is
	// changed only while the VM is powered on.
	PowerState vimtypes.VirtualMachinePowerState

	// Reconnect applies the reconnect half of the connection policy in the
	// same edit: the adapter is connected while the VM is powered on, starts
	// connected, and allows guest control. Previous connection settings are
	// not restored. When false, the adapter's connection settings are kept.
	Reconnect bool
}

// BuildAttachmentEdit edits the adapter's single mutable copy in c so that
// the adapter is attached to the target, and returns that copy.
//
// The copy keeps the adapter's concrete type, key, controller key, unit
// number, PCI slot, and every field that other reconcilers own, such as
// NUMA, VMXNET3 and UPT fields, resource allocation, and wake-on-LAN. The
// backing is kept when it identifies the target network and is otherwise
// replaced with the input's fresh backing. The external ID follows the
// target's intent, and the address type and MAC address follow the MAC
// transition table in replacement.md.
//
// Every input is validated before the copy is made, so on error c is
// unchanged unless another contributor already edits the adapter. A change
// that needs to clear a field, i.e. an external ID with a Clear intent, a
// pinned MAC address that becomes vSphere-managed, or a subnet ID on an
// adapter whose backing changes, returns an error wrapping
// ErrClearUnsupported.
func BuildAttachmentEdit(
	c *DeviceChanges,
	observed ObservedAdapter,
	in AttachmentEditInput) (vimtypes.BaseVirtualDevice, error) {

	have, err := observedEthernetCard(observed)
	if err != nil {
		return nil, err
	}
	if in.Target == nil {
		return nil, fmt.Errorf("cannot edit device %d: no target attachment", observed.Key)
	}
	target := *in.Target

	sameBacking := backingUnchanged(target, observed)
	if !sameBacking && in.Backing == nil {
		return nil, fmt.Errorf(
			"cannot edit device %d: its backing changes and no Ethernet card backing is given",
			observed.Key)
	}

	// A subnet ID belongs to the previous attachment and must not be
	// transplanted onto a new one, but it cannot be cleared on the wire.
	if !sameBacking && have.SubnetId != "" {
		return nil, fmt.Errorf("cannot edit device %d: its subnet ID %q must be cleared: %w",
			observed.Key, have.SubnetId, ErrClearUnsupported)
	}

	setExternalID, err := externalIDEdit(observed.Key, target.ExternalID)
	if err != nil {
		return nil, err
	}

	pinnedMAC, err := macEdit(observed.Key, target.MAC, have.AddressType)
	if err != nil {
		return nil, err
	}

	dev, err := c.Edit(observed)
	if err != nil {
		return nil, err
	}
	ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()

	if !sameBacking {
		ethCard.Backing = in.Backing
	}

	if setExternalID != nil {
		ethCard.ExternalId = *setExternalID
	}

	if pinnedMAC != "" {
		ethCard.MacAddress = pinnedMAC
		ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
	}

	if in.Reconnect {
		connect := connectInfo(ethCard)
		if in.PowerState == vimtypes.VirtualMachinePowerStatePoweredOn {
			connect.Connected = true
		}
		connect.StartConnected = true
		connect.AllowGuestControl = true
	}

	return dev, nil
}

// BuildDisconnect edits the adapter's single mutable copy in c so that the
// adapter is disconnected while its replacement attachment is pending, and
// returns that copy. The adapter is not connected, does not start
// connected, and does not allow guest control; its attachment and every
// other field are kept. The runtime connection is meaningful only while the
// VM is powered on, but it is reported as not connected in every power
// state, so it is cleared regardless.
func BuildDisconnect(
	c *DeviceChanges,
	observed ObservedAdapter) (vimtypes.BaseVirtualDevice, error) {

	if _, err := observedEthernetCard(observed); err != nil {
		return nil, err
	}

	dev, err := c.Edit(observed)
	if err != nil {
		return nil, err
	}
	ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()

	connect := connectInfo(ethCard)
	connect.Connected = false
	connect.StartConnected = false
	connect.AllowGuestControl = false

	return dev, nil
}

// observedEthernetCard returns the observed adapter's device as a read-only
// Ethernet card.
func observedEthernetCard(observed ObservedAdapter) (*vimtypes.VirtualEthernetCard, error) {
	if observed.device == nil {
		return nil, fmt.Errorf("cannot edit device %d: the observed device is not available",
			observed.Key)
	}
	ethDev, ok := observed.device.(vimtypes.BaseVirtualEthernetCard)
	if !ok {
		return nil, fmt.Errorf("cannot edit device %d: %T is not an Ethernet card",
			observed.Key, observed.device)
	}
	return ethDev.GetVirtualEthernetCard(), nil
}

// backingUnchanged reports whether the adapter's backing already identifies
// the target network, so the observed backing must be kept. An NSX segment
// is identified by its external ID rather than by the portgroup that carries
// it, so its backing changes only when the external ID changes.
func backingUnchanged(target Attachment, observed ObservedAdapter) bool {
	if target.Backing.Kind == BackingKindNSXSegment {
		switch target.ExternalID.Op {
		case IntentSet:
			have := observed.Attachment.ExternalID
			return have.Op == IntentSet && have.Value == target.ExternalID.Value
		case IntentUnknown, IntentKeep, IntentClear:
		}
		return true
	}
	return backingMatches(target.Backing, observed)
}

// externalIDEdit returns the external ID to write, or nil to keep the
// adapter's external ID.
func externalIDEdit(key int32, intent FieldIntent) (*string, error) {
	switch intent.Op {
	case IntentKeep:
		return nil, nil
	case IntentSet:
		if intent.Value != "" {
			value := intent.Value
			return &value, nil
		}
		// An empty value is a clear.
	case IntentClear:
	case IntentUnknown:
		return nil, fmt.Errorf("cannot edit device %d: the target external ID is not known", key)
	default:
		return nil, fmt.Errorf("cannot edit device %d: invalid external ID intent %d", key, intent.Op)
	}
	return nil, fmt.Errorf("cannot edit device %d: its external ID must be cleared: %w",
		key, ErrClearUnsupported)
}

// macEdit applies the MAC transition table. It returns the pinned MAC
// address to write with a manual address type, or an empty string to keep
// the adapter's address type and MAC address.
func macEdit(key int32, target MACPolicy, observedAddressType string) (string, error) {
	if target.Pinned != "" {
		// Write the destination's MAC address without adopting the old one.
		return target.Pinned, nil
	}

	// A vSphere-managed MAC address. Keep a Generated or Assigned address
	// with its address type; vSphere reports a requested Generated address
	// as Assigned. A manual address belongs to the previous provider and
	// must not be kept, but a reset to a vSphere-managed address has no
	// validated payload.
	if strings.EqualFold(observedAddressType, string(vimtypes.VirtualEthernetCardMacTypeManual)) {
		return "", fmt.Errorf(
			"cannot edit device %d: its manual MAC address must be cleared for a vSphere-managed address: %w",
			key, ErrClearUnsupported)
	}
	return "", nil
}

// connectInfo returns the card's connection information, allocating it when
// missing.
func connectInfo(ethCard *vimtypes.VirtualEthernetCard) *vimtypes.VirtualDeviceConnectInfo {
	if ethCard.Connectable == nil {
		ethCard.Connectable = &vimtypes.VirtualDeviceConnectInfo{}
	}
	return ethCard.Connectable
}
