# Research: Netplan `dhcp-overrides`

Investigation log backing [`spec.md`](./spec.md) and [`plan.md`](./plan.md).

## Source

Netplan reference: <https://netplan.readthedocs.io/en/stable/netplan-yaml/#dhcp-overrides>

`dhcp4-overrides` / `dhcp6-overrides` are per-device mappings that tune how a
netplan-rendered interface behaves once it has DHCP-assigned state. Netplan
already fully models these in the generated schema this repo vendors at
`pkg/util/netplan/schema/netplan.go` (`type DHCPOverrides struct { ... }`,
attached to `EthernetConfig`/`VLANConfig` via `Dhcp4Overrides`/
`Dhcp6Overrides *DHCPOverrides`). Nothing in the schema layer needs to
change; the gap is entirely in whether and how VM Operator populates it from
the VM Spec.

## Full `dhcp-overrides` catalog and VM Spec fit

| Key | Backend(s) | Default | What it does | VM Spec analog today | Verdict |
|---|---|---|---|---|---|
| `use-routes` | networkd + NetworkManager | `true` | Installs (or, if `false`, ignores) routes received from DHCP, including the default route/gateway. | `Gateway4`/`Gateway6` already has a `"None"` sentinel meaning "ignore the network provider's gateway" (`VirtualMachineNetworkInterfaceSpec.Gateway4/6` godoc, `api/v1alpha6/virtualmachine_network_types.go`). Today that sentinel is only meaningful for static addressing; it has no effect when DHCP is active because the mutual-exclusivity check in the validating webhook rejects `Gateway4`/`Gateway6` set to anything but `""` whenever `DHCP4`/`DHCP6` is `true`. | **Implement.** Extending the existing `"None"` sentinel to also be legal (only as `"None"`) while DHCP is active is a one-value carve-out of an existing field, not new API surface, and maps 1:1 onto `use-routes: false`. |
| `use-mtu` | networkd only | `true` | Applies (or ignores) the MTU value received from DHCP. | `MTU *int64` already exists per-interface, applied unconditionally today regardless of DHCP state. | **Defer.** A real gap — an explicit `MTU` alongside active DHCP can be silently clobbered by a DHCP-provided MTU — but resolving it means deciding when to auto-derive `use-mtu: false` (e.g., "whenever `MTU` is explicitly set on a DHCP interface") and confirming that's the desired default instead of leaving user opt-in. Worth a follow-up spec once `use-routes` ships and the override plumbing (see `plan.md`) is proven out. |
| `use-dns` | networkd only | `true` | DHCP-provided DNS servers take precedence over statically configured ones (both are used; DHCP wins ties). | `Nameservers []string` exists per-interface. | **Defer.** Unlike `use-routes`/`use-mtu`, `use-dns: false` doesn't just suppress DHCP's contribution — semantics of "precedence" vs. "exclusive" differ across the two DNS-related keys (see `use-domains` below) and need their own design pass. |
| `use-domains` | networkd only | unset | Whether the DHCP-provided domain name is used as a DNS search domain (`true`), only for routing DNS queries (`"route"`), or not at all. | `SearchDomains []string` exists per-interface. | **Defer.** Same reasoning as `use-dns`; the three-way value (`bool`/`"route"`) also doesn't map cleanly onto a plain boolean flag on the VM Spec without new API surface. |
| `route-metric` | networkd + NetworkManager | unset | Metric applied to routes netplan auto-installs from DHCP. | `VirtualMachineNetworkRouteSpec.Metric` exists, but only for the static `Routes` list — there's no field for "metric of the DHCP-supplied default route." | **Defer.** Needs a genuinely new field; no existing sentinel or reinterpretation of a current field covers it. |
| `send-hostname` | networkd only | `true` | Whether the machine's hostname is sent to the DHCP server. | `spec.network.hostName` exists, but it's VM-level, not per-interface. | **Defer.** Lowest priority: no per-interface analog, and a multi-NIC VM has no obvious interface to "own" this VM-level setting. |
| `use-hostname` | networkd only | `true` | Whether the hostname received from DHCP is set as the guest's transient hostname. | Same VM-level/per-interface mismatch as `send-hostname`. | **Defer.** Same reasoning as `send-hostname`; also the most likely to trip the dhcp4/dhcp6-overrides "must match" constraint (see below) if set asymmetrically per family, since a transient hostname is not really a per-address-family concept. |
| `hostname` | networkd only | unset | Custom hostname value sent to the DHCP server in place of the machine's own. | None. | **Defer.** Same reasoning as `send-hostname`. |

**Ranked follow-up order**, if/when more overrides are picked up: `use-mtu` →
`use-dns`/`use-domains` (as a pair, since they interact) → `route-metric` →
`send-hostname`/`use-hostname`/`hostname` (as a group, lowest priority, most
new API surface required).

## The dhcp4/dhcp6-overrides parity constraint

Netplan docs, verbatim:

> If both `dhcp4` and `dhcp6` are `true`, the `networkd` backend requires that
> `dhcp4-overrides` and `dhcp6-overrides` contain the same keys and values.
> If the values do not match, an error will be shown and the network
> configuration will not be applied.

This is a hard constraint on the rendered YAML, not a VM Operator policy
choice — get it wrong and the guest's `netplan apply` fails outright,
leaving the interface without applied config. It has two independent
enforcement points in this feature (see `plan.md` "Test strategy" for why
both are needed, not just one):

1. **Admission time** — only catches the case where `spec.network.interfaces[].dhcp4`
   and `.dhcp6` are both explicitly `true` in the request being validated.
2. **Generation time** — the network provider (NetOP, NCP, VPC) can itself
   decide to hand out DHCP for a family the user never set explicitly
   (`DHCP4`/`DHCP6` left `nil` in the spec, subnet configured for DHCP). The
   effective, post-provider-resolution DHCP state is not knowable at
   admission time, so the parity constraint must also be enforced where the
   netplan config is actually assembled, using the fully-resolved state.

## Existing code already doing 90% of the `Gateway4`/`Gateway6` = `"None"` work

`pkg/providers/vsphere/network/bootstrap.go`'s `InterfaceBootstrap` already
special-cases the `"None"` sentinel (`gatewayIgnored` constant) to clear a
statically-assigned gateway out of `bootstrap.IPConfigs[i].Gateway`. That
logic is orthogonal to DHCP (it only touches `IPConfigs`, which are empty for
a pure-DHCP interface) — this feature adds a second, DHCP-specific
consumption of the same sentinel rather than replacing the existing one.
