---
id: pattern.runtimes.custom_runtime_profile_delete
kind: pattern
title: "Custom runtime deletion must remove the profile definition"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4456
source_pr_number: 4456
source_issue_refs:
  - MUL-3571
merged_at: 2026-06-23T08:52:46Z
merge_commit: 294953ba379d0a9a24fd86693e95dbe5180ff8f5
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - packages/views
  - server
signals:
  - "Deleting a custom runtime appears to succeed but the daemon recreates the runtime later"
  - "Runtime UI delete action removes only a daemon-registered instance row"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/runtimes/components/runtime-detail.tsx
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 294953ba379d0a9a24fd86693e95dbe5180ff8f5
    content_hash: 2bd2a6aa39a42f01a202667493bc04ce4bc70bdd
fix_pattern: "Route custom runtime delete actions through runtime profile deletion, hide actions that the actor cannot perform, and reject direct deletion of profile-backed runtime instances at the API boundary."
verification:
  - packages/views/runtimes/components/runtime-detail-visibility.test.tsx
  - packages/views/runtimes/components/runtime-row-menu.test.tsx
  - server/internal/handler/runtime_cascade_test.go
gotchas:
  - "Do not treat a profile-backed runtime instance row as the durable runtime definition."
  - "Do not let the UI report success for direct instance deletion when the daemon can recreate that instance."
related: []
tags:
  - packages-views
  - server
  - runtime
  - permissions
---

# Custom runtime deletion must remove the profile definition

## 症状

用户从 Runtime UI 删除 custom runtime 后看起来成功,但 daemon 后续可能重新注册同一个 runtime。列表和详情页删除入口的行为也可能不一致。

## 根因

custom runtime 的 durable source 是 runtime profile definition,不是 daemon 注册出来的 runtime instance row。UI 删除路径命中了 instance delete,于是只删掉派生行;profile 仍在,daemon 重启或同步时会重新创建 instance。API 也允许直接删除 profile-backed instance,导致客户端可以展示一个并不持久的成功。

## 正确修复模式

把 runtime list row 和 runtime detail diagnostics 的 custom runtime 删除都路由到 runtime profile delete dialog。对非 admin runtime owner 隐藏会被拒绝的 delete action,同时保留其可执行的 visibility editing。后端对 profile-backed runtime instance 的直接删除返回 409,防止任何客户端误报成功。

## 为什么这个修法正确

删除操作必须作用在资源的权威定义上。profile-backed runtime instance 是 daemon 同步出来的派生状态,删除它不能表达"删除 custom runtime"。前端入口和后端边界同时收敛,才能避免其它客户端绕过 UI 复现同类问题。

## 验证方式

PR 增加了 runtime detail 和 row menu 的 custom-runtime delete 覆盖,并增加后端 guard tests,验证 direct instance deletion 和 archive cascade 下的 profile-backed runtime instance 会被拒绝。

## 适用边界

适用于存在 definition/profile 与 runtime instance 两层模型的资源。普通非 profile-backed runtime instance 删除不能直接套用 profile delete 流程。

## 不要照搬

不要只在前端隐藏按钮;后端 API 必须拒绝错误层级的删除,否则其它入口仍可能制造假成功。
