---
id: pattern.editor.mention_selection_identity
kind: pattern
title: "@mention selection must track item identity, not slot index"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4488
source_pr_number: 4488
source_issue_refs:
  - MUL-3607
merged_at: 2026-06-24T02:53:51Z
merge_commit: 8a0934c7411cd4c7bb38fbdffe5eb22235a0bf71
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/views
signals:
  - "@mention popup highlights one row but commits a neighboring item"
  - "Selection jumps back to the first row when async mention results arrive"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/editor/extensions/mention-suggestion.tsx
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 8a0934c7411cd4c7bb38fbdffe5eb22235a0bf71
    content_hash: b246bb068869537e40fb980b5bdde4d013406cc4
fix_pattern: "For async, regrouped suggestion lists, keep selected item identity as state and derive the numeric index from the rendered order."
verification:
  - packages/views/editor/extensions/mention-suggestion.test.tsx
gotchas:
  - "Do not store selection as an index into raw data when render order is produced by a grouping function."
  - "Do not reset active selection to row 0 just because a fresh result array arrived."
related: []
tags:
  - packages-views
  - editor
  - mentions
  - async-results
---

# @mention selection must track item identity, not slot index

## 症状

用户在 @mention popup 中看到高亮的是 A,按 Enter 或点击后实际插入的是邻近的 B。异步 search result 到达后,当前选择还可能跳回第一行。

## 根因

组件用 `selectedIndex` 指向 `displayItems`,但实际渲染顺序由 `groupItems()` 重新分桶得到。server search 结果追加在数据末尾,渲染时又被提升到前面,于是 data order 和 rendered order 分裂。另一个 `useEffect` 在 `displayItems` 引用变化时强制 `setSelectedIndex(0)`,进一步放大了异步结果导致的跳选。

## 正确修复模式

把选择状态改成 item identity,例如 `type:id`。先派生出实际渲染的 `orderedItems`,再用 `selectedKey` 在这个顺序里解析数字 index。键盘导航、Enter、点击、高亮和滚动都只使用 `orderedItems` 这一套 index space。

## 为什么这个修法正确

异步列表的长度和分组都会变化,slot index 不是稳定目标。identity 在重排时仍然指向同一个实体,当实体消失时再显式 fallback 到第一行。

## 验证方式

PR 增加了 reordering list 回归测试,确认被高亮的 row 与提交的 item 一致,包括初始状态和向下移动后。

## 适用边界

适用于 search/autocomplete/mention picker 这类会异步追加结果、同时又按分组渲染的列表。

## 不要照搬

不要只把 off-by-one 修在 click 或 Enter 的单个 handler 上;只要存在两套 index space,其它路径还会继续漂移。
