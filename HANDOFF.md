# VM context allocation refactor handoff

## Branch and scope

- Branch: `bryanv/vm-context-allocation-refactor`.
- Parent branch: `bryanv/vm-reconcile-steady-state-allocs`.
- Baseline commit: `eadfc7414`.
- Date: 2026-10-30.
- Status: implemented and measured local experiment; not pushed or deployed.

The user requested verification of the copying and interface-boxing costs of `VirtualMachineContext` in the VM provider's create/update workflow, followed by a broad experimental fix and before/after measurements. Sol implemented the refactor; the primary agent independently reviewed the diff and ran the broader validation and measurements.

The branch includes the refactor, regression tests, this handoff, and the complete [SDD experiment](.sdd/specs/013-vm-context-cost/spec.md), including its index entry, plan, tasks, research, and normalized benchmark samples. The pre-existing untracked `test/e2e/binary` is unrelated and must not be staged or deleted as part of this work.

## Confirmed costs

On Go 1.27.3, darwin/arm64, `VirtualMachineContext` is 1,992 bytes, of which the inline `mo.VirtualMachine` occupies 1,944 bytes. Passing it by value copies the struct shallowly, including pointer and slice headers, rather than deep-copying referenced data. Optimized production assembly confirmed full copies at real provider call sites.

Passing the value to a `context.Context` parameter boxes the entire struct, rather than extracting its embedded context. The measured conversions allocate 2,048 bytes each. This compiler's nonescaping interface stack-temporary optimization is limited to values of at most 1,024 bytes, so even a conversion reported as nonescaping can allocate for this type. The struct is not inherently too large for an ordinary stack variable; copying, frame size, and interface allocation are separate concerns.

Initial isolated measurements were approximately 23.5 ns for a non-inlined value call versus 0.72 ns for a pointer call, and 317 ns plus one 2,048-byte allocation for `pkgcfg.FromContext(vmCtx)` versus 17.8 ns and zero allocations for `pkgcfg.FromContext(vmCtx.Context)`. These isolate individual mechanisms and are not reconciliation latency measurements.

## Implementation and invariants

- `NewVirtualMachineContext` returns a pointer. Consumers and helper methods use pointers to avoid repeated large argument copies.
- `Context` is a named field instead of an embedded interface. Neither the struct nor its pointer now implicitly implements `context.Context`; context-only calls explicitly receive `.Context`.
- The struct still contains an inline managed object and remains 1,992 bytes. Its size was not reduced.
- Explicit shallow copies preserve previous field isolation in provider update, session update, snapshot-revert logging, and backup's missing-properties retrieval branch.
- Async creation explicitly copies the struct and deep-copies the Kubernetes VM before starting the goroutine. Preserve this boundary in follow-up edits.
- Existing sharing of managed-object pointer fields and slice backing arrays is unchanged. Do not replace the deliberate shallow copies with shared pointers without reviewing mutation semantics.
- Reconciliation order, existing feature gates, Kubernetes API types, and CRDs are unchanged. Internal Go helper signatures have changed, so downstream Go callers need pointer arguments and explicit context forwarding.

Key files: [typed context](pkg/context/virtualmachine_context.go), [provider workflow](pkg/providers/vsphere/vmprovider_vm.go), [session update](pkg/providers/vsphere/session/session_vm_update.go), [snapshot revert](pkg/providers/vsphere/vmprovider_vmsnapshot.go), and [backup](pkg/providers/vsphere/virtualmachine/backup.go).

## Before/after results

The existing `BenchmarkCreateOrUpdateVirtualMachineUpdatePath` workloads were preserved. Its only source change takes the address of a context literal to satisfy the changed helper signature. Creation and fixture convergence occur outside the timed region; assertions verify the update path, unchanged vSphere task lists, and expected guest network/PVC status.

Host/toolchain: Apple M4 Pro, Go 1.27.3, darwin/arm64, `GOMAXPROCS=6`. Six serial before/after pairs, alternating order, with 200 measured updates per workload per binary per pair. Values below are medians.

| Workload | Allocated bytes/update before | After | Reduction | Time before | Time after |
|---|---:|---:|---:|---:|---:|
| Minimal VM | 1,217,691.5 | 1,155,818.5 | 5.08% | 1.8168 ms | 1.8216 ms |
| Two NICs and ten PVCs | 4,624,668 | 4,505,401.5 | 2.58% | 5.2231 ms | 5.2367 ms |

This saves approximately 62 KB and 119 KB of allocation traffic per update respectively. Allocation counts decrease from 15,109 to 15,082.5 and from 48,140 to 48,078. Fractional values are medians of an even number of samples.

There is no demonstrated latency improvement: both time medians increased approximately 0.26%, and the timing ranges overlap. Allocation ranges do not overlap. The harness includes in-process vCenter simulator and fake Kubernetes client allocations; these percentages are not production service memory reductions. Allocation traffic is also not retained heap size. Create latency was not measured.

Optimized ARM64 local frame sizes decreased from 6,344 to 408 bytes for `createOrUpdateVirtualMachine`, from 2,040 to 616 bytes for `getCreateArgs`, and from 2,264 to 296 bytes for `reconcileConfig`. These are individual frame sizes, not peak goroutine stack measurements.

See [research and reproducibility details](.sdd/specs/013-vm-context-cost/research.md), [before samples](.sdd/specs/013-vm-context-cost/before.txt), and [after samples](.sdd/specs/013-vm-context-cost/after.txt).

## Validation and known failures

- Entire root module and its tests compile: `GOMAXPROCS=6 go test ./... -run '^$'`.
- Nineteen affected package suites passed, including provider, controller, network, session, placement, storage, virtualmachine, boot options, and context.
- The lifecycle suite passed 661 of 666 specs. Five CloudInit assertions comparing exact gzip/base64 strings failed. Restoring all changed Go files to the baseline through a Go overlay reproduced exactly the same five failures; the compressed strings differ but decompress to identical userdata/metadata. These pre-existing failures were left unchanged, so the full validation command exits nonzero.
- New regressions cover async creation preserving the caller's VM, inherited recent-task state with local REST/finder enrichment, and isolated backup property retrieval with incremental restore both enabled and disabled.
- The async-create tests pass under the race detector.
- `git diff --check` passed.

Run the affected suites from the repository root, adjusting the assets directory for the host:

```sh
KUBEBUILDER_ASSETS="$PWD/hack/tools/bin/darwin_arm64" \
  go test -p 2 ./pkg/context ./pkg/providers/vsphere/... \
    ./pkg/vmconfig/bootoptions ./pkg/util/kube \
    ./controllers/virtualmachine/virtualmachine -count=1

go test -race ./pkg/providers/vsphere -run TestVSphereProvider \
  -ginkgo.focus 'using async create' -count=1
```

## Evidence and next steps

Durable findings and numeric benchmark samples are in `.sdd/specs/013-vm-context-cost/`. The spec remains Draft with tracking tickets pending, as this was requested as a local experiment.

On the original development machine, `/tmp/vmctx-refactor/` contains saved before/after test binaries, per-pair logs, `compare.py`, parsed results, allocation profiles, compiler output, validation logs, and baseline CloudInit diagnostics. `/tmp/vmctx-cost.MBefuX/` contains the initial microbenchmark and compiler investigation. These temporary paths are not committed and may disappear; use the checked-in research and samples as the persistent record.

Before shipping:

1. Review the pointer-signature migration and deliberate copy boundaries, especially asynchronous ownership and managed-object mutation.
2. Repeat measurements with the deployment Go toolchain and Linux/AMD64. The root module declares Go 1.26.10; the reported timings were collected with Go 1.27.3 on ARM64.
3. Assign real epic/story tickets and update the SDD artifacts before merge. No new product feature or runtime feature flag was introduced by this structural experiment.
4. Address the pre-existing gzip golden-string assertions separately if a fully green suite is required on this toolchain.
5. Treat this as a demonstrated allocation reduction. Further performance work should profile remaining costs rather than assume that eliminating these copies materially improves end-to-end latency.
