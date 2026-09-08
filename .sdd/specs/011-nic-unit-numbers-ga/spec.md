# Feature Specification: NIC Unit Numbers — GA promotion and flag removal

- **Feature branch**: TBD
- **Created**: 2026-09-07
- **Status**: Planned
- **Epic**: vmop-3982 (closes out [008-nic-unit-numbers](../008-nic-unit-numbers/))
- **Design docs**: N/A

---

> **Numbering note**: the 008 plan/tasks anticipated filing this as
> `005-nic-unit-numbers-ga`, but `005` was taken in the meantime
> (`005-ipv6-template-funcs`), and `009`/`010` went to the powered-on and
> device-edit follow-ons filed alongside this spec; `011` is the next free
> number.

## Summary

[008-nic-unit-numbers](../008-nic-unit-numbers/) ships behind the opt-in
`supports_VM_service_network_unit_numbers` Supervisor capability
(`CapabilityKeyVMNetworkUnitNumbers`), default-off, with the
`FeatureVersionNICUnitNumbers` schema-upgrade bit. This spec tracks GA
promotion: enabling the capability by default once the feature has been
validated in production, and then removing the flag/gate plumbing per this
repository's standard feature-lifecycle practice.

## Motivation

008's constitution check requires every new behavior to ship behind a feature
flag, and 008's rollout section records the operational effects of enabling it
(the I24 "every VM is not-upgraded for a window" effect, the T034
spec.network.interfaces edit freeze, the paused disk/CD-ROM mutators). Those
effects are acceptable during opt-in validation but must become one-time and
cluster-wide at GA, after which the flag plumbing is dead weight to be removed.

## Goals

- MUST define the GA criteria here (filled in as production validation
  proceeds): sustained production validation of backfill, admission
  assignment, exact-only matching, status population, and the divergence
  condition on heterogeneous environments (multiple vSphere versions and NIC
  types, including SR-IOV — 008's Q3 remained open at implementation time and
  MUST be closed before GA).
- MUST enable the capability by default in the Supervisor capabilities
  contract (or flip the default if it has become an FSS/env flag by then).
- MUST, after a full release cycle at default-on, remove the
  `VMNetworkUnitNumbers` feature-state plumbing, the capability key handling,
  and the flag gates across the webhook/backfill/matching code — following the
  repository's flag-removal practice (flag checks become unconditional, or are
  deleted outright).
- MUST keep the `FeatureVersionNICUnitNumbers` bit parsing/removal semantics
  safe across the removal (older binaries reading newer annotations must not
  wedge; 008's downgrade analysis is the reference).

## Non-goals

- No behavior change beyond making the feature unconditional: the identity
  model, matching, backfill, and validation rules are 008's and are frozen at
  GA (follow-on behavior changes belong to
  [009](../009-nic-unit-numbers-powered-on/) /
  [010](../010-nic-unit-numbers-device-edit/)).
- No new API fields.

---

## Plan

To be written when this spec is picked up.

## Tasks

To be written when this spec is picked up.
