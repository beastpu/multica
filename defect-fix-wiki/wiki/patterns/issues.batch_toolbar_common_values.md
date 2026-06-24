---
id: pattern.issues.batch_toolbar_common_values
kind: pattern
title: "Batch toolbar pickers must derive real common selected values"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4403
source_pr_number: 4403
source_issue_refs:
  - MUL-3510
merged_at: 2026-06-22T10:23:05Z
merge_commit: 149cc9bd0a592d75001ba5ebfcd6e0d2aab15d15
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - packages/core
  - packages/views
signals:
  - "Bulk status picker always shows todo as checked"
  - "Bulk priority or assignee picker displays a constant current value instead of the selected issues' shared value"
code_refs:
  - repo: multica-ai/multica
    path: packages/core/issues/batch.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 149cc9bd0a592d75001ba5ebfcd6e0d2aab15d15
    content_hash: 9db58622eeb1bf1c047fbb0b14d9731b66ad401c
fix_pattern: "For batch edit UI, derive common selected fields from the selected issue objects and pass nullable current values to pickers, preserving a distinction between mixed selections and a real shared empty value."
verification:
  - packages/core/issues/batch.test.ts
  - packages/views/issues/components/batch-action-toolbar.test.tsx
gotchas:
  - "Do not hardcode picker current values in a shared batch toolbar."
  - "Do not collapse all-unassigned and mixed assignee selections into the same UI state."
related: []
tags:
  - packages-core
  - packages-views
  - issues
  - batch
---

# Batch toolbar pickers must derive real common selected values

## 症状

在 Kanban List 等批量操作入口选中多个 issue 后,bulk Status picker 总是把 `todo` 显示为当前值,不反映选中 issue 的真实状态。相同问题也会影响 priority 和 assignee。

## 根因

`BatchActionToolbar` hardcode 了 picker 的 current value,例如 `status="todo"`、`priority="none"` 和空 assignee。selection store 只保存 ID,toolbar 没有拿到 issue objects,所以只能传常量。这个 toolbar 被多个 issues 入口复用,所有入口都会继承错误显示。

## 正确修复模式

把可选 issue 集合作为 prop 传给 toolbar,由 toolbar 根据 selected IDs 过滤出选中 issue,再用纯 helper 计算共同字段。所有选中项共享同一个 status / priority / assignee 时显示该值;混合选择返回 `null` 表示没有单一当前值;全未分配 assignee 保留为真实共享值。

## 为什么这个修法正确

批量 picker 的 current value 是 server state 派生出的 UI 表达,必须从当前 issue 数据推导,不能从 selection ID 或默认常量推导。把计算放在 `packages/core` 纯函数里,也能让多个复用入口共享同一语义。

## 验证方式

PR 增加了 `commonIssueFields` 的 core 单元测试,并覆盖 batch toolbar 在共享值、混合值、全未分配 assignee 下的 picker 显示。

## 适用边界

适用于批量编辑工具栏、跨列表复用 picker、selection store 只保存 ID 的场景。单 issue 编辑不需要引入 mixed 状态。

## 不要照搬

不要把 mixed selection 展示成某个默认值,否则用户会误以为当前选中项已经共享该字段。
