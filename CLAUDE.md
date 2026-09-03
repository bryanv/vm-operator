# VM Operator — Claude Code Guidelines

This project follows Spec-Driven Development (SDD). The authoritative guidelines live in `.sdd/memory/`. This file is a Claude Code proxy that maps those rules into the contexts where they apply, following the same pattern as `.cursor/rules/*.mdc`.

> **Do not duplicate guidance here.** Update `.sdd/memory/*.md` instead; this file, the `.claude/rules` proxies, and the `.cursor` rules all stay current automatically via their `@` imports.

---

## SDD index

Before starting any non-trivial task, read `.sdd/INDEX.md`. If the files you are touching correspond to an in-progress spec, read that spec's `spec.md` and `plan.md` before writing code — the spec is the acceptance criteria.

---

## Always-active rules

@.sdd/memory/constitution.md

@.sdd/memory/commit-message-standards.md

@.sdd/memory/pull-request-standards.md

@.sdd/memory/e2e-sync-with-changes.md

---

## Path-scoped rules

The rules below are **not** always-loaded — they live in `.claude/rules/` and load only when you touch a matching file, mirroring the `globs:` in the `.cursor/rules/*.mdc` proxies. Each is a thin proxy that `@`-imports the authoritative file in `.sdd/memory/`.

| Rule file | Loads for | Proxies |
|-----------|-----------|---------|
| `.claude/rules/architectural-standards.md` | `**/*.go` | `.sdd/memory/architectural-standards.md` |
| `.claude/rules/operator-best-practices.md` | `controllers/**/*.go`, `pkg/providers/**/*.go`, `pkg/errors/**/*.go`, `pkg/util/kube/cource/**/*.go`, `services/**/*.go` | `.sdd/memory/operator-best-practices.md` |
| `.claude/rules/testing-standards.md` | `**/*_test.go` | `.sdd/memory/testing-standards.md` |

If you are reasoning about one of these areas without having opened a matching file, read the corresponding `.sdd/memory/*.md` directly.
