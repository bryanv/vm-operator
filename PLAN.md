# Plan: Backfill global DNS/Search Domains to only the "primary" CloudInit interface

## Context

In `pkg/providers/vsphere/vmlifecycle/bootstrap.go`, `GetBootstrapArgs()` backfills global DNS
information (from the WCP global ConfigMap via `config.GetDNSInformationFromConfigMap`) into the
per-interface `network.Bootstrap` values. Today, for CloudInit VMs, the backfill applies the
global nameservers (and, for TKG VMs, search domains) to **every** interface that lacks its own
values and is not DHCP4/DHCP6. CloudInit (Netplan) supports per-interface nameservers, so
broadcasting the global DNS to all interfaces is a quirk. Additionally, **NoIPAM** interfaces
(addressing configured out-of-band in the guest) still receive the global DNS today — the skip
list only covers DHCP.

### Current behavior inventory

- `GetBootstrapArgs` (`pkg/providers/vsphere/vmlifecycle/bootstrap.go`):
  - Scans non-DHCP bootstraps; if any lacks nameservers (and VM-spec DNS empty) or lacks search
    domains (TKG or non-GOSC), fetches global ns/ss from the ConfigMap.
  - GOSC (LinuxPrep/Sysprep): `bsa.DNSServers` → `CustomizationGlobalIPSettings`
    (`bootstrap_linuxprep.go` / `bootstrap_sysprep.go`). GOSC never receives ConfigMap search
    suffixes (V1ALPHA1 rule).
  - CloudInit: loops over **all** bootstraps, skipping only DHCP4/DHCP6, backfilling
    `b.Nameservers = ns` and (TKG only) `b.SearchDomains = ss`.
- Separate per-interface fallback in `InterfaceBootstrap`
  (`pkg/providers/vsphere/network/bootstrap.go`): when `interfaceSpec.Nameservers` is empty and
  `spec.bootstrap.cloudInit.useGlobalNameserversAsDefault` is true (the default), each interface
  gets the VM-spec-level `spec.network.nameservers` / `spec.network.searchDomains`.
- Netplan rendering (`pkg/providers/vsphere/network/netplan.go` → `NetPlanCustomization`) writes
  each `Bootstrap.Nameservers/SearchDomains` into per-ethernet sections.
- Interface ordering: `BuildBootstraps` zips `devices[i]` with `spec.network.interfaces[i]`;
  bootstrap index order == spec order, so index 0 is the first interface in the spec.
- `UpdateNetworkStatusConfig` (`update_status.go`) mirrors per-interface `Nameservers`/
  `SearchDomains` into `status.network.config`, so it automatically reflects the new behavior.

## Approach

### New semantics (capability enabled, no legacy annotation)

1. **Primary interface** = the first interface in `spec.network.interfaces` order whose
   `Bootstrap` is not `NoIPAM`. (DHCP exclusion is unnecessary because of rule 2; NoIPAM
   interfaces are never eligible and never receive backfilled DNS.) If all interfaces are
   NoIPAM, there is no primary and no backfill occurs. Single-interface VMs: the primary is the
   only interface — zero change.
2. **Any-DHCP ⇒ skip backfill entirely**: if *any* interface is DHCP4 or DHCP6, no global
   backfill happens at all — neither the VM-spec-level fallback nor the ConfigMap fallback, for
   CloudInit *or* GOSC (DHCP is expected to provide DNS). (User-confirmed.)
3. **CloudInit backfill targets the primary interface only**, in three layers:
   - `interfaceSpec.Nameservers` / `SearchDomains` (per-interface spec, unchanged);
   - else `spec.network.nameservers` / `searchDomains` when the respective
     `useGlobalNameserversAsDefault` / `useGlobalSearchDomainsAsDefault` flag is true (default);
   - else the ConfigMap global ns (and, for TKG VMs only — existing V1ALPHA1 rule — the
     ConfigMap search suffixes).
4. **Expanded `useGlobal*` flag meaning**: when `useGlobalNameserversAsDefault` is false, the
   ConfigMap is not used as the final DNS fallback either (same for
   `useGlobalSearchDomainsAsDefault` / search domains). Today those flags only gate the
   VM-spec-level fallback; the ConfigMap backfill ignores them. (User-confirmed.)
5. **GOSC**: unchanged except the any-DHCP skip of the ConfigMap `bsa.DNSServers` backfill.
   Search-suffix rules unchanged (GOSC never gets ConfigMap suffixes).
6. `bsa.DNSServers` / `bsa.SearchSuffixes` (VM-level globals feeding GOSC identity,
   `status.network.config.DNS`, and legacy template data) keep their current meaning; the
   ConfigMap fetch for them is gated by the same new conditions (any-DHCP skip, and for
   CloudInit the `useGlobal*` flags). No backfill occurs when no primary exists.

### Legacy behavior (capability disabled, OR capability enabled + legacy annotation set)

Exactly today's behavior: ConfigMap ns/ss backfilled to every non-DHCP interface (CloudInit),
NoIPAM included, `useGlobal*` flags not consulted for the ConfigMap, GOSC backfill regardless
of DHCP. This is both the pre-upgrade default and the per-VM escape hatch.

### Capability & annotation

- This is gated by a **capability**, not a feature flag: new capability key in
  `pkg/config/capabilities/capabilities.go` (cf. `CapabilityKeyVMEviction` →
  `fs.VMEviction`), mapped onto the corresponding `FeatureStates` field in
  `pkg/config/config.go`.
- Legacy-behavior annotation constant kept **internal** (not published in
  `api/v1alpha6`) — e.g. in `pkg/constants` (already imported as `pkgconst` in
  `bootstrap.go`). No CRD regeneration either way; keeping it out of the public API means no
  compatibility surface until the behavior is proven.

### Breaking-change surface

- Capability-disabled environments: none.
- Capability-enabled: single-NIC VMs unchanged. Multi-NIC CloudInit VMs: only the primary keeps the
  ConfigMap/spec-level DNS. Mixed DHCP/static VMs: no global DNS anywhere (intended). All-NoIPAM
  VMs: no global DNS. Rendered netplan changes for existing multi-NIC VMs on next bootstrap
  reconfigure (guestinfo ExtraConfig rewrite); cloud-init only re-applies network config on
  first boot / instance-id change, so running guests are mostly insulated. Per-VM rollback via
  the legacy annotation.

## Files to modify

- `pkg/providers/vsphere/vmlifecycle/bootstrap.go` — rework the backfill section of
  `GetBootstrapArgs`: primary determination, any-DHCP skip, `useGlobal*` gating, capability/annotation
  branching.
- `pkg/providers/vsphere/network/bootstrap.go` — gate the per-interface `networkSpec` fallback
  in `InterfaceBootstrap` so it only runs in legacy mode (capability off or legacy annotation); in
  new mode the spec-level fallback moves to `GetBootstrapArgs` and applies to the primary only.
- `pkg/config/config.go` — new `FeatureStates` entry.
- `pkg/config/capabilities/capabilities.go` — new capability key (cf. `CapabilityKeyVMEviction`).
- `pkg/constants` — internal legacy-behavior annotation constant (not in `api/v1alpha6`).
- `pkg/providers/vsphere/vmlifecycle/bootstrap_test.go` — unit tests for all matrix cases.
- `pkg/providers/vsphere/network/bootstrap_test.go` — unit tests for the gated
  `InterfaceBootstrap` fallback.
- `.sdd/specs/008-primary-interface-dns-backfill/` — new SDD spec entry (spec.md, plan.md,
  tasks.md) per `sdd-standards.md`.
- `test/e2e/...` — E2E coverage per `e2e-sync-with-changes.md`.

## Reuse

- `config.GetDNSInformationFromConfigMap` (`pkg/providers/vsphere/config/config.go`) — fetch
  unchanged.
- `network.Bootstrap` fields `NoIPAM`, `DHCP4`, `DHCP6`, `Nameservers`, `SearchDomains`.
- `kubeutil.HasCAPILabels` — TKG check for the search-domain rule.
- `ptr.DerefWithDefault` for the `useGlobal*` default-true semantics (already used in
  `InterfaceBootstrap`).
- Existing annotation-constant naming patterns for reference (internal constants need not
      match the public `GroupName + "/..."` form).

## Steps

- [ ] Create `.sdd/specs/008-primary-interface-dns-backfill/` (spec.md with the semantics above
      as acceptance criteria, plan.md, tasks.md), tagging the ticket per `sdd-standards.md`.
- [ ] Add the capability key in `pkg/config/capabilities/capabilities.go` and its
      `FeatureStates` field in `pkg/config/config.go` (cf. `CapabilityKeyVMEviction`).
- [ ] Add the legacy annotation constant to `pkg/constants` (internal only, not published
      in `api/v1alpha6`).
- [ ] In `InterfaceBootstrap`, gate the `networkSpec` nameservers/search-domains fallback on
      legacy mode (capability off or annotation set).
- [ ] Rework `GetBootstrapArgs` backfill for new mode: determine any-DHCP and the primary
      (first non-NoIPAM); apply spec-level then ConfigMap fallback to the primary only, gated by
      `useGlobal*` and any-DHCP; apply any-DHCP skip to the GOSC `bsa.DNSServers` backfill.
- [ ] Unit tests: legacy mode byte-for-byte behavior; new mode — single NIC (no change),
      multi-NIC primary-only, NoIPAM excluded, any-DHCP skip (CloudInit + GOSC),
      `useGlobal*=false` suppresses ConfigMap fallback, TKG search-domain rule preserved,
      all-NoIPAM ⇒ no backfill, annotation restores legacy when the capability is on.
- [ ] E2E coverage for the capability-on behavior (multi-NIC VM netplan + status.network.config).

## Verification

- `go test ./pkg/providers/vsphere/vmlifecycle/... ./pkg/providers/vsphere/network/...`
- Ginkgo unit tests as listed in Steps; confirm legacy tests keep passing unchanged
  (regression guard for the capability-off path).
- E2E (capability enabled): multi-NIC VM boots with global DNS present only on the primary interface's
  netplan section; `status.network.config` shows DNS on the primary only; NoIPAM NIC has none.
- Manual review checklist per AGENTS.md: no business logic outside providers/vsphere, no API
  breaking change (annotation only, no CRD regen), capability gates new behavior via
  `pkgcfg.FromContext(ctx)`, copyright headers on any new files.