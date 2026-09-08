# Feature Specification: NIC device Edit convergence and type support

- **Feature branch**: TBD (filed from the `nic-unit-numbers` work)
- **Created**: 2026-09-07
- **Status**: Planned
- **Epic**: vmop-3982 (extends [008-nic-unit-numbers](../008-nic-unit-numbers/))
- **Design docs**: N/A

---

## Summary

Two committed follow-ons from [008-nic-unit-numbers](../008-nic-unit-numbers/)
that share one mechanism — constructing the desired NIC device from
`spec.network.interfaces[i]` and converging disagreement by device-preserving
Edit instead of Remove+Add:

1. **Type support (008 I2).** The desired NIC device is built with the
   hardcoded default type (`defaultEthernetCardType = "vmxnet3"`,
   `pkg/providers/vsphere/network/network.go`). This spec constructs the device
   from `spec.network.interfaces[i].type` (the enum already exists:
   `SRIOV`/`E1000`/`E1000e`/`VMXNet2`/`PCNet32` in
   `api/v1alpha6/virtualmachine_network_advanced_types.go`, and the Telco
   `NICConfigFromMoVM` backfill already populates `type` from the observed
   device) and treats a type disagreement as a diffable property.
2. **Edit-instead-of-replace.** 008 converges *any* disagreement between a
   numbered interface and the device at its declared unit number by
   Remove+Add at the same unit number — an explicit interim decision with one
   accepted regression: a numbered interface loses the device-preserving Edit
   path (e.g. a MutableNetworks network migration replaces the device, new
   `Key`, new MAC when `Generated`). Where an Edit can express the change
   (backing, specified MAC, specified ExternalID — and, with item 1, type),
   emit a device-preserving Edit instead.

## Motivation

- 008 Design point 7 names this work as the motivating driver for accepting
  the replace-not-Edit interim behavior; users re-pointing networks on
  numbered interfaces pay a device replacement (MAC/DHCP churn) that an Edit
  would avoid.
- Type support is simultaneously (a) type-changing convergence and (b) what
  makes 008's compare-then-replace *type-preserving*: the replacement Add would
  materialize `spec.type` rather than always VMXNet3, so a user who sets
  `type: E1000` keeps that type across a replacement.

## Goals

- MUST construct the desired NIC device from `spec.type` everywhere a desired
  device is built (create-path default devices, reconcile Add/replacement).
- MUST include type in the per-device comparison when the spec specifies a
  type, and converge a type disagreement per the Edit-vs-Replace rules below.
- MUST emit a device-preserving Edit where the desired state differs only in
  Editable properties (backing, specified MAC, specified ExternalID, and type
  where vSphere permits an in-place type edit; otherwise Remove+Add).
- MUST restore the orphaned-CR Edit optimization's benefit to numbered
  interfaces: a numbered interface may again resolve through
  `findExistingEthCardForOrphanedCR`-style device-preserving edits where the
  declared unit number is consistent with the located device.

## Non-goals

- No change to the unit-number identity model or the exact-only match rule
  (008 G11/G12): the Edit is of the device *at the declared slot*, never a
  relocation of the slot.
- No powered-on convergence — that is
  [009-nic-unit-numbers-powered-on](../009-nic-unit-numbers-powered-on/).
- No new API fields; `spec.type` exists.

## Inherited constraints (from 008 — load-bearing, restate verbatim in substance)

- **MAC/ExternalId coherence (008 Design point 3).** The Edit MUST resolve the
  MAC from the claiming interface's addressing mode: emit the interface's own
  pinned MAC when `AddressType: Manual`, adopt the located device's MAC only
  when `Generated`. Editing a device claimed from another interface while
  writing the claiming interface's `ExternalId` alongside the *other's* MAC
  would pair one interface's `ExternalId` with another's MAC — breaking
  MAC-pinned NSX-T/VPC logical ports while looking healthy.
- **Ordering constraint (008 Rollout).** This spec must land before
  `VMNetworkUnitNumbers` is enabled in any environment that relies on
  class-ConfigSpec-provided non-default NIC types; until then 008 replaces
  non-default devices with VMXNet3 ones when their properties disagree.

---

## Plan

To be written when this spec is picked up.

## Tasks

To be written when this spec is picked up.
