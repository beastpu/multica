---
id: pattern.runtimes.clear_incompatible_model_on_switch
kind: pattern
title: "Runtime switches should clear known-incompatible saved models"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4233
source_pr_number: 4233
source_issue_refs:
  - MUL-3341
merged_at: 2026-06-17T06:23:20Z
merge_commit: eb6dffdbc6fac93f810f910b2d0415b23ad6fb6c
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Agent switches runtime but keeps a saved model known to be incompatible with the new runtime"
  - "Runtime switch without explicit replacement model leaves stale provider-specific model state"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/agent.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: eb6dffdbc6fac93f810f910b2d0415b23ad6fb6c
    content_hash: 4f37fcab45a73bfdfa97b7b5533bbd9cb684ca8f
fix_pattern: "When changing an agent runtime without an explicit model replacement, clear only saved models that are known incompatible with the destination runtime, while preserving explicit updates and unknown custom model strings."
verification:
  - server/internal/handler/agent_thinking_test.go
  - server/pkg/agent/models_test.go
gotchas:
  - "Do not clear unknown custom model IDs just because the compatibility classifier cannot prove them valid."
  - "Do not override an explicit model update supplied with the runtime switch."
related:
  - pattern.runtimes.custom_runtime_args_registration_errors
tags:
  - server
  - runtime
  - models
  - compatibility
---

# Runtime switches should clear known-incompatible saved models

## 症状

用户把 agent 从一个 runtime 切到另一个 runtime,但请求没有提供新的 model。旧 runtime 保存的 provider-specific model 被保留下来,导致新 runtime 使用一个已知不兼容的 model。

## 根因

runtime 和 model 是相关字段。更新 runtime 时如果没有显式 model replacement,旧 model 不能盲目保留;但也不能无脑清空,因为未知 custom model string 可能对目标 runtime 是合法的。

## 正确修复模式

引入兼容性分类器。runtime switch 且未显式提供 replacement model 时,只清除 known-incompatible model。显式 model update 原样尊重;未知 custom model string 保留,交给运行时或后续验证处理。

## 为什么这个修法正确

这是 API compatibility 与用户意图的平衡:明确错误的状态要自动修正,用户明确提交的新值不能被覆盖,系统无法分类的 custom 值也不应被保守删除。

## 验证方式

PR 增加 Claude-to-Codex runtime switch 的回归覆盖,并为 compatibility classifier 增加 unit coverage。

## 适用边界

适用于 runtime/provider/model 三者强相关的 agent 配置更新。普通独立字段更新不需要这种联动清理。

## 不要照搬

不要把 unknown 等同 invalid;custom runtime 和自定义模型场景会被误伤。
