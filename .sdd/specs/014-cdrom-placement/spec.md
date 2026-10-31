# Feature Specification: CD-ROM Placement and Creation

- **Feature branch**: `bryanv/use-cdrom-for-placement`
- **PR target**: `vmware-tanzu/vm-operator`
- **Created**: 2026-10-31
- **Status**: Draft
- **Epic**: TBD
- **Incident**: vmop-11174

## Goals

- Placement MUST account for every requested ISO backing before selecting a zone, datastore, and host, for individual VMs and VM groups.
- VM creation MUST include the requested CD-ROMs and their required controllers so attaching installation media does not require a subsequent reconfiguration.
- Creation MUST honor the selected host and preserve desired connection and guest-control settings.
- Invalid or unavailable ISO references MUST fail before placement or VM creation.

## Non-goals

- Relocating already-created VMs whose ISO is inaccessible, copying ISO files across zones, changing the public API, or changing powered-on device update semantics.
- Replacing the content library service or redesigning unrelated disk and controller reconciliation.

## User stories / acceptance criteria

### US1: DevOps user creating a VM with installation media

The following behavior is enabled when the Supervisor advertises the activated `supports_vm_service_cdrom_placement` capability. Without activation, the existing placement and post-create attachment behavior is retained.

1. Given ISO media accessible only from a subset of eligible hosts, placement considers that media and does not create the VM on an incompatible host.
2. Given multiple CD-ROMs, all their backing paths participate in placement and all devices exist in the initial creation request, with unique device keys and controller slots.
3. Given explicit IDE or SATA placement, the initial VM configuration uses that bus and unit, reusing existing controllers and creating missing required controllers.
4. Given automatic controller placement, available IDE/SATA slots are reused and additional SATA controllers are created when necessary.
5. Given a powered-off newly created VM, connected media starts connected at power-on without setting its runtime connected state prematurely.
6. Given an invalid image, a duplicate backing, or an occupied explicit slot, creation fails with actionable context before submitting a partial configuration.
7. Given a VM group, each member's requested media participates in group placement.
8. Given no requested CD-ROMs, existing device construction is preserved. VMs without ISO backings retain their existing placement shortcuts.

## Open questions

- [NEEDS CLARIFICATION: Associate the incident with an epic and implementation stories before merge; Jira is unavailable in this workspace session.]
