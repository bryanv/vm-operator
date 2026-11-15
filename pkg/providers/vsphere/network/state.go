// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
)

// ReconcileState is the per-pass network reconciliation state. It is
// allocated by updateVirtualMachine before status is reconciled, carried in
// the context, and discarded when the pass returns. Only orchestration code
// replaces its fields; every phase reads and replaces whole snapshots rather
// than mutating a snapshot another phase holds.
type ReconcileState struct {
	// Policy holds the capability-dependent choices for this pass. It is
	// fixed when the state is allocated.
	Policy Policy

	// Observed is the most recent network observation for this pass.
	Observed *Info
}

// Policy holds the capability-dependent choices for one pass. It is the only
// place the network hot plug capability is read. Everything else in the
// network code is unconditional.
type Policy struct {
	// LiveChanges applies eligible network changes while the VM is powered on.
	LiveChanges bool

	// EditAttachments enables the broader Edit and Disconnect behavior. When
	// false, only the legacy edits are planned.
	EditAttachments bool

	// InterfaceStatus enables spec-first interface status entries and the new
	// per-interface status fields.
	InterfaceStatus bool

	// AdapterTypes honors explicit interface adapter types and replaces an
	// adapter whose type does not match.
	AdapterTypes bool

	// NICsOutOfGOSCHash keeps NIC changes from triggering guest
	// re-customization.
	NICsOutOfGOSCHash bool

	// ChangeVersion guards NIC reconfigure tasks with the VM ChangeVersion.
	ChangeVersion bool
}

// PolicyFromContext returns the network Policy for the current pass.
//
// Each field is wired to pkgcfg.FromContext(ctx).Features.VMNetworkHotPlug
// by the last task of its delivery group. Until then, each field is a literal
// false so the restructured code reproduces today's behavior even when the
// capability is enabled.
func PolicyFromContext(_ context.Context) Policy {
	return Policy{
		LiveChanges:       false,
		EditAttachments:   false,
		InterfaceStatus:   false,
		AdapterTypes:      false,
		NICsOutOfGOSCHash: false,
		ChangeVersion:     false,
	}
}

// ObservationIdentity ties an observation snapshot to the inputs it was
// computed from so that a stale snapshot can be detected.
type ObservationIdentity struct {
	// VMUID is the UID of the VirtualMachine.
	VMUID types.UID

	// Generation is the VirtualMachine metadata.generation.
	Generation int64

	// NetworkHash is a hash of spec.network.interfaces.
	NetworkHash string

	// ChangeVersion is the vSphere VM config.changeVersion.
	ChangeVersion string
}

// Info is an immutable observation snapshot of the VM's network interfaces,
// their provider resources, and the VM's virtual ethernet cards.
type Info struct {
	// Identity describes the inputs this snapshot was computed from.
	Identity ObservationIdentity

	// Enabled is false when spec.network is nil or disabled.
	Enabled bool

	// Interfaces has one entry per spec.network.interfaces entry, in spec
	// order, including the entries that are Pending, Failed, or Unknown.
	Interfaces []InterfaceState

	// Adapters is the inventory of the VM's virtual ethernet cards in
	// observed device order.
	Adapters []ObservedAdapter

	// Owned is the list of VM-owned provider resources from the list call.
	Owned []ProviderRef

	// Complete is true when every read needed to build this snapshot
	// succeeded.
	Complete bool

	// owned holds the raw provider resources listed for Owned.
	owned []ctrlclient.Object
}

// InterfaceState holds everything known about one desired interface in one
// place: its API spec, the provider outcome, its hardware match, and the
// attachment-change classification.
type InterfaceState struct {
	// Spec is an owned copy of the interface's API spec. Its name and unit
	// number are read directly from here. The interface's position in
	// Info.Interfaces supplies presentation order, never identity.
	Spec vmopv1.VirtualMachineNetworkInterfaceSpec

	// Provider is the provider outcome for the interface.
	Provider Resolution

	// Match is the interface's claim on an observed adapter.
	Match Match

	// AttachmentChange classifies whether the matched adapter's attachment
	// differs from the desired one. It is independent of provider readiness.
	AttachmentChange AttachmentChange
}

// ProviderRef identifies a network provider resource. It survives readiness
// changes, and a recreated resource is distinguishable by its UID.
type ProviderRef struct {
	// Type is the network provider type.
	Type pkgcfg.NetworkProviderType

	// Namespace is the namespace of the provider resource.
	Namespace string

	// Name is the name of the provider resource. For the named-network test
	// path, it is the name of the vSphere network.
	Name string

	// UID is the UID of the provider resource. It is empty when the resource
	// does not exist yet and for the named-network test path.
	UID types.UID
}

// ResolutionPhase is the phase of a provider resolution.
type ResolutionPhase string

const (
	// ResolutionReady indicates the provider resource is ready and the
	// normalized target attachment is known.
	ResolutionReady ResolutionPhase = "Ready"

	// ResolutionPending indicates the provider resource is missing or not
	// ready yet.
	ResolutionPending ResolutionPhase = "Pending"

	// ResolutionFailed indicates a terminal configuration or provider
	// failure that requires user intervention.
	ResolutionFailed ResolutionPhase = "Failed"

	// ResolutionUnknown indicates the provider outcome could not be
	// established because a read or write failed.
	ResolutionUnknown ResolutionPhase = "Unknown"
)

// Resolution is the provider outcome for one interface spec.
type Resolution struct {
	// Phase is the provider resolution phase.
	Phase ResolutionPhase

	// Reason is a stable reason for a phase other than Ready.
	Reason string

	// Message is a human-readable message for a phase other than Ready.
	Message string

	// Ref identifies the provider resource.
	Ref ProviderRef

	// Target is the normalized desired attachment. It is set only when Phase
	// is Ready.
	Target *Attachment

	// Err is set only when Phase is Failed or Unknown.
	Err error

	// obj is the raw provider resource, retained for lazy projections such
	// as bootstrap and device construction without a refetch.
	obj ctrlclient.Object

	// backing is the resolved vSphere network reference. It is set only by
	// Prepare for Ready targets.
	backing object.NetworkReference
}

// BackingKind is the kind of network an adapter is attached to.
type BackingKind string

const (
	// BackingKindNetwork is a standard vSphere network.
	BackingKindNetwork BackingKind = "Network"

	// BackingKindDVPortgroup is a distributed virtual portgroup.
	BackingKindDVPortgroup BackingKind = "DVPortgroup"

	// BackingKindOpaque is an opaque network.
	BackingKindOpaque BackingKind = "Opaque"

	// BackingKindNSXSegment is an NSX segment identified by its logical
	// switch UUID.
	BackingKindNSXSegment BackingKind = "NSXSegment"
)

// BackingID is the normalized identity of an adapter's network backing.
type BackingID struct {
	// Kind is the backing kind.
	Kind BackingKind

	// ID is the network MoRef value, portgroup key, opaque network ID, or
	// logical switch UUID, depending on Kind.
	ID string

	// SwitchUUID is the distributed switch UUID. It is used only for
	// DVPortgroup backings.
	SwitchUUID string

	// PortKey is the distributed port key. It is set only when the provider
	// pins a port.
	PortKey string
}

// Intent describes what should happen to a provider-owned field.
type Intent uint8

const (
	// IntentUnknown means the field's value is not known yet.
	IntentUnknown Intent = iota

	// IntentKeep means the field's current value is kept.
	IntentKeep

	// IntentSet means the field is set to the accompanying value.
	IntentSet

	// IntentClear means the field is cleared.
	IntentClear
)

// FieldIntent is an intent for a provider-owned field together with its
// value. On an observed adapter, Op is IntentSet when the device carries a
// value and IntentClear when it carries none.
type FieldIntent struct {
	// Op is the intent.
	Op Intent

	// Value is the value to use when Op is IntentSet.
	Value string
}

// MACPolicy describes whether an adapter's MAC address is pinned or managed
// by vSphere.
type MACPolicy struct {
	// Pinned is the normalized, lower-case pinned MAC address. It is empty
	// when the MAC address is managed by vSphere.
	Pinned string
}

// Attachment is the comparable, normalized identity of what an adapter is
// plugged into. The same type describes both a desired target and an
// observed adapter.
type Attachment struct {
	// Backing is the network backing identity.
	Backing BackingID

	// ExternalID is the adapter's external ID.
	ExternalID FieldIntent

	// MAC is the adapter's MAC address policy.
	MAC MACPolicy
}

// ConnectionState is the observed connection state of an adapter.
type ConnectionState struct {
	// Present is true when the device has connectable information.
	Present bool

	// Connected is meaningful only while the VM is powered on.
	Connected bool

	// StartConnected indicates the device connects when the VM powers on.
	StartConnected bool

	// AllowGuestControl indicates the guest may connect or disconnect the
	// device.
	AllowGuestControl bool
}

// ObservedAdapter is a read-only, normalized view of one of the VM's virtual
// ethernet cards.
type ObservedAdapter struct {
	// Key is the device key.
	Key int32

	// UnitNumber is the device unit number, if any.
	UnitNumber *int32

	// Type is the concrete vSphere type name, e.g. "VirtualVmxnet3".
	Type string

	// MAC is the normalized, lower-case MAC address of the device.
	MAC string

	// AddressType is the device's MAC address type: Manual, Generated, or
	// Assigned.
	AddressType string

	// Attachment is the normalized attachment of the device.
	Attachment Attachment

	// Connect is the device's connection state.
	Connect ConnectionState

	// device is the observed device. It must be treated as read-only; copy it
	// before editing.
	device vimtypes.BaseVirtualDevice
}

// MatchKind is the kind of claim a desired interface has on an observed
// adapter.
type MatchKind string

const (
	// MatchUnitNumber is an exact, exclusive unit number match.
	MatchUnitNumber MatchKind = "UnitNumber"

	// MatchRecord is a match through an operation record.
	MatchRecord MatchKind = "Record"

	// MatchIdentity is a match through MAC address, external ID, or backing.
	MatchIdentity MatchKind = "Identity"

	// MatchOrphan is a match through an old provider resource for the same
	// interface name.
	MatchOrphan MatchKind = "Orphan"

	// MatchPositional is an accepted legacy positional match.
	MatchPositional MatchKind = "Positional"

	// MatchNone means no adapter is claimed.
	MatchNone MatchKind = "None"

	// MatchAmbiguous means several adapters are candidates and all of them
	// are protected.
	MatchAmbiguous MatchKind = "Ambiguous"
)

// Match is a desired interface's claim on an observed adapter.
type Match struct {
	// Kind is the kind of match.
	Kind MatchKind

	// AdapterKey is the claimed adapter's device key. It is valid only for
	// the claiming kinds.
	AdapterKey int32

	// Candidates are the candidate adapter keys of an Ambiguous match.
	Candidates []int32

	// Evidence describes the match for messages and logs.
	Evidence string
}

// AttachmentChange classifies whether the matched adapter's attachment
// differs from the desired one.
type AttachmentChange string

const (
	// AttachmentChangeUnknown means the applied allocation could not be
	// established.
	AttachmentChangeUnknown AttachmentChange = "Unknown"

	// AttachmentChangeUnchanged means the applied attachment is the desired
	// one.
	AttachmentChangeUnchanged AttachmentChange = "Unchanged"

	// AttachmentChangeReplacement means there is positive evidence that the
	// desired attachment differs from the applied one.
	AttachmentChangeReplacement AttachmentChange = "Replacement"
)

type contextKey uint8

const stateContextKey contextKey = 0

// WithContext returns a new context with the provided reconcile state.
func WithContext(
	parent context.Context,
	s *ReconcileState) context.Context {

	return context.WithValue(parent, stateContextKey, s)
}

// FromContext returns the reconcile state from the context.
func FromContext(ctx context.Context) (*ReconcileState, bool) {
	obj := ctx.Value(stateContextKey)
	if obj == nil {
		return nil, false
	}
	val, ok := obj.(*ReconcileState)
	return val, ok && val != nil
}
