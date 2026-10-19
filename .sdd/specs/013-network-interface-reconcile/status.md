# Per Interface Status and Guest Report Validity

The proposed interface status is assembled from desired interfaces, observed virtual hardware, and validated guest reports. Each source owns different fields. This lets a pending interface appear immediately and prevents stale Tools data from creating a false picture of the current attachment.

Everything in this document applies only with the hot-plug capability (`Policy.InterfaceStatus` in [provider-design.md](./provider-design.md)). Without it, the restructured status code keeps today's Tools-reported entries, and the new fields are removed from the installed CRD.

## Current implementation

`vmprovider_vm.go:reconcileStatus` builds a device-key-to-spec-index map, then passes it to `vmlifecycle.ReconcileStatus`. Its guest phase calls `updateGuestNetworkStatus` in `pkg/providers/vsphere/vmlifecycle/update_status.go`.

That function currently iterates `GuestInfo.Net`, skips negative `DeviceConfigId` pseudo-devices, assigns the spec interface name when the device-key map resolves it, and creates an entry containing the guest-reported key, MAC, IP configuration/addresses/DHCP, and DNS. Unmatched guest records can appear with an empty name. It does not seed entries from the desired spec or verify each guest MAC against current hardware. It also does not gate this construction on Tools-running status; that condition is updated separately afterward.

Primary IPs come from `guest.ipAddress`, with cloud-init `guestinfo.local-ipv4`/`local-ipv6` taking precedence when applicable and per-interface dual-stack fallbacks filling eligible gaps. `VirtualMachineGuestNetworkConfigSynced` primarily checks whether selected primary IPs occur in the per-NIC guest data. Agreement between two cached values is therefore enough for that check; it is not evidence that either describes the current attachment.

NIC tuning status later looks up an existing status entry by interface name and fills hardware-derived NUMA/VMXNet3 fields. It returns without creating an entry if none exists. Thus a newly desired interface without a Tools report has no entry in the current implementation, even if its virtual adapter already exists. If all guest data disappears, the guest status builder can clear network status while preserving `status.network.config`.

`status.network.config` is separately built from resolved bootstrap arguments. It describes intended/configured IPAM, DHCP, routes, and DNS settings, not what Tools observed and not proof that customization succeeded. Keep this distinction in the new model.

## Proposed public fields

Q6 confirms the API version before these fields are implemented. The default is spec 008's precedent: extend v1alpha6 `VirtualMachineNetworkInterfaceStatus`, and remove the new fields from the installed CRD in `pkg/crd/crd.go` while the hot-plug capability is absent, as is done for `unitNumber`. Add:

| Field | Type and validation | Meaning |
|---|---|---|
| `conditions` | Optional `[]metav1.Condition`; map list keyed by `type`; standard condition validation | Per-interface outcomes, with reason/message, observed generation, and transition time. |
| `hardware` | Optional object | Present only for a definitively matched, observed virtual adapter. |
| `hardware.macAddress` | Optional MAC string, using existing MAC validation conventions | vSphere hardware MAC, available without Tools and never synthesized from desired state. |
| `hardware.connected` | Optional boolean | Observed runtime connection state; omit when not meaningful, including a powered-off VM. |
| `hardware.startConnected` | Optional boolean | Observed persistent startup connection policy. |

Keep existing `deviceKey`, NUMA/VMXNet3 fields, and the unit-number work's observed field (`status.network.interfaces[].unitNumber`, already in v1alpha6) at their established locations. Spec-first assembly also populates that field for interfaces Tools does not report. Its godoc, which currently says entries exist only for Tools-reported interfaces, and spec 008 G2/T021 must be updated together. `deviceKey` is populated from actual hardware, never from a proposed Add. Do not rename or change its type in place. The guest `ip.macAddr`, addresses, DHCP, and `dns` retain guest provenance; the new hardware MAC avoids pretending Tools supplied a value.

The desired interface's spec name is its status-entry identity. Preserve existing interface-list schema semantics: `name` remains optional for real unmanaged hardware entries, so the list must not suddenly become a map keyed by name. Conditions within each entry can use map semantics independently. Fields are controller-owned observations, optional, and not user-immutable inputs. Conditions use the current VM generation to describe the evaluated desired state; that generation is not a guest-report revision.

## Condition meanings

| Type | True | False | Unknown |
|---|---|---|---|
| `ProviderReady` | Required provider resource/backing data resolved | Known provider waiting/failure, with its reason | Observation unavailable or read failed. |
| `HardwareReady` | Current adapter type, attachment, and connection policy match the requested state | Missing adapter, pending edit/disconnect/type replacement, or unsupported live operation | Hardware unavailable or mapping ambiguous. |
| `Ready` | ProviderReady and HardwareReady are true for this desired interface | A prerequisite is known false | No known false prerequisite, but some required fact is unknown. |
| `GuestInfoValid` | Guest report passes current identity checks and has no unresolved attachment invalidation | Report contradicts hardware/attachment, such as `MACMismatch` | `DeviceNotPresent`, `NotReported`, `ToolsUnavailable`, `VMNotRunning`, or `AwaitingGuestReport`. |

`Ready` means ready at the provider/virtual-hardware boundary. It makes no promise of guest IP configuration or reachability. `GuestInfoValid=True` means accepted under these validation rules, not a measured maximum report age. Actual guest network synchronization and probe results remain separate concepts. Known missing/invalid guest information must not make the derived network-readiness predicate for bootstrap and power-on depend on a running guest.

Conditions are projections of internal state, never inputs to device planning or power decisions. Preserve `lastTransitionTime` when the condition status stays the same; change reason/message/generation without manufacturing transitions. Update aggregate VM conditions through their established owners using the same internal facts. Explicitly clear/update the aggregate guest-synchronization condition when guest observations vanish, rather than leaving an earlier True behind.

## Construction and missing interfaces

1. Seed one entry for every desired spec interface, in deterministic spec order, before resolving provider or hardware state. This also applies before VM creation completes; unavailable hardware is an explicit state.
2. Resolve provider status independently. A newly created but unready CR changes only provider facts; it does not create an observed adapter.
3. Overlay current hardware using the shared matching result. Fill observed key, unit number, hardware MAC/connection state, and tuning fields. If mapping is absent or ambiguous, leave those fields unset; do not borrow another adapter or copy desired placement into observed fields.
4. Join guest candidates to this hardware only after the checks below. Fill guest `ip` and `dns` only from accepted reports. Assemble from current facts rather than carrying fields over by array index.
5. Append actual unmanaged or retiring hardware entries in stable key order with no claimed spec name. Tools-only records whose device is no longer in hardware do not create ghost entries. For a retiring managed card, use conditions with the prior interface name in the message, actual key/unit, and `RemovalPending`/`PowerOffRequired`; do not lose the user's ability to identify the pending removal merely because its spec entry disappeared. Unmanaged cards are not falsely given desired-interface ProviderReady/Ready conditions. After removal is observed, the retiring entry disappears even if Tools still reports it.
6. Select primary IPs from accepted, eligible per-interface addresses and existing preference rules. Separately publish current resolved per-interface intent in `status.network.config` without requiring execution of customization or a complete all-interface bootstrap list. Omit obsolete config for a replaced/removed/pending interface instead of retaining old values under the same name. Retain valid resolved values for independent interfaces and represent them in stable order. This remains intended configuration, not proof of application or guest observation.

The early status phase uses pre-config observations. Provider resolution later in the pass may refresh its owned conditions, but hardware conditions remain pending until an observed refresh. No phase reordering is required. Config failure cannot erase the seeded status entries.

Seed the interface list at the common provider create/update entry, including create failures before a vSphere VM exists; otherwise a new status builder reached only by update would still hide initial provider waiting. Each writer must preserve fields owned by other status contributors. Patch only changed status with the repository's patch helper; stable observations must not continually update timestamps or trigger reconciles. `observedGeneration` records which desired state was evaluated, not successful application.

| State | Hardware fields | Guest fields | Representative conditions |
|---|---|---|---|
| Provider pending; adapter absent | Omitted | Omitted | ProviderReady=False, HardwareReady=False/DeviceNotPresent, Ready=False, GuestInfoValid=Unknown. |
| Provider ready; Add not yet observed | Omitted | Omitted | ProviderReady=True, HardwareReady=False/DeviceNotPresent, Ready=False. |
| Adapter added; Tools has not reported it | Actual key/unit/MAC/configuration | Omitted | HardwareReady=True, Ready=True, GuestInfoValid=Unknown/NotReported. |
| Guest report accepted | Actual hardware | Accepted guest data | GuestInfoValid=True; Ready still reflects provider/hardware only. |
| Replacement pending; old card retained | Actual old card, including disconnected state once observed | Invalidate old attachment's active guest fields | HardwareReady=False/AttachmentPending, Ready=False, GuestInfoValid=Unknown/AwaitingGuestReport. |
| VM powered off or Tools unavailable | Preserve observed hardware; omit runtime-only connection state when off | Omit current guest fields | GuestInfoValid=Unknown/VMNotRunning or ToolsUnavailable. |

Illustrative pending status in the selected API version:

```yaml
status:
  network:
    interfaces:
    - name: eth1
      conditions:
      - type: ProviderReady
        status: "False"
        reason: ProviderPending
        message: Waiting for SubnetPort allocation
        observedGeneration: 12
        lastTransitionTime: "2026-10-18T12:00:00Z"
      - type: HardwareReady
        status: "False"
        reason: DeviceNotPresent
        message: No adapter has been observed for this interface
        observedGeneration: 12
        lastTransitionTime: "2026-10-18T12:00:00Z"
      - type: Ready
        status: "False"
        reason: ProviderPending
        message: Waiting for network prerequisites
        observedGeneration: 12
        lastTransitionTime: "2026-10-18T12:00:00Z"
      - type: GuestInfoValid
        status: "Unknown"
        reason: DeviceNotPresent
        message: No adapter is available to associate with guest information
        observedGeneration: 12
        lastTransitionTime: "2026-10-18T12:00:00Z"
```

After hardware appears, `deviceKey`, `hardware`, and the applicable observed unit number are filled even if `ip`/`dns` remain absent. No synthetic guest IP or zero-valued placeholder key is needed.

## Stale Tools data

The documented `GuestNicInfo` has a device link, MAC, connection flag, network description, and IP/DNS data, but no per-NIC report timestamp or attachment generation. Therefore retrieval time is not report time, and the operator cannot prove age solely from this object. [GuestNicInfo API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.GuestInfo.NicInfo.html)

Apply these checks before publishing active guest data:

- Require a currently observed hardware device for `DeviceConfigId`; never let a guest record resurrect a removed card.
- Require an unambiguous association. Compare normalized guest and hardware MACs when available; mismatch is invalid. Missing identity evidence or duplicate candidates is unknown, not a reason to fall back to positional matching. A legitimate guest MAC override still needs an explicit supported association policy; it cannot silently be treated as the hardware MAC.
- Treat VM-not-running and Tools-unavailable observations as unknown guest validity, even when vCenter still exposes cached NIC values. Tools-running by itself does not prove a report was refreshed.
- Invalidate reports across relevant attachment or hardware changes: backing, effective address identity, remove/add (including type changes with both key and MAC reuse), and uncertain reconnect/revert transitions. Unrelated spec generations and compute reconfigures do not invalidate every NIC.
- For a change that retains key and MAC, retain an attachment-specific invalidation marker and pre-change report evidence. A matching key/MAC and agreeing cached global IP cannot clear that marker. A display-name `GuestNicInfo.network` comparison is not a globally unique backing identity.
- Revalidate using a demonstrated report update/acknowledgment mechanism selected by Q7, scoped to the affected attachment. A different report payload can be evidence to investigate, but payload change alone does not guarantee all fields are current. The same is true of a generic property-collector event. Until revalidation is established, use Unknown/AwaitingGuestReport; do not auto-accept the old value after a fixed delay.

Persist invalidation before or atomically with an attachment-changing reconfigure, so a crash after the task cannot make a stale report appear valid. A candidate design records the target attachment revision and previous guest-report digest in VM ExtraConfig in the same task; an alternative must provide equivalent crash safety. Merely adding a new context field or changing a status condition after the task is insufficient. Q7 must resolve failed/uncertain tasks, out-of-band changes, initial adoption, rollback, snapshot restore, and record cleanup. A broad hardware `changeVersion` is not a guest-report generation. Status conditions can expose uncertainty but cannot be the reconciliation journal.

On restart, missing/ambiguous durable evidence cannot clear an existing invalidation. On snapshot revert or attachment drift, reconstruct or invalidate the evidence again. Do not require Tools report changes forever merely because the operator restarted when no attachment change occurred and durable evidence remains valid. This distinction and the liveness of same-IP recovery are gating experiments, not assumptions.

Reject invalidated data from current `ip`, `dns`, primary IPs, and guest-synchronization calculations. Do not keep last-known addresses in active fields with only a warning condition: existing Service/probe consumers read primary fields directly. Historical guest data is not added in this proposal; if later needed, it belongs in a distinctly historical field with explicit provenance.

Global `guest.ipAddress`, cloud-init local-IP hints, and fallback-selected addresses must be corroborated by accepted eligible per-interface observations before primary publication. An IP found only on a disconnected, retiring, or invalidated attachment is ineligible. Do not filter the raw guest address list to exact provider IPAM addresses: guests can legitimately have additional addresses. Reject proven contradictions where desired configuration constrains a value; subnet membership or equality to an expected address is not a freshness certificate.

Global guest IP stacks/routes cannot always be attributed to an interface. Suppress unassignable aggregate guest network data during an attachment-invalidated interval rather than suggesting a valid NIC certifies the whole stack. Hostname remains separate guest information and is not evidence of network convergence. A stricter hostname freshness contract is outside this NIC proposal.

## User feedback and recovery

Keep reason constants stable and messages specific. Include interface name, target network/provider object reference, actual observed connection state, and next action where known; include old-to-new attachment details for replacement without exposing credentials or dumping whole API objects. Expected waiting is not a Warning Event on every pass. Emit rate-limited transition Events for disconnect, unsupported operation, reconfigure failure, completed attachment and reconnection, and failed resource retirement.

| Situation | Example feedback and recovery |
|---|---|
| New interface waiting on provider | `ProviderPending`: `eth1 is waiting for SubnetPort ns/port-b for network net-b; no adapter has been added. Inspect that resource's Ready condition if allocation does not complete.` |
| Device type replacement pending | `HardwarePending`: identify the observed and requested types and unit number. Explain whether the complete Remove+Add pair is waiting for provider readiness, a user-requested power-off, or correction of an unsupported destination configuration. Keep actual hardware visible until replacement is observed. |
| Replacement disconnected | `ReplacementPending`: `eth1 remains on the old attachment with connection disabled while port-b is pending. Once port-b is ready, the adapter will switch to it and reconnect in the same reconfigure; restore the previous network in the spec to cancel.` Cancellation revalidates the old allocation before automatically reconnecting. |
| Live edit or remove unsupported | `PowerOffRequired`: identify `eth1` (or retiring key/unit) and operation; state `Set spec.powerState to PoweredOff, wait for the change to apply, then request PoweredOn.` Use the actual API enum spelling in shipped messages. A guest reboot alone is not power-off. |
| Disconnect failed | `DisconnectFailed`: `eth1 is still connected to the old attachment; the disconnect task failed: <concise fault>.` Give the actual retry/operator action rather than asserting isolation. |
| Virtual hardware ready, guest configuration absent | `Ready=True`; `GuestInfoValid=Unknown/NotReported`: `The adapter is attached. Guest settings are available in status.network.config; configure the guest or wait for its network agent. Hot-plug does not rerun first-boot customization.` |
| Guest report invalidated | `AwaitingGuestReport`: identify affected interface and withdrawn address publication; state the validated live recovery option or the supported offline recovery procedure. Do not leave the user with only “waiting.” |
| Cleanup pending after successful switch | Proposed VM condition `VirtualMachineNetworkResourcesCleanedUp=False`: name the old CR and error/dependency. Current interface Ready may stay true. Cleanup retries independently and clears the condition on success. |

After a configurable/documented observation interval, an unchanged unknown guest report can update its reason to `GuestReportUnverified` and emit a rate-limited diagnostic Event. The interval changes feedback, not validity. Do not invent a promise that an adapter is usable merely because the user waited long enough. Before rollout, select and document the interval through the ordinary configuration policy; it is not an API freshness SLA.

The platform offers `ResetGuestInformation` to clear cached guest data while a VM is powered off, requiring `VirtualMachine.Config.ResetGuestInfo`. This supplies an offline recovery mechanism to validate in R12: after a user-requested shutdown and observed attachment convergence, clear the cache once for the pending operation, observe cleared guest fields, then allow the requested power-on and validate subsequently populated reports. It affects VM-wide guest information, so account for all interfaces and other guest-status consumers. Do not call it while running, automatically power-cycle a VM, or repeat it on every powered-off reconcile. Privilege/support failures remain visible and do not turn guest validity into a prerequisite for first power-on. [ResetGuestInformation API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.VirtualMachine.html#resetGuestInformation)

The reset does not establish that cached cloud-init ExtraConfig hints were refreshed, nor that the guest adopted the intended IP configuration. Keep primary hints subject to accepted NIC data, and report desired-versus-guest configuration mismatch separately from report validity where expectations are checkable. Q7 still gates automatic live same-key/MAC recovery. Do not enable a case whose only outcome is permanently withdrawn service addresses with no tested recovery route.

Unknown guest data on one interface does not withdraw valid primary addresses from independent interfaces. Removing an interface's last eligible primary address must withdraw its Service endpoint and default probe target; recovery restores them. No code should consume the new condition as an authorization shortcut in place of the internal observation predicates.

## API compatibility and verification

Q6 confirms v1alpha6 (spec 008 precedent) or chooses the next version. Generate deepcopy, conversions, and CRDs. Document whether older APIs retain their historical Tools-driven projection or expose compatible subsets of spec-first entries; do not accidentally change that contract via generated conversion alone.

Test conversion annotation round trips, annotation-lossy old clients, status-subresource patches, and conflicts. Existing older conversions restore whole status from conversion data; new fields must not be erased, nor may old restoration data overwrite fresher guest validity. Never use restored condition values as evidence that an attachment's report is valid. Test absent versus false `hardware.connected`, condition list merging, unmanaged unnamed entries, and no fake observed device/unit when hardware is absent.

Add E2E for pending-before-create/add, Tools absent, same-key/different-MAC replacement, same-key/same-MAC network edits, removal with a lingering Tools report, Tools restart, operator crash/restart, and snapshot revert. Verify both reported conditions and removal/recovery of Service endpoints/default probe targets, plus first power-on success while GuestInfoValid is unknown.
