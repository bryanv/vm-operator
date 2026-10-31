# Tasks: CD-ROM Placement and Creation

- **Spec**: [spec.md](./spec.md)
- **Plan**: [plan.md](./plan.md)
- **Epic**: TBD
- Implementation is tracked locally against vmop-11174; epic/story association must be resolved before merge.

## Phase 1: Investigation and specification

- [x] T001 Record acceptance criteria and support-bundle findings in `.sdd/specs/014-cdrom-placement/` and register `.sdd/INDEX.md`.

## Phase 2: Implementation

- [x] T002 [US1] [vmop-11174] Add Supervisor capability discovery and activation/deactivation coverage in `pkg/config/config.go`, `pkg/config/capabilities/capabilities.go`, and `pkg/config/capabilities/capabilities_test.go`.
- [x] T003 [US1] [vmop-11174] Build CD-ROMs before placement in `pkg/providers/vsphere/virtualmachine/cdrom.go` and `pkg/providers/vsphere/vmprovider_vm.go`; honor the host in `pkg/providers/vsphere/vmlifecycle/create_contentlibrary.go`. Extend recommendation handling in `pkg/providers/vsphere/placement/zone_placement.go`, `pkg/providers/vsphere/placement/group_placement.go`, and `pkg/providers/vsphere/placement/cluster_placement.go`.

## Phase 3: Validation

- [x] T004 [US1] [vmop-11174] Extend device and provider regression coverage in `pkg/providers/vsphere/virtualmachine/cdrom_test.go`, `pkg/providers/vsphere/vmprovider_vm_iso_test.go`, and `pkg/providers/vsphere/vmprovider_vmgroup_test.go`.
- [x] T005 [US1] [vmop-11174] Extend ISO lifecycle E2E coverage in `test/e2e/vmservice/vmservice/virtualmachine/virtualmachinelcm.go` and declare its capability requirement in `test/e2e/vmservice/consts/consts.go`.
- [x] T006 Run focused local tests and lint; record outcomes in `research.md`.
- [ ] T007 Validate on a stretched Supervisor with an ISO datastore inaccessible from at least one candidate zone, then remove the E2E experimental label.
