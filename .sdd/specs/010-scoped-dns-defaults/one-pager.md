# One-Pager: Scoped Guest DNS Defaults

- **Spec**: [`spec.md`](./spec.md) · **Plan**: [`plan.md`](./plan.md)
- **Branch**: `bryanv/network-dns-bootstrap-changes`
- **Epic**: TBD
- **Status**: Draft

## Summary

A Supervisor has default DNS nameservers and search domains, set in the `vmoperator-network-config` ConfigMap. VM Operator falls back to them when it bootstraps a VM's guest network. Today these defaults are applied too broadly. They reach interfaces that cannot use them, they override DNS from DHCP, and for Windows they go to a setting the guest ignores.

This change adds a **Scoped** mode, gated by a new Supervisor capability. In Scoped mode, the defaults are applied only to the VM's first interface, and only when that interface needs them. DNS in the VM spec is always applied. Existing VMs stay on the current **Legacy** behavior, so their DNS never changes underneath them.

## Background: DNS support per bootstrap provider

Each provider can configure a different part of the guest's DNS, and the validation webhook only accepts the DNS fields the provider can apply:

| | Cloud-Init | LinuxPrep | Sysprep | vAppConfig only, or no bootstrap |
|---|---|---|---|---|
| **Guest DNS servers** | Per interface (netplan) | Global only; Linux GOSC ignores the per-adapter list | Per adapter only; Windows ignores the global list | Not configured by VM Operator; templates may use `.Net.Nameservers` |
| **Guest search domains** | Per interface (netplan) | Global only | Global only | Not configured by VM Operator |
| **Interaction with DHCP** | Netplan adds the configured nameservers to DHCP's | A non-empty global list overrides DHCP on every interface | A non-empty adapter list overrides DHCP on that adapter | — |
| `spec.network.nameservers` | Allowed when `useGlobalNameserversAsDefault` is unset or true; copied to each interface without its own | Allowed; global list | Allowed. Legacy: global list, which Windows ignores. Scoped: each non-DHCP adapter without its own | Rejected |
| `spec.network.searchDomains` | Allowed when `useGlobalSearchDomainsAsDefault` is unset or true; copied to each interface without its own | Allowed; global list | Allowed; global list | Rejected |
| `interfaces[].nameservers` | Allowed | Rejected | Allowed; adapter list | Rejected |
| `interfaces[].searchDomains` | Allowed | Rejected | Rejected | Rejected |

A Linux VM with no bootstrap provider is customized with LinuxPrep, but the webhook still treats it as having no provider, so none of its DNS fields can be set. Adding `spec.bootstrap.linuxPrep`, which the webhook allows after creation, makes them available.

So the Supervisor defaults have to fit two shapes: per interface for Cloud-Init and Sysprep nameservers, and global for LinuxPrep and Sysprep search domains.

## Problem

The defaults are applied based on the bootstrap engine, with little regard for the interface they land on:

| Engine | What happens today | Why it is a problem |
|---|---|---|
| Cloud-Init | The default nameservers go on **every** non-DHCP interface, including interfaces on networks without IP management (NoIPAM) and interfaces without a gateway. | Netplan DNS is per interface. Resolvers are configured on links that cannot reach them, and every lookup on those links can time out. IPv6 resolvers are added to IPv4-only links. |
| LinuxPrep | The default nameservers go into the GOSC **global** DNS server list, even when an interface uses DHCP. | GOSC treats a non-empty global list as an override of DHCP. A VM with a DHCP interface loses the DNS servers its DHCP server provides. |
| Sysprep | The default nameservers, **and `spec.network.nameservers`**, go only into the GOSC global DNS server list. | Windows does not use the global DNS server list; only the per-adapter list. The VM gets no DNS servers from either the defaults or the VM spec. |
| Implicit LinuxPrep (Linux VM with no bootstrap provider) | The engine is chosen as "no GOSC", so the default search domains are applied. | Explicit LinuxPrep and Sysprep VMs never get them. The behavior depends on whether the provider was spelled out. |

`useGlobalNameserversAsDefault: false` and `useGlobalSearchDomainsAsDefault: false` on Cloud-Init also do not opt a VM out of the Supervisor defaults today. The webhook already rejects VM-level DNS when the knob is false, so the knob has no effect on the guest.

## Goals / non-goals

**Goals**

- DNS from the VM spec always wins over the Supervisor defaults. Per interface, the order is: interface-level DNS, then DNS from the network provider (see below), then VM-level DNS, then the Supervisor default as a fallback.
- A Supervisor default is applied to at most one interface: the **primary interface**.
- A default never overrides DHCP, and never goes to a NoIPAM interface.
- VM-level nameservers actually reach Windows guests.
- No change to the DNS of VMs that are already deployed.

**Non-goals**

- New API fields. The only API change is field documentation.
- Per-adapter search domains for Sysprep.
- Changing DNS for vAppConfig-only VMs, whose templates configure the guest.

## Proposal

### Primary interface

The **primary interface** is the VM's first interface in `spec.network.interfaces`, but only if all of the following hold:

- it has a static IP address;
- it does not use DHCPv4 or DHCPv6;
- it is not on a NoIPAM network;
- it has a gateway for at least one IP family. A `gateway4`/`gateway6` of `None` does not count. For IPv6, accepting Router Advertisements counts.

If the first interface does not qualify, the VM has no primary interface, and no Supervisor defaults are applied. Later interfaces are never considered. This matches the vSphere GOSC notion of a primary adapter. An interface without a gateway usually cannot reach resolvers on other subnets.

The default nameservers are **filtered to the IP families** the primary interface has a gateway for. For example, an IPv4-only interface does not get IPv6 resolvers. VM-level nameservers are never filtered.

### Per engine, in Scoped mode

- **Cloud-Init**
  - VM-level DNS is copied to every interface that does not specify its own, exactly as today. This includes DHCP interfaces, since netplan adds these nameservers to the ones from DHCP.
  - The primary interface gets the defaults when it still has no nameservers or search domains, and only when the matching `useGlobal*AsDefault` knob is unset or true.
  - Search domains follow the same rule as nameservers, for TKG and non-TKG VMs alike.
- **LinuxPrep (explicit or implicit)**: DNS is only global on Linux GOSC.
  - The global lists get the VM-level DNS when it is set.
  - Otherwise they get the defaults, but only when the VM has a primary interface and **no interface uses DHCP**.
- **Sysprep**: on Windows, DNS servers are per adapter and search suffixes are global.
  - The global DNS server list is always empty.
  - The VM-level nameservers go to every adapter that has none of its own and does not use DHCP. A per-adapter list replaces DHCP's DNS servers on Windows, so DHCP adapters are skipped. Interface-level nameservers on a DHCP adapter are still applied, which lets a user override DHCP deliberately.
  - The primary adapter gets the default nameservers when it still has none.
  - The global suffix list gets the VM-level search domains, or else the defaults when the VM has a primary interface.

### DNS from the network provider

VPC SubnetPorts are expected to report nameservers and search domains per port. The SubnetPort API is not available yet, so only the plumbing is in place. In Scoped mode, an interface's provider DNS:

- is applied to that interface unless the interface spec sets its own DNS;
- takes precedence over VM-level DNS and the Supervisor defaults on that interface, which then doesn't get the defaults;
- like the defaults, is never applied where GOSC would use it to override DHCP: not on a Sysprep adapter that uses DHCP, and not at all for LinuxPrep when any interface uses DHCP. Cloud-Init applies it to DHCP interfaces too, since netplan adds it to the DNS from DHCP;
- is merged into the global list where the engine only supports global DNS: nameservers and search domains for LinuxPrep, and search domains for Sysprep. The VM-level values come first, then each interface's in interface order, without duplicates.

Legacy mode ignores provider DNS, so its output doesn't change when SubnetPorts start reporting it.

### Status and templates

- `status.network.config.dns` reports only the global DNS that the engine applies:
  - Cloud-Init: the VM-level DNS.
  - LinuxPrep: the global lists.
  - Sysprep: the search suffixes, and no nameservers.
  - No engine configures the network (vAppConfig only, no bootstrap, or bootstrap disabled): the resolved DNS, meaning VM-level falling back to the defaults, so users can configure the guest by hand.
- DNS applied to an interface is reported under `status.network.config.interfaces[].dns`.
- Templates (`.Net.Nameservers`) keep receiving the resolved nameservers, as today, so existing vAppConfig and Sysprep templates render unchanged.

### Mode selection and upgrade safety

- **Capability**: Scoped mode requires the Supervisor capability `supports_vm_service_scoped_dns_defaults` (placeholder name). While it is not activated, a VM without the annotation below uses Legacy mode, and nothing on the VM changes.
- **Annotation**: while the capability is activated, the internal annotation `vmoperator.vmware.com/dns-defaults` pins each VM to `legacy` or `scoped`. If the annotation is absent, VM Operator sets it on the next reconcile:
  - `legacy` if the guest may already have been configured, meaning the VM has a bootstrap hash annotation, `first-boot-done`, `restored-vm`, `imported-vm`, or `failed-over-vm`;
  - `scoped` otherwise.

  Without this pin, activating the capability would change the hashes of existing VMs. Cloud-Init would re-apply guestinfo on running VMs, and GOSC could re-customize on the next power-on.
- **Who can change it**: only privileged users may add, change, or remove the annotation. An admin can set it to `legacy` to restore the old behavior for a single VM.
- **Legacy mode**: produces exactly the same customization data as today.

## Examples

All examples assume the Supervisor's ConfigMap has:

```yaml
nameservers: "10.0.0.53, fd00::53"
searchsuffixes: "corp.local"
```

and a new VM, so Scoped mode is selected. Interfaces are IPv4-only with a gateway unless noted.

### 1. Cloud-Init, two static interfaces, no DNS in the spec

```yaml
spec:
  bootstrap:
    cloudInit: {}
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
    - name: eth1   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| eth0 nameservers | 10.0.0.53, fd00::53 | 10.0.0.53 |
| eth0 search domains | none (non-TKG) | corp.local |
| eth1 nameservers | 10.0.0.53, fd00::53 | none |
| eth1 search domains | none | none |

In Scoped mode, the defaults are on the first interface only, and only the IPv4 resolver is kept.

### 2. Cloud-Init, first interface without a gateway

```yaml
spec:
  bootstrap:
    cloudInit: {}
  network:
    interfaces:
    - name: eth0   # static, gateway4: None
    - name: eth1   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| eth0 nameservers | 10.0.0.53, fd00::53 | none |
| eth1 nameservers | 10.0.0.53, fd00::53 | none |

eth0 is not a primary interface, so the VM has none. To give it DNS, set `spec.network.nameservers`.

### 3. Cloud-Init, NoIPAM second interface

```yaml
spec:
  bootstrap:
    cloudInit: {}
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
    - name: eth1   # NoIPAM network
```

| | Legacy | Scoped |
|---|---|---|
| eth0 nameservers | 10.0.0.53, fd00::53 | 10.0.0.53 |
| eth1 nameservers | 10.0.0.53, fd00::53 | none |

### 4. Cloud-Init, DNS in the spec

```yaml
spec:
  bootstrap:
    cloudInit: {}
  network:
    nameservers: ["192.168.1.53"]
    searchDomains: ["app.example"]
    interfaces:
    - name: eth0   # static
    - name: eth1   # DHCP
```

| | Legacy | Scoped |
|---|---|---|
| eth0 | 192.168.1.53 / app.example | 192.168.1.53 / app.example |
| eth1 | 192.168.1.53 / app.example, added to DHCP | same |

VM-level DNS behaves the same in both modes.

### 5. Cloud-Init, opting out

```yaml
spec:
  bootstrap:
    cloudInit:
      useGlobalNameserversAsDefault: false
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| eth0 nameservers | 10.0.0.53, fd00::53 | none |
| eth0 search domains | none | corp.local |

The knob now opts out of the default nameservers. Search domains have their own knob, which is unset here.

### 6. LinuxPrep, DHCP and static interfaces

```yaml
spec:
  bootstrap:
    linuxPrep: {}
  network:
    interfaces:
    - name: eth0   # DHCP
    - name: eth1   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| GOSC global DNS servers | 10.0.0.53, fd00::53 | none |
| GOSC global search suffixes | none | none |
| Effect on eth0 | DHCP's DNS servers replaced | DHCP's DNS servers kept |

### 7. LinuxPrep, static only

```yaml
spec:
  bootstrap:
    linuxPrep: {}
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
    - name: eth1   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| GOSC global DNS servers | 10.0.0.53, fd00::53 | 10.0.0.53 |
| GOSC global search suffixes | none | corp.local |

With `spec.network.nameservers` or `searchDomains` set, both modes put those values in the global lists instead.

### 8. Sysprep, no DNS in the spec

```yaml
spec:
  bootstrap:
    sysprep: {...}
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
    - name: eth1   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| GOSC global DNS servers | 10.0.0.53, fd00::53 (ignored by Windows) | none |
| eth0 adapter DNS servers | none | 10.0.0.53 |
| eth1 adapter DNS servers | none | none |
| GOSC global search suffixes | none | corp.local |

### 9. Sysprep, DNS in the spec

```yaml
spec:
  bootstrap:
    sysprep: {...}
  network:
    nameservers: ["192.168.1.53"]
    searchDomains: ["app.example"]
    interfaces:
    - name: eth0   # static
    - name: eth1   # DHCP
    - name: eth2   # static, nameservers: ["172.16.0.53"]
```

| | Legacy | Scoped |
|---|---|---|
| GOSC global DNS servers | 192.168.1.53 (ignored by Windows) | none |
| eth0 adapter DNS servers | none | 192.168.1.53 |
| eth1 adapter DNS servers | none (DHCP) | none (DHCP) |
| eth2 adapter DNS servers | 172.16.0.53 | 172.16.0.53 |
| GOSC global search suffixes | app.example | app.example |

In Legacy mode, `spec.network.nameservers` never reaches the Windows guest. To override DHCP on eth1, set `nameservers` on that interface; both modes apply it.

### 10. Linux VM with no bootstrap provider

```yaml
spec:
  # no bootstrap; the guest is Linux, so LinuxPrep is used
  network:
    interfaces:
    - name: eth0   # static, gateway4 set
```

| | Legacy | Scoped |
|---|---|---|
| GOSC global DNS servers | 10.0.0.53, fd00::53 | 10.0.0.53 |
| GOSC global search suffixes | corp.local | corp.local |

Scoped mode treats this VM the same as explicit LinuxPrep (example 7).

## Compatibility and rollout

- **Legacy output is unchanged**: Legacy mode's output is byte-for-byte identical to today's, so existing VMs keep their customization hashes.
- **Mixed clusters during rollout**: multi-NIC VKS clusters will mix DNS layouts for a while. Existing nodes stay `legacy` and new nodes are `scoped`, until every node is replaced.
- **Deactivating the capability**: annotated VMs keep their mode, so no existing VM's bootstrap changes. Switching a VM back would re-apply Cloud-Init guestinfo, and re-customize LinuxPrep and Sysprep VMs without the latch at their next power-on. VMs created while the capability is off use Legacy mode. An admin can still set a VM's annotation to `legacy`.
- **Release note**: call out the following.
  - Only the first interface gets the Supervisor defaults, and only when it is static with a gateway.
  - `useGlobal*AsDefault: false` now opts out of the defaults.
  - Sysprep now applies `spec.network.nameservers` per adapter.

## Risks and open questions

- **Provider DNS rollout**: once SubnetPorts report DNS, the bootstrap of existing scoped VMs changes. That re-applies Cloud-Init guestinfo and re-customizes GOSC VMs without the latch. This needs a plan before it lands, for example shipping it together with the capability.
- **ReplicaSet annotations**: the ReplicaSet controller copies its template's annotations onto VMs as a privileged account, so a DevOps user can set `dns-defaults` that way. This also affects other privileged annotations and is tracked separately.

- **Capability name and epic**: the capability key name (owner: WCP capabilities) and the epic ticket are not yet assigned.
- **Existing-guest detection**: the annotations used to detect an existing guest may miss some VMs. Examples are VMs jump-upgraded from builds that predate the bootstrap hash annotations, and VMs powered on outside VM Operator. One candidate extra signal is "already powered on", which would select `legacy`.
- **NoIPAM interfaces with static addresses**: they never qualify as primary, so they get no defaults. Legacy mode gave them the defaults.
- **Resolvers reachable without a gateway**: an interface whose resolver is on its own subnet, or reachable through `routes`, still does not qualify as primary without a gateway.
- **Windows E2E**: Windows E2E coverage of per-adapter nameservers is waiting for a testbed with a second workload network and Windows images.

## Testing

- **Unit tests**: `vmlifecycle` and `network` unit tests cover each engine in both modes, primary-interface selection, family filtering, the annotation pinning, and status.
- **Webhook**: unit tests cover the privileged-only annotation.
- **E2E**: a capability-gated Context in `vm_guestcustomization.go` covers Cloud-Init and LinuxPrep.
