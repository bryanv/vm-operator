# Feature Specification: Netplan DHCP Overrides — `use-routes`

- **Feature branch**: `bryanv/netplan-dhcp-overrides`
- **Created**: 2026-08-10
- **Status**: Draft
- **Epic**: TBD [NEEDS CLARIFICATION: file the epic ticket before this spec merges]

---

## Summary

Netplan's `dhcp4-overrides`/`dhcp6-overrides` let a rendered interface keep
DHCP-assigned addressing while opting out of specific DHCP-driven behaviors.
This feature exposes exactly one of those overrides — `use-routes` — by
reinterpreting the existing `Gateway4`/`Gateway6` `"None"` sentinel so it is
also honored while `DHCP4`/`DHCP6` is active: a DevOps user who sets
`Gateway4: "None"` on an interface with `DHCP4: true` gets an address from
DHCP but no DHCP-installed default route (or other DHCP-supplied routes),
leaving them free to supply their own via `spec.network.interfaces[].routes`.
No new field is added to the VM Spec. See [`research.md`](./research.md) for
why the other `dhcp-overrides` keys are out of scope for this change.

## Goals

- `Gateway4: "None"` on an interface with `DHCP4: true` MUST result in the
  guest's rendered netplan config carrying `dhcp4-overrides: {use-routes:
  false}` for that interface, and MUST NOT change any other DHCP4 behavior
  (address assignment, DNS, MTU, etc.).
- `Gateway6: "None"` with `DHCP6: true` MUST behave symmetrically for
  `dhcp6-overrides`.
- The validating webhook MUST continue to reject any concrete IPv4/IPv6
  address in `Gateway4`/`Gateway6` while `DHCP4`/`DHCP6` is `true`
  (unchanged from today) but MUST allow the literal value `"None"` in that
  combination.
- When an interface's `DHCP4` and `DHCP6` are both explicitly `true` in the
  request, the webhook MUST reject the request unless `Gateway4 == "None"`
  and `Gateway6 == "None"` agree (both `"None"` or both not), because
  netplan's `networkd` backend requires `dhcp4-overrides` and
  `dhcp6-overrides` to carry identical keys/values whenever both families
  are DHCP-enabled, and a mismatch here is otherwise silently rejected by
  the guest's `netplan apply`, not by VM Operator.
- Because a network provider MAY enable DHCP for a family the DevOps user
  never set explicitly in `DHCP4`/`DHCP6` (left `nil`), the same
  dhcp4/dhcp6-overrides parity constraint MUST also be enforced using the
  fully-resolved, post-provider DHCP state at netplan-generation time, not
  only at admission time. On a mismatch discovered at that point, VM
  Operator MUST render neither `dhcp4-overrides` nor `dhcp6-overrides`
  for that interface rather than emit netplan config the guest would refuse
  to apply.
- This behavior is CloudInit/netplan-only, matching every other
  interface-level networking knob that isn't universally honored (`MTU`,
  `Routes`, `SearchDomains`); it has no effect on the GOSC (Sysprep/LinuxPrep)
  customization path.
- The internal representation of "which DHCP overrides apply to this
  interface" MUST be structured so that adding support for another
  `dhcp-overrides` key (see `research.md`'s ranked follow-up list) is
  additive: a new field on the internal overrides type, population logic
  for it, and no changes to the already-complete generated netplan schema.

## Non-goals

- No new field is added to `VirtualMachineNetworkInterfaceSpec` or any other
  VM Spec type.
- No other `dhcp-overrides` key (`use-mtu`, `use-dns`, `use-domains`,
  `route-metric`, `send-hostname`, `use-hostname`, `hostname`) is
  implemented by this change. See `research.md` for the deferral rationale
  and ranked follow-up order.
- No behavior changes to the GOSC (Sysprep/LinuxPrep) customization path.
- No changes to the vendored netplan schema
  (`pkg/util/netplan/schema/netplan.go`); it already models the full
  `DHCPOverrides` type.
- No API version bump; `Gateway4`/`Gateway6` already exist verbatim across
  `v1alpha2`..`v1alpha6`, and only the current hub version's (`v1alpha6`)
  godoc changes, since the field's Go type/JSON shape is unchanged.

## User stories / acceptance criteria

### US1 — DevOps user: DHCP addressing without a DHCP default route (Priority: P0)

A DevOps user wants an interface to get its address via DHCPv4, but wants to
supply its own routing (e.g., a non-default-gateway topology) instead of
whatever the DHCP server hands out.

**Acceptance criteria:**
- Setting `spec.network.interfaces[].dhcp4: true` and `.gateway4: "None"` is
  accepted by the validating webhook.
- The VM's rendered netplan config (visible via the CloudInit
  network-config data, or observable end-to-end via guest network state in
  an E2E run) shows `dhcp4-overrides: {use-routes: false}` for that
  interface, `dhcp4: true`, and no `gateway4` entry.
- Any static `routes` the user also configured on the interface are still
  present and installed inside the guest.

**Independent test**: create a VM with one interface, `dhcp4: true`,
`gateway4: "None"`, and one static route; observe the guest has a DHCP-leased
address, no default route from DHCP, and the static route installed.

### US2 — DevOps user: symmetric dual-stack opt-out (Priority: P1)

A DevOps user wants the same `use-routes: false` behavior for both address
families on a dual-stack interface.

**Acceptance criteria:**
- `dhcp4: true, dhcp6: true, gateway4: "None", gateway6: "None"` is accepted
  and renders matching `dhcp4-overrides`/`dhcp6-overrides` blocks.
- `dhcp4: true, dhcp6: true, gateway4: "None", gateway6: ""` (or any
  mismatched combination) is **rejected** by the webhook with a clear
  `field.Invalid` error naming both `gateway4` and `gateway6`.

**Independent test**: submit both the matching and mismatched interface
specs above; confirm the first is admitted and the second is rejected before
reaching a controller.

### US3 — Platform engineer: provider-inferred DHCP doesn't silently break netplan apply (Priority: P1)

A network provider (not the DevOps user) decides to serve DHCP for a family
the user left unset in the spec, on a VM where the user also set
`gateway4`/`gateway6` to `"None"` for whichever family they did configure.

**Acceptance criteria:**
- The rendered netplan config for that interface never contains
  `dhcp4-overrides`/`dhcp6-overrides` that mismatch when both families are
  effectively DHCP-enabled; VM Operator drops both overrides for that
  interface rather than render a config `netplan apply` would reject.

**Independent test**: unit-test `InterfaceBootstrap` with an `initial`
Bootstrap where the provider has both DHCP4 and DHCP6 active, and
`interfaceSpec.Gateway4 == "None"` but `Gateway6 == ""`; assert neither
`DHCP4Overrides` nor `DHCP6Overrides` is populated on the result.

## Open questions

- [NEEDS CLARIFICATION: epic ticket] — no `vmop-NNN` epic exists yet for this
  work; file one before merging and update the header above.
