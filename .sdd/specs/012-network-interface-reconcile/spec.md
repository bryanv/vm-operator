# Feature Specification: Network Interface Reconciliation and Hot Plug

- **Feature branch**: proposed `feature/network-interface-reconcile` (draft prepared in the current checkout)
- **Fork**: TBD
- **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-10-18
- **Status**: Draft
- **Epic**: TBD — assign before merge
- **Related work**: [NIC unit numbers](../008-nic-unit-numbers/spec.md), vmop-3982; [device and bootstrap separation](../004-remove-network-interface-result/plan.md), vmop-3999

Existing VMs need to reconcile each network interface independently, preserve existing adapters when a network attachment is replaced, and apply supported changes while running. An interface awaiting its network provider must be visible as pending without preventing unrelated VM work.

## Goals

- **G1 MUST**: Add and remove interfaces on a powered-on VM when the platform and that VM support the requested operation. Unsupported live operations remain pending until the VM is powered off; the operator does not power-cycle the VM automatically.
- **G2 MUST**: Continue independent reconciliation for an existing VM when an interface is pending. Block bootstrap customization and transitions from powered off to powered on until required network prerequisites and hardware configuration are observed ready. Allow requested shutdown while networking is pending.
- **G3 MUST**: When the attachment for an existing logical interface is replaced, preserve its adapter using an in-place edit where supported. Preserve adapter type and placement, and preserve a generated MAC when valid for the new attachment. A change in a provider-pinned MAC must apply the new MAC coherently with its attachment identity.
- **G4 MUST**: If a replacement attachment is pending, retain and disconnect the identified existing adapter when supported, including disabling connection at the next power-on and guest reconnection. An unsupported or failed disconnect must report the actual connection state and required action. A temporary readiness failure of an unchanged attachment alone must not disconnect a working adapter. An unresolved mapping must not cause removal of a possibly still-required adapter.
- **G5 MUST**: Support NetOP/VDS, NCP/NSX-T, VPC, per-namespace provider selection, and the named-network test path. A provider need not supply a MAC before adapter creation; customization uses the MAC observed after vSphere configures the adapter.
- **G6 MUST**: Preserve the NIC unit-number feature's exclusive hardware identity semantics when enabled. A numbered interface with no adapter at its number stays unmatched; it never claims another adapter by backing, MAC, or array position. Unnumbered interfaces retain a supported compatibility path.
- **G7 MUST**: Report pending interface names and actionable reasons through VM conditions. Keep observed network status distinct from desired guest configuration. Neither a successful task submission nor provider readiness alone means hardware is converged.
- **G8 MUST**: Retain an old provider resource until hardware no longer uses its attachment. Cleanup must survive retries, operator restart, failed reconfiguration, and deletion/recreation of a provider object with the same name.
- **G9 SHOULD**: Provide an internal, undocumented escape hatch to replace an adapter instead of editing it when an attachment changes. It must obey readiness and live-operation restrictions and must not cause replacement on every reconcile.
- **G10 MUST**: Keep existing behavior with the new feature disabled, preserve create-path prerequisites, and deliver cluster-observable behavior with E2E coverage.
- **G11 MUST**: Expose status and conditions for every desired interface, including interfaces whose provider or virtual adapter does not yet exist. Distinguish provider readiness, applied hardware, and guest information. Absence of guest information must not prevent hardware readiness, bootstrap, or initial power-on.
- **G12 MUST**: Reject guest network reports that conflict with current adapter identity, and invalidate guest information when an attachment changes. Stale or unverified addresses must not be advertised as current primary addresses or make guest synchronization appear successful. Lack of freshness evidence must be represented explicitly; reading the same cached value again does not make it fresh.
- **G13 MUST**: Pending/error feedback identifies the affected interface, target provider resource/network, actual connection state, and whether the user should wait, fix a resource, configure the guest, or request shutdown. Removal and resource-cleanup work remain visible after an interface leaves the spec. Report transition Events without flooding unchanged reconciles.
- **G14 MUST**: An attachment replacement preserves unrelated adapter settings and intentional connection policy, clears obsolete provider identity correctly, and survives cancellation, superseding edits, restart, and concurrent VM updates. Each supported replacement scenario has a tested completion or explicit recovery path.

## Non-goals

- Replay first-boot customization to configure a newly hot-added guest interface. Guest operating-system configuration remains subject to the existing bootstrap mechanism and guest support.
- Change NIC unit-number assignment, backfill, admission, or old-version conversion contracts.
- Infer permission to change adapter type from a backing change. Type-changing convergence is separate work; unsupported types must remain intact and report any deferred operation.
- Treat an unvalidated placeholder backing as a prerequisite for the baseline implementation.
- Guarantee exact guest-report age without a source that supplies freshness evidence. The feature distinguishes contradictory, unavailable, and unverified information and does not invent report timestamps.

## User stories and acceptance criteria

### US1 Running VM changes

1. **AC1**: Given a running VM and supported adapter, adding a ready interface creates exactly one adapter without a power transition. Removing that interface removes its adapter; other adapters keep their identity and configuration. Repeated reconciliation performs no further change.
2. **AC2**: Given an unsupported live operation, the VM remains running, its condition names the deferred interface and explains that power-off is required, and independent supported work proceeds. After a user-requested shutdown, the pending change converges.
3. **AC3**: Given a pending new interface, unrelated VM updates and ready interface changes continue, and a requested shutdown succeeds. The pending interface does not disappear from readiness reporting or cause deletion of another adapter or desired provider resource.

### US2 Replacement attachments

4. **AC4**: Given an existing logical interface whose network attachment is replaced, a supported edit preserves adapter type and placement and a valid generated MAC. A provider-pinned MAC and attachment identifier both match the new provider resource afterward. Reconciliation is subsequently a no-op.
5. **AC5**: Given a pending replacement with a known existing adapter and supported disconnect, the adapter is retained and disconnected. It cannot reconnect on the next start or through guest control. Once the new attachment is ready, it is edited and its original connection policy restored; a previously disconnected adapter is not forced connected. Unsupported/failed disconnect reports actual state and required action. An unchanged attachment becoming temporarily unready does not trigger disconnection.
6. **AC6**: Given the replacement escape hatch, a real attachment change produces one removal and one addition when permitted. Leaving the annotation present does not churn a converged adapter. An unsupported replacement remains pending without removing only half of the pair.
7. **AC7**: Given failed, uncertain, or interrupted reconfiguration, old provider resources remain while their attachments are still used. After successful hardware observation, cleanup retries safely and does not delete a newer object that reused the old name.

### US3 Readiness and provider compatibility

8. **AC8**: Given an existing powered-off VM with an unready interface or unapplied network changes, bootstrap customization and power-on wait while independent reconciliation continues. Once current hardware and required MACs are observed, those actions can proceed. A new VM continues to wait for provider prerequisites before creation.
9. **AC9**: Given a provider that supplies no MAC, creation or hot-add succeeds without inventing a manual MAC. Status and applicable bootstrap data later use the vSphere-assigned MAC for the correct interface, including multiple interfaces on the same network. Unknown or ambiguous identity remains pending rather than assigning another interface's MAC.
10. **AC10**: Given numbered, unnumbered, and mixed interfaces, device reconciliation, status, network boot order, and NIC tuning agree about which hardware belongs to each interface. Reordering the spec list does not change numbered interface identity. An exact-number miss never falls back.
11. **AC11**: Given a provider read failure, a paused or suspended VM, a running task, a snapshot revert, or an operator restart, networking honors existing lifecycle restrictions and rebuilds its observations before acting. A missing readiness result cannot accidentally permit power-on.
12. **AC12**: Given any supported provider or feature-flag combination, feature-disabled behavior remains compatible. Flag-enabled create, powered-off resize, off-to-on, and powered-on paths obey the same network identity and readiness contracts.
13. **AC13**: Given a newly desired interface, status immediately contains its name and per-interface conditions even before its provider resource or adapter exists. Hardware identity and guest IP data are absent until observed; provider readiness alone does not imply that the adapter was added.
14. **AC14**: Given an observed adapter with no usable guest report, hardware status remains populated and hardware readiness can be true while guest information is unknown. A VM can bootstrap and power on without waiting for Tools to run.
15. **AC15**: Given removed hardware, a reused device key with a different MAC, duplicate/ambiguous guest reports, or unavailable Tools, stale guest data does not populate the current interface or primary addresses. The condition explains the failure or uncertainty. Services and default-target probes do not keep consuming withdrawn primary addresses.
16. **AC16**: Given an in-place attachment change preserving key and MAC, the old guest report is invalidated. Operator restart and snapshot recovery do not erase that uncertainty. A new attachment is not declared guest-synchronized merely because cached global and per-interface IPs agree. Repeated reads or elapsed time alone do not clear the uncertainty.
17. **AC17**: Given old-version clients, status patches, and conversion round trips, the new conditions and hardware fields obey an explicit API-version contract. Compatibility tests prevent information from being silently erased, wrongly attributed, or restored over fresher status.
18. **AC18**: Given a provider migration requiring removal of old attachment identity, observed hardware no longer carries the old external identity after success; missing required provider addressing remains pending rather than creating a wrongly addressed adapter. Existing adapter type, placement, and unrelated tuning survive an edit.
19. **AC19**: Given cancellation or superseding replacement, or an operator restart after disconnect, the latest desired attachment converges with the saved connection policy and no stale-resource deletion. A concurrent VM change is not overwritten by a stale device edit.
20. **AC20**: Given a removed interface that cannot yet be detached, users can still identify the retiring adapter and required action in status. Given unused-resource cleanup failure, hardware readiness remains accurate and cleanup feedback identifies the affected resource.
21. **AC21**: Given hardware-ready hot-plug with no guest configuration agent, status explains that guest configuration is still required and exposes current resolved settings. Unknown guest validity does not report end-to-end connectivity success. A documented and tested recovery procedure exists for stale data that cannot refresh live.
22. **AC22**: Given old/new provider allocations sharing a network or preventing overlap, replacement either completes through a validated handoff or reports the specific blocked dependency. It neither leaks resources through backing-only matching nor silently deletes an active allocation to break the dependency.

## Open questions

- **Q1** [NEEDS CLARIFICATION: Feature owner to assign the epic and shipping-task tickets before merge.]
- **Q2** [NEEDS CLARIFICATION: Platform owner to validate the supported live edit/add/remove matrix, including pinned/generated MAC transitions, pass-through adapters, guest constraints, and connection policy. Blocks enabling each unvalidated live operation.]
- **Q3** [NEEDS CLARIFICATION: Validate whether a disconnected placeholder backing is needed to release an old attachment, and whether it is accepted across supported backings and power states. Optional extension; baseline retains the old backing and resource until a successful edit/removal.]
- **Q4** [NEEDS CLARIFICATION: Feature owner to confirm the internal replacement annotation name and scope. Proposed VM-wide opt-in applies only to attachment replacement; blocks escape-hatch implementation.]
- **Q5** [NEEDS CLARIFICATION: Resolve indistinguishable unnumbered adapters on the same backing after restart. Preferred rollout uses the unit-number feature for these cases; validate whether legacy evidence can provide a durable unique mapping without adding persistence. Blocks claiming general hot-plug support for ambiguous unnumbered VMs.]
- **Q6** [NEEDS CLARIFICATION: API owner to select the next unshipped API version for per-interface status changes, or establish that v1alpha6 is still eligible for changes. Define conversion and older-version status projection before implementing serialized fields.]
- **Q7** [NEEDS CLARIFICATION: Validate a guest refresh/revalidation signal for attachment edits retaining key and MAC, and the durable invalidation record's location. A cache reread, Tools-running flag, arbitrary timeout, or unrelated property change is insufficient. Until validated, affected guest data stays unknown and is excluded from active IP publication. Blocks claiming automatic guest-data recovery for these cases.]
- **Q8** [NEEDS CLARIFICATION: Validate provider allocation overlap/release contracts, naming collisions, old-resource reachability on shared backings, and applied-allocation evidence for NetOP/NCP/VPC. Blocks enabling unvalidated provider replacement combinations.]

