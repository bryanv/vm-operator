# Implementation Plan: CD-ROM Placement and Creation

- **Spec**: [spec.md](./spec.md)
- **Epic**: TBD
- **Date**: 2026-10-31

## Summary

Resolve requested ISO images and build their devices in the common creation-spec builder before either individual or group placement. Reuse the existing ISO lookup and controller assignment code. Pass the same devices into VM creation and honor the placement host in the legacy ISO create path.

## Technical context

- Go 1.26.10; existing govmomi and controller-runtime dependencies.
- Root and `test/e2e` modules; no API or CRD changes, new dependencies, RBAC, or webhook changes.
- Provider packages: `pkg/providers/vsphere`, `virtualmachine`, `vmlifecycle`, and `placement`.

## Constitution check

| Rule | Status | Notes |
|------|--------|-------|
| API compatibility and generated manifests | OK | No API changes. |
| Thin controllers and vSphere boundary | OK | All behavior remains in the vSphere provider. |
| Reconcile order and status | OK | Existing update order and status reconciliation remain intact. |
| Feature gating | OK | `pkgcfg.FromContext(ctx).Features.CDROMPlacement` gates the new creation path; defaults false and is populated from the Supervisor capability `supports_vm_service_cdrom_placement`. |
| Unit/integration/E2E coverage | Implemented; validation in progress | Extend existing test files and the ISO lifecycle E2E scenario; hardware validation remains pending. |
| SDD and tickets | Pending before merge | Local draft uses the supplied incident reference; epic/story association still needs confirmation. |
| Copyright, imports, godoc, errors | Required | Follow repository standards. |
| Ticket masking | OK | Use vmop-11174; no internal URLs or bundle contents. |

## Device construction

Work on a copy of the creation spec and commit only after successful resolution and validation. Replace class/image CD-ROM additions with requested devices. Preserve other devices. Iterate requested media in manifest order, reuse controllers by type and bus, reject occupied slots and duplicate ISO backings, and allocate unique negative keys. Add controllers before their dependent CD-ROMs. Keep ISO backing paths intact in placement; the synthetic placement disk remains independent.

## Placement and creation handoff

ISO-backed creation requests require both a host and a home datastore recommendation, even when the VM has a zone label or only one zone is available. Inspect the actual ConfigSpec for ISO backings so unrelated VMs retain the existing placement shortcuts. Group placement includes all media and requests home datastore recommendations when any member has an ISO. Parse datastore recommendations based on what was requested, rather than only on FastDeploy. Legacy ISO creation consumes the selected home datastore and host. Preserve an already-selected instance-storage host when checking its compatibility again.

## Test strategy

- Device construction: empty specs, implicit/explicit IDE/SATA, multiple media, existing disks/controllers, duplicate or missing images, key/slot safety, connection settings, idempotence, and rollback.
- Provider simulator: inspect placement and initial creation requests, including group placement where available, and confirm no subsequent CD-ROM add is needed.
- E2E: extend ISO lifecycle coverage to inspect powered-off creation and the host's access to the backing datastore; mark experimental until validated on a real Supervisor.

## Rollout / migration

`CDROMPlacement` defaults false and follows `status.supervisor.supports_vm_service_cdrom_placement.activated` on the `supervisor-capabilities` resource through the existing capability discovery path. There is no environment-variable override. The platform must advertise and activate the capability to enable this behavior; deactivation restores the previous creation path. No schema upgrade. Existing VMs continue through the established update reconcilers. The E2E scenario requires this capability.

## Complexity tracking

Keep this as a local reviewable change. Do not publish a spec-only branch or create external tickets during implementation. Ticket association and hardware validation remain explicit pre-merge follow-ups.
