# Research: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **Design docs**: WIKI page 2516224120 — "One-Pager: Per-Interface DNS and Policy-Based Routing for the Supervisor Cluster"

---

## Problem context

A multi-homed VM (two+ interfaces on two+ subnets) cannot route correctly with a single routing table: the kernel selects a route by destination only, so reply traffic can leave via the wrong interface (asymmetric routing), which stateful firewalls drop. Linux's answer is *policy-based routing*: multiple routing tables + `ip rule` entries that pick a table based on the packet's source (or other selectors). Netplan exposes this as per-interface `routing-policy` plus a `table` attribute on routes.

## Existing state in this repo

- **Per-interface DNS is already implemented.** `VirtualMachineNetworkInterfaceSpec.Nameservers` / `.SearchDomains` exist (`api/v1alpha6/virtualmachine_network_types.go`), the validator checks them, `pkg/providers/vsphere/network/bootstrap.go` resolves per-interface vs. global with the `UseGlobalNameserversAsDefault` fallback, and `pkg/providers/vsphere/network/netplan.go` emits a per-ethernet `nameservers` block. → No code change needed; guarded by E2E only.
- **Static routes** exist as `VirtualMachineNetworkRouteSpec{To, Via, Metric}` and are validated in `webhooks/virtualmachine/validation/virtualmachine_validator.go` (route loop with family-mixing check) and translated in `netplan.go`. There is **no** table or routing-policy support today.
- **The Netplan util layer already supports everything needed.** `pkg/util/netplan/schema/netplan.go`:
  - `RoutingConfig` (route) already has `From`, `Table`, `Scope`, `Type`, `OnLink`, `MTU`.
  - `RoutingPolicy` type exists: `{From, To, Table (required int64), Priority, Mark, TypeOfService}`, and every ethernet/vlan config carries a `routing-policy` list.
  - → **No change to `pkg/util/netplan`.** Only the VM API, validator, provider translation (`netplan.go`), and the result plumbing (`network.go` / `bootstrap.go`) change.

## Netplan behavior — what the webhook should (and should not) enforce

Authoritative source is the vendored schema at `pkg/util/netplan/schema/schema.json`:

- Route `table` (`schema.json:2125`): `type: integer`, `format: uint16`, `minimum: 0`.
- Routing-policy `table` (`schema.json:2192`): `type: integer`, `format: uint16`, `minimum: 0`; `table` is in the block's `required` list.
- `routing-policy.from` / `.to`: strings, `addr` or `addr/prefixlen`, IPv4 or IPv6.

Findings:

1. **Netplan does not reject reserved routing tables.** The schema accepts any `uint16` (0–65535), including 0 (unspec), 253 (default), 254 (main), 255 (local). The doc prose "positive integers starting from 1" is advisory, not enforced. Installing a route explicitly into `main` (254) is legitimate. → The webhook will **not** add a reserved-table blocklist; that would be stricter than Netplan.
2. **Chosen bounds:** `Table` in `1..65535`. Upper bound mirrors Netplan's `uint16`. `Minimum=1` (vs. Netplan's `0`) is marginally stricter but matches Netplan's documented "positive integers starting from 1"; table 0 is the unspec table and meaningless for a user route. (Product decision: `Minimum=1`.)
3. **`priority` is mandatory on the NetworkManager backend.** We keep it optional in the API (per product field choice) but document that omitting it is unsafe on NM guests.
4. **Netplan has no structural check for "a rule must match something"** or for family consistency — those are the two semantic checks the webhook adds, reusing the existing route family-mixing pattern.

## Field-set decisions (confirmed with product)

- `VirtualMachineNetworkRoutingPolicySpec` = `{From, To, Table, Priority}` only. `mark` and `type-of-service` are **not** exposed.
- `VirtualMachineNetworkRouteSpec` gains **`Table` only** (no `from` on routes).
- Feature gate reuses the existing `PerNamespaceNetworkProvider` capability (no new flag).
- Per-interface DNS is already shipped and out of scope.

## References

- Netplan YAML reference: <https://netplan.readthedocs.io/en/latest/netplan-yaml/>
- Linux reserved routing tables (`/etc/iproute2/rt_tables`): 0 unspec, 253 default, 254 main, 255 local.
- Vendored schema: `pkg/util/netplan/schema/schema.json`, generated types `pkg/util/netplan/schema/netplan.go`.
