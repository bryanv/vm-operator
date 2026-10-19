# Network Reconciliation State Model

This is the proposed internal model for [spec 012](./spec.md). The public per-interface status extension and guest-report policy are specified in [status.md](./status.md). `Bootstrap` remains a separate, late-built value; it does not become part of the network inventory.

## State ownership

`Info` is an observation snapshot. `Plan` describes proposed actions. `ReconcileState` is a per-pass holder allocated by the outer provider and shared through context; only orchestration replaces its snapshots/results. These are conceptual names, not a requirement to create a separate abstraction for every row below.

| Record | Contents | Invariant |
|---|---|---|
| Observation identity | VM UID, generation/network fingerprint, config change version | Stale snapshots cannot authorize power-on, customization, or cleanup. |
| Desired interface | Spec name/index, spec copy, optional unit number | One entry for every desired interface, even when resolution fails. |
| Provider identity | Provider kind, namespace/name, UID, observed resource version, attachment identity | Identity survives pending readiness; a recreated CR is distinguishable from the previous object. |
| Provider resolution | Ready/Pending/Failed, reason/message, optional resolved backing, external ID, MAC policy | Ready does not require a vSphere-generated MAC to exist yet. |
| MAC policy | Provider/user-pinned value, or vSphere-managed Generated/Assigned addressing without a pinned desired value | A desired pinned MAC is never overwritten by hardware observation. Preserve provider-specific override rules and valid managed address types. |
| Attachment field intent | Keep/Set/Clear for fields owned by the provider adapter; Unknown until resolved | Empty required provider data is pending, not a clear or permission to generate. Wire construction preserves presence semantics. |
| Observed adapter | Deep-copyable concrete card, actual key/unit/backing/address type/MAC/connection state | Never modified while computing a plan. Actual key and MAC are separate from proposed values. |
| Match | ExactUnitNumber, LegacyIdentity, OrphanAttachment, Unmatched, Ambiguous; candidate keys/evidence | One hardware card has at most one definitive claim. Pending/ambiguous claims protect candidates from cleanup. |
| Action | Kind, target identity, payload, dependencies, eligibility/reason | Edit and Remove cannot target the same card in one final ConfigSpec. Replace is an indivisible pair. |
| Progress | Unobserved/Waiting/ChangesPending/Converged/Failed plus per-interface details | Submitted work is never represented as observed success. |
| Retirement candidate | Old provider identity and observed attachment reference | Desired resources and hardware-referenced resources are retained. Delete uses UID preconditions. |
| Guest observation | Candidate Tools report, identity checks, validity reason, attachment revision/invalidation evidence | A cached report does not become current merely because it was reread; guest validity does not gate initial power-on. |
| Replacement operation evidence | Applied/target allocation identities, pre-disconnection connection policy, guest invalidation revision | Persist only facts that cannot be reconstructed after a crash; share an operation identity rather than creating multiple contradictory journals. |

Use explicit optional pointers/presence flags rather than treating an empty desired MAC as failed resolution or a zero device key as proof of absence. Empty and disabled networks are explicit observations, distinct from unknown state. Define disabled-network device-removal behavior from the existing feature-enabled/disabled contract and test it; do not conflate it with a failed provider listing.

## Planning table

Eligibility is checked per proposed operation and current power state. Any row that needs unknown information becomes Deferred for the affected interface, while independently safe actions continue.

| Desired and provider state | Hardware mapping | Proposed outcome |
|---|---|---|
| Ready desired interface | No matching hardware, absence established | Add with provider MAC or generated addressing policy. |
| Pending new interface | No hardware known | Wait; reserve the desired entry and provider resource. |
| Ready desired interface | Exact match, attachment agrees | NoOp; learn observed MAC without modifying desired identity. |
| Ready replacement | Existing adapter identified; edit supported | Edit attachment fields on a copy of the concrete adapter; preserve other fields. |
| Ready replacement and escape hatch | Existing adapter identified; remove and add both supported | Replace once; clear stale observed association and re-observe after completion. |
| Pending replacement | Existing adapter identified | Save policy, disconnect when supported, and retain old backing/resource; restore saved policy after replacement is ready or safely cancelled. Report blocked isolation honestly. |
| Unchanged attachment temporarily pending | Existing adapter identified | Retain working configuration; report waiting. |
| Interface removed from desired spec | Hardware association definitive | Remove if eligible; retain provider resource until observed detached. |
| Any desired interface | Ambiguous mapping or incomplete inventory | Protect candidate adapters; defer affected operations. Never infer removal from a partial ready list. |
| Supported desired state, live operation unsupported | Any | Defer to power-off; keep dependent operations together. |

Matching and comparison are separate: a numbered card may be correctly identified while its backing or pinned MAC is wrong. When a provider recreates a CR, distinguish a changed UID from a changed hardware attachment; do not churn hardware solely because a UID changed if all required attachment fields still agree.

See [replacement.md](./replacement.md) for exact identity, addressing, field clearing, connection restoration, and atomic device-change composition. These are required semantics, not optional optimizations.

## Readiness predicates

- **ProviderReady**: Every required desired interface has sufficient provider data for its next hardware operation. A generated MAC may still be absent.
- **HardwareConverged**: The current observation proves required additions, edits, connection policy, and removals have completed, with unambiguous identity and no outstanding dependent changes. Powered-off cards are judged by persistent configuration, not the runtime-only `Connected` value.
- **MACsResolved**: Every bootstrap-required interface has a verified effective MAC. Provider-pinned values must agree with the applied hardware; generated values come from hardware.
- **ReadyForBootstrap**: The preceding predicates hold against the same current desired state, plus existing bootstrap prerequisites.
- **ReadyForPowerOn**: Network hardware and required bootstrap work are ready for the current desired state. Never inferred from a condition, a prior pass, or an absent state holder.

The create path checks provider prerequisites before creating hardware; it cannot require a generated MAC or hardware convergence before creation. After creation it enters the same observe-before-bootstrap/power-on contract. No-network VMs bypass network-specific requirements once that state is explicitly established.

## Observation after reconfiguration

An Edit may retain a known key for later verification. An Add or Replace must resolve against new observed hardware. A temporary add key is not a durable key, and a reused observed key is not evidence that replacement did not occur.

When enabled and present, unit number is the exclusive match. For legacy unnumbered interfaces, use unique provider/attachment evidence and surviving known mappings; do not zip a filtered set of ready interfaces with hardware. Multiple indistinguishable generated-MAC adapters on the same backing remain ambiguous until Q5 is resolved. A process-local before/after device delta can help diagnose a result but cannot be the sole restart-recovery mechanism.

## Conditions and status

Use the existing `VirtualMachineNetworkReady` condition to communicate required network prerequisites. Proposed reason constants are `InterfacesPending`, `HardwarePending`, `PowerOffRequired`, `InterfaceMappingAmbiguous`, and `InterfaceResolutionFailed`; final names should reuse suitable existing constants where available. Messages include sorted interface names and provider details. Clear stale reasons after convergence. Preserve the historical deletion of this condition for no-network VMs.

This broadens update-path reporting and must be covered in E2E. `VirtualMachineNetworkConfigSynced` retains its existing NIC-tuning owner. The unit-number hardware verification condition reports placement/configuration disagreement through its established owner; do not introduce a second writer with competing semantics.

Build status from all desired interfaces first, overlay observed hardware, then merge only guest reports accepted by the rules in [status.md](./status.md). Per-interface `ProviderReady`, `HardwareReady`, `Ready`, and `GuestInfoValid` conditions are projections of internal facts. `Ready` combines provider and hardware readiness; it excludes Tools availability to avoid a bootstrap/power-on cycle. Unknown guest observations do not erase valid hardware fields. Desired guest configuration remains distinct from both hardware and guest observations.

The early status phase reports only facts observed before configuration. Provider resolution later in the pass may refresh its owned conditions, but it cannot turn planned hardware into observed hardware. A successful reconfigure retains the existing observation barrier; the next pass publishes the actual hardware result. Preserve condition transition times when status does not change and use the current VM generation for the desired state evaluated.

## Internal annotation

Proposed key: `vmoperator.vmware.com/network-interface-replace-on-change`. Only the exact value `"true"` enables it; absent, false, or other values do not. It is read only under the new feature flag and applicable mutable-network policy. Its scope is a VM's necessary attachment replacements, not all device drift, no-op reconciles, pending attachments, or adapter-type changes. The annotation cannot bypass live eligibility, schema-upgrade checks, or unit-number identity rules. Q4 must be resolved before shipping it.

