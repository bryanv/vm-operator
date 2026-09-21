# Review: 009-primary-interface-dns-backfill

- **Reviewed artifacts**: `PLAN.md` (repo root, HEAD commit `3a9fa2443`), `.sdd/specs/009-primary-interface-dns-backfill/{spec.md,plan.md,tasks.md}`, `.sdd/INDEX.md`
- **Reviewed against**: `pkg/providers/vsphere/vmlifecycle/bootstrap.go`, `pkg/providers/vsphere/network/{bootstrap.go,gosc.go,netplan.go}`, `pkg/providers/vsphere/session/session_vm_update.go`, `pkg/providers/vsphere/vmlifecycle/{update_status.go,bootstrap_templatedata.go,bootstrap_linuxprep.go}`, `api/v1alpha6/virtualmachine_{network,bootstrap}_types.go`, `.sdd/memory/*.md`
- **Date**: 2026-09-21
- **Status of the change set under review**: planning artifacts only. No product code has been written yet.

## How to use this document

Each finding carries a **Verdict**, the **Evidence** that supports it (file:line plus what is at that line, so it can be verified with a single `sed -n`), the **Artifact** the fix lands in, and the **Required edit**.

Three verdict classes, and they are not interchangeable:

- **BLOCKER** — the plan as written produces incorrect behavior or cannot be verified. Fix before any code is written. Resolve these autonomously.
- **DECISION** — a semantic choice that changes user-visible contracts. **Do not resolve autonomously.** Surface the options to the spec author and wait. Recording the open question in `spec.md` as `[NEEDS CLARIFICATION: ...]` is the correct action here; picking an answer is not.
- **HOUSEKEEPING** — mechanical corrections. Resolve autonomously.

## What the plan gets right (do not redesign these)

The core approach is sound and should survive this review intact:

- Gating via a **capability** (not an `FSS_*` feature flag) sourced from the Capabilities CRD, mapped onto `pkgcfg.FeatureStates`. This matches how every capability since `SVAsyncUpgrade` is wired.
- The **legacy branch kept reachable** during rollout, with capability default off, so upgrade is a no-op until the capability is activated.
- **Positional primary selection** (first non-NoIPAM interface in spec order) rather than a new published API field. `BuildBootstraps` zips `devices[i]` with `spec.network.interfaces[i]` and nothing reorders the slice, so index order is a stable contract.
- The **per-VM annotation escape hatch**, kept internal in `pkg/constants` rather than published in `api/v1alpha6`.
- The identification of the two edit sites (`GetBootstrapArgs` and `InterfaceBootstrap`) and the observation that `UpdateNetworkStatusConfig` reflects the change into `status.network.config` automatically.

Do not rewrite these. The findings below are corrections and gaps, not a counter-proposal.

---

# Section A — Blockers

## A1. The new-path ConfigMap fetch silently drops DNS for vAppConfig-only and no-bootstrap VMs

**Verdict**: BLOCKER. This is a behavior regression for VMs the spec never mentions.

**Claim**: `plan.md` says to fetch the ConfigMap "**only when needed**: `!anyDHCP` and at least one of {CloudInit with `useGlobalNS` or `useGlobalSS`, GOSC} still has an unfilled value." That condition set omits every VM that is neither CloudInit nor GOSC. Under the capability, those VMs lose their global DNS entirely.

**Evidence**:

- `pkg/providers/vsphere/session/session_vm_update.go:1183` calls `vmlifecycle.GetBootstrapArgs(...)` unconditionally for every VM being reconciled. There is no bootstrap-provider branch upstream of it. This is the sole production call site.
- `pkg/providers/vsphere/vmlifecycle/bootstrap.go:315-324` fills `bsa.DNSServers` and `bsa.SearchSuffixes` from the ConfigMap today with no provider check (`isGOSC` only suppresses the *suffixes*, not the servers).
- Those two fields are consumed by paths that have nothing to do with CloudInit or GOSC:
  - `pkg/providers/vsphere/vmlifecycle/update_status.go:1056` — `hn, dn, ns, sd := args.HostName, args.DomainName, args.DNSServers, args.SearchSuffixes`, which populates `status.network.config.dns` for **every** VM.
  - `pkg/providers/vsphere/vmlifecycle/bootstrap_templatedata.go:38` (and :44, :50, :56, :62, :68) — `Nameservers: bsArgs.DNSServers`, feeding the vApp/template render data for all six API versions.
  - Those template funcs are publicly documented: `docs/concepts/workloads/guest.md:550` and `docs/tutorials/deploy-vm/vappconfig.md:24` both show `{{ (index .V1alpha6.Net.Nameservers 0) }}` as a supported vAppConfig property value.
- A **nil** bootstrap spec also reaches this code. `bootstrap.go:254-257` copies the spec, and both `isCloudInit` and `isGOSC` are false when `Spec.Bootstrap` is nil.

**Artifact**: `plan.md` — the "Design → `GetBootstrapArgs`" section, bullets 3 and 6. Also `spec.md`, which currently frames every goal in terms of CloudInit and GOSC only.

**Required edit**: Make the **VM-level** fill (`bsa.DNSServers` / `bsa.SearchSuffixes`) provider-agnostic. Fill when empty, gated by `anyDHCP` and — for CloudInit only — the `useGlobal*` flags. Do not gate it on the VM having a CloudInit or GOSC bootstrap. Note explicitly in `plan.md` that this **overrides** PLAN.md's "fetch only when needed" optimization: the optimization as stated is the source of the regression, so a downstream implementer must know it is being deliberately relaxed rather than accidentally ignored. Add an acceptance criterion to `spec.md` covering a vAppConfig-only VM and a nil-bootstrap VM retaining `status.network.config.dns` under the capability.

**Related quirk to record while you are here**: a nil bootstrap is treated as *non*-GOSC by `GetBootstrapArgs`, so it receives ConfigMap search suffixes — even though `DoBootstrap` later defaults exactly that VM to LinuxPrep (`pkg/providers/vsphere/vmlifecycle/bootstrap.go:94-108`, the "V1ALPHA1: We had always defaulted to LinuxPrep" block). So the invariant "GOSC never receives ConfigMap search suffixes" is true only for an *explicit* LinuxPrep or Sysprep spec. State this in `research.md` (see H8) so the legacy-mode characterization tests assert the real behavior rather than the idealized rule.

## A2. There are no existing unit tests for `GetBootstrapArgs`, so the plan's regression guard does not exist

**Verdict**: BLOCKER. The task ordering depends on a safety net that is absent.

**Claim**: `plan.md` ("Test strategy") and `tasks.md` T008 both say capability-off behavior is protected because "existing expectations in `pkg/providers/vsphere/vmlifecycle/bootstrap_test.go` pass unchanged." No such expectations exist. T006 ("move the existing backfill loop behind the legacy branch with no logic edits") would therefore be an unverified refactor of the single most intricate function in the change.

**Evidence**:

- `grep -rn "GetBootstrapArgs" --include='*_test.go' .` returns **no matches** (exit status 1). Verified against control cases in the same invocation: the identical pattern finds `UpdateNetworkStatusConfig` in `pkg/providers/vsphere/vmlifecycle/update_status_test.go` and `BootStrapCloudInit` in `pkg/providers/vsphere/vmlifecycle/bootstrap_cloudinit_test.go`, so the empty result is a real absence and not a shell-glob artifact.
- `pkg/providers/vsphere/vmlifecycle/bootstrap_test.go` covers only `IsPending`, `SanitizeConfigSpec`, `SanitizeCustomizationSpec`, and `DoBootstrap`.
- The only coverage of the ConfigMap reader is `pkg/providers/vsphere/config/config_test.go:105` (`Describe("GetDNSInformationFromConfigMap")`), which tests the reader in isolation, not the backfill that consumes it.

**Artifact**: `tasks.md` — phase ordering; `plan.md` — "Test strategy".

**Required edit**: Insert a new task **before** T006/T007 that writes characterization tests pinning today's behavior, and restate the test strategy as "add the regression guard, then split the branch" rather than "existing tests pass unchanged." Minimum cases for the characterization suite, each of which encodes behavior the new path must either preserve or knowingly change:

1. Multi-NIC CloudInit, no per-interface DNS: ConfigMap nameservers land on **every** interface, NoIPAM interfaces **included**.
2. DHCP interface present: that interface is skipped by the ConfigMap loop (`bootstrap.go:332`) but **still receives** the VM-level fallback from `InterfaceBootstrap` (see D4).
3. TKG VM (CAPI labels): ConfigMap search domains applied; non-TKG: not applied.
4. Explicit LinuxPrep and Sysprep: `bsa.DNSServers` filled from ConfigMap, `bsa.SearchSuffixes` **not**.
5. vAppConfig-only VM and nil-bootstrap VM: `bsa.DNSServers` and `bsa.SearchSuffixes` both filled (this is the A1 case).
6. ConfigMap absent: `IgnoreNotFound` tolerates it and returns empty args, no error (`bootstrap.go:310`).
7. ConfigMap contains the `<worker_dns>` placeholder: the error from `pkg/providers/vsphere/config/config.go:143-145` propagates out of `GetBootstrapArgs` as a hard failure.

**Label convention**: use `testlabels.API`, not `testlabels.Controller`. Neighboring provider tests use the former — see `pkg/providers/vsphere/network/netplan_test.go:21` (`Describe("Netplan", Label(testlabels.API), ...)`) and `pkg/providers/vsphere/network/bootstrap_test.go:1293`. `plan.md` currently says "`testlabels.Controller`-style"; correct it.

## A3. "GOSC has no per-interface concept" is factually wrong

**Verdict**: BLOCKER for the rationale, not for the outcome. Left uncorrected it will mislead an implementer into wiring the `useGlobal*` flags into the Sysprep adapter path.

**Claim**: `plan.md` justifies skipping `useGlobal*` for GOSC by asserting "GOSC has no per-interface concept and no `useGlobal*` fields." The second half is true. The first half is false.

**Evidence**:

- `pkg/providers/vsphere/network/gosc.go:19` — `DnsServerList: b.Nameservers`, set **per adapter** inside the loop over bootstraps, with the in-code comment "Per-adapter is only supported on Windows. Linux only supports the global and ignores this field."
- `api/v1alpha6/virtualmachine_network_types.go:162-174` — the per-interface `Nameservers` godoc states "this feature is available only with the following bootstrap providers: CloudInit **and Sysprep**."

**Artifact**: `plan.md` — "Design → `GetBootstrapArgs`", the `useGlobalNS`/`useGlobalSS` bullet. Also `spec.md` — the non-goal reading "nor GOSC per-adapter configuration other than DNS", which is confusing for the same reason (per-adapter DNS is exactly what `gosc.go` writes).

**Required edit**: Restate the rationale as: *GOSC per-adapter DNS is populated from `Bootstrap.Nameservers`, which is only ever filled from `interfaceSpec.Nameservers` for GOSC VMs (the VM-level fallback in `InterfaceBootstrap` is CloudInit-gated at `pkg/providers/vsphere/network/bootstrap.go:216`). Therefore no global backfill reaches a GOSC adapter today, and the `useGlobal*` flags remain CloudInit-only — not because GOSC lacks per-adapter DNS, but because GOSC never participates in the global fallback.* Rewrite the `spec.md` non-goal to say the change does not alter how per-adapter DNS is derived for Sysprep.

---

# Section B — Decisions required from the spec author

**Do not resolve these autonomously.** Each changes a user-visible contract. Record each as a `[NEEDS CLARIFICATION: ...]` in `spec.md` with the options laid out, and leave the dependent tasks blocked in `tasks.md` per `sdd-standards.md`.

## D4. Any-DHCP suppression treats explicit VM-level nameservers the same as infrastructure defaults

**Verdict**: DECISION.

**Claim**: PLAN.md rule 2 ("any interface is DHCP4/DHCP6 ⇒ no global backfill at all, neither the VM-spec-level fallback nor the ConfigMap fallback") is recorded as user-confirmed. It is a larger change than the surrounding text suggests, and it lands asymmetrically across providers.

**Evidence**:

- Today the DHCP skip exists **only** in the ConfigMap loop: `pkg/providers/vsphere/vmlifecycle/bootstrap.go:332` (`if b.DHCP4 || b.DHCP6 { continue }`).
- The VM-level per-interface fallback in `pkg/providers/vsphere/network/bootstrap.go:224-234` has **no** DHCP check at all, so a DHCP CloudInit interface receives `spec.network.nameservers` today.
- For GOSC, `bsa.DNSServers` is assigned from `networkSpec.Nameservers` at `pkg/providers/vsphere/vmlifecycle/bootstrap.go:276`, before any DHCP consideration. Under the new rule, a GOSC VM with one DHCP NIC keeps its explicit VM-level DNS (that assignment is outside the backfill block), while a CloudInit VM in the same shape loses it. That asymmetry may be intended, but it is currently unstated.

**Artifact**: `spec.md` — the any-DHCP goal; `plan.md` — rule 2.

**Options to put to the author**:

- **(a) As written.** Any DHCP suppresses both layers for CloudInit. Simple rule, but it discards a value the user typed explicitly into `spec.network.nameservers`, and the field's own godoc promises it will be used.
- **(b) Suppress only infrastructure-derived values.** Any DHCP skips the ConfigMap fallback; explicit `spec.network.nameservers` still lands on the primary. Preserves the documented meaning of the published field and keeps CloudInit and GOSC symmetric.

Whichever is chosen, `spec.md` must state the CloudInit/GOSC asymmetry explicitly rather than leaving it implicit.

## D5. Expanding the `useGlobal*` flags changes the documented meaning of two published API fields

**Verdict**: DECISION, with a consequential correction attached (see H-note below, which applies regardless of the decision).

**Claim**: PLAN.md rule 4 redefines `useGlobalNameserversAsDefault` / `useGlobalSearchDomainsAsDefault` to also suppress the ConfigMap fallback. Today they gate only the VM-level fallback. Both `spec.md` and `plan.md` assert "no CRD regeneration"; that assertion becomes false the moment the godoc changes.

**Evidence**:

- `api/v1alpha6/virtualmachine_bootstrap_types.go:133-138` — "UseGlobalNameserversAsDefault will use the global nameservers specified in **the NetworkSpec** as the per-interface nameservers when the per-interface nameservers is not provided." The ConfigMap is not mentioned. Same shape at :143-148 for search domains.
- These flags are read only at `pkg/providers/vsphere/network/bootstrap.go:218-219`; the ConfigMap block at `bootstrap.go:308-346` never consults them.
- Field descriptions are baked into generated manifests: `config/crd/bases/vmoperator.vmware.com_virtualmachines.yaml`, `config/crd/bases/vmoperator.vmware.com_virtualmachinereplicasets.yaml`, and `docs/ref/api/v1alpha6.md` all carry the current text.

**Artifact**: `spec.md` (goal + the "no CRD regeneration" non-goal), `plan.md` ("API / CRD strategy" and "Project structure"), `tasks.md` (a regeneration task).

**Options to put to the author**:

- **(a) Expand as planned.** Then the godoc must be rewritten, `make generate-manifests` run, and the two CRD YAMLs plus `docs/ref/api/v1alpha6.md` added to the project structure and task list. The regeneration is description-only (no schema change), so it is not an API break — but "no CRD regeneration" is still wrong as written and must be corrected in both artifacts.
- **(b) Leave the flags alone.** The ConfigMap fallback stays ungated by them. Smaller blast radius, no doc drift, at the cost of a user who set the flag to `false` still receiving infrastructure DNS.

## D6. All-NoIPAM VMs: does "no primary" suppress the VM-level DNS too?

**Verdict**: DECISION.

**Claim**: PLAN.md rule 6 says "No backfill occurs when no primary exists." Read literally, an all-NoIPAM VM loses not just per-interface DNS but also `bsa.DNSServers` — which means the GOSC guest-wide DNS list and the `status.network.config.dns` block both go empty. The `spec.md` goals only ever talk about *per-interface* backfill, so the two artifacts disagree.

**Evidence**:

- `spec.md` goal: "MUST backfill the global DNS nameservers and search domains onto at most **one** interface" — scoped to interfaces.
- `spec.md` acceptance criterion for NoIPAM only asserts what interfaces A and B receive; it says nothing about `status.network.config.dns`.
- `pkg/providers/vsphere/vmlifecycle/update_status.go:1056-1072` derives the VM-level DNS status block from `args.DNSServers` / `args.SearchSuffixes`, independent of any interface.

**Recommended framing for the author**: scope primary selection to **CloudInit per-interface writes only**, and let the VM-level fill depend only on `anyDHCP` (plus the `useGlobal*` flags per D5). That keeps A1's provider-agnostic fill coherent and avoids an all-NoIPAM VM reporting no DNS at all. Confirm before implementing.

## D7. SLAAC-only interfaces count as "static" for both primary selection and any-DHCP

**Verdict**: DECISION (low stakes, but it must be written down).

**Claim**: An interface with `AcceptRA: true`, `DHCP6: false`, and no static addresses is dynamically addressed in practice, yet the plan's rules classify it as neither DHCP nor NoIPAM — so it is eligible to be the primary and does not trigger the any-DHCP skip.

**Evidence**: `pkg/providers/vsphere/network/bootstrap.go:403-427` — in the VPC path, `raActive` and `dhcp6Active` are tracked independently; `initial.AcceptRA = raActive` can be set with `initial.DHCP6 = false`, and `NoIPAM` is only set when `!ipv6Dynamic`. The `Bootstrap.AcceptRA` godoc at :57-60 confirms "AcceptRA alone means SLAAC-only."

**Artifact**: `spec.md` — primary-interface definition and the any-DHCP goal.

**Options**: (a) SLAAC-only is static — matches today's behavior, no code change. (b) SLAAC-only is dynamic — it joins the any-DHCP skip and becomes primary-ineligible. Either way the spec must say which, because "the first interface that is not NoIPAM" is silent on it.

## D12. The escape-hatch annotation is user-settable as specified

**Verdict**: DECISION.

**Claim**: `plan.md` proposes `LegacyGlobalDNSBackfillAnnotation = "vmoperator.vmware.com/legacy-global-dns-backfill"`. Any DevOps user with VM write access can set it. Neither spec nor plan names who is allowed to.

**Evidence**:

- `webhooks/virtualmachine/validation/virtualmachine_validator.go:2635` (`validateAnnotation`) restricts only an explicit list — cluster-module keys, `InstanceIDAnnotation`, `FirstBootDoneAnnotation`, `RestoredVMAnnotation`, `FailedOverVMAnnotation`, `ImportedVMAnnotation`, the `anno2extraconfig` set, and the `CreatedAt*`/`UpgradedTo*` keys. An unlisted annotation is unrestricted.
- The repo already has a convention for admin-only keys: the `vmoperator.vmware.com.protected/` prefix, e.g. `pkg/constants/constants.go:142` (`SkipValidationAnnotationKey`), :160, :165.

**Artifact**: `spec.md` — the escape-hatch user story currently says only "a VM carries the internal legacy-backfill annotation"; `plan.md` — the constant name.

**Options**: (a) Admin-only — use the `.protected/` prefix and add a webhook check. (b) Tenant-settable — keep the plain prefix and say so explicitly in the spec, naming the persona (DevOps user) who may set it.

---

# Section C — Housekeeping

## H8. Delete the root `PLAN.md`; move its inventory into the missing `research.md`

**Verdict**: HOUSEKEEPING.

**Evidence**: `.sdd/memory/constitution.md` — "All SDD artifacts live under `.sdd/` at the repository root. There is no other 'specs' or 'memory' tree." The root `PLAN.md` added by `3a9fa2443` duplicates the spec and plan, and its "Files to modify" and "Steps" sections still point at `.sdd/specs/008-primary-interface-dns-backfill/` — but `008` is `nic-unit-numbers`; this feature is `009`.

**Artifact**: delete `PLAN.md`; create `.sdd/specs/009-primary-interface-dns-backfill/research.md`.

**Required edit**: PLAN.md's "Current behavior inventory" is genuinely valuable and has no home in the SDD artifacts — `sdd-standards.md` says `research.md` SHOULD exist and is where investigation and prior art belong. Move the inventory there, corrected per A1 and A3, and add the nil-bootstrap/LinuxPrep-default quirk from A1. Then delete `PLAN.md`. Note that `008-nic-unit-numbers/` already ships a `research.md` and `model.md` as precedent.

## H9. `spec.md` contains implementation detail that `sdd-standards.md` forbids

**Verdict**: HOUSEKEEPING.

**Evidence**: `sdd-standards.md`, "`spec.md` — feature specification": "Forbidden content: technology choices, package paths, function signatures, Go code," and anti-pattern #1. The current `spec.md` names `config.GetDNSInformationFromConfigMap`, refers to "the `api/` package", and asserts "no ConfigMap lookup is performed" as an acceptance criterion — which is not observable via kubectl, vCenter UI, or an API response, and so fails the standard's observability requirement for acceptance criteria.

**Required edit**: Move the function reference to `plan.md` (where it already appears under "Reuse"). Restate the DHCP criterion in observable terms: no global DNS appears in the rendered netplan, in the GOSC customization spec, or in `status.network.config`. Keep "no ConfigMap lookup" as a unit-test assertion in `plan.md`'s test strategy, where it belongs.

## H10. User-facing documentation needs a task

**Verdict**: HOUSEKEEPING. Neither `plan.md`'s project structure nor `tasks.md` mentions `docs/`.

**Evidence**:

- `docs/concepts/workloads/vm.md:949-954` documents the derivation table for `status.network.config.dns.*` and `status.network.config.interfaces[].dns.*`. It is **already inaccurate**: it claims per-interface DNS comes only "From the corresponding `spec.network.interfaces[].nameservers[]`", omitting both the VM-level and ConfigMap fallbacks that exist today. The capability makes the drift worse.
- `docs/concepts/workloads/vm.md:749-750` marks `spec.network.nameservers` as LinuxPrep/Sysprep-only with no Cloud-Init checkmark, which understates the current behavior.
- In-code doc that moves with the change: `pkg/providers/vsphere/network/bootstrap.go:66-74` — the `Nameservers`/`SearchDomains` field comments describe the CloudInit VM-level fallback that H14/T005 relocates.

**Required edit**: Add a Phase Final task covering the `vm.md` derivation table and the `Bootstrap` struct comments. If D5 resolves to (a), fold the API godoc regeneration into the same task.

## H11. The capability key is not yet owned, and the E2E plan overstates what the suite can assert

**Verdict**: HOUSEKEEPING.

**Evidence**:

- Capability keys are Supervisor-owned strings; `pkg/config/capabilities/capabilities.go:34-154` is a list of names defined elsewhere. `CapabilityKeyPrimaryInterfaceDNSBackfill = "supports_primary_interface_dns_backfill"` is invented by this plan and must be confirmed with the platform before it can ever read `true`.
- E2E tests consume these names from a separate constant block: `test/e2e/vmservice/consts/*.go:41-50` (`VMGroupsCapabilityName`, `VirtualMachineConfigPolicyCapabilityName`, …), gated through `skipper.SkipUnlessSupervisorCapabilityEnabled` (`test/e2e/vmservice/skipper/skipper.go:48`). `plan.md` omits the consts entry.
- The E2E assertions the plan describes have no precedent in the suite. `grep` for `Nameservers|SearchDomains` across `test/e2e/**/*.go` finds only vApp template-function checks (`vm_guestcustomization.go:341-360`) and one coarse assertion, `virtualmachinelcm.go:712-715`, which asserts merely that `dns.Nameservers` is non-empty. Nothing asserts *per-interface* DNS. Nothing in `test/e2e` references NoIPAM at all.
- The multi-NIC fixture `test/e2e/fixtures/yaml/vmoperator/virtualmachines/v1a2vm-multi-network.yaml.in` is **v1alpha2**, hardcodes exactly two interfaces with no DNS or bootstrap DNS fields, and has one consumer: `vm_vpcnetworking.go:284`.

**Required edit**: Mark the capability name provisional in `plan.md` and add a task to confirm it with the platform team. Add `test/e2e/vmservice/consts/` to the project structure. State in `plan.md` that the E2E will skip until the capability ships, so a green run is not evidence of coverage. Scope the NoIPAM assertion as conditional on testbed support, and add a task for the new or extended multi-NIC fixture, since the existing one cannot express the scenario.

## H13. The rollout section describes only the Cloud-Init half

**Verdict**: HOUSEKEEPING.

**Claim**: `plan.md` says "cloud-init only re-applies network config on first boot / instance-id change, so running guests are mostly insulated." True, and it is also only half the picture: the two providers re-apply on completely different triggers.

**Evidence**:

- The bootstrap chain runs for powered-on VMs, not just at create: `pkg/providers/vsphere/session/session_vm_update.go:348` calls `reconcileNetworkAndGuestCustomizationState` from the shared powered-off/powered-on update path.
- CloudInit: `DoBootstrap` compares the ConfigSpec hash (`pkg/providers/vsphere/vmlifecycle/bootstrap.go:154-188`, `BootstrapHashConfigSpecAnnotationKey`) and reconfigures as soon as the rendered guestinfo changes — so the ExtraConfig rewrite happens on the next reconcile after the capability flips.
- GOSC: `pkg/providers/vsphere/vmlifecycle/bootstrap_linuxprep.go:32` — `if !vmCtx.IsOffToOn() { return nil, nil, nil, nil }`. A LinuxPrep VM re-customizes only on an off-to-on transition, so a running GOSC VM sees nothing until it is power-cycled.

**Required edit**: Split the rollout note by provider, and state that `status.network.config` updates immediately for both (it is written from `bootstrapArgs` at `session_vm_update.go:1195`, before `DoBootstrap` runs) even when the guest has not yet been re-customized. That divergence between reported intent and guest state is the thing an operator will file a bug about.

## H14. The two-place predicate is a correctness coupling worth reconsidering — after A2

**Verdict**: HOUSEKEEPING (design note, not a blocker).

**Claim**: T005 gates the fallback inside `InterfaceBootstrap`, which forces the `network` package to read both the capability and the VM annotation, and makes new-mode correctness in `GetBootstrapArgs` depend on `InterfaceBootstrap` having *not* filled `Nameservers` — a silent coupling across two packages with no compile-time link.

**Evidence**: `pkg/providers/vsphere/network/bootstrap.go:215-234` performs the fallback; `pkg/providers/vsphere/vmlifecycle/bootstrap.go:290-346` performs the backfill. `InterfaceBootstrap` currently ignores its `context.Context` entirely (the parameter is named `_` at `bootstrap.go:109`), so gating it means threading config into a function that today is pure with respect to config. There is exactly one production chain through both (`session_vm_update.go:1174` then :1183).

**Required edit**: Record the alternative in `plan.md` — do **all** global defaulting in `GetBootstrapArgs` for both modes and leave `InterfaceBootstrap` handling only `interfaceSpec` values. It removes the cross-package coupling and the duplicated predicate. It is a larger diff, so only take it once A2's characterization tests exist to prove legacy output is unchanged. Note that `plan.md` currently leaves this open ("put the predicate in `network` if both need it, or duplicate the two-line check") — pick one.

## H15. `tasks.md` nits

**Verdict**: HOUSEKEEPING.

- **T004 offers two alternatives** ("add the shared predicate ... or the equivalent two-line local check"). `sdd-standards.md` requires each task to name the exact files it touches. Pick one, per H14.
- **T017 is already complete.** `.sdd/INDEX.md` was updated in the same HEAD commit (`3a9fa2443`) with the `009` row. Check the box or drop the task.
- **Every task is tagged `[vmop-TBD]`** and both `spec.md` and `plan.md` carry `Epic: TBD`. Per `constitution.md` and `sdd-standards.md` anti-pattern #11, a spec merging with `Epic: TBD` is a constitutional violation. This is already tracked as T016 and as the spec's sole open question; it is listed here only so the downstream implementer does not treat the `TBD`s as an oversight to invent values for. **Do not fabricate ticket numbers.**

## H16. The three artifacts use hard line wrapping, which the constitution forbids

**Verdict**: HOUSEKEEPING.

**Evidence**: `constitution.md`, "Markdown" — "Markdown files should not use hard line wrapping. Do not wrap lines; allow IDEs to do it for the user if that is their wish." The exception is fenced code blocks only. `spec.md`, `plan.md`, and `tasks.md` as committed are all hard-wrapped at roughly 95 columns.

**Required edit**: Unwrap prose lines in all three when they are next edited. Keep wrapping inside fenced blocks.

---

# Do-not-change list

A downstream model with initiative should leave these alone. They were reviewed and are correct:

1. **Capability-based gating** sourced from the Capabilities CRD, read via `pkgcfg.FromContext(ctx).Features.*`, default off. Do not convert this to an `FSS_*` environment feature flag. Only the name of the key is in question (H11).
2. **Positional primary selection** (first non-NoIPAM in `spec.network.interfaces` order). Do not add a `spec.network.primaryInterface` API field — `plan.md`'s complexity-tracking table already rejected it for good reason.
3. **The internal annotation escape hatch** living in `pkg/constants` rather than `api/v1alpha6`. Only its prefix and who may set it are open (D12).
4. **Keeping the legacy branch reachable** rather than deleting the old behavior outright, and the follow-up GA spec that removes it (T019).
5. **The choice of edit sites**: `GetBootstrapArgs` and `InterfaceBootstrap`. H14 questions the split between them, not the fact that these are the right two functions.
6. **The existing precedence within an interface** — an interface that specifies its own nameservers is never overridden. Every proposal here preserves that.
