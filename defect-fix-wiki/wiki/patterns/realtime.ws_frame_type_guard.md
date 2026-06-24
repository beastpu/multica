---
id: pattern.realtime.ws_frame_type_guard
kind: pattern
title: "WebSocket clients must validate frame type at the trust boundary"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4304
source_pr_number: 4304
source_issue_refs:
  - MUL-3418
merged_at: 2026-06-18T09:24:48Z
merge_commit: 9eaea31892f555e1f6cd81c4ea205fe97f369dbf
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/core
signals:
  - "Client telemetry floods with TypeError reading split of undefined from realtime sync"
  - "Parsed WebSocket JSON without string type reaches onAny subscribers"
code_refs:
  - repo: multica-ai/multica
    path: packages/core/api/ws-client.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 9eaea31892f555e1f6cd81c4ea205fe97f369dbf
    content_hash: 447df46b12e494938973fb081eaa109bb56f2899
fix_pattern: "Validate incoming WebSocket frames once at the client boundary; require an object with a string type before dispatching to onAny or typed subscribers."
verification:
  - packages/core/analytics/benign-exceptions.test.ts
  - packages/core/api/ws-client.test.ts
gotchas:
  - "Do not patch only the downstream split call; malformed frames should never enter the dispatcher."
  - "Rate-limit or suppress known benign telemetry separately from root-cause protocol guards."
related: []
tags:
  - packages-core
  - realtime
  - websocket
  - telemetry
---

# WebSocket clients must validate frame type at the trust boundary

## 症状

PostHog `$exception` 中大量出现 `Cannot read properties of undefined (reading 'split')`。连接没有真正崩溃,后续 frames 还能处理,但错误淹没了真实客户端异常。

## 根因

`onmessage` 解析 JSON 后直接 dispatch 给 `onAny` 和所有 subscribers,没有验证 frame shape。缺少 string `type` 的 out-of-protocol frame 到达 realtime sync 后,`msg.type.split(":")` 抛错。

## 正确修复模式

在 WebSocket client 边界统一校验 frame:必须是 object 且 `type` 为 string,否则丢弃并按连接限频记录。下游 handler 可以继续假设协议 frame 合法。

## 为什么这个修法正确

WebSocket 是网络信任边界。即使 server 当前不会发 malformed frame,代理、扩展、版本漂移都可能制造异常输入。边界校验能保护整个 dispatch path,比在单个 subscriber 里做 optional chaining 更彻底。

## 验证方式

PR 增加了 bad-frame guard 的 `ws-client` 回归测试,并增加 benign ResizeObserver exception suppression 测试来降低 telemetry 噪声。

## 适用边界

适用于客户端实时协议入口、事件总线入口、任何会 fan-out 给多个 handler 的消息解析点。

## 不要照搬

不要把所有异常都吞掉;只有明确 benign 的浏览器噪声才应在 analytics before_send 中丢弃。
