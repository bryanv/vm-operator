# Data Model: Policy-Based Routing

- **Spec**: [`spec.md`](./spec.md)
- **API version**: `v1alpha6` (`api/v1alpha6/virtualmachine_network_types.go`)

All additions are additive to the current active alpha (`v1alpha6`). No field is removed, renamed, or retyped, so no conversion webhook is required. Values are only meaningful with the CloudInit bootstrap provider and the `PerNamespaceNetworkProvider` capability; the webhook enforces both.

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
| `PerNamespaceNetworkProvider` gate | webhook | Reject `routingPolicies` or any route `table` when the capability is disabled (`field.Forbidden`, `featureNotEnabled`). |

Reserved table numbers (0/253/254/255) are **not** blocked — Netplan itself accepts them (`schema.json` `minimum: 0`, `uint16`), and this feature does not enforce restrictions stricter than Netplan's.

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
