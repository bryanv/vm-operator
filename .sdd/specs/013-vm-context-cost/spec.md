# VM reconciliation context allocation experiment

**Branch**: bryanv/vm-context-allocation-refactor
**Parent branch**: bryanv/vm-reconcile-steady-state-allocs
**Fork**: Current working checkout
**PR target**: Not opened; local experiment
**Created**: 2026-10-30
**Status**: Draft
**Epic**: TBD (required before merge)

## Goals

- Reduce allocation traffic and repeated state copying during VM reconciliation.
- Preserve VM creation, update, status, cancellation, logging, and asynchronous creation behavior.
- Measure the change using identical steady-state reconciliation workloads before and after the change, including a VM with two guest NICs and ten PVC-backed disks.

## Non-goals

- Change cluster-visible APIs, feature defaults, reconciliation order, or VM lifecycle behavior.
- Claim production throughput improvements from simulator timings alone.
- Publish or merge this experimental change without assigning tracking tickets and completing review.

## Acceptance criteria

1. A platform engineer can reproduce before/after allocation measurements with the existing simulator workloads and identify the toolchain, platform, baseline revision, and sample count.
2. Repeated reconciliation preserves VM status, guest network information, attached PVCs, and the existing no-new-vSphere-tasks assertions.
3. Existing VM lifecycle tests continue to pass, including asynchronous creation and cancellation/context propagation checks relevant to the refactor.
4. The measured update path allocates less memory per reconciliation; timings and limitations are reported separately.

## Open questions

- [NEEDS CLARIFICATION: Assign epic and implementation tracking tickets before turning the local experiment into a mergeable PR.]
- Production architecture and production Go toolchain measurements remain follow-up validation; local measurements must state their platform.
