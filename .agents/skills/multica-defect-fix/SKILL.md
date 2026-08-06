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
- Before editing, run an internal defect-case lookup when
  `defect-fix-wiki/indexes/cases.index.jsonl` exists. Use:
  `pnpm defect-fix-wiki query --text "<symptoms and errors>" --module <module>`
  and add `--file <path>` when the report already points at specific files.
  Record only the internal working note:
  - which cases were checked,
  - why each case is or is not similar,
  - which fix pattern or gotcha is reusable,
  - what still must be verified in the current code.
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
- Do not cite private defect-fix wiki case IDs, private wiki links, or internal
  case-library contents in public PR descriptions, GitHub issue comments, or
  Multica issue comments. Public output should explain only the current issue's
  root cause, fix, and verification.

## Concurrency and State Authority Rules

Races in daemon/queue/sync code on this repo keep reappearing in two shapes.
Both are design problems that look like missing checks:

- A point-in-time re-check is not a fix when the window you must cover lies
  between that check and the action it guards. "Read the local state again just
  before sending the request" only lowers the reproduction rate; the reverse
  operation can complete in the gap. Put the action itself inside the existing
  order instead — extend the critical section to span decide → apply → external
  call — and keep the re-check only as an in-order predicate.
- When several paths can reach the same destructive verdict but only one holds
  the state that makes acting on it safe (a barrier, a sequenced hold, a lease),
  do not give the other paths that state too. Have them report the verdict as
  "this response is not authoritative about these items", preserve the current
  rows, and let the single owner act. Distributing the authority means several
  places now maintain an ordering that only worked because one place did.
- Deferring an action to the single owner has a cost. State it explicitly and
  check it is acceptable — "at most one refresh tick later, which is the
  pre-verdict status quo" is a reason; "probably fine" is not. If the delay is a
  real new exposure, converge the entry points instead of letting the verdict
  hang.
- Discarding a probe's return value does not mean the path is not acting on it.
  Check how the caller treats absent items: if absence is treated as deletion,
  the verdict is already being executed, just without a record.

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
- Make the test double reproduce the identity and idempotency semantics the
  system under test depends on, not just the response shape. A fake register
  endpoint that minted a fresh row ID per call made a whole class of
  cleanup-undoes-recovery interleaves impossible to express, because the two
  operations never named the same row — the suite stayed green through the bug.
  Mirror the real upsert: same key, same ID.
- Gate the external call, not the goroutine, when you need a deterministic
  interleave. Blocking inside the fake server's handler pins the exact in-flight
  window; sleeps around the caller do not.
- Name tests after the regression or invariant they protect. A future revert
  should fail for the right reason.

## Multi-Round Review Discipline

Long review threads on this repo are usually structural, not a run of
oversights. These rules come from a fix that took eight rounds:

- If two consecutive rounds each close the identified race and reveal a
  narrower one behind it, stop patching. Name the structural problem and
  propose the design — with the open questions you actually need answered —
  in a comment before implementing it. Asking first saved a round; guessing
  cost several.
- Re-review your own new code with the same lens you would apply to someone
  else's, before pushing. Disclose what that pass found instead of shipping it
  silently; two of the defects in that thread were caught this way.
- Do not silently reverse a decision the reviewer already approved. If new
  evidence contradicts it, say so and let them decide.
- When a later change invalidates something you asserted in an earlier PR
  comment, correct it explicitly in the next comment.
- Keep formatting and import churn out of files the change does not otherwise
  touch. A repo-wide `gofmt` or format-on-save sweep dragged six unrelated
  files into that PR and the maintainer had to strip them before merge.
- Re-check migration numbering against upstream `main` immediately before
  merge, not only when the branch was created; the number you took can be
  claimed while the PR is in review.
- State the verification you actually ran (commands, database state, which
  failures pre-exist on `main`). Do not report a check as passing on a
  different revision than the one you pushed.
- A maintainer may push commits to your branch and then file findings against
  their own commits, explicitly not as changes requested of you. Answer the one
  question they are actually asking — who takes the fix — instead of treating it
  as a normal review round. Taking it is usually right: the invariant carries
  your feature's name, and the branch is yours to land.
- When you substitute a different test for one the reviewer proposed, say so and
  why. If the chosen design makes their scenario unconstructible, that is the
  point worth stating — silently shipping a different assertion reads as having
  missed the request.
- Prefer the reviewer's stated preference when they offer two options and lean
  one way, unless you can name a concrete reason the other is better. Say which
  you took and why in the same comment.

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
