# Implementation Plan: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD <!-- mirrors the spec header -->
- **Date**: 2026-07-06

## Summary

Add per-interface routing-policy rules and a per-route routing table to the `v1alpha6` VM network API, validate them in the VM admission webhook, and translate them into Netplan for CloudInit-bootstrapped guests. Per-interface DNS is already supported and is out of scope except for an E2E guard.

## Technical context

- **Go version**: repo default (see root `go.mod`).
- **API version(s) touched**: `v1alpha6` gains the fields; `v1alpha1`–`v1alpha5` need conversion updates (drop + annotation restore) since the changed types round-trip through every spoke.
- **Modules touched**: root module (`api/`, `webhooks/`, `pkg/providers/vsphere/network/`, `pkg/config/`, `test/e2e/`) and the `api/test` module (conversion round-trip tests). `pkg/util/netplan` already supports the required schema — untouched.
- **New dependencies**: none.
- **Feature gate**: new VM Service capability `supports_vm_service_routing_policies` mapped to a new `pkgcfg.Features.VMRoutingPolicies`, following the `supports_vm_service_vlan_subinterface` / `VMVlanSubinterface` pattern (`pkg/config/config.go`, `pkg/config/capabilities/capabilities.go`). Capability-driven only; no FSS env var.

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility | OK | Additive to active alpha `v1alpha6`; no removal/rename/retype. The conversion webhook already exists; the new fields are dropped on down-conversion and preserved via the `MarshalData` annotation so an older-version client's read-modify-write does not lose them (this is the repo's mitigation for the UPDATE hazard the constitution calls out via kubernetes#111703). |
| Thin controllers | OK | No controller change; logic lives in the provider (`pkg/providers/vsphere/network`) and webhook. |
| No direct vSphere calls in controllers | OK | N/A — provider layer only. |
| CEL vs Go validation | OK | Range via kubebuilder markers; cross-field (family, at-least-one-of, bootstrap gate) in Go validator, which is vSphere/feature-flag dependent. |
| One test file per package | OK | Extend existing `_test.go` in each touched package. |
| E2E ships with behavior | OK | New CloudInit E2E in the same change set (see Test strategy). |
| Import aliases / grouping | OK | Follow `.golangci.yml`; no new widely-imported package. |
| Markdown no hard-wrap | OK | Spec docs unwrapped. |

No constitutional rule needs bending; **Complexity tracking** is empty.

## Project structure

```
api/v1alpha6/
  virtualmachine_network_types.go        # +Table on route, +RoutingPolicySpec, +RoutingPolicies
  zz_generated.deepcopy.go               # regenerated
api/v1alpha{1..5}/
  virtualmachine_conversion.go           # manual RouteSpec Convert funcs; extend restore_v1alpha6_VirtualMachineNetworkInterfaces
  zz_generated.conversion.go             # regenerated (make generate-go-conversions)
api/test/v1alpha{1..5}/
  virtualmachine_conversion_test.go      # round-trip coverage for the new fields
config/crd/...                           # regenerated manifests
pkg/config/
  config.go                              # +Features.VMRoutingPolicies
  capabilities/capabilities.go           # +CapabilityKeyVMRoutingPolicies ("supports_vm_service_routing_policies") + CRD switch case
webhooks/virtualmachine/validation/
  virtualmachine_validator.go            # routing-policy + route-table validation + gating
  virtualmachine_validator_unit_test.go  # (existing test file) new cases
pkg/providers/vsphere/network/
  network.go                             # +Table on NetworkInterfaceRoute; +NetworkInterfaceRoutingPolicy; +RoutingPolicies
  bootstrap.go                           # copy Table + routing policies into results
  netplan.go                             # emit route table + routing-policy block
  netplan_test.go / bootstrap_test.go    # unit coverage
test/e2e/vmservice/vmservice/virtualmachine/
  vm_networking.go                       # CloudInit PBR E2E (extend existing networking suite)
```

## API / CRD strategy

- Additive only; see [`model.md`](./model.md) for the field-by-field schema.
- Regenerate with `make generate-go` (deepcopy), `make generate-manifests` (checked-in CRDs), and `make generate-go-conversions` (spoke `zz_generated.conversion.go`).
- Range validation via `+kubebuilder:validation:Minimum/Maximum`; semantic validation in the Go webhook.
- **Conversion (v1alpha1–v1alpha5)** — the repo convention for new hub fields, per [`model.md`](./model.md) § Conversion:
  - `VirtualMachineNetworkInterfaceSpec` already has manual `Convert_v1alpha6_..._To_v1alphaN_...` wrappers in every spoke, so `RoutingPolicies` only needs regeneration there. `VirtualMachineNetworkRouteSpec` is currently a purely generated conversion; adding `Table` requires a new manual `Convert_v1alpha6_VirtualMachineNetworkRouteSpec_To_v1alphaN_VirtualMachineNetworkRouteSpec` in each spoke that references it.
  - Extend `restore_v1alpha6_VirtualMachineNetworkInterfaces` in each spoke's `virtualmachine_conversion.go` to restore `RoutingPolicies` and per-route `Table` from the `MarshalData` annotation on up-conversion (matching interfaces by name, and routes by `(to, via)` within a matched interface).
  - Round-trip tests in `api/test/v1alphaN/virtualmachine_conversion_test.go` (hub-spoke-hub with the new fields populated).

## Controller / webhook impact

- **Controllers**: none.
- **Webhook** (`webhooks/virtualmachine/validation`): extend `validateNetworkInterfaceSpec` to validate route `Table` (marker-only) and call a new `validateNetworkInterfaceRoutingPolicies`; add the CloudInit + `VMRoutingPolicies` gate in `validateNetworkSpecWithBootStrap` (mirroring how routes are already gated and how `WorkloadIPv6` / `TelcoVMServiceAPI` use `field.Forbidden` + `featureNotEnabled`). Note routes are already CloudInit-only, so for route `table` the gate only adds the capability check; `routingPolicies` needs both checks. The gate runs on create **and** update (the shared `validateNetwork` path), matching the existing feature gates — deactivating the capability rejects subsequent spec updates of VMs that use the fields.
- **Feature gate plumbing** (`pkg/config`): add `VMRoutingPolicies` to `FeatureStates` and `CapabilityKeyVMRoutingPolicies = "supports_vm_service_routing_policies"` with its case in `updateCapabilitiesFeaturesFromCRD`. Capabilities-CRD-driven only (like other post-`SVAsyncUpgrade` capabilities); no ConfigMap or env-var path.
- **Provider** (`pkg/providers/vsphere/network`): plumb the new fields from spec → `NetworkInterfaceResult` → Netplan. The GOSC path (`gosc.go`, non-CloudInit) is unaffected and cannot express these; the webhook prevents them there.
- **RBAC**: unchanged.

## Test strategy

- **Unit — webhook** (`testlabels.Validation`): at-least-one-of `from`/`to`, mixed-family rejection, malformed addr, out-of-range table, feature-disabled rejection on create **and on update** (fields present in the updated spec while the capability is deactivated), non-CloudInit rejection, happy path.
- **Unit — conversion** (`api/test/v1alphaN`): hub-spoke-hub round-trip with `routingPolicies` and route `table` populated survives via the annotation restore, for each spoke version.
- **Unit — feature gate** (`pkg/config/capabilities`): `supports_vm_service_routing_policies` activation toggles `Features.VMRoutingPolicies`.
- **Unit — provider** (`netplan_test.go`, `bootstrap_test.go`): route `table` emitted; `routing-policy` block emitted with `from`/`to`/`table`/`priority`; empty when unset.
- **Integration**: covered by existing network suite where applicable; no new envtest surface.
- **E2E (mandatory — cluster-observable)**: extend the existing networking suite (`test/e2e/vmservice/vmservice/virtualmachine/vm_networking.go`) with a CloudInit VM that has **two interfaces on the same network** — no new subnet or network is provisioned. The second interface carries a route in `table N` and a routing-policy rule matching the interface's own address to `table N`; guest-exec asserts the rule appears in `ip rule show` and the route in `ip route show table N` (rule/table presence is observable regardless of whether the interfaces share a subnet). Also assert per-interface DNS still lands (guard for the already-shipped behavior). The spec is gated on the `supports_vm_service_routing_policies` capability being activated on the testbed.

## Rollout / migration

- **Feature flag**: new capability `supports_vm_service_routing_policies` → `pkgcfg.Features.VMRoutingPolicies`. Deactivated by default; activated per Supervisor via the capabilities CRD like the other VM Service capabilities. No FSS env var.
- **Schema upgrade / backfill**: none — purely additive optional fields.
- **Partner comms**: API doc update in the generated reference; release note describing routing-policy + route table.

## Complexity tracking

_None._
