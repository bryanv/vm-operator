# Tasks: Netplan DHCP Overrides — `use-routes`

Decomposes [`plan.md`](./plan.md). All tasks target epic `TBD`
[NEEDS CLARIFICATION: epic ticket] — tag with the real story/sub-task ticket
once filed.

## Phase 1 — Foundational (shared plumbing, blocks every user story)

- `[T001]` `[US1]` Add `NetworkInterfaceDHCPOverrides` (today: `UseRoutes
  *bool`) and `DHCP4Overrides`/`DHCP6Overrides` fields on
  `NetworkInterfaceResult` (`pkg/providers/vsphere/network/network.go`).
- `[T002]` `[US1]` Add `type DHCPOverrides = schema.DHCPOverrides` alias
  (`pkg/util/netplan/netplan.go`).

## Phase 2 — US1: DHCP addressing without a DHCP default route

- `[T003]` `[US1]` `[P]` Loosen the `Gateway4`/`Gateway6` mutual-exclusivity
  check in `validateNetworkInterfaceSpec` to allow the literal `"None"`
  while `DHCP4`/`DHCP6` is `true`, leaving the existing rejection message
  for real addresses unchanged
  (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
- `[T004]` `[US1]` Add `DHCP4Overrides`/`DHCP6Overrides` fields to
  `Bootstrap`; populate `UseRoutes = false` from `bootstrap.DHCP4` (resolved)
  + `interfaceSpec.Gateway4 == "None"` (symmetric for v6); propagate through
  `devAndBootstrapToNetworkInterfaceResult`
  (`pkg/providers/vsphere/network/bootstrap.go`). Depends on `[T001]`.
- `[T005]` `[US1]` `NetPlanCustomization`: convert
  `NetworkInterfaceDHCPOverrides` to `*netplan.DHCPOverrides`, set
  `npEth.Dhcp4Overrides`/`Dhcp6Overrides`
  (`pkg/providers/vsphere/network/netplan.go`). Depends on `[T001]`, `[T002]`.
- `[T006]` `[US1]` `[P]` Update `Gateway4`/`Gateway6`/`DHCP4`/`DHCP6` godoc
  (`api/v1alpha6/virtualmachine_network_types.go`); regenerate
  `config/crd/bases/` via `make generate-manifests`.
- `[T007]` `[US1]` Unit tests: `InterfaceBootstrap` override population
  (`pkg/providers/vsphere/network/bootstrap_test.go`), `NetPlanCustomization`
  rendered output (`pkg/providers/vsphere/network/netplan_test.go`), webhook
  `Entry` for `gateway4: "None"` + `dhcp4: true` allowed
  (`webhooks/virtualmachine/validation/virtualmachine_validator_unit_test.go`).
  Depends on `[T003]`–`[T005]`.

## Phase 3 — US2: symmetric dual-stack opt-out + admission-time parity

- `[T008]` `[US2]` Add the dhcp4/dhcp6-overrides parity check to
  `validateNetworkInterfaceSpec`: when `DHCP4` and `DHCP6` are both
  explicitly `true`, require `(Gateway4 == "None") == (Gateway6 == "None")`,
  else `field.Invalid` naming both fields
  (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
  Depends on `[T003]`.
- `[T009]` `[US2]` Unit tests for the matching-allowed and
  mismatched-rejected cases
  (`webhooks/virtualmachine/validation/virtualmachine_validator_unit_test.go`).
  Depends on `[T008]`.

## Phase 4 — US3: provider-inferred DHCP defense-in-depth

- `[T010]` `[US3]` In `InterfaceBootstrap`, after computing
  `DHCP4Overrides`/`DHCP6Overrides` from the resolved state, re-check parity
  using `bootstrap.DHCP4 && bootstrap.DHCP6`; on mismatch, clear both
  override structs rather than render a config `netplan apply` would reject
  (`pkg/providers/vsphere/network/bootstrap.go`). Depends on `[T004]`.
- `[T011]` `[US3]` Unit test: provider resolves both DHCP4/DHCP6 active,
  spec sets only `Gateway4: "None"`; assert neither override is populated on
  the result (`pkg/providers/vsphere/network/bootstrap_test.go`). Depends on
  `[T010]`.

## Phase Final — Polish

- `[T012]` **Decision recorded**: E2E coverage for `gateway4/6: "None"` +
  `dhcp4/6: true` is scoped as a same-epic follow-up, not this PR. Existing
  network E2E coverage (`test/e2e/vmservice/vmservice/virtualmachine/`)
  checks `status.network.config` against provider info, not
  guest-observable routing state; asserting that a DHCP-leased interface's
  guest routing table lacks a DHCP-installed default route needs guest
  command execution this session's environment cannot exercise or verify.
  The unit-level coverage in Phase 2–4 (`bootstrap_test.go`,
  `netplan_test.go`) already pins the rendered netplan YAML byte-for-byte;
  the follow-up's job is solely the guest-side assertion. State this
  explicitly in the PR description per `e2e-sync-with-changes.md`.
- `[T013]` `[P]` Release note in the PR description covering the new
  `"None"` + DHCP behavior.
- `[T014]` File the epic ticket referenced by `spec.md`/`plan.md`/this file
  and replace every `TBD` with the real `vmop-NNN`.
