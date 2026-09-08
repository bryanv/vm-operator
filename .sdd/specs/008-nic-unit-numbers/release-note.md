# Release Note Draft (T025)

> Paste into the PR description's release-note section when the PR is opened.

## Release note

```markdown
### Added

- `spec.network.interfaces[i].unitNumber`: an optional PCI unit number
  (7-16) for a network interface's virtual device. A value the user sets
  is honored at device creation; values the user omits are recorded from
  observed hardware once the VM has been reconciled, and assigned at
  admission for interfaces added after that — the same way disk and
  CD-ROM unit numbers behave. Once set, the unit number is the
  interface's stable identity for its underlying vSphere device and is
  immutable to users while the VM is powered on.
- `status.network.interfaces[i].unitNumber`: the observed PCI unit
  number of the interface's virtual device (informational; only present
  for interfaces reported by VMware Tools).
- The feature is gated behind the opt-in
  `supports_VM_service_network_unit_numbers` Supervisor capability and
  is disabled by default.

### Notable behaviors to be aware of

- **Changing an existing interface's `unitNumber` (powered off) is a
  NIC-replacement operation, not a slot relocation.** The underlying
  vSphere device is not moved: a new device is created (new device key,
  and — for an auto-assigned MAC — a new MAC address, and therefore
  possibly a new DHCP-assigned IP), and the old device is removed.
  Expect a brief connectivity interruption, the same as deleting and
  re-adding the interface. Once an interface carries a unit number,
  changing its network (or its MAC/ExternalID where the provider
  specifies them) likewise replaces the device rather than editing it
  in place; a device-preserving edit is planned as a follow-on.
  Un-numbered interfaces are unaffected.
- **Network changes on a powered-on VM are admitted but applied at the
  next power-off** (a new interface, or a renumbered/removed one). This
  matches today's behavior for other NIC device changes; reflecting NIC
  hardware changes while powered on is planned as a follow-on.
- **Enabling the capability marks every existing VM "not upgraded"
  until its next reconcile** (the schema upgrade records each VM's
  existing NIC slots into its spec first). During that window,
  user edits to `spec.network.interfaces` are rejected and the
  disk/CD-ROM mutators pause, cluster-wide. This is inherent to adding
  a feature-version bit and clears once VMs reconcile.
- **Old-version clients that drop the conversion-data annotation now
  fail loudly instead of silently wiping the field**: an update from an
  annotation-lossy old API client that drops a set `unitNumber` fails
  with "cannot change unit number while VM is powered on" rather than
  silently clearing it.
```
