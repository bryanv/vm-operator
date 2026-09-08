# Feature Specification: Reflect NIC hardware changes while the VM is powered on

- **Feature branch**: TBD (filed from the `nic-unit-numbers` work)
- **Created**: 2026-09-07
- **Status**: Planned
- **Epic**: vmop-3982 (extends [008-nic-unit-numbers](../008-nic-unit-numbers/))
- **Design docs**: N/A

---

## Summary

Today — and after 008 ships — NIC device changes are computed only in the
powered-off reconcile path (`getConfigSpecForPoweredOffVM` /
`resizeVMWhenPoweredStateOff`, the latter only under `MutableNetworks`):
`poweredOnReconfigure` emits no NIC device changes at all. A new interface, a
removed interface, or the Add/Remove pair produced by renumbering an existing
interface's `unitNumber` may be *admitted* to a powered-on VM's spec, but none
of it takes effect until the VM is next powered off and reconciled. This spec
commits the follow-on stated in 008: **allow these changes to be reflected
while the VM is powered on**.

## Motivation

- 008's identity model makes a renumber an *admitted* spec change with a
  long-lived divergence window: the declared slot holds no device (or holds the
  interface's old device) until the next power-off — surfaced by the
  008 hardware condition (`VirtualMachineHardwareNICsVerified`) and tolerated
  by boot options (008 G13), but never converged.
- Users adding a NIC to a running VM currently see it appear only after a power
  cycle, which is surprising for day-2 elasticity workflows (telco MutableNetworks
  flows in particular).
- vSphere supports NIC hot-add/hot-remove; the gap is purely in the operator's
  reconcile dispatch, not the platform.

## Goals

- MUST compute NIC device changes (Add, Remove, and the compare-then-replace
  pair) in the powered-on reconcile path, applying them via hot-add/hot-remove
  when the VM's hardware/guest supports it.
- MUST degrade gracefully when hot-add is not possible (guest tools absent,
  device class not hot-pluggable, hard affinity constraints): leave the change
  admitted-but-unapplied — the 008 G13 state — rather than failing the
  reconcile; the 008 condition continues to report it.
- MUST re-use 008's matching and convergence semantics unchanged
  (exact-unit-only for numbered interfaces, compare-then-replace, I4
  bookkeeping); this spec changes *when* device changes are computed, not
  *what* they are.

## Non-goals

- No change to the unit-number identity model, matching, or backfill (008).
- No change to the `spec.network.interfaces[i].type` handling — type support is
  tracked separately (see [010-nic-unit-numbers-device-edit](../010-nic-unit-numbers-device-edit/)).
- No memory/CPU or other device-class hot-plug work.
- No change to the E2E flag/capability surface.

## Known constraints to carry in

- `resizeVMWhenPoweredStateOff` computes NIC changes only under
  `MutableNetworks` today; the dispatch table in 008's plan ("Reconcile entry
  points and flag interactions") is the map of paths this spec must widen.
- Hot-remove of the device a renumber leaves behind must respect guest
  connected state and must not race the fixup pass that re-identifies new
  devices by unit number (008 I4).

---

## Plan

To be written when this spec is picked up.

## Tasks

To be written when this spec is picked up.
