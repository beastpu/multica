---
id: pattern.issues.swimlane_drag_settle_lock
kind: pattern
title: "Swimlane drag needs a settle lock after drop"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4416
source_pr_number: 4416
source_issue_refs:
  - MUL-3493
merged_at: 2026-06-22T12:27:59Z
merge_commit: 42b4bc6af58a441bcb6c54262a004ceebe3ef54a
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - packages/views
signals:
  - "Swimlane drag can rebuild local cells after drop but before the move mutation settles"
  - "Board and list drag surfaces are stable, but swimlane still flickers on remaining refetches"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/issues/components/swimlane-view.tsx
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 42b4bc6af58a441bcb6c54262a004ceebe3ef54a
    content_hash: 1278de3d9a176ecaf0e556ec7567874fa9d41365
fix_pattern: "Hold a drag-settle lock from drop until the move mutation's onSettled callback, gate local resync and derived maps during that window, then perform one resync from reconciled cache state."
verification:
  - packages/views/issues/components/swimlane-view.test.tsx
gotchas:
  - "Do not stop guarding local drag state at drag end; the mutation may not have settled yet."
  - "Do not share board/list assumptions blindly with swimlane; its two-dimensional local cell model needs explicit coverage."
related:
  - pattern.issues.optimistic_drag_flicker_refetch
tags:
  - packages-views
  - issues
  - drag
  - swimlane
---

# Swimlane drag needs a settle lock after drop

## 症状

在 swimlane view 拖拽 issue 时,drop 之后、move mutation settle 之前如果 cache 发生变化,`localCells` 可能被重新构建,导致乐观移动被中途覆盖。board/list 的暴露面已经被 cache-layer 修复降低,但 swimlane 仍可能在剩余 membership-change refetch 下抖动。

## 根因

swimlane 的 resync `useEffect` 和 `issueMap` freeze 只检查 `isDraggingRef`。drag end 以后这个 guard 解除,但 move mutation 还没 settle。这个窗口里到达的 cache change 可以重建本地二维 cells,破坏 drop 后的 optimistic state。

## 正确修复模式

增加 `isSettlingRef` 和 settle version。`handleDragEnd` 从 drop 开始持有 settle lock,并把 release 放到 move mutation 的 `onSettled` callback。resync effect 和 `issueMap` freeze 同时受 dragging/settling gate 控制,settle 后再从 reconciled cache 做一次同步。

## 为什么这个修法正确

拖拽交互的临界区不是到 `dragend` 就结束,而是到 server mutation 和 cache reconciliation 完成才结束。settle lock 明确覆盖这个窗口,让本地交互状态不会被中间 cache 事件覆盖。

## 验证方式

PR 更新了 swimlane drag assertions,并新增 settle callback 测试,验证 drop 后会等 move settle 再释放同步窗口。

## 适用边界

适用于组件维护本地拖拽布局,同时又从 React Query cache resync 的交互面。纯展示列表或无本地 optimistic layout 的组件不需要这个锁。

## 不要照搬

不要只加一个更长的 timeout 来掩盖竞态;settle release 应绑定 mutation 的真实 `onSettled`。
