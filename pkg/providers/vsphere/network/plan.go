// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"errors"
	"fmt"

	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// ActionKind is the kind of change the planner proposes for a desired
// interface or an observed adapter.
type ActionKind string

const (
	// ActionNoOp keeps the interface's current hardware as it is.
	ActionNoOp ActionKind = "NoOp"

	// ActionAdd adds a new adapter for the interface.
	ActionAdd ActionKind = "Add"

	// ActionEdit edits the interface's matched adapter in place.
	ActionEdit ActionKind = "Edit"

	// ActionDisconnect disconnects the interface's matched adapter while its
	// replacement attachment is pending.
	ActionDisconnect ActionKind = "Disconnect"

	// ActionRemove removes an adapter that no desired interface owns.
	ActionRemove ActionKind = "Remove"

	// ActionReplace removes the interface's matched adapter and adds a new one
	// in the same reconfigure.
	ActionReplace ActionKind = "Replace"

	// ActionDeferred means a change is needed but cannot be planned or applied
	// on this pass.
	ActionDeferred ActionKind = "Deferred"
)

// Stable reasons carried by planned actions. Where an interface condition
// reason exists for the same situation, the planner uses its value.
const (
	// PlanReasonAttachmentUnchanged is used for a NoOp whose matched adapter
	// already has the desired attachment.
	PlanReasonAttachmentUnchanged = "AttachmentUnchanged"

	// PlanReasonAttachmentUnknown is used for a NoOp whose matched adapter is
	// retained because its applied attachment cannot be compared with the
	// desired one.
	PlanReasonAttachmentUnknown = "AttachmentUnknown"

	// PlanReasonProviderPending is used when the interface's provider
	// resource is not ready.
	PlanReasonProviderPending = vmopv1.VirtualMachineNetworkInterfaceProviderPendingReason

	// PlanReasonProviderFailed is used when the interface's provider outcome
	// is a terminal failure.
	PlanReasonProviderFailed = "ProviderFailed"

	// PlanReasonProviderUnknown is used when the interface's provider outcome
	// could not be read.
	PlanReasonProviderUnknown = "ProviderUnknown"

	// PlanReasonReplacementPending is used when the matched adapter remains
	// on its previous attachment while the replacement provider resource is
	// pending.
	PlanReasonReplacementPending = vmopv1.VirtualMachineNetworkInterfaceReplacementPendingReason

	// PlanReasonDeviceNotPresent is used for an Add because no adapter exists
	// for the interface.
	PlanReasonDeviceNotPresent = vmopv1.VirtualMachineNetworkInterfaceDeviceNotPresentReason

	// PlanReasonAttachmentPending is used for an Edit or Replace because the
	// matched adapter is not attached to the desired network.
	PlanReasonAttachmentPending = vmopv1.VirtualMachineNetworkInterfaceAttachmentPendingReason

	// PlanReasonRemovalPending is used for a Remove of an adapter that no
	// desired interface owns.
	PlanReasonRemovalPending = vmopv1.VirtualMachineNetworkInterfaceRemovalPendingReason

	// PlanReasonInterfaceMappingAmbiguous is used when several adapters are
	// candidates for the interface.
	PlanReasonInterfaceMappingAmbiguous = "InterfaceMappingAmbiguous"

	// PlanReasonPowerOffRequired is used when a hardware change cannot be
	// applied in the VM's current power state.
	PlanReasonPowerOffRequired = vmopv1.VirtualMachinePowerOffRequiredReason

	// PlanReasonNetworkChangesNotAllowed is used when the dispatch path this
	// plan is built for does not apply network changes.
	PlanReasonNetworkChangesNotAllowed = "NetworkChangesNotAllowed"

	// PlanReasonBackingUnavailable is used when the ethernet card backing of
	// a Ready interface was not resolved, so no device can be built for it.
	PlanReasonBackingUnavailable = "BackingUnavailable"

	// PlanReasonDeviceChangeFailed is used when the device change for an
	// action could not be composed.
	PlanReasonDeviceChangeFailed = "DeviceChangeFailed"
)

// Action is one planned outcome for a desired interface or for an observed
// adapter that no desired interface owns.
type Action struct {
	// Kind is the planned action.
	Kind ActionKind

	// Proposed is the hardware action a Deferred action stands for. It is
	// empty for other kinds and when no hardware action can be proposed yet,
	// e.g. while the interface's provider resource is pending.
	Proposed ActionKind

	// Interface is the name of the desired interface. It is empty for an
	// adapter that no desired interface owns.
	Interface string

	// Adapter is the device key of the existing adapter the action keeps,
	// edits, removes, or replaces. It is zero when no adapter is involved.
	Adapter int32

	// DeviceKey is the temporary device key of the adapter an Add or Replace
	// adds. It is zero for other kinds.
	DeviceKey int32

	// Reason is a stable reason for the action.
	Reason string

	// Message is a human-readable message for the action.
	Message string
}

// changes reports whether the action carries a hardware change.
func (a Action) changes() bool {
	switch a.Kind {
	case ActionAdd, ActionEdit, ActionDisconnect, ActionRemove, ActionReplace:
		return true
	case ActionNoOp, ActionDeferred:
	}
	return false
}

// Plan is the planner's result for one network observation. It holds one
// action per desired interface, in spec order, followed by one action per
// adapter to remove, in observed device order, and the device changes that
// implement the actions that are not deferred.
type Plan struct {
	// Identity is the identity of the observation the plan was built from.
	Identity ObservationIdentity

	// Actions are the planned actions.
	Actions []Action

	changes *DeviceChanges
}

// DeviceChanges returns the device changes that implement the plan. It is
// never nil. Later contributors to the same reconfigure, such as NIC tuning,
// must edit the devices through it.
func (p *Plan) DeviceChanges() *DeviceChanges {
	if p.changes == nil {
		p.changes = NewDeviceChanges()
	}
	return p.changes
}

// InterfaceAction returns the action planned for the named desired
// interface.
func (p *Plan) InterfaceAction(name string) (Action, bool) {
	for _, a := range p.Actions {
		if a.Interface == name {
			return a, true
		}
	}
	return Action{}, false
}

// PlanInput holds everything BuildPlan needs besides the observation.
type PlanInput struct {
	// Policy holds the capability-dependent choices for the pass.
	Policy Policy

	// PowerState is the VM's current vSphere power state. Hardware changes
	// are applied only while the VM is powered off unless Policy.LiveChanges
	// permits them.
	PowerState vimtypes.VirtualMachinePowerState

	// ApplyChanges is true when the dispatch path the plan is built for
	// applies network hardware changes. Today the powered-off update path
	// always applies them, while the powered-off resize path applies them
	// only with MutableNetworks.
	ApplyChanges bool

	// MutableNetworks is the value of the MutableNetworks feature. Without
	// it, an adapter that carries a previous provider resource's attachment
	// for the same interface name is replaced rather than edited, as today.
	MutableNetworks bool

	// UnitNumbers is the value of the VMNetworkUnitNumbers feature. With it,
	// an added card is placed at its interface's declared unit number, the
	// only adapter MatchInterfaces will claim for a numbered interface.
	UnitNumbers bool

	// CardBackings holds the ethernet card backing of each Ready interface,
	// keyed by interface name. See EthernetCardBackings. A Ready interface
	// without one cannot be added or edited.
	CardBackings map[string]vimtypes.BaseVirtualDeviceBackingInfo
}

// EthernetCardBackings resolves the ethernet card backing of every Ready
// interface whose network backing Prepare resolved, keyed by interface name.
// Unlike BuildPlan, it performs vCenter I/O. A failure for one interface is
// joined into the returned error, and the interface is absent from the map.
func EthernetCardBackings(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	info *Info) (map[string]vimtypes.BaseVirtualDeviceBackingInfo, error) {

	if info == nil {
		return nil, nil
	}

	ctx = pkgctx.WithVCOpID(ctx, vm, "ethernetCardBackings")

	var errs []error
	out := map[string]vimtypes.BaseVirtualDeviceBackingInfo{}
	for _, iface := range info.Interfaces {
		r := iface.Provider
		if r.Phase != ResolutionReady || r.backing == nil {
			continue
		}
		backing, err := r.backing.EthernetCardBackingInfo(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"unable to get Ethernet card backing info for network interface %q (%s): %w",
				iface.Spec.Name, r.backing.Reference().Value, err))
			continue
		}
		out[iface.Spec.Name] = backing
	}

	return out, errors.Join(errs...)
}

// BuildPlan plans the network hardware changes for an observation. It is a
// pure function: it performs no Kubernetes or vCenter I/O, and it never
// mutates info or the devices info refers to.
//
// It first decides one action per desired interface and per adapter that no
// desired interface owns, then defers the hardware changes that cannot be
// applied on this pass, and finally composes the device changes for the
// remaining actions. Removals are composed in observed device order before
// any other change, and the other changes follow in spec order.
//
// With every Policy field false, the device changes match what
// ReconcileNetworkInterfaces produces today for the same claims.
func BuildPlan(info *Info, in PlanInput) *Plan {
	p := &Plan{changes: NewDeviceChanges()}
	if info == nil {
		return p
	}
	p.Identity = info.Identity

	adapters := make(map[int32]ObservedAdapter, len(info.Adapters))
	for _, a := range info.Adapters {
		adapters[a.Key] = a
	}

	// Decide.
	for _, iface := range info.Interfaces {
		p.Actions = append(p.Actions, decideInterface(iface, in))
	}
	for _, a := range info.Adapters {
		if action, ok := decideAdapter(info, a); ok {
			p.Actions = append(p.Actions, action)
		}
	}

	// Defer.
	for i := range p.Actions {
		p.Actions[i] = deferIfBlocked(p.Actions[i], in)
	}

	// Compose. Build the new cards first so that an action whose card cannot
	// be built is deferred before its removal is composed. Only interface
	// actions, which come first and in spec order, add or edit devices, so
	// an action's index is also its interface's index.
	newCards := make([]vimtypes.BaseVirtualDevice, len(p.Actions))
	for i, action := range p.Actions {
		switch action.Kind {
		case ActionAdd, ActionReplace, ActionEdit:
			card, err := buildPlannedAdd(info.Interfaces[i], in)
			if err != nil {
				p.Actions[i] = deferred(action, PlanReasonDeviceChangeFailed, err.Error())
				continue
			}
			newCards[i] = card
		case ActionNoOp, ActionDisconnect, ActionRemove, ActionDeferred:
		}
	}

	// Remove in observed device order.
	removing := map[int32]int{}
	for i, action := range p.Actions {
		switch action.Kind {
		case ActionRemove, ActionReplace:
			removing[action.Adapter] = i
		case ActionNoOp, ActionAdd, ActionEdit, ActionDisconnect, ActionDeferred:
		}
	}
	for _, a := range info.Adapters {
		i, ok := removing[a.Key]
		if !ok {
			continue
		}
		// Only adapters that are not edited are removed, so this fails only
		// when the observed device is not available. A Replace whose removal
		// fails must not add its new card either.
		if err := p.changes.Remove(a); err != nil {
			p.Actions[i] = deferred(p.Actions[i], PlanReasonDeviceChangeFailed, err.Error())
		}
	}

	for i, action := range p.Actions {
		switch action.Kind {
		case ActionAdd, ActionReplace:
			p.Actions[i].DeviceKey = p.changes.Add(newCards[i])
		case ActionEdit:
			if err := p.legacyAttachmentEdit(adapters[action.Adapter], newCards[i]); err != nil {
				p.Actions[i] = deferred(action, PlanReasonDeviceChangeFailed, err.Error())
			}
		case ActionNoOp, ActionDisconnect, ActionRemove, ActionDeferred:
		}
	}

	return p
}

// decideInterface decides the action for one desired interface, before
// eligibility is considered.
func decideInterface(iface InterfaceState, in PlanInput) Action {
	r := iface.Provider
	m := iface.Match
	action := Action{Interface: iface.Spec.Name}

	claimed := isClaim(m.Kind)
	if claimed {
		action.Adapter = m.AdapterKey
	}

	switch r.Phase {
	case ResolutionFailed, ResolutionUnknown:
		reason := PlanReasonProviderFailed
		if r.Phase == ResolutionUnknown {
			reason = PlanReasonProviderUnknown
		}
		action.Reason = reason
		action.Message = r.Message
		if claimed {
			// The matched adapter is retained as it is.
			action.Kind = ActionNoOp
		} else {
			action.Kind = ActionDeferred
		}
		return action

	case ResolutionPending:
		action.Message = r.Message
		if !claimed {
			// The interface keeps its place in the spec and its provider
			// resource until the resource is ready.
			action.Kind = ActionDeferred
			action.Reason = PlanReasonProviderPending
			return action
		}
		action.Kind = ActionNoOp
		action.Reason = PlanReasonProviderPending
		if iface.AttachmentChange == AttachmentChangeReplacement {
			// TODO(T027): With Policy.EditAttachments and the operation
			// evidence that distinguishes an unfinished handoff from a later
			// guest disconnect, plan ActionDisconnect here. Until then the
			// old adapter stays connected.
			action.Reason = PlanReasonReplacementPending
		}
		return action

	case ResolutionReady:
	}

	switch m.Kind {
	case MatchNone:
		// TODO(T037): With Policy.AdapterTypes, honor an explicit spec.type.
		action.Kind = ActionAdd
		action.Reason = PlanReasonDeviceNotPresent
		action.Message = fmt.Sprintf("add an adapter for network interface %q", iface.Spec.Name)
		return action

	case MatchAmbiguous:
		action.Kind = ActionDeferred
		action.Reason = PlanReasonInterfaceMappingAmbiguous
		action.Message = m.Evidence
		return action

	case MatchUnitNumber, MatchRecord, MatchIdentity, MatchOrphan, MatchPositional:
	}

	// TODO(T037): With Policy.AdapterTypes, an explicit spec.type that differs
	// from the matched adapter's type is a Replace, decided before any
	// attachment-only Edit.

	switch iface.AttachmentChange {
	case AttachmentChangeUnchanged:
		action.Kind = ActionNoOp
		action.Reason = PlanReasonAttachmentUnchanged
	case AttachmentChangeUnknown:
		action.Kind = ActionNoOp
		action.Reason = PlanReasonAttachmentUnknown
		action.Message = fmt.Sprintf(
			"the attachment of adapter %d cannot be compared with the desired attachment",
			m.AdapterKey)
	case AttachmentChangeReplacement:
		action.Reason = PlanReasonAttachmentPending
		if attachmentEditAllowed(m.Kind, in) {
			action.Kind = ActionEdit
			action.Message = fmt.Sprintf(
				"edit adapter %d to attach network interface %q to its desired network",
				m.AdapterKey, iface.Spec.Name)
		} else {
			action.Kind = ActionReplace
			action.Message = fmt.Sprintf(
				"replace adapter %d with an adapter attached to the desired network of network interface %q",
				m.AdapterKey, iface.Spec.Name)
		}
	}

	return action
}

// attachmentEditAllowed reports whether an attachment replacement on an
// adapter claimed by a match of the given kind may be planned as an Edit of
// that adapter. Otherwise the adapter is replaced.
//
// A positional claim never permits an Edit: replacement.md forbids inferring
// an in-place change of an adapter's attachment from position alone, so a
// positional replacement is always Remove+Add, as today's unmatched case is.
// This holds even when Policy.EditAttachments is enabled.
func attachmentEditAllowed(kind MatchKind, in PlanInput) bool {
	switch kind {
	case MatchOrphan:
		// Today's same-name orphan edit is planned only with MutableNetworks,
		// the only case today's code lists the orphaned provider resources.
		// TODO(T026): Also permit the Edit with Policy.EditAttachments.
		return in.MutableNetworks
	case MatchUnitNumber, MatchRecord, MatchIdentity:
		// TODO(T026): Permit the Edit with Policy.EditAttachments.
		return false
	case MatchPositional:
		// Never; see above.
		return false
	case MatchNone, MatchAmbiguous:
	}
	return false
}

// decideAdapter decides the action for an adapter that no desired interface
// claims. Protected adapters are never removed.
func decideAdapter(info *Info, a ObservedAdapter) (Action, bool) {
	d, ok := info.Disposition[a.Key]
	if !ok {
		// An adapter without a disposition has not been matched, so it is
		// protected.
		return Action{}, false
	}

	switch d {
	case AdapterDispositionUnclaimed, AdapterDispositionRetiring:
		return Action{
			Kind:    ActionRemove,
			Adapter: a.Key,
			Reason:  PlanReasonRemovalPending,
			Message: fmt.Sprintf("remove adapter %d that no network interface owns", a.Key),
		}, true
	case AdapterDispositionClaimed, AdapterDispositionProtected:
	}
	return Action{}, false
}

// deferIfBlocked defers an action whose hardware change cannot be applied on
// this pass.
func deferIfBlocked(action Action, in PlanInput) Action {
	if !action.changes() {
		return action
	}

	switch in.PowerState {
	case vimtypes.VirtualMachinePowerStatePoweredOff:
	case vimtypes.VirtualMachinePowerStatePoweredOn, vimtypes.VirtualMachinePowerStateSuspended:
		// TODO(T031): With Policy.LiveChanges, defer only the live actions
		// that are not eligible, and keep the VPC restore Edit (T026) live.
		return deferred(action, PlanReasonPowerOffRequired, fmt.Sprintf(
			"%s requires the VM to be powered off", action.Message))
	default:
		return deferred(action, PlanReasonPowerOffRequired, fmt.Sprintf(
			"%s requires the VM to be powered off, and its power state %q is not known",
			action.Message, in.PowerState))
	}

	if !in.ApplyChanges {
		return deferred(action, PlanReasonNetworkChangesNotAllowed, fmt.Sprintf(
			"%s is not applied on this path", action.Message))
	}

	switch action.Kind {
	case ActionAdd, ActionEdit, ActionReplace:
		if in.CardBackings[action.Interface] == nil {
			return deferred(action, PlanReasonBackingUnavailable, fmt.Sprintf(
				"%s requires the Ethernet card backing of its network, which is not resolved",
				action.Message))
		}
	case ActionNoOp, ActionDisconnect, ActionRemove, ActionDeferred:
	}

	return action
}

func deferred(action Action, reason, message string) Action {
	action.Proposed = action.Kind
	action.Kind = ActionDeferred
	action.Reason = reason
	action.Message = message
	action.DeviceKey = 0
	return action
}

// legacyAttachmentEdit composes today's same-name orphan edit: it changes the
// matched adapter's backing, address type, MAC address, and external ID to
// those of the new card, and clears its subnet ID, keeping its concrete type,
// key, and every other field.
func (p *Plan) legacyAttachmentEdit(a ObservedAdapter, newCard vimtypes.BaseVirtualDevice) error {
	dev, err := p.changes.Edit(a)
	if err != nil {
		return err
	}

	ethDev := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	newEthDev := newCard.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()

	ethDev.Backing = newEthDev.Backing
	ethDev.AddressType = newEthDev.AddressType
	ethDev.MacAddress = newEthDev.MacAddress
	ethDev.ExternalId = newEthDev.ExternalId

	// Clear SubnetID when the backing changes to ensure we don't have a
	// mismatch between the subnet ID and the port group specified in the
	// backing. The subnetID is populated by the platform automatically when
	// the network adapter is connected to a subnet.
	ethDev.SubnetId = ""

	return nil
}

// buildPlannedAdd builds the new card for a Ready interface with BuildAdd.
// With VMNetworkUnitNumbers, the card is placed at the interface's declared
// unit number, if any. Today's update path never places an added card, so
// vSphere could otherwise choose a different unit number, and the next pass
// would replace the card again.
func buildPlannedAdd(iface InterfaceState, in PlanInput) (vimtypes.BaseVirtualDevice, error) {
	add := AddInput{
		Interface: iface.Spec.Name,
		Target:    iface.Provider.Target,
		Backing:   in.CardBackings[iface.Spec.Name],
	}
	if in.UnitNumbers {
		add.UnitNumber = iface.Spec.UnitNumber
	}
	return BuildAdd(add)
}

func isClaim(kind MatchKind) bool {
	switch kind {
	case MatchUnitNumber, MatchRecord, MatchIdentity, MatchOrphan, MatchPositional:
		return true
	case MatchNone, MatchAmbiguous:
	}
	return false
}

// DeviceChanges is the single owner of the network device changes of one
// reconfigure. It holds one deep copy per edited or removed device key, so
// the observed devices are never mutated, and it assigns each added device a
// distinct temporary key.
type DeviceChanges struct {
	// removes holds the copies of the removed devices in the order they were
	// removed.
	removes []vimtypes.BaseVirtualDevice
	removed map[int32]struct{}

	// order holds the keys of the edited and added devices in the order
	// they were first edited or added.
	order   []int32
	devices map[int32]vimtypes.BaseVirtualDevice
	added   map[int32]struct{}

	nextKey int32
}

// firstTemporaryKey is the first temporary key assigned to an added device.
const firstTemporaryKey int32 = -100

// NewDeviceChanges returns an empty DeviceChanges.
func NewDeviceChanges() *DeviceChanges {
	return &DeviceChanges{
		removed: map[int32]struct{}{},
		devices: map[int32]vimtypes.BaseVirtualDevice{},
		added:   map[int32]struct{}{},
		nextKey: firstTemporaryKey,
	}
}

// Edit returns the single mutable copy of the adapter's device, deep-copying
// the observed device on first use. It fails if the device is being removed.
func (c *DeviceChanges) Edit(a ObservedAdapter) (vimtypes.BaseVirtualDevice, error) {
	if _, ok := c.removed[a.Key]; ok {
		return nil, fmt.Errorf("cannot edit device %d because it is being removed", a.Key)
	}
	if dev, ok := c.devices[a.Key]; ok {
		return dev, nil
	}
	dev, err := copyDevice(a.device)
	if err != nil {
		return nil, fmt.Errorf("cannot edit device %d: %w", a.Key, err)
	}
	c.devices[a.Key] = dev
	c.order = append(c.order, a.Key)
	return dev, nil
}

// Add adds a new device, assigns it a distinct temporary key, and returns
// that key. The device is owned by c from then on.
func (c *DeviceChanges) Add(dev vimtypes.BaseVirtualDevice) int32 {
	key := c.nextKey
	c.nextKey--
	dev.GetVirtualDevice().Key = key
	c.devices[key] = dev
	c.added[key] = struct{}{}
	c.order = append(c.order, key)
	return key
}

// Remove removes the adapter's device. Removing a device twice has no
// further effect. It fails if the device is being edited.
func (c *DeviceChanges) Remove(a ObservedAdapter) error {
	if _, ok := c.removed[a.Key]; ok {
		return nil
	}
	if _, ok := c.devices[a.Key]; ok {
		return fmt.Errorf("cannot remove device %d because it is being edited", a.Key)
	}
	dev, err := copyDevice(a.device)
	if err != nil {
		return fmt.Errorf("cannot remove device %d: %w", a.Key, err)
	}
	c.removes = append(c.removes, dev)
	c.removed[a.Key] = struct{}{}
	return nil
}

// Lookup returns the mutable copy of the edited or added device with the
// given key.
func (c *DeviceChanges) Lookup(key int32) (vimtypes.BaseVirtualDevice, bool) {
	dev, ok := c.devices[key]
	return dev, ok
}

// Removed reports whether the device with the given key is being removed.
func (c *DeviceChanges) Removed(key int32) bool {
	_, ok := c.removed[key]
	return ok
}

// Len returns the number of device changes.
func (c *DeviceChanges) Len() int {
	return len(c.removes) + len(c.order)
}

// Specs returns the device change specs: the removals first, then the edits
// and additions in the order they were first made.
func (c *DeviceChanges) Specs() []vimtypes.BaseVirtualDeviceConfigSpec {
	if c.Len() == 0 {
		return nil
	}

	specs := make([]vimtypes.BaseVirtualDeviceConfigSpec, 0, c.Len())
	for _, dev := range c.removes {
		specs = append(specs, &vimtypes.VirtualDeviceConfigSpec{
			Device:    dev,
			Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
		})
	}
	for _, key := range c.order {
		op := vimtypes.VirtualDeviceConfigSpecOperationEdit
		if _, ok := c.added[key]; ok {
			op = vimtypes.VirtualDeviceConfigSpecOperationAdd
		}
		specs = append(specs, &vimtypes.VirtualDeviceConfigSpec{
			Device:    c.devices[key],
			Operation: op,
		})
	}
	return specs
}

// copyDevice returns a deep copy of a device that keeps its concrete type.
// The device is wrapped in a device config spec because the copy facility
// round-trips its argument through JSON, which records the concrete type of
// an interface-typed field but not of the root value.
func copyDevice(dev vimtypes.BaseVirtualDevice) (vimtypes.BaseVirtualDevice, error) {
	if dev == nil {
		return nil, errors.New("the observed device is not available")
	}
	spec, err := vimtypes.DeepCopy(vimtypes.VirtualDeviceConfigSpec{Device: dev})
	if err != nil {
		return nil, fmt.Errorf("failed to copy device: %w", err)
	}
	if spec.Device == nil {
		return nil, errors.New("failed to copy device")
	}
	return spec.Device, nil
}
