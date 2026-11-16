// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcond "github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
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

	// Plan is the network plan built from Observed, if any.
	Plan *Plan

	// Submitted is true when the device changes of Plan were handed to this
	// pass's reconfigure. The observed hardware no longer describes the VM
	// once they are, so the network cannot be ready until the next pass
	// observes the result.
	Submitted bool

	// RequeueAfter is the deferred retry interval to apply at the end of the
	// pass. Zero means no network retry is needed.
	RequeueAfter time.Duration
}

// PendingRequeueAfter is the bounded retry interval for a network wait that
// has no dependable wake-up, such as a provider resource that is not ready
// yet or a provider read that failed. It matches the default
// CreateVMRequeueDelay and PoweredOnVMHasIPRequeueDelay.
const PendingRequeueAfter = 10 * time.Second

// Reasons that Readiness reports in addition to the reasons of planned
// actions.
const (
	// ReadinessReasonNotEvaluated is used when the network has not been
	// observed and planned on this pass, for example because the state or
	// its plan is missing.
	ReadinessReasonNotEvaluated = "NetworkNotEvaluated"

	// ReadinessReasonObservationStale is used when the observation or the
	// plan no longer describes the current VM inputs.
	ReadinessReasonObservationStale = "ObservationStale"

	// ReadinessReasonObservationIncomplete is used when a read needed to
	// observe the network failed.
	ReadinessReasonObservationIncomplete = "ObservationIncomplete"

	// ReadinessReasonHardwarePending is used when network device changes
	// were submitted on this pass, or when a desired interface's hardware
	// is not in its desired state yet.
	ReadinessReasonHardwarePending = vmopv1.VirtualMachineNetworkInterfaceHardwarePendingReason

	// ReadinessReasonMACAddressPending is used when the adapter of a desired
	// interface does not report a MAC address yet.
	ReadinessReasonMACAddressPending = "MACAddressPending"

	// ReadinessReasonMACMismatch is used when the adapter of a desired
	// interface does not have the MAC address its provider pins.
	ReadinessReasonMACMismatch = vmopv1.VirtualMachineNetworkInterfaceMACMismatchReason

	// ReadyConditionNotReadyReason is the VirtualMachineNetworkReady
	// condition reason when the network is not ready for several reasons,
	// or when the create path's prerequisites failed.
	ReadyConditionNotReadyReason = "NotReady"
)

// PendingInterface describes one reason the network is not ready.
type PendingInterface struct {
	// Name is the name of the desired interface. It is empty for an adapter
	// that no desired interface owns and for a reason that concerns the
	// whole network.
	Name string

	// Adapter is the device key of the adapter that no desired interface
	// owns. It is zero otherwise.
	Adapter int32

	// Reason is a stable reason.
	Reason string

	// Message is a human-readable message.
	Message string
}

// String returns the pending reason in the form used by condition messages.
func (p PendingInterface) String() string {
	var who string
	switch {
	case p.Name != "":
		who = p.Name
	case p.Adapter != 0:
		who = fmt.Sprintf("adapter %d", p.Adapter)
	}

	s := p.Reason
	if p.Message != "" {
		s += " (" + p.Message + ")"
	}
	if who != "" {
		s = who + ": " + s
	}
	return s
}

// Readiness is the derived network readiness of one pass. It is computed by
// ReconcileState.Readiness from the current observation, plan, and task
// submission state, and is never stored.
type Readiness struct {
	// Known is true when a complete observation and a plan built from it
	// both describe the current VM inputs, and no network device changes
	// were submitted on this pass.
	Known bool

	// ProviderReady is true when every desired interface's provider
	// resource is Ready.
	ProviderReady bool

	// HardwareConverged is true when every desired interface claims an
	// adapter that needs no change, every adapter is claimed, and the plan
	// holds no other change, including a deferred one.
	HardwareConverged bool

	// MACsResolved is true when every claimed adapter reports a MAC address
	// and agrees with the MAC address its provider pins.
	MACsResolved bool

	// Pending holds the reasons the network is not ready: first the reasons
	// that concern the whole network, then the desired interfaces' reasons
	// sorted by interface name, then the reasons of adapters that no desired
	// interface owns, sorted by device key.
	Pending []PendingInterface
}

// Ready returns true when the network is ready. Bootstrap and the
// powered-off to powered-on transition both require it, in addition to their
// own lifecycle prerequisites.
func (r Readiness) Ready() bool {
	return r.Known && r.ProviderReady && r.HardwareConverged && r.MACsResolved
}

// PendingNames returns the names of the desired interfaces that are not
// ready, sorted.
func (r Readiness) PendingNames() []string {
	var names []string
	for _, p := range r.Pending {
		if p.Name != "" && !slices.Contains(names, p.Name) {
			names = append(names, p.Name)
		}
	}
	return names
}

// Readiness derives the network readiness of the current pass. It performs no
// I/O and does not mutate the state. It is safe to call on a nil state, in
// which case the network is not ready: an absent state is never treated as an
// empty network.
//
// The network is ready when all of these hold:
//
//   - The state holds an observation and a plan built from it, both describe
//     the current VM and vSphere VM, the observation is complete, and no
//     network device changes were submitted on this pass.
//   - Every desired interface's provider resource is Ready.
//   - Every desired interface claims an adapter, its planned action is a
//     NoOp, and its attachment is not being replaced. A NoOp alone is not
//     enough, because a pending replacement keeps its old adapter untouched.
//   - Every observed adapter is claimed. An adapter that a required removal
//     targets is still present until an observation shows otherwise, even
//     while it is disconnected.
//   - Every claimed adapter reports a MAC address, and that address agrees
//     with the MAC address the interface's provider pins.
func (s *ReconcileState) Readiness(vmCtx pkgctx.VirtualMachineContext) Readiness {
	if s == nil || s.Observed == nil || s.Plan == nil {
		return Readiness{
			Pending: []PendingInterface{{
				Reason:  ReadinessReasonNotEvaluated,
				Message: "the network has not been observed and planned on this pass",
			}},
		}
	}

	info, plan := s.Observed, s.Plan
	r := Readiness{
		ProviderReady:     true,
		HardwareConverged: true,
		MACsResolved:      true,
	}

	switch {
	case plan.Identity != info.Identity ||
		vmCtx.VM == nil ||
		info.Identity != observationIdentity(vmCtx.VM, vmCtx.MoVM):
		r.Pending = append(r.Pending, PendingInterface{
			Reason:  ReadinessReasonObservationStale,
			Message: "the network observation does not describe the current VM",
		})
	case !info.Complete:
		r.Pending = append(r.Pending, PendingInterface{
			Reason:  ReadinessReasonObservationIncomplete,
			Message: "the network observation is incomplete",
		})
	case s.Submitted:
		r.Pending = append(r.Pending, PendingInterface{
			Reason:  ReadinessReasonHardwarePending,
			Message: "network device changes were submitted and are not observed yet",
		})
	default:
		r.Known = true
	}

	adapters := make(map[int32]ObservedAdapter, len(info.Adapters))
	for _, a := range info.Adapters {
		adapters[a.Key] = a
	}

	var named []PendingInterface
	for _, iface := range info.Interfaces {
		if p, ok := interfaceReadiness(&r, iface, plan, adapters); !ok {
			named = append(named, p)
		}
	}

	var unowned []PendingInterface
	for _, a := range info.Adapters {
		if info.Disposition[a.Key] == AdapterDispositionClaimed {
			continue
		}
		r.HardwareConverged = false
		p := PendingInterface{
			Adapter: a.Key,
			Reason:  PlanReasonRemovalPending,
			Message: fmt.Sprintf("adapter %d is not claimed by any network interface", a.Key),
		}
		if action, ok := plan.adapterAction(a.Key); ok {
			p.Reason, p.Message = action.Reason, action.Message
		}
		unowned = append(unowned, p)
	}

	slices.SortStableFunc(named, func(a, b PendingInterface) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortStableFunc(unowned, func(a, b PendingInterface) int {
		return cmp.Compare(a.Adapter, b.Adapter)
	})
	r.Pending = append(append(r.Pending, named...), unowned...)

	return r
}

// interfaceReadiness evaluates one desired interface, clears the predicates
// of r that it fails, and returns the reason it is not ready. It returns
// false when the interface is not ready.
func interfaceReadiness(
	r *Readiness,
	iface InterfaceState,
	plan *Plan,
	adapters map[int32]ObservedAdapter) (PendingInterface, bool) {

	name := iface.Spec.Name
	action, hasAction := plan.InterfaceAction(name)

	pending := func(reason, message string) PendingInterface {
		if hasAction && action.Reason != "" {
			reason = action.Reason
		}
		if hasAction && action.Message != "" {
			message = action.Message
		}
		return PendingInterface{Name: name, Reason: reason, Message: message}
	}

	switch iface.Provider.Phase {
	case ResolutionReady:
	case ResolutionPending:
		r.ProviderReady = false
		return pending(PlanReasonProviderPending, iface.Provider.Message), false
	case ResolutionFailed:
		r.ProviderReady = false
		return pending(PlanReasonProviderFailed, iface.Provider.Message), false
	case ResolutionUnknown:
		r.ProviderReady = false
		return pending(PlanReasonProviderUnknown, iface.Provider.Message), false
	default:
		r.ProviderReady = false
		return pending(PlanReasonProviderUnknown, iface.Provider.Message), false
	}

	if !hasAction || action.Kind != ActionNoOp ||
		!IsClaim(iface.Match.Kind) ||
		iface.AttachmentChange == AttachmentChangeReplacement {

		r.HardwareConverged = false
		return pending(ReadinessReasonHardwarePending, fmt.Sprintf(
			"the hardware of network interface %q is not in its desired state", name)), false
	}

	a, ok := adapters[iface.Match.AdapterKey]
	if !ok {
		r.HardwareConverged = false
		return PendingInterface{
			Name:    name,
			Reason:  PlanReasonDeviceNotPresent,
			Message: fmt.Sprintf("adapter %d is not present", iface.Match.AdapterKey),
		}, false
	}

	if a.MAC == "" {
		r.MACsResolved = false
		return PendingInterface{
			Name:    name,
			Reason:  ReadinessReasonMACAddressPending,
			Message: fmt.Sprintf("adapter %d does not report a MAC address yet", a.Key),
		}, false
	}

	if t := iface.Provider.Target; t != nil && t.MAC.Pinned != "" &&
		!strings.EqualFold(t.MAC.Pinned, a.MAC) {

		r.MACsResolved = false
		return PendingInterface{
			Name:   name,
			Reason: ReadinessReasonMACMismatch,
			Message: fmt.Sprintf("adapter %d has MAC address %s instead of %s",
				a.Key, a.MAC, t.MAC.Pinned),
		}, false
	}

	return PendingInterface{}, true
}

// SetReadyCondition sets the VM's VirtualMachineNetworkReady condition. The
// create path, which waits for the provider prerequisites before creating
// the VM, and the update path, which reports the derived Readiness, both use
// it so the condition has one owner:
//
//   - A non-nil err marks the condition False with the NotReady reason.
//   - Otherwise a VM without network interfaces, i.e. a nil or disabled
//     spec.network or one without interfaces, has no condition, as it
//     historically has not.
//   - Otherwise no pending reasons mark the condition True.
//   - Otherwise the condition is False. Its reason is the pending reason
//     when they all share one, or NotReady, and its message lists every
//     pending reason in the order Readiness reports them.
func SetReadyCondition(
	vm *vmopv1.VirtualMachine,
	hasInterfaces bool,
	pending []PendingInterface,
	err error) {

	switch {
	case err != nil:
		pkgcond.MarkError(vm, vmopv1.VirtualMachineConditionNetworkReady,
			ReadyConditionNotReadyReason, err)
	case !hasInterfaces:
		pkgcond.Delete(vm, vmopv1.VirtualMachineConditionNetworkReady)
	case len(pending) == 0:
		pkgcond.MarkTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)
	default:
		reason := pending[0].Reason
		messages := make([]string, 0, len(pending))
		for _, p := range pending {
			if p.Reason != reason {
				reason = ReadyConditionNotReadyReason
			}
			messages = append(messages, p.String())
		}
		pkgcond.MarkFalse(vm, vmopv1.VirtualMachineConditionNetworkReady,
			reason, "%s", strings.Join(messages, "; "))
	}
}

// PendingRetryAfter returns the bounded retry interval a pass with this
// observation and plan should end with, or zero. A retry is needed only for a
// wait that may end without a dependable wake-up: a provider resource that is
// not ready yet, a provider outcome or backing that could not be read, or an
// incomplete observation. A change deferred until the VM is powered off and a
// terminal provider failure do not need one, because the change in the VM's
// spec or in the provider resource that ends them triggers a reconcile.
func PendingRetryAfter(info *Info, plan *Plan) time.Duration {
	if info == nil {
		return 0
	}
	if !info.Complete {
		return PendingRequeueAfter
	}
	for _, iface := range info.Interfaces {
		switch iface.Provider.Phase {
		case ResolutionPending, ResolutionUnknown:
			return PendingRequeueAfter
		case ResolutionReady, ResolutionFailed:
		}
	}
	if plan != nil {
		for _, a := range plan.Actions {
			if a.Kind == ActionDeferred && a.Reason == PlanReasonBackingUnavailable {
				return PendingRequeueAfter
			}
		}
	}
	return 0
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
// capability is enabled. NICsOutOfGOSCHash is wired.
func PolicyFromContext(ctx context.Context) Policy {
	on := pkgcfg.FromContext(ctx).Features.VMNetworkHotPlug
	return Policy{
		LiveChanges:       false,
		EditAttachments:   false,
		InterfaceStatus:   false,
		AdapterTypes:      false,
		NICsOutOfGOSCHash: on,
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

	// Disposition holds the disposition of every adapter, keyed by device
	// key.
	Disposition map[int32]AdapterDisposition

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
