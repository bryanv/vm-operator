# Implementation Plan: Network Interface Reconciliation and Hot Plug

- **Spec**: [spec.md](./spec.md)
- **Epic**: TBD
- **Date**: 2026-10-18
- **Status**: Draft proposal; platform experiments and ticket assignment remain open

## Summary

Introduce a per-reconcile network inventory, separate identity matching from desired-state comparison, and compute a device-change plan shared by all eligible update paths. Provider pending states become per-interface data; bootstrap and power-on consume explicit readiness predicates. Preserve the existing separation between `Device` and lazily constructed `Bootstrap` established by [vmop-3999](../004-remove-network-interface-result/plan.md).

## Technical context

- Root module uses Go 1.26.9, govmomi, controller-runtime, Ginkgo v2, and Gomega. No new dependencies are proposed.
- Current VM API is v1alpha6. Root-module provider, config, VM configuration adapters, the API module, generated CRDs, conversion code, and the separate `test/e2e` module change. Per-interface serialized status additions target the next unshipped API version selected under Q6.
- [Spec 008](../008-nic-unit-numbers/plan.md) is pending work, not an implementation to assume exists in this checkout. Its exclusive unit-number matching contract is the integration boundary. The new feature supersedes only its explicitly interim remove/add convergence when the new feature is enabled.
- The current source already uses `[]Device` and lazy `BuildBootstraps`; older names and paths in spec 008 must be reconciled against this checkout during implementation.

## Data flow and ownership

Use `network.Info` and `network.ReconcileState` in `pkg/providers/vsphere/network`, with a small `WithContext`/`FromContext` API following the volume-info pattern. Keep vSphere work inside the provider tree. See [model.md](./model.md) for the proposed fields and state transitions.

Allocate one state holder at the outer provider update boundary when the feature is enabled. Its initial readiness is unknown. Fetch/read observed network information alongside the existing volume-info preparation before status; do not create provider resources or mutate hardware at this stage. Provider read failures are recorded without turning preparation into a new early return that prevents status or shutdown. Status uses the observation. Existing schema-upgrade and snapshot-revert ordering remains intact.

Inside the existing config phase, ensure provider resources for every desired interface, refresh their readiness, then rebuild matching against the current spec and hardware. Never compact the desired interface list to only ready entries. The config phase explicitly replaces the state holder's observation and plan; helpers do not mutate observed hardware or overwrite desired fields as a side effect. This avoids today's value-copy/shared-slice behavior of `NetworkInterfaceResults` and `UpdatedEthCards`.

The state is valid for a VM UID, spec generation/network fingerprint, and observed hardware version/change version. A schema upgrade that changes the spec requires a rebuild; revert or a completed reconfigure invalidates hardware observation. Recompute on the next pass, or explicitly re-fetch before any dependent action. A pointer shared across copied `VirtualMachineContext` values keeps the outer power-state phase from seeing a stale local context replacement.

## Provider resolution and matching

Replace the update path's polling collector with an ensure-all/read-all pass. Preserve provider identity and error details even when no usable `Device` can yet be built. Expected waiting is `Pending`; transport/API failures are `Failed` with wrapped errors. Continue independent work, aggregate genuine errors at the provider boundary, and use existing CR watches plus a bounded retry for states without a dependable watch or hardware refresh trigger. Missing provider status is not a successful resolution.

Build one mapping used by device reconciliation, MAC observation, status, boot options, and NIC ExtraConfig. Claim numbered interfaces first, gated by `VMNetworkUnitNumbers`, using exact unit number only. Match unnumbered interfaces against remaining hardware using legacy MAC/external-ID/backing evidence and the same-name orphan-CR association. Matching is one-to-one; distinguish unmatched from ambiguous. Legacy positional behavior remains available on feature-disabled paths, but a positional guess is not sufficient authorization for destructive hot-plug actions.

Identity matching returns an observed card without copying its MAC into desired state. A separate comparison decides whether its attachment differs. All desired interfaces, including pending and failed ones, participate in device reservation. An ambiguous or incomplete observation protects candidate devices from unmatched-device removal. Definitively removed interfaces can still be removed when their hardware association is known.

## Device planning

Produce NoOp, Add, Edit, Disconnect, Remove, Replace, or Deferred per interface. The detailed decision table is in [model.md](./model.md).

The payload and handoff contract is in [replacement.md](./replacement.md). In particular, resolved provider fields carry Keep/Set/Clear intent; assigning an empty Go string is not evidence that an unset was serialized. Replacement detection compares the applied and desired attachment identities before constructing any card.

- Edit a typed deep copy of the observed concrete adapter for a replacement attachment. Preserve key, unit number, controller/PCI placement, device subtype, and fields owned by other reconcilers. Change backing, address policy, provider MAC, and external ID coherently; validate the wire-level reset of old `SubnetId` when the attachment changes. The current empty-string assignment omits this field from XML and does not establish server clearing behavior.
- A vSphere-managed MAC is an observed output. Preserve it and its Generated or Assigned type on a compatible edit when permitted; never adopt an observed MAC over a provider-pinned desired MAC. Manual-to-managed transitions require a validated policy, not reuse of the old provider's address by default. A provider that requires a pinned MAC but has not supplied one remains pending.
- A pending replacement with a proven mapping plans disconnection: `Connected=false` while running, `StartConnected=false`, and `AllowGuestControl=false` on validated platforms. Persist the pre-disconnection policy with the operation so it survives retries/restart, then restore it when replacement completes or is safely cancelled. Do not reconnect a card that was already intentionally disconnected. Unsupported disconnection reports actual state and a blocked reason rather than claiming isolation. Q2 covers the exact connection payload.
- The optional placeholder `VirtualEthernetCardNetworkBackingInfo` is Q3 work. It must not use an arbitrary real network as a substitute. Keeping the old backing requires retaining the old provider resource until observation proves it is unused.
- The proposed `vmoperator.vmware.com/network-interface-replace-on-change: "true"` changes an otherwise necessary attachment Edit into Replace. It does not change identity selection or force replacement on no-op passes. Both removal and addition must be supported together. Do not silently retry every edit failure as a destructive replacement.
- Allocate distinct temporary keys for additions. Emit removals before additions when a slot is reused. Compose each device's edits once; never emit an Edit targeting a card removed by the same plan.

## Live-operation eligibility

Resolve device options for the relevant VM hardware version and execution environment through the existing environment-browser facilities where available. `VirtualDeviceOption.plugAndPlay` describes hot-add; `hotRemoveSupported` describes hot-remove. They are separate checks, and neither establishes support for a backing/MAC edit. [vSphere device options](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDeviceOption.html)

Also account for current runtime restrictions, adapter subtype, backing, relevant extraConfig, and guest constraints validated in Q2. Unknown support defers the affected live operation. Cache capability observations only within an appropriate VM/environment lifetime; migration and hardware upgrades can invalidate them. Report a typed deferred result rather than repeatedly submitting an operation known to be unsupported. Real task faults remain authoritative and must not be swallowed as readiness waiting.

Partition independent operations before building the ConfigSpec. Unsupported NIC work must not suppress independent compute/disk work; replacement pairs and other dependent device changes stay together. A definitive NIC-specific task fault must not cause indefinite resubmission of the same failing combined batch; observe, classify the fault, and defer only a known unsupported group while allowing independent work on a later pass. Unknown task outcomes require observation first. Suspended, paused, or task-busy VMs retain current dispatch behavior. Preserve the established post-reconfigure return barrier while hardware is stale.

Use one owned device-change accumulator with typed deep copies, semantic field comparison, and explicit conflicts between contributors. After semantic no-op detection, set `ConfigSpec.ChangeVersion` for a real task using the observation version; refresh and replan on conflict. Do not add a changeVersion-only task on a steady-state pass. Keep environment-browser I/O outside the pure network planner: the root provider helper cannot be imported back into its network subpackage without a cycle, so inject resolved options or move only the query helper to a shared provider subpackage.

## Integration and readiness gates

Feed the planner into `poweredOnReconfigure`, `getConfigSpecForPoweredOffVM`, and `resizeVMWhenPoweredStateOff` through a common helper. Cover off-to-on and stay-off dispatch explicitly. Do not make network convergence depend accidentally on CPU/memory resize flags.

After applying changes, use the existing `ErrReconfigure` barrier and rebuild from observed hardware on the next pass. Ensure task/property watches or a bounded requeue guarantee that pass. Do not build bootstrap data using a submitted Add's temporary key or an unobserved generated MAC. A task timeout has an unknown outcome: observe before retrying the plan.

`ReadyForBootstrap` requires all required provider data, unambiguous hardware mappings, observed applied changes, and resolved effective MACs. Build the complete, ordered `[]Device` projection only then and call the existing lazy `BuildBootstraps`; do not feed it a partial list. Audit `UpdatedEthCards` consumers so removals and edits are handled correctly without relying on a boolean that is lost across copies or reconciles. Preserve existing bootstrap completion/idempotence rules; hot-add does not authorize replaying one-shot customization.

At the outer `reconcilePowerState`, gate only a transition that starts the VM on explicit network state and existing bootstrap prerequisites. Unknown/missing state blocks that transition. A running VM with pending networking stays running, and off requests remain available. VM conditions communicate these facts but are never consulted to make the decision. Restart/resume paths must be audited for network-dependent customization; this feature does not automatically restart a VM to apply networking.

When an already-running VM requests power-off, defer new live NIC tasks until the powered-off pass instead of repeatedly returning the reconfigure barrier ahead of shutdown. Keep existing task guards and top-level ordering. Expected interface waiting is returned as structured data, never wrapped in `NoRequeueError` or joined with fatal errors where it would short-circuit power-off. Genuine transient failures receive bounded retries; unsupported cases wake on relevant spec/power/provider changes with an actionable condition.

Keep `VirtualMachineNetworkReady` owned by the network prerequisite path, including interface names/reasons in deterministic order. Do not reuse `VirtualMachineNetworkConfigSynced`, which currently describes NIC tuning and has a separate writer. Report hardware divergence through the unit-number feature's hardware validation integration. Preserve current no-network condition deletion semantics while treating nil/disabled networking explicitly rather than as an uninitialized state.

## Resource cleanup

Compute the desired provider-resource set from all spec entries and provider identities, including pending entries. List only VM-owned resources. Garbage collection runs independently of bootstrap, but only deletes allocations that are no longer desired and whose provider-specific handoff/release prerequisites are established from fresh observations. A disconnected card still using the old allocation retains that resource; sharing its backing with another allocation does not itself establish a reference. Retain uncertain resources and retry; readiness cannot be used as a proxy for allocation reachability.

Use object UID delete preconditions. Recheck desired identity before deletion so a rapid spec change back to an old network cancels retirement. Restart reconstructs retirement candidates from owned resources and observed hardware; an in-memory plan is not the cleanup journal. Cleanup failures must be surfaced and retried rather than logged and forgotten. Generalize the VPC restore fixup through the same matching/edit path, preserving the restored-interface annotation until convergence is observed.

Apply provider-specific allocation reachability, not “any card on this backing still references this CR.” Shared VDS networks require distinguishing allocations that use the same backing; Q8 defines applied-allocation evidence and provider overlap/release contracts. Report cleanup separately through a proposed `VirtualMachineNetworkResourcesCleanedUp` condition and transition Events; cleanup of an unused old resource does not block otherwise-ready bootstrap or power-on. A missing ownership/handoff proof retains the resource and reports the concrete ambiguity.

## API and CRD strategy

Add per-interface `conditions` and optional `hardware` status as described in [status.md](./status.md). Seed an entry for every desired interface before hardware exists; keep existing guest `ip`/`dns` fields separate. This is a serialized API and status-semantics change. The constitution does not consider additive fields safe in a shipped version: default to the next unshipped version, with Q6 resolving whether that is a new version or an eligible current version. Do not simply append fields to shipped v1alpha6.

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

Propose `pkgcfg.Features.VMNetworkReconcile`, default false, for the new path, with mutability still requiring `MutableNetworks`. Access both through `pkgcfg.FromContext(ctx)`. Provider choice, MAC source, and unit-number matching remain independent dimensions. Test all combinations of the new flag, `MutableNetworks`, `VMNetworkUnitNumbers`, and per-namespace provider selection. Flag-off behavior uses the existing implementation. Before rollout, define whether disabling the new path while replacement is pending is supported; conservatively require convergence first so legacy code cannot strand a disconnected adapter.

## Project structure

- `pkg/providers/vsphere/network/{info.go,matching.go,plan.go,capabilities.go,edit.go,transition.go}`: proposed inventory, matching, planner, eligibility, edit construction, and handoff state; existing `network.go`, `devices.go`, `reconcile.go`, `list_interfaces.go`, `bootstrap.go`, `nsxt.go` become adapters or consumers. Consolidate files where that improves cohesion; these boundaries do not require a new abstraction for each file.
- `pkg/providers/vsphere/vmprovider_vm.go`, `pkg/providers/vsphere/session/session_vm_update.go`: collection, dispatch, explicit readiness, cleanup, create compatibility.
- `pkg/providers/vsphere/vmlifecycle/update_status.go`, `pkg/vmconfig/bootoptions/bootoptions_reconciler.go`, `pkg/vmconfig/networkextraconfig/reconciler.go`, `pkg/providers/vsphere/virtualmachine/networkextraconfig/{nic_matcher.go,nic_fields.go}`: consume common identity mapping and compose device edits.
- `pkg/config/config.go`, `pkg/config/capabilities/capabilities.go`, API network/status types in the version selected under Q6, older-version conversion code, and `config/crd/`: feature wiring, per-interface status, and generated schemas.
- Existing provider/session/network tests and `test/e2e/vmservice/vmservice/virtualmachine/`: behavioral coverage.

## Test strategy

Unit tests cover every planner row, exact-number misses, mixed claim order, ambiguous mappings, deep-copy safety, desired/observed MAC separation, disconnect/reconnect, coherent pinned-MAC edits, and conflicting device changes. Use external test packages and existing Ginkgo suites.

Envtest/vcsim tests cover real reconcile entry points, pending interfaces alongside independent changes, power-on blocking and shutdown progress, fresh observation after task completion/failure, restart recovery, provider CR UID changes, partial resolution, resource retention, and generated-MAC association. Vcsim tests establish orchestration behavior, not platform hot-plug support.

Status tests cover desired-but-absent adapters, Tools-free hardware reporting, stable condition transitions, all guest identity rejection reasons, stale primary hints, same-key/MAC backing changes, durable invalidation after crashes, and API conversion/status-patch round trips. E2E also verifies Service endpoint withdrawal and default-target probe behavior when a formerly published address becomes invalid, followed by recovery once an acceptable report is available.

E2E scenarios ship in each behavior-changing PR: add/remove while running; edit and pending replacement; unsupported operation then user shutdown; generated MAC followed by customization; old-resource cleanup; reordered/mixed interfaces; VPC restore; and feature-disabled compatibility. Exercise each provider in its available environment and verify hardware plus guest-observable effects where appropriate. New/significantly changed E2E cases carry `experimental` until validated on real hardware. See [tasks.md](./tasks.md) for traceability and [research.md](./research.md) for gating platform experiments.

## Rollout and constitution check

| Rule | Plan |
|---|---|
| Spec before behavior | Draft spec/model/plan/tasks precede code; assign real tickets before merge. |
| Feature gating | New path defaults off; no silent change to existing flag semantics. |
| Thin controllers and vSphere boundary | Controllers only orchestrate/watch; provider owns vSphere calls and planning. |
| Reconcile order | Keep existing phase order, including status before upgrade and revert before config. |
| Conditions communicate state | Explicit state predicates gate bootstrap and power-on. |
| API compatibility | Per-interface status requires Q6 version selection, conversion/status-patch tests, and regenerated CRDs; preserve spec 008's identity contracts. |
| Tests and E2E | Tests ship with behavior; platform claims require real-environment evidence. |
| Code conventions | Correct Go headers, godoc, aliases, contextual errors, and existing labels. |
| Ticket references | `vmop-NNN` only; no invented ticket IDs. Draft shipping tasks remain unassigned. |

Roll out after unit-number compatibility, Q2 support cases, Q6 API compatibility, and Q7 guest invalidation/recovery are validated for the enabled cases. Release notes distinguish hardware hot-plug from guest IP configuration, document spec-first status entries and withdrawn unverified IPs, and identify deferred operations. No new spec-field backfill is proposed; the guest invalidation record's initialization/migration is part of Q7. The escape hatch remains internal, but its behavior and tests live in this spec. No constitutional exception is proposed.

Q8 also gates provider replacement support. Use explicit supported combinations instead of claiming every provider transition works before its allocation overlap, addressing, and cleanup behavior is validated. Source-level review and serializer experiments do not establish platform acceptance.

