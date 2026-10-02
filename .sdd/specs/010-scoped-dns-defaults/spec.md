# Feature Specification: Scoped Guest DNS Defaults

- **Feature branch**: `bryanv/network-dns-bootstrap-changes`
  - **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-09-26
- **Status**: Draft
- **Epic**: TBD <!-- [NEEDS CLARIFICATION: epic ticket] -->

---

## Summary

The Supervisor defines default, "global" DNS nameservers and search domains in the `vmoperator-network-config` ConfigMap. VM Operator falls back to those defaults when bootstrapping a VM's guest networking. Today it applies them too broadly:

- **Cloud-Init**: the default nameservers are written to **every** non-DHCP interface in the generated netplan, including interfaces without IP management (NoIPAM). Netplan's DNS is per interface, so the same resolvers end up configured on links that may not be able to reach them.
- **LinuxPrep**: the default nameservers are written to the customization's global DNS server list. Guest OS Customization (GOSC) treats a non-empty global list as an override of DNS learned from DHCP, so a VM with DHCP interfaces loses the DNS servers its DHCP server provides.
- **Sysprep**: the default nameservers are written only to the global list. Windows DNS servers are configured per adapter, and GOSC documents the per-adapter list as the one Windows uses.
- **VMs with no bootstrap provider**: Linux VMs without a bootstrap provider are customized with LinuxPrep. For them, the operator decides whether search domains apply as if they used no GOSC engine at all. As a result, they receive the default search domains even though LinuxPrep and Sysprep VMs never do.

This feature adds a second mode, **Scoped**. In Scoped mode, a global default is applied only to the VM's first interface, and only when that interface needs it: it has a static IP address and a gateway, and nothing in the VM spec provides its DNS. DNS in the VM spec is applied as it is documented today. The existing behavior remains available as **Legacy** mode.

## Terminology

| Term | Meaning |
|---|---|
| **Global defaults** | `nameservers` / `searchsuffixes` from the `vmoperator-network-config` ConfigMap in the VM Operator namespace. |
| **VM-level DNS** | `spec.network.nameservers` / `spec.network.searchDomains`. |
| **Interface-level DNS** | `spec.network.interfaces[i].nameservers` / `.searchDomains`. |
| **Static interface** | An interface that has at least one static IP address, is not DHCPv4, is not DHCPv6, and whose network does not report NoIPAM. |
| **Primary interface** | The VM's first interface in `spec.network.interfaces` order, when it is static and has a gateway for at least one IP family. Otherwise, for example when the first interface is DHCP, NoIPAM, or has no gateway (including `gateway4`/`gateway6: None`), the VM has none. This matches the vSphere GOSC primary adapter: the first adapter, when it has a static IP address and a static gateway. |
| **Gateway family** | IPv4 counts when the interface has a static IPv4 address with a gateway. IPv6 counts when it has a static IPv6 address with a gateway, or accepts Router Advertisements. |
| **TKG VM** | A VM carrying Cluster API labels (a VKS node). TKG VMs are always Linux and always use Cloud-Init. |
| **Resolved global DNS** | VM-level DNS, falling back to the global defaults. Exposed to bootstrap templates as `.Net.Nameservers`. |

## Goals

### Precedence

- **G0 (MUST)** — In Scoped mode, DNS from the VM spec always wins over the global defaults. For each interface, interface-level DNS comes first, then DNS from the network provider (G0a), then VM-level DNS. A global default is used only as a fallback, only on the primary interface, and only when none of those provides that value for it. Nameservers and search domains each fall back independently. A default is never applied to a DHCP or NoIPAM interface, or to any interface other than the primary one.
- **G0a (MUST)** — In Scoped mode, DNS for an interface from the network provider (such as a VPC SubnetPort) MUST be applied to that interface unless the interface-level DNS is set, and MUST take precedence over the VM-level DNS and the global defaults. Nameservers and search domains are handled independently. Like the global defaults, provider DNS MUST NOT be applied where GOSC would use it to override DHCP: not on a Sysprep adapter that uses DHCP, and not at all for LinuxPrep when any interface uses DHCP. Cloud-Init applies it to DHCP interfaces too, since netplan adds it to the DNS from DHCP. When the engine supports a value only globally, the interfaces' values MUST be added to the global list after the VM-level values, in interface order and without duplicates (G9, G11). Legacy mode MUST ignore provider DNS, so its output does not change when a provider starts to report it. [NEEDS CLARIFICATION: the SubnetPort API fields are not available yet; only the plumbing is done.]

### Mode selection

- **G1 (MUST)** — Scoped mode MUST be gated by a Supervisor capability. [NEEDS CLARIFICATION: capability key name; the placeholder `supports_vm_service_scoped_dns_defaults` is used until WCP assigns one.] When the capability is not activated, every VM MUST use Legacy mode, whether or not it has the mode annotation, and the operator MUST NOT add or change the annotation (G3). Deactivating the capability therefore returns `scoped` VMs to Legacy mode, which re-applies Cloud-Init guestinfo, and re-customizes LinuxPrep and Sysprep VMs without the latch at their next power-on.
- **G2 (MUST)** — Legacy mode MUST produce exactly the same customization data as today, for every bootstrap provider.
- **G3 (MUST)** — The internal annotation `vmoperator.vmware.com/dns-defaults` MUST select the mode for a VM while the capability is activated:
  - `legacy` selects Legacy mode. Any unrecognized non-empty value also selects Legacy mode.
  - `scoped` selects Scoped mode.
  - If the annotation is absent, the operator MUST set it on the VM's next reconcile. It sets `legacy` when the VM's guest may already have been configured, and `scoped` otherwise. A guest may already have been configured if the VM carries any of these annotations: a bootstrap hash annotation, `first-boot-done`, `restored-vm`, `imported-vm`, or `failed-over-vm`. Existing VMs therefore keep the DNS configuration they were deployed with, and only new VMs receive Scoped behavior.
- **G4 (MUST)** — Only privileged users MAY add, modify, or remove `vmoperator.vmware.com/dns-defaults`.

### Scoped mode: Cloud-Init

- **G5 (MUST)** — Interface-level and VM-level DNS MUST be applied as they are today: when `useGlobalNameserversAsDefault` is unset or true, VM-level nameservers are applied to every interface that does not have interface-level or provider nameservers (G0a). `useGlobalSearchDomainsAsDefault` works the same way for search domains.
- **G6 (MUST)** — The global default nameservers MUST be written to the primary interface only, filtered to the IP families the primary interface has a gateway for. For Cloud-Init and Sysprep, they MUST be written only when `spec.network.nameservers` is empty and the primary interface has no interface-level or provider nameservers; `useGlobalNameserversAsDefault` does not affect them. Interface-level nameservers on other interfaces do not prevent this. VM-level nameservers MUST NOT be filtered.
- **G7 (MUST)** — As in Legacy mode, the global default search domains MUST only be applied to TKG VMs with Cloud-Init, and never by LinuxPrep or Sysprep. They MUST be written to the primary interface only, and only when `spec.network.searchDomains` is empty and the primary interface has no interface-level or provider search domains; `useGlobalSearchDomainsAsDefault` does not affect them. They are never filtered by IP family.
- **G8 (MUST)** — DHCP and NoIPAM interfaces MUST NOT receive global defaults.

### Scoped mode: LinuxPrep (explicit or implicit)

- **G9 (MUST)** — The GOSC global DNS server list MUST contain the VM-level nameservers followed by the interfaces' nameservers (G0a), when any are set. The VM-level nameservers are applied even when an interface uses DHCP, since the user chose them; the interfaces' are not (G0a). Otherwise it MUST contain the global default nameservers, filtered to the primary interface's gateway families, but only when the VM has a primary interface and **no** interface uses DHCP. GOSC treats a non-empty global list as a DHCP override for every interface. The global suffix list MUST be the VM-level search domains followed by the interfaces' search domains; the global default search domains are not applied (G7).

### Scoped mode: Sysprep

- **G10 (MUST)** — Windows configures DNS servers per adapter and does not use the GOSC global DNS server list, so the global list MUST be empty. The VM-level nameservers MUST be applied, unfiltered, to every adapter that does not have interface-level or provider nameservers (G0a) and does not use DHCP. A non-empty per-adapter list overrides the DNS servers from DHCP, so DHCP adapters keep their DHCP-provided DNS servers unless the user sets interface-level nameservers on them, which MUST be applied.
- **G11 (MUST)** — The primary adapter MUST get the global default nameservers, filtered by gateway family, when it still has no nameservers after G10. On Windows the search-suffix list is global, so the GOSC global suffix list MUST be the VM-level search domains followed by the adapters' search domains (G0a); the global default search domains are not applied (G7).

### Scoped mode: status and templates

- **G12 (MUST)** — The resolved nameservers provided to vAppConfig and Sysprep templates MUST be the global nameservers (the VM-level nameservers, followed for LinuxPrep by the interfaces' nameservers per G0a), falling back to the global defaults even when every interface has nameservers of its own, so existing templates that index `.Net.Nameservers` keep rendering.
- **G12a (MUST)** — Status MUST report only DNS that was applied:
  - For Cloud-Init, `status.network.config.dns` MUST report only the VM-level DNS. For LinuxPrep, it MUST report the GOSC global lists that are applied. For Sysprep, it MUST report no nameservers, since the global DNS server list is not used, and the GOSC global suffix list that is applied. Nameservers applied to an adapter are reported on that interface.
  - When no bootstrap engine configures the guest network (vAppConfig only, no bootstrap provider, or bootstrap disabled), it MUST report the resolved global DNS, for users who configure the guest by hand.
  - `status.network.config.interfaces[].dns` MUST reflect the DNS applied to each interface, so it changes along with G5, G6, G7, G10 and G11.

  Legacy mode keeps reporting the resolved global DNS, as it does today.
- **G13 (MUST)** — The global-defaults ConfigMap SHOULD be read only when a default or the resolved global DNS is actually needed.

## Non-goals

- Adding new API fields.
- Per-adapter search domains (`dnsDomain`) for Sysprep.
- Changing DNS for VMs that use only vAppConfig (the operator does not configure DNS for them; templates do).

## User stories / acceptance criteria

### DevOps user

- **Given** the capability is activated and a new Cloud-Init VM has two static interfaces and no DNS in its spec, **when** it is deployed, **then** the netplan contains the global default nameservers on the first interface only, and no default search domains. For a TKG VM, the first interface also carries the default search domains.
- **Given** the capability is activated and a new Cloud-Init VM's first interface is static with `gateway4: None` and its second static interface has a gateway, **when** it is deployed, **then** no interface carries the global defaults.
- **Given** the capability is activated and a new Cloud-Init VM's first interface is IPv4 only, **when** it is deployed, **then** it carries only the IPv4 global default nameservers.
- **Given** the capability is activated and a new Cloud-Init VM has a DHCP interface followed by a static interface, **when** it is deployed, **then** no interface carries the global defaults.
- **Given** the capability is activated and a new Cloud-Init VM's second interface sets interface-level nameservers, **when** it is deployed, **then** the first interface still carries the global default nameservers.
- **Given** the capability is activated and a new Cloud-Init VM sets `spec.network.nameservers`, **when** it is deployed, **then** every interface without interface-level nameservers carries them, including DHCP interfaces, and no interface carries the global default nameservers.
- **Given** the capability is activated and a new Cloud-Init VM sets `useGlobalNameserversAsDefault: false` with a static first interface with a gateway, **when** it is deployed, **then** the first interface carries the filtered global default nameservers.
- **Given** the capability is activated and a new Cloud-Init VM has only DHCP or NoIPAM interfaces, **when** it is deployed, **then** no interface carries the global defaults.
- **Given** the capability is activated and a new LinuxPrep VM has only static interfaces, a primary interface, and no VM-level nameservers, **when** it is customized, **then** the GOSC global DNS server list contains the global default nameservers of the primary interface's IP families.
- **Given** the capability is activated and a new LinuxPrep VM has a DHCP interface and no VM-level nameservers, **when** it is customized, **then** the GOSC global DNS server list is empty.
- **Given** the capability is activated and a new Sysprep VM has a static first adapter and no DNS in its spec, **when** it is customized, **then** the first adapter carries the global default nameservers of its IP families, and the global DNS server list is empty.
- **Given** the capability is activated and a new Sysprep VM sets `spec.network.nameservers`, **when** it is customized, **then** every adapter that does not use DHCP and has no interface-level nameservers carries them, and the global DNS server list is empty.
- **Given** a Sysprep VM has a DHCP adapter with interface-level nameservers, **when** it is customized, **then** that adapter carries those nameservers, overriding the DNS servers from DHCP.
- **Given** Legacy mode, **when** a VM is reconciled, **then** `status.network.config.dns` reports the same resolved global DNS as it does today.
- **Given** the capability is activated and a new LinuxPrep VM has a DHCP interface and no VM-level nameservers, **when** it is reconciled, **then** `status.network.config.dns` reports no nameservers.
- **Given** the capability is activated and a new Cloud-Init VM has two static interfaces and no DNS in its spec, **when** it is reconciled, **then** `status.network.config.interfaces[]` reports nameservers for the first interface only.

### CSP admin

- **Given** the capability is activated and a VM needs the prior behavior, **when** an admin sets `vmoperator.vmware.com/dns-defaults: legacy` on it, **then** subsequent bootstraps use Legacy mode.
- **Given** the capability becomes activated on a Supervisor with existing VMs, **when** those VMs are reconciled, **then** each already-bootstrapped VM is annotated `legacy`, and its customization data does not change.
- **Given** a DevOps user, **when** they try to add, change, or remove `vmoperator.vmware.com/dns-defaults`, **then** the request is denied.

## Open questions

- [NEEDS CLARIFICATION: capability key name (owner: WCP capabilities).]
- [NEEDS CLARIFICATION: epic ticket.]
- Resolved: static-only LinuxPrep VMs get the global defaults when no interface uses DHCP (G9).
- Resolved: the global defaults apply only to the first interface, and only when it is static with a gateway. This replaced an earlier rule that searched for the first static interface with a gateway, which was harder to reason about and could drop VM-level DNS.
- Resolved: VM-level DNS is applied as documented today, never scoped to one interface (G5). Sysprep applies VM-level nameservers per adapter (G10), since Windows does not use the GOSC global DNS server list.
- Resolved: Sysprep does not apply VM-level nameservers to DHCP adapters, since that would override DHCP. Interface-level nameservers on a DHCP adapter are applied, so users can still override DHCP explicitly. A Windows VM whose adapters all use DHCP therefore does not apply `spec.network.nameservers`.
- Resolved: `useGlobal*AsDefault: false` also opts out of the global defaults. The webhook already rejects VM-level DNS when the knob is false, so in Legacy mode the knob had no effect on the guest; in Scoped mode it now suppresses the global defaults. New VMs created from manifests that set `false` get no default DNS; call this out in the release note.
- Resolved: in Scoped mode, the global default search domains keep applying to TKG Cloud-Init VMs only, now only on the primary interface (G7).
- The first interface does not qualify when it has no gateway, even if a later static interface does. Users can set `spec.network.nameservers` instead, except for VMs with no bootstrap provider or with vAppConfig only, which the webhook does not allow to set VM-level DNS. They can add `spec.bootstrap.linuxPrep`, which the webhook allows after creation.
- [NEEDS CLARIFICATION: on NoIPAM networks, users can still specify static addresses. Cloud-Init configures those addresses, but in Scoped mode such an interface is never primary, so it receives no defaults (Legacy mode applied them). Confirm this is acceptable.]
- [NEEDS CLARIFICATION: a DNS server on the interface's own subnet, or reachable through `interfaces[].routes`, needs no gateway. Such an interface is still not primary. Refine only if this comes up.]
- During the rollout, multi-NIC VKS clusters will mix DNS layouts: existing nodes stay `legacy` and new nodes are `scoped`, until every node is replaced.
- [NEEDS CLARIFICATION: the G3 annotations may not identify every existing guest. The bootstrap-hash annotations date from 2025-09, and `first-boot-done` is set only when VM Operator itself powers the VM on. A VM that is powered on but carries none of them, for example after a jump upgrade from an older build or when powered on outside VM Operator, would be classified as `scoped`, and a running Cloud-Init VM would have its guestinfo metadata rewritten. A candidate additional signal: a VM without the annotation that is already powered on is `legacy`. This is safe for new VMs because the mode is decided before the first power-on. Undecided.]
- Resolved: deactivating the capability returns every VM to Legacy mode, including annotated VMs (G1). The annotation is kept, so a VM resumes its mode if the capability is reactivated. To return a `scoped` VM to Legacy mode while the capability is activated, an admin sets the annotation to `legacy`.
- Follow-up: the VirtualMachineReplicaSet controller copies its template's annotations onto the VMs it creates as a privileged account, so a DevOps user can set `vmoperator.vmware.com/dns-defaults` through a ReplicaSet template, bypassing G4. The same applies to other privileged annotations such as `first-boot-done`, so it is tracked separately.
