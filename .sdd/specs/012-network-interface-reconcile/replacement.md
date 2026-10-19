# Replacing a Network Attachment by Editing Its Adapter

This is the detailed implementation contract for the replacement path in [plan.md](./plan.md). Replacing a provider attachment and replacing virtual hardware are distinct actions: the normal attachment replacement edits the existing concrete adapter. Remove/add is an explicit fallback, subject to independent capability and readiness checks.

## Identify a replacement before building a device

Keep three identities separate: the logical interface (VM UID and spec name), provider allocation (group/kind, namespace/name, UID and effective attachment), and hardware adapter (exclusive unit number when enabled, otherwise a proven legacy association). A spec array index is presentation order, not identity.

| Observation | Classification and action |
|---|---|
| Same logical interface/adapter, different desired network or resolved attachment | Attachment replacement. Compare the old applied tuple with the new resolved tuple and prefer Edit. |
| Same CR name, new UID, unchanged effective backing/MAC/external ID | Resource recreation; refresh ownership/readiness evidence without an unnecessary hardware edit. |
| Same provider resource, new effective backing/MAC/external ID | Attachment update; UID equality does not imply a no-op. |
| Same resource temporarily unready, no evidence of changed attachment | Report provider waiting without disconnecting the working card. |
| Explicit new network reference, new provider allocation not ready | Replacement pending. Reserve and disconnect the identified old card when that operation is supported. |
| Numbered interface with no card at its declared unit | Unmatched under spec 008. Do not rename/reuse a different card as a shortcut. |
| Spec name removed and new name added | Ordinary removal/addition unless the unit-number contract explicitly identifies existing hardware; do not infer a rename from position. |

Network identity includes API group/kind and namespace/name, not only the reference's name. A default network must be resolved to its effective provider identity. Old NetOP/NCP names and `Subnet` versus `SubnetSet` can collide under current name helpers; do not mutate a still-applied old CR merely to reuse a name. The provider adapter must either confirm the existing resource represents the desired network or use the provider's validated replacement lifecycle. This is Q8 work.

Before modifying provider intent, capture a usable observation of the applied attachment, including the old CR identity and matching evidence. An orphan CR label or name suffix alone is insufficient to claim arbitrary hardware. Missing or conflicting evidence is `InterfaceMappingAmbiguous`, protecting candidates from removal. The exact-unit-number path still takes precedence over all legacy evidence.

## Normalize provider output

Provider adapters return a resolved target with backing identity, external-ID intent, MAC policy, and provider allocation identity. These are values consumed by a pure planner; no Kubernetes or vSphere I/O occurs inside matching/diff construction.

Use explicit Keep/Set/Clear semantics for optional attachment fields. A provider being unready is Unknown, not Clear. A resolved destination that does not use external IDs is an authoritative Clear of an old external ID. Conversely, a provider that requires a pinned MAC or external ID but reports an empty/zero value is incomplete and remains pending; do not silently downgrade it to generated addressing. Preserve provider-specific override rules.

Resolve NSX logical networks for the VM's actual cluster/host context. Standard networks, DV portgroups/ports, and opaque networks need distinct comparisons. Compare semantic identity, not the entire server-populated structure:

- Standard network: use the resolved network MoRef where supported, with its resolved device name for the request. Do not equate same display names across different inventory scopes. Validate the standard-network serializer/target selection in Q2.
- DV portgroup: compare switch UUID and portgroup key. Compare a port key only when the provider explicitly requests a particular port. Ignore a server-assigned port key/cookie when the desired target is a portgroup.
- Opaque network: compare opaque network ID and type.

If the logical backing is unchanged, retain the observed backing on an Edit for MAC/external-ID fields; recreating an unspecified port connection can unnecessarily rebind the adapter. When the backing changes, construct a fresh destination backing and do not copy the old DV port key/cookie or standard-network MoRef into it. The server supplies DV connection cookies and may populate the bound port. [DV port connection API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.dvs.PortConnection.html)

## Construct the Edit

Start from the observed concrete card with `vimtypes.DeepCopy[vimtypes.BaseVirtualDevice]`, handle its error with interface/key context, and assert Ethernet-card support. The pinned library preserves concrete subtype through its typed copier. Do not shallow-copy embedded structs, mutate `MoVM.Config.Hardware.Device`, or create a default VMXNet3 as an edit template.

| Field group | Edit behavior |
|---|---|
| Concrete type, key, controller key, unit number, PCI slot information | Preserve. Renumbering/type conversion is not an attachment Edit. |
| NUMA, VMXNet3/UPT fields, resource allocation, wake-on-LAN and other non-owned fields | Preserve unless that field's owning reconciler explicitly contributes a change. Unsupported subtypes require their own validated edit path; generic Ethernet inheritance does not establish SR-IOV safety. |
| Backing | Preserve when semantically unchanged; otherwise replace with a complete fresh target backing. |
| Address type and MAC | Apply the policy table below as a pair, without copying an observed MAC over a pinned desired one. |
| External ID | Keep/Set/Clear according to resolved provider intent, including authoritative clearing on provider migration. |
| Subnet ID | Do not transplant an old subnet ID onto a new attachment. Validate the required unset/reset behavior and subsequent platform population. The current `SubnetId = ""` assignment alone does not prove a clear was sent. |
| Connectable | Clone and apply the connection policy below. Allocate it explicitly if missing rather than dereferencing nil or defaulting all fields to false. |

Emit one `VirtualDeviceConfigSpec{Operation: Edit, Device: editedConcreteCard}` with no disk `FileOperation`. The API expects the necessary full device configuration, so a hand-built base Ethernet card containing only a key and new backing is not the payload contract. [Device specification API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualDeviceSpec.html)

Do not use `reflect.DeepEqual` over observed versus desired Ethernet cards as the convergence test. Compare owned fields with explicit presence and normalization, including case-insensitive MACs, ignored server fields, and defined nil/default connection behavior. The second reconcile must be a no-op even when vSphere populates `DeviceInfo`, port binding, MAC, or subnet data.

## MAC transitions

| Destination policy | Observed policy | Required behavior |
|---|---|---|
| Pinned | Any | Write the destination's exact normalized MAC with manual addressing and its coherent backing/external ID. Never adopt the old card's MAC first. |
| vSphere-managed | Generated or Assigned | Preserve the current valid address and its actual address type where accepted. Do not change Assigned to Generated just because the provider returned no MAC. Read back the result because the server can replace invalid/duplicate managed addresses. |
| vSphere-managed | Manual | Do not silently retain a former provider's pinned address as generated. Use a validated reset/generation payload; if unsupported live, defer to the supported powered-off operation or the explicit replacement fallback. |
| Pinned provider with missing/zero MAC | Any | ProviderPending; do not reinterpret missing required data as permission to generate. |

For a new Add with a managed MAC, omit a desired MAC and let vSphere allocate it. For a hardware Replace, do not carry the retired generated address or key into the new card. Returning to the same unit may reuse a device key; always resolve the post-task MAC again. The API permits Generated/Assigned addresses to be overridden by the server, so MAC retention is a validated preference, not an unconditional promise. [Ethernet card API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualEthernetCard.html)

## Empty string is not an explicit clear in the pinned SDK

The pinned govmomi fields `ExternalId` and `SubnetId` have `xml:",omitempty"` tags. A local serialization experiment on 2026-10-18 confirmed that assigning `""` removes both elements from the request. The public API explicitly specifies an empty `externalId` value for clearing; omission is not proof of that operation. The documented subnet property does not specify equivalent clearing semantics, so validate it separately. [Ethernet card API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.device.VirtualEthernetCard.html)

Carry field-presence intent to the final wire-building boundary. Before enabling a transition that needs clearing, implement and test a narrow request serializer or a suitable upstream SDK fix that emits the required empty element while preserving the original vSphere `xsi:type` and the full device configuration. Do not edit `external/` or local module-cache files, introduce a fake MAC/external ID sentinel, perform brittle string replacements on serialized SOAP, or set `xsi:nil` without a validated API contract. Wire tests must cover omission versus explicit empty for each supported concrete card and the complete `ReconfigVM_Task` request, followed by real-vCenter read-back. Until this is implemented and validated, such a transition is unsupported; mark it accordingly instead of claiming convergence.

## Disconnect, retry, and restore connection policy

There is no per-interface user connection-policy field in this checkout. Therefore “restore desired connection policy” must be concrete: preserve the known pre-replacement persistent policy and runtime intent for an existing card. A new card uses the established create-path defaults, made explicit and tested. Do not always reconnect a card that was intentionally disconnected before replacement.

Before temporarily disconnecting an existing card, persist the original `StartConnected`, `AllowGuestControl`, and applicable runtime-connected state with the attachment operation identity. The disconnect payload sets `Connected=false` when running, `StartConnected=false`, and `AllowGuestControl=false`; validation against supported platforms remains Q2. The record and disconnection must survive restart together. Subsequent pending passes must not overwrite the saved policy with the already-disconnected values. A ready replacement restores the saved policy in the same Edit that installs the new attachment; an already-ready replacement need not have a separate disconnect/reconnect task.

If disconnection is unsupported or denied, do not claim the adapter is disconnected. Report its actual connection state and `DisconnectFailed` or `PowerOffRequired`, retain resources, and allow the user's shutdown. A new provider read error alone never authorizes disconnection. When a pending replacement is cancelled by restoring the old spec, re-resolve the old resource and restore its saved policy only when still valid; never reconnect blindly to an obsolete allocation.

Runtime reconnection is part of the handoff, not a new perpetual enforcement loop. Once the handoff completes and guest control is restored, a later guest disconnect must not cause continuous reconnect edits. Hardware readiness evaluates the applied attachment and operator-owned persistent policy; the separate observed `hardware.connected` field describes current link state. Tests must distinguish an operator-imposed pending disconnect from a guest-controlled disconnect after successful handoff.

## Compose and submit once

Use one per-device change accumulator shared by attachment and NIC-tuning contributions, keyed by observed key for existing hardware and interface identity for Adds. Each mutable device is copied at most once. Contributors apply only owned fields; an incompatible collision is an explicit error. Fold initial tuning into an Add rather than emitting an Edit for its temporary key. Reject Edit+Remove for the same current device. SR-IOV and unsupported tuning fields are not coerced into generic card types.

Perform final semantic no-op detection before adding the changeVersion precondition. Necessary operation-record persistence or cleanup counts as a real change; unchanged bookkeeping does not. Only for a nonempty reconfigure set `ConfigSpec.ChangeVersion` to the config snapshot used to build the edits. Otherwise a lone changeVersion could turn every steady-state pass into a reconfigure. On version conflict, refresh and replan, preserving external changes. A Kubernetes generation change or provider UID/version change invalidates the relevant intent snapshot; recheck before destructive provider cleanup. This guards observations but does not pretend Kubernetes and vCenter share a transaction. [ConfigSpec API](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.vm.ConfigSpec.html)

Keep NIC work in the existing shared config task; do not introduce a second independent reconfigure that races disks, crypto, or tuning. Remove/add fallbacks are emitted removes-first and remain in one dependent group with ordinary fail behavior, not a best-effort “skip failed device” mode. `ReconfigVM_Task` is documented as atomic for the submitted config. A client timeout is nevertheless an unknown outcome and requires observation before retry or cleanup. Provider allocation and deletion are separate operations outside that atomic boundary. [ReconfigVM_Task](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.VirtualMachine.html#reconfigure)

Preflight known unsupported NIC operations so independent configuration can proceed. A definitive, attributable NIC fault can be reported and the unchanged failing NIC group deferred on a later observed pass, allowing independent work; retry that group when its relevant inputs/support change or according to the error's retry policy. Do not blindly resubmit an atomic batch that keeps failing on one NIC and starves unrelated updates. Unknown task outcome, generic faults, and busy state do not permit speculative partial retries.

When the VM is on and the user requests off, defer newly planned live NIC work to the powered-off pass so repeated network reconfigures cannot postpone shutdown. Existing in-flight task guards still apply. Keep the provider's phase order unchanged.

## Observe and retire the old allocation

On the next fresh hardware observation, verify the complete owned tuple and restored connection policy. Do not mark Ready, delete the old CR, or release stale-guest invalidation just because a task succeeded. Changed generated addresses come from read-back.

Provider allocation references are not identical to network references. Two NetOP CRs can use the same DV portgroup; treating any card on that portgroup as a reference to every CR would retain old CRs forever. Conversely, absence of a matching portgroup is not sufficient evidence to delete an NSX allocation with a still-applied external ID. Use provider-specific attachment identity and a validated applied-allocation handoff record where hardware cannot encode CR identity. Q8 must define its persistence and release rules, including IPAM use when an allocation changes without an L2 hardware change.

Test whether old/new allocations may coexist. VPC ports use a logical attachment reference based on VM/interface name; a provider may impose constraints even when CR names differ. If a provider requires release-before-allocation, the retain-old-until-new-ready algorithm cannot complete by itself. Do not automatically delete the old allocation to break that dependency; support a separately validated handoff or report `ProviderHandoffBlocked` with an operator action. Retain UID delete preconditions and do not clean up unrelated VMs' resources.

## Required replacement tests

Cover each backing family and provider transition; same-CR/new-attachment and new-CR/same-attachment; mixed numbered/un-numbered claims; pinned/Generated/Assigned MAC cases; explicit field clearing on the wire; preservation of E1000/E1000e/VMXNet3 type and tuning; unsupported SR-IOV; no-op after server normalization; missing Connectable; pre-disconnected cards; cancellation A→B→A; superseding A→B→C; runtime policy restoration after restart; version conflict; combined attachment/tuning edits; failed/uncertain atomic tasks; same-network resource retirement; and providers that prevent overlapping allocations. Wire tests and simulator tests complement real-platform results; neither substitutes for them.
