---
id: pattern.issues.timeline_unknown_status_fallback
kind: pattern
title: "Timeline icons must tolerate unknown status and priority values"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4206
source_pr_number: 4206
source_issue_refs:
  - "#4202"
  - https://github.com/multica-ai/multica/issues/4202
merged_at: 2026-06-17T06:56:10Z
merge_commit: d26cac0008342bcc60b809e0288565c72601e770
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/views
signals:
  - "Issue timeline crashes when activity details.to is an unknown status"
  - "Backend or stored activity data contains enum values the current client does not recognize"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/issues/components/status-icon.tsx
    line_hint: 1
    ref_kind: root_cause
    verified_commit: d26cac0008342bcc60b809e0288565c72601e770
    content_hash: 81d6b8032cccde8548fca566e37e00b2c05a817c
fix_pattern: "Render known enum values with configured icons/colors and unknown activity values with a muted fallback instead of throwing."
verification:
  - packages/views/issues/components/issue-detail.test.tsx
  - packages/views/issues/components/status-icon.test.tsx
gotchas:
  - "Do not assume activity timeline enum values are limited to the current frontend's static union."
  - "Preserve the activity text even when icon rendering falls back."
related: []
tags:
  - packages-views
  - issues
  - compatibility
  - enum-drift
---

# Timeline icons must tolerate unknown status and priority values

## 症状

Issue activity timeline 渲染到未知 status 或 priority 字符串时触发 error boundary。用户看不到 timeline,但原始 activity 文本其实还能表达发生了什么。

## 根因

icon renderer 假设 activity 中的 `details.to` 一定是当前前端知道的 enum。实际 API/历史数据/后端漂移可能传来未知值,静态配置查不到对应 icon/color 后抛错。

## 正确修复模式

已知 status/priority 继续使用配置的 icon/color。未知值渲染 muted fallback icon,同时保留 activity 文本,让 corrupted 或未来 enum 值不会让整个 timeline 崩掉。

## 为什么这个修法正确

timeline 是历史事件视图,必须兼容旧数据和后端枚举演进。未知值应降级展示,而不是把页面交给 error boundary。

## 验证方式

PR 增加了 status icon fallback 测试,并覆盖 issue detail activity timeline 中 unknown `details.to` 的崩溃路径。

## 适用边界

适用于 activity/log/history 视图中的 server-driven enum。编辑表单保存当前值时仍应使用更严格的校验。

## 不要照搬

不要在 mutation path 默默接受未知 enum;这里的宽容只用于展示历史或漂移数据。
