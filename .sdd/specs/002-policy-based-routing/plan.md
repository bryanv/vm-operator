# Implementation Plan: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD <!-- mirrors the spec header -->
- **Date**: 2026-07-06

## Summary

Add per-interface routing-policy rules and a per-route routing table to the `v1alpha6` VM network API, validate them in the VM admission webhook, and translate them into Netplan for CloudInit-bootstrapped guests. Per-interface DNS is already supported and is out of scope except for an E2E guard.

## Technical context

- **Go version**: repo default (see root `go.mod`).
- **API version(s) touched**: `v1alpha6` (only).
- **Modules touched**: root module (`api/`, `webhooks/`, `pkg/providers/vsphere/network/`, `test/e2e/`). No sub-module changes. `pkg/util/netplan` already supports the required schema — untouched.
- **New dependencies**: none.
- **Feature gate**: existing `pkgcfg.Features.PerNamespaceNetworkProvider` (no new flag).

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility | OK | Additive to active alpha `v1alpha6`; no removal/rename/retype; no conversion webhook. |
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
config/crd/...                           # regenerated manifests
webhooks/virtualmachine/validation/
  virtualmachine_validator.go            # routing-policy + route-table validation + gating
  virtualmachine_validation_test.go      # (existing test file) new cases
pkg/providers/vsphere/network/
  network.go                             # +Table on NetworkInterfaceRoute; +NetworkInterfaceRoutingPolicy; +RoutingPolicies
  bootstrap.go                           # copy Table + routing policies into results
  netplan.go                             # emit route table + routing-policy block
  netplan_test.go / bootstrap_test.go    # unit coverage
test/e2e/vmservice/virtualmachine/       # CloudInit PBR E2E
```

## API / CRD strategy

- Additive only; see [`model.md`](./model.md) for the field-by-field schema.
- Regenerate with `make generate-go` (deepcopy) and `make generate-manifests` (checked-in CRDs).
- Range validation via `+kubebuilder:validation:Minimum/Maximum`; semantic validation in the Go webhook.

## Controller / webhook impact

- **Controllers**: none.
- **Webhook** (`webhooks/virtualmachine/validation`): extend `validateNetworkInterfaceSpec` to validate route `Table` (marker-only) and call a new `validateNetworkInterfaceRoutingPolicies`; add the CloudInit + `PerNamespaceNetworkProvider` gate in `validateNetworkSpecWithBootStrap` (mirroring how routes are already gated and how `WorkloadIPv6` / `TelcoVMServiceAPI` use `field.Forbidden` + `featureNotEnabled`).
- **Provider** (`pkg/providers/vsphere/network`): plumb the new fields from spec → `NetworkInterfaceResult` → Netplan. The GOSC path (`gosc.go`, non-CloudInit) is unaffected and cannot express these; the webhook prevents them there.
- **RBAC**: unchanged.

## Test strategy

- **Unit — webhook** (`testlabels.Validation`): at-least-one-of `from`/`to`, mixed-family rejection, malformed addr, out-of-range table, feature-disabled rejection, non-CloudInit rejection, happy path.
- **Unit — provider** (`netplan_test.go`, `bootstrap_test.go`): route `table` emitted; `routing-policy` block emitted with `from`/`to`/`table`/`priority`; empty when unset.
- **Integration**: covered by existing network suite where applicable; no new envtest surface.
- **E2E (mandatory — cluster-observable)**: CloudInit VM with a route in `table N` and a routing-policy matching a source subnet to `table N`; guest-exec asserts `ip rule show` and `ip route show table N`; also assert per-interface DNS still lands (guard for the already-shipped behavior). Under `test/e2e/vmservice/virtualmachine/`.

## Rollout / migration

- **Feature flag**: reuse `pkgcfg.Features.PerNamespaceNetworkProvider` (no new flag, no new default to manage).
- **Schema upgrade / backfill**: none — purely additive optional fields.
- **Partner comms**: API doc update in the generated reference; release note describing routing-policy + route table.

## Complexity tracking

_None._
