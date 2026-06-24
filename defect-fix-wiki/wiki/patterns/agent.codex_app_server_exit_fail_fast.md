---
id: pattern.agent.codex_app_server_exit_fail_fast
kind: pattern
title: "Codex app-server exits should become sticky transport failures"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4228
source_pr_number: 4228
source_issue_refs:
  - MUL-2840
merged_at: 2026-06-17T06:34:29Z
merge_commit: 114a1ffb8fdc48f3b7f2d3cbba5d47d4ac1ef341
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "JSON-RPC requests wait until the outer task context after Codex app-server exits"
  - "thread/resume falls back to thread/start even though the transport process has died"
  - "Active turn hangs when app-server exits after turn/start but before final events"
code_refs:
  - repo: multica-ai/multica
    path: server/pkg/agent/codex.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 114a1ffb8fdc48f3b7f2d3cbba5d47d4ac1ef341
    content_hash: 3a7803127a80843d4da116d9db22701dadf140af
fix_pattern: "Record app-server process exit as sticky transport state, fail future requests immediately, and distinguish process EOF from caller cancellation or timeout."
verification:
  - server/pkg/agent/codex_test.go
gotchas:
  - "Do not fall back from resume to start when the failure is transport/process-exit."
  - "Do not misreport user cancellation or task timeout as a Codex process exit."
related:
  - pattern.agent.codex_permission_auto_grant_observability
tags:
  - server
  - agent
  - codex
  - transport
---

# Codex app-server exits should become sticky transport failures

## 症状

Codex app-server 退出后,后续 JSON-RPC request 仍等待外层 task context,而不是立刻失败。`thread/resume` 可能错误 fallback 到 `thread/start`。active turn 在 `turn/start` 已接受但最终事件未到达时,也可能等到超时才结束。

## 根因

process exit 没被记录成 sticky transport state。调用路径只能从当前 request/context 观察失败,无法知道 transport 已经永久不可用,也无法区分 process EOF 与调用方取消/超时。

## 正确修复模式

app-server process exit 后记录 sticky transport failure。未来 request 直接 fail fast。resume 遇到 transport/process-exit 不再 fallback start。active turn 如果 process 在 final events 前退出,立即失败;如果 cancellation/timeout 杀死进程,优先报告 caller context terminal state。

## 为什么这个修法正确

进程退出是连接级状态,不是单个 request 的普通错误。把它提升为 transport state 可以避免无意义等待和错误 fallback,同时保留 cancellation/timeout 的真实语义。

## 验证方式

PR 增加 request-after-exit、resume-after-exit、active-turn process-exit、timeout/cancel-vs-process-EOF 等 Codex regression tests。

## 适用边界

适用于代理 runtime 通过子进程 JSON-RPC/app-server 通信的场景。普通业务 API 的单次 5xx 不应变成 sticky failure。

## 不要照搬

不要把所有 EOF 都报告成 process exit;如果调用方 context 已取消或超时,应保留用户可理解的取消/超时结果。
