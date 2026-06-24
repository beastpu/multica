---
id: pattern.workspace.deployment_host_url_prefix
kind: pattern
title: "Workspace URL prefixes must come from deployment config, not brand literals"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4286
source_pr_number: 4286
source_issue_refs:
  - "#4263"
  - MUL-3400
  - https://github.com/multica-ai/multica/issues/4263
merged_at: 2026-06-18T03:12:10Z
merge_commit: 6f29a4c0a6ea2c8262c07c160bbcd6ffc7144e06
first_ingested_at: 2026-06-24
last_reviewed_at: 2026-06-24
modules:
  - packages/core
  - packages/views
signals:
  - "Self-hosted create-workspace UI still shows multica.ai as the workspace URL prefix"
  - "Desktop and web must render the operator's configured app host without relying on window.location"
code_refs:
  - repo: multica-ai/multica
    path: packages/core/workspace/workspace-url.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 6f29a4c0a6ea2c8262c07c160bbcd6ffc7144e06
    content_hash: 646baf1efa1d4222606e800c8197efcd879cec81
fix_pattern: "Derive workspace URL display prefixes from configured daemon_app_url, with the brand host only as fallback, and share the parser between onboarding and workspace creation UI."
verification:
  - packages/core/workspace/workspace-url.test.ts
  - packages/views/onboarding/steps/step-workspace.test.tsx
  - packages/views/workspace/create-workspace-form.test.tsx
gotchas:
  - "Do not use window.location.origin as the primary source; desktop does not run on the workspace domain."
  - "Keep the brand host fallback for managed cloud where daemon_app_url is intentionally omitted."
related: []
tags:
  - packages-core
  - packages-views
  - workspace
  - self-host
---

# Workspace URL prefixes must come from deployment config, not brand literals

## 症状

自托管部署里,create-workspace 和 onboarding UI 仍显示 `multica.ai/...` 作为 workspace URL prefix,没有反映运营者自己的域名。

## 根因

多个视图硬编码了品牌域名。直接改成 `window.location.origin` 也不正确,因为同一 shared view 会在 Electron desktop 中渲染,那里的 origin 不是 workspace domain。

## 正确修复模式

在 `packages/core` 放一个纯 helper,从 `/api/config` 暴露的 `daemon_app_url` 提取 host,处理 path、scheme、trailing slash、port、bare host 等情况。managed cloud 中该字段可为空,此时 fallback 到 `multica.ai`。onboarding 和 create workspace 共用这个 helper。

## 为什么这个修法正确

workspace URL 是部署配置,不是运行容器的浏览器 origin。用 config store 作为来源能同时适配 web、自托管和 desktop。

## 验证方式

PR 增加 helper 单测,并在 create-workspace form 与 onboarding step 测试中覆盖默认品牌 host 和配置 host 两种情况。

## 适用边界

适用于显示/生成 deployment-dependent URL 的 UI。纯 marketing copy 或固定外部文档链接不需要从 daemon config 派生。

## 不要照搬

不要在 shared package 直接访问 `window` 或 Next/Electron 平台 API。
