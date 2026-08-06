---
name: go-backend-quality
description: |
  Use when changing Multica Go backend code under server/, database migrations,
  sqlc queries, background workers, webhooks, external integrations, task queues,
  or state synchronization flows. Enforces TDD, multi-replica safety,
  idempotency, transactional boundaries, and operational reliability.
---

# Go Backend Quality

Use this skill before implementing backend changes in `server/`, `server/migrations/`,
or `server/pkg/db/queries/`.

## Required Workflow

1. State the behavior contract before editing:
   - What user-visible or API-visible behavior changes?
   - What data changes, and which table owns the state?
   - What happens on duplicate requests, retries, concurrent calls, and partial failure?
2. Write the failing test first unless the change is a mechanical rename or pure docs change.
3. Implement the smallest change that makes the test pass.
4. Run the narrow test, then the package test:
   - `cd server && go test ./path/to/package -run TestName`
   - `cd server && go test ./path/to/package`
5. For DB/query changes, run `make sqlc` and include the generated files.
6. Before finishing, run at least:
   - `cd server && go test ./internal/handler ./internal/service ./cmd/server`
   - `git diff --check`

If you skip a required test, say exactly why and what residual risk remains.

## Design Gates

### Handler Boundaries

- Handlers parse and validate HTTP input, authorize, call service/query code, and shape responses.
- Put business workflows in `internal/service` or narrow helper functions.
- Do not bury cross-system logic inside handlers unless it is a small adapter.
- Validate raw UUIDs with the repository's handler conventions before write queries.

### Database And Transactions

- Prefer DB-enforced invariants over process-local checks.
- Use unique indexes for idempotency keys and one-owner relationships.
- Use explicit transactions when multiple writes must commit or roll back together.
- If a transaction includes an external API call, justify the ordering. Prefer:
  - persist intent/state first, then call remote, then reconcile; or
  - call remote first, then commit local only if remote succeeded.
- Never rely on a read-then-insert check without `ON CONFLICT`, row locks, or a unique constraint.

### Writes That Span Object Storage Or Another Non-Transactional System

Postgres and an object store (or payment provider, IM platform, external tracker)
share no transaction, so "did my write land?" is unanswerable at the moment an
error surfaces: a `PutObject` may have been fully received before the connection
dropped, and a `COMMIT` may have applied before its ack was lost.

- Classify every cross-system call as **known-failed** (the remote definitively
  rejected it: 4xx, validation, precondition) or **result-uncertain** (timeout,
  reset, context deadline, lost ack). Only known-failed may be compensated inline.
- For result-uncertain outcomes, persist the intent **before** the remote write
  (a ledger row keyed by the object key, carrying the URL the referencing row
  will hold), and delete that row **inside the same transaction** that inserts
  the referencing row. Commit landed ⇔ intent gone, atomically — an ambiguous
  commit then needs no adjudication.
- Never compensate inline on a result-uncertain outcome, and never treat an
  empty verification `SELECT` as proof that a `COMMIT` rolled back. It proves one
  snapshot, not a terminal state.
- Resolve every ambiguity toward a reclaimable orphan, never toward a dangling
  reference. Bytes in a bucket are cheap; a row pointing at a deleted object is
  user-visible corruption.
- Settle leftovers in an independent worker through a persisted state machine
  (`pending` → `deleting` → done) with a lease. Check for a durable reference
  **after** winning the claim, and do the remote I/O **outside** any transaction
  — never hold a DB connection or row lock across it.
- If a reclaim pass finds a durable reference where the state machine says it
  cannot exist, that is a broken invariant: keep the object, clear the row, log
  it, and count it on a dedicated metric. Do not delete through it.
- A deletion cannot be ordered against a request the client already abandoned.
  If that matters, keep a tombstone and re-delete on a widening schedule rather
  than betting on a single settle window.
- Timing must not carry correctness weight. If a settle delay exists, write down
  and test the invariant that makes it a buffer only (`settle >> every inline
  budget in the pipeline`).
- Derive the object key from the row it will attach to, not from an upstream id
  that can be ingested twice — otherwise a second ingest collides with the first
  one's ledger row and silently drops its payload.
- Local-disk writes go through a temp file and `rename`, and the temp path must
  be derived from the object key so a crash between write and rename leaves
  something the delete path can still find.

### Multi-Replica Safety

Assume multiple `multica-server` pods are running.

- Do not use process-local memory as the source of truth for shared state.
- Do not assume only one worker, websocket consumer, or webhook handler is active.
- Background workers must be safe under duplicate execution.
- For claim/consume flows use one of:
  - `FOR UPDATE SKIP LOCKED`
  - Postgres advisory locks
  - a lease column with expiry
  - Redis atomic operations with expiry
  - unique constraints plus idempotent upsert
- Compute every deadline (lease expiry, settle cutoff, retry backoff) from the
  database clock — pass a duration and let SQL do `now() ± interval`. A process
  timestamp compared against `now()` makes a drifting replica reclaim work early
  or hand out leases that are born expired.
- A claim is a promise to do the work now: claim one unit immediately before
  processing it rather than reserving a batch up front, so `attempt`/backoff
  describe attempts that actually happened.
- Realtime fanout must work across pods via the existing Redis relay or a documented fallback.

### Idempotency And Retries

Every webhook, callback, sync job, and worker tick must define:

- Deduplication key, if the remote system supplies an event/message ID.
- Idempotent local write behavior for repeated processing.
- Retry behavior for transient remote errors.
- Permanent failure recording visible to operators or users.
- Partial success semantics for batch work.

### External Integrations

- Wrap remote APIs behind a small interface so tests can use fake clients.
- Tests must cover success, remote error, malformed response, and missing mapping/config.
- Do not log secrets, tokens, full auth headers, or raw webhook secrets.
- Store secrets write-only from the UI. Return only `has_*_secret` booleans to clients.
- If remote and local state can diverge, define which side is authoritative and how reconciliation happens.

### State Machines

- Status changes must respect both Multica and external system transition rules.
- Test invalid transitions and missing mappings.
- Do not update local state first if a remote authoritative state machine can reject the transition.
- If multi-hop remote transitions are required, implement them explicitly and test each hop.

## Test Expectations

Choose tests that match the blast radius:

- Pure mapping/parsing logic: table-driven unit tests.
- Handler behavior: `httptest` plus database fixtures.
- DB invariants: tests that attempt duplicate/concurrent writes.
- Worker/queue code: two workers running concurrently should not double-process.
- External integrations: fake client tests, not live API calls.
- Frontend-facing API responses: test error messages when the UI depends on them.

For concurrency-sensitive code, include one test using goroutines or two independent store/client instances when practical.

## Review Checklist

Before final response or PR:

- Is there a failing test that would have caught the original bug or requested behavior?
- For each cross-system write: what happens if the remote applied it but the
  client saw an error? Is there a test where the fake performs the side effect
  and then returns the error?
- Are all new DB invariants enforced in migrations, not only in Go code?
- Can this run with two server replicas?
- Is the operation idempotent under retry?
- Are partial failures observable?
- Are secrets redacted and write-only?
- Are logs useful without leaking sensitive data?
- Did `make sqlc` run after query changes?
- Did the narrow and package tests pass?
