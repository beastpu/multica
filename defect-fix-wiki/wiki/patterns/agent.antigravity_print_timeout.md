---
id: pattern.agent.antigravity_print_timeout
kind: pattern
title: "Antigravity print mode hidden timeout truncates long turns"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4462
source_pr_number: 4462
source_issue_refs:
  - "#4453"
  - MUL-3570
  - https://github.com/multica-ai/multica/issues/4453
merged_at: 2026-06-23T10:57:51Z
merge_commit: ac84b8c70c94299b4e0a3ccb003b800bacac867d
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - server
signals:
  - "Antigravity turns stop around five minutes while the daemon records completion"
  - "Output ends after the agent says it will wait for tests or a long command"
code_refs:
  - repo: multica-ai/multica
    path: server/pkg/agent/antigravity.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: ac84b8c70c94299b4e0a3ccb003b800bacac867d
    content_hash: 7ad24c878753a0c4c939f7bbc6d9797a46c78d40
fix_pattern: "When wrapping an external runtime, explicitly set runtime defaults that affect turn duration and translate runtime-local timeout markers into Multica timeout results instead of treating exit 0 as success."
verification:
  - server/pkg/agent/antigravity_test.go
gotchas:
  - "Do not infer no timeout from an omitted flag; the wrapped runtime may apply its own default."
  - "Do not treat exit code 0 as sufficient success when the runtime writes a timeout marker to its own log."
related: []
tags:
  - server
  - agent
  - runtime
  - timeout
---

# Antigravity print mode hidden timeout truncates long turns

## 症状

Antigravity turns reported as successful even though the visible narration stopped during a long wait, such as waiting for tests or a build to finish. The source PR describes the user report as repeated Antigravity disconnects, but the confirmed failure mode was a hidden print-mode timeout.

## 根因

`agy --print-timeout` defaults to `5m0s` when omitted. Multica treated an omitted timeout as "no cap" because `DefaultAgentTimeout = 0`, so `buildAntigravityArgs` did not pass the flag. Long commands could burn the runtime's five-minute print budget. `agy` then printed a timeout message and exited 0, so the daemon recorded the turn as completed.

## 正确修复模式

Always pass an explicit print timeout to the wrapped runtime. For Multica's no-cap case, pass a large sentinel value so the daemon's own idle/tool watchdogs remain the effective safety net. Also inspect runtime logs for known timeout markers and map them to a timeout result even when the process exits 0.

## 为什么这个修法正确

The hidden timeout lives inside the runtime process, while Multica's daemon watchdogs are outside it. The wrapper must align runtime-local defaults with Multica's timeout model and classify runtime-local timeout evidence before reporting task completion.

## 验证方式

The PR added tests for argument construction, print-timeout budget resolution, timeout-marker detection, and an end-to-end fake `agy` case where a timeout marker plus exit 0 returns a timeout result.

## 适用边界

Use this pattern for runtime adapters where an external CLI has hidden defaults or log-only failure markers. It is not a generic instruction to set every timeout to 24h.

## 不要照搬

Do not copy the Antigravity-specific marker detection to another runtime without verifying that runtime's actual log text and exit semantics.
