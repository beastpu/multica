---
id: pattern.lark.originating_thread_reply
kind: pattern
title: "Lark bot replies should preserve the originating thread target"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4262
source_pr_number: 4262
source_issue_refs:
  - MUL-3378
merged_at: 2026-06-22T05:34:41Z
merge_commit: 0aa3b53c255a070d06e5688ab5913ded2c884445
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - server
signals:
  - "A bot mentioned inside a Lark topic replies at the group chat level"
  - "Outbound Lark send path only knows the chat binding and loses the inbound thread target"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/integrations/lark/chat.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 0aa3b53c255a070d06e5688ab5913ded2c884445
    content_hash: a693521a4b2d38a7c41c768f6313d86540f6d84e
fix_pattern: "Persist the latest inbound Lark message/thread target on the chat binding and route outbound replies through the thread reply endpoint only when the trigger came from a thread."
verification:
  - server/internal/integrations/lark/dispatcher_test.go
  - server/internal/integrations/lark/http_client_test.go
  - server/internal/integrations/lark/outbound_test.go
  - server/internal/integrations/lark/outcome_replier_test.go
  - server/internal/integrations/lark/ws_frame_decoder_test.go
gotchas:
  - "Do not create new threads for ordinary group messages."
  - "Do not plumb the trigger target through every task lifecycle layer if the binding can safely remember the latest trigger."
related: []
tags:
  - server
  - lark
  - threads
  - integrations
---

# Lark bot replies should preserve the originating thread target

## 症状

用户在 Lark topic/thread 里 @ bot,但 Multica 的回复出现在群聊主层级,而不是原 thread。普通群聊消息又必须保持原来的 chat-level send。

## 根因

outbound path 是事件驱动的,只从 `chat_session` / `lark_chat_session_binding` 找到 `lark_chat_id`,不知道触发消息的 `message_id` / `thread_id`。inbound thread 语义在写入任务后丢失。

## 正确修复模式

在 ingest 时把最近触发的 Lark `message_id` 和 `thread_id` 持久化到 binding。outbound 发送时,如果 binding 记录的是 thread trigger,走 Lark reply endpoint 并设置 `reply_in_thread=true`;非 thread 消息保持 chat-level send。失败时可以 fallback 到 chat-level send。

## 为什么这个修法正确

thread reply target 是触发上下文的一部分。把它持久在 binding 上,能让解耦的 outbound patcher 在不穿透整个 task lifecycle 的情况下恢复正确目标。

## 验证方式

PR 增加了 thread_id decoding、dispatcher forwarding、reply endpoint wire shape、thread vs chat-level routing、markdown/error card thread reply 和 fallback 的 Lark integration tests。

## 适用边界

适用于外部 IM 集成中 inbound trigger 和 outbound response 解耦的场景。若产品需要同一群内多 thread 并发强隔离,还需要更细粒度的 per-thread session 模型。

## 不要照搬

不要对所有 Lark 消息都用 reply endpoint;只有触发消息本身在 thread 中时才应 thread reply。
