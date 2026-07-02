# Perforce Webhook 按项目策略（Per-Project Strategy）

> Status: Implemented
> Last updated: 2026-07-02
> Code: `server/internal/handler/perforce_strategy.go`, `server/internal/handler/perforce_webhook.go`
> Schema: `server/migrations/131_perforce_project_strategy.up.sql`
> Queries: `server/pkg/db/queries/perforce_strategy.sql`

## 背景

Perforce / Helix Swarm 的 review 事件统一打到**现存** endpoint
`POST /api/webhooks/p4-swarm`（地址、鉴权 token `MULTICA_P4_SWARM_WEBHOOK_TOKEN`
都不变）。默认行为是**把 review link 到 description 里引用的已存在 issue**
（`MUL-123`），commit 时把该 issue 推到 done。

但某些项目的诉求不同：它们的 review 不引用已存在 issue，而是希望
**每来一个事件就在指定 workspace 新建一个 Multica issue、指派一个默认 agent**，
并把 changelist 号、author 带进 issue。

本特性用「**代码提供能力 + 数据选择能力**」的方式支持这类项目特例：

- **代码**只注册命名能力（strategy），不认识任何具体 `swarm.url`；
- **数据**（一张独立表）决定「某个项目（swarm.url）用哪个能力 + 参数」。

上线一个新项目、换 agent、临时关掉，都是**改数据**，不改代码、不发版、多副本一致。

## 数据模型

两张**新增独立表**，既有 `perforce_connection` / `perforce_review` /
`issue_perforce_review` 一列不改，schema 零影响。

### `perforce_project_strategy`（配置数据）

| 列 | 说明 |
| --- | --- |
| `swarm_url` | 路由键 = 项目身份。归一化（小写、去尾斜杠）后**全局唯一**。 |
| `workspace_id` | 目标 workspace（在此建 issue）。 |
| `strategy` | 能力名，代码注册表里的一项，如 `create_per_event`。 |
| `default_assignee_id` | 默认指派的 agent（`ON DELETE RESTRICT`）。 |
| `enabled` | 是否启用；`FALSE` 则该项目回落到默认 link 行为。 |

### `perforce_strategy_created_issue`（运行时幂等台账）

`UNIQUE(strategy_id, review_id, review_updated_at)` —— create_per_event 的
幂等键，同时是「哪个事件产生了哪个 issue」的溯源。

## 分发原理

```text
POST /api/webhooks/p4-swarm
  → 解析 payload、鉴权、状态校验（均不变）
  → GetPerforceProjectStrategyBySwarmURL(swarm.url)
      命中且 enabled → 跑 reviewStrategies[strategy]（能力表查表，非 switch swarm.url）
      未命中        → 原样走默认 link-to-existing-issue 流程（完全不动）
```

代码侧能力注册表（`perforce_strategy.go`）：

```go
var perforceReviewStrategies = map[string]perforceReviewStrategyFunc{
    "create_per_event": (*Handler).createIssuePerEvent,
}
```

未来项目 #2 有别的流程 = 加一个能力 + 把它的 `swarm_url` 指过去，主干不变。
`strategy` 值不在注册表里时**降级为 `ignored`**（不 500，防止 enum 漂移打挂入口）。

## `create_per_event` 行为

1. **只在 `review.created` / `review.updated` 两种事件生效**：其它 `event_type`
   （如 `review.commented`、`review.archived`）一律 ack + `ignored`，不建 issue。
2. **每个不同事件建一个 issue**：以 `review.updated` 区分事件；台账唯一键保证
   **网络重试（updated 相同）不重复建**，而 Swarm 真正 bump `updated` 的新事件会再建一个。
3. **建 issue**：`status=todo`、`assignee=default_assignee_id`（agent）、creator=同一 agent；
   title 用 `review.title`（空则 `CL <cl> by <author>`）。
4. **CL# / author 进描述**（约定的零精度落点）：描述里渲染 changelist
   （committed 优先，否则 shelved）、author、state、review#，以及原始 review 描述。
5. 建完 publish `issue:created` 并 `EnqueueTaskForIssue` —— agent 立即开工。
6. **不跑 auto-advance/close**：每个事件是独立 issue，只建不推进。

## 配置示例（策略数据 = 直接写表）

没有产品 API/UI —— 策略是**约定的后台数据**，由运维/DBA 直接写入。

### 1. 启用一个项目

```sql
-- 让 swarm.url = https://swarm.example.com/game-client 的所有 review 事件
-- 在 workspace <WS_UUID> 新建 issue，指派 agent <AGENT_UUID>
INSERT INTO perforce_project_strategy
    (workspace_id, swarm_url, strategy, default_assignee_id, enabled)
VALUES (
    '<WS_UUID>',
    'https://swarm.example.com/game-client',
    'create_per_event',
    '<AGENT_UUID>',
    TRUE
);
```

### 2. 一次 webhook 事件的输入 → 输出

Swarm 打到 `POST /api/webhooks/p4-swarm` 的 payload（节选）：

```json
{
  "event_type": "review.created",
  "swarm":  { "url": "https://swarm.example.com/game-client", "branch": "main" },
  "review": {
    "id": 500123, "state": "needsReview", "title": "fix crash on boot",
    "description": "", "author": "alice",
    "changes": [500120], "commits": [],
    "created": 1700000000, "updated": 1700000100
  }
}
```

Multica 新建的 issue：

- **Title**：`fix crash on boot`
- **Assignee**：agent `<AGENT_UUID>`（creator 也是它）
- **Description**：见下方渲染结果。

Description 内容：

```text
Created from a Perforce / Helix Swarm review event.

- Changelist: 500120
- Author: alice
- State: needsReview
- Review: #500123
```

响应 `202 {"status":"processed"}`。相同 `updated` 重投 → `202 {"status":"duplicate"}`，不重复建。

### 3. 临时关闭 / 换 agent / 下线

```sql
-- 临时关闭（回落到默认 link 行为）
UPDATE perforce_project_strategy SET enabled = FALSE
WHERE lower(rtrim(swarm_url,'/')) = 'https://swarm.example.com/game-client';

-- 换默认 agent
UPDATE perforce_project_strategy SET default_assignee_id = '<NEW_AGENT_UUID>'
WHERE lower(rtrim(swarm_url,'/')) = 'https://swarm.example.com/game-client';

-- 彻底下线
DELETE FROM perforce_project_strategy
WHERE lower(rtrim(swarm_url,'/')) = 'https://swarm.example.com/game-client';
```

改完对**下一个 webhook** 立即生效（每次请求实时查库，无缓存）。

## 运维注意

- `default_assignee_id` 用 `ON DELETE RESTRICT`：删一个仍被某条启用策略引用的
  agent 会被拦。先改/删策略，再删 agent。
- `swarm_url` 归一化后全局唯一：一个 Swarm 项目只能配一条策略。跨 workspace
  抢同一个 `swarm.url` 会失败（约束层面直接拒绝），避免路由歧义。
- create_per_event 会为该项目**每个事件**唤起 agent（有成本）。仅给确实需要
  「每 CL 一 issue」的项目开启。

## 测试

`server/internal/handler/perforce_strategy_test.go`（真库）：

- 配了 create_per_event 的项目 → 每事件建 issue，assignee = 默认 agent，描述含 CL#/author；
- 相同 `review.updated` 重投 → 不重复建；
- Swarm bump `updated` 的新事件 → 再建一个；
- 未配置策略的 `swarm.url` → 仍走默认 link 行为（既有 Perforce 测试零回归）。
