---
id: pattern.daemon.autopilot_workdir_gc_terminal
kind: pattern
title: "Autopilot run workdirs can be reclaimed at terminal status"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4287
source_pr_number: 4287
source_issue_refs:
  - MUL-3392
  - MUL-3403
merged_at: 2026-06-18T03:18:00Z
merge_commit: e7daf876bdc7b75e1c9f1768daf7185bce24916e
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "High-frequency autopilot runs accumulate hundreds of stale workdirs"
  - "Terminal autopilot run directories remain for the full GC TTL even though they are never reused"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/gc.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: e7daf876bdc7b75e1c9f1768daf7185bce24916e
    content_hash: d30fff16306f51300ac81d3a5e8000109b3762c3
fix_pattern: "Classify autopilot_run workdirs by reuse semantics: terminal runs are dead weight and can be cleaned immediately, while pending/running and active env roots remain protected."
verification:
  - server/internal/daemon/gc_test.go
gotchas:
  - "Do not apply issue/chat task reuse rules to autopilot runs when autopilot has no PriorWorkDir handoff."
  - "Keep active env root and non-404 error safeguards intact."
related: []
tags:
  - server
  - daemon
  - gc
  - autopilot
---

# Autopilot run workdirs can be reclaimed at terminal status

## 症状

高频 autopilot 运行后,daemon 机器上积累大量 stale workdirs,例如数百个目录和几十 GB 空间。运行已经 terminal,目录仍等默认 24h GC TTL。

## 根因

autopilot run 的 workdir 不会被后续 run 复用,但 GC 逻辑沿用了 issue/chat task 的 TTL 思路。terminal 后 `completed_at` 只是诊断信息,不应该继续作为保留目录的 anchor。

## 正确修复模式

按任务类型的 reuse semantics 区分 GC。`autopilot_run` 达到 `completed`、`failed`、`skipped`、`issue_created` 等 terminal 状态后立即 clean。`pending` / `running` 仍 skip;active env root、404 orphan fallback、非 404 error skip、`local_directory` override 等安全约束保持不变。

## 为什么这个修法正确

GC 策略应围绕“目录是否可能还被使用”而不是统一 TTL。autopilot 输出已经在 server 侧持久化,`issue_created` 后后续 issue task 拥有自己的 envRoot,旧目录没有继续保留价值。

## 验证方式

PR 更新了 `TestShouldCleanTaskDir_KindDispatch` 的 autopilot cases,覆盖 within TTL terminal、无 completed_at skipped、failed、pending 等状态。

## 适用边界

适用于没有工作目录复用语义的 ephemeral run。issue/chat task 如存在 PriorWorkDir 或用户可见恢复能力,不能直接套用立即清理。

## 不要照搬

不要删除 `isActiveEnvRoot` 保护;终态判断仍要防止正在运行的 envRoot 被误删。
