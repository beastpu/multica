---
id: antipattern.storage.inline_compensation_on_uncertain_write
kind: antipattern
title: "Treating a returned error as proof the side effect did not happen"
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
  - "Cleanup code that deletes a remote object or row because the call returned an error"
  - "A verification SELECT used to decide whether a COMMIT rolled back"
  - "Comments claiming a single request cannot land after its client gave up"
  - "Review rounds that each close one race and reveal a narrower one behind it"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/integrations/lark/media_ingest.go
    line_hint: ResolveMedia
    ref_kind: root_cause
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: 61ff82fc7515225d9643bcb882dc1fd3210985d4
  - repo: multica-ai/multica
    path: server/internal/service/channel_media_reconciler.go
    line_hint: 1
    ref_kind: related
    verified_commit: 60048172a7d636c632ab46128824014c56e386e3
    content_hash: 5a173e3d7aa86116f7fc00c439f71ce78a40048f
fix_pattern: "Classify every cross-system call as known-failed or result-uncertain. Result-uncertain outcomes get a durable artifact and an asynchronous settler, never an inline compensating delete — see pattern.channel.media_object_intent_ledger."
verification:
  - server/internal/service/channel_media_reconciler_test.go
gotchas:
  - "An empty verification read proves one snapshot, not a terminal transaction state."
  - "A DELETE issued after a client-side upload error is a concurrent write against a PUT that may still be in flight; object stores do not order those."
  - "Retrying the compensation does not help — the uncertainty is in the question, not the attempt count."
related:
  - pattern.channel.media_object_intent_ledger
tags:
  - server
  - storage
  - postgres
  - consistency
  - data-lifecycle
---

# Treating a returned error as proof the side effect did not happen

## 症状

Cleanup logic reads naturally and passes tests, yet production accumulates orphaned objects *and* occasionally breaks live references. Each review round closes the identified race and a narrower one appears behind it.

The shape in PR #5580, across three successive revisions:

1. `if err := upload(); err != nil { continue }` — the attempted key never reached the caller, so nothing could ever reclaim an object the store had actually written.
2. `if err := tx.Commit(ctx); err != nil { deleteObjects(refs) }` — a lost commit ack deleted the objects that a durably committed `attachment` row already pointed at.
3. `if none of the URLs are visible { assume rollback; delete }` — an empty read from one snapshot was treated as proof the COMMIT had terminated in a rollback.
4. `deleteObjectNow(key)` on upload error — a DELETE racing a PUT the client had abandoned, with no ordering between them.

## 根因

Two systems, no shared transaction. "Did my write land?" is unanswerable at the moment the error surfaces: `PutObject` may have been fully received before the connection dropped; `COMMIT` may have been applied before the ack was lost; an abandoned request may still materialize afterwards. Inline compensation has to answer that question to be correct, so it cannot be made correct by narrowing the window — only by removing the question.

## 正确修复模式

Split outcomes into *known-failed* (the remote definitively rejected: 4xx, validation, precondition) and *result-uncertain* (timeout, connection reset, context deadline, lost ack). Known-failed may compensate inline. Result-uncertain must leave a durable artifact — an intent row written *before* the call — and hand the decision to an asynchronous settler that runs long after any in-flight operation can land, and that decides from persisted state rather than from the error value. See `pattern.channel.media_object_intent_ledger` for the full shape.

## 为什么这个修法正确

The settler reads durable state, so its answer is terminal rather than a guess, and it always resolves ambiguity toward the cheaper failure (a reclaimable orphan instead of a dangling reference).

## 验证方式

Write the fault-injection tests first — they are what makes the difference visible: storage-accepted-but-client-errored, DB-committed-but-client-errored, and object-materializes-after-DELETE. A test whose fake returns an error *without* performing the side effect cannot express the case that matters, and will pass against the broken code.

## 适用边界

Any write to a system that does not share a transaction with the database: object storage, payment providers, IM platforms, external issue trackers, mail.

## 不要照搬

Do not read this as "never clean up inline." A definitively rejected request leaves nothing to clean up, and a same-transaction rollback is still the right tool for a single-system write.

## Source

- PR: https://github.com/multica-ai/multica/pull/5580
- Linked issues: MUL-4934
- Merge commit: 60048172a7d636c632ab46128824014c56e386e3
