# VM Operator — Claude Code Guidelines

This project follows Spec-Driven Development (SDD). The authoritative guidelines live in `.sdd/memory/`. This file is a Claude Code proxy that maps those rules into the contexts where they apply.

> **Do not duplicate guidance here.** Add or change rules in `.sdd/memory/*.md`; this file only points at them. `@path` inlines a file into every session, so it is reserved for rules that apply to every change; file-scoped rules are plain links, read on demand.

---

## SDD index

Before starting any non-trivial task, read `.sdd/INDEX.md`. If the files you are touching correspond to an in-progress spec, read that spec's `spec.md` and `plan.md` before writing code — the spec is the acceptance criteria.

---

## Always-active rules

@.sdd/memory/constitution.md

@.sdd/memory/commit-message-standards.md

@.sdd/memory/pull-request-standards.md

@.sdd/memory/e2e-sync-with-changes.md

@.sdd/memory/dev-commands.md

---

## Read-on-demand rules

Before editing a path below, read the linked file.

| When editing | Read |
|---|---|
| Any `**/*.go` file, tests included | [`.sdd/memory/architectural-standards.md`](.sdd/memory/architectural-standards.md) |
| `controllers/**/*.go`, `pkg/providers/**/*.go`, `pkg/errors/**/*.go`, `pkg/util/kube/cource/**/*.go`, `services/**/*.go` | [`.sdd/memory/operator-best-practices.md`](.sdd/memory/operator-best-practices.md) |
| `**/*_test.go` | [`.sdd/memory/testing-standards.md`](.sdd/memory/testing-standards.md) |
| `test/e2e/**/*.go` | [`.sdd/memory/e2e-testing.md`](.sdd/memory/e2e-testing.md) and `test/e2e/README.md` |

---

## Running long commands

Test suites here run for minutes to hours (see `dev-commands.md`). Start them in the background with output redirected to a log file and poll the log with `tail` / `grep`; do not block on them and do not schedule timed wake-ups to check on them. Never start a second run while one is still in flight.
