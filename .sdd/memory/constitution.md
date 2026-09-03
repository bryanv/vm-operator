# Constitution: vm-operator Spec-Driven Development

- **Repository**: [`github.com/vmware-tanzu/vm-operator`](https://github.com/vmware-tanzu/vm-operator)
- **Last updated**: 2026-09-02
- **Applies to**: All feature specs under `.sdd/specs/`

---

## Project identity

`vm-operator` (`github.com/vmware-tanzu/vm-operator`) implements VM Operator on vSphere Supervisor. It is a **multi-module** repository: the root `go.mod` is the main binary module and several sub-directories each have their own `go.mod`. All modules share the same git history. See [`architectural-standards.md`](./architectural-standards.md) for the full project structure and module listing.

---

## How to use this constitution

This document defines the **non-negotiables** that govern every change in this repository. Detailed guidance on _how_ to satisfy them lives in the companion files under `.sdd/memory/`, indexed with one-line descriptions in [`.sdd/INDEX.md`](../INDEX.md). When a section below references a companion file, treat that file as authoritative for the details. The constitution states **what is required**; the companion files state **how to do it**.

`.cursor/rules/*.mdc` and `.claude/rules/*.md` are thin proxies — each points at the matching `.md` here so that humans and AI assistants read the same source of truth.

---

## Tickets and WIKI links

Due to Broadcom rules, internal links are disallowed in upstream repositories. To that end, any internal tickets or WIKI links should not be used in this repository:

* Instead, the prefix `vmop` is used in place of our internal JIRA project. Therefore, an internal ticket `XXX-123` becomes `vmop-123`.
* In place of a direct WIKI URL, just reference the page ID with the prefix `WIKI page`, ex. `WIKI page 123456`.
* JIRA field IDs (Epic Link, Acceptance Criteria), the spec ↔ epic and task ↔ story mapping, and the design-docs wiki page are specified in [`sdd-standards.md`](./sdd-standards.md) "Tickets and wiki links".

---

## Spec-Driven Development (SDD)

All non-trivial work in this repository follows a Spec-Driven Development workflow rooted at `.sdd/`. The intent is that **specifications drive code**, not the other way around. See [`sdd-standards.md`](./sdd-standards.md) for the full workflow, templates, artifact contracts, and the amendment process.

### Non-negotiables for SDD

- All SDD artifacts live under `.sdd/` at the repository root. There is no other "specs" or "memory" tree.
- Repository-wide rules live in `.sdd/memory/*.md`. Per-feature artifacts live in `.sdd/specs/NNN-slug/`.
- Each feature directory **MUST** contain `spec.md`, `plan.md`, and `tasks.md`. It **SHOULD** contain `research.md`; it **SHOULD** contain `model.md` when the feature introduces or changes a data model / API.
- Feature directory names follow `NNN-slug`, where `NNN` is a zero-padded auto-incrementing scalar starting at `000`. The scalar expands beyond three digits when needed (`1000-...`).
- AI-assistant-specific rule files (e.g. `.cursor/rules/*.mdc`) **MUST** be proxies that link back into `.sdd/memory/*.md`. Do not duplicate guidance across the two trees.
- New features that materially affect product behavior **MUST** ship with their spec, plan, tasks, and (if applicable) model artifacts in the same change set as the code. Code-only PRs that bypass `.sdd/specs/` are reserved for trivial fixes (see [`sdd-standards.md`](./sdd-standards.md) for the exemption list).
- The constitution is **immutable per change**: a PR that needs to bend a constitutional rule must amend the constitution in the same PR with a documented rationale, and call that amendment out in the PR description.
- An SDD spec should be related to an epic ticket.
- All SDD tasks should be related to story or sub-task tickets.

---

## API compatibility

- The default API group for this repository is `vmoperator.vmware.com`. These types are consumed by controllers, webhooks, and external partners, all within this same repo. Breaking changes (field removal, rename, type change) require a version bump and a conversion webhook. Additive changes are *not* safe once an API has shipped due to the way Kubernetes handles `UPDATE` operations. See <https://github.com/kubernetes/kubernetes/issues/111703> for more information on this topic.
- Every new CRD must include `+kubebuilder:object:root=true`, a `+groupversion` doc.go entry, a `// +groupName:` marker, and deepcopy generated via `make generate-go`.
- CRD manifests are generated under `config/crd/`; they are **checked in** (not generated at deploy time) and must be regenerated with `make generate-manifests`.
- There are also some external APIs maintained in this repository:
  - By default these live under the `external/` directory.
  - Each of them belong to their own Go module.
  - Their manifests may be generated with `make generate-external-manifests`.

## Controller design

The non-negotiables below are constitutional; for the canonical reconcile-loop template, watch setup, error semantics, and channel-source patterns see [`operator-best-practices.md`](./operator-best-practices.md).

- Controllers are **thin**: reconcile loops live in `controllers/`; all business logic lives in `pkg/`.
- No controller may call vSphere APIs directly; use the provider abstraction under `pkg/providers/vsphere/`.
- Controllers must track `status.observedGeneration` and set a `Ready` condition.
- Fan-out to child objects uses `controllerutil.CreateOrPatch` by default; ownership is set via `controllerutil.SetControllerReference` unless the object is cluster-scoped, in which case use labels for ownership tracing. When multiple parents fan-in write to the same *list-typed* field on a shared child (e.g., owner references, a status list each parent upserts an entry into), patch with `client.MergeFromWithOptimisticLock` instead and skip the write when nothing changed — a plain merge patch replaces list fields wholesale with no conflict detection and can silently drop a writer's contribution, whereas the optimistic lock makes a concurrent write fail with a conflict that the next reconcile retries. Disjoint fields or a shared map keyed per writer are unaffected. See [`operator-best-practices.md`](./operator-best-practices.md#fan-out-to-child-objects-patch-vs-createorupdate).
- Controllers for API groups other than `vmoperator.vmware.com` should not be placed directly in the `controllers/` directory.

## Webhooks

- Admission webhooks live in `webhooks/`; share validation logic with unit tests via unexported validator types (not embedded in the webhook handler).
- `+kubebuilder:webhook` markers drive code generation; registration lives in `webhooks/suite_test.go` and `main.go`.
- CEL validation is preferred for simple structural rules; Go validation for complex, cross-field, or vSphere-data-dependent rules.
- Webhooks for API groups other than `vmoperator.vmware.com` should not be placed directly in the `webhooks/` directory.

## Testing

The non-negotiables below are constitutional; for unit and integration patterns see [`testing-standards.md`](./testing-standards.md), and for the E2E suite see [`e2e-testing.md`](./e2e-testing.md) and [`e2e-sync-with-changes.md`](./e2e-sync-with-changes.md).

- **One test file** per package: `<package>_test.go` (external `_test` package).
- **One suite bootstrap** per package: `<package>_suite_test.go` containing only the `TestXxx(t *testing.T)` entry-point that calls `RunSpecs`.
- Do **not** use the old `_unit_test.go` / `_intg_test.go` split — all tests live in `_test.go` and are differentiated **only** by Ginkgo `Label()` decorators.
- Labels come from `pkg/constants/testlabels` and are applied to top-level `Describe` (or `Context`) blocks so they propagate to every spec inside. A pure unit test carries only the category label (e.g. `testlabels.Controller`) and no infrastructure label; tests requiring `vcsim` add `testlabels.VCSim` and tests requiring `envtest` add `testlabels.EnvTest`.
- Any change observable on a Supervisor cluster **must** have E2E coverage in the same PR per [`e2e-sync-with-changes.md`](./e2e-sync-with-changes.md).

## Markdown

- Markdown files should not use hard line wrapping. Do not wrap lines; allow IDEs to do it for the user if that is their wish.
  - Exception: fenced code blocks (` ``` ` … ` ``` `). Lines inside a code fence should still be kept to ≤ 80 characters where practical.

## Repository layout and Go modules

`vm-operator` is a **multi-module** repository. The root `go.mod` is the main binary module; every `go.mod` nested below it is an independent Go module that is imported by the root (or by tooling) via `replace` directives in the workspace. For the package-level organization principles, import aliases, and naming conventions, see [`architectural-standards.md`](./architectural-standards.md).

The authoritative module list is whatever `find . -name go.mod -not -path './vendor/*'` reports; the directory layout is whatever `ls` shows.

## Coding style

Wherever a rule is also enforced by `golangci-lint`, [`.golangci.yml`](../../.golangci.yml) is the **source of truth** — import aliases (`importas`), import grouping (`goimports`), and forbidden imports (`depguard`) are enforced there and documented in [`architectural-standards.md`](./architectural-standards.md). The non-negotiables below are the ones the linter does **not** catch:

- `+optional` / `+required` markers on every struct field; `omitempty` JSON tags on optional fields.
- Resource names must be DNS-subdomain safe (`^[a-z][a-z0-9-]{0,61}[a-z0-9]?$`).
- Managed object IDs (`spec.id`) are immutable after create. Enforce this per the CEL/Go split in the Webhooks section above: a plain `self == oldSelf` transition rule for the unconditional case, a webhook only when the immutability rule is conditional or cross-field.
