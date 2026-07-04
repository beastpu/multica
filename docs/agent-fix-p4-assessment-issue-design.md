---
type: design
status: draft
module: operations
created: 2026-07-04
---

# P4 Assessment 评估 Issue 与自动派发设计

> Related:
> - `docs/agent-fix-p4-assessment-next-design.md`（评估边界与 evidence 基线）
> - `docs/agent-fix-p4-assessment-api-workflow.md`（batch pull/submit 契约）
> - `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/`（worker 技能契约）

## Summary

评估执行目前对用户是黑盒：batch worker 一次任务评多张单，无法回答
"评估智能体正在处理哪个 issue / 某单评估遇到了什么问题 / 结果是什么"。

本设计把评估过程投影成平台原生对象：

1. 每次评估运行创建一个**评估 issue**（专属「P4 评估」project，指派给评估
   智能体），执行结果以**评论**落在评估 issue 上——不污染真实缺陷 issue。
2. **服务端 dispatcher** 按队列状态自动给评估智能体派发 batch 任务，
   替代人工/自动化任务触发。
3. `agent_fix_p4_assessment` 队列表**仍是唯一真相**；评估 issue 只是投影，
   任何指标不得读 issue 状态。

## Decisions（已确认）

- 评估智能体由**用户自建 runtime**（内网 P4/Swarm 访问是网络拓扑约束），
  能力（skill/结果契约/提示词版本）由平台标准化，用户不改写评估提示词。
- 评估 issue **每次运行一个**；重评（force）创建**新的**评估 issue，旧 issue
  留存不归档——issue 序列即评估历史，天然解决"重评覆盖历史"。
- 创建时机只有两个入口，全部收敛到 `P4AssessmentService.Trigger`：
  1. 运营面板对指定 issue 手动点击（现有 AssessmentTriggerButton 路径）；
  2. 定时扫描新增未评估单（现有 `feishu_project_sync_worker` →
     `BackfillDoneBindings` 周期路径，本设计不新增扫描器）。
- 执行仍走 batch worker（pull-by-lease）。**不回退**每单一任务/一会话的
  旧模型；per-issue 归属靠租约字段与评估 issue 投影，不靠 task.issue_id。
- 监控/提示词优化智能体不在本期范围（先以效果分析 Tab + 周期健康报告覆盖）。

## 概念模型

```
真实缺陷 issue（禁写，不变）
   └─ feishu_binding ── agent_fix_p4_assessment（队列行，唯一真相，per binding 一行）
                            │ assessment_task_id（租约 → batch task → 评估智能体）
                            │ assessment_issue_id（当前这次运行的评估 issue）
                            ▼
                    评估 issue（「P4 评估」project，per RUN 一个，指派评估智能体）
                        ├─ 描述：真实 issue 链接、binding、触发来源、prompt_version
                        ├─ 评论：结果摘要 / 失败原因（执行记录）
                        └─ 状态：队列行状态的投影
```

## 数据模型

`agent_fix_p4_assessment` 新增列（一次迁移）：

| 列 | 类型 | 语义 |
|---|---|---|
| `assessment_issue_id` | `uuid NULL REFERENCES issue(id) ON DELETE SET NULL` | 当前运行对应的评估 issue；重评时指向新 issue |
| `attempt_count` | `int NOT NULL DEFAULT 0` | 被 lease 的累计次数（LeaseP4AssessmentsPending 时 +1） |
| `last_error` | `text NOT NULL DEFAULT ''` | 最近一次失败/释放原因；成功完成时清空 |

评估 issue 侧不加列，复用 `issue.metadata`（jsonb，已支持 `@>` 过滤）。
**metadata 标记是评估 issue 的权威标识**——写权限守卫、隔离过滤、历史查询
全部认 `metadata.p4_assessment` 的存在，而不是 project 归属（project 可被
移动/改名，metadata 创建即固化且用户不可改写该键）：

```json
{
  "p4_assessment": {
    "binding_id": "<uuid>",
    "real_issue_id": "<uuid>",
    "trigger": "manual | scan | force_rerun",
    "prompt_version": "p4-assessment-v1"
  }
}
```

同一真实 issue 的评估历史 =
`metadata @> {"p4_assessment":{"real_issue_id": X}}` 的 issue 序列。
服务端需保证普通 issue 编辑路径不能写入/篡改 `metadata.p4_assessment`
（server 侧保留键，创建后只读）。

「P4 评估」project：workspace 首次触发评估时懒创建，id 记录在评估配置里
（见 Dispatcher 一节），仅作组织/视图用途。评估 issue 标题形如
`评估 WAR-1234【BUG-70046xxx】<真实标题截断>`。

## 状态投影（同事务，无第二真相）

队列行状态变更的服务函数在**同一个 DB 事务**里更新评估 issue 状态：

| 队列行事件 | 评估 issue |
|---|---|
| Trigger/Upsert → `pending` | 创建新 issue，状态 todo，指派评估智能体 |
| Lease → `running`（attempt+1） | in_progress |
| 租约过期被重新 lease | 仍 in_progress（同一 issue，attempt+1） |
| ReleaseP4AssessmentLease（evidence 失败退回） | todo；**写 `last_error`**（消除现有静默黑洞），服务端追加一条失败评论 |
| Complete（submit-by-ref） | done；服务端把结果摘要写成评论 |
| Fail | failed；`last_error` + 失败评论 |
| force 重评 | 旧 issue 保持终态；建新 issue，`assessment_issue_id` 重指向 |

**评论归 agent，状态归服务端**——这是写权限的核心切分：

- **评论：agent 直接写**。评估 issue 本来就是指派给它的工作项，过程叙事、
  证据链、结果说明由 agent 以评论形式写在自己的评估 issue 上（batch pull
  的每个 item 返回 `assessment_issue_id`，agent 知道往哪写）。
  `rejectAnalysisTaskWrite` 改为精确放行：analysis-task actor 的评论目标
  issue 必须携带 `metadata.p4_assessment` 标记且属于同 workspace——对真实
  缺陷 issue 的禁写一分不松。
- **状态：仅服务端投影可写**。issue 状态由队列行状态同事务投影（上表），
  agent 对评估 issue 的状态/字段/指派修改照旧拒绝——否则 agent 和投影
  双写状态必然打架。
- **结果摘要评论仍由服务端代写**（completion/failure 路径内，结构化
  markdown，数据源即 result payload）——保证每张评估 issue 无论 agent
  是否叙事，都至少有一条机器可读的结果评论；agent 评论是增量叙事。

## 评估智能体的创建与指定

评估智能体是**普通 agent**，不引入新的 agent 类别。它的特殊性只有两点：
运行在能访问内网 P4/Swarm 的 daemon runtime 上；被 workspace 评估配置
指定。能力注入不依赖用户装 skill——评估任务的 context/handoff 已由服务端
注入内置技能契约（`multica-agent-fix-p4-assessment`，user-invocable:false），
换 agent 不丢能力，平台升级 skill 全网生效。

入口切分：**创建在智能体入口，指定在设置**。agent 的唯一出生地是现有的
智能体创建流程（评估智能体创建完就活在智能体列表里，编辑/换 runtime/看
任务都在那，出生地与居住地一致）；设置页不内嵌第二套创建表单（避免
runtime 选择/校验双份维护）。

1. workspace 设置 → 评估配置页：「指定评估智能体」选择器 + 「创建评估
   智能体」快捷入口——后者是**深链**到智能体创建流程并带评估模板参数，
   创建完成跳回设置页自动填入选择器，旅程闭环、表单只有一份。
2. 评估模板预填：建议名称（如 `P4 评估员`）、说明、runtime 选择器
   **只列在线的 daemon/local runtime**（评估必须内网执行，云端 runtime
   不可选）。模板不常驻通用创建流程（未启用评估的 workspace 会困惑），
   仅经设置深链可达或按评估功能可用性门控显示。
3. 指定时服务端校验：agent 存在、属于本 workspace、runtime_mode 为
   daemon/local。网络可达性服务端无法验证（外网 server 不通内网），
   通过**连通性自检任务**兜底：指定后可一键下发 smoke-test 任务
   （`p4 -V`、evidence endpoint 可达、Swarm 只读探测），结果写在
   自检评估 issue 的评论里——复用本设计的全部机制。
4. 不自动创建 agent 的原因：runtime 归属与 P4 凭证是用户决策，平台
   替用户选 runtime 只会造出一个永远跑不动的 agent。

替换 agent：配置改指向新 agent 即可；在途 batch 任务跑完即止，队列行
不受影响（租约过期自然回池，由新 agent 的任务接手）。

## Dispatcher（队列驱动，替代白名单 env + 人工触发）

workspace 级评估配置（新表或并入现有 integration 配置）：

| 字段 | 语义 |
|---|---|
| `assessment_agent_id` | 指定的评估智能体（替代 `P4_ASSESSMENT_WORKSPACE_ALLOWLIST`，配置即启用，fail-closed 语义保留：未配置 = 不评估、API 403） |
| `assessment_project_id` | 懒创建的「P4 评估」project |
| `max_concurrent_tasks` | batch 任务并发上限，默认 1 |

派发循环（宿主：`feishu_project_sync_worker` 同级的 server 后台 goroutine）：

```
每 tick（≈30s）对每个已配置 workspace：
  可认领行数（pending/failed/stale/租约过期） > 0
  AND 评估智能体 runtime 在线
  AND 该 workspace 活跃 batch 评估任务数 < max_concurrent_tasks
→ 创建 batch 评估任务（assignee = 评估智能体，task 不挂 issue_id）
```

多副本安全：派发判定 + 任务创建放同一事务，用
`SELECT ... FOR UPDATE` 锁配置行（或 advisory lock per workspace），
保证两副本不会同时各派一个任务。worker 死亡 → 租约过期 → 行回池 →
下一 tick 自动补发，全链路自愈。

## 隔离清单（实现时逐项验收）

评估 issue / 评估任务不得出现在（排除条件一律认
`metadata.p4_assessment` 标记，不依赖 project 归属）：

- [ ] 运营 feed（`ListWorkspaceAgentFixes` spine 按 fix 任务 + binding 组织，
      评估 issue 无 binding 天然排除——加测试锁死）
- [ ] usage 统计与 agent 排行榜的"任务/issue 完成数"（评估灌水）
- [ ] 真实 issue 的任务历史 / latest-run / 会话恢复 / 去重
      （现有隔离过滤已覆盖 task 侧，保持）
- [ ] 默认 issue 视图与看板（评估 project 默认不选中；搜索需可显式进入）
- [ ] 通知默认策略（评估 issue 状态流转不推送给真实 issue 的关注者）

## 可观测性落点

- 运营明细行（P0 透传）：`running` 显示 执行者智能体 + 租约剩余 +
  评估 issue 链接；`failed` 显示 attempt_count + last_error。
- 评估智能体「最近工作」：其被指派的评估 issue 列表即是，零新查询模型。
- 点击评估 issue：描述（关联真实单）+ 评论时间线（执行记录）+
  所属 batch 任务会话链接（深挖兜底）。

## Rollout

1. **P0**：迁移加三列；Lease/Release/Fail/Complete 写 attempt/last_error；
   运营明细透传。（不依赖评估 issue，先解观测之痛）
2. **评估 issue 投影**：懒创建 project；Trigger 建 issue（metadata 权威
   标记 + server 保留键）；状态同事务投影；服务端结果/失败评论；
   batch pull item 返回 `assessment_issue_id`；`rejectAnalysisTaskWrite`
   metadata 精确放行（agent 可评论评估 issue）；skill 文档同步；
   隔离清单验收。
3. **Dispatcher + 评估配置 + 智能体创建流**：workspace 配置替代 env
   白名单；「创建评估智能体」引导 + 校验 + 连通性自检任务；自动派发。
4. **后续（可选）**：周期评估健康报告。

## Non-goals

- 不回退 per-issue 评估任务/会话模型。
- 不让评估写**真实缺陷 issue**（评论/状态/字段一律禁止）；对评估 issue
  也仅放行评论,状态/字段/指派仍归服务端投影。
- 不引入监控/提示词优化常驻智能体。
- 不引入新的 agent 类别（评估智能体是普通 agent + 配置指定）。
- 指标口径不变：运营面板全部 KPI 继续只读 `agent_fix_p4_assessment`。
