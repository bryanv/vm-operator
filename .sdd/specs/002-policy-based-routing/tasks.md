# Tasks: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **Plan**: [`plan.md`](./plan.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD <!-- mirrors the spec header; file the epic and its stories before shipping code -->

> `[vmop-NNN]` tags are placeholders until the epic and its stories/sub-tasks are filed.
> Every shipping-code task MUST carry a real tag linked to the epic via `customfield_10830`
> before merge.

## Phase 1 — Setup

- [ ] T001 Add `Table *int64` to `VirtualMachineNetworkRouteSpec`, add `VirtualMachineNetworkRoutingPolicySpec`, and add `RoutingPolicies []VirtualMachineNetworkRoutingPolicySpec` to `VirtualMachineNetworkInterfaceSpec` with kubebuilder markers (`api/v1alpha6/virtualmachine_network_types.go`).
- [ ] T002 Regenerate deepcopy and CRD manifests: `make generate-go` and `make generate-manifests` (`api/v1alpha6/zz_generated.deepcopy.go`, `config/crd/...`).

## Phase 2 — Foundational (provider plumbing)

- [ ] T003 [vmop-NNN] Add `Table *int64` to `NetworkInterfaceRoute`; add `NetworkInterfaceRoutingPolicy{From, To string; Table int64; Priority *int64}`; add `RoutingPolicies []NetworkInterfaceRoutingPolicy` to `NetworkInterfaceResult` (`pkg/providers/vsphere/network/network.go`).
- [ ] T004 [vmop-NNN] Copy route `Table` and interface `RoutingPolicies` from the interface spec into the bootstrap struct and `NetworkInterfaceResult` (`pkg/providers/vsphere/network/bootstrap.go`).

## Phase 3 — US1: Declare routing policy and route tables (DevOps user)

- [ ] T005 [US1] [vmop-NNN] Emit route `table` and a `routing-policy` block (`from`/`to`/`table`/`priority`) per ethernet in `NetPlanCustomization` (`pkg/providers/vsphere/network/netplan.go`).
- [ ] T006 [P] [US1] [vmop-NNN] Unit tests: route `table` and `routing-policy` translation, including empty/unset cases (`pkg/providers/vsphere/network/netplan_test.go`).
- [ ] T007 [P] [US1] [vmop-NNN] Unit tests: bootstrap plumbing copies `Table` and `RoutingPolicies` (`pkg/providers/vsphere/network/bootstrap_test.go`).

## Phase 4 — US2: Validation and gating (DevOps user / unsupported config)

- [ ] T008 [US2] [vmop-NNN] Add `validateNetworkInterfaceRoutingPolicies`: at-least-one-of `from`/`to`, `from`/`to` parse as IP or CIDR, single-family per rule; wire into `validateNetworkInterfaceSpec` (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
- [ ] T009 [US2] [vmop-NNN] Add the CloudInit-only + `PerNamespaceNetworkProvider` gate for `routingPolicies` and any route `table` in `validateNetworkSpecWithBootStrap` (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
- [ ] T010 [P] [US2] [vmop-NNN] Webhook unit tests: at-least-one-of, mixed-family, malformed addr, out-of-range table, feature-disabled, non-CloudInit, happy path (`webhooks/virtualmachine/validation/virtualmachine_validation_test.go`).

## Phase 5 — E2E

- [ ] T011 [US1] [vmop-NNN] CloudInit E2E: deploy a VM with a route in `table N` and a routing-policy matching a source subnet to `table N`; assert `ip rule show` and `ip route show table N` via guest exec; assert per-interface DNS still lands (`test/e2e/vmservice/virtualmachine/`).

## Phase Final — Polish

- [ ] T012 [vmop-NNN] Update generated API reference docs and add the release note (`docs/`, PR description).
- [ ] T013 Flip `spec.md` status `Draft` → `Implemented`, resolve open questions, replace `Epic: TBD` and `[vmop-NNN]` placeholders with real tickets.
