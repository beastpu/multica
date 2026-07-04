---
type: design
status: draft
module: operations
created: 2026-07-04
---

# Agent 派生工作（agent_work）通用原语 与 P4 评估实例化

> Related:
> - `docs/agent-fix-p4-assessment-next-design.md`（评估边界与 evidence 基线）
> - `docs/agent-fix-p4-assessment-api-workflow.md`（batch pull/submit 契约）
> - `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/`（worker 技能契约）

## Summary

评估执行目前对用户是黑盒：batch worker 一次任务评多张单，无法回答
"评估智能体正在处理哪个 issue / 某单评估遇到了什么问题 / 结果是什么"。

解决它需要的四样东西——**工作投影 issue、能力智能体指定、队列驱动派发、
隔离与写权限规则**——没有一样是 P4 专属的，且全部要落在平台共享代码上
（评论守卫、feed 过滤、统计口径）。因此本设计分两部分：

- **Part 1**：定义平台通用的「Agent 派生工作」原语。适用于一切
  "针对既有工作派生的 agent 后台工作，需要可追踪的叙事载体，且不得污染
  主工作流统计"的场景（P4 评估、连通性自检、周期健康报告，未来的
  review/回归验证 agent 等）。
- **Part 2**：P4 评估作为第一个实例化（kind = `p4_assessment`），
  连通性自检作为第二个（kind = `connectivity_check`），用两个消费者
  校准抽象边界。

刻意不做的：不把队列表/lease 框架化（只有一个真实队列消费者，抽象缺
第二个样本必然抽错——业务队列留在各自模块里）。

---

# Part 1 · 通用原语：Agent 派生工作

## 1.1 派生工作 issue（投影载体）

派生工作 issue 是**普通 issue**，不引入新 issue 类型。权威标识是
`issue.metadata` 上的 **server 保留键**（创建后只读，普通编辑路径不可
写入/篡改该键；jsonb `@>` 已支持过滤）：

```json
{
  "agent_work": {
    "kind": "p4_assessment | connectivity_check | ...",
    "source_issue_id": "<uuid, 可空>",
    "source_ref": "<业务侧引用, 如 binding_id, 可空>",
    "trigger": "manual | scan | force_rerun | ...",
    "extra": { "prompt_version": "..." }
  }
}
```

- **每次运行一个 issue**；重跑创建新 issue，旧 issue 留存不归档——
  issue 序列即运行历史。
- 同一来源的历史 = `metadata @> {"agent_work":{"source_issue_id": X}}`
  的 issue 序列（kind 可再收窄）。
- 每个 kind 归属一个**懒创建的系统 project**（如「P4 评估」），仅作
  组织/视图用途——守卫、隔离、查询一律认 metadata，不认 project 归属
  （project 可被移动/改名，metadata 创建即固化）。
- 标题约定：`<动作> <来源标识>【<外部单号>】<截断标题>`。

## 1.2 写权限：评论归 agent，状态归服务端

- **评论：执行该派生工作的 agent 直接写**（这是它被指派的工作项）。
  `rejectAnalysisTaskWrite` 从"全拒"改为精确放行：analysis-task actor
  的评论目标必须携带 `metadata.agent_work` 标记、同 workspace、且 kind
  与该任务的工作类型一致。对真实业务 issue 的禁写一分不松。
- **状态/字段/指派：仅服务端投影可写**。派生 issue 状态由业务侧状态机
  同事务投影（见 1.4），agent 双写状态必然和投影打架，照旧拒绝。
- **兜底结果评论由服务端代写**：业务完成/失败路径内写一条结构化
  markdown 评论（数据源即业务 result payload）——保证每张派生 issue
  无论 agent 是否叙事，至少有一条机器可读的结果；agent 评论是增量叙事。

## 1.3 能力智能体：workspace 能力角色

派生工作由**普通 agent** 承担，不引入新 agent 类别。workspace 级配置表
（通用，替代按功能加列/加 env）：

| 字段 | 语义 |
|---|---|
| `workspace_id` + `capability`（如 `p4_assessment`） | 唯一键 |
| `agent_id` | 承担该能力的 agent |
| `project_id` | 该 kind 的系统 project（懒创建后回填） |
| `max_concurrent_tasks` | 派发并发上限，默认 1 |

fail-closed 语义：未配置 = 该能力不启用（替代
`P4_ASSESSMENT_WORKSPACE_ALLOWLIST` 这类 env 白名单）。

**入口切分：创建在智能体入口，指定在设置。** agent 的唯一出生地是现有
智能体创建流程（创建完活在智能体列表里，编辑/换 runtime/看任务都在那）；
设置页只放「指定」选择器 + 「创建」深链快捷入口——深链带能力模板参数
（预填名称/说明；需要内网执行的能力，runtime 选择器**只列在线
daemon/local runtime**），创建完成跳回设置自动填入选择器。模板不常驻
通用创建流程（未启用该能力的 workspace 会困惑），仅经深链可达或按能力
可用性门控。不做自动创建：runtime 归属与凭证是用户决策，平台代选只会
造出跑不动的 agent。

指定时服务端校验 agent 存在/同 workspace/runtime 形态；网络可达性无法
从外网 server 验证，由**连通性自检**（kind=`connectivity_check` 的派生
工作，见 Part 2）兜底。

## 1.4 队列驱动派发（dispatcher 契约）

server 后台派发循环（宿主与 `feishu_project_sync_worker` 同级），循环体
按 kind 注册，每个 kind 只需实现两个函数：

```go
type AgentWorkKind interface {
    // 该 workspace 当前可认领的工作量（0 = 不派发）
    ClaimableCount(ctx, workspaceID) (int, error)
    // 创建一次批量任务，assignee = 能力角色配置的 agent
    CreateBatchTask(ctx, workspaceID, agentID) error
}
```

```
每 tick（≈30s）对每个已配置 (workspace, capability)：
  ClaimableCount > 0
  AND 能力 agent 的 runtime 在线
  AND 该 (workspace, capability) 活跃批量任务数 < max_concurrent_tasks
→ CreateBatchTask
```

多副本安全：判定 + 建任务同事务，`SELECT ... FOR UPDATE` 锁能力配置行
（或 per (workspace, capability) advisory lock）。worker 死亡 → 业务侧
租约过期 → 工作回池 → 下一 tick 补发，全链路自愈。

## 1.5 隔离清单（每个 kind 落地时逐项验收）

派生 issue / 派生任务不得出现在（排除条件一律认 `metadata.agent_work`）：

- [ ] 运营 feed（spine 按 fix 任务 + binding 组织，派生 issue 无 binding
      天然排除——加测试锁死）
- [ ] usage 统计与 agent 排行榜的"任务/issue 完成数"（灌水）
- [ ] 真实 issue 的任务历史 / latest-run / 会话恢复 / 去重
      （现有 task 侧隔离过滤保持）
- [ ] 默认 issue 视图与看板（系统 project 默认不选中；搜索可显式进入）
- [ ] 通知默认策略（派生 issue 状态流转不推送给来源 issue 的关注者）

**订阅是白送的能力**：派生工作是 issue，平台订阅/通知机制免费继承——
想要"评估失败告警"的用户订阅该系统 project 即可，无需另建告警系统。

---

# Part 2 · 实例化

## 2.1 kind = `p4_assessment`（首个消费者）

### 概念模型

```
真实缺陷 issue（禁写，不变）
   └─ feishu_binding ── agent_fix_p4_assessment（队列行，唯一真相，per binding 一行）
                            │ assessment_task_id（租约 → batch task → 评估智能体）
                            │ assessment_issue_id（当前运行的派生 issue）
                            ▼
                    派生 issue（agent_work.kind=p4_assessment，per RUN 一个）
```

`agent_fix_p4_assessment` **仍是唯一真相**：运营面板全部 KPI 只读此表，
任何指标不得读 issue 状态。

### 数据模型（一次迁移，三列）

| 列 | 类型 | 语义 |
|---|---|---|
| `assessment_issue_id` | `uuid NULL REFERENCES issue(id) ON DELETE SET NULL` | 当前运行的派生 issue；重评时指向新 issue |
| `attempt_count` | `int NOT NULL DEFAULT 0` | 被 lease 的累计次数 |
| `last_error` | `text NOT NULL DEFAULT ''` | 最近失败/释放原因；完成时清空 |

### 创建时机（全部收敛到 `P4AssessmentService.Trigger`）

1. 运营面板对指定 issue 手动点击（现有 AssessmentTriggerButton 路径）；
2. 定时扫描新增未评估单（现有 `feishu_project_sync_worker` →
   `BackfillDoneBindings` 周期路径，不新增扫描器）。

### 状态投影（同事务）

| 队列行事件 | 派生 issue |
|---|---|
| Trigger/Upsert → `pending` | 创建新 issue（todo，指派评估智能体） |
| Lease → `running`（attempt+1） | in_progress |
| 租约过期重新 lease | 仍 in_progress（同一 issue，attempt+1） |
| Release（evidence 失败退回） | todo；写 `last_error` + 服务端失败评论（消除现有静默黑洞） |
| Complete（submit-by-ref） | done；服务端结果摘要评论 |
| Fail | failed；`last_error` + 失败评论 |
| force 重评 | 旧 issue 保持终态；建新 issue 并重指向 |

### 执行契约增量

- 仍走 batch worker（pull-by-lease）。**不回退**每单一任务/一会话的
  旧模型；per-issue 归属靠租约字段 + 派生 issue 投影。
- batch pull 的每个 item 返回 `assessment_issue_id`，agent 据此把过程
  叙事/证据链评论到自己的派生 issue（写权限见 1.2）。
- skill 文档（SKILL.md + source map）同步该契约。

### 可观测性落点

- 运营明细行：`running` 显示执行者智能体 + 租约剩余 + 派生 issue 链接；
  `failed` 显示 attempt_count + last_error。
- 评估智能体「最近工作」= 其被指派的派生 issue 列表，零新查询模型。
- 点击派生 issue：描述（关联真实单）+ 评论时间线（执行记录）+
  batch 任务会话链接（深挖兜底）。

## 2.2 kind = `connectivity_check`（第二个消费者，校准抽象）

设置页指定评估智能体后一键下发 smoke-test：`p4 -V`、evidence endpoint
可达、Swarm 只读探测。实现即派生工作的完整复用——自检 issue（同系统
project 或独立 project）、结果写评论、状态投影、隔离规则——**零新增
共享代码**。它的存在证明 Part 1 的边界画对了。

## Rollout

1. **P0**：三列迁移；Lease/Release/Fail/Complete 写 attempt/last_error；
   运营明细透传。（不依赖派生 issue，先解观测之痛）
2. **通用原语 + P4 投影**：`metadata.agent_work` 保留键与守卫放行；
   系统 project 懒创建；Trigger 建 issue；状态同事务投影；服务端结果/
   失败评论；pull item 返回 `assessment_issue_id`；skill 文档同步；
   隔离清单验收。
3. **能力角色 + dispatcher + 创建流**：能力配置表替代 env 白名单；
   智能体入口模板 + 设置深链；`AgentWorkKind` 注册式派发；连通性自检。
4. **后续（可选）**：周期评估健康报告（kind=`health_report`，第三个
   消费者，届时检验注册式派发）。

## Non-goals

- 不把业务队列表/lease 框架化（L3 过度抽象；业务队列留在各自模块）。
- 不让派生工作写**来源业务 issue**（评论/状态/字段一律禁止）；对派生
  issue 也仅放行评论，状态/字段/指派归服务端投影。
- 不引入新的 issue 类型或 agent 类别。
- 不引入监控/提示词优化常驻智能体。
- 指标口径不变：运营面板全部 KPI 继续只读 `agent_fix_p4_assessment`。
