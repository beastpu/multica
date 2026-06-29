---
id: pattern.projects.delete_requires_admin
kind: pattern
title: "Project deletion must authorize against the project workspace"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4327
source_pr_number: 4327
source_issue_refs:
  - "#4220"
  - MUL-3438
  - https://github.com/multica-ai/multica/issues/4220
merged_at: 2026-06-21T15:54:58Z
merge_commit: 31d942d010df9db84486268aaae70bd3522f892b
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/views
  - server
signals:
  - "Plain workspace members can see or invoke project deletion"
  - "Project delete authorization depends on caller supplied context instead of the loaded project row"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/project.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 31d942d010df9db84486268aaae70bd3522f892b
    content_hash: 68b21d30f5a148dd5868e73519bb5bcba779ca75
fix_pattern: "For destructive project actions, load the project, authorize against the project's workspace membership, and hide the UI affordance for non-admin/non-owner users."
verification:
  - server/internal/handler/project_validation_test.go
gotchas:
  - "Do not trust only request context when the resource row has its own workspace identity."
  - "Do not rely on hidden UI alone; the handler must deny plain members."
related: []
tags:
  - packages-views
  - server
  - projects
  - permissions
---

# Project deletion must authorize against the project workspace

## 症状

普通 workspace member 可以看到或触发 project delete affordance,而删除 project 是高风险操作。

## 根因

删除授权没有严格使用 project row 所属 workspace 来判断操作者是否为 owner/admin。只依赖 caller supplied request context 容易在跨资源或路由上下文变化时放大权限漏洞。

## 正确修复模式

handler 先加载 project row,再基于该 project 的 workspace membership 检查用户是否 owner/admin。前端也同步隐藏 non-admin member 的 delete action,减少误导,但真正的权限边界在服务端。

## 为什么这个修法正确

删除是 resource-scoped write。权限判断必须绑定被删除资源本身,而不是只绑定请求入口上的 workspace hint。

## 验证方式

PR 增加了 plain member denied 和 admin allowed 的回归测试,并覆盖相关 project handler validation cases。

## 适用边界

适用于 project、runtime、workspace 等 destructive actions。读操作或普通编辑也应同样确认资源归属,但风险等级不同。

## 不要照搬

不要只改按钮可见性;任何客户端都可能直接调用 API。
