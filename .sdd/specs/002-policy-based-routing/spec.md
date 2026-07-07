# Feature Specification: Policy-Based Routing for the Supervisor Cluster

- **Feature branch**: [`bryanv/network-route-policies`](https://github.com/bryanv/vm-operator/tree/bryanv/network-route-policies/)
  - **Fork**: `bryanv/vm-operator`
  - **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-07-06
- **Status**: Draft
- **Epic**: TBD <!-- file the epic before this spec merges -->
- **Design docs**: WIKI page 2516224120 — "One-Pager: Per-Interface DNS and Policy-Based Routing for the Supervisor Cluster"

---

## Summary

A VM attached to more than one network needs its egress traffic to leave through the interface that matches the traffic's source — the classic multi-homing / asymmetric-routing problem. A single `main` routing table cannot express this, because the kernel picks a route by destination alone. Linux solves it with multiple routing tables plus routing-policy rules (`ip rule`) that select a table based on the source address.

This feature lets a DevOps user express that intent declaratively on a `VirtualMachine`: a per-interface list of routing-policy rules, plus the ability to place a static route in a specific routing table. VM Operator translates it into Netplan for guests using the CloudInit bootstrap provider.

Per-interface DNS is **already supported** in the shipped API (`spec.network.interfaces[].nameservers` / `.searchDomains`) and is explicitly out of scope here (see Non-goals).

## Goals

- A DevOps user **MUST** be able to declare, per network interface, a list of routing-policy rules that match on source and/or destination address and select a routing table.
- A DevOps user **MUST** be able to place a per-interface static route into a specific numbered routing table.
- The VM validation webhook **MUST** reject a routing-policy rule that specifies neither a source nor a destination match.
- The VM validation webhook **MUST** reject a routing-policy rule whose source and destination belong to different IP families.
- Routing-policy rules and route tables **MUST** be applied only for VMs using the CloudInit bootstrap provider; the webhook **MUST** reject them for any other bootstrap provider.
- Routing-policy rules and route tables **MUST** be gated behind a new VM Service capability, `supports_vm_service_routing_policies` (feature `VMRoutingPolicies`), that denotes these fields are supported; the webhook **MUST** reject them when the capability is deactivated, on both create and update.
- The feature **MUST NOT** enforce restrictions stricter than Netplan's own schema, other than the two semantic checks above (at-least-one-of source/destination, and single-family per rule) and the documented `1..65535` table range.

## Non-goals

- **Per-interface DNS** — already supported; no API, provider, or webhook change. This spec only guards it with an E2E assertion.
- Routing policy on **VLAN** sub-interfaces — the Netplan schema supports it, but the VM VLAN spec stays L2-only for now.
- Firewall-mark (`fwmark`) and type-of-service matching on routing-policy rules — excluded per product direction (`mark`, `type-of-service` not exposed).
- Bootstrap providers other than CloudInit (LinuxPrep, Sysprep, and the GOSC path cannot express routing-policy).
- Reporting applied routing-policy rules back in `status`.

## User stories / acceptance criteria

### DevOps user (multi-homed VM)

- **Given** a VM with two interfaces (which may be attached to the same network — E2E uses two interfaces on one network; a separate subnet is not required to observe the rules) and the `supports_vm_service_routing_policies` capability activated, **When** the user adds a routing-policy rule on the second interface matching `from: <second interface's address>` to `table: 100` and a static route on that interface with `table: 100`, **Then** the guest (CloudInit) boots with an `ip rule` selecting table 100 for that source and the route present in `ip route show table 100`.
- **Given** a routing-policy rule with neither `from` nor `to` set, **When** the VM is created or updated, **Then** the request is rejected with a clear validation error.
- **Given** a routing-policy rule whose `from` is IPv4 and `to` is IPv6, **When** the VM is created or updated, **Then** the request is rejected with an "cannot mix IP address families" error.

### DevOps user (unsupported configuration)

- **Given** the `supports_vm_service_routing_policies` capability is deactivated, **When** a VM create or update specifies any routing policy or route table, **Then** the request is rejected as feature-not-enabled. (The gate applies to updates too, matching the existing VLAN and WorkloadIPv6 gates.)
- **Given** a VM whose bootstrap provider is LinuxPrep or Sysprep, **When** it specifies any routing policy or route table, **Then** the request is rejected as unsupported for that bootstrap provider.

### Platform engineer

- **Given** a valid routing-policy configuration, **When** the VM is reconciled, **Then** the generated Netplan contains a `routing-policy` block on the corresponding ethernet with `from`/`to`/`table`/`priority`, and routes carrying a `table` are emitted with that `table`.
- **Given** a VM with routing policies and route tables created via `v1alpha6`, **When** the object is read and written back through an older API version (`v1alpha1`–`v1alpha5`), **Then** `routingPolicies` and per-route `table` values survive the round-trip via the conversion annotation.

## Open questions

- [NEEDS CLARIFICATION: Epic ticket number — file the epic and replace `Epic: TBD` before merge.]
- [NEEDS CLARIFICATION: Confirm the one-pager does not also request routing-policy on VLAN sub-interfaces; this spec assumes it does not.]
