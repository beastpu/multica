---
title: 任务接管（Takeover）— 改派即接管
type: design
status: approved
date: 2026-08-09
topic: agent-collaboration
artifact_readiness: implemented
execution: authorized
---

# 任务接管（Takeover）— 改派即接管

> P1 已实现。实现偏差见 §8 各项标注。

## 0. 一句话

接管不是新状态、不是新通道，是一个原子动作：**停掉 agent 的在途任务 + 把 issue 改派给我 + 把工作现场交到我手上**。三件事里前两件的机制已经存在但没有连在一起，第三件缺一张"现场交接卡"。

## 1. 为什么现在做

干预链条目前是这样的：

| 情况 | 现有动作 | 空洞 |
| --- | --- | --- |
| agent 方向不对，还想让它继续 | 取消任务 + 评论纠偏 → 恢复原会话（已落地） | 无 |
| agent 做了 80%，人想接手补完剩下 20% | **没有** | 只能强制完成（承认这步没做完）或回滚（上游全部重做） |
| 返工到达 max_attempts，节点 blocked | 强制完成 / 跳过 / 回滚 | 三个都是"绕过工作"，没有"我来做完它" |
| 人做到一半想还给 agent | **没有** | 改派回去会开新任务，但人做了什么 agent 不知道 |

第二、三行就是接管要填的洞。触发场景真实存在：返工谈不拢的节点，人最自然的反应是"行，我自己来"——现在这句话在产品里没有对应的按钮。

## 2. 两种心智模型，选哪个

**askhz 路线（CSC 会话接管）**：人进入 agent 正在跑的终端会话，实时对话、打断、批权限，然后交还或判定。需要四方会话绑定（node_run + runtime + device + session）、device gateway、独立于 workspace 的运行时权限体系、Web 终端 UI。干预过程留在会话里，issue 时间线上没有痕迹。

**Multica 路线（改派接管）**：agent 是同事。从同事手里接工作，动作是**把 issue 改派给自己**——这是用户已有的心智，不需要学习。issue 的 assignee 本来就是多态的（member 或 agent），接管只是一次特殊的改派：特殊在它同时要停掉 agent 的在途任务、并把 agent 的工作现场（分支、目录、会话）交出来。

选 Multica 路线，理由：

1. **状态机零改动。** askhz 复用 blocked 表达"被接管"，靠 `completed_at` 是否为 NULL 区分两种 blocked——一个状态两种含义。改派模型里，"谁在做"由 assignee 表达，节点状态机完全不动：节点还是 active，run 还是等 `required_issue_outcome`，人把 issue 做到 done，节点照常推进。接管是 assignment 事实，不是 workflow 状态。
2. **审计免费。** 改派、取消、交还全部落在 issue 时间线，谁在什么时候从 agent 手里接过、说了什么，事后全可查。
3. **本地 runtime 场景下，我们的"现场交接"反而比 Web 终端强。** daemon 通常跑在开发者自己的机器上，任务行已经记录 `work_dir`、`session_id`、`runtime_id`（daemon 中途就 pin，`UpdateAgentTaskSession`）。接管后把这三样亮出来，人直接 `cd` 进那个目录、用自己的编辑器和终端接着干，甚至 `claude --resume <session-id>` 接上 agent 的对话——这是真终端，不是网页模拟的。

## 3. 设计总览

```
┌─ 接管（takeover）───────────────────────────────────────────┐
│ 1. 取消该 issue 上所有在途 agent 任务（CancelTasksForIssue）    │
│ 2. 改派 assignee → 发起人（member）                           │
│ 3. 发一条系统 note 评论："X 从 @agent 接管了此任务"             │
│ 4. 返回现场交接卡：runtime / 分支 / 工作目录 / 会话 id          │
└─────────────────────────────────────────────────────────────┘
                    │ 人在自己的环境里干活
                    ▼
┌─ 交还（handback，可选）─────────────────────────────────────┐
│ 改派 assignee → 原 agent，附 handoff note（已有机制）          │
│ → 派发新任务，恢复同一会话（cancelled resume 已落地）           │
│ → note + 期间的评论就是人类工作的交接说明                       │
└─────────────────────────────────────────────────────────────┘
```

没有"接管态"。接管期间就是一个普通的"issue 派给人"的状态；交还就是一次普通的"issue 派给 agent"。两个方向的改派都已存在，接管动作的价值是把"停任务"和"改派"绑成原子操作，并把现场信息交出来。

## 4. 服务端

### 4.1 接管动作

`POST /api/issues/{id}/takeover`

请求体：`{ "idempotency_key": "..." }`（无其他字段——接管人就是调用者）。

处理顺序（单事务外加任务取消，与 leave workspace 同类的顺序敏感流程）：

1. `loadIssueForUser` 解析 issue（支持 `MUL-123` 与 UUID，遵循 handler UUID 约定）。
2. 权限：调用者具备该 issue 的改派权限即可。不引入新权限层——workflow 节点的完整性由 submission / verdict 门保护，不靠 assignee 保护。私有 agent 的可见性沿用 `canAccessPrivateAgent` 门（与 `CancelTaskByUser` 相同）。
3. `CancelTasksForIssue`（`service/task.go:1939`，已存在）：取消全部在途任务，daemon 轮询到取消即中断 agent。squad 多任务一并覆盖。
4. 改派 assignee → 调用者，`SuppressRun` 语义（改派给 member 本来就不派发任务）。
5. 写系统 note 评论，标注接管来源与被接管的 agent。
6. 响应带现场交接卡（见 4.3）。

幂等与并发：

- agent 任务恰好在接管瞬间完成 → 取消是 no-op，改派照常，卡片展示已终态任务的现场。接管不失败。
- 重复接管（已是自己）→ 200，返回当前卡片。
- `idempotency_key` 防止双击产生两条 note。

### 4.2 为什么不做"优雅收尾"（v1）

理想的接管是先让 agent"把手头的改动 commit 并 push，写一句进展"再退出。这需要向运行中会话注入消息——即"agent 求助/中途注入"那条 daemon 协议通道，是独立特性。v1 直接硬取消：daemon 的中断本来就不清 workdir，未提交的改动原样留在目录里，人接手时看得到。等注入通道落地后，接管动作前置一步 wrap-up 即可，API 形状不变。

### 4.3 现场交接卡

接管响应与 issue 详情各暴露一份（后者供接管后回看）：

```json
{
  "takeover": {
    "from_agent": { "id": "...", "name": "Dev Agent" },
    "task_id": "...",
    "runtime": { "id": "...", "name": "beast 的 MacBook" },
    "work_dir": "~/multica-envs/<ws>/<task>",
    "branch": "agent/MUL-123-fix",
    "session_id": "sess-abc",
    "provider": "claude"
  }
}
```

数据全部来自已存在的任务行字段（`work_dir` / `session_id` / `runtime_id`，daemon 中途 pin）。两个注意点：

- **`work_dir` 走 `RelativeWorkDir` 隐私规则**（`agent.go:687`），完整路径只在调用者对该 runtime 有权限时给出。
- **`session_id` 目前不在用户侧任务响应里**，需要新增暴露；只对有 runtime 访问权限的调用者返回，语义是"你可以在那台机器上 `claude --resume` 它"。远程/他人 runtime 上这行不显示——不承诺做不到的事。

分支名不在任务行上；v1 从任务的执行日志/最后评论提取（daemon 已把 PR/分支写进结果评论），拿不到就不显示。不为它加列。

### 4.4 交还

不加新端点。交还就是改派回 agent，现有机制已经拼齐：

- 改派 UI 已支持 `handoff_note`（MUL-3375，`EnqueueTaskForIssueWithHandoff`），note 渲染进新任务的开场 prompt——这就是人对 agent 的交接说明。
- 新任务按 `(agent, issue)` 恢复最近会话；**cancelled 会话可恢复已在本分支落地**（`GetLastTaskSession`），所以 agent 接回来时带着被接管前的全部对话上下文。
- 人接管期间的评论都在 issue 上，任务 prompt 本来就带评论上下文。

唯一要补的：交还时若人动过 agent 的 workdir，恢复的会话对文件状态的记忆是旧的。`handoff_note` 模板里预置一句提示（"working tree 可能已被人工修改，先 `git status` 对齐"），由前端在交还表单里默认填充,不做服务端魔法。

## 5. Workflow 节点

- **入口**：workbench 节点管理菜单，在强制完成 / 跳过 / 回滚旁加"接管"，仅当节点的 executor 解析为 agent 且节点开放时显示。动作即对节点子 issue 调用 4.1 端点，无 workflow 专属逻辑。
- **attempt 语义**：接管不消耗 attempt。attempt 计的是"退回重做"的轮次，接管是换人不是退回。
- **完成路径不变**：人把子 issue 做到 done（或走 Completion form 提交 outputs / artifacts / 结论），节点照常按 `required_issue_outcome` + submission 门推进。submission 的 `submitted_by` 就是接管人——归因天然正确。
- **attribution**：接管时向 `workflow_node_participant` 插一行 `role='participant', actor=member`（表已存在）。运行历史里能看出这个节点有人介入过。
- **issue_policy=none 的节点没有子 issue**，无从改派——接管按钮不显示。这类节点本来就是人在 workbench 直接操作的，不存在"从 agent 手里接管"的对象；agent 直执任务（execution source）跑歪时用取消即可。

## 6. 边界情况

| 情况 | 行为 |
| --- | --- |
| 接管时 agent 任务已 completed | 跳过取消，只改派 + note；卡片展示终态任务现场 |
| squad 执行中（leader + 成员多任务） | `CancelTasksForIssue` 全部取消；改派给接管人；note 点名 squad |
| 接管后 agent 被再次 @mention | 正常触发新任务（mention 语义不变）——接管不是封禁；若不希望，用户不 @ 即可 |
| 接管人中途放弃 | 改派给任何人/任何 agent 都是普通改派；无悬挂状态需要清理 |
| workflow run 被取消/节点被跳过 | 与普通 issue 一致，无特殊耦合 |
| 远程 runtime（非本人机器） | 卡片不显示 session/完整路径；接管仍然成立（人用自己的环境从分支接着做） |

## 7. 不做什么

- **不做 CSC 式 Web 终端**。触发条件明确：当"远程 runtime + 必须实时干预"成为高频诉求时再评估，且届时 issue 时间线与会话接入并不互斥。
- **不做接管专用状态 / 列 / 表**。全部复用 assignee、任务行、评论、participant。
- **不做优雅收尾（wrap-up）**。依赖中途注入通道，作为该特性落地后的增量。
- **不做分支名落列**。展示层尽力提取，不为展示加存储。

## 8. 分期与验收

**P1（本设计核心）**
- [x] `POST /api/issues/{id}/takeover`：取消在途任务 + 改派 + note。幂等改为结构性而非 key：取消对空在途是 no-op、改派对已是本人是 no-op、note 只在 assignee 实际变更时写——双击天然只产生一条 note，请求体不再需要 idempotency_key
- [x] `session_id` 经交接卡暴露（不改通用任务响应；workdir 走既有 RelativeWorkDir 隐私规则）；交接卡组件两个入口：issue 执行日志运行行、workbench 节点子 issue 行
- [x] 接管后 `GET /api/issues/{id}/takeover` 可回看卡片（派生自最近任务行，不落存储）
- [x] workflow 入口落在节点子 issue 行（能看到 assignee 的位置）而非管理下拉。**participant 归因行取消**：迁移 311 已把 participant 收敛为 owner/reviewer 座位（语义是"节点在等谁"），塞接管行会弯曲表义；归因由系统 note + assignee + submission.submitted_by 承担
- [ ] E2E 未运行（需起前后端）；节点随 issue done 推进由既有 required_issue_outcome 测试覆盖，takeover 端点不触碰任何 workflow 表

**P2（依赖交接卡反馈）**
- [ ] 交还表单预置 handoff note 模板
- [ ] 本地 runtime 检测：卡片上的 `claude --resume` 一键复制命令按 provider 生成

**验收标准**：返工耗尽 blocked 的节点上，一个非 admin 的节点 owner 能在 60 秒内完成"接管 → 本地打开工作目录"，且 issue 时间线完整记录接管前后的归因。
