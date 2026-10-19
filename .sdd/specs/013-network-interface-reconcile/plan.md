# Implementation Plan: Network Interface Reconciliation and Hot Plug

- **Spec**: [spec.md](./spec.md)
- **Epic**: vmop-4418
- **Date**: 2026-10-18
- **Status**: Draft proposal; platform experiments and shipping-task ticket assignment remain open

## Summary

Introduce a per-reconcile network inventory, separate identity matching from desired-state comparison, and compute a device-change plan shared by all eligible update paths. Provider pending states become per-interface data; bootstrap and power-on consume explicit readiness predicates. Preserve the existing separation between `Device` and lazily constructed `Bootstrap` established by [vmop-3999](../004-remove-network-interface-result/plan.md).

The restructuring ships ungated and replaces today's implementation with behavior parity, plus the pending-interface and cleanup fixes. A Supervisor capability gates only the new user-visible behavior: live changes, in-place attachment edits, adapter-type support, per-interface status, and the bootstrap rule. See "Feature gating".

## Technical context

- Root module uses Go 1.26.9, govmomi `v0.57.0-alpha.0.0.20260930024634-9b65cf3964d3`, controller-runtime, Ginkgo v2, and Gomega. No new dependencies are proposed.
- Current VM API is v1alpha6. Root-module provider, config, VM configuration adapters, `pkg/crd`, the API module (types, godoc, and older-version conversions), generated CRDs, and the separate `test/e2e` module change. Per-interface serialized status additions follow spec 008's v1alpha6 precedent unless Q6 selects another version.
- [Spec 008](../008-nic-unit-numbers/plan.md) is partially implemented. Its API surface landed in `3bff55da6` (#2002): `spec`/`status.network.interfaces[].unitNumber`, the `VMNetworkUnitNumbers` flag and capability, `FeatureVersionNICUnitNumbers`, older-version restore, and CRD field gating. Its matching, placement, backfill, admission, status population, and divergence tasks are still open. Its exclusive unit-number matching contract is the integration boundary. This feature is the follow-on committed by spec 008 T032 (vmop-4092). When enabled, it supersedes 008's interim replace-not-edit convergence and its Tools-only status scope. Both are written into the published `unitNumber` godoc and must be revised (see "API and CRD strategy").
- The current source already uses `[]Device` and lazy `BuildBootstraps`. Spec 008 tasks that still name `NetworkInterfaceResult` (for example T007) must be reconciled against this checkout.
- The detailed shape of the new provider code — packages, types, the per-pass flow, and how each existing call site changes — is in [provider-design.md](./provider-design.md).

## Data flow and ownership

Use `network.Info` and `network.ReconcileState` in `pkg/providers/vsphere/network`, with a small `WithContext`/`FromContext` API following the volume-info pattern. Keep vSphere work inside the provider tree. See [model.md](./model.md) for the proposed fields and state transitions.

Allocate one state holder at the outer provider update boundary on every update pass. Its initial readiness is unknown. Fetch/read observed network information alongside the existing volume-info preparation before status; do not create provider resources or mutate hardware at this stage. Provider read failures are recorded without turning preparation into a new early return that prevents status or shutdown. Status uses the observation. Existing schema-upgrade and snapshot-revert ordering remains intact.

Inside the existing config phase, ensure provider resources for every desired interface, refresh their readiness, then rebuild matching against the current spec and hardware. Keep one `InterfaceState` per desired interface, containing its spec, provider resolution, hardware match, and attachment-change classification; do not use parallel slices or compact the list to only ready entries. A matched adapter and a Pending desired CR coexist in that same record. The config phase explicitly replaces the state holder's observation and plan; helpers do not mutate observed hardware or overwrite desired fields as a side effect. This avoids today's value-copy/shared-slice behavior of `NetworkInterfaceResults` and `UpdatedEthCards`.

The state is valid for a VM UID, spec generation/network fingerprint, and observed hardware version/change version. A schema upgrade that changes the spec requires a rebuild; revert or a completed reconfigure invalidates hardware observation. Recompute on the next pass, or explicitly re-fetch before any dependent action. A pointer shared across copied `VirtualMachineContext` values keeps the outer power-state phase from seeing a stale local context replacement.

Store the API interface spec once on `InterfaceState.Spec`; derive its name/unit number from that spec and presentation order from the containing slice. Do not add a separate desired-interface wrapper or a progress enum. Readiness and sorted pending messages are calculated from the current observation, matching plan, and task-submission state. Bootstrap and the off-to-on network gate share this one network-readiness predicate; the lifecycle still checks its separate customization/power prerequisites.

## Provider resolution and matching

Replace the update path's polling collector with an ensure-all/read-all pass. Preserve provider identity and error details even when no usable `Device` can yet be built. Expected waiting is `Pending`; transport/API failures are `Failed` with wrapped errors. Continue independent work, aggregate genuine errors at the provider boundary, and use existing CR watches plus a bounded retry for states without a dependable watch or hardware refresh trigger. Missing provider status is not a successful resolution.

Expose a read-only `network.Observe` before status and one `network.Prepare` call inside configuration. They share collection/normalization code; Prepare also ensures CRs and resolves ready targets' backing handles. Decode each collected CR once and use create/patch results directly, retaining per-interface errors. Session does not orchestrate separate Ensure/Resolve/Observe passes or carry intermediate raw CR slices. Raw CRs and backing handles stay private to the network package, which builds device and lazy bootstrap projections. The config-phase refresh is still required after schema upgrade/revert; eliminating duplicate resolution does not allow reusing stale early observations.

Today's waits are `pkgerr.NoRequeueError` values (`ErrNetworkInterfaceNotReady`, NCP's missing `ProviderStatus`, VPC's missing `LogicalSwitchUUID`, a missing network `APIVersion`). Any of them makes `updateVirtualMachine` return before power state. Provider adapters return these as per-interface `Pending`/`Failed` outcomes, not errors. The config phase returns nil (or the usual `ErrReconfigure`), and a bounded retry is expressed as `pkgerr.RequeueError{After: …}`. `RequeueError` is not a `NoRequeueError`, so it lets an on-to-off transition proceed, while the explicit power-on gate still blocks starting the VM.

Attachment changes do not come only from `MutableNetworks` edits. Fail-over re-pointing (`vmopv1.FailedOverVMAnnotation`, admitted without `MutableNetworks`) and VPC restore (`nsx/reconfigure-nic`) produce the same "same logical interface, new attachment" observation. They go through the same planner instead of today's separate paths, with or without the capability.

Build one mapping used by device reconciliation, MAC observation, status, boot options, and NIC ExtraConfig. Claim numbered interfaces first, gated by `VMNetworkUnitNumbers`, using exact unit number only. Then claim adapters named by a durable operation record (Q7), which is what keeps adapters this feature has touched unambiguous after restart. Match remaining unnumbered interfaces against remaining hardware using legacy MAC/external-ID/backing evidence and the same-name orphan-CR association. Use a positional guess only for immutable-network status display, and never as a basis for planning. Matching is one-to-one; distinguish unmatched from ambiguous. Legacy positional behavior remains available on feature-disabled paths, but a positional guess is not sufficient authorization for destructive hot-plug actions.

Identity matching returns an observed card without copying its MAC into desired state. Separate comparisons decide whether its attachment or explicitly requested type differs. Each interface's `AttachmentChange` distinguishes Unchanged, Replacement, and Unknown using current hardware, provider references, and any durable applied-allocation evidence. Pending readiness alone cannot establish Replacement. All desired interfaces, including pending and failed ones, participate in device reservation. An ambiguous or incomplete observation protects candidate devices from unmatched-device removal. Definitively removed interfaces can still be removed when their hardware association is known.

## Device planning

Produce NoOp, Add, Edit, Disconnect, Remove, Replace, or Deferred per interface. The detailed decision table is in [model.md](./model.md).

The payload and handoff contract is in [replacement.md](./replacement.md). In particular, resolved provider fields carry Keep/Set/Clear intent; assigning an empty Go string is not evidence that an unset was serialized. Replacement detection compares the applied and desired attachment identities before constructing any card.

- Edit a typed deep copy of the observed concrete adapter for a replacement attachment. Preserve key, unit number, controller/PCI placement, device subtype, and fields owned by other reconcilers. Change backing, address policy, provider MAC, and external ID coherently; validate the wire-level reset of old `SubnetId` when the attachment changes. The current empty-string assignment omits this field from XML and does not establish server clearing behavior.
- A vSphere-managed MAC is an observed output. Preserve it and its Generated or Assigned type on a compatible edit when permitted; never adopt an observed MAC over a provider-pinned desired MAC. Manual-to-managed transitions require a validated policy, not reuse of the old provider's address by default. A provider that requires a pinned MAC but has not supplied one remains pending.
- A pending replacement with a proven mapping plans disconnection: `Connected=false` while running, `StartConnected=false`, and `AllowGuestControl=false` on validated platforms. Once the desired provider CR is ready, set `Connected=true` while running, `StartConnected=true`, and `AllowGuestControl=true` in the same device Edit that applies its resolved attachment, or on the Add when the type also changes. Do not save or restore the previous connection settings. Cancellation revalidates the latest desired allocation before reconnecting under the same rule. Attachment-operation evidence distinguishes an unfinished handoff from a later guest disconnect; it is not a saved connection policy. Unsupported disconnection or reconnection reports actual state and a blocked reason rather than claiming success. Q2 covers the exact connection payload. Connected/StartConnected spec fields remain future work.
- The optional placeholder `VirtualEthernetCardNetworkBackingInfo` is Q3 work. It must not use an arbitrary real network as a substitute. Keeping the old backing requires retaining the old provider resource until observation proves it is unused.
- The proposed `vmoperator.vmware.com/network-interface-replace-on-change: "true"` changes an otherwise necessary attachment Edit into Replace. It does not change identity selection or force replacement on no-op passes. Both removal and addition must be supported together. Do not silently retry every edit failure as a destructive replacement.
- Allocate distinct temporary keys for additions. Emit removals before additions when a slot is reused. Compose each device's edits once; never emit an Edit targeting a card removed by the same plan.
- Honor `spec.type` through a shared destination-type builder when `Policy.AdapterTypes` is enabled. An explicit type selects the requested concrete device; an omitted type preserves matched/class/image hardware and otherwise defaults to VMXNet3 for a new interface. This includes the type-support follow-on from spec 008 T032. Preserve existing type behavior when the policy is off, and audit create/deploy paths and existing API field gates without widening admission.
- A matched adapter with a different explicit type is always Replace: Remove followed by Add of the requested type at the same unit number in the same reconfigure. This decision precedes attachment-only Edit and applies even when the attachment is unchanged; no escape-hatch annotation or type-changing Edit is involved. Construct a fresh destination card with ready provider fields, connection defaults, and compatible tuning. Preserve the desired provider CR for a type-only change. If the attachment also changes, use its pending-disconnect policy until ready, then connect the new card in the replacement task. Retain the old card until the complete pair is eligible. Reobserve type/key/MAC and invalidate prior guest data even if identifiers are reused. An attachment-only escape-hatch Replace retains the observed type when none is requested.
- A numbered interface added to a powered-on VM gets its unit number at admission (spec 008 G4). A live Add places the adapter at that number. If live Add is unsupported, the interface stays in spec 008's G13 "no device at the declared number" state until power-off, and nothing else may claim that slot.

## Live-operation eligibility

Resolve device options for the relevant VM hardware version and execution environment through the existing environment-browser facilities where available. `VirtualDeviceOption.plugAndPlay` describes hot-add; `hotRemoveSupported` describes hot-remove. They are separate checks, and neither establishes support for a backing/MAC edit. [vSphere device options](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDeviceOption.html)

Also account for the guest descriptor's `supportsHotPlugPCI`, current runtime restrictions, adapter subtype, backing, relevant extraConfig, and guest constraints validated in Q2. A type replacement checks hot-remove for the source type and hot-add for the destination type, plus R14's supported pair at the same unit; eligibility of the old type alone is insufficient. Every new vCenter call this adds (configuration-option query, optional cache reset) wraps its context with `pkgctx.WithVCOpID` inside the provider method that makes it. Unknown support defers the affected live operation. Cache capability observations only within an appropriate VM/environment lifetime; migration and hardware upgrades can invalidate them. Report a typed deferred result rather than repeatedly submitting an operation known to be unsupported. Real task faults remain authoritative and must not be swallowed as readiness waiting.

Partition independent operations before building the ConfigSpec. Unsupported NIC work must not suppress independent compute/disk work; replacement pairs and other dependent device changes stay together. A definitive NIC-specific task fault must not cause indefinite resubmission of the same failing combined batch; observe, classify the fault, and defer only a known unsupported group while allowing independent work on a later pass. Unknown task outcomes require observation first. Suspended, paused, or task-busy VMs retain current dispatch behavior. Preserve the established post-reconfigure return barrier while hardware is stale.

Use one owned device-change accumulator with typed deep copies, semantic field comparison, and explicit conflicts between contributors. The NIC-tuning reconciler (`vmconfig/networkextraconfig`) runs later, inside `doReconfigure`, and today appends an Edit of the observed device. It must look up the planner's copy for the same key and reject a key the plan removes. Set `ConfigSpec.ChangeVersion` from `MoVM.Config.ChangeVersion` at the end of `doReconfigure`: after every `vmconfig` contributor, after the existing empty-spec check, and only when the capability is enabled and the final spec carries planned network device changes. This keeps the guard from widening to unrelated reconfigures. Refresh and replan on a version conflict. Do not add a changeVersion-only task on a steady-state pass. Keep environment-browser I/O outside the pure network planner: the root provider helper cannot be imported back into its network subpackage without a cycle, so inject resolved options or move only the query helper to a shared provider subpackage.

## Integration and readiness gates

Feed the planner into `poweredOnReconfigure`, `getConfigSpecForPoweredOffVM`, and `resizeVMWhenPoweredStateOff` through a common helper. Cover off-to-on and stay-off dispatch explicitly. Do not make network convergence depend accidentally on CPU/memory resize flags. Today the update path always applies NIC changes and the resize path does so only with `MutableNetworks`. Both paths take the same planner output. Fold `handleRestoredVPCInterfaces` into the shared config task rather than its separate `Reconfigure`. Clear the restore annotation only after the edit is observed, not after task submission as the powered-off paths do today.

After applying changes, use the existing `ErrReconfigure` barrier and rebuild from observed hardware on the next pass. Ensure task/property watches or a bounded requeue guarantee that pass. Do not build bootstrap data using a submitted Add's temporary key or an unobserved generated MAC. A task timeout has an unknown outcome: observe before retrying the plan.

`ReconcileState.Readiness(vmCtx).Ready()` derives whether all required provider data, unambiguous hardware mappings, observed applied changes, and effective MACs are available for the current inputs. An absent/stale plan or submitted hardware task cannot report ready. NoOp alone is insufficient because pending work may intentionally leave a card untouched; a required removal remains unapplied while its disconnected card still exists. Build the complete, ordered bootstrap projection only when ready; if existing `BuildBootstraps` needs a temporary `[]Device`, keep that adapter inside `network` rather than passing another device list through Session or storing it on reconcile state. Remove `UpdatedEthCards` (`NetworkInterfaceResults`, `BootstrapArgs`, the `BootStrapCloudInit` gate) rather than porting it. It can never be true when bootstrap runs today (see research), and the derived predicate replaces the protection it was meant to give.

Existing bootstrap idempotence is hash-based (`DoBootstrap`). At each off-to-on transition, LinuxPrep/Sysprep compare a hash of the whole `CustomizationSpec` with the `vmoperator.vmware.com/bootstrap-hash-customspec` annotation, unless `customizeAtNextPowerOn` is false. That spec includes the per-NIC `NicSettingMap`. Cloud-Init guestinfo is rewritten on a running VM whenever its inputs change.

Q9 is resolved (spec G15): with the capability, interface changes do not re-run LinuxPrep/Sysprep. One stored hash cannot tell a NIC-only change from any other change, so T032 adds a second hash:

- **New hash.** Hash the `CustomizationSpec` with `NicSettingMap` cleared and store it in a new annotation (proposed `vmoperator.vmware.com/bootstrap-hash-customspec-without-nics`; T003 fixes the name). With the capability, customize only when this hash changes. That customization still carries the full, current `NicSettingMap`.
- **Unchanged inputs.** When the new hash matches, refresh both annotations to their current values without customizing. Keeping the legacy full-spec hash current means that disabling the capability later does not re-customize for NIC changes made while it was enabled.
- **Migration.** When the new annotation is absent, compare the legacy hash as today. If it matches, write both annotations without customizing. If not, follow today's rule, because the old hash cannot show what changed. Enabling the capability alone therefore never customizes.
- **Unchanged rules.** `customizeAtNextPowerOn=false` still skips customization, and the Sysprep/LinuxPrep latch still forces it. Cloud-Init and vAppConfig keep the bootstrap ConfigSpec hash rule. Rewriting Cloud-Init metadata on a running guest does not re-apply its network configuration unless the guest's cloud-init policy does; this is today's behavior.
- **Consequence.** On a LinuxPrep/Sysprep VM, VM Operator does not configure an added or changed interface inside the guest. Status reports that guest configuration is still required (AC21), and a change to another customization input applies the current settings. Q10 evaluates vSphere flexible guest customization (network-only, API 9.1, running VM) for this gap; it needs guest credentials, which VM Operator does not hold (see research).

At the outer `reconcilePowerState`, gate only a transition from powered off to powered on on explicit network state and existing bootstrap prerequisites. Unknown/missing state blocks that transition. Do not gate resume from suspended. `reconcileSuspendedVM` never runs the network phase, so readiness is never established there; gating resume would deadlock a VM whose pending change needs it running or off, and resume runs no bootstrap. A blocked start returns without a NoRequeue error, so later steps such as snapshot create still run. `SetPowerStateSyncedCondition` then leaves `VirtualMachinePowerStateSynced` False because spec and status differ, and `VirtualMachineNetworkReady` carries the network reason. A running VM with pending networking stays running, and off requests remain available. VM conditions communicate these facts but are never consulted to make the decision. Restart/resume paths must be audited for network-dependent customization; this feature does not automatically restart a VM to apply networking.

When an already-running VM requests power-off, defer new live NIC tasks until the powered-off pass instead of repeatedly returning the reconfigure barrier ahead of shutdown. Keep existing task guards and top-level ordering. Expected interface waiting is returned as structured data, never wrapped in `NoRequeueError` or joined with fatal errors where it would short-circuit power-off. Genuine transient failures receive bounded retries; unsupported cases wake on relevant spec/power/provider changes with an actionable condition.

Keep `VirtualMachineConditionNetworkReady` (`VirtualMachineNetworkReady`) owned by the network prerequisite path, including interface names/reasons in deterministic order. Today only the create path writes it, so the update path becomes a second writer of the same condition through the same helper. Do not reuse `VirtualMachineNetworkConfigSynced`, which currently describes NIC tuning and has a separate writer. Report hardware divergence through the unit-number feature's hardware validation integration. Preserve current no-network condition deletion semantics while treating nil/disabled networking explicitly rather than as an uninitialized state.

## Resource cleanup

Compute the desired provider-resource set from all spec entries and provider identities, including pending entries. List only VM-owned resources. Garbage collection runs independently of bootstrap, but only deletes allocations that are no longer desired and whose provider-specific handoff/release prerequisites are established from fresh observations. A disconnected card still using the old allocation retains that resource; sharing its backing with another allocation does not itself establish a reference. Retain uncertain resources and retry; readiness cannot be used as a proxy for allocation reachability.

Use object UID delete preconditions. Recheck desired identity before deletion so a rapid spec change back to an old network cancels retirement. Restart reconstructs retirement candidates from owned resources and observed hardware; an in-memory plan is not the cleanup journal. Cleanup failures must be surfaced and retried rather than logged and forgotten. Generalize the VPC restore fixup through the same matching/edit path, preserving the restored-interface annotation until convergence is observed.

Apply provider-specific allocation reachability, not “any card on this backing still references this CR.” Shared VDS networks require distinguishing allocations that use the same backing; Q8 defines applied-allocation evidence and provider overlap/release contracts. Report cleanup separately through a proposed `VirtualMachineNetworkResourcesCleanedUp` condition and transition Events; cleanup of an unused old resource does not block otherwise-ready bootstrap or power-on. A missing ownership/handoff proof retains the resource and reports the concrete ambiguity.

## API and CRD strategy

Add per-interface `conditions` and optional `hardware` status as described in [status.md](./status.md). Seed an entry for every desired interface before hardware exists; keep existing guest `ip`/`dns` fields separate. This is a serialized API and status-semantics change.

Default to spec 008's precedent from this same release cycle unless Q6 rules otherwise. Add optional fields to v1alpha6. Remove them from the installed CRD while the capability is absent, using the `pkg/crd/crd.go:Install` `removeFields` gate exactly as `unitNumber` does. Rely on the existing older-version status restore (`dst.Status = restored.Status` in v1alpha2–v1alpha5) for hub-only status fields. The constitution's concern (k8s#111703) is that additive fields get wiped by old-version UPDATEs. For status, that concern reduces to old-version status-subresource writers. This repository writes VM status only through the hub version, but external writers are possible, so still test it. If Q6 instead selects a new version, use `hack/new-schema-version.py` and treat the whole status extension as that version's content.

Revise published documentation in the same change set that activates the behavior. `VirtualMachineNetworkInterfaceSpec.UnitNumber` currently says a numbered interface re-pointed at another network is replaced (new key, new MAC). `VirtualMachineNetworkInterfaceStatus.UnitNumber` says only Tools-reported interfaces have entries. Both must describe the feature-enabled behavior, then be regenerated into the CRD descriptions.

Specify older-version projection, conversion restoration, annotation-loss behavior, status-subresource updates, deepcopy/conversion generation, and CRD regeneration before implementation. Tests must cover current `dst.Status = restored.Status` conversion behavior so a preserved annotation cannot replace newer observations. Keep the interfaces list's existing semantics; do not declare it keyed by optional `name`, since observed unmanaged entries can be unnamed. Conditions themselves are a map list keyed by `type`.

Keep unit-number assignment/backfill in spec 008 and integrate its field in the selected API. No additional CRD kind is proposed. Reuse aggregate condition owners while deriving per-interface conditions from internal observations rather than using persisted conditions as flow-control inputs.

## Status assembly and stale guest reports

Replace the Tools-driven construction of the interface list with spec-first assembly, hardware overlay, validated guest overlay, and final primary-IP selection. Hardware fields remain available when Tools is missing. A desired adapter not yet present gets `HardwareReady=False/DeviceNotPresent`, no observed key, and no invented MAC or IP. Provider-ready/hardware-pending and hardware-ready/guest-unknown are separate states.

Validate guest device key and MAC against current hardware, exclude removed/replaced identities, and invalidate an attachment's guest information when backing/address identity changes. Key/MAC agreement alone does not validate cached IPs after an in-place edit. Track attachment revision and invalidation durably before a task can leave cached reports associated with changed hardware; resolve exact persistence and revalidation mechanics through Q7. Full policy, uncertainty behavior, and examples are in [status.md](./status.md).

Filter guest global IPs, cloud-init cached primary hints, and dual-stack fallbacks through the accepted per-interface observations before publishing active primary addresses. Audit `VirtualMachineService` and default-target TCP probes, which consume those addresses, and verify withdrawal/recovery. Do not replay bootstrap, change hardware, or block first power-on because Tools data is unavailable. Global IP stacks and hostname have separate provenance and must not silently be treated as validated by a single accepted NIC report.

Decouple `status.network.config` publication from executing customization: publish current resolved per-interface intent by name and omit obsolete values for pending replacements. Preserve the complete ordered input requirement for actual bootstrap execution. Users must be able to discover resolved guest settings without replaying one-shot customization or waiting for an unrelated pending interface. NIC readiness conditions explain which interfaces are not applied yet.

Provide the status messages, retiring-interface representation, Events, and guest recovery procedure in [status.md](./status.md). Offline `ResetGuestInformation` is a documented cache-reset mechanism worth validating for recovery, including its distinct privilege; it is never attempted while running and never triggers an automatic shutdown. Live same-identity recovery remains Q7. Do not enable a live-replacement scenario with indefinitely withdrawn addresses and no tested recovery procedure.

## Controller and webhook impact

Business logic stays under the vSphere provider. Audit existing NetOP, NCP, and VPC watches for pending-to-ready, delete, and failure transitions; add only missing watch/requeue behavior. No new Kubernetes resource kinds or RBAC are expected. Audit vCenter privileges separately, especially device connection/edit/add-remove and the optional `VirtualMachine.Config.ResetGuestInfo` recovery operation. Preserve `MutableNetworks` admission and the unit-number feature's powered-on and schema-upgrade guards; live convergence does not permit renumbering a running VM.

User-initiated mutability still requires `MutableNetworks`. Fail-over and VPC-restore attachment changes do not, as today. Test all combinations of the capability, `MutableNetworks`, `VMNetworkUnitNumbers`, and per-namespace provider selection. Provider choice, MAC source, and unit-number matching remain independent dimensions.

## Feature gating

The feature owner decided on 2026-10-20 that the restructuring ships outside any gate, and a Supervisor capability gates only the new behavior.

- **Capability.** Proposed key `supports_VM_service_network_hot_plug`, mapped to `pkgcfg.Features.VMNetworkHotPlug` in `pkg/config/capabilities` the same way `VMNetworkUnitNumbers` is. The platform must publish the key; T001 confirms the name with the platform owner. Code reads it only through `pkgcfg.FromContext(ctx)`.
- **Policy.** The planner and its consumers receive one `network.Policy` per pass, derived from the capability (see provider-design.md). Gated code reads only the policy, never the flag, so tests can exercise each behavior independently.
- **Activation.** The capability is a platform signal, not a VM Operator release boundary. A gated slice that merges before the rest of its delivery group stays unreachable: its policy field remains false until the group's last task enables it (tasks.md "Activation rules").
- **No fallback for the restructuring.** Parity rests on the T012 characterization tests, the existing unit and vcsim suites, and E2E. Land it in the small slices T013–T022 so each can be reverted alone.
- **Turning the capability off.** Reading and finishing an existing operation record is ungated. An already-started replacement may complete its validated attachment Edit and automatic reconnection once the current desired provider CR is ready; cancellation likewise revalidates its target before reconnecting. Capability withdrawal does not reconnect an obsolete allocation or authorize new replacement operations. Platform eligibility and shutdown priority still apply.

| Behavior | Without the capability | With the capability |
|---|---|---|
| Observe, match, plan, apply; one shared mapping | Yes, with behavior parity | Same |
| Pending interfaces as data; shutdown and independent work proceed; explicit power-on gate; update path writes `VirtualMachineNetworkReady` | Yes (G2 fix) | Same |
| Provider pass without polling; end-of-pass `RequeueError` | Yes | Same |
| Cleanup with UID preconditions, retried and reported | Yes (G8 fix) | Same |
| Unified update and resize dispatch; fail-over and VPC restore in the shared task | Yes | Same |
| `UpdatedEthCards` removed; MACs taken from observation | Yes | Same |
| Network change on a running VM | New operations deferred to power-off, now reported as `PowerOffRequired`; validated completion of an existing handoff remains permitted | Applied when eligible (G1) |
| VPC restore (`nsx/reconfigure-nic`) | In-place Edit while running and Remove and Add while powered off, as today; now in the shared task, and the annotation is cleared only once observed | Ordinary attachment Edit in both states |
| Attachment change on an identified adapter | Remove and Add, as today | Edit in place; disconnect while pending; escape hatch (G3, G4, G9) |
| Add with an explicit `type` | Existing behavior | Requested concrete type when supported (G16) |
| Explicit type differs from matched adapter | No new type convergence | Remove+Add at the same unit; live only for validated pairs (G16) |
| Per-interface status | Tools-reported entries; new fields removed from the CRD | Spec-first entries and new fields (G11, G12) |
| `ChangeVersion` guard on NIC tasks | Not set | Set |
| Operation record and guest invalidation | Read and finish existing records only | Written for new operations and updated through completion |
| LinuxPrep/Sysprep after an interface change | Re-customizes at next start, as today | Does not (G15) |

## Project structure

See [provider-design.md](./provider-design.md) for file-by-file contents.

- `pkg/providers/vsphere/network/{state.go,provider.go,matching.go,plan.go,edit.go,eligibility.go,operation.go}`: per-pass state and context accessors, provider resolution, identity matching, the device planner, edit construction, live-operation eligibility, and the durable operation record. Existing `network.go`, `devices.go`, `reconcile.go`, `list_interfaces.go`, `bootstrap.go`, `nsxt.go` become adapters or consumers. Consolidate files where that improves cohesion; these boundaries do not require a new abstraction for each file.
- `pkg/providers/vsphere/vmprovider_vm.go`, `pkg/providers/vsphere/session/session_vm_update.go`, `pkg/providers/vsphere/session/session_vm_update_network.go` [new]: collection, dispatch, explicit readiness, the power-on gate, cleanup, and create compatibility. The configuration-option query is resolved here and injected, because `network` and `session` cannot import the root provider package.
- `pkg/providers/vsphere/vmlifecycle/{update_status.go,bootstrap.go,bootstrap_cloudinit.go}`, `pkg/vmconfig/bootoptions/bootoptions_reconciler.go`, `pkg/vmconfig/networkextraconfig/reconciler.go`, `pkg/providers/vsphere/virtualmachine/networkextraconfig/{nic_matcher.go,nic_fields.go}`: consume common identity mapping, compose device edits, and drop `UpdatedEthCards`.
- `pkg/config/config.go`, `pkg/config/capabilities/capabilities.go`, `pkg/crd/crd.go`, API network/status types and godoc in the version selected under Q6 (default v1alpha6), older-version conversion tests, and `config/crd/`: feature wiring, CRD field gating, per-interface status, and generated schemas.
- Existing provider/session/network tests and `test/e2e/vmservice/vmservice/virtualmachine/`: behavioral coverage.

## Test strategy

Unit tests cover every planner row, exact-number misses, mixed claim order, ambiguous mappings, deep-copy safety, desired/observed MAC separation, disconnect/reconnect, coherent pinned-MAC edits, and conflicting device changes. Derived-readiness tests cover missing/stale plans, pending NoOp, task submission, and required removals still present while disconnected. Collection tests verify Observe performs no writes, Prepare retains per-interface errors, and neither path refetches objects merely to decode provider status twice. Use external test packages and existing Ginkgo suites.

Envtest/vcsim tests cover real reconcile entry points, pending interfaces alongside independent changes, power-on blocking and shutdown progress, fresh observation after task completion/failure, restart recovery, provider CR UID changes, partial resolution, resource retention, and generated-MAC association. Vcsim tests establish orchestration behavior, not platform hot-plug support.

Status tests cover desired-but-absent adapters, Tools-free hardware reporting, stable condition transitions, all guest identity rejection reasons, stale primary hints, same-key/MAC backing changes, durable invalidation after crashes, and API conversion/status-patch round trips. E2E also verifies Service endpoint withdrawal and default-target probe behavior when a formerly published address becomes invalid, followed by recovery once an acceptable report is available.

E2E scenarios ship in each behavior-changing PR: add/remove while running; edit and pending replacement; explicit-type create/add and same-unit type replacement; omitted-type preservation; unsupported operation then user shutdown; generated MAC followed by customization; old-resource cleanup; reordered/mixed interfaces; VPC restore; and capability-absent compatibility. Type replacement verifies source/destination eligibility, CR retention, guest invalidation, compatible tuning, no-op after restart, and no Edit targeting the removed card. Exercise each provider in its available environment and verify hardware plus guest-observable effects where appropriate. New/significantly changed E2E cases carry `experimental` until validated on real hardware. See [tasks.md](./tasks.md) for traceability and [research.md](./research.md) for gating platform experiments.

## Constitution check

| Rule | Status | Notes |
|---|---|---|
| Spec before behavior | OK | Draft spec/model/plan/tasks precede code; epic vmop-4418 is assigned. Assign shipping-task tickets before merge. |
| Thin controllers; no direct vSphere calls from controllers | OK | Controllers only gain watch/requeue changes if T022 finds gaps; the provider owns vSphere calls and planning. |
| Reconcile order (status → schema upgrade → revert → config → power → snapshot) | OK | No phase is reordered. The power-on gate is added inside `reconcilePowerState`; the observation is prepared before status. |
| Conditions communicate state, never control flow | OK | Explicit internal predicates gate bootstrap and power-on; per-interface and VM conditions are projections. |
| `pkgerr` requeue semantics | OK | Pending networking is data, not `NoRequeueError`; bounded retry uses `RequeueError`; `ErrReconfigure` remains the post-task barrier. |
| VC operation IDs (`WithVCOpID`) | OK | New configuration-option and guest-cache-reset calls set an op ID in the provider method that makes them. |
| Fan-out to child objects | OK | Provider CRs keep single-parent `CreateOrPatch` with a controller reference; deletion adds UID preconditions. No list-typed fan-in field is written. |
| API compatibility | Pending Q6 | Default is spec 008's v1alpha6 precedent: optional fields, `pkg/crd` gating while off, conversion and status-patch tests, regenerated CRDs. Published `unitNumber` godoc is revised in the activating change set. |
| Feature gating | OK | New user-visible behavior is gated by a capability read through `pkgcfg.FromContext`. The restructuring is ungated by the feature owner's decision, with parity tests and the G2/G8 fixes; see "Feature gating". |
| Tests and E2E | OK | Tests ship with behavior; E2E cases carry `experimental` until validated; platform claims require real-environment evidence. |
| One test file per package, `Label()` only | Deviation, accepted by the feature owner 2026-10-20 | `pkg/providers/vsphere/network` already has one test file per source file (eight today). The new source files in tasks.md follow that package convention; see Complexity tracking. Legacy `_intg_test.go`/`_unit_test.go` files are extended only where the package already uses them, never created. |
| Code conventions | OK | Copyright headers, godoc, `importas` aliases, wrapped errors, existing labels. |
| Ticket references | OK | `vmop-NNN` only; no invented ticket IDs. Draft shipping tasks remain unassigned. |

## Complexity tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| New per-source-file test files in `pkg/providers/vsphere/network` instead of a single `network_test.go` | Matches the package's existing layout; planner, matching, and edit tables are large | Appending to the 2,900-line `network_test.go` hurts review. The feature owner accepted the package's per-file layout on 2026-10-20. |

No other constitutional exception is proposed.

## Rollout / migration

The ungated restructuring rolls out with ordinary releases once T012's parity tests and the existing E2E suites pass. Enable the capability only after unit-number compatibility, Q2 support cases, Q6 API compatibility, and Q7 guest invalidation/recovery and the operation record are resolved for the enabled cases. Q9 is resolved; Q10 is optional and does not gate rollout. Release notes distinguish hardware hot-plug from guest IP configuration and state that interface changes no longer re-customize LinuxPrep/Sysprep guests when the capability is enabled. They also document spec-first status entries and withdrawn unverified IPs, call out the revised `unitNumber` replace-versus-edit behavior, and identify deferred operations. No new spec-field backfill is proposed; the operation record's initialization/migration is part of Q7, and the second bootstrap hash migrates as described above. The escape hatch remains internal, but its behavior and tests live in this spec.

Q8 also gates provider replacement support. Use explicit supported combinations instead of claiming every provider transition works before its allocation overlap, addressing, and cleanup behavior is validated. Source-level review and serializer experiments do not establish platform acceptance.
