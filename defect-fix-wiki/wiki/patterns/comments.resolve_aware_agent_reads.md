---
id: pattern.comments.resolve_aware_agent_reads
kind: pattern
title: "Agent comment reads should fold resolved threads by default"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4463
source_pr_number: 4463
source_issue_refs:
  - MUL-3555
merged_at: 2026-06-24T01:52:18Z
merge_commit: b79777caece68b20548521bc70778c24c52baca0
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Agents pay tokens for long resolved comment discussions"
  - "Human timeline shows resolved conclusions but CLI comment reads return raw settled threads"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/comment.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: b79777caece68b20548521bc70778c24c52baca0
    content_hash: 768fc03236fc89e6de0abf7ad685ee478bd73c6b
fix_pattern: "Expose an opt-in server fold projection for complete-thread reads, make the CLI fold resolved threads by default, and keep --full as the escape hatch."
verification:
  - server/cmd/multica/cmd_issue_test.go
  - server/internal/handler/comment_fold_test.go
gotchas:
  - "Do not fold partial thread reads such as since, tail, or roots-only; folding them can hide an unfetched resolution."
  - "Keep root plus conclusion for reply-resolved threads; a conclusion alone is often referential."
related:
  - pattern.agent.assignment_comment_catch_up
tags:
  - server
  - comments
  - agent
  - token-budget
---

# Agent comment reads should fold resolved threads by default

## 症状

Agent 读取长 issue comment 时,会把已经 resolved 的讨论也完整读进上下文,浪费 token。人类 timeline 已经能折叠 resolved thread,但 `multica issue comment list` 给 agent 的路径仍返回原始讨论。

## 根因

resolve 机制只接到了 human UI,agent read path 没有消费 `resolved_at`。CLI 默认读取完整 thread 时不知道哪些讨论已经沉淀为结论。

## 正确修复模式

在 server `ListComments` 增加 `fold=true` projection: unresolved thread 保持原样;reply-resolved 保留 root + conclusion;root-resolved 保留 root。CLI 对完整 thread read 默认发送 fold,并提供 `--full` 展开。`since`、`tail`、`roots_only` 等 partial read 不折叠并拒绝不安全组合。

## 为什么这个修法正确

agent 需要结论而不是每条已 settled 消息。fold 作为 projection 不改变底层数据,还能让 CLI 与 human timeline 的 resolved thread 心智保持一致。

## 验证方式

PR 增加了 comment fold 的 DB 测试,覆盖 reply-resolved、root-resolved、unresolved、latest-reply tiebreak、recent/summary/thread 组合和不安全参数拒绝。CLI 测试覆盖默认 fold 与 `--full` escape。

## 适用边界

适用于 agent comment reads 和 token-sensitive history loading。不适用于审计、调试或用户明确要完整历史的场景。

## 不要照搬

不要在 SQL 层或存储层删除 resolved replies;这是读取投影,不是数据压缩。
