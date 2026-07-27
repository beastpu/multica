---
id: pattern.channel.media_object_intent_ledger
kind: pattern
title: "Object storage + Postgres writes need a durable intent ledger settled by a reconciler"
status: active
confidence: high
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/5580
source_pr_number: 5580
source_issue_refs:
  - MUL-4934
merged_at: 2026-07-27T07:44:40Z
merge_commit: 60048172a7d636c632ab46128824014c56e386e3
first_ingested_at: 2026-07-27
last_reviewed_at: 2026-07-27
modules:
  - server
signals:
  - "A feature uploads a blob to object storage and then writes a row referencing it (attachments, avatars, exports, imports)"
  - "Orphaned objects in the bucket after upload errors, timeouts, or a crash between upload and DB commit"
  - "Attachment/file rows pointing at an object that no longer exists (broken image, 404 download)"
  - "Review feedback that inline cleanup 'deletes on error' cannot know whether the side effect happened"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/service/channel_media_reconciler.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: 5a173e3d7aa86116f7fc00c439f71ce78a40048f
  - repo: multica-ai/multica
    path: server/pkg/db/queries/channel.sql
    line_hint: "channel_media_pending_object queries"
    ref_kind: touched
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: be28b5ccd07d2a0b336ae9c0e0850dd7c8552463
  - repo: multica-ai/multica
    path: server/internal/integrations/channel/engine/session.go
    line_hint: BindMediaRefs
    ref_kind: touched
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: 565b5f701667827b2975b7057890f8052f78fdae
  - repo: multica-ai/multica
    path: server/internal/integrations/lark/media_ingest.go
    line_hint: ResolveMedia
    ref_kind: touched
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: 61ff82fc7515225d9643bcb882dc1fd3210985d4
fix_pattern: "Persist the upload intent in a ledger row BEFORE the PUT, delete that row inside the same transaction that inserts the referencing row, never compensate inline, and let an independent reconciler settle whatever is left through a persisted state machine (pending -> deleting -> tombstoned) with a lease, DB-computed deadlines, and a re-delete schedule for late-materializing PUTs."
verification:
  - server/internal/service/channel_media_reconciler_test.go
  - server/internal/storage/local_atomic_test.go
  - server/internal/integrations/lark/media_dedup_test.go
  - server/cmd/server/channel_media_invariant_test.go
gotchas:
  - "Do not delete an object because the upload or commit call returned an error: both have a result-uncertain window where the side effect landed anyway."
  - "Do not treat an empty verification read as proof that a COMMIT rolled back; it only proves one snapshot."
  - "Do not let a settle delay carry correctness weight — the state flip must be what fences bind vs delete."
  - "Do not hold a DB transaction or row lock across object-storage I/O; claim, then do the I/O outside the transaction under a lease."
  - "Do not skip the reference check on a re-delete pass; keeping a referenced object and alerting beats deleting one something reads."
  - "Do not derive the object key from an upstream message id alone if that message can be ingested twice — a tombstoned key silently blocks the second ingest."
related:
  - antipattern.storage.inline_compensation_on_uncertain_write
tags:
  - server
  - storage
  - postgres
  - reconciler
  - idempotency
  - data-lifecycle
---

# Object storage + Postgres writes need a durable intent ledger settled by a reconciler

## 症状

Inbound Feishu images/videos were downloaded, uploaded to S3-compatible storage, and then written as `attachment` rows. Every failure mode leaked in one of two directions:

- upload error, resolve deadline, or a crash between upload and bind left an object in the bucket that nothing would ever reclaim;
- a bind failure (including a `COMMIT` that returned an error but had durably committed) deleted objects that a persisted `attachment` row already referenced, producing a broken attachment the user hits.

## 根因

Object storage and Postgres are two systems with no shared transaction, and the inline compensation logic asked a question neither system can answer at the moment it is asked: *did my side effect actually happen?* `PutObject` can succeed server-side and still return a client error (lost response, dropped connection, context deadline firing after the write landed). `tx.Commit` can return an error after the transaction durably committed. Compensating on "the call returned an error" therefore corrupts state in both directions, and each narrowing of the window surfaced the next race behind it — the pattern was structural, not a series of oversights.

A second, subtler variant: even a *correct* decision to delete cannot be ordered against a PUT the client already abandoned. The store may materialize that object after the DELETE completes, so "delete now" and "delete after a settle delay" are both bets on timing.

## 正确修复模式

Make the ledger row, not the API result, the source of truth.

1. **Intent before the write.** Before the PUT, upsert a row keyed by the deterministic storage key: `(storage_key PK, workspace_id, owner_row_id, storage_url, state, lease_token, lease_expires_at, attempt, next_attempt_at, last_error)`. The URL is a pure function of config, so it can be persisted before the object exists. If the intent write fails, skip the upload — no durable intent, no object.
2. **Clear the intent inside the referencing transaction.** `DELETE FROM ledger WHERE storage_key = ANY($1) AND state = 'pending' RETURNING storage_key`, in the same transaction that inserts the `attachment` rows. Commit landed ⇔ intents are gone, atomically. This dissolves the ambiguous-commit problem instead of adjudicating it: no verification query, no "unknown result" sentinel. A key the reconciler already owns is not returned, and its ref must not be attached.
3. **Never compensate inline.** Upload error, deadline, bind failure, ambiguous commit, crash — all do nothing but leave the row.
4. **Settle asynchronously through a persisted state machine.** An independent worker claims a due row (`pending` → `deleting`) with a lease in a short transaction, checks for a durable reference *after* winning the claim (race-free: bind can no longer succeed on that key), then does the object DELETE **outside any transaction**, then clears the row. Failures keep the row, bump `attempt`, and back off; lease expiry lets another replica reclaim a crashed worker's rows.
5. **Fence the abandoned PUT with a tombstone, not a timeout.** After deleting an unreferenced object, keep the row as `tombstoned` and re-delete on a widening schedule (15m/1h/6h/24h), dropping it only when the schedule is exhausted. A late materialization is caught by a later pass instead of resting on a single settle-window bet.
6. **Keep-and-alert on a durable reference.** Re-run the reference check on every pass, tombstones included. A tombstone finding a reference is a broken invariant, not a race — keep the object, clear the row, log it, and count it on a dedicated metric. Deleting there would manufacture exactly the dangling reference the ledger exists to prevent.

## 为什么这个修法正确

- The only ordering guarantee that exists is Postgres transactionality, so the design puts every ambiguous outcome on the side of "a reclaimable orphan," never "a broken reference." An orphan costs bytes; a dangling attachment is user-visible corruption.
- The state flip (`pending` → `deleting`) is what fences bind against delete. The settle delay is an operational buffer with no correctness weight — an explicitly tested invariant (`settle >> every media/HTTP/DB budget`).
- Crash coverage falls out for free: the intent row is written first, so a crash anywhere after it leaves exactly the artifact the reconciler needs.

## 验证方式

Fault injection is the deliverable — roughly half the diff is tests:

- storage accepted the object but the client got an error → no orphan;
- DB committed but the client got a commit error → the bound object survives;
- bind-wins vs reconciler-wins on the same key;
- claim-then-crash recovery via lease expiry, and DELETE failure → retry with backoff;
- the object materializes *after* the reconciler's DELETE → reclaimed by a later tombstone pass;
- a durable reference found on a tombstone pass → object kept, anomaly counter incremented.

## 适用边界

Any flow that writes a blob to object storage and then a row that references it: chat/issue attachments, avatars, exports, imports, generated artifacts. The heavier machinery (tombstone schedule, lease, metrics) is warranted when the object is user-visible and the write path can be abandoned mid-flight; a purely internal cache can stop at intent + reconciler.

## 不要照搬

- Do not copy the settle/lease/backoff constants without re-deriving them against the pipeline's own budgets; the invariant is a ratio, not the numbers.
- Do not add the tombstone schedule to a store whose DELETE is strongly ordered against in-flight writes.
- Do not reuse a "delete the attempted key on error" helper from an older revision of this code — that is the antipattern this replaced.
- The reconciler is deliberately its own worker: do not fold it into another sweeper's cadence, or object-storage latency spikes will starve that sweeper.

## Source

- PR: https://github.com/multica-ai/multica/pull/5580
- Linked issues: MUL-4934
- Merge commit: 60048172a7d636c632ab46128824014c56e386e3
