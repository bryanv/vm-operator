# Implementation Plan: Netplan DHCP Overrides — `use-routes`

Links back to [`spec.md`](./spec.md); investigation in [`research.md`](./research.md).

## Summary

Reinterpret the existing `Gateway4`/`Gateway6` `"None"` sentinel to also be
legal while `DHCP4`/`DHCP6` is `true`, and thread that intent through the
network-bootstrap pipeline into netplan's `dhcp4-overrides`/`dhcp6-overrides`
`use-routes` key. No new VM Spec field. No CRD schema shape change — only
godoc (CRD `description`) updates.

## Technical context

- Go module: root (`github.com/vmware-tanzu/vm-operator`).
- Target API version: `v1alpha6` (current `vmopv1`) godoc only; `Gateway4`/
  `Gateway6` already exist identically in `v1alpha2`..`v1alpha6`, so no
  conversion changes are needed.
- Affected packages: `webhooks/virtualmachine/validation`,
  `pkg/providers/vsphere/network`, `pkg/util/netplan`.
- Netplan schema (`pkg/util/netplan/schema/netplan.go`) is unchanged; it is
  quicktype-generated and already models `DHCPOverrides.UseRoutes *bool`.

## Constitution check

- **API compatibility**: additive-safe — no field added, removed, or
  retyped. Only doc comments change on an existing string field. ✅
- **Controllers stay thin / vSphere access through providers**: no
  controller changes; all new logic lives in `pkg/providers/vsphere/network`
  (business logic) and the validating webhook (admission). ✅
- **CEL vs Go validation**: the new checks are cross-field
  (`Gateway4`/`Gateway6` vs. `DHCP4`/`DHCP6`) and reuse the existing Go
  validator function (`validateNetworkInterfaceSpec`) rather than CEL,
  consistent with how the pre-existing mutual-exclusivity checks in that same
  function are implemented. ✅
- **E2E sync with changes**: this is cluster-observable guest-network
  behavior. See "Test strategy" below for the concrete decision.
- No constitutional rule needs to be bent; no complexity-tracking entry
  required.

## Project structure

New/modified files, by package:

```
api/v1alpha6/virtualmachine_network_types.go
  — godoc updates on Gateway4, Gateway6, DHCP4, DHCP6 (VirtualMachineNetworkInterfaceSpec)

config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml
  — regenerated via `make generate-manifests` (description text only)

webhooks/virtualmachine/validation/virtualmachine_validator.go
  — validateNetworkInterfaceSpec: allow "None" as an exception to the
    gateway/dhcp mutual-exclusivity check; add the dhcp4/dhcp6-overrides
    parity check for the explicit-both-true case.
webhooks/virtualmachine/validation/virtualmachine_validator_unit_test.go
  — new Entry cases for the above.

pkg/providers/vsphere/network/network.go
  — new NetworkInterfaceDHCPOverrides struct (today: UseRoutes *bool only);
    DHCP4Overrides/DHCP6Overrides fields on NetworkInterfaceResult.
pkg/providers/vsphere/network/bootstrap.go
  — same two fields on Bootstrap; populate from Gateway4/6=="None" + the
    resolved DHCP4/DHCP6; parity guard using resolved state; propagate
    through devAndBootstrapToNetworkInterfaceResult.
pkg/providers/vsphere/network/bootstrap_test.go
  — new Context("DHCP Overrides", ...) cases, including the provider-inferred
    mismatch case (US3).

pkg/util/netplan/netplan.go
  — add `type DHCPOverrides = schema.DHCPOverrides` alias.
pkg/providers/vsphere/network/netplan.go
  — NetPlanCustomization: convert NetworkInterfaceDHCPOverrides to
    *netplan.DHCPOverrides, assign to npEth.Dhcp4Overrides/Dhcp6Overrides.
pkg/providers/vsphere/network/netplan_test.go
  — new Context asserting the rendered Ethernets carry the expected
    dhcp4-overrides/dhcp6-overrides.
```

No changes to `pkg/providers/vsphere/network/gosc.go` (GOSC path has no
netplan/`dhcp-overrides` equivalent — out of scope per spec).

## API / CRD strategy

Additive-safe doc-only change on an existing field; no version bump, no
conversion webhook changes. `Gateway4`/`Gateway6` docs gain:

1. The `"None"` sentinel is now also legal while the paired `DHCP4`/`DHCP6`
   is `true`, and what it does in that case (disables netplan's
   `use-routes` for that DHCP family — DHCP-provided routes, including the
   default gateway, are not installed).
2. This is honored only for the CloudInit bootstrap provider (matching
   `MTU`/`Routes`/`SearchDomains` phrasing already in the file).
3. The dual-stack parity constraint (both `"None"` or neither, when both
   `DHCP4` and `DHCP6` are `true`), so the constraint is discoverable from
   `kubectl explain` / generated API docs, not only from webhook error text.

`DHCP4`/`DHCP6` docs are loosened from an unqualified "mutually exclusive
with the Gateway4/6 field" to carve out the `"None"` exception.

## Controller / webhook impact

- No controller changes.
- Webhook: `validateNetworkInterfaceSpec` in
  `webhooks/virtualmachine/validation/virtualmachine_validator.go` gets:
  - The existing `gw := interfaceSpec.Gateway4; gw != ""` mutual-exclusivity
    check narrowed to `gw != "" && gw != "None"` (symmetric for `Gateway6`).
    The pre-existing error message text for the still-rejected case (a real
    address alongside `dhcp4: true`) is left byte-for-byte unchanged, so the
    existing unit test at `virtualmachine_validator_unit_test.go:2551` keeps
    passing untouched.
  - A new check, only when `DHCP4` and `DHCP6` are both explicitly `true`:
    `(Gateway4 == "None") != (Gateway6 == "None")` is a `field.Invalid`
    against the interface path, mirroring the existing
    `field.Invalid(p.Index(i), "", "cannot mix IP address families")`
    pattern used for route validation in the same function.
- No new RBAC, no new feature flag — this ships unconditionally, matching
  the un-flagged `Gateway4`/`Gateway6` behavior it extends.
- Provider (`pkg/providers/vsphere/network`): `InterfaceBootstrap` computes
  the per-family override from the **resolved** `bootstrap.DHCP4`/`DHCP6`
  (spec-explicit or provider-inferred), not from the spec's raw `DHCP4`/
  `DHCP6` pointers, then re-checks parity using that same resolved state
  before returning — this is the defense-in-depth layer for US3, since the
  webhook cannot see provider-inferred DHCP at admission time.

## Test strategy

- **Unit** (`testlabels.Controller` where applicable, no infra label needed):
  - `webhooks/virtualmachine/validation/virtualmachine_validator_unit_test.go`:
    new `Entry`s for (a) `gateway4: "None"` + `dhcp4: true` allowed, (b)
    matching `gateway4`/`gateway6` `"None"` with both DHCP true allowed, (c)
    mismatched combination rejected with the new message.
  - `pkg/providers/vsphere/network/bootstrap_test.go`: `InterfaceBootstrap`
    cases for override population (explicit spec-driven) and the
    provider-inferred mismatch/drop case (US3).
  - `pkg/providers/vsphere/network/netplan_test.go`: `NetPlanCustomization`
    renders `Dhcp4Overrides`/`Dhcp6Overrides` correctly from
    `NetworkInterfaceResult`.
- **Integration**: none added — no controller or envtest-observable
  reconcile-loop behavior changes; this is pure data transformation from
  spec → bootstrap → netplan config, already covered by the unit layer
  above at each stage.
- **E2E**: this is guest-observable behavior once a real VM boots CloudInit
  with the rendered netplan config, but asserting it needs guest command
  execution (reading the guest's routing table / `netplan get`), which the
  existing network E2E coverage under
  `test/e2e/vmservice/vmservice/virtualmachine/` does not yet do — that
  coverage checks `status.network.config` against provider info, not
  guest-observed state. Per `e2e-sync-with-changes.md`, this PR explicitly
  scopes that guest-side E2E assertion as a same-epic follow-up rather than
  leaving it unstated; see `tasks.md` Phase Final (`T012`) for the written
  decision and rationale.

## Rollout / migration

- No feature flag; ships as a direct extension of existing, unflagged
  `Gateway4`/`Gateway6` behavior.
- No backfill/schema-upgrade impact — existing VMs with `Gateway4`/`Gateway6`
  unset or set to concrete addresses are unaffected; only new/updated specs
  using the `"None"` + DHCP combination opt in.
- Release note: call out that `gateway4`/`gateway6: "None"` is now honored
  alongside `dhcp4`/`dhcp6: true` to suppress DHCP-installed routes.
