# VM context allocation experiment

## Scope and design

Baseline: `eadfc7414`, compared with the local experimental refactor. The implementation was delegated to Sol, then reviewed and measured independently. The experiment changes internal Go helper signatures, not Kubernetes API types or CRDs.

`VirtualMachineContext` remains a 1,992-byte struct because it still owns an inline 1,944-byte managed object. Its constructor and consumers now use pointers. The `Context` field is named rather than embedded, so neither the struct nor its pointer implicitly satisfies `context.Context`; context-only consumers explicitly receive `.Context`. This prevents reintroducing whole-struct boxing by accident.

Four operations retain deliberate local copies to preserve prior field-isolation semantics: provider update, session update, snapshot-revert logging, and backup property retrieval when properties are missing. Asynchronous creation retains a separate struct copy and `VM.DeepCopy()` before the goroutine starts. The refactor does not deep-copy the managed object or change the previous sharing of its pointer fields and slice backing arrays.

The production diff was checked for changes beyond pointer conversion and `.Context` forwarding. The only such files are the typed-context constructor and the four files containing these copy boundaries. The existing reconciliation order and feature gates are unchanged.

## Benchmark method

- Host: Apple M4 Pro, darwin/arm64; Go 1.27.3. The root module declares Go 1.26.10.
- Save the original provider test binary before editing, then build the changed binary with the same Go toolchain.
- Use the existing `BenchmarkCreateOrUpdateVirtualMachineUpdatePath` benchmark without changing its workloads. The only edit to its source is taking the address of a context literal for the changed helper signature.
- `Baseline` disables networking. `GuestNetwork10PVCs` includes two NICs with guest addresses, DNS and routes, plus ten PVC-backed data disks.
- Creation, initial convergence, and fixture setup are outside the timed region. The benchmark checks that measured calls took the update path, did not add vSphere tasks, and preserved expected guest network and PVC status.
- Final comparison: six pairs, 200 measured updates per scenario per binary per pair, `GOMAXPROCS=6`. Alternate before/after order each pair and run binaries serially after validation finishes. Report medians and sample ranges; do not mix instrumented profile timings into the comparison.
- Raw profiles and binary artifacts are local to `/tmp/vmctx-refactor/`. Normalized benchmark samples are retained alongside this report.

Build each binary from its respective checkout:

```sh
go test -c -o /tmp/vmctx-refactor/before.test ./pkg/providers/vsphere
go test -c -o /tmp/vmctx-refactor/after.test ./pkg/providers/vsphere
```

From the `pkg/providers/vsphere` working directory, run each binary with:

```sh
GOMAXPROCS=6 /tmp/vmctx-refactor/before.test \
  -test.run '^$' \
  -test.bench '^BenchmarkCreateOrUpdateVirtualMachineUpdatePath$' \
  -test.benchmem -test.benchtime=200x -test.count=1
```

Use `after.test` for the changed binary and alternate the order for six pairs. Test logging can split benchmark names from their numeric result lines; the retained samples join those lines without changing the numeric values.

## Results

Medians of six samples per workload/version (200 measured updates per sample):

| Workload | Metric | Before | After | Change |
|---|---|---:|---:|---:|
| Baseline | Bytes/update | 1,217,691.5 | 1,155,818.5 | -5.08% |
| Baseline | Allocations/update | 15,109 | 15,082.5 | -0.18% |
| Baseline | Time/update | 1.8168 ms | 1.8216 ms | +0.26% |
| GuestNetwork10PVCs | Bytes/update | 4,624,668 | 4,505,401.5 | -2.58% |
| GuestNetwork10PVCs | Allocations/update | 48,140 | 48,078 | -0.13% |
| GuestNetwork10PVCs | Time/update | 5.2231 ms | 5.2367 ms | +0.26% |

The median allocation reduction is 61,873 bytes/update for Baseline and 119,266.5 bytes/update for GuestNetwork10PVCs. Fractional medians result from averaging the middle two of six samples, not fractional individual allocation events.

The before/after allocation ranges do not overlap:

| Workload | Before bytes/update range | After bytes/update range |
|---|---:|---:|
| Baseline | 1,216,501–1,221,890 | 1,154,496–1,160,774 |
| GuestNetwork10PVCs | 4,621,970–4,631,295 | 4,503,483–4,509,888 |

Timing ranges overlap substantially: Baseline is 1.7987–1.8302 ms before versus 1.8005–1.8252 ms after; GuestNetwork10PVCs is 5.2032–5.3120 ms before versus 5.1788–5.3308 ms after. These samples demonstrate lower allocation traffic, but do not demonstrate a latency improvement. No statistical significance claim is made for timing.

The exact normalized samples are in [before.txt](before.txt) and [after.txt](after.txt). Full per-pair logs, the serial comparison driver, and parsed JSON remain in `/tmp/vmctx-refactor/`.

## Compiler evidence

Optimized native assembly reports the following local frame sizes. These describe individual functions, not peak goroutine stack use, and do not include their caller-owned argument areas.

| Function | Before | After |
|---|---:|---:|
| `createOrUpdateVirtualMachine` | 6,344 B | 408 B |
| `getCreateArgs` | 2,040 B | 616 B |
| `updateVirtualMachine` | 4,904 B | 4,248 B |
| `reconcileConfig` | 2,264 B | 296 B |

The remaining managed-object value arguments in other APIs are outside this change. The provider still allocates a local VM context where property retrieval requires it and explicitly allocates the async copy. Avoiding repeated copies does not mean that all context storage has disappeared.

## Allocation profiles

Separate baseline-workload profiles use 200 measured updates and `-test.memprofilerate=1`. They include initialization and fixture setup, so their absolute totals are not per-reconcile results. Their timings are also heavily affected by profiling and are excluded from the performance comparison.

At selected directly affected functions, flat allocated bytes decrease consistently with removed context boxing:

| Function | Before profile | After profile |
|---|---:|---:|
| `getTags` | 2,114,352 B | 1,214,432 B |
| `network.MapEthernetDevicesToSpecIdx` | 1,647,776 B | 1,195,168 B |
| `reconcileLocation` | 1,543,176 B | 1,089,488 B |

For example, the two full-context interface conversions in `getTags` are replaced by forwarding the existing context interface. Other allocations in these functions remain.

## Validation

- Entire root module and its tests compile: `GOMAXPROCS=6 go test ./... -run '^$'`.
- New regressions pass: async creation preserves the caller's VM; session update inherits recent tasks while keeping REST/finder enrichment local; backup property retrieval preserves caller managed-object fields with incremental restore enabled and disabled.
- Race check passes for provider tests matching `using async create`.
- Full affected validation completed: 19 package suites passed, including provider, controller, network, session, placement, storage, virtualmachine, boot options, and context tests. The lifecycle suite passed 661 of 666 specs; five CloudInit gzip/base64 golden-string assertions failed.
- A Go overlay restoring all changed Go files to `eadfc7414` reproduced exactly the same five lifecycle failures (23 focused CloudInit specs: 18 passed, 5 failed). Failure names and actual/expected encoded strings matched. Both variants decompress to identical `cloud-init-userdata` or `cloud-init-metadata` text. These existing compressed-byte expectation failures were left unchanged; the full test command therefore returns a nonzero exit code.
- `git diff --check` passes.

Full-suite command, from the repository root:

```sh
KUBEBUILDER_ASSETS="$PWD/hack/tools/bin/darwin_arm64" \
  go test -p 2 ./pkg/context ./pkg/providers/vsphere/... \
    ./pkg/vmconfig/bootoptions ./pkg/util/kube \
    ./controllers/virtualmachine/virtualmachine -count=1
```

Async race check:

```sh
go test -race ./pkg/providers/vsphere -run TestVSphereProvider \
  -ginkgo.focus 'using async create' -count=1
```

Detailed validation logs: `/tmp/vmctx-refactor/full-tests.txt`, `/tmp/vmctx-refactor/race-async.txt`, and `/tmp/vmctx-refactor/baseline-cloudinit.txt`. The baseline gzip diagnostic is in `/tmp/vmctx-refactor/cloudinit-gzip-diagnostic.json`.

## Interpretation and limits

The benchmark includes in-process vCenter simulator and fake Kubernetes client allocations, not just operator code. Percentage changes therefore apply to this harness and should not be presented as production service memory or latency improvements. Allocation traffic is not retained heap size. Timings on the shared development machine may vary substantially.

This measures steady-state updates, not create latency. Existing synchronous and asynchronous create tests validate behavior. Production Linux/AMD64 measurements, deployment-toolchain validation, tracking tickets, and ordinary review remain prerequisites for a shipping change.
