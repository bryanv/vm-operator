# Provider Design: Network Interface Reconciliation

This document describes what the new network interface code in the vSphere provider looks like and how a reconcile flows through it. It implements [plan.md](./plan.md). It realizes the conceptual records in [model.md](./model.md), the edit contract in [replacement.md](./replacement.md), and the status rules in [status.md](./status.md). Type and function names are proposals. The restructured code is the only implementation; there is no flag-off copy of the old path. A per-pass `Policy`, derived from the hot-plug capability, selects the new user-visible behaviors (see "Capability policy").

## Today's flow

```text
updateVirtualMachine
  5  reconcileStatus
       MapEthernetDevicesToSpecIdx     CR Get per iface; zip if immutable
       updateGuestNetworkStatus        entries only from Tools reports
  10 reconcileConfig -> session.UpdateVirtualMachine
       reconcilePoweredOffOrPoweredOnVM
         reconcileNetworkInterfaces
           CreateAndWaitForNetworkInterfaces  poll <=15s each; all or none
           CreateVirtualEthernetCard          always VMXNet3
           ListOrphanedNetworkInterfaces      off + MutableNetworks only
         on : poweredOnReconfigure            VPC restore task only
         off: poweredOffReconfigure | resize  ReconcileNetworkInterfaces
                doReconfigure                 vmconfig reconcilers run here
                  bootoptions                 MapEthernetDevicesToSpecIdx
                  networkextraconfig          DefaultNICMatcher (zip)
         reconcileNetworkAndGuestCustomizationState
           delete orphans                     log-and-forget
           fixupMacAddresses                  refetch; zip or match
           BuildBootstraps -> DoBootstrap
  11 reconcilePowerState
```

The problems this design removes are recorded in [research.md](./research.md):

- Any not-ready interface is a `NoRequeueError` that ends the pass before power state.
- Five call sites match interfaces to hardware with three different rules.
- Matching writes observed MACs into desired values and edits observed devices in place.
- Cleanup trusts the previous pass's task submission instead of an observation.
- The powered-on path only knows the VPC restore special case.

## Shape of the new code

The design splits the work into three layers with one direction of data flow:

1. **Collect** (I/O): `Observe` reads provider CRs and VM hardware before status; `Prepare` additionally ensures desired CRs during configuration. Both use the same collection and normalization code and return an immutable `Info` snapshot with hardware matches. Matching itself is pure.
2. **Plan** (pure): compare each matched interface with its desired state, decide per-interface actions, and build device changes. It does no Kubernetes or vSphere I/O, so it can be table-tested exhaustively.
3. **Apply** (I/O, orchestration): the session adds the plan's device changes to the one shared `ConfigSpec`, the existing `doReconfigure` submits it, and the next pass observes the result.

A per-pass `ReconcileState` pointer is allocated in `updateVirtualMachine` before status (step 4b below) and carried in the context, following the volume-info pattern. Every phase reads and replaces whole snapshots in it; no helper mutates a snapshot another phase holds. This is scratch data for one call, discarded when the call returns and rebuilt from authoritative observations on the next call. No process-local state is required between reconciles.

Keep one representation of each fact. An interface holds its API spec directly; name, unit number, and presentation order are not separately cached. Readiness and pending summaries are computed from the current observation and plan, not maintained as another mutable state machine. Session calls the network package to prepare and plan, adds its changes to the existing shared task, and invokes cleanup/bootstrap projections; provider-specific CR handling remains inside `network`.

## Package layout

| File | Status | Contents |
|---|---|---|
| `network/state.go` | new | `ReconcileState`, `Info`, `Readiness`, `ObservationIdentity`, `Policy`, `WithContext`/`FromContext`. |
| `network/provider.go` | new | `Observe` and `Prepare` share provider collection and normalization; ensuring CRs and decoding provider status are private helpers. Reuses today's `create*NetworkInterface` and `get*Device` helpers. The update path no longer calls the `waitFor*` pollers; the create path's prerequisite wait is unchanged. |
| `network/matching.go` | new | Attachment normalization for desired and observed sides, and `Match`. Absorbs `FindMatchingEthCard`'s predicate and the per-provider `findMatchingEthCard*` helpers. |
| `network/plan.go` | new | `BuildPlan`, `Action`, `DeviceChanges` accumulator. |
| `network/edit.go` | new | `BuildAdd`, `BuildAttachmentEdit`, `BuildDisconnect`, connection-policy helpers, wire-presence handling. |
| `network/eligibility.go` | new | `Eligibility` built from an injected `VirtualMachineConfigOption`; pure per-operation verdicts. |
| `network/operation.go` | new | `OperationRecord` encode/decode to VM ExtraConfig. |
| `network/network.go`, `reconcile.go`, `devices.go`, `nsxt.go` | changed | `UpdateEthCardDeviceChanges`, `reconcileNetworkInterfaces`, and `VPCPostRestoreBackingFixup` are replaced by the planner. `MapEthernetDevicesToSpecIdx` reads `Info`. `nsxt.go` keeps building per-cluster DVPG backings for Add and Edit; NSX attachments are compared by external ID, so no reverse lookup is added. |
| `network/list_interfaces.go` | changed | `RetireProviderResources` with UID preconditions. |
| `network/bootstrap.go` | changed | Extract `BuildBootstrap` for one device; `BuildBootstraps` keeps its complete-list contract. |
| `session/session_vm_update_network.go` | new | Orchestration: `reconcileNetwork`, `addNetworkChanges`, `finishNetwork`. Keeps `session_vm_update.go` from growing further. |
| `session/session_vm_update.go` | changed | Three dispatch sites call the helpers above; `doReconfigure` sets `ChangeVersion` when the policy allows. |
| `vmprovider_vm.go` | changed | Allocate state before status, inject the eligibility resolver, gate power-on, convert deferred retries into a final `RequeueError`. |
| `vmlifecycle/update_status.go` | changed | Interface assembly from `Info`; spec-first entries and new fields when `Policy.InterfaceStatus`. |
| `vmlifecycle/bootstrap.go`, `bootstrap_cloudinit.go` | changed | `UpdatedEthCards` removed. `DoBootstrap` keeps a second customization hash when `Policy.NICsOutOfGOSCHash` (G15). |
| `vmconfig/bootoptions`, `vmconfig/networkextraconfig`, `virtualmachine/networkextraconfig` | changed | Read the shared match from state; copy before editing; skip keys the plan removes. |

Paths are relative to `pkg/providers/vsphere/` except `vmconfig/`, which is `pkg/vmconfig/`.

## Core types

The desired side has one entry per spec interface, in spec order. `InterfaceState.Spec` is an owned copy of the API spec. Read its name and unit number directly; the matcher consults unit number only when `VMNetworkUnitNumbers` is enabled. Slice position supplies presentation order, never identity. No separate `DesiredInterface` wrapper duplicates these fields.

Provider identity survives readiness changes, and a recreated CR is distinguishable by UID.

```go
type ProviderRef struct {
	Type      pkgcfg.NetworkProviderType
	Namespace string
	Name      string
	UID       types.UID // empty for the named-network test path
}

type ResolutionPhase string

const (
	ResolutionReady   ResolutionPhase = "Ready"
	ResolutionPending ResolutionPhase = "Pending"
	ResolutionFailed  ResolutionPhase = "Failed"
	ResolutionUnknown ResolutionPhase = "Unknown" // read failed
)

// Resolution is the provider outcome for one interface spec.
type Resolution struct {
	Phase    ResolutionPhase
	Reason   string
	Message  string
	Ref      ProviderRef
	Target   *Attachment            // set only when Ready
	Err      error                  // Failed/Unknown only
	object   ctrlclient.Object      // private; lazy bootstrap projection
	network  object.NetworkReference // private; device construction
}
```

Raw CRs and backing-resolution handles remain private to `network`. Consumers use `Ref`, phase, and normalized target fields; bootstrap/device builders in that package can use the private values without refetching or teaching Session about NetOP, NCP, or VPC object types.

An `Attachment` is the comparable, normalized identity of what an adapter is plugged into. The same type describes both the desired target and an observed adapter, so comparison is field-by-field equality on the semantic fields only.

```go
type BackingKind string // Network, DVPortgroup, Opaque, NSXSegment

type BackingID struct {
	Kind       BackingKind
	ID         string // MoRef, portgroup key, opaque ID, or LS UUID
	SwitchUUID string // DVPortgroup only
	PortKey    string // only when the provider pins a port
}

type Intent uint8 // IntentUnknown, IntentKeep, IntentSet, IntentClear

type FieldIntent struct {
	Op    Intent
	Value string
}

type Attachment struct {
	Backing    BackingID
	ExternalID FieldIntent
	MAC        MACPolicy
}

// MACPolicy is pinned (Manual) or vSphere-managed.
type MACPolicy struct {
	Pinned string // normalized lower-case; "" when managed
}
```

For NSX (NCP and VPC), the allocation's external ID (the logical port or attachment ID) is the attachment identity, together with the pinned MAC. The DV portgroup that carries the segment is derived per cluster through `nsxt.go`'s cache and differs across a stretched Supervisor, so it is never compared. `BackingID` for NSX records the logical-switch UUID from the CR; the observed side knows it only for an opaque backing. That keeps matching free of vCenter I/O and stops a portgroup difference from looking like an attachment change.

The observed side never holds a mutable vSphere device. The original device is kept only so the edit builder can deep-copy it.

```go
type ObservedAdapter struct {
	Key         int32
	UnitNumber  *int32
	Type        string // concrete vim type, e.g. "VirtualE1000e"
	MAC         string
	AddressType string // Manual, Generated, Assigned
	Attachment  Attachment
	Connect     ConnectionState
	Record      *OperationRecord // decoded from ExtraConfig
	device      vimtypes.BaseVirtualDevice // read-only; copy to edit
}

type ConnectionState struct {
	Present           bool // Connectable != nil
	Connected         bool // meaningful only while powered on
	StartConnected    bool
	AllowGuestControl bool
}
```

Matching produces one result per desired interface plus the disposition of every unclaimed adapter.

```go
type MatchKind string

const (
	MatchUnitNumber MatchKind = "UnitNumber" // exact, exclusive
	MatchRecord     MatchKind = "Record"     // operation record
	MatchIdentity   MatchKind = "Identity"   // MAC/external ID/backing
	MatchOrphan     MatchKind = "Orphan"     // old CR for same name
	MatchPositional MatchKind = "Positional" // immutable status only
	MatchNone       MatchKind = "None"
	MatchAmbiguous  MatchKind = "Ambiguous"
)

type Match struct {
	Kind       MatchKind
	AdapterKey int32   // valid for the claiming kinds
	Candidates []int32 // Ambiguous: all protected
	Evidence   string  // for messages and logs
}

type AdapterDisposition string // Claimed, Protected, Retiring, Unclaimed
```

Each interface carries its desired spec, provider outcome, and hardware claim together. `AttachmentChange` is computed after matching from current hardware, desired provider identity, and available applied-allocation evidence. It is independent of provider readiness: Pending alone does not mean Replacement. If the old allocation cannot be established, the classification is Unknown, and the planner retains the card rather than guessing that it must be disconnected.

```go
type AttachmentChange string // Unknown, Unchanged, Replacement

type InterfaceState struct {
	Spec             vmopv1.VirtualMachineNetworkInterfaceSpec
	Provider         Resolution
	Match            Match
	AttachmentChange AttachmentChange
}
```

For example, a known replacement whose new CR is not ready has one record with these facts:

```text
Spec:             name=eth0, unitNumber=7, network=net-b
Provider:         ref=ns/nic-b, phase=Pending, reason=AllocationPending
Match:            kind=UnitNumber, adapterKey=4000
AttachmentChange: Replacement
```

The record still claims the observed adapter and names the desired CR even though no usable destination backing exists yet. The planner selects Disconnect if eligible; status reports both the pending CR and actual adapter state; cleanup retains both desired and still-applied resources. If the CR becomes ready, the next pass reconstructs the record with Ready provider fields and plans the combined attachment update and reconnect (or Remove+Add when type also differs). For the same CR becoming temporarily unready on an unchanged attachment, `AttachmentChange=Unchanged` keeps the working card connected. The record is per-pass data, not a persistent pod-side journal or a status condition used for control flow.

The snapshot ties everything to the inputs it was computed from. Observed adapters remain a separate inventory because unclaimed, retiring, and ambiguous hardware must also be represented; each interface refers to its match by actual device key, not a parallel array index.

```go
type ObservationIdentity struct {
	VMUID         types.UID
	Generation    int64
	NetworkHash   string // hash of spec.network.interfaces
	ChangeVersion string // MoVM.Config.ChangeVersion
}

type Info struct {
	Identity    ObservationIdentity
	Enabled     bool // false: nil or disabled spec.network
	Interfaces  []InterfaceState // one per spec entry, including Pending/Failed
	Adapters    []ObservedAdapter
	Disposition map[int32]AdapterDisposition
	Owned       []ProviderRef // VM-owned CRs from the list call
	Complete    bool          // every read succeeded
}
```

`ReconcileState` is the only mutable object, and only orchestration code replaces its fields.

```go
type ReconcileState struct {
	Policy       Policy // fixed for the pass
	Observed     *Info
	Plan         *Plan
	Submitted    bool          // a NIC change was in the task
	RequeueAfter time.Duration // deferred retry, applied at the end
}

func WithContext(ctx context.Context, s *ReconcileState) context.Context
func FromContext(ctx context.Context) (*ReconcileState, bool)
```

## A reconcile, step by step

The numbers are the existing steps of `updateVirtualMachine`. Steps not listed are unchanged.

### Step 4b: prepare (new, next to volume info)

```go
st := &network.ReconcileState{Policy: network.PolicyFromContext(vmCtx)}
vmCtx.Context = network.WithContext(vmCtx.Context, st)
st.Observed = network.Observe(vmCtx, network.ObserveInput{
	Client: vs.k8sClient,
	VM:     vmCtx.VM,
	MoVM:   vmCtx.MoVM,
})
```

```go
type ObserveInput struct {
	Client   ctrlclient.Client
	VM       *vmopv1.VirtualMachine
	MoVM     mo.VirtualMachine
}
```

`Observe` lists VM-owned CRs, gets each desired CR by its expected name without creating it, normalizes adapters from `MoVM.Config.Hardware.Device`, decodes operation records from ExtraConfig, and runs `Match`. Kubernetes reads come from the manager cache. Provider status is decoded once for each collected object. `Resolution.Target` carries identity fields from CR status (network ID, external ID, MAC); this early pass does no backing-resolution vCenter I/O. A read failure sets `Complete=false` and that interface's phase to `Unknown`; it is never returned as an error, so status and shutdown still run.

### Step 5: status

`ReconcileStatus` reads `st.Observed` instead of `NetworkDeviceKeysToSpecIdx`. Spec-first assembly ([status.md](./status.md)) seeds one entry per `Interfaces` record using its `Spec`, overlays the adapter selected by that record's `Match`, and then overlays guest reports that pass identity checks against that adapter. The same record's `Provider` supplies readiness and its resource reference. Hardware conditions published here describe only what observation established.

### Step 7: schema upgrade

The unit-number backfill may write `spec.network.interfaces[].unitNumber`. That changes `NetworkHash`, so the config phase below rebuilds rather than reusing `st.Observed`.

### Step 10: config

`session.reconcilePoweredOffOrPoweredOnVM` replaces its `reconcileNetworkInterfaces` call with:

```go
plan, err := s.reconcileNetwork(vmCtx, args.NetworkEligibility)
if err != nil {
	return err // genuine I/O failure only, never "pending"
}
```

`reconcileNetwork` (in `session_vm_update_network.go`):

1. `network.Prepare` ensures resources for every desired interface and returns a fresh `Info` using the same collection code as `Observe`. It consumes the objects returned by create/patch directly, decodes their status once, resolves backing handles for ready targets using the injected session backing resolver, and runs matching/classification. Errors stay on their interface records. NCP's missing `ProviderStatus` and VPC's missing `LogicalSwitchUUID` become Pending with a reason, without polling. Session neither passes an intermediate CR slice through separate Ensure/Resolve/Observe calls nor performs provider-specific switches.
2. Replace `st.Observed` with that result and clear any previous `st.Plan`. This config-phase collection follows schema upgrade/revert, so it intentionally refreshes the earlier status observation. Each collection uses its own current inputs; this is not a cache across reconciles.
3. `BuildPlan(st.Observed, opts)` computes actions. `opts` carries `st.Policy`, the current and desired power state, `MutableNetworks`, `VMNetworkUnitNumbers`, the escape-hatch annotation, and an `Eligibility` value. The eligibility resolver is called lazily, only if the plan contains a live operation. It is injected through `VMUpdateArgs`/`VMResizeArgs` as a function built in `vmprovider_vm.go`, because `session` cannot import the root provider package that owns `QueryConfigOptionEx`.
4. Set `st.Plan = plan`. Readiness is a pure projection of the current observation, plan, and task-submission state (below); no caller sets readiness flags or copies pending reasons into another stored list. Genuine errors are aggregated at the provider boundary after independent work, as described in plan.md.

The three dispatch sites each call one helper instead of `UpdateEthCardDeviceChanges`:

```go
// poweredOnReconfigure, getConfigSpecForPoweredOffVM,
// resizeVMWhenPoweredStateOff
addNetworkChanges(configSpec, plan)
```

`addNetworkChanges` appends `plan.Specs()` and the plan's operation-record ExtraConfig entries. It is called on all three paths regardless of `VMResizeCPUMemory`, which removes today's resize-flag dependency. `handleRestoredVPCInterfaces` is removed, because VPC restore is an ordinary attachment update in the plan.

Inside `doReconfigure`, the `vmconfig` reconcilers run as today:

- `bootoptions` resolves a bootable interface name to the `Match` on its `st.Observed.Interfaces` entry. A numbered interface with no adapter yields no boot entry rather than another NIC (spec 008 G13).
- `networkextraconfig` gets its `NICDeviceMatcher` from state rather than `DefaultNICMatcher`. `findOrCreateDeviceEdit` returns the planner's existing copy for that key. When none exists, it deep-copies the observed device instead of appending it directly. It skips any key the plan removes.

After the existing empty-spec check, `doReconfigure` sets `configSpec.ChangeVersion = moVM.Config.ChangeVersion` only when `st.Policy.ChangeVersion` and `len(plan.Specs()) > 0`, reading the plan through `network.FromContext`. A successful task sets `st.Submitted = true` and returns `ErrReconfigure`, the existing barrier; the next pass observes the result.

If no task was needed, `finishNetwork` runs where `reconcileNetworkAndGuestCustomizationState` runs today:

1. `RetireProviderResources` deletes eligible old CRs (see "Cleanup"). Failures are recorded on state and set `st.RequeueAfter`; they are not returned.
2. If `st.Readiness(vmCtx).Ready()`, it asks the network package to build the complete ordered bootstrap projection from each `Interfaces` entry's `Provider`, `Spec`, and the observed MAC selected by its `Match`, then calls `GetBootstrapArgs` and `DoBootstrap` as today. Any temporary `[]Device` required by existing `BuildBootstraps` stays inside that compatibility adapter; it is not stored on Session args or state. `fixupMacAddresses` is not called: the current observation already contains the applied MACs.
3. `status.network.config` is published from per-interface `BuildBootstrap` results for every Ready interface, even when bootstrap itself is not ready.

### Step 11: power state

```go
if current == Off && desired == On {
	st, ok := network.FromContext(vmCtx)
	if !ok {
		return errors.New("network state missing before power on")
	}
	readiness := st.Readiness(vmCtx)
	if !readiness.Ready() {
		vmCtx.Logger.Info("Waiting for networking before power on",
			"pending", readiness.PendingNames())
		return nil
	}
}
```

A missing state holder is a programming error, so it fails loudly instead of silently permitting or silently blocking. The gate sits after the existing paused and running-task checks. It returns nil, so snapshot create still runs; `VirtualMachinePowerStateSynced` stays False because spec and status differ. Resume from suspended and power-off are not gated.

### End of pass

If `reconcileErr` is nil and `st.RequeueAfter > 0`, `updateVirtualMachine` returns `pkgerr.RequeueError{After: st.RequeueAfter}`. This provides bounded retry for cleanup failures and for waits with no dependable watch, without making them block power-on or later steps.

## Matching

`Match` claims adapters one-to-one in this order. A later pass never takes an adapter an earlier pass claimed.

1. **Unit number** (`VMNetworkUnitNumbers` on, interface numbered): the adapter at exactly that unit, or `MatchNone`. Never falls through (spec 008 G11).
2. **Operation record**: an adapter whose ExtraConfig record names this VM UID and interface name. Records are written in the same task as the change, so this mapping survives restart and disambiguates adapters this feature has touched (Q5).
3. **Identity** (unnumbered, resolution Ready): unclaimed adapters whose normalized `Attachment` equals the target, comparing the pinned MAC only when pinned and the external ID only when the target sets one. Exactly one candidate claims; more than one is `MatchAmbiguous`, and all candidates become `Protected`.
4. **Orphan**: for an interface whose current CR does not match, an owned CR labeled with the same interface name whose last-applied attachment identifies exactly one unclaimed adapter. That adapter is claimed as the interface's existing adapter, which makes the result an attachment replacement.
5. **Positional** (`MutableNetworks` off only): today's zip is kept as a last resort so immutable-network VMs keep their status names and boot entries. The planner treats a positional-only match as ambiguous: it never adds, edits, or removes on that basis, and it reports `InterfaceMappingAmbiguous` instead.

Each unclaimed adapter is then dispositioned:

- `Protected`: it is a candidate of an ambiguous match, or `Info.Complete` is false, or some unnumbered desired interface is `Pending`/`Unknown` with no claim. No removal is planned.
- `Retiring`: its record or an orphan CR ties it to an interface that is no longer in the spec.
- `Unclaimed`: the inventory is complete and nothing could own it; this is today's "remove unmatched adapter" case.

`MapEthernetDevicesToSpecIdx`, status, boot options, NIC tuning, bootstrap MAC lookup, and the planner all read this one result.

## Planning

`BuildPlan` is a pure function from `Info` and options to a `Plan`. It first decides one action per desired interface and per non-claimed adapter, then filters by eligibility, then groups.

```go
type ActionKind string // NoOp Add Edit Disconnect Remove Replace Deferred

type Action struct {
	Kind      ActionKind
	Interface string // "" for an adapter with no desired entry
	Adapter   int32  // 0 for Add
	Reason    string // stable reason constant
	Message   string
	Group     int // actions that must be submitted together
}

type Plan struct {
	Identity ObservationIdentity
	Actions  []Action
	changes  DeviceChanges
	records  []vimtypes.BaseOptionValue
}
```

Per `InterfaceState`, read `d=Spec`, `r=Provider`, `m=Match`, and `AttachmentChange` together (full table in [model.md](./model.md)). Whether an action is live follows from the observed VM power state; do not store another `Action.Live` flag. Keep action groups for actual dependencies such as Remove+Add sharing a slot.

```text
r Unknown|Failed      -> claimed: NoOp (keep); else Deferred
r Pending, claimed    -> if AttachmentChange == Replacement
                           and EditAttachments:
                           Disconnect else NoOp
r Pending, unclaimed  -> Deferred (reserve slot and CR)
r Ready, None         -> Add (explicit supported type with AdapterTypes;
                           otherwise the existing creation behavior)
r Ready, Ambiguous    -> Deferred InterfaceMappingAmbiguous
r Ready, claimed      -> AdapterTypes and explicit type mismatch?
                           Replace with requested type, before considering Edit
                        else equal(attachment)
                           ? NoOp (or reconnect an unfinished handoff)
                           : EditAttachments
                             ? Edit, or Replace with the escape hatch
                             : Replace (today's Remove+Add);
                               VPC restore while on: Edit
```

Per adapter: `Retiring` gives Remove, `Unclaimed` gives Remove, and `Protected` gives nothing.

An omitted type preserves a claimed adapter's concrete type. For an interface with no existing class/image or matched adapter, it defaults to VMXNet3. Resolve and validate the destination type before emitting any changes; unsupported types or explicit incompatible tuning produce Deferred with an actionable reason. Type never changes matching. A type-only replacement retains its provider CR and waits for Ready resolution; if that CR is temporarily unready, it does not disconnect the unchanged attachment. The shared type-selection rules also apply to create/deploy construction, including explicit overrides of class/image devices. Preserve existing API field and mutability gates.

Disconnect requires Q7's operation evidence to distinguish an unfinished handoff from a later guest disconnect, including after cancellation or operator restart. It does not require saving previous connection settings. A ready replacement automatically connects in the same task that updates the attachment, whether via Edit or the Add of a type replacement. Until T027 ships, a pending replacement keeps the old adapter connected (NoOp) and reports `ReplacementPending` with its actual connection state.

Eligibility then rewrites live actions. With the VM powered on and `Policy.LiveChanges` false, new live actions except the VPC-restore Edit are `Deferred` with `PowerOffRequired`, which is today's behavior made visible. Completion of a recorded unfinished handoff is also permitted after capability withdrawal: revalidate the current desired provider CR and apply its attachment and automatic reconnection together, subject to platform eligibility and shutdown priority. This exception cannot start a new replacement. Otherwise, `Add` needs device `plugAndPlay` and guest `supportsHotPlugPCI`; `Remove` needs `hotRemoveSupported`; and `Edit`, `Disconnect`, and `Replace` need the support validated by Q2 for that adapter type and transition. An unknown verdict is unsupported. Unsupported live actions become `Deferred` with `PowerOffRequired`, and every action in the same group is deferred with them. When the spec requests power-off or suspend, every new live action is deferred so the barrier cannot postpone the transition. A known attributable NIC fault from a previous task, recorded in the operation record, defers that group until its inputs change.

## Building device changes

`DeviceChanges` is the single owner of NIC device specs for a pass.

```go
type DeviceChanges struct {
	edits   map[int32]vimtypes.BaseVirtualDevice // copies
	removes map[int32]vimtypes.BaseVirtualDevice
	adds    []vimtypes.BaseVirtualDevice // temporary keys
	nextKey int32                        // -100, -101, ...
}

// Edit returns the single mutable copy for key, deep-copying the
// observed device on first use. It fails if key is being removed.
func (c *DeviceChanges) Edit(o ObservedAdapter) (
	*vimtypes.VirtualEthernetCard, error)

func (c *DeviceChanges) Add(d vimtypes.BaseVirtualDevice)
func (c *DeviceChanges) Remove(o ObservedAdapter) error

// Specs returns removes, then edits, then adds.
func (c *DeviceChanges) Specs() []vimtypes.BaseVirtualDeviceConfigSpec
```

- **Add** (`BuildAdd`): a fresh concrete card from the destination-type builder, plus the declared unit number, a distinct temporary key, compatible NIC-tuning fields folded in, and an explicit default `Connectable`. When `AdapterTypes` is false, keep existing creation behavior. A managed MAC is omitted so vSphere assigns it.
- **Edit** (`BuildAttachmentEdit`): the copy from `DeviceChanges.Edit`. When the backing is unchanged, the backing is kept; otherwise it is replaced with a fresh target backing. Address type and MAC are set as a pair per the MAC table in [replacement.md](./replacement.md), and the external ID per its intent. For a ready replacement or completion of a cancelled pending handoff, the same Edit sets `Connected=true` while running, `StartConnected=true`, and `AllowGuestControl=true`. Previous connection settings are not restored. Type, key, unit, PCI slot, and tuning fields are untouched. Unrelated device edits preserve connection state rather than invoking this handoff rule.
- **Disconnect** (`BuildDisconnect`): the copy with `Connected=false` while running, `StartConnected=false`, and `AllowGuestControl=false`.
- **Replace**: `Remove` plus an `Add` at the same unit number in one task/group. With `AdapterTypes`, use the explicit destination type or preserve the observed type when omitted. An explicit type mismatch always uses this path, without an escape-hatch annotation or type-changing Edit. Check source hot-remove, destination hot-add, and the validated pair together. Populate the ready provider attachment and connected/start-connected defaults on the Add; do not transplant source-only fields. Resolve actual type/key/MAC afterward and persist guest invalidation in the same task, including key/MAC reuse.

A field whose intent is `Clear` cannot be expressed through the pinned SDK, because empty strings are omitted on the wire. Until the wire-presence work (T016, gated by R10 in T007) lands, `BuildAttachmentEdit` returns `ErrClearUnsupported`, and the planner turns that into `Deferred` with an actionable reason instead of sending an edit that silently keeps the old external ID.

## Readiness

`Readiness` is a calculated return value, never a field to update on `ReconcileState`. Both bootstrap and the off-to-on network gate use the same `Ready()` predicate; the outer lifecycle retains its additional customization and power prerequisites. This removes duplicate network readiness flags without equating network readiness with completed guest customization.

```go
type Readiness struct {
	Known             bool // a matching current plan exists; no stale task data
	ProviderReady     bool
	HardwareConverged bool // desired hardware, including removals, is observed
	MACsResolved      bool
	Pending           []PendingInterface // sorted by name
}

// Readiness derives a summary without I/O or mutation.
func (s *ReconcileState) Readiness(
	vmCtx pkgctx.VirtualMachineContext) Readiness

func (r Readiness) Ready() bool {
	return r.Known && r.ProviderReady &&
		r.HardwareConverged && r.MACsResolved
}
```

`Known` is false if observation/plan is absent, their identities differ, either no longer describes the current VM inputs, or a submitted task made the hardware observation stale. This covers paused, task-busy, suspended, early-return, schema-upgrade, and revert cases without another progress enum. A fully evaluated disabled or empty network is ready only after any required device removals are observed; absence of state is never treated as an empty network.

Derive provider readiness from each `Provider`, hardware convergence from current matches and comparison of owned fields plus required removals, and effective MACs from matched observed hardware. NoOp alone does not prove convergence: a Pending interface can deliberately retain a card without changing it. A disconnected retiring card is still present and does not satisfy a required removal. Ignore runtime-only `Connected` while powered off. Build sorted pending messages from the same provider outcomes and planned actions. Provider-resource cleanup and guest-report validity remain independent of this gate.

| Consumer | Reads |
|---|---|
| Bootstrap (`finishNetwork`) | Derived `Readiness.Ready()` plus existing bootstrap prerequisites |
| Power-on gate (`reconcilePowerState`) | The same `Readiness.Ready()` plus existing lifecycle prerequisites |
| `VirtualMachineNetworkReady` | `Pending` reasons, aggregated |
| Per-interface conditions | The `InterfaceState` for that name (`Provider`, `Match`, `AttachmentChange`) and its planned action |
| `VirtualMachineNetworkResourcesCleanedUp` | retirement results only |

## Operation record

One proposed ExtraConfig entry per adapter requiring durable attachment evidence. The key follows the existing NIC convention (`vmservice.nic.ethernet%d.managedKeys`), and the record carries the interface identity, because device keys are reused after replacement. Previous connection settings are not stored: automatic connection of a ready replacement is defined by the reconciliation contract. An unfinished-handoff marker distinguishes cancellation/recovery from a later guest disconnect; applied-allocation and guest-validity evidence have separate lifetimes within the record.

```text
vmservice.nic.ethernet<N>.operation = {"v":1,
  "vm":"<uid>","iface":"eth1",
  "from":{"provider":"VPC","name":"vm-a-eth1","uid":"..."},
  "to":{"provider":"VPC","name":"vm-b-eth1","uid":"..."},
  "handoffPending":true,
  "guestRev":3}
```

Operation evidence is written in the same `ConfigSpec` as the Disconnect, Edit, or Replace it describes. A successful attachment-and-reconnect task completes the handoff marker in that same task; later reconciles observe the result and do not replay reconnection merely because the guest disconnected afterward. Type replacement records guest invalidation even when the provider allocation, key, and MAC are unchanged; the observation/fingerprint includes the concrete type. Completing the handoff does not clear guest invalidation or applied-allocation evidence still needed for matching or resource retirement. Clear the whole record only when none of its facts is needed; Q7 defines the exact lifecycle, including Remove. Because it lives in VM configuration, the proposal preserves evidence across operator restart and aims to keep it coherent with hardware across snapshot revert or restore, to be validated under Q7. Matching, unfinished-handoff recovery, cleanup reachability, and guest invalidation read this evidence independently of per-pass scratch state.

## Cleanup

```go
func RetireProviderResources(ctx context.Context, c ctrlclient.Client,
	info *Info, plan *Plan) []RetireResult
```

A CR in `info.Owned` is deleted only when all of these hold:

- No desired interface resolves to it by name and UID.
- No adapter is claimed, protected, or retiring through it, judged by its provider-specific attachment and any operation record (Q8). Sharing a backing alone neither proves nor rules out a reference.
- `info.Complete` is true and the plan submitted nothing this pass.

Deletion uses `ctrlclient.Preconditions{UID: &uid}`, so a newer object that reused the name is never deleted. `NotFound` counts as success; any other failure becomes a `RetireResult` for the cleanup condition and a deferred requeue.

## Errors and requeue

| Situation | Returned from the config phase | Effect |
|---|---|---|
| Interface pending, CR watch exists | nil; `Pending` in state | Independent work and power-off proceed; power-on gated; watch wakes the VM. |
| Wait without a dependable watch | nil; `st.RequeueAfter` | As above, plus `RequeueError` at the end of the pass. |
| CR create/patch or list failure | wrapped error | Ordinary error: the pass ends unless the spec requests power-off (existing `IsOnToOff` escape). |
| Unsupported live operation | nil; `Deferred` action | Condition names the interface and `PowerOffRequired`. |
| NIC task submitted | `ErrReconfigure` | Existing barrier; the next pass observes. |
| `ChangeVersion` conflict | wrapped task error | Ordinary retry; the next pass re-observes and replans. |
| Cleanup failure | nil; `st.RequeueAfter` | Cleanup condition False; bootstrap and power-on unaffected. |

## Capability policy

`Policy` is the only place the hot-plug capability is read. It is built once per pass in step 4b and stored on `ReconcileState`, so every phase sees the same answer.

```go
// Policy holds the capability-dependent choices for one pass.
// Everything else in the network code is unconditional.
type Policy struct {
	LiveChanges       bool // apply eligible changes while powered on
	EditAttachments   bool // Edit/Disconnect; false keeps Remove+Add
	InterfaceStatus   bool // spec-first entries and new status fields
	AdapterTypes      bool // honor explicit types; Replace on mismatch
	NICsOutOfGOSCHash bool // G15: NIC changes do not re-customize
	ChangeVersion     bool // guard NIC tasks with ChangeVersion
}

func PolicyFromContext(ctx context.Context) Policy {
	on := pkgcfg.FromContext(ctx).Features.VMNetworkHotPlug
	return Policy{
		// Each field is wired to on by the last task of its
		// delivery group; until then it is a literal false.
		LiveChanges:       on,
		EditAttachments:   on,
		InterfaceStatus:   on,
		AdapterTypes:      on,
		NICsOutOfGOSCHash: on,
		ChangeVersion:     on,
	}
}
```

With every field false, the restructured code reproduces today's results:

- Attachment changes on an identified adapter become Remove+Add.
- An Add of any type builds VMXNet3.
- Status keeps Tools-reported entries.
- LinuxPrep/Sysprep keep the single customization hash.
- No `ChangeVersion` is set.
- Running-VM changes wait for power-off, except VPC restore. Today `handleRestoredVPCInterfaces` edits a running VM's restored adapter in place (same MAC, new external ID and backing). The planner keeps that one live Edit when `EditAttachments` is false, and keeps Remove+Add for a powered-off restore.

The intended differences are the G2 and G8 fixes:

- Pending interfaces no longer stop the pass, and the power-on gate replaces the early return that also blocked power-off.
- Deferred live work is reported on `VirtualMachineNetworkReady`.
- Cleanup uses UID preconditions and retries.
- A running Cloud-Init VM's guestinfo is not rewritten while a network change is deferred, because derived network readiness is false until the hardware is applied. Today it is rewritten to describe an interface that does not exist yet. This also holds back other Cloud-Init input changes made during that window; T020 must test and document it.

T012's characterization tests pin everything else before each extraction.

New operation records are written only for capability-enabled attachment or hardware-replacement work. Existing records are always read and may be updated to finish an already-started handoff after capability withdrawal. Once the current desired provider CR is ready, recovery applies its attachment and automatic reconnection together, without restoring historical connection settings or initiating another replacement. Platform eligibility and shutdown priority still apply.

### Bootstrap hash (G15)

With `NICsOutOfGOSCHash`, `DoBootstrap` computes a second hash of the LinuxPrep/Sysprep `CustomizationSpec` with `NicSettingMap` cleared. It stores that hash in a new annotation next to `bootstrap-hash-customspec`:

```text
new hash present?  no  -> legacy hash == full(current)?
                            yes: write both, no customize
                            no : customize (today's rule); write both
                   yes -> new hash == partial(current)?
                            yes: refresh legacy hash, no customize
                            no : customize; write both
```

Customization always carries the full current `NicSettingMap`. `customizeAtNextPowerOn` and the customize latch keep their meaning. The ConfigSpec hash (Cloud-Init, vAppConfig) is unchanged.

## Testing seams

- `Match`, `BuildPlan`, `Eligibility`, `BuildAttachmentEdit`, and the operation-record codec are pure. Each gets table-driven unit tests over constructed `Info` values, covering every planner row, exact-number misses, ambiguous candidates, and the MAC table.
- `DeviceChanges` tests assert that the observed device is never mutated, that Edit+Remove conflicts are rejected, and that one copy is made per key.
- Wire tests marshal the final `ConfigSpec` with `vim25/xml` for each concrete card type and assert element presence for every Keep/Set/Clear intent.
- `Observe` and `Prepare` use the fake client with provider CRs in each status shape. Verify the early observer performs no writes, preparation retains ensure/read errors on the affected records, and collection does not refetch an object merely to decode its status again.
- Derived readiness tests cover absent/stale plans, spec changes, task submission, Pending with NoOp, removed-but-still-present disconnected hardware, missing generated MACs, and cleanup/Tools failures that must not gate network readiness.
- Session and provider tests (vcsim, existing suites) drive the real `updateVirtualMachine` across power states, policies, flags, and restarts. They establish orchestration only, not platform hot-plug support.

## Migration from today's code

The order matches [tasks.md](./tasks.md):

1. Add `state.go` with an all-false `Policy`, and characterization tests for today's matching.
2. Add `provider.go` and `matching.go` and wire `Observe` into step 4b. Status and boot options read it; nothing else changes.
3. Add `plan.go` for Add/Remove/Replace parity with `ReconcileNetworkInterfaces`, `RetireProviderResources`, the power-on gate, and the end-of-pass requeue. This is the first behavior change, and it is ungated: pending interfaces stop blocking shutdown and independent configuration.
4. Add the capability key and `PolicyFromContext`, with every field still false.
5. Add per-interface status, then `edit.go` and the operation record, then `eligibility.go` and the live dispatch in `poweredOnReconfigure`. Each group's last task wires its policy fields to the capability.
6. Once the feature is GA, remove the policy fields and their false branches in a follow-up spec.
