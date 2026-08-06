---
id: pattern.daemon.registration_cleanup_ordering
kind: pattern
title: "清理请求必须和它跟随的注册在同一个顺序里，而不是靠时点重查"
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
  - "daemon 在心跳一个 server 侧已经 offline 的 runtime，两边状态相反且无人报错"
  - "任务一直没人认领，直到 stale-heartbeat 兜底扫描才被回收"
  - "先在锁里算出要删的 ID，出锁后再发 HTTP Deregister"
  - "用一次 tracked 重查来防止删掉刚恢复的行"
  - "同一个 workspace 的注册入口有多个（refresh / sync / runtime_gone / drift），彼此不串行"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/daemon.go
    line_hint: "workspaceRegisterLock / withWorkspaceRegisterLock / untrackedRuntimeIDs"
    ref_kind: root_cause
    verified_commit: 31cd51ae2fa294ec0808840a40d84b4a6dc9c5cd
    content_hash: abe8384cdcdded6e6075113e3c3e2a5c79c9f440
  - repo: multica-ai/multica
    path: server/internal/daemon/agents_refresh.go
    line_hint: "demoteBelowMinimumRuntimes / deregisterDroppedRuntimes"
    ref_kind: touched
    verified_commit: 31cd51ae2fa294ec0808840a40d84b4a6dc9c5cd
    content_hash: b58100c0f0bd1096e9cb9f4ef562247171c902e3
fix_pattern: "把一个 workspace 的 send Register → apply/reject → cleanup 放进同一把 per-workspace 锁里，让清理请求本身进入顺序，而不是在请求前加一次 tracked 重查。"
verification:
  - server/internal/daemon/agent_version_refresh_test.go
  - server/internal/daemon/runtime_probe_batch_test.go
gotchas:
  - "不要用『发请求前再查一次本地是否还持有』来修这类竞态；要覆盖的窗口恰好在这次查询和这次请求之间。"
  - "假服务端每次 register 都发新 runtime ID 时，清理和恢复永远不指向同一行，这类交错在测试里完全不可见。"
  - "跨 workspace 的清理要按 workspace 分组、一次只持有一把锁，否则引入锁序死锁。"
related:
  - pattern.runtimes.profile_drift_sync
  - antipattern.daemon.verdict_without_authority
tags:
  - server
  - daemon
  - runtime
  - concurrency
  - registration
---

# 清理请求必须和它跟随的注册在同一个顺序里，而不是靠时点重查

## 症状

daemon 认为自己持有并在心跳某个 runtime，server 却把这一行标成 offline。两边状态相反，而且谁都不会报错：server 不往这个 runtime 派活，daemon 也不知道自己被冷落，派给它的任务一直挂着，只能等 150 秒的 stale-heartbeat 扫描兜底。

## 根因

daemon 里每条"注册后清理"路径都是同一个形状：在 `d.mu` 里算出要下线的 runtime ID，出锁，再发 HTTP `Deregister`。

`d.mu` 不能一直持有到 HTTP 结束（会把整个 daemon 锁死），所以决定和请求之间必然有一段空窗。同一个 workspace 的注册入口有四个（版本刷新、workspace sync、runtime_gone 恢复、profile drift），它们互不串行。一个合法的恢复注册可以整个落在这段空窗里：它把同一行重新建起来——而且往往是**同一个 runtime ID**，因为 server 端是 upsert——然后那条更老的 `Deregister` 才落地，把刚恢复的行又打下去。

中间尝试过的修法是"发请求前再查一次本地是否还跟踪这个 ID"。这个修法把窗口收窄了，但没有关掉：要覆盖的窗口恰好就在这次查询和这次请求之间。一个时点检查在原理上就修不了它。

## 正确修复模式

不要试图把检查做得更准，把请求本身放进顺序里。

原本 per-workspace 的注册锁只包住 "send Register + 记录发了什么版本"。把它的临界区扩到 **send → apply/reject → cleanup** 整段：

- 三个 register 发送函数改成 `*Locked` 后缀，锁由调用方持有整段序列；
- 所有 apply（authoritative 替换、builtin 增量合并、首次注册直接发布）和所有 cleanup 都在这把锁里跑；
- 跨 workspace 的降级清理按 workspace 分组，逐个拿对应的锁，一次只持有一把。

这样"锁序 = server 处理序 = 本地记录序"这条已有的不变式，第一次覆盖到了清理。原来的 tracked 重查保留下来，但它不再是防线，只是在锁内做一次正确的判断：如果恢复注册已经完成，这个 ID 现在是被跟踪的，跳过即可。

## 为什么这个修法正确

竞态的本质是两个操作没有全序，而不是某个判断不够新。加检查是在猜"现在应该还没变"，扩锁是让"不可能在中间插入"。后者不依赖时序假设，也就不会在负载变化时重新出现。

代价是注册期间会多持有一把 per-workspace 锁（包含版本探测和 profile 拉取）。可以接受：这些路径是 5 分钟级的周期任务，且锁是按 workspace 分的，不同 workspace 之间照常并发。

## 验证方式

`TestDemoteBelowMinimumRuntimes_CleanupCannotOutliveANewerRecovery`：把一个 `Deregister` 阻塞在假服务端，在它在途时发起恢复注册，断言恢复注册**没有**穿过去；放行后断言这一行最终是 online。变异验证（把清理挪出锁）时两个断言都挂。

## 适用边界

适用于任何"本地状态 + 远端状态"需要一致、而清理是异步 HTTP 的场景。判断标准：如果一次清理的决定和它的请求之间可以插进一次完整的反向操作，就不要用重查，要扩顺序。

## 不要照搬

- 不要用"发请求前再查一次"来修这类竞态。它看起来收敛，实际上只是把复现概率降低。
- 跨 workspace 的清理不要一次持有多把锁，会引入锁序死锁。分组、排序、逐个拿。
- 测试用的假服务端如果每次 register 都发新 ID，这类交错根本不会发生。要让它按 (workspace, provider) 复用 ID，模拟真实的 upsert 行为，否则测试会绿着放过去。
