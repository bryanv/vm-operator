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
interface. The same ConfigMap backfill is applied to the guest-wide GOSC (LinuxPrep/Sysprep)
DNS server list.

GOSC customization is not symmetric across guest OS families: Sysprep (Windows) honors a
per-adapter DNS server list, while LinuxPrep only ever honors the single guest-wide DNS server
list. VM Operator already writes the per-adapter list for both flavors, populated from each
interface's own explicit `nameservers`, but the **global** DNS backfill (VM-level and
ConfigMap) is gated on Cloud-Init and never reaches that per-adapter field for GOSC VMs — it
only ever lands in the guest-wide DNS server list. This feature does not change either of
those facts; it is called out here because it bounds the scope of this feature (see
Non-goals).

Three problems follow from today's backfill behavior:

1. **Interfaces on separate network segments receive DNS servers they cannot reach.** The
   global DNS is most likely only reachable from the VM's primary network path, but this is not
   guaranteed. On a multi-homed VM whose secondary NIC sits on a different segment, the
   broadcast render may be wrong, and the guest may prefer an unreachable resolver.
2. **NoIPAM interfaces receive DNS they should not.** An interface with no DHCP and no static
   pool assignment (`noIPAM`) has its addressing configured out-of-band by the guest. The
   backfill skip-list today only covers DHCP4/DHCP6, so a NoIPAM interface still gets the
   global nameservers written into its Netplan section.
3. **GOSC DHCP override.** GOSC writes its DNS server list as a static, guest-wide value. When
   the VM also uses DHCP, a ConfigMap-derived static GOSC list overrides the DHCP-supplied DNS
   in the guest, which is most likely not what the user wants — DHCP is expected to be the
   source of truth for infrastructure-default DNS on such VMs. An explicit VM-level nameserver
   value is a deliberate user choice, not an infrastructure default, so it is not subject to
   this override concern.

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
- MUST NOT backfill from the WCP global ConfigMap when **any** interface on the VM uses DHCP4 or
  DHCP6 — for both the Cloud-Init per-interface path and the GOSC guest-wide DNS server list.
  DHCP is expected to supply DNS for such VMs, and the ConfigMap value is an infrastructure
  default that would otherwise silently override DHCP-supplied DNS. This DHCP suppression does
  **not** extend to an explicit VM-level `spec.network.nameservers` / `spec.network.searchDomains`
  value: that still backfills (to the primary interface for Cloud-Init, to the guest-wide list
  for GOSC) even when DHCP is present, since it reflects the VM owner's explicit intent rather
  than an infrastructure default.
- MUST backfill in the order: per-interface `spec.network.interfaces[].nameservers` /
  `searchDomains`, then VM-level `spec.network.nameservers` / `searchDomains`, then the WCP
  global ConfigMap values.
- MUST expand the meaning of `spec.bootstrap.cloudInit.useGlobalNameserversAsDefault` and
  `useGlobalSearchDomainsAsDefault`: when either is `false`, the ConfigMap values are **also**
  not used as the final fallback for that value (in addition to the existing suppression of the
  VM-level fallback). These two flags live under `spec.bootstrap.cloudInit` and apply **only**
  to Cloud-Init VMs; they have no effect on GOSC (LinuxPrep/Sysprep) VMs. GOSC's global-DNS
  backfill is governed solely by the DHCP-skip rule above and the TKG-only search-domain rule
  below.
- MUST preserve the existing V1ALPHA1 rule that ConfigMap search domains are applied only to
  TKG VMs, and that GOSC never receives ConfigMap search suffixes. TKG VMs are always
  bootstrapped with Cloud-Init and never with LinuxPrep/Sysprep, so these are not two
  independent carve-outs: because GOSC only runs on non-TKG VMs, "GOSC never receives ConfigMap
  search suffixes" follows directly from "ConfigMap search domains are applied only to TKG
  VMs."
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
- Does not change how the per-adapter GOSC DNS server list (honored by Sysprep, ignored by
  LinuxPrep) is populated; it continues to reflect only each interface's own explicit
  `nameservers`, never the global backfill. The only new rule for GOSC introduced by this
  feature is suppressing the ConfigMap fallback for the guest-wide DNS server list when any
  interface uses DHCP; an explicit VM-level `spec.network.nameservers` value still backfills
  regardless of DHCP (Goals).
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

- **Given** a VM with one DHCP interface, one static interface with no DNS, and no VM-level
  nameservers,
  **When** the capability is enabled,
  **Then** neither interface receives global DNS and no ConfigMap lookup is performed for
  backfill purposes. The same is true for the GOSC DNS server list.
- **Given** a GOSC LinuxPrep VM with a single DHCP interface and no VM-level nameservers,
  **When** the capability is enabled,
  **Then** the guest-wide GOSC DNS server list (`GlobalIPSettings.dnsServerList`) is left empty
  so DHCP-supplied DNS in the guest is not overridden by a static ConfigMap value.
- **Given** a VM with one DHCP interface, one static interface with no DNS, and an explicit
  VM-level `spec.network.nameservers` value X,
  **When** the capability is enabled,
  **Then** no ConfigMap lookup is performed, but X still backfills onto the VM's primary
  interface (as defined above) for Cloud-Init, or onto the guest-wide GOSC DNS server list — the
  explicit VM-level value is not suppressed by the presence of DHCP elsewhere on the VM. This
  holds even when the primary interface is itself the DHCP interface: X is written to it,
  taking precedence over that interface's DHCP-supplied DNS, because it reflects the VM owner's
  deliberate choice rather than an infrastructure default.

### Tenant user (`useGlobal*` flags — Cloud-Init only)

- **Given** a Cloud-Init VM with `useGlobalNameserversAsDefault: false`, one non-DHCP interface,
  and no VM-level nameservers,
  **When** the capability is enabled,
  **Then** the ConfigMap nameserver values are not written to the interface and are not reported
  as the VM-level DNS.
- **Given** a GOSC (LinuxPrep or Sysprep) VM,
  **Then** `useGlobalNameserversAsDefault` / `useGlobalSearchDomainsAsDefault` do not apply (the
  fields live under `spec.bootstrap.cloudInit`); GOSC's backfill is governed only by the
  DHCP-skip rule and the TKG-only search-domain rule.

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
