# Implementation Plan: Primary-Interface DNS Backfill

- **Spec**: [`spec.md`](./spec.md)
- **Epic**: TBD  <!-- mirrors the spec header; replace with the real vmop-NNN before merge -->
- **Date**: 2026-09-02

## Summary

Restrict the bootstrap-time backfill of global DNS nameservers and search domains to a single
"primary" interface (the first non-NoIPAM interface in `spec.network.interfaces` order), never
backfill NoIPAM interfaces, and skip all global DNS backfill when any interface uses DHCP. The
new behavior is gated by a new capability, with a per-VM internal annotation that restores the
legacy broadcast behavior. When the capability is disabled the code path is unchanged.

## Technical context

- **Go version**: whatever `go.mod` pins for the module (no toolchain change).
- **API version(s) touched**: `v1alpha6` is the storage version on `main`; **no API type change**
  and no CRD regeneration. `vmopv1` is imported per-branch, and the only API-adjacent artifact is
  an internal annotation constant, so the change is cherry-pick-safe to `release/*` branches.
- **Modules touched**: `pkg/config`, `pkg/config/capabilities`, `pkg/constants`,
  `pkg/providers/vsphere/network`, `pkg/providers/vsphere/vmlifecycle`.
- **New dependencies**: none.

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility | OK | No CRD field added/removed/renamed; annotation kept internal, so no compatibility surface. No version bump needed. |
| Thin controllers | OK | All logic stays in `pkg/providers/vsphere/{network,vmlifecycle}`; no controller changes. |
| Provider boundary (vSphere API calls only in providers) | OK | No new vSphere API calls; `GetDNSInformationFromConfigMap` keeps its signature. |
| Feature gating for new behavior | OK | New capability in `pkg/config/capabilities/capabilities.go` → `pkgcfg.Features.*`, read via `pkgcfg.FromContext(ctx)`. |
| Conditions communicate state, not gate flow | OK | No conditions touched. `status.network.config` continues to be derived from the applied bootstrap. |
| Copyright header on new files | OK | Any new `.go` file (none currently planned) gets the standard header. E2E/unit additions go into existing files. |
| Ticket references use `vmop-NNN` | Action | The spec header is `Epic: TBD`; must be replaced before merge. |
| SDD spec exists for non-trivial change | OK | This directory. |
| E2E coverage for cluster-observable behavior | Action | Planned (Phase 4); mandatory before merge per `e2e-sync-with-changes.md`. |
| No new cluster-observable behavior without E2E in same PR | OK | Same PR. |

## Project structure

```
pkg/config/capabilities/capabilities.go     # new capability key + CRD status mapping
pkg/config/config.go                        # new FeatureStates field
pkg/constants/constants.go                  # internal legacy-backfill annotation constant
pkg/providers/vsphere/network/bootstrap.go  # gate the per-interface networkSpec fallback
pkg/providers/vsphere/vmlifecycle/bootstrap.go  # primary selection + new backfill rules
pkg/providers/vsphere/vmlifecycle/bootstrap_test.go
pkg/providers/vsphere/network/bootstrap_test.go
test/e2e/vmservice/vmservice/virtualmachine/vm_networking.go   # E2E (capability-enabled)
test/e2e/vmservice/vmservice/virtualmachine/virtualmachinelcm.go  # status.network.config assertions
```

## Design

### Capability

- Add `CapabilityKeyPrimaryInterfaceDNSBackfill = "supports_primary_interface_dns_backfill"` to
  the capability key block in `pkg/config/capabilities/capabilities.go`, with a godoc comment in
  the same style as the neighbouring keys.
- Map it in `updateCapabilitiesFeaturesFromCRD` to a new
  `pkgcfg.FeatureStates.PrimaryInterfaceDNSBackfill bool`. (Capability-only; no `FSS_*` env var,
  no ConfigMap path — newer capabilities are set exclusively from the Capabilities CRD.)
- Default off. When off, `GetBootstrapArgs` and `InterfaceBootstrap` take the legacy path.

### Legacy escape hatch

- Add an internal constant to `pkg/constants/constants.go`, e.g.
  `LegacyGlobalDNSBackfillAnnotation = "vmoperator.vmware.com/legacy-global-dns-backfill"`,
  with a godoc comment marking it internal-only and intended for per-VM opt-out while the
  capability rolls out.
- Helper predicate (in `vmlifecycle`, e.g. alongside `GetBootstrapArgs`) that answers
  "use legacy broadcast behavior?":
  `!features.PrimaryInterfaceDNSBackfill || vm.Annotations[LegacyGlobalDNSBackfillAnnotation] != ""`.
  `network.InterfaceBootstrap` needs the same answer; it already receives the `vm` and a
  `context.Context`, so it can compute it locally via `pkgcfg.FromContext(ctx)` (avoid importing
  `vmlifecycle` from `network`; put the predicate in `network` if both need it, or duplicate the
  two-line check).

### `GetBootstrapArgs` (`pkg/providers/vsphere/vmlifecycle/bootstrap.go`)

Replace the current detection-then-broadcast block with:

1. Compute `anyDHCP` = any `b.DHCP4 || b.DHCP6`.
2. Compute `legacy` via the predicate above.
3. **Legacy path**: leave the existing loop exactly as-is (move it behind the `legacy` branch, no
   logic edits) so capability-off behavior is byte-for-byte identical.
4. **New path**:
   - `primary := -1`; first index `i` where `!bootstraps[i].NoIPAM`. Bail out of all backfill when
     `primary < 0`.
   - `useGlobalNS` / `useGlobalSS` from `spec.bootstrap.cloudInit.useGlobalNameserversAsDefault`
     / `useGlobalSearchDomainsAsDefault` via `ptr.DerefWithDefault(..., true)` — only meaningful
     for CloudInit; treat GOSC as "not applicable" (GOSC has no per-interface concept and no
     `useGlobal*` fields).
   - Fetch the ConfigMap **only when needed**: `!anyDHCP` and at least one of
     {CloudInit with `useGlobalNS` or `useGlobalSS`, GOSC} still has an unfilled value. Reuse
     `config.GetDNSInformationFromConfigMap` and keep the `IgnoreNotFound` handling.
   - When `anyDHCP`: perform **no** backfill at all — not the per-interface values, not
     `bsa.DNSServers` / `bsa.SearchSuffixes`, for CloudInit or GOSC.
   - CloudInit, primary interface only: fill `Nameservers` from `spec.network.nameservers` when
     empty and `useGlobalNS`, then from ConfigMap `ns` when still empty and `useGlobalNS`;
     likewise `SearchDomains` from `spec.network.searchDomains` then ConfigMap `ss` (ConfigMap
     search domains only for TKG VMs, per the existing V1ALPHA1 rule).
   - `bsa.DNSServers` / `bsa.SearchSuffixes` (VM-level globals for GOSC identity,
     `status.network.config.dns`, and legacy template data): keep the existing "fill when empty"
     semantics, but under the new conditions — `!anyDHCP`, and for CloudInit additionally gated
     by `useGlobalNS` / `useGlobalSS`; GOSC search suffixes remain never-backfilled.
   - NoIPAM interfaces are never written to (only `primary` is written at all).

### `InterfaceBootstrap` (`pkg/providers/vsphere/network/bootstrap.go`)

- The existing `defaultToGlobalNameservers` / `defaultToGlobalSearchDomains` block performs the
  VM-level per-interface fallback for CloudInit. Gate that block on the legacy predicate so that
  in new mode it does not run for every interface; `GetBootstrapArgs` then applies the VM-level
  fallback to the primary interface only. Keep `ptr.DerefWithDefault(..., true)` semantics.

## API / CRD strategy

- Additive, no public API change: no new CRD field, no CEL rule, no conversion, no
  `config/crd/bases` regeneration.
- The annotation is intentionally internal (`pkg/constants`), so no webhook validation and no
  compatibility promise while the behavior is rolled out.

## Controller / webhook impact

- None. `DoBootstrap` consumes the mutated `BootstrapArgs` as before; `UpdateNetworkStatusConfig`
  already derives `status.network.config` from the same structs, so the new behavior surfaces in
  status automatically. No RBAC changes. No new watches.
- Bootstrap hashes (`BootstrapHashConfigSpecAnnotationKey` / `...CustomSpec...`) will change for
  affected VMs when the capability is enabled, which triggers the normal bootstrap reconfigure
  path — the intended mechanism for applying the change.

## Test strategy

- **Unit** (`testlabels.Controller`-style Ginkgo, external `_test` packages):
  - `pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`: capability-off regression matrix
    (must match current expectations unchanged), capability-on matrix — single NIC, multi-NIC
    primary-only, NoIPAM-excluded, any-DHCP skip (CloudInit + GOSC), `useGlobal*=false`
    suppression of the ConfigMap fallback, TKG search-domain rule, all-NoIPAM ⇒ no backfill,
    annotation restoring legacy under capability-on.
  - `pkg/providers/vsphere/network/bootstrap_test.go`: `InterfaceBootstrap` per-interface
    fallback runs only in legacy mode.
- **Integration**: existing envtest/VCSim bootstrap suites must continue to pass untouched with
  the capability off (default).
- **E2E** (mandatory, capability enabled in the test environment): a multi-NIC CloudInit VM
  asserts `status.network.config.dns` and per-interface DNS so that only the primary interface
  carries the global nameservers, and that a NoIPAM interface carries none.

## Rollout / migration

- Capability default `false`; the legacy behavior is the default on upgrade, so no VM is
  re-rendered until the capability is activated.
- Activation is per-environment (Capabilities CRD). Per-VM opt-out via the internal annotation
  while the capability is enabled.
- Removal criteria: once the capability is the default everywhere and the annotation has no
  consumers, delete the legacy branch, the predicate, and the internal annotation in a follow-up
  spec (`.sdd/specs/NNN-primary-interface-dns-backfill-ga/`).
- Release note: "Capability-gated change to how global DNS nameservers and search domains are
  backfilled onto VM network interfaces: they are now applied to the VM's primary (first
  non-NoIPAM) interface only, are no longer applied to NoIPAM interfaces, and are skipped
  entirely when any interface uses DHCP."

## Complexity tracking

| Violation | Why needed | Simpler alternative rejected because |
|-----------|------------|--------------------------------------|
| Two code paths (legacy broadcast vs primary-only) live side by side during rollout | Capability-gated rollout requires the old path to remain reachable until the capability is universally enabled | Removing the legacy path outright would change behavior for every existing multi-NIC VM with no rollback lever |
| Primary-selection logic is positional rather than user-designated | Avoids new published API surface and CRD regeneration for a behavior fix; spec order is already the stable interface order | A new `spec.network.primaryInterface`-style field would be a public API addition whose semantics must be maintained forever |
