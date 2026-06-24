---
id: pattern.issues.child_done_backlog_parent_wake
kind: pattern
title: "Child-done notifications should not wake backlog parents"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4391
source_pr_number: 4391
source_issue_refs:
  - MUL-3497
merged_at: 2026-06-22T05:56:48Z
merge_commit: 5556f4570b9d4a9c3108350a1fd73eae2532790e
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - server
signals:
  - "Backlog subtask workflow unexpectedly activates parked work"
  - "A system Multica actor appears to wake agents from a child completion"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/issue_child_done.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 5556f4570b9d4a9c3108350a1fd73eae2532790e
    content_hash: c629196a2ad072b0c1b55f700b9be4ed21a92d63
fix_pattern: "For automatic parent wake/notification flows, treat backlog as an explicit inert parking state and skip side effects until the user moves the parent back into an active status."
verification:
  - server/internal/handler/issue_child_done_test.go
gotchas:
  - "Do not suppress child-done wakeups for active parents; the serial subtask workflow depends on them."
  - "Do not solve parked-parent behavior by changing broad issue update semantics unless that wider product decision is intended."
related: []
tags:
  - server
  - issues
  - backlog
  - agent-trigger
---

# Child-done notifications should not wake backlog parents

## 症状

Tasks deliberately parked in Backlog were auto-activated into To Do after a child issue completed. Users saw a system Multica actor appear to prompt agents even though the parent was parked.

## 根因

When a sub-issue transitions to done, `notifyParentOfChildDone` posts a system comment on the parent and wakes the parent's assignee agent. The woken agent can promote sibling backlog sub-issues into todo. If the parent itself is in backlog, this violates the product meaning of backlog as a user-triggered parking lot.

## 正确修复模式

Add backlog to the terminal/inert guard set for parent child-done notifications. When the parent is in backlog, skip the system comment, mention, agent trigger, and squad trigger.

## 为什么这个修法正确

The create/assign path already treats backlog as a state that does not enqueue agent work. Child-done propagation should honor the same semantic boundary so parked parent work remains inert until the user explicitly reactivates it.

## 验证方式

The PR added a regression test where a backlog parent receives a child-done event and no system comments are posted on the parent. The broader `TestChildDone*` suite covered active-parent, member, squad, and self-loop guard behavior.

## 适用边界

Use this pattern for automatic side-effect flows that wake agents or promote work. It is specifically about parked parent issues, not all parent-child notifications.

## 不要照搬

Do not remove active-parent wakeups. For active parent statuses, child completion still drives serial subtask progress.
