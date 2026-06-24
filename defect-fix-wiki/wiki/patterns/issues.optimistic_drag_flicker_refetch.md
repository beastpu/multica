---
id: pattern.issues.optimistic_drag_flicker_refetch
kind: pattern
title: "Whole-list refetch after optimistic issue moves causes drag flicker"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4415
source_pr_number: 4415
source_issue_refs:
  - MUL-3493
merged_at: 2026-06-23T01:20:02Z
merge_commit: 45dae3185f01cdcd60967df82ca12c16917e566a
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - packages/core
  - packages/views
signals:
  - "Dragging an issue snaps back to the origin and then jumps to the target"
  - "Filtered boards repull the whole issue list after single-field changes"
code_refs:
  - repo: multica-ai/multica
    path: packages/core/issues/cache-helpers.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 45dae3185f01cdcd60967df82ca12c16917e566a
    content_hash: 9709c584d8bf986e9210140a48855f5d9fe17b3d
fix_pattern: "After optimistic issue moves or single-field edits, patch the affected issue across workspace and filtered query caches as the source of truth, and only invalidate whole lists when the change can alter list membership."
verification:
  - packages/core/issues/cache-helpers.test.ts
  - packages/core/issues/mutations.test.tsx
  - packages/core/issues/ws-updaters.test.ts
  - packages/views/issues/components/swimlane-view.test.tsx
  - packages/views/issues/utils/drag-utils.test.ts
gotchas:
  - "Do not invalidate and refetch a whole board immediately after applying an optimistic placement."
  - "Do not forget filtered caches such as My Issues, project boards, or actor panels when patching a single issue."
related:
  - pattern.issues.swimlane_drag_settle_lock
tags:
  - packages-core
  - packages-views
  - issues
  - optimistic-update
  - react-query
---

# Whole-list refetch after optimistic issue moves causes drag flicker

## 症状

拖拽 issue card 或做单字段修改时,卡片先回弹到原位置,再跳到目标位置。My Issues、Project、actor panel 等过滤视图也会在每次变化后重新拉整张列表。

## 根因

多个入口重复了同一个反模式:先做 optimistic update,随后又 invalidate/refetch whole list。后续 refetch 或 WebSocket echo 会丢掉刚刚写入的 optimistic placement,造成 snap-back/flicker。过滤视图的 cache 也没有被同步 patch,只能靠整表 refetch 追数据。

## 正确修复模式

让单 issue cache patch 成为拖拽和单字段变更的主要同步机制。drop 时乐观更新目标位置,同时 patch workspace cache 和相关 filtered cache;mutation settle 后不要无条件 whole-list invalidate。WebSocket updater 收到非 membership 变化时也做就地 patch,只有 assignee/project 等可能改变过滤成员关系的字段变化才 invalidate 对应 filtered list。

## 为什么这个修法正确

React Query 负责 server state cache。对于已知实体的局部变化,最稳定的策略是同步 patch 所有相关 query cache,而不是先 patch 后立即用旧或中间态的列表结果覆盖它。membership 变化才需要重新计算列表归属。

## 验证方式

PR 覆盖了 cache helper、mutation optimistic path、WebSocket updater、board/list drag 行为和 filtered-board 同步的回归测试,并声明 core 与 views 测试、typecheck、eslint 通过。

## 适用边界

适用于 issue board/list 中的拖拽、批量更新、WebSocket 回放和过滤视图同步。真正改变查询成员关系的字段仍然可以触发有针对性的 invalidate。

## 不要照搬

不要用"为了保险再 refetch 一次整表"覆盖刚完成的 optimistic move;这类保险在交互路径上会直接变成用户可见抖动。
