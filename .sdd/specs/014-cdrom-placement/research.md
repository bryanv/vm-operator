# Research: CD-ROM Placement and Creation

## Incident evidence

The local support bundle for vmop-11174 contains repeated `vim.fault.InvalidDeviceSpec` failures during `VirtualMachine.reconfigure`. In `vpxd-4.log.gz`, the failure at 2026-09-17T17:40:56.303Z identifies `com.vmware.vim.vpxd.vmprov.inaccessibleUrlPath` and `deviceChange[1].device.backing.fileName`. The failing device is an ISO-backed VirtualCdrom being added to an already-created VM, with startConnected true, connected false, IDE controller key 200, and unit 0. This confirms that attachment fails after creation because the backing URL is inaccessible. The Kubernetes VM dump is empty because its API connection was refused; it cannot establish the original VM spec. No customer paths, credentials, or raw bundle contents are copied into the repository.

## Code trace

- `vmCreateGenConfigSpec` supplies both individual placement and `vmGroupGetVMPlacementConfigSpec`.
- `CreateConfigSpecForPlacement` already retains CD-ROM backing paths if supplied; they were absent upstream.
- CD-ROM lookup and creation currently occur only during update reconciliation.
- Explicit controller placement is gated by VMSharedDisks and mutation supplies controller addresses.
- Fast deployment passes the selected host to CreateVM; legacy ISO `createVM` currently passes nil.
- Existing image/controller helpers can be reused without changing the VM update reconcile order.

## Implementation findings

- Checking the actual ConfigSpec for an ISO backing avoids changing placement shortcuts for VMs without ISO media. ISO VMs request a host and home datastore even with a pinned zone or one candidate zone.
- Datastore recommendation parsing previously depended on FastDeploy. It now follows the requested placement result so legacy ISO creation can use the recommended home datastore instead of choosing another policy-compatible datastore independently.
- Automatic controller selection is shared by create and update. Creation reconstructs controller child lists from devices before allocating slots because ConfigSpec child lists are optional and may reference replaced media.
- Existing instance-storage host selections are preserved when ISO compatibility requires another placement check.
- The new path defaults disabled and follows activation of the Supervisor capability `supports_vm_service_cdrom_placement`; it has no FSS environment toggle. Capability discovery tests cover absence, activation, deactivation, and dry-run behavior.

## Local validation

- All placement package tests passed.
- All virtualmachine device tests, CD-ROM reconciler tests, and configuration package tests passed. Focused CD-ROM tests were rerun after extracting the shared controller-selection helper.
- Provider request-capture tests passed for automatic and explicit controller placement, fast and legacy ISO creation, pinned zones, rollback, no-compatible-host rejection, OVF VMs with ISO media, and fast/legacy group placement.
- The E2E virtualmachine package compiles. Its updated ISO lifecycle scenario is experimental pending a real Supervisor run; no stretched-cluster result is claimed.
- Lint passed for the changed provider/config packages and the E2E virtualmachine package. The installed linter was built with Go 1.26 and initially panicked when the default Go 1.27 toolchain was selected; running it with `GOTOOLCHAIN=go1.26.10` succeeds.
- The complete vSphere provider suite passed (`go test ./pkg/providers/vsphere`). Later additions for OVF-attached ISO media and legacy group placement also passed in a focused run.
