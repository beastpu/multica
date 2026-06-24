---
id: pattern.cli.remote_setup_callback_host
kind: pattern
title: "Remote CLI setup needs an explicit callback host"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4360
source_pr_number: 4360
source_issue_refs:
  - "#4357"
  - https://github.com/multica-ai/multica/issues/4357
merged_at: 2026-06-21T16:06:14Z
merge_commit: 737c976b0dc45e72742b663b8cad57e4ab37aab6
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-23
modules:
  - server
signals:
  - "Browser login in setup fails or confuses users when the CLI runs over SSH"
  - "Setup help does not expose the callback host option users need for remote machines"
code_refs:
  - repo: multica-ai/multica
    path: server/cmd/multica/cmd_auth.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 737c976b0dc45e72742b663b8cad57e4ab37aab6
    content_hash: 02c6a88ba6552f767768a95f4b7c72b3ac14dd6e
fix_pattern: "Expose the callback-host override on every setup entry point that can start browser auth, reuse the existing login callback plumbing, and print SSH-specific tunnel guidance when a loopback callback is used from an SSH session."
verification:
  - server/cmd/multica/cmd_auth_test.go
  - server/cmd/multica/cmd_setup_test.go
gotchas:
  - "Do not add a separate setup-only callback path when login already owns the auth callback behavior."
  - "Do not assume users will discover a child-command flag if parent-command help hides it."
related: []
tags:
  - server
  - cli
  - auth
  - setup
---

# Remote CLI setup needs an explicit callback host

## 症状

`multica setup` 或 `multica setup cloud` 在远程 SSH 环境里触发浏览器登录时,用户需要配置回调地址或 SSH tunnel,但 setup 命令没有把 callback host 作为清晰的入口暴露出来。

## 根因

登录流程已有 callback host override,但 setup 命令没有把这个能力完整接到自己的入口和 help 输出里。远程机器上使用默认 loopback callback 时,CLI 也缺少 SSH 场景提示,用户不知道需要把本机浏览器和远端 callback 端口连起来。

## 正确修复模式

把 `--callback-host` 暴露到会触发浏览器 auth 的 setup 入口,复用登录命令已有的 callback override,并在检测到 SSH session + loopback callback 时打印 tunnel hint。父命令 help 也要展示本地 flag,让 `multica setup --help` 能解释这个选项。

## 为什么这个修法正确

问题不在认证协议本身,而在 setup 对已有认证能力的入口封装不完整。复用登录流程能避免两套 callback 行为分叉,同时把远程环境差异放在 CLI 指引层处理。

## 验证方式

PR 增加了 callback flag 读取、setup help 展示、setup flag wiring 和 SSH remote hint 的 CLI 测试,并用 `go run ./cmd/multica setup --help`、`go run ./cmd/multica setup cloud --help` 验证帮助文本。

## 适用边界

适用于 CLI setup/login 这种"本地 callback + 外部浏览器"的认证流程。不是要求所有 CLI 命令都添加 callback host。

## 不要照搬

不要为 setup 新建一套独立 callback server 或 auth URL 拼装逻辑;优先找现有 login/auth plumbing 并把入口接齐。
