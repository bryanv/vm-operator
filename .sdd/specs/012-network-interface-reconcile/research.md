# Network Reconciliation Research

## Repository findings

Findings refer to the checkout inspected on 2026-10-18. Proposed behavior is in [plan.md](./plan.md); the items below describe the code and existing design constraints that motivate it.

| Area | Evidence | Implication |
|---|---|---|
| Volume context | `pkg/providers/vsphere/vmprovider_vm.go`: `getVolumeInfo`; `pkg/util/volumes/volumes_util.go`: `VolumeInfo`, `WithContext`, `FromContext` | A per-pass observation in context is established practice. NIC identity semantics still differ from disk placement semantics. |
| Update preparation | `session/session_vm_update.go`: `reconcilePoweredOffOrPoweredOnVM` returns on `reconcileNetworkInterfaces` error before hardware reconfiguration | One pending provider currently blocks independent config work in that path. |
| Update provider reads | `network/network.go`: `CreateAndWaitForNetworkInterfaces` and provider-specific polling helpers | Update collection aggregates errors but discards all devices on any error. Replace with complete per-interface outcomes. |
| Create provider reads | `network/network.go`: `CreateNetworkDevices`; provider `vmCreateDoNetworking` | Create already ensures all CRs before collecting results, but still requires all to be ready. Preserve create prerequisites. |
| Mixed desired/observed state | `network/network.go`: `Device.EthCard`, `EthCardKey`, `MacAddress`; `network/reconcile.go`: `ReconcileNetworkInterfaces` | Matching currently writes observed MAC into desired card/result; distinguish those roles before adding broader edit behavior. |
| Existing edit precedent | `network/reconcile.go`: `findExistingEthCardForOrphanedCR` and edit branch | Same-name orphan CR evidence can retain an adapter's concrete type; the current edit resets backing/address fields and clears `SubnetId`. |
| Generated MAC | `network/network.go`: `getNetOPNetworkInterfaceDevice`; session `fixupMacAddresses` and `fixupMacAddressMutableNetworks` | NetOP may not supply MAC. Hardware observation is already needed, but positional and backing-based matching need stronger ambiguity handling for hot-plug. |
| Bootstrap separation | `network/bootstrap.go`: `BuildBootstraps`; session `reconcileNetworkAndGuestCustomizationState`; [vmop-3999 plan](../004-remove-network-interface-result/plan.md) | Keep bootstrap lazy and complete. Its length/order assumptions do not support filtering out pending interfaces. |
| Live path | Session `poweredOnReconfigure` | General NIC Add/Remove is absent; the VPC restore fixup is a special case that already edits attachments. |
| Task barrier | Session `doReconfigure` returns `ErrReconfigure` after successful reconfiguration | Post-apply observation normally belongs to the next pass. A proposed context cannot pretend it was refreshed in place. |
| Cleanup | `network/list_interfaces.go`: `ListOrphanedNetworkInterfaces`; session `reconcileNetworkAndGuestCustomizationState` | Desired CRs are currently derived from ready devices. A partial-result refactor must change that or it will misclassify pending CRs as orphaned. Cleanup is currently tied to bootstrap processing. |
| Matching consumers | `network/devices.go`; `pkg/vmconfig/bootoptions/bootoptions_reconciler.go`; `pkg/providers/vsphere/virtualmachine/networkextraconfig/nic_matcher.go` | Status/boot options and NIC tuning use separate matching paths; all must consume the same identity decision. |
| Conditions | `api/v1alpha6/virtualmachine_types.go` and `virtualmachine_network_types.go` | NetworkReady exists; NetworkConfigSynced is specifically NIC tuning; interface status has no readiness-condition field in this checkout. |
| Interface status creation | `vmlifecycle/update_status.go`: `updateGuestNetworkStatus`, `guestNicInfoToInterfaceStatus`, `updateInterfaceStatus` | Entries originate from Tools reports; missing reports mean missing entries. Hardware tuning only fills an existing named entry. Spec-first assembly is a deliberate behavior change. |
| Guest identity validation | `vmlifecycle/update_status.go`: guest NIC loop | Negative pseudo-device IDs are excluded, but no hardware MAC cross-check occurs here and missing device-map entries can become unnamed guest entries. |
| Primary IP synchronization | `vmlifecycle/update_status.go`: primary selection and `primaryIPsInNICData` | Global guest/cloud-init primary values and per-NIC data can agree while both remain stale. Current synchronization does not establish current-attachment validity. |
| Active IP consumers | `controllers/virtualmachineservice/virtualmachineservice_controller.go`: `virtualMachinePrimaryIPs`; `pkg/prober/probe/tcp.go`: `Probe` | A warning condition alone will not stop use of invalidated primary IPs. Verify active-field withdrawal and downstream recovery. |
| Older API status restore | `api/v1alpha2/virtualmachine_conversion.go`, `api/v1alpha5/virtualmachine_conversion.go`: `ConvertTo` and `ConvertFrom` | Conversion annotations preserve hub fields and whole-status restoration exists. Test that older status updates cannot overwrite fresher validity observations. |
| Adapter copy safety | `network/reconcile.go` and `network/nsxt.go` edit the selected observed card directly; `virtualmachine/networkextraconfig/nic_fields.go:findOrCreateDeviceEdit` also appends the observed device directly | The new planner must own typed deep copies and compose field contributions without changing the pre-task observation. |
| Empty fields on the wire | Pinned govmomi `vim25/types/types.go:VirtualEthernetCard` uses XML `omitempty` for ExternalId, SubnetId, AddressType and MacAddress | A Go zero value is not an explicit clearing request. Verify wire intent, especially cross-provider edits. |
| Provider allocation names | `network/network.go`: `NetOPCRName`, `NCPCRName`, `VPCCRName`, provider create helpers | Network-name-based naming, old naming fallbacks, and a shared VPC attachment reference require validated coexistence/handoff behavior. |
| Existing backing comparison | `pkg/util/resize/backings.go` matches DV switch/portgroup but not explicit port key; standard backing comparison uses device name | Reuse only where semantics fit; normalize actual requested backing identity rather than silently ignoring pinned port IDs or inventory scope. |
| Cloud-init action gate | `vmlifecycle/bootstrap_cloudinit.go:BootStrapCloudInit` gates on UpdatedEthCards with a comment assuming no hot-plug | Replace this incidental boolean with explicit apply/readiness state while preserving bootstrap idempotence and separating status projection from execution. |
| Admission and watches | `webhooks/virtualmachine/validation/virtualmachine_validator.go`: mutable-network update validation; `controllers/virtualmachine/virtualmachine/virtualmachine_controller.go`: provider CR watches | Admission and CR watches already support parts of the workflow; runtime eligibility still belongs in the provider. |
| Pending spec differences | [Spec 008](../008-nic-unit-numbers/plan.md) references older result names and some older package locations | Integrate its contracts against current source, not its old symbol spellings. |

Paths beginning `network/` and `session/` in this table are relative to `pkg/providers/vsphere/`.

## Unit-number constraints

[Spec 008 G11](../008-nic-unit-numbers/spec.md) requires exact-only matching for numbered interfaces and a numbered-first claim pass. It explicitly defers edit-in-place and live hardware convergence. Its MAC-coherence warning applies directly: an edit must never combine a new provider attachment ID with a MAC inherited from a different provider-pinned interface.

The [existing platform research](../008-nic-unit-numbers/research.md) reports explicit unit-number hot-add in E15 and same-slot removal/addition in E06 on its recorded vCenter/ESX builds. It also reports key reuse after replacement. This is useful evidence for those testbed cases, not a supported matrix for all NIC types, guest OSes, providers, or live edits. Tests must not equate unchanged device key with absence of replacement.

## Platform API references

- `VirtualDeviceOption.plugAndPlay` describes hot-add capability, while `hotRemoveSupported` describes hot-remove capability. This provides separate eligibility inputs, not a complete live-edit policy. [VirtualDeviceOption](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDeviceOption.html)
- `VirtualDeviceConnectInfo.connected` is meaningful while running; `startConnected` controls connection on startup, and `allowGuestControl` controls guest connection control. The pending-replacement policy must account for all applicable fields. [VirtualDeviceConnectInfo](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDevice.ConnectInfo.html)
- `GuestNicInfo` documents device identity and guest configuration fields without a per-interface report timestamp. `GuestInfo.toolsRunningStatus` describes Tools availability, not the age of a NIC report. No freshness guarantee can be inferred from rereading these cached properties. [GuestNicInfo](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.GuestInfo.NicInfo.html), [GuestInfo](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.GuestInfo.html)
- The Ethernet-card API explicitly uses an empty external ID to unset it and permits the platform to override Generated/Assigned MACs. The subnet-ID description does not establish the same unset behavior. [VirtualEthernetCard](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualEthernetCard.html)
- DV port connection cookies are server-generated; a portgroup target need not pin a port, and binding rules constrain when a port key may be supplied. [DistributedVirtualSwitchPortConnection](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.dvs.PortConnection.html)
- Device configuration expects necessary complete device properties. ConfigSpec supports a changeVersion guard. ReconfigVM applies the submitted configuration atomically; this does not include provider CR changes or make client timeouts definitive. [VirtualDeviceConfigSpec](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDeviceSpec.html), [ConfigSpec](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.ConfigSpec.html), [ReconfigVM_Task](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.VirtualMachine.html#reconfigure)
- `ResetGuestInformation` clears the VM's guest cache only while powered off and requires `VirtualMachine.Config.ResetGuestInfo`. This is an offline recovery candidate, not permission to power-cycle automatically or evidence of live report freshness. [ResetGuestInformation](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.VirtualMachine.html#resetGuestInformation)

## Local serialization experiment

Ran a temporary Go program using the repository's pinned govmomi `v0.57.0-alpha.0.0.20260930024634-9b65cf3964d3`. It created a `VirtualVmxnet3` with a connected `Connectable`, nonempty external/subnet IDs, copied it with `types.DeepCopy[types.BaseVirtualDevice]`, assigned empty IDs and `Connected=false` on the copy, then marshaled a ConfigSpec containing the Edit with `vim25/xml.Marshal`.

```text
concrete_copy_type=*types.VirtualVmxnet3
original_connection_preserved=true
external_id_element_present=false
subnet_id_element_present=false
connected_false_on_wire=true
```

This verifies copy isolation for the exercised nested pointer and wire omission of empty IDs. It does not prove how any vCenter treats omitted subnet IDs, establish a working explicit-empty serializer, or validate platform live edits. The complete request serializer and server read-back remain required implementation tests.

## Required experiments

Record VM hardware/guest versions, actual card subtype, provider/backing, host and vCenter versions, capability inputs, exact requested ConfigSpec, fault/task results, and before/after hardware. Use disposable test VMs. Vcsim is not evidence of live platform support.

1. **R1 / Q2**: Hot-add and hot-remove each supported card/provider combination; include denied support and runtime restrictions. Verify capability lookup against actual acceptance and guest observation.
2. **R2 / Q2**: Edit backing, external ID, and provider-pinned MAC together while on/off. Cover generated-to-generated, pinned-to-pinned, pinned-to-generated, and generated-to-pinned transitions. Verify destination-port/MAC coherence, generated-address preservation where permitted, and idempotence.
3. **R3 / Q2**: Disconnect a pending replacement while retaining its backing, prevent guest reconnection, restart the operator, then reconnect after provider readiness. Check startup behavior and explicit connection policy.
4. **R4 / Q3**: Try a disconnected `VirtualEthernetCardNetworkBackingInfo` placeholder with no unintended real attachment across supported backing types/power states. Determine whether it releases the old provider port and whether reverting to the real backing preserves identity. Optional; do not enable it on assumption.
5. **R5 / Q2**: Replace at the same unit number while running, including pair rejection. Observe actual MAC/key results and verify no cleanup follows a failed or uncertain task.
6. **R6 / Q5**: Add two unnumbered, generated-MAC NICs to the same backing; interrupt after reconfiguration and restart before MAC observation. Establish whether provider identity can distinguish them durably. If not, require unit numbers for this case or design explicit persistence before enabling it.
7. **R7**: Fail or time out reconfiguration, delete/recreate CRs with the same names, rapidly change desired networks, and revert a snapshot. Verify reconstruction from observed state, correct UID deletion preconditions, and no stale power-on authorization.
8. **R8 / Q7**: Change attachment while preserving key/MAC and initially preserving the guest IP report. Record guest-property changes, Tools state, task ordering, and any available refresh/acknowledgment evidence. Test same-IP recovery, delayed mixed reports, guest MAC overrides, and no Tools. Determine exactly which signal establishes acceptable post-change data; do not equate a poll timestamp or arbitrary timeout with freshness.
9. **R9 / Q7**: Crash between invalidation persistence, reconfigure, and status publication; repeat with failed/uncertain tasks, snapshot revert, and out-of-band NIC changes. Prove restart preserves uncertainty and does not indefinitely invalidate unchanged attachments. Validate active primary-IP withdrawal/recovery through Service endpoints and default probes.
10. **R10 / Q2**: Capture full SOAP requests for explicit ExternalId clear, subnet-ID reset behavior, Generated/Assigned preservation and manual-to-managed generation. Verify each concrete card's `xsi:type`, full configuration, platform read-back, and subsequent no-op. Cover same-backing external-ID change without accidental port rebind.
11. **R11 / Q8**: Exercise allocation overlap, same-network replacement, legacy CR names, Subnet/SubnetSet name collisions, A→B→A cancellation, and A→B→C supersession for every provider. Record when old allocations can be released, especially when hardware has no per-CR identity or guest IPAM still uses the old allocation.
12. **R12 / Q7**: Validate powered-off cache reset, cleared-data observation, next-boot report recovery, same-IP and changed-IP cases, missing privileges, crash between steps, and VM-wide effects. Verify cloud-init cached primary hints cannot undo invalidation and that guest validity does not block power-on. This gives Q7 a concrete offline recovery path while the live signal is investigated.

## Draft decisions

Keep old backing plus disconnection as the baseline; make placeholder backing optional. Use explicit readiness data rather than conditions for control flow. Preserve unit-number identity and lazy bootstrap construction. Prefer unique matching evidence over destructive guesses. Keep old provider resources until observed detachment, even if this delays cleanup. Gate rollout of platform-dependent cases on the experiments above.

The user-requested status extension supersedes the initial no-new-fields scope: add per-interface conditions and observed hardware fields in an eligible API version, and assemble entries independently of Tools. See [status.md](./status.md). Treat guest-report consistency and source freshness as distinct claims; unvalidated refresh behavior remains Q7.

## Correctness review dispositions

The review tightened the plan in six areas: explicit replacement classification and field ownership; XML-presence semantics for clearing; saved connection policy and cancellation; changeVersion/no-op handling plus atomic task fault isolation; allocation-specific cleanup; and actionable status with a tested guest-data recovery route. [replacement.md](./replacement.md) is the detailed payload contract. The review also requires current resolved config to remain discoverable without running bootstrap, and shutdown to take precedence over new live NIC tasks.

Remaining rollout gates are intentional: live edit/MAC/subnet behavior (Q2/R10), ambiguous legacy mapping (Q5), API version/conversion (Q6), guest recovery/persistence (Q7/R12), and provider allocation handoff (Q8/R11). The local experiment is not a substitute for these platform/provider tests.

