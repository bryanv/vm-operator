# Tasks: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **Plan**: [`plan.md`](./plan.md)
- **Model**: [`model.md`](./model.md)
- **Epic**: TBD <!-- mirrors the spec header; file the epic and its stories before shipping code -->

> `[vmop-NNN]` tags are placeholders until the epic and its stories/sub-tasks are filed.
> Every shipping-code task MUST carry a real tag linked to the epic via `customfield_10830`
> before merge.

## Phase 1 — Setup (API + gate)

- [ ] T001 Add `Table *int64` to `VirtualMachineNetworkRouteSpec`, add `VirtualMachineNetworkRoutingPolicySpec`, and add `RoutingPolicies []VirtualMachineNetworkRoutingPolicySpec` to `VirtualMachineNetworkInterfaceSpec` with kubebuilder markers (`api/v1alpha6/virtualmachine_network_types.go`).
- [ ] T002 Regenerate deepcopy, CRD manifests, and spoke conversions: `make generate-go`, `make generate-manifests`, `make generate-go-conversions` (`api/v1alpha6/zz_generated.deepcopy.go`, `config/crd/...`, `api/v1alpha{1..5}/zz_generated.conversion.go`).
- [ ] T003 [vmop-NNN] Conversion: add manual `Convert_v1alpha6_VirtualMachineNetworkRouteSpec_To_v1alphaN_VirtualMachineNetworkRouteSpec` in each spoke whose generated route-spec conversion breaks; extend `restore_v1alpha6_VirtualMachineNetworkInterfaces` in each spoke to restore `RoutingPolicies` (whole-field) and per-route `Table` (match routes by `(to, via)`), per `model.md` § Conversion (`api/v1alpha{1..5}/virtualmachine_conversion.go`).
- [ ] T004 [P] [vmop-NNN] Conversion round-trip tests: hub-spoke-hub with `routingPolicies` and route `table` populated, per spoke version (`api/test/v1alpha{1..5}/virtualmachine_conversion_test.go`).
- [ ] T005 [vmop-NNN] Feature gate: add `VMRoutingPolicies` to `FeatureStates` (`pkg/config/config.go`); add `CapabilityKeyVMRoutingPolicies = "supports_vm_service_routing_policies"` and its case in `updateCapabilitiesFeaturesFromCRD` (`pkg/config/capabilities/capabilities.go`); unit test that the capability toggles the feature (`pkg/config/capabilities/capabilities_test.go`).

## Phase 2 — Foundational (provider plumbing)

- [ ] T006 [vmop-NNN] Add `Table *int64` to `NetworkInterfaceRoute`; add `NetworkInterfaceRoutingPolicy{From, To string; Table int64; Priority *int64}`; add `RoutingPolicies []NetworkInterfaceRoutingPolicy` to `NetworkInterfaceResult` (`pkg/providers/vsphere/network/network.go`).
- [ ] T007 [vmop-NNN] Copy route `Table` and interface `RoutingPolicies` from the interface spec into the bootstrap struct and `NetworkInterfaceResult` (`pkg/providers/vsphere/network/bootstrap.go`).

## Phase 3 — US1: Declare routing policy and route tables (DevOps user)

- [ ] T008 [US1] [vmop-NNN] Emit route `table` and a `routing-policy` block (`from`/`to`/`table`/`priority`) per ethernet in `NetPlanCustomization` (`pkg/providers/vsphere/network/netplan.go`).
- [ ] T009 [P] [US1] [vmop-NNN] Unit tests: route `table` and `routing-policy` translation, including empty/unset cases (`pkg/providers/vsphere/network/netplan_test.go`).
- [ ] T010 [P] [US1] [vmop-NNN] Unit tests: bootstrap plumbing copies `Table` and `RoutingPolicies` (`pkg/providers/vsphere/network/bootstrap_test.go`).

## Phase 4 — US2: Validation and gating (DevOps user / unsupported config)

- [ ] T011 [US2] [vmop-NNN] Add `validateNetworkInterfaceRoutingPolicies`: at-least-one-of `from`/`to`, `from`/`to` parse as IP or CIDR, single-family per rule; wire into `validateNetworkInterfaceSpec` (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
- [ ] T012 [US2] [vmop-NNN] Add the CloudInit-only + `VMRoutingPolicies` gate for `routingPolicies` and any route `table` in `validateNetworkSpecWithBootStrap`; routes are already CloudInit-only, so route `table` needs only the capability check (`webhooks/virtualmachine/validation/virtualmachine_validator.go`).
- [ ] T013 [P] [US2] [vmop-NNN] Webhook unit tests: at-least-one-of, mixed-family, malformed addr, out-of-range table, feature-disabled on create, feature-disabled on update (fields present in the updated spec while the capability is deactivated), non-CloudInit, happy path (`webhooks/virtualmachine/validation/virtualmachine_validator_unit_test.go`).

## Phase 5 — E2E

- [ ] T014 [US1] [vmop-NNN] CloudInit E2E in the existing networking suite: deploy a VM with **two interfaces on the same network** (no new subnet); the second interface has a route in `table N` and a routing-policy rule matching its own address to `table N`; assert `ip rule show` and `ip route show table N` via guest exec; assert per-interface DNS still lands; gate the spec on the `supports_vm_service_routing_policies` capability being activated on the testbed (`test/e2e/vmservice/vmservice/virtualmachine/vm_networking.go`).

## Phase Final — Polish

- [ ] T015 [vmop-NNN] Update generated API reference docs and add the release note (`docs/`, PR description).
- [ ] T016 Flip `spec.md` status `Draft` → `Implemented`, resolve open questions, replace `Epic: TBD` and `[vmop-NNN]` placeholders with real tickets.
