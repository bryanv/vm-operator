# Tasks

Tracking tickets are pending for this local experiment and must be assigned before merge. Checkboxes record experimental implementation and validation, not release approval.

- [x] [T001] Preserve the baseline provider test binary and inspect the existing workloads (`pkg/providers/vsphere/vmprovider_vm_allocations_test.go`).
- [x] [T002] Replace implicit context embedding and value passing with explicit context use and pointer state, preserving local mutation and async boundaries (`pkg/context/virtualmachine_context.go`, affected callers under `pkg/` and tests).
- [x] [T003] Validate context propagation and lifecycle behavior, compile affected packages, and run relevant suites (existing tests under `pkg/context/` and `pkg/providers/vsphere/`). Five pre-existing gzip golden-string failures were reproduced on the baseline; all other suites passed.
- [x] [T004] Measure identical before/after benchmark workloads and allocation profiles (`pkg/providers/vsphere/vmprovider_vm_allocations_test.go`, `.sdd/specs/013-vm-context-cost/research.md`).
- [x] [T005] Review the final diff for unexpected behavior and document results and limitations (`.sdd/specs/013-vm-context-cost/research.md`).
- [ ] [T006] Before merge, assign real epic/story tickets and complete deployment-platform validation (`.sdd/specs/013-vm-context-cost/spec.md`, `.sdd/specs/013-vm-context-cost/tasks.md`).
