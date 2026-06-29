---
id: pattern.editor.inflight_upload_external_sync_guard
kind: pattern
title: "External content sync must not wipe in-flight upload nodes"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4196
source_pr_number: 4196
source_issue_refs:
  - MUL-3312
merged_at: 2026-06-16T09:57:17Z
merge_commit: f46b929ebc7b306a0f7ba1d0e840f485e471c90d
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/core
  - packages/views
signals:
  - "First upload after switching chat agent vanishes and leaves empty markdown file syntax"
  - "Session creation changes draftKey mid-upload and external defaultValue sync clears editor content"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/editor/content-editor.tsx
    line_hint: 1
    ref_kind: root_cause
    verified_commit: f46b929ebc7b306a0f7ba1d0e840f485e471c90d
    content_hash: c709991f4271e3ccf7b504421814c33689bc481d
fix_pattern: "Treat uploading editor nodes as local in-flight state and skip external defaultValue synchronization while any upload node is present."
verification:
  - packages/core/chat/store.test.ts
  - packages/views/chat/components/chat-input.test.tsx
  - packages/views/editor/content-editor.test.tsx
gotchas:
  - "Do not fix this by remounting the editor; the confirmed wipe happens inside content sync."
  - "Do not let draftKey/session changes overwrite local uploading nodes."
related:
  - pattern.editor.mention_selection_identity
tags:
  - packages-core
  - packages-views
  - editor
  - uploads
  - chat
---

# External content sync must not wipe in-flight upload nodes

## 症状

用户在新 chat 中切换 agent 后立刻上传文件,文件节点先插入,随后消失,最终只留下空的 `!file[name]()`。等更久再上传不解决;第二次上传或已有 session 的 chat 正常。

## 根因

切换 agent 后 chat 尚无 session。第一次上传插入 uploading fileCard,随后上传 handler 懒创建 session 并把 `activeSessionId` 从 null 改为 uuid。这个变化改变 `draftKey`,新 key 的 draft 是空字符串,`ContentEditor` 的 external `defaultValue` sync effect 执行 `setContent("")`,把仍在上传的 node 擦掉。上传完成后 finalize 找不到 node,无法写入真实 href。

## 正确修复模式

在 content-sync effect 顶部检查 editor 是否包含 uploading node。只要有 in-flight upload,就跳过外部 `defaultValue` 同步。上传节点是本地临时状态,优先级应高于外部空 draft 的同步。

## 为什么这个修法正确

这个 bug 不是 remount,而是 external sync 覆盖 local in-flight state。和 dirty/focused guard 类似,uploading node 表示用户操作仍未 settle,此时不能让 session/draftKey 派生出的默认值清空编辑器。

## 验证方式

PR 增加/更新了 chat store、chat input 和 content editor 测试,覆盖 first upload/session creation/content sync 相关路径。

## 适用边界

适用于 rich editor 中有本地临时 node 的异步操作,例如上传、嵌入生成、暂存附件。纯受控文本框不需要这个 guard。

## 不要照搬

不要永久停止外部同步;guard 只应覆盖 upload in-flight 窗口。
