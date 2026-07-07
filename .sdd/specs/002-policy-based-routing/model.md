# Data Model: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **API version**: `v1alpha6` (`api/v1alpha6/virtualmachine_network_types.go`)

All additions are additive to the current active alpha (`v1alpha6`). No field is removed, renamed, or retyped. The existing conversion webhook must drop the new fields on down-conversion and restore them via the `MarshalData` annotation on up-conversion (see § Conversion below). Values are only meaningful with the CloudInit bootstrap provider and the `supports_vm_service_routing_policies` capability (`Features.VMRoutingPolicies`); the webhook enforces both.

---

## Changed type — `VirtualMachineNetworkRouteSpec`

Add a single field. `To`, `Via`, `Metric` are unchanged.

```go
// +optional
// +kubebuilder:validation:Minimum=1
// +kubebuilder:validation:Maximum=65535

// Table is the routing table into which this route is installed. When unset,
// the route is installed into the main table. Used together with
// routingPolicies to implement policy-based routing.
//
// Please note this feature is available only with the following bootstrap
// providers: CloudInit.
Table *int64 `json:"table,omitempty"`
```

## New type — `VirtualMachineNetworkRoutingPolicySpec`

Exactly four fields (per product direction: no `mark`, no `typeOfService`).

```go
// VirtualMachineNetworkRoutingPolicySpec defines a routing-policy rule (an
// "ip rule") that selects a routing table for traffic matching a source
// and/or destination address.
type VirtualMachineNetworkRoutingPolicySpec struct {
	// +optional

	// From matches the source address of the traffic. It is an IP4 or IP6
	// address, optionally with a network prefix length, ex. 192.168.0.0/24.
	From string `json:"from,omitempty"`

	// +optional

	// To matches the destination address of the traffic. It is an IP4 or IP6
	// address, optionally with a network prefix length, ex. 10.0.0.0/8.
	To string `json:"to,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535

	// Table is the routing table this rule selects for matching traffic.
	Table int64 `json:"table"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4294967295

	// Priority is the rule priority. Lower numbers are evaluated first.
	// Specifying an explicit priority is strongly recommended, and is
	// mandatory for guests using the NetworkManager backend.
	Priority *int64 `json:"priority,omitempty"`
}
```

## Changed type — `VirtualMachineNetworkInterfaceSpec`

Add one list field.

```go
// +optional
// +listType=atomic

// RoutingPolicies is a list of routing-policy rules ("ip rules") for this
// interface. Each rule matches traffic by source and/or destination address
// and selects a routing table. Combine with per-route Table values to
// implement policy-based routing on a multi-homed VM.
//
// Please note this feature is available only with the following bootstrap
// providers: CloudInit.
RoutingPolicies []VirtualMachineNetworkRoutingPolicySpec `json:"routingPolicies,omitempty"`
```

---

## Validation rules (webhook)

Structural range checks are handled by the kubebuilder markers above; the webhook adds the semantic checks Netplan cannot express structurally.

| Rule | Where | Behavior |
|------|-------|----------|
| `table` in `1..65535` | CRD marker | Server rejects out-of-range. Mirrors Netplan `uint16`; `Minimum=1` per Netplan's documented "positive integers starting from 1". |
| at-least-one-of `from`/`to` | webhook | Reject a rule with neither set (`field.Required`). |
| single IP family per rule | webhook | When both `from` and `to` are set, reject mixed families (mirrors the existing route family check). |
| `from`/`to` parse as IP or CIDR | webhook | Reject malformed values. |
| CloudInit-only | webhook | Reject `routingPolicies` or any route `table` for non-CloudInit bootstrap. |
| `VMRoutingPolicies` gate | webhook | Reject `routingPolicies` or any route `table` when the `supports_vm_service_routing_policies` capability is deactivated (`field.Forbidden`, `featureNotEnabled`). Applies on create and update, like the existing VLAN and WorkloadIPv6 gates. |

Reserved table numbers (0/253/254/255) are **not** blocked — Netplan itself accepts them (`schema.json` `minimum: 0`, `uint16`), and this feature does not enforce restrictions stricter than Netplan's.

---

## Conversion (v1alpha1–v1alpha5)

The hub is `v1alpha6`; every older version converts through it, so both new fields participate in spoke round-trips:

- **Down-conversion (hub → spoke)** drops the fields. `Convert_v1alpha6_VirtualMachineNetworkInterfaceSpec_To_v1alphaN_...` wrappers already exist in every spoke (added for earlier hub-only interface fields), so `RoutingPolicies` needs only `make generate-go-conversions`. `VirtualMachineNetworkRouteSpec` is today a purely generated conversion; adding `Table` breaks that, so each spoke gains a manual `Convert_v1alpha6_VirtualMachineNetworkRouteSpec_To_v1alphaN_VirtualMachineNetworkRouteSpec` that delegates to the regenerated `autoConvert`.
- **Up-conversion (spoke → hub)** restores the dropped values from the `MarshalData` annotation: extend each spoke's `restore_v1alpha6_VirtualMachineNetworkInterfaces` to copy `RoutingPolicies` (whole-field, like `Type` / `AdvancedProperties`) and per-route `Table`. Interfaces are matched by `Name` (existing behavior); within a matched interface, routes are matched by `(to, via)` — if the spoke client edited a route's `to`/`via`, the old `table` is intentionally not restored onto the changed route.
- **Tests**: hub-spoke-hub round-trip cases with both fields populated in `api/test/v1alphaN/virtualmachine_conversion_test.go` for each spoke version.

This annotation-based preservation is what keeps an older-version client's read-modify-write from silently clearing the new fields (the additive-change UPDATE hazard the constitution references).

---

## Example YAML

```yaml
apiVersion: vmoperator.vmware.com/v1alpha6
kind: VirtualMachine
metadata:
  name: multi-homed-vm
spec:
  bootstrap:
    cloudInit: {}
  network:
    interfaces:
      - name: eth0
        # primary interface, default route via the main table
      - name: eth1
        addresses:
          - 192.168.20.10/24
        gateway4: 192.168.20.1
        routes:
          - to: default
            via: 192.168.20.1
            table: 100          # NEW: route lives in table 100
        routingPolicies:         # NEW
          - from: 192.168.20.0/24
            table: 100
            priority: 100
```

Resulting Netplan (per-ethernet, on `eth1`):

```yaml
network:
  version: 2
  ethernets:
    eth1:
      routes:
        - to: default
          via: 192.168.20.1
          table: 100
      routing-policy:
        - from: 192.168.20.0/24
          table: 100
          priority: 100
```
