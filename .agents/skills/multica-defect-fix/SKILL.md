---
name: multica-defect-fix
description: Use when evaluating, triaging, fixing, or reviewing Multica bugs/defects from GitHub issues, GitHub PRs, user bug reports, regressions, or suspected product problems; use before deciding whether an issue is worth fixing, implementing a bugfix PR, adding regression tests, or reviewing whether a bugfix solves the root cause without new risks.
---

# Multica Defect Fix

Use this skill for Multica bug/defect work: issue triage, product validity
assessment, root-cause investigation, implementation, regression tests,
self-review, PR updates, and duplicate/overlapping PR decisions.

## Required Read

Before planning or implementing, read:

- `CLAUDE.md` and `AGENTS.md` for architecture, boundaries, commands, and test
  placement.
- `docs/agent-skills/go-backend-quality/SKILL.md` when touching Go backend,
  database migrations, sqlc queries, queues, daemons, webhooks, or state
  synchronization.
- Relevant issue/PR text, comments, linked PRs, and current upstream `main`
  behavior when the task references GitHub.

## Operating Rules

- Start with product validity: decide whether this is a real product problem,
  an enhancement, a duplicate, already fixed, or not worth fixing now.
- Verify current `main` or the PR branch before assuming the report is still
  valid.
- State a root-cause hypothesis before code changes.
- Map all relevant entry points, not only the reported surface.
- Add or update a regression test for every non-trivial fix. The test should
  fail on the old behavior and pass with the fix.
- Keep the patch focused. Avoid unrelated refactors, formatting churn, new
  dependencies, workflow/CI changes, or broad file moves.
- Self-review the diff against the original issue: root cause, coverage,
  edge cases, permission/workspace boundaries, pagination/caching/realtime
  effects, and potential new regressions.
- Run targeted checks that match the touched surface; run broader checks when
  the blast radius is high.
- After pushing a PR, watch CI and inspect failures before guessing.

## Regression Test Coverage Rules

Maintainers have consistently valued tests that prove the bug boundary, not
tests that merely exercise changed code. For non-trivial fixes, use these rules:

- Put the test at the closest layer that exposes the contract:
  handler/integration tests for HTTP behavior, CLI tests with fake servers for
  command behavior, `packages/views` tests for shared UI behavior, package tests
  for daemon/concurrency/service logic, and DB-backed tests for persistence or
  idempotency invariants.
- Cover both negative and positive paths when fixing permissions, destructive
  actions, state transitions, or validations. Example shape: denied member path
  plus allowed admin/owner path.
- Cover boundary and drift cases explicitly: unknown enum values, missing or
  malformed API fields, conflict/error responses, timezone/clock skew,
  concurrency races, platform-specific behavior, and retry/idempotency cases.
- Assert the user-visible or system contract, not incidental implementation:
  HTTP status and row survival/deletion, stdout JSON and guidance text, rendered
  fallback text/icon class, queue claim counts, persisted idempotency keys, or
  emitted events.
- When a bug is hard to reproduce behaviorally, a structural test is acceptable
  if it checks the invariant directly and briefly explains why. Keep platform
  probes guarded with skips where needed.
- If the production fix touches both normal and skip/error/fallback branches,
  add tests for each materially changed branch. Do not only test the happy path.
- For DB integration tests, own cleanup completely. Delete dependent rows in
  schema order, use unique fixture data, and clean up both pre-seed leftovers
  and `t.Cleanup` rows so shared test workspaces are not polluted.
- Avoid live external services. Use fake HTTP servers, fake clients, fixtures,
  deterministic clocks, atomics, buffered channels, and deadlines for
  concurrency tests.
- Name tests after the regression or invariant they protect. A future revert
  should fail for the right reason.

## PR Shape

When reporting or opening a PR, include:

- Product verdict and user impact.
- Root cause and covered entry points.
- Files changed and why.
- Regression tests added or updated, including negative/positive/boundary
  coverage.
- Exact commands run and results.
- Remaining risk or follow-up, if any.

For reviews, lead with findings:

- Does the PR solve the issue root cause?
- Is coverage complete across relevant entry points and changed branches?
- Could the patch introduce new problems?
- Are tests meaningful and sufficient?
