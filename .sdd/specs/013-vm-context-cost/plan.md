# Implementation plan

This behavior-preserving experiment implements the [specification](spec.md) by removing repeated copies of the large VM reconciliation context and preventing implicit conversion of the entire struct to `context.Context`.

## Technical context

- Baseline revision: `eadfc7414`.
- Local toolchain: Go 1.27.3, darwin/arm64, Apple M4 Pro. The root module declares Go 1.26.10.
- Baseline `VirtualMachineContext` size: 1,992 bytes, including a 1,944-byte `mo.VirtualMachine`; allocating interface conversions consume 2,048 bytes each on this toolchain.
- Existing benchmark: `BenchmarkCreateOrUpdateVirtualMachineUpdatePath`, covering `Baseline` and `GuestNetwork10PVCs`.

## Implementation

1. Make `VirtualMachineContext.Context` a named field so the type no longer implicitly implements `context.Context`.
2. Return and pass `*VirtualMachineContext`, including pointer receivers for its helper methods. Pass `.Context` explicitly to context-only consumers.
3. Audit assignments to Context, Logger, MoVM, and nested inline fields. Preserve previous value-parameter isolation where needed with an explicit copy at the mutation boundary rather than at every call.
4. Preserve the asynchronous creation boundary: explicitly copy the state struct and deep-copy the Kubernetes VM before launching the goroutine.
5. Update affected callers and tests across provider, lifecycle, network, session, and configuration packages. Preserve the existing benchmark workloads.

## Constitution check

- This is a cross-package internal refactor, so plan and tasks are recorded under `.sdd/specs/`.
- Controllers remain thin; vSphere operations and reconcile order remain unchanged.
- No CRD or Kubernetes API contract changes; no code generation, RBAC, conversion webhook, or new feature flag is required.
- No new cluster-visible behavior is introduced. Existing lifecycle coverage is used; this experiment does not add a new E2E behavior.
- Existing feature gates remain in place. This structural optimization is not an in-progress product feature and does not introduce a runtime gate with duplicate call graphs.
- The existing stored context is made explicit; this experiment does not introduce stored contexts in additional types. Splitting context and state into separate parameters everywhere is outside this experiment.
- Existing repository test files are extended instead of imposing an unrelated test layout migration. New Go code follows repository headers and aliases.
- Tracking tickets are intentionally unresolved for the user's local experiment and must be assigned before merge. The user requested a local branch containing the implementation, SDD artifacts, and handoff. No tickets, pushes, or PRs are created as part of this experiment.

## Validation and measurement

- Save the unchanged provider test binary before editing, then build the changed binary with the same toolchain.
- Run both binaries from the same provider package working directory, using the same sub-benchmarks, iteration counts, and sample counts. Separate setup from timed reconciliation using the existing benchmark.
- Compare allocation bytes, allocation counts, and wall time. Use allocation profiles to verify that removed costs correspond to VM context boxing rather than a skipped workload.
- Compile all affected callers and tests, run relevant suites, and exercise async behavior and context propagation. Use race detection where feasible for affected asynchronous tests.
- Record commands, results, and limitations in `research.md`.

## Rollout

Keep this as a reviewable local experiment. Any production rollout requires normal code review, ticket assignment, and validation with the deployment toolchain and architecture. There are no data migrations or API changes.
