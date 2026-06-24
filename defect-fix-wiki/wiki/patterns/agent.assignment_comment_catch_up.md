---
id: pattern.agent.assignment_comment_catch_up
kind: pattern
title: "Assignment-triggered agents must catch up on recent comments"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4392
source_pr_number: 4392
source_issue_refs:
  - MUL-3502
merged_at: 2026-06-22T07:46:47Z
merge_commit: 5fd3d01d13609aca6c395f87045f979c9116b7d5
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "Assigned agent acts on stale or incomplete issue context"
  - "Earlier comments contain repo, prior findings, or reassignment reasons that the agent misses"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/execenv/reply_instructions.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 5fd3d01d13609aca6c395f87045f979c9116b7d5
    content_hash: 40ada85375296cd32f5e992cdd0ffbaa73c37de4
fix_pattern: "For assignment-triggered runs, require a recent comment catch-up command before planning, while preserving explicit pagination for older threads and keeping comment-triggered tail behavior unchanged."
verification:
  - server/internal/daemon/execenv/execenv_test.go
  - server/internal/daemon/prompt_test.go
gotchas:
  - "Do not make comment history reading optional for cold assignment runs."
  - "Do not replace thread/tail behavior for comment-triggered runs when only assignment-triggered catch-up is broken."
related: []
tags:
  - server
  - agent
  - comments
  - prompt
---

# Assignment-triggered agents must catch up on recent comments

## 症状

Agent 因 assignment 被唤醒后,可能只读 issue body 或触发上下文,没有阅读近期 comment threads。结果是漏掉 repo 位置、前一个 agent 的结论、重新分配原因等关键信息,然后按过期理解行动。

## 根因

assignment-triggered workflow 的 prompt 没有强制近期评论 catch-up。comment-triggered run 有 thread/tail 语义,但冷启动 assignment run 缺少等价的“先读最近活跃评论”步骤。

## 正确修复模式

在 assignment workflow 中明确要求先执行 `multica issue comment list <issue-id> --recent 10 --output json`。如果 recent window 显示还需要更旧上下文,再用 `Next thread cursor`、`--before`、`--before-id` 分页。comment-triggered 的 thread/tail 行为保持不变,只收紧跨线程 fallback。

## 为什么这个修法正确

assignment 是接手工作,不是从零解释 issue body。近期评论是任务状态的一部分,必须在计划前读取。把 catch-up 写进 runtime guidance,能减少 agent 因上下文缺失做错方向的概率。

## 验证方式

PR 增加/更新了 prompt 和 execenv 测试,覆盖 assignment trigger mentions recent、cold start read、comment trigger 行为和默认 prompt 文案。

## 适用边界

适用于 agent 被分配、重新分配、接力处理 issue 的工作流。用户刚刚在同一 thread 里直接触发 agent 的场景,仍应优先保留 thread/tail 语义。

## 不要照搬

不要无脑分页读取所有历史评论;默认 recent 10 是冷启动 catch-up,更旧线程应在发现需要时再分页。
