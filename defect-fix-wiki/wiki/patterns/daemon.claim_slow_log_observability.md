---
id: pattern.daemon.claim_slow_log_observability
kind: pattern
title: "Restore diagnostics separately from reverted transport behavior"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4322
source_pr_number: 4322
source_issue_refs:
  - MUL-3433
merged_at: 2026-06-21T15:41:06Z
merge_commit: 745832b5366d8b4a55deabd3feb5485633412990
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "A revert correctly removes gzip middleware but also removes useful slow-log payload fields"
  - "Claim endpoint response-size diagnostics disappear even though wire behavior should stay unchanged"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/handler/daemon.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 745832b5366d8b4a55deabd3feb5485633412990
    content_hash: 78775541b26dbdc9b855c6157101e310bccd7931
fix_pattern: "When a revert removes both behavior and observability, restore only the diagnostic layer with byte-identical response tests and no transport middleware changes."
verification:
  - server/internal/handler/daemon_test.go
  - server/internal/handler/handler_writejson_test.go
gotchas:
  - "Do not reintroduce the reverted gzip middleware when the issue is missing observability."
  - "Prove that measured JSON output is byte-identical to the normal writer before calling it no wire change."
related: []
tags:
  - server
  - daemon
  - observability
  - revert-followup
---

# Restore diagnostics separately from reverted transport behavior

## 症状

`/api/daemon/tasks/claim` 的慢请求日志失去了 response size、skill count、skill payload bytes 等诊断字段。原因是一次正确 revert gzip middleware 时,把同一 PR 中的 observability 改动也一并回滚了。

## 根因

原改动把 transport compression 和 response measurement 放在一起。revert 目标是 gzip wire behavior,但 collateral damage 删除了不改变 wire 的 measured JSON helper 和慢日志字段。

## 正确修复模式

只恢复 observability:增加能返回 encoded body size 的 JSON writer,但保证输出和原 `writeJSON` byte-identical。claim response 构建处采集 payload bytes、agent/builtin skill count、skill payload bytes,写入 slow-log。明确不改 router/middleware/compression。

## 为什么这个修法正确

修复 observability 不能重新引入刚被 revert 的行为风险。用 byte-identical 测试锁住 wire contract,可以安全恢复诊断信号。

## 验证方式

PR 增加 `TestWriteMeasuredJSONByteIdenticalToWriteJSON`,覆盖 HTML escaping、unicode、嵌套结构、大 payload 等输入,并恢复 slow-log 字段测试。

## 适用边界

适用于 revert 后发现“诊断能力被误删”的情况。不是鼓励把已 revert 的产品行为拆开再加回来。

## 不要照搬

不要声称 no wire change 却没有 byte-level 测试;日志 helper 很容易不小心改变 headers/status/body。
