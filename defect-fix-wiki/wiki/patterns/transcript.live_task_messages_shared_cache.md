---
id: pattern.transcript.live_task_messages_shared_cache
kind: pattern
title: "Running task transcript dialogs should subscribe to shared task message cache"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4452
source_pr_number: 4452
source_issue_refs:
  - "#4440"
  - MUL-3554
  - https://github.com/multica-ai/multica/issues/4440
merged_at: 2026-06-23T08:35:30Z
merge_commit: 2857a4c64933e61fcfde6d3b458bb6e33c782e71
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - packages/core
  - packages/views
signals:
  - "Issue execution-log or agent activity transcript opened on a running task never appends new messages"
  - "Transcript only updates after task completion or page reload"
code_refs:
  - repo: multica-ai/multica
    path: packages/core/chat/queries.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 2857a4c64933e61fcfde6d3b458bb6e33c782e71
    content_hash: f151fbdadaf807da0cd770b94822a1b4e4423502
fix_pattern: "For live task UI, render from the shared React Query task-message cache seeded by WebSocket events, and merge forced backfills by sequence instead of replacing the cache."
verification:
  - packages/core/chat/queries.test.ts
  - packages/views/common/task-transcript/transcript-button.test.tsx
gotchas:
  - "Do not mount live subscriptions for closed terminal rows that only need lazy one-shot fetch."
  - "Do not blindly replace task-message cache with a backfill page while WebSocket events may arrive concurrently."
related: []
tags:
  - packages-core
  - packages-views
  - transcript
  - realtime
  - react-query
---

# Running task transcript dialogs should subscribe to shared task message cache

## 症状

The issue execution log and agent activity transcript dialog showed only a snapshot when opened on a running task. New tool calls, thinking, and text did not append until the task finished or the page reloaded.

## 根因

`TranscriptButton` fetched once through `api.listTaskMessages` on click and stored the result in local state. It did not subscribe to the shared `["task-messages", taskId]` cache that the WebSocket `task:message` stream already updates. The chat live card worked because it used the shared cache path.

## 正确修复模式

Add a live-cache mode for running persisted task IDs: render from the shared task-message cache, use a read-only observer so React Query does not refetch and replace the cache, force backfill on open and terminal transition, and merge fetched messages by sequence.

## 为什么这个修法正确

Server state for task messages already lives in React Query and is updated by WebSocket events. The transcript dialog should observe that canonical cache rather than copying a one-shot snapshot into local component state.

## 验证方式

The PR added tests for sequence merge behavior, live cache append, forced backfill on open, terminal one-shot behavior, and keeping the dialog populated across a running to terminal transition.

## 适用边界

Use this pattern for issue or agent task transcript UI that has a task id and receives `task:message` events. It does not cover autopilot `run_only` logs when the backend does not broadcast task messages for issue-less runs.

## 不要照搬

Do not make every transcript dialog live. Terminal tasks can keep lazy fetch semantics, and live rows should avoid extra baseline requests when closed.
