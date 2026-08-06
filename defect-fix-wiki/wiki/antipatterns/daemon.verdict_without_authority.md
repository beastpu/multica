---
id: antipattern.daemon.verdict_without_authority
kind: antipattern
title: "读到了判定就照着做，但没有执行这个判定所需的前置授权"
status: active
confidence: high
visibility: private
repo: multica-ai/multica
source_pr: https://github.com/multica-ai/multica/pull/4096
source_pr_number: 4096
source_issue_refs:
  - MUL-3269
merged_at: 2026-08-05T11:54:22Z
merge_commit: 31cd51ae2fa294ec0808840a40d84b4a6dc9c5cd
first_ingested_at: 2026-08-05
last_reviewed_at: 2026-08-05
modules:
  - server
signals:
  - "同一个下线/失效判定有多个地方会得出并各自执行"
  - "某条路径丢弃了探测函数的返回值，却仍按同样的语义处理缺席项"
  - "保护性状态（barrier、hold、seq）只在其中一条路径上写"
  - "review 一轮修好一处，下一轮在另一条路径上以同样形态复现"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/daemon.go
    line_hint: "registerRuntimesForWorkspaceLocked / preserveProvidersFromProbe / applyRegisterResponseInPlace"
    ref_kind: root_cause
    verified_commit: 31cd51ae2fa294ec0808840a40d84b4a6dc9c5cd
    content_hash: abe8384cdcdded6e6075113e3c3e2a5c79c9f440
  - repo: multica-ai/multica
    path: server/internal/daemon/agents_refresh.go
    line_hint: "demoteBelowMinimumRuntimes"
    ref_kind: verification
    verified_commit: 31cd51ae2fa294ec0808840a40d84b4a6dc9c5cd
    content_hash: b58100c0f0bd1096e9cb9f4ef562247171c902e3
fix_pattern: "把判定作为『这个响应对这些项不具权威性』回传给调用方让它们保留现状，由唯一持有 barrier 和 hold 的那条路径执行下线；不要让每条得出判定的路径各自执行。"
verification:
  - server/internal/daemon/agent_version_refresh_test.go
gotchas:
  - "不要把『补齐授权』当成修法：三处都拿 barrier 和 hold，等于三处共同维护一个只有单点才成立的顺序。"
  - "延迟执行的代价要说清楚——这里是最多晚一个刷新周期，等于判定前的现状，不是新增暴露面。"
  - "丢弃返回值不等于没有行动：调用方仍按缺席=删除处理时，判定其实已经被执行了，只是没留下记录。"
related:
  - pattern.daemon.registration_cleanup_ordering
  - pattern.runtimes.profile_drift_sync
tags:
  - server
  - daemon
  - runtime
  - concurrency
  - state-machine
---

# 读到了判定就照着做，但没有执行这个判定所需的前置授权

## 症状

一个 agent CLI 被降级到最低支持版本以下，daemon 把它的 runtime 下线了；紧接着一个在途的注册响应落地，这个 provider 又回来了，继续接任务。或者反过来：正在执行任务的 runtime 被某条不相干的路径（profile drift、runtime_gone 恢复）拆掉，任务被 server 判成孤儿失败重试，而本地进程还在跑。

## 根因

"这个 CLI 版本太低，要下线"这个判定，在 daemon 里有三个地方会得出：周期性的版本刷新、runtime_gone 恢复、profile drift 刷新。三个地方都调同一个探测函数 `detectBuiltinRuntimes`。

但安全地执行这个判定需要两样东西，只有版本刷新那条路径有：

- **claim barrier** —— 保证没有任务正在这个 runtime 上跑的时候才动它；
- **带序号的 hold 记录** —— 保证一个在判定之前发出、判定之后才落地的注册响应不会把 provider 复活。

另外两条路径把探测函数的第二个返回值（本轮的 below-minimum 名单）丢掉了：

```go
builtins, _, unavailable := d.detectBuiltinRuntimes(ctx)
```

但它们随后把响应当作该 workspace runtime 集合的**权威**来 apply，缺席的 provider 连行带记录一起删掉再 Deregister。丢掉返回值并不意味着没有行动——判定其实被执行了，只是没留下任何记录。于是下一个在途响应把它撤销，而且撤销得悄无声息。

这是个反复出现的形态：review 第 3 轮修好版本刷新那条路径，第 4 轮同样的问题在另外两条路径上原样复现。

## 为什么会走到这一步

因为缺的东西看起来像"证据"，其实是"授权"。这三条路径确实都拿到了真实、确凿的判定——版本读出来了，也确实低于下限。差别不在它们知道什么，而在它们能不能安全地据此行动。

一旦把问题理解成"证据没传下去"，最自然的修法就是把 `belowMinimum` 也传进 apply、在同一个 `d.mu` 段里写下带序号的判定。这条路能走通，但结果是三个地方共同维护一个顺序，而这个顺序之所以成立，恰恰是因为原本只有一个地方维护它。

## 正确修复模式

让判定回传的语义变成"**这个响应对这些 provider 不具权威性**"，而不是"这些 provider 该下线了"。

具体做法：探测的两类结果——探测失败（transient）和确认低于下限（confirmed）——合并成同一个 preserve 集合回传。authoritative 的 apply 路径看到 preserve 里的 provider，就保留它现有的行，什么都不做。真正的下线只留给持有 barrier 和 hold 的那条路径。

三条下线路径变成一条。附带修掉的另一个问题：drift 和 runtime_gone 不再会在任务执行中拆掉 runtime，因为它们根本不下线了。

## 代价要算清楚

延迟执行是有代价的，必须说明白而不是含糊过去：一个过老的 CLI 最多多在线一个刷新周期（5 分钟）。这是判定生效**之前**的现状，不是新增的暴露面。能这样论证，是这个方案可以被接受的前提。

## 验证方式

`TestRegisterRuntimesForWorkspace_ReportsBelowMinimumAsNotAuthoritative` 盯住被丢弃的那个返回值本身。
`TestReregisterAfterRuntimeGone_LeavesBelowMinimumToTheDemotionPath` 和 `TestProfileDriftRefresh_LeavesBelowMinimumToTheDemotionPath` 断言两条 authoritative 路径保留 provider、不发 Deregister，随后各跑一次刷新周期，证明判定是被推迟而不是被丢弃。

## 适用边界

适用于任何"多处能得出同一个破坏性判定、但只有一处持有执行它所需的保护性状态"的场景。识别信号：某条路径丢弃了探测/校验函数的返回值，却仍按同样的语义处理缺席项。

## 不要照搬

- 不要通过"给每条路径都补上 barrier 和 hold"来修。授权分散出去之后，那个只有单点才成立的顺序就没人保证了。
- 不要在没算清延迟代价的情况下改成延迟执行。如果推迟一个周期意味着真实的新增风险（比如安全边界），那就应该反过来收敛入口，而不是让判定悬着。
- 不要因为"返回值反正丢掉了"就以为这条路径没在执行判定。看调用方怎么处理缺席项。
