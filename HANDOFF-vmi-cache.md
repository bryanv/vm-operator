# Not caching VirtualMachineImage: handoff

Short-term fix for UBMWM-4090: stop caching namespace-scoped `VirtualMachineImage` (VMI) resources, keep caching `ClusterVirtualMachineImage` (CVMI). Branch: `bryanv/do-not-cache-vmi`. The findings below come from reading the code and running tests; no production profiles or latency measurements were taken.

## What is done (three commits on the branch)

1. `d2de0450a` **Add selectable fields to VM image CRDs.** `+kubebuilder:selectablefield` markers in `api/v1alpha6/virtualmachineimage_types.go`: `status.name` and `status.providerItemID` on VMI, `status.name` on CVMI. Regenerated CRDs in `config/crd/bases/`.
2. `01ee83ab3` **Do not cache VirtualMachineImage resources.** VMI added to the client `DisableFor` list in `pkg/manager/manager.go`. The VMI `status.name` field index was removed from the mutation webhook (`webhooks/virtualmachine/mutation/virtualmachine_mutator.go`); the CVMI index stays. `ResolveImageName` (`pkg/util/vmopv1/vm.go`) is unchanged: its `MatchingFields{"status.name": ...}` list is now evaluated by the API server through the selectable field.
3. `2f806747c` **Stop watching VirtualMachineImage in publish req.** Removed the VMI watch and mapper from `controllers/virtualmachinepublishrequest`. The published-image lookup now uses `MatchingFields{"status.providerItemID": ...}`. `test/builder/fake.go` registers a matching `status.providerItemID` index on the shared fake client so unit tests emulate the selector.

Verification: `go build ./...` and `go vet` on the changed packages are clean. `go test` over `./pkg/... ./controllers/... ./webhooks/... ./services/...` passed (tool output was truncated, saw "174 passed in 101 packages", no failures). The publish request suite ran 49 specs against envtest and passed. `make lint-go` was not run.

## Why this works (verified)

- controller-gen v0.22.0 (`hack/tools/go.mod`) supports `+kubebuilder:selectablefield:JSONPath="..."` and emits `selectableFields` in the CRD.
- On a real kube-apiserver v1.34.1 (envtest binaries in `hack/tools/bin/darwin_arm64`), an uncached client filtered correctly by `status.name` and `status.providerItemID`, and rejected an undeclared field (`status.type`) with `field label not supported`.
- `DisableFor` only affects client `Get`/`List`. It does not stop informers created by `Watches(...)` or `IndexField(...)`, which is why the VMI index and the publish request watch had to go.

## Constraints and caveats

- **Kubernetes version:** selectable fields are beta (default on) from 1.30 and GA in 1.32. Not checked which version Supervisor ships. Below 1.30 this approach does not work.
- **Per-version:** `selectableFields` is declared per CRD version. Only v1alpha6 has them. A field-selector request through v1alpha1 to v1alpha5 is rejected. No in-repo caller does that.
- **Limits:** at most 8 selectable fields per CRD, each must be a scalar.
- **Publish request latency:** without the watch, noticing the published image takes up to 10s (the `requeueResult()` poll) instead of being immediate.
- **Do not `git add -A` on `test/`:** the untracked `test/e2e/binary` (88 MB) was accidentally committed once and removed by amend. It is still untracked on disk. Stage specific paths. `HANDOFF.md` (unrelated work) is also untracked.
- Per repo rules, the change did not add or update a spec under `.sdd/specs/` and made no E2E changes. Decide whether the PR needs either.

## Remaining live-GET cost (not addressed)

VMI reads now go to the API server. Create-time sites are one GET per VM create and are accepted: `GetVirtualMachineImageSpecAndStatus` (`pkg/providers/vsphere/vmprovider_utils.go`), mutation `ResolveImageNameOnCreate`, validation `getVMImageConfigSpec`, and the unified storage quota webhook.

The update-path sites are the ones to look at:

| Site | Frequency | Notes |
|---|---|---|
| Policy reconciler `getPolicyEvaluationResults` → `GetImage` (`pkg/vmconfig/policy/policy_reconciler_util.go:52`) | Every VM update reconcile when `VSpherePolicies` is on | **Hot path.** Only needs the image name and labels. |
| CD-ROM reconciliation `processImage` (`pkg/providers/vsphere/virtualmachine/cdrom.go`) | Every update reconcile for VMs with image-backed CD-ROMs; also hardware validation status and schema upgrade backfill | Narrow but per reconcile. |
| Storage policy usage controller (`controllers/virtualmachine/storagepolicyusage/storagepolicyusage_controller.go:384`) | Per VM per StoragePolicyUsage reconcile | Scales with VMs per policy per namespace. |
| Content library controller (`controllers/contentlibrary/utils/controller_builder.go`, `CreateOrPatch`) | Every ContentLibraryItem reconcile, including resyncs | No VMI watch, so no event amplification. |

## TODO

- [ ] Confirm the minimum Kubernetes version on Supervisor is 1.30 or newer.
- [ ] Run `make lint-go` and fix findings.
- [ ] Decide on a spec update under `.sdd/specs/` and on E2E coverage (publish request still reaches the image-available state; VM create by friendly image name still resolves).
- [ ] Measure API server call volume for the update-path sites above before optimizing. Candidates: skip the policy image lookup when the VM already exists and has policy tags, cache the CD-ROM backing file name per reconcile.
- [ ] Open the PR using `.sdd/memory/pull-request-standards.md`, noting the 10s publish request latency and the Kubernetes version requirement in the release note.
- [ ] Optionally add selectable-field markers to older API versions if any client queries them.

## Related

- Analysis gist (secret): https://github-vcf.devops.broadcom.net/gist/b197b20521e90c0d9b5d959bf4d57ada. It predates the selectable-field finding, so its in-memory-filter suggestion for `ResolveImageName` is superseded by commit 2 and the publish request section by commit 3.
