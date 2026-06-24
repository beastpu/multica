---
id: pattern.runtimes.custom_runtime_args_registration_errors
kind: pattern
title: "Custom runtime commands need structured args and failed-profile visibility"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4408
source_pr_number: 4408
source_issue_refs:
  - MUL-3495
merged_at: 2026-06-23T06:20:18Z
merge_commit: 12ea1f6a8c70f0334929eeee8fce758c8916e4d6
first_ingested_at: 2026-06-23
last_reviewed_at: 2026-06-24
modules:
  - packages/views
  - server
signals:
  - "Pasted custom runtime commands lose fixed arguments or accept shell-only syntax"
  - "Unresolved custom runtime profiles disappear instead of showing actionable registration failure metadata"
code_refs:
  - repo: multica-ai/multica
    path: packages/views/runtimes/components/runtime-profile-catalog.ts
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 12ea1f6a8c70f0334929eeee8fce758c8916e4d6
    content_hash: 6076bf904fb298726daa007201b0ada523dd39cb
  - repo: multica-ai/multica
    path: server/internal/handler/daemon.go
    line_hint: 1
    ref_kind: root_cause
    verified_commit: 12ea1f6a8c70f0334929eeee8fce758c8916e4d6
    content_hash: 4c37bd854e30376416ded0976a524cfb174b2c9d
fix_pattern: "Parse custom runtime commands into structured command_name plus fixed_args, reject shell-only syntax before saving, carry fixed_args through daemon launch, and surface failed profile registrations as offline profile-backed runtime rows."
verification:
  - packages/views/runtimes/components/runtime-profile-catalog.test.ts
  - packages/views/runtimes/components/runtime-profiles-dialog.test.tsx
  - server/cmd/multica/cmd_runtime_profile_test.go
  - server/internal/daemon/runtime_profile_drift_test.go
  - server/internal/daemon/runtime_profile_test.go
  - server/internal/handler/daemon_test.go
  - server/internal/handler/runtime_profile_handler_test.go
gotchas:
  - "Do not store a pasted shell command as an opaque string when later launch code needs argv semantics."
  - "Do not hide unresolved profile-backed runtimes; users need failure metadata in the runtime list."
  - "Roll out server support before newer daemons that can send failed_profiles-only registrations."
related:
  - pattern.runtimes.custom_runtime_profile_delete
tags:
  - packages-views
  - server
  - runtime
  - daemon
  - compatibility
---

# Custom runtime commands need structured args and failed-profile visibility

## 症状

Custom runtime 配置需要支持类似 `command arg1 arg2` 的固定参数,但如果把用户粘贴的命令当字符串保存,后续 daemon launch 无法可靠组合 provider extra args。另一个症状是 profile 无法解析或注册失败时,UI 看不到明确的离线 runtime row 和失败原因。

## 根因

runtime profile 的输入、存储和 daemon launch 缺少一致的 argv 语义。UI 没有把粘贴命令拆成 `command_name` + `fixed_args`,也没有明确拒绝 shell-only syntax。daemon/server registration 也没有把 unresolved profile 作为可见失败状态上报。

## 正确修复模式

在 UI 创建时解析命令并预览 chips,拒绝需要 shell 解释的语法。把 `fixed_args` 贯穿 profile registration 和 task launch,并在 provider `ExtraArgs` 前拼接。daemon registration 上报 failed profiles,server 将其展示成 offline profile-backed runtime rows,带可操作的 failure metadata。

## 为什么这个修法正确

runtime command 是进程启动 contract,需要结构化 argv,不能依赖 shell 字符串猜测。profile-backed runtime 的失败也属于用户需要修复的配置状态,隐藏失败会让 runtime 看似消失,难以诊断。

## 验证方式

PR 覆盖了 runtime profile catalog parsing、dialog create payload、CLI profile 测试、daemon launch/drift、daemon registration handler 和 runtime profile handler 等路径。

## 适用边界

适用于自定义 runtime/profile 这类跨 UI、server、daemon 传递启动参数的功能。普通单字段配置不需要引入完整 command parser。

## 不要照搬

不要无条件接受 shell-only syntax;如果需要支持 shell,应作为显式 runtime 类型或 shell wrapper 设计,不能混在 argv parser 里。
