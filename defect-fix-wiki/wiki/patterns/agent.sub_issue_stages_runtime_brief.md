---
id: pattern.agent.sub_issue_stages_runtime_brief
kind: pattern
title: "New workflow primitives must be surfaced in the always-on runtime brief"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4426
source_pr_number: 4426
source_issue_refs:
  - MUL-3508
merged_at: 2026-06-22T17:08:10Z
merge_commit: 48b8dbf43971e5ea974bf827220cd212a1240c72
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Agents keep hand-managing backlog chains after a stage feature ships"
  - "A new CLI workflow is documented in a skill but absent from the always-on runtime brief"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/execenv/runtime_config.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 48b8dbf43971e5ea974bf827220cd212a1240c72
    content_hash: d1b82a3bb58984138a9aad86be4a2472c159b61d
fix_pattern: "When a workflow primitive should change default agent behavior, add the nudge to the always-on runtime brief, not only to optional skills or docs."
verification:
  - server/internal/daemon/execenv/runtime_config_test.go
gotchas:
  - "Do not assume agents will open a domain skill before making sub-issues."
  - "Do not add parent-notification details to a brief section that is only supposed to guide sub-issue creation."
related:
  - pattern.issues.sub_issue_stage_barrier
tags:
  - server
  - agent
  - runtime-brief
  - issues
  - stages
---

# New workflow primitives must be surfaced in the always-on runtime brief

## 症状

Sub-issue stages 已经上线,但 agent 创建 sub-issues 时仍默认用 `--status todo` / `--status backlog` 手工串链。`--stage` 只写在工作技能里,agent 没打开技能时就不会使用新机制。

## 根因

always-on runtime brief 是每个 issue-bound run 都会看到的高优先级提示,但它仍只有旧的 sub-issue creation guidance。新 primitive 没出现在默认 brief,导致 agent 行为没有随功能上线改变。

## 正确修复模式

把 stage nudge 加进 always-on brief 的 `## Sub-issue Creation` 段落:有阶段或依赖时用 `--stage <N>` 分组;同 stage 可并行;整个 stage 完成才唤醒 assignee;第一阶段通常 todo,后续阶段 backlog;无 stage 表示一个隐式组。命令列表也要包含 `issue children` 以便查看布局。

## 为什么这个修法正确

技能文档是按需读取,always-on brief 才是默认行为入口。会改变 agent 默认工作方式的 primitive,必须出现在默认 brief,否则功能只对读到对应技能的 agent 生效。

## 验证方式

PR 扩展了 `TestSubIssueCreationSectionPresentForIssueRuns`,同时保留 skip、无 parent_issue_id、无 parent-notification 的 canary 测试,并运行 `go test ./internal/daemon/execenv/`。

## 适用边界

适用于 agent 默认行为必须改变的工作流能力。低频、危险或只在特定任务中使用的能力仍可留在专门 skill 里,避免污染默认 brief。

## 不要照搬

不要把所有新功能都塞进 always-on brief;只放那些不提示就会导致 agent 继续走旧错误路径的工作流 primitive。
