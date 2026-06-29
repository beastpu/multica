---
id: pattern.agent.codex_permission_auto_grant_observability
kind: pattern
title: "Auto-granted permission protocols need observable narrowing"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4390
source_pr_number: 4390
source_issue_refs:
  - MUL-3451
merged_at: 2026-06-22T05:43:42Z
merge_commit: b13e1808a426541ad48b3f34145c276a4afe3baf
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Codex permission approval auto-grant silently ignores malformed params"
  - "New permission keys can be narrowed away with no warning"
code_refs:
  - repo: multica-ai/multica
    path: server/pkg/agent/codex.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: b13e1808a426541ad48b3f34145c276a4afe3baf
    content_hash: d6b2ed6029760c7e7b2997117b103b15eb9e253b
fix_pattern: "When auto-granting a protocol-shaped permission request, preserve the wire response but warn on malformed params and unknown permission keys so protocol drift is observable."
verification:
  - server/pkg/agent/codex_test.go
gotchas:
  - "Do not silently unmarshal into an empty grant when params are malformed."
  - "Do not treat unknown future permission keys as harmless; log them before narrowing."
related: []
tags:
  - server
  - agent
  - codex
  - permissions
  - observability
---

# Auto-granted permission protocols need observable narrowing

## 症状

Codex `item/permissions/requestApproval` 被 daemon 自动批准后,外部协议里新增或异常的 permission shape 可能被静默丢弃。系统仍返回成功 grant,但没有日志说明请求被缩窄。

## 根因

auto-grant path 只读取已知的 `network` / `fileSystem` 字段。旧实现对 malformed `params` 忽略 unmarshal error,未知 key 也自然落在硬编码分支之外,因此协议漂移和错误 payload 都不可观测。

## 正确修复模式

保持 grant response shape 不变,但把解析改成显式 key loop。malformed params 记录 WARN,未知 permission key 也记录 WARN,让未来 app-server 协议扩展或 payload 变形能在日志中暴露。

## 为什么这个修法正确

auto-grant 是安全/权限边界的一部分。即使产品决定继续自动批准当前 turn scope,也必须知道自己是否丢弃了请求中的新语义,否则后续 timeout 或 unsupported request 会变成难排查的隐性兼容问题。

## 验证方式

PR 增加了 `TestCodexPermissionsApprovalResponseDropsUnknownKeysAndLogs` 和 `TestCodexPermissionsApprovalResponseMalformedParamsLogs`,并运行 `go test ./pkg/agent -count=1`。

## 适用边界

适用于 agent runtime 与外部 app-server/CLI 协议对接处,尤其是自动接受、降级或兼容旧协议的路径。

## 不要照搬

不要借 observability 改变 wire response,除非当前 issue 明确要求协议行为变化;这类修复应先暴露漂移,再另行设计兼容策略。
