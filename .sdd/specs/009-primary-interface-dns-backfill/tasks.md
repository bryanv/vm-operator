# Tasks: Primary-Interface DNS Backfill

- **Spec**: [`spec.md`](./spec.md)
- **Plan**: [`plan.md`](./plan.md)
- **Epic**: TBD  <!-- mirrors the spec header -->

> Ticket tags are written as `[vmop-TBD]` until the epic and its stories/sub-tasks are filed;
> each shipping-code task MUST carry a real `[vmop-NNN]` linked to the epic via
> `customfield_10830` before merge (`sdd-standards.md`).

## Phase 1 — Setup

- [ ] T001 [vmop-TBD] Add capability key `CapabilityKeyPrimaryInterfaceDNSBackfill =
      "supports_primary_interface_dns_backfill"` and map it in
      `updateCapabilitiesFeaturesFromCRD` (`pkg/config/capabilities/capabilities.go`).
- [ ] T002 [P] [vmop-TBD] Add `FeatureStates.PrimaryInterfaceDNSBackfill bool` with the
      capability-key comment (`pkg/config/config.go`).
- [ ] T003 [P] [vmop-TBD] Add the internal `LegacyGlobalDNSBackfillAnnotation` constant with a
      godoc note that it is internal/not published (`pkg/constants/constants.go`).

## Phase 2 — Foundational

- [ ] T004 [vmop-TBD] Add the shared legacy-behavior predicate (capability off OR annotation
      set) where both consumers can use it, or the equivalent two-line local check in
      `pkg/providers/vsphere/network/bootstrap.go` and
      `pkg/providers/vsphere/vmlifecycle/bootstrap.go`.
- [ ] T005 [vmop-TBD] Gate the `defaultToGlobalNameservers` / `defaultToGlobalSearchDomains`
      VM-level per-interface fallback in `InterfaceBootstrap` on the legacy predicate
      (`pkg/providers/vsphere/network/bootstrap.go`).

## Phase 3 — User stories

### US6 (operator: capability-off regression) & US1 (Cloud-Init multi-NIC)

- [ ] T006 [US6] [vmop-TBD] Move the existing backfill loop in `GetBootstrapArgs` behind the
      legacy branch with no logic edits, so capability-off output is unchanged
      (`pkg/providers/vsphere/vmlifecycle/bootstrap.go`).
- [ ] T007 [US1] [vmop-TBD] Implement the new path in `GetBootstrapArgs`: `anyDHCP` detection,
      `primary` = first non-NoIPAM interface, `useGlobal*` gating, primary-only VM-level then
      ConfigMap nameserver/search-domain backfill (`pkg/providers/vsphere/vmlifecycle/bootstrap.go`).
- [ ] T008 [P] [US6] [vmop-TBD] Capability-off regression tests: existing expectations in
      `pkg/providers/vsphere/vmlifecycle/bootstrap_test.go` pass unchanged, plus
      `InterfaceBootstrap` fallback tests (`pkg/providers/vsphere/network/bootstrap_test.go`).
- [ ] T009 [P] [US1] [vmop-TBD] Table tests for single-NIC (no change) and multi-NIC
      primary-only Cloud-Init backfill, including the "interface B specifies its own DNS is not
      overwritten" case (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).

### US2 (NoIPAM)

- [ ] T010 [US2] [vmop-TBD] NoIPAM tests: first interface `noIPAM` + first non-NoIPAM receives
      the globals; all-NoIPAM ⇒ no backfill
      (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).

### US3 (DHCP)

- [ ] T011 [US3] [vmop-TBD] Any-DHCP tests: no ConfigMap lookup/fallback for Cloud-Init or GOSC
      (LinuxPrep + Sysprep) when no VM-level nameservers/search domains are set; and, when an
      explicit VM-level value is set, confirm it still backfills onto the primary interface
      (Cloud-Init) or `bsa.DNSServers`/guest-wide list (GOSC) despite `anyDHCP`
      (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).

### US4 (`useGlobal*` flags)

- [ ] T012 [US4] [vmop-TBD] `useGlobalNameserversAsDefault: false` /
      `useGlobalSearchDomainsAsDefault: false` tests: ConfigMap values are not applied to the
      primary interface and not reported as VM-level DNS
      (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).

### US5 (escape hatch)

- [ ] T013 [US5] [vmop-TBD] Annotation tests: capability on + annotation ⇒ legacy broadcast
      (NoIPAM included); capability off + annotation ⇒ unchanged from today
      (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).
- [ ] T014 [P] [US5] [vmop-TBD] Preserve the TKG-only ConfigMap search-domain rule under
      capability-on, including the non-TKG/GOSC case where "no ConfigMap search suffixes"
      follows directly from "TKG-only" rather than being tested as a separate rule
      (`pkg/providers/vsphere/vmlifecycle/bootstrap_test.go`).

## Phase 4 — E2E (mandatory, capability enabled)

- [ ] T015 [US1] [vmop-TBD] Multi-NIC Cloud-Init E2E: assert the global nameservers appear only
      on the primary interface in `status.network.config.interfaces` and in
      `status.network.config.dns`, and that a NoIPAM interface reports no DNS
      (`test/e2e/vmservice/vmservice/virtualmachine/vm_networking.go`,
      `test/e2e/vmservice/vmservice/virtualmachine/virtualmachinelcm.go`).

## Phase Final — Polish

- [ ] T016 [vmop-TBD] Replace `Epic: TBD` in `spec.md` / `plan.md` with the real `vmop-NNN`,
      attach real tickets to every task, and flip the spec status to `In Progress`.
- [ ] T017 [P] [vmop-TBD] Register the spec in the `.sdd/INDEX.md` specs table.
- [ ] T018 [P] [vmop-TBD] Add the release-note block to the PR description per
      `pull-request-standards.md` and note the capability name for CSP admins.
- [ ] T019 [vmop-TBD] File the follow-up GA spec that removes the legacy branch, the predicate,
      and the internal annotation once the capability is the default.
