---
id: pattern.markdown.bare_filename_linkify
kind: pattern
title: "Bare source filenames should not become fuzzy external URLs"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4245
source_pr_number: 4245
source_issue_refs:
  - "#4222"
  - MUL-3359
  - https://github.com/multica-ai/multica/issues/4222
merged_at: 2026-06-17T09:06:10Z
merge_commit: 0f36c888559c3902252222763fe82e26b4ee0d68
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/ui
  - packages/views
signals:
  - "Agent comment turns plan.md into https://plan.md"
  - "Bare source/config filenames with TLD-like extensions open dead external sites"
code_refs:
  - repo: multica-ai/multica
    path: packages/ui/markdown/linkify.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 0f36c888559c3902252222763fe82e26b4ee0d68
    content_hash: 27fd2dd8934e963f2fea6f2fe0a9bc470058d72e
fix_pattern: "Suppress schemeless fuzzy linkify matches for single-segment source/config filenames, while preserving explicit schemes and explicit file paths."
verification:
  - packages/views/editor/utils/preprocess-links.test.ts
gotchas:
  - "Do not disable explicit https links just because their TLD is also a file extension."
  - "Do not claim this makes project file content openable; it only prevents bad external links."
related: []
tags:
  - packages-ui
  - packages-views
  - markdown
  - editor
  - linkify
---

# Bare source filenames should not become fuzzy external URLs

## 症状

Agent 在 issue comment 里提到 `plan.md`、`main.rs`、`build.sh` 等文件名时,renderer 会把它们变成 `https://...` 外部链接。用户点击后打开死链或无关域名。

## 根因

`preprocessLinks()` 使用 `linkify-it` 的 fuzzy URL detection。`.md`、`.sh`、`.rs`、`.py` 等后缀可能也是真实 TLD,所以 bare filename 被识别成 schemeless domain。已有 file path regex 只覆盖 `/`、`~/`、`./` 开头的路径,不覆盖单段文件名。

## 正确修复模式

收集 linkify matches 时,如果 match 是 schemeless fuzzy match,且 token 是单 path segment + 已知 source/config extension,就 suppress 该链接,渲染为普通文本。显式 `https://` 仍然链接,真实普通域名仍然链接,显式 `./src/main.go` 仍按文件路径规则处理。

## 为什么这个修法正确

在 agent-heavy 产品中,评论里裸写源文件名比裸写这些 TLD 域名更常见。只 suppress schemeless bare filename 能修掉错误链接,同时保留用户明确写 scheme 的意图。

## 验证方式

PR 增加了 bare `.md`、CJK 语境、`README.md`、`sh/rs/py`、显式 scheme、真实 domain 和 `./` path regression 的 preprocess-links 测试。

## 适用边界

适用于自动 linkify Markdown/comment text。附件预览、真实项目文件打开能力是另一个产品功能。

## 不要照搬

不要把所有 fuzzy link 都禁掉;`example.com` 这类真实域名仍应可点击。
