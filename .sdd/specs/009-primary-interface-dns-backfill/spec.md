# Feature Specification: Primary-Interface DNS Backfill

- **Feature branch**: `feature/primary-interface-dns-backfill`
  - **Fork**: `vmware-tanzu/vm-operator`
  - **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-09-02
- **Status**: Draft
- **Epic**: TBD  <!-- MUST be replaced with a real `vmop-NNN` epic before merge -->
- **Design docs**: —

---

## Background

When a VM is bootstrapped with Cloud-Init, VM Operator renders a Netplan document in which
every ethernet section may carry its own `nameservers.addresses` and `nameservers.search`.
Cloud-Init/Netplan therefore supports per-interface DNS.

VM Operator does not currently take advantage of that. For a VM whose interfaces do not all
specify their own DNS, the bootstrap path backfills the **global** DNS values — both the
VM-level values (`spec.network.nameservers` / `spec.network.searchDomains`) and, as a final
fallback, the infrastructure values from the WCP global ConfigMap — onto **every** non-DHCP
interface. The same ConfigMap backfill is applied to the guest-wide GOSC
(LinuxPrep/Sysprep) DNS server list.

Two problems follow from broadcasting global DNS to every interface:

1. **Interfaces on separate network segments receive DNS servers they cannot reach.** The
   global DNS is only guaranteed to be reachable from the VM's primary network path. On a
   multi-homed VM whose secondary NIC sits on a different segment, the broadcast render is
   wrong, and the guest may prefer an unreachable resolver.
2. **NoIPAM interfaces receive DNS they should not.** An interface with no DHCP and no static
   pool assignment (`noIPAM`) has its addressing configured out-of-band by the guest. The
   backfill skip-list today only covers DHCP4/DHCP6, so a NoIPAM interface still gets the
   global nameservers written into its Netplan section.

The intended outcome is that the infrastructure-derived and VM-level global DNS defaults land
on exactly one interface — the interface VM Operator determines to be the VM's **primary**
interface — and never on a NoIPAM interface. Because this changes cluster-observable bootstrap
output, the new behavior is opt-in behind a capability, with a per-VM escape hatch that
restores the legacy behavior.

## Goals

- MUST backfill the global DNS nameservers and search domains onto at most **one** interface:
  the VM's primary interface, defined as the **first interface in `spec.network.interfaces`
  order that is not `noIPAM`**.
- MUST NOT backfill nameservers or search domains onto any interface whose bootstrap reports
  `noIPAM`.
- MUST NOT backfill any global DNS (VM-level or ConfigMap) when **any** interface on the VM uses
  DHCP4 or DHCP6 — for both the Cloud-Init per-interface path and the GOSC guest-wide DNS server
  list. DHCP is expected to supply DNS for such VMs.
- MUST backfill in the order: per-interface `spec.network.interfaces[].nameservers` /
  `searchDomains`, then VM-level `spec.network.nameservers` / `searchDomains`, then the WCP
  global ConfigMap values.
- MUST expand the meaning of `spec.bootstrap.cloudInit.useGlobalNameserversAsDefault` and
  `useGlobalSearchDomainsAsDefault`: when either is `false`, the ConfigMap values are **also**
  not used as the final fallback for that value (in addition to the existing suppression of the
  VM-level fallback).
- MUST preserve the existing V1ALPHA1 rule that ConfigMap search domains are applied only to
  TKG VMs, and that GOSC never receives ConfigMap search suffixes.
- MUST gate all of the above behind a new capability; when the capability is disabled the
  behavior is byte-for-byte identical to today's behavior.
- MUST provide a per-VM annotation escape hatch that, when the capability is enabled, restores
  the legacy broadcast behavior for that VM. The annotation is defined internally (not published
  in the `api/` package) for now.
- MUST continue to report the effective per-interface DNS in `status.network.config` interfaces
  and the VM-level DNS in `status.network.config.dns`, consistent with what was applied.

## Non-goals

- Does not change the precedence *within* an interface: an interface that specifies its own
  nameservers/searchDomains is never overridden.
- Does not change GOSC identity fields (hostname, domain, timezone, password), nor GOSC
  per-adapter configuration other than DNS.
- Does not introduce, remove, or rename any published API field; no CRD regeneration.
- Does not add a publicly documentable way for users to designate the primary interface; primary
  selection is positional (first non-NoIPAM interface in spec order).
- Does not change IPv6 RA/DHCP handling, static route handling, MTU handling, or VLAN rendering.
- Does not change how the global ConfigMap is read
  (`config.GetDNSInformationFromConfigMap`) or its content/format.

## User stories / acceptance criteria

### Tenant user (Cloud-Init, multi-NIC)

- **Given** a VM with three Cloud-Init interfaces in spec order `[A, B, C]`, none of which
  specifies nameservers, and a VM-level or ConfigMap global nameserver X,
  **When** the capability is enabled and no interface uses DHCP,
  **Then** only interface A's Netplan section contains X; B and C contain no nameservers.
- **Given** the same VM where interface B specifies its own nameserver Y,
  **When** the capability is enabled,
  **Then** A still receives the global X and B still resolves Y (B is never overwritten).

### Tenant user (NoIPAM)

- **Given** a VM whose first interface `A` is `noIPAM` and whose second interface `B` is a static
  pool interface with no DNS of its own,
  **When** the capability is enabled and no interface uses DHCP,
  **Then** `A` receives no nameservers or search domains, and `B` (the first non-NoIPAM
  interface) receives the global values.

### Tenant user (DHCP)

- **Given** a VM with one DHCP interface and one static interface with no DNS,
  **When** the capability is enabled,
  **Then** neither interface receives global DNS and no ConfigMap lookup is performed for
  backfill purposes. The same is true for the GOSC DNS server list.

### Tenant user (`useGlobal*` flags)

- **Given** a VM with `useGlobalNameserversAsDefault: false`, one non-DHCP interface, and no
  VM-level nameservers,
  **When** the capability is enabled,
  **Then** the ConfigMap nameserver values are not written to the interface and are not reported
  as the VM-level DNS.

### User (escape hatch)

- **Given** the capability is enabled and a VM carries the internal legacy-backfill annotation,
  **Then** the VM retains today's behavior: ConfigMap nameservers are broadcast to every
  non-DHCP interface, and NoIPAM interfaces are included exactly as before.
- **Given** the capability is disabled and a VM carries the annotation,
  **Then** behavior is unchanged from today (the annotation has no additional effect).

### Operator

- **Given** the capability is disabled,
  **Then** every VM's rendered Netplan, GOSC customization spec, and `status.network.config` are
  identical to the pre-change behavior, and the existing unit-test suite passes unchanged.

## Open questions

- [NEEDS CLARIFICATION: the epic ticket number — the header currently reads `Epic: TBD` and
  MUST be replaced with the real `vmop-NNN` before the spec PR merges, per
  `sdd-standards.md`.] Owner: spec author.
