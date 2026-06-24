---
id: pattern.agent.chat_output_context_boundary
kind: pattern
title: "Chat runtime briefs must not inherit issue-comment output rules"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4387
source_pr_number: 4387
source_issue_refs:
  - MUL-3446
merged_at: 2026-06-22T07:51:04Z
merge_commit: 4fe8b54e9bbb714aff9c6a5fefbca71d3f67077b
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Chat responses appear as issue comments or inherit issue-comment MUST guidance"
  - "A chat run is told to write output somewhere other than the chat window"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/execenv/runtime_config.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 4fe8b54e9bbb714aff9c6a5fefbca71d3f67077b
    content_hash: b2595bf839efa12eed86536345fb299903c56fe9
fix_pattern: "Keep output rules scoped by runtime context: chat briefs should default to chat-window output, while issue comments should only be allowed when the user explicitly names a target issue."
verification:
  - server/internal/daemon/execenv/runtime_config_test.go
gotchas:
  - "Do not let shared brief text for issue-bound runs leak into chat runs."
  - "Do not forbid explicit issue-comment requests in chat; only remove the default MUST guidance."
related: []
tags:
  - server
  - agent
  - chat
  - runtime-brief
---

# Chat runtime briefs must not inherit issue-comment output rules

## 症状

Chat 场景下的 agent response 没有稳定留在 chat window,而是继承了 issue-bound brief 里的 issue-comment 输出约束。用户只是开了 chat,但模型可能被提示去写 issue comment。

## 根因

runtime brief 的 Output 段落没有按运行上下文区分。issue-bound run 需要强调 issue comment 规则,但 chat run 的默认输出面应该是 chat window。共享一段输出规则会让 chat 继承 issue-comment MUST warning。

## 正确修复模式

为 chat 增加独立 Output 分支:默认把回答留在 chat window。只有当用户明确要求写入某个具体 issue 时,才允许 issue comment。issue-bound 输出规则继续保留在 issue 场景。

## 为什么这个修法正确

agent 输出位置是产品语义的一部分,不能只靠模型从上下文猜。把输出 contract 按 run surface 分支,能让同一个 runtime brief 生成器服务 chat 和 issue,但不混淆二者的默认行为。

## 验证方式

PR 增加了回归测试,确认 chat brief 不继承 issue-comment MUST warning,并运行 `go test ./internal/daemon/execenv`。

## 适用边界

适用于 runtime brief、prompt injection、reply instructions 这类按场景生成 agent 输出规则的代码。普通 UI 文案不需要套用这个 pattern。

## 不要照搬

不要把 issue comment 能力从 chat 中完全删除;用户显式要求写具体 issue 时仍应可用。
