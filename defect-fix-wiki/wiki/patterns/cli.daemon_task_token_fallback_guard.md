---
id: pattern.cli.daemon_task_token_fallback_guard
kind: pattern
title: "Daemon task CLI must fail closed, never fall back to a user PAT"
status: active
confidence: medium
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4209
source_pr_number: 4209
source_issue_refs:
  - "#4204"
  - https://github.com/multica-ai/multica/issues/4204
merged_at: 2026-07-02T07:08:07Z
merge_commit: 0c4c3ff038715d3dff7a4a29932a7c08d46e81cc
first_ingested_at: 2026-07-03
last_reviewed_at: 2026-07-03
modules:
  - server
signals:
  - "Agent-authored comments silently attributed to the workspace owner instead of the agent"
  - "A daemon task subprocess loses MULTICA_TOKEN / MULTICA_AGENT_ID / MULTICA_TASK_ID and the CLI uses the config-file mul_ PAT"
  - "Actor resolves as member instead of agent because the request carries a user PAT, not a mat_ task token"
code_refs:
  - repo: multica-ai/multica
    path: server/cmd/multica/cmd_auth.go
    line_hint: 70
    ref_kind: root_cause
    verified_commit: 0c4c3ff038715d3dff7a4a29932a7c08d46e81cc
    content_hash: 913bb7a3dea0d7a9e8c8676be9a46ba3fa050825
  - repo: multica-ai/multica
    path: server/cmd/multica/cmd_agent.go
    line_hint: 311
    ref_kind: fix
    verified_commit: 0c4c3ff038715d3dff7a4a29932a7c08d46e81cc
    content_hash: 2b1a44c48d58c68faa8661223a7f526d7b54891e
  - repo: multica-ai/multica
    path: server/internal/daemon/execenv/context.go
    line_hint: 1
    ref_kind: fix
    verified_commit: 0c4c3ff038715d3dff7a4a29932a7c08d46e81cc
    content_hash: 70c1529814b00ad54ee4d97af6b6c7b77913d10a
fix_pattern: "Detect daemon-managed execution from a layered set of signals (MULTICA_AGENT_ID / MULTICA_TASK_ID / MULTICA_DAEMON_PORT env plus a workdir marker file that survives env stripping); when in that context and no mat_ task token is present, fail closed instead of falling back to the config-file mul_ PAT."
verification:
  - server/cmd/multica/cmd_agent_test.go
  - server/cmd/multica/cmd_auth_test.go
  - server/internal/daemon/execenv/execenv_test.go
gotchas:
  - "Do not treat MULTICA_SERVER_URL alone as a daemon signal; users set it in normal shells, so it must still pair with the saved config token."
  - "A daemon-context guard that fails closed on any non-IsNotExist read error while walking up for the marker will refuse a normal user's PAT for no reason (unsearchable ancestor dir, foreign file at the marker name). Only a readable marker whose managed_by matches counts; every other outcome means no signal, keep walking."
  - "When a new env var becomes a behavioral signal, clear it in test setup (TestMain and per-test t.Setenv). Otherwise go test becomes environment-dependent and false-fails inside any daemon-managed run; CI passing only proves the runner happened not to set it."
  - "A workdir marker that gates auth can be left behind by a crash before sidecar cleanup, making the user's own later CLI runs in that directory fail closed. Surface an actionable error that names the leftover file, and prefer sweeping stale markers on daemon start."
related: []
tags:
  - server
  - cli
  - daemon
  - auth
  - agent
  - security
---

# Daemon task CLI must fail closed, never fall back to a user PAT

## 症状

Agent 发出的评论偶发地被记成 **workspace owner 的 member 账号**,而不是 agent 身份。触发条件是某个 daemon 任务子进程丢失了 `MULTICA_TOKEN` / `MULTICA_AGENT_ID` / `MULTICA_TASK_ID` 环境变量(例如 Hermes 的 `execute_code` 沙箱不继承这些变量),此时 CLI 静默回退到 `~/.multica/config.json` 里的用户 `mul_` PAT。服务端 `resolveActor()` 看到的是用户 PAT 而非 `mat_` task token,于是把 actor 解析成 `member`(owner)。

## 根因

`resolveToken()` 的回退链是:`MULTICA_TOKEN` → `inAgentExecutionContext()` 为真则返回空 → 否则读 config 文件 PAT。当三个 env 同时缺失时 `inAgentExecutionContext()` 返回 `false`(它只看 `MULTICA_AGENT_ID` / `MULTICA_TASK_ID`),于是流程落到第三步,用 owner 的个人 PAT 认证。问题在于"是否处于 daemon 托管执行"这个判断的信号面太窄,子进程可以在保留 daemon 身份的同时把它们全部丢掉。

## 正确修复模式

把 daemon 托管执行识别为一组**分层信号**:env 层用 `MULTICA_AGENT_ID` / `MULTICA_TASK_ID` / `MULTICA_DAEMON_PORT`,再加一个写在 agent 自己 workdir 里的 marker 文件(`.multica/daemon_task_context.json`,`managed_by` 匹配)——即使沙箱把所有 `MULTICA_*` env 都剥掉,marker 仍在磁盘上兜底。只要判定为 daemon 上下文且没有 `mat_` task token,就**fail closed**(返回空并报错),绝不回退到 config 文件的 `mul_` PAT。`newAPIClient` 对 daemon marker 路径同样施加"必须是 `mat_` task-scoped token"的既有约束。

## 为什么这个修法正确

误归因的根因不是认证协议,而是"身份上下文判定"在子进程丢 env 时会假阴。单靠 env 无法覆盖"沙箱剥掉 env"这一类,所以要加一个不依赖 env 的 workdir marker 做第二层。fail closed 是安全默认:宁可让一次调用报错要求正确 token,也不能静默用错误身份写数据。同时刻意**不**把 `MULTICA_SERVER_URL` 当 daemon 信号,保留普通用户 CLU + profile token 的正常路径。

## 验证方式

CLI 测试覆盖矩阵:daemon 信号存在且无 `mat_` token 时 fail closed;有显式 task token 时 task token 胜出;仅 `MULTICA_SERVER_URL` 不阻断 config 回退;无任何 daemon 信号的普通 CLI 仍能读 config token。marker 的 execenv 测试覆盖写入/清理。均使用 fake server 与 `t.Setenv`,无外部依赖。

## 适用边界

适用于"同一个 CLI 二进制既被人类用户直接调用、又被 daemon/agent 以子进程方式调用"的身份判定场景。核心是:用分层信号识别受控执行上下文,并在受控上下文里对凭证 fail closed。不是要求所有 CLI 命令都加 marker,也不改变普通用户的认证路径。

## 不要照搬

- 不要只靠环境变量判断执行身份——子进程可以把它们全部丢掉;需要一个不依赖 env 的兜底信号。
- 不要把 marker 的向上查找写成"任何非 `IsNotExist` 读错误都当作 daemon 信号"。这会因为某个不可搜索的祖先目录或同名异物文件,把普通用户的 PAT 无理由拒掉。只有"能读且 `managed_by` 匹配"才算信号,其它一律视为"此处无信号,继续向上找"。
- 不要在把某个环境变量提升为行为信号后,忘记在测试里清理它。`TestMain` 和每个用例的 `t.Setenv` 必须清掉这组 daemon 信号(`MULTICA_AGENT_ID` / `MULTICA_TASK_ID` / `MULTICA_TOKEN` / `MULTICA_DAEMON_PORT` / `MULTICA_WORKSPACE_ID` / `MULTICA_SERVER_URL`),否则 `go test` 会依赖运行环境,在任何 daemon 托管环境里成片假失败;CI 变绿只说明 runner 恰好没设这个变量。用一个共享的清理 helper 防止未来新增信号时再次漂移。
- 不要忽视崩溃残留:写在 workdir 的 marker 若在 sidecar 清理前因异常退出留下,会让用户之后在该目录下手动跑 CLI 也 fail closed。报错要指名残留文件让用户能自救,并尽量在 daemon 启动时清扫过期 marker。
