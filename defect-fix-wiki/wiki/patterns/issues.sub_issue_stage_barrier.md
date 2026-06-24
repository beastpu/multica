---
id: pattern.issues.sub_issue_stage_barrier
kind: pattern
title: "Sub-issue parent wakes should close stage barriers, not every child"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4410
source_pr_number: 4410
source_issue_refs:
  - MUL-3508
merged_at: 2026-06-22T16:14:43Z
merge_commit: a123dfc2df753dde7a103610dc016a4aa18a393f
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - packages/core
  - packages/views
  - server
signals:
  - "Parent assignee wakes on every sub-issue completion"
  - "Serial sub-issue chains require hand-managed backlog promotion"
  - "Backlog siblings should hold the parent wake barrier open"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/issue_child_done.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: a123dfc2df753dde7a103610dc016a4aa18a393f
    content_hash: 401fa1da0843c8710a1d2fd89d980d04dab10054
fix_pattern: "Represent dependent sub-issues as ordered stages and wake the parent assignee only when the lowest unfinished stage barrier closes, while leaving next-stage promotion as an agent-driven action."
verification:
  - packages/core/api/schemas.test.ts
  - packages/core/issues/batch.test.ts
  - packages/core/issues/mutations.test.tsx
  - packages/core/issues/queries.test.ts
  - packages/core/issues/ws-updaters.test.ts
  - packages/views/issues/components/batch-action-toolbar.test.tsx
  - packages/views/issues/components/issue-detail.test.tsx
  - packages/views/issues/components/issues-page.test.tsx
  - packages/views/issues/components/pickers/stage-picker.test.tsx
  - packages/views/issues/components/swimlane-view.test.tsx
  - packages/views/issues/hooks/issue-delete-mutations.test.tsx
  - packages/views/issues/utils/filter.test.ts
  - packages/views/modals/create-issue.test.tsx
  - server/internal/handler/issue_batch_test.go
  - server/internal/handler/issue_child_done_stage_test.go
gotchas:
  - "Do not wake a parent on every child completion when siblings are meant to run as a barrier."
  - "Do not have the server auto-promote later backlog stages; the woken agent should decide."
  - "Treat unstaged siblings as one implicit stage for existing data."
related:
  - pattern.issues.child_done_backlog_parent_wake
  - pattern.agent.sub_issue_stages_runtime_brief
tags:
  - packages-core
  - packages-views
  - server
  - issues
  - sub-issues
  - stages
---

# Sub-issue parent wakes should close stage barriers, not every child

## 症状

父 issue 下有多个 sub-issues 时,每完成一个 child 都可能唤醒 parent assignee,导致 parent 过早行动。对于串行或分阶段工作,团队只能手动维护 backlog 链,容易出现 surprise auto-cascade。

## 根因

旧模型只有 child done -> parent notification,没有表达 sibling 分组和阶段 barrier 的字段。server 不知道哪些 children 可以并行、哪些需要等前一批全部完成,于是只能逐个 child completion 触发。

## 正确修复模式

引入 nullable `issue.stage`。未设置 stage 的 sibling set 视为一个隐式 stage,最后一个 child 结束才关闭 barrier。设置 stage 时,当最低未完成 stage 中所有 sub-issues 都达到 terminal 状态后,才触发 parent notification / wake。下一阶段 backlog sub-issues 是否提升到 todo,由被唤醒 agent 决定。

## 为什么这个修法正确

parent wake 是工作流状态转换,不是每个 child 的完成回调。stage barrier 让 server 只表达“某一阶段完成”的事实,避免替 agent 做推进决策,同时兼容无 stage 的旧数据。

## 验证方式

PR 覆盖了 stage barrier/progress summary 的后端测试,并更新 API schema、batch/update、Web UI picker/detail/create modal、WS/query 相关测试。还验证了 migration、sqlc、Go build/vet 和 packages/views 测试。

## 适用边界

适用于 sub-issue 存在并行批次、串行阶段或 backlog 停车语义的场景。完全独立、无父子依赖的普通 issue 不需要 stage barrier。

## 不要照搬

不要把 stage barrier 实现成 server 自动推进下一阶段;那会把 agent 的计划和判断变成隐式副作用。
