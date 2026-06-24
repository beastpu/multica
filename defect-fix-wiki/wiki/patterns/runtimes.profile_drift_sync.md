---
id: pattern.runtimes.profile_drift_sync
kind: pattern
title: "Daemons must detect runtime profile drift without restart"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4225
source_pr_number: 4225
source_issue_refs:
  - MUL-3332
merged_at: 2026-06-17T04:36:31Z
merge_commit: 6bb8cac9ea197bfbb6c16ba1984d561cbec9872f
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "New custom runtime profile does not appear until daemon restart"
  - "Already-tracked workspace sync refreshes repos but not runtime profiles"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/daemon.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 6bb8cac9ea197bfbb6c16ba1984d561cbec9872f
    content_hash: 5ea7bb65b8fe7650c30a3fed07291b499763e110
fix_pattern: "Compute a stable signature over registration-affecting runtime profile fields during workspace sync and re-register only when the signature changes."
verification:
  - server/internal/daemon/runtime_profile_drift_test.go
  - server/internal/daemon/runtime_profile_test.go
gotchas:
  - "Do not rely on frontend daemon:register events to update daemon state; those events are not relayed to the daemon."
  - "Do not trigger re-registration on row reorder or transient fetch errors."
related:
  - pattern.runtimes.custom_runtime_args_registration_errors
  - pattern.runtimes.clear_incompatible_model_on_switch
tags:
  - server
  - daemon
  - runtime
  - profile-drift
---

# Daemons must detect runtime profile drift without restart

## 症状

用户通过 Web UI 或 CLI 创建 custom runtime profile 后,新 runtime 不出现在 daemon 的 runtime list 中,必须重启 daemon 才能看到。

## 根因

`syncWorkspacesFromAPI` 对已跟踪 workspace 只刷新 settings/repos,没有重新拉取 runtime profiles。新 workspace 分支和 runtime gone recovery 会注册 profile runtimes,但普通 sync tick 不会。server 虽发布 profile create/update/delete 事件,但 daemon WebSocket hub 只 relay task available,daemon 收不到这些事件。

## 正确修复模式

在 workspace sync tick 中拉取 runtime profiles,对影响注册的字段计算 order-independent signature,与上次成功 fetch 的 signature 比较。有 drift 时复用 runtime gone recovery 的 re-register converger。fetch error 保留旧 signature,避免抖动;无变化时不重复 Register。

## 为什么这个修法正确

daemon profile state 是 server state 的缓存。没有事件通道时,轮询 sync 必须覆盖 profile drift,但要用 signature 避免每 30 秒无意义注册。

## 验证方式

PR 增加了 signature reorder stability、registration-affecting fields、no drift no reregister、新 profile triggers reregister、fetch error best-effort 等 daemon tests。

## 适用边界

适用于 daemon 缓存 server-side 配置但缺少可靠事件推送的场景。有实时事件通道时,仍可保留签名作为兜底。

## 不要照搬

不要把 transient profile fetch failure 当 drift;否则会让 daemon 进入无意义 re-register loop。
