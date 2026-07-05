---
type: design
status: draft
module: operations
created: 2026-07-04
updated: 2026-07-05
---

# Agent 派生工作原语 与 P4 评估执行模型（方案 C：原生任务流）

> Related:
> - `docs/agent-fix-p4-assessment-next-design.md`（评估边界与 evidence 基线）
> - `docs/agent-fix-p4-assessment-api-workflow.md`（batch pull/submit 契约，本设计将其退役）
> - `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/`

## 演进说明

- **已落地**（develop）：观测三列（迁移 133）、派生 issue 投影 + `metadata.agent_work`
  保留键 + 写守卫（迁移 134，MR !185）。这些在方案 C 中全部保留。
- **本次修订**：评审后放弃「batch worker + lease + dispatcher」作为长期执行模型，
  phase 3 转向**方案 C：原生任务流**。理由见附录 Alternatives。

## Part 1 · 通用原语（保留，仅一处修订）

`metadata.agent_work` 保留键、每次运行一个派生 issue、系统 project 懒创建、
「评论归 agent / 状态归服务端投影」写切分、隔离清单、能力角色配置
（workspace × capability → agent/project/并发上限）——**全部维持已落地/已设计
的形态**。修订一处：

- ~~1.4 队列驱动派发（dispatcher）~~ **删除**。方案 C 中"指派即派发"：
  派生 issue 创建时指派给能力 agent，平台现有的 issue-assignment→task 原生
  链路即完成派发；任务持久在 server 端 `agent_task_queue`（Postgres），
  daemon 重启不丢，认领用现有 prepare-lease 自愈。不再需要独立派发循环。

## Part 2 · P4 评估的方案 C 执行模型

### 概念

```
真实缺陷 issue（禁写）
   └─ 触发（面板手动 / 定时扫描）
        → 评估 issue（agent_work 标记，指派评估智能体）
             → 原生 task（挂在评估 issue 上，daemon 认领，一单一会话）
                  → agent 读 evidence → 只读 P4/Swarm 核查
                  → 过程叙事评论（写在评估 issue）
                  → POST 结果 → 结果表（类型化列）
```

- **并发**：评估智能体的 `max_concurrent_tasks`，无需额外机制。
- **每单独立会话**：执行日志天然按单隔离（batch 模型的交织缺陷消失）。
- **重评**：新评估 issue + 新 task；旧 issue/结果留存为历史。

### 结果表降级（不删除）

`agent_fix_p4_assessment` 从「队列+结果」降级为**类型化结果读模型**：

- 保留：结果列（归因/质量枚举 CHECK、confidence、CL 数组、warnings、
  summary、evidence）、`assessment_issue_id`、binding 唯一键（面板读
  "每缺陷最新结果"，O(1) join 不变）。
- 职能移除：lease（`leased_until`）、pending 池语义——执行状态归 task。
  行状态改为 task 生命周期的**单向投影**（task running→running、终态失败
  →failed、result 端点提交→completed），无反向路径、无双向同步。
- 不选全 JSONB 方案（C2）的原因：条件 CHECK/表达式索引虽可行，但把领域
  约束叠进全库最热的共享表，且指标读路径从 O(1) join 变 latest-task-per-issue
  lateral join；结果表就是这些 jsonb 抽取的物化，成本已付。
- 历史 498 行：保留为结果存档，batch 时期的行无投影 issue，照常展示。

### 守卫：保留，换锚点（显式记录威胁模型）

守卫防的不是"任务挂在哪"，而是两个方案 C 改变不了的事实：evidence 必然
携带真实 issue 身份；agent task token 是 workspace 级写能力（mention/协作
依赖，无按-issue 写限制）。一次批量扫描期间评估 agent 手握几十个真实 issue
ID，一次跑偏（提示词漂移/旧版 daemon/LLM 误操作）即可面状污染，且真实
issue 状态变更会经飞书集成**状态回写**穿透到外部系统。故：

- `rejectAnalysisTaskWrite` 保留，锚点从 `task_category=='analysis'` 换为
  「任务所挂 issue 携带 `metadata.agent_work`」：此类任务仅可评论**自己
  所挂的派生 issue**，对一切其他 issue 的评论/状态/字段写一律拒绝。
- 全系统从此只有一个派生工作标记（issue metadata），无并行标记需同步。

### `task_category` 退役

方案 C 下其查询隔离职责消失（评估任务挂派生 issue，按 issue 维度的
latest-run/去重/cancel/会话恢复查询天然不命中真实 issue），守卫换锚后
判定职责也消失。清理迁移删除该列与 130 引入的全部 `task_category='fix'`
过滤及对应 SQL 不变量测试。**时序约束**：必须在 batch 契约退役、在途
batch/legacy 任务排干之后（batch 任务无 issue_id，过渡期守卫仍依赖列）。

### batch 契约退役与 skill 改写

- 退役：`GET /api/operations/assessments/pending`、
  `POST /api/operations/assessments/result`（by-ref）、30 分钟评估租约、
  `LeaseP4AssessmentsPending` 等查询。先标记 deprecated，排干后删除。
- 执行契约转正 legacy per-binding 路径：task context 携带 binding，
  `GET .../p4-evidence` 读证据、`POST .../p4-assessment/result` 提交
  （同一 `validateP4AssessmentPayload`）。该通道现存且可用。
- SKILL.md 改写为"处理指派给你的评估 issue"工作流：从 task context 取
  binding → 读 evidence → 只读核查 → 叙事评论（仅限所挂评估 issue）→
  提交结果。批量循环、ref、租约章节删除。

### 触发与能力角色

触发入口不变（面板手动 / `feishu_project_sync_worker` 扫描），收敛到
Trigger：建评估 issue（含标记）→ 指派给能力角色配置的评估智能体 →
原生链路建 task。未配置能力角色 = 不触发（fail-closed，替代 env 白名单）。
「创建在智能体入口、指定在设置、深链闭环」的 provisioning 设计不变。

## Rollout（修订）

1. ✅ 已落地：观测三列；派生 issue 投影 + 守卫 + 保留键（batch 模型上）。
2. **C-1 能力角色 + 原生任务流**：能力配置表；Trigger 改为指派+原生 task；
   守卫换锚 issue-metadata；行状态单向投影改造；skill 改写；batch 端点
   deprecated；连通性自检（kind=connectivity_check，走同一原生链路）。
3. **C-2 清理**：排干在途 batch 任务；删除 batch 端点/lease 查询/
   `task_category` 列与过滤（一次清理迁移）。

## Non-goals

- 不删除结果表（类型化读模型保留）。
- 不移除写守卫（威胁模型见上，作为显式决策记录）。
- 不让派生工作写真实业务 issue；对派生 issue 也仅放行评论。
- 不引入新 issue 类型 / agent 类别 / 监控常驻智能体。
- 指标口径不变：面板 KPI 只读结果表。

## 附录 · Alternatives（决策记录）

| | A/C 原生 task（采纳） | B batch+lease（退役） |
|---|---|---|
| 心智模型 | issue→task→会话，零新概念 | 租约/批次两层新概念 |
| 每单执行日志 | 独立会话，天然干净 | 会话交织，靠评论补 |
| token/调度开销 | 每单多付固定开销（约 +25–40%，非早期估计的 5×） | 摊薄 |
| 派发 | 指派即派发，无 dispatcher | 需队列驱动 dispatcher |
| 持久/自愈 | server 端任务队列 + prepare-lease，等价 | 评估租约，等价 |
| 状态机 | 单一（task），结果表为单向投影 | 单一（行），但需独立契约 |
| 隔离 | 按 issue 天然隔离，category 可退役 | 依赖 category 过滤 |

决策：B 的省钱优势不抵其概念负担与双契约维护；B 时代的产出
（投影 issue、标记、守卫、观测列、结果表）全部被 C 复用，仅执行通道更换。
