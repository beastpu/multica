---
type: design
status: draft
module: operations
created: 2026-06-27
---

# AI 修单 P4/Swarm 前置判断与人工验收方案

> 实施计划：`docs/agent-fix-p4-assessment-plan.md`
> 数据与限制：`docs/agent-fix-evidence-data-limitations.md`
> 外部依赖：`docs/agent-fix-p4-external-dependencies.md`

## 背景

warpath3 的 AI 修单统计口径以飞书/Meego 工单为主：只有外部工单状态映射为 `done`，才进入完成率、正确率等统计分母。当前要补齐的是一条完整链路：

1. 飞书/Meego 工单同步到 Multica。
2. 外部工单完成后，AI 先做 P4/Swarm 前置判断。
3. 人基于 AI 证据和判断做最终验收。
4. Operations 汇总业务结果、AI 修单效果、AI 判断准确性。

第一版只处理 P4/Swarm，不接入 GitHub PR。仓库里虽然已有 GitHub PR 集成，但本方案不读取、不展示、不参与状态推导，避免把 P4 CL 和 GitHub PR 抽象成难维护的通用交付物模型。

## 核心原则

- 飞书/Meego 外部工单完成态是唯一统计完成事实。
- `issue.status` 只代表 Multica 内部工作流，不代表统计完成。
- `agent_task_queue.status` 只代表 agent 运行状态，不代表修复质量。
- AI 前置判断要落库，后续用于分析 AI 判断是否准确。
- 人工验收是最终事实，用于通过率、返工率、拒绝率等核心质量统计。
- `agent_fix_p4_assessment` 记录是 assessment 存在性的事实来源。
- `issue.metadata.p4_assessment`、`metadata.demo`、title 中的 `p4 assessment/swarm` 只作为 demo/兼容兜底，不能作为真实主流程事实。
- Operations 单条触发按钮只是手动补跑入口；真实流程从外部 done binding 的候选池出发。
- `/issues` 不新增 AI 修单 tab，只在原 issue 卡片/详情融合轻量字段。
- `/operations` 保留原运营明细，扩展 P4 证据、AI 判断、人工验收、分析和导出。

## 当前代码能力

### 已有数据源

| 数据源 | 现有表/接口 | 可提供的信息 |
|---|---|---|
| Multica issue | `issue` | 标题、描述、内部状态、assignee、项目、labels |
| Agent run | `agent_task_queue` | task 状态、context/result/error、运行时间、失败原因 |
| Agent comment | `comment` | agent 留下的 shelve/review/说明线索 |
| Token usage | `task_usage` | 成本、token、模型使用 |
| 飞书/Meego | `feishu_project_issue_binding` | 外部工单 ID、URL、状态、外部字段、同步时间 |
| 飞书配置 | `feishu_project_integration` | `status_mapping`、work item type、同步配置 |
| P4/Swarm | `perforce_review` | Swarm review、state、shelved CL、committed CL |
| Issue ↔ P4 | `issue_perforce_review` | issue 与 Swarm review 关联、`close_intent` |

### 当前缺口

- 没有存 AI 前置判断的结构化表。
- 没有存人工最终验收的结构化表。
- `/operations/agent-fixes` 当前主轴是“每个 issue 最新 agent run”，不是“外部已完成工单”。
- `external_fields` 在 DB 是 JSONB object，但前端 issue schema 当前按 `Record<string,string>` 解析；P4 evidence 需要保留 raw JSONB，不应强行 string 化。

### 2026-06-27 真实样本与代码确认

已在 W3 P4 runtime `10.104.9.14` 只读确认以下样本形态：

- `WAR-7392 / BUG-7008945446`
  - Multica issue description 中有 `External-Id: BUG-7008945446`、`External-Url: https://project.feishu.cn/...`。
  - agent run completed，产出 pending P4 CL `280825`。
  - 无 Swarm review，原因是 runtime P4 client 为 non-stream client，`p4 submit` 被拒绝。
  - 这是第一版必须支持的真实状态：**有 pending/shelved CL，但没有 Swarm review，也没有 final CL**。
- `WAR-7512 / BUG-7010257927`
  - agent 评论中记录 `Shelved CL: 267639`、`Swarm Review: http://w3-swarm.lilithgame.com/reviews/267641`。
  - Swarm API 返回 review `267641`：
    - `state = needsReview`
    - `changes = [267639, 267642]`
    - `commits = []`
    - `author = svr_ci`
    - `participants = {"svr_ci":[]}`
  - `267642` 是 Swarm 生成的 companion pending CL，owner/user 为 `swarm`，不是 AI 原始 shelve。

由此确认：

- Swarm review state 必须保存原始值，例如 `needsReview`；不能把“已提交”伪造成 `state=committed`。
- 是否提交由 `commits` 或 committed CL 派生，不能从 Swarm `state` 推断。
- `swarm_reviews[].changes` 必须是数组，并且可能包含 Swarm companion CL。
- `ai_shelved_cls` 不能直接等于 Swarm `changes[]`，需要用 P4 change owner/user、agent comment、task result 等 evidence 过滤出 agent 产物。
- `external_committed_cls` 必须来自飞书/Meego external fields 或人工最终确认，不能从未提交 Swarm review 推导。

### 当前飞书/Meego 同步机制

现有同步由 `FeishuProjectSyncService` 和后台 worker 完成：

- `server/cmd/server/feishu_project_sync_worker.go` 每 5 分钟扫描 enabled integration。
- 每个 integration 按 `work_item_types` 遍历 Meego work item type。
- `QueryWorkItemPagesWithOptions` 通过 Feishu Project OpenAPI 拉取工单；非 targeted sync 会用 status mapping 里的外部状态 key 限定范围。
- `syncWorkItem` 将 Feishu/Meego work item 映射成 Multica issue，并 upsert `feishu_project_issue_binding`。
- `feishu_project_issue_binding.external_status_label` 保存外部原始状态。
- `feishu_project_integration.work_item_types[].status_mapping` 保存外部状态到 Multica 状态的映射。
- `external_fields` 目前只从 work item fields 中提取“提交分支”“开发分支”，并以 `map[string]string` 形式落库。
- OpenAPI parser 已把 fields/multi_texts 同时按 field key 和中文 display name 放入 `FieldValues`；要支持 P4 final CL，仍需扩展 `feishuProjectExternalFieldDisplayName`，把 `提交记录` / `field_6e908d` 纳入 `external_fields`。
- issue detail response 会附带 `external_fields`，但当前只暴露过滤后的字符串 map；完整 binding、raw work item fields、status mapping 不会直接暴露给 agent CLI。
- Multica issue 本地状态变更会按 reverse mapping 尝试反向流转 Feishu 状态；P4 assessment task 必须绕开这条写路径。

因此 P4 assessment agent 可以复用现有 Multica issue、task run、comment、P4/Swarm 证据能力，但还需要一个受控只读 evidence endpoint 把 binding、integration mapping、raw external fields、P4/Swarm 状态统一暴露出来。

P4 assessment 自动触发需要接入这条 sync 链路，但不能改变 Feishu sync 的主结果：

- 接入点在 `syncWorkItem` 成功 upsert `feishu_project_issue_binding` 之后。
- 只有 status mapping 后的本地状态为 `done` 才触发。
- trigger 失败只记录 warning，不让 Feishu sync 从 `created/updated/skipped` 变成 `error`。
- 现有 `reconcileSyncedIssueTasks` 会在 issue 到 `done/cancelled` 时取消该 issue active task；assessment task 上线后，终态取消 SQL 必须排除 `context.type = agent_fix_p4_assessment`，否则下一轮 sync 会误取消 assessment task。

#### 2026-06-27 飞书字段/API 小探测

在 `10.104.9.14` 使用 runtime 的 Multica cloud token 只读探测当前生产 workspace：

- Feishu Project integration 已启用：
  - `project_key = 6718cd2a1d3fdf1b50810683`
  - work item type 为 `issue`，标识前缀 `BUG`
  - status mapping 中 `Iw0fE6Yfa`、`vcvaCnnGi` 映射为 `done`
- 通过现有 Multica API `GET /api/workspaces/{workspace_id}/feishu-project/fields?work_item_type=issue` 能拉到字段元数据。
- 命中的关键字段：
  - `field_6e908d`，名称 `提交记录`，类型 `text`
  - `field_d7788a`，名称 `开发分支（QA不用手动改，这个字段QA不用维护）`，类型 `multi_select`
- `GET /api/issues/by-feishu-project?work_item_type=issue&work_item_id=7008945446` 能反查到 `WAR-7392`。
- 当前部署的 issue detail response 没有暴露 `external_fields`；也没有现成 API 直接返回 binding/raw work item fields。
- 当前 Multica API 没有 Feishu Project 评论/动态代理接口；评论/动态是否可通过官方 OpenAPI 获取，需要在服务端持有 `plugin_secret` 的环境中继续验证，或新增受控只读探测 endpoint。

对第一版的影响：

- `提交记录` 是 final CL 的候选结构化来源，优先级高于飞书评论正则解析。
- 第一版 evidence collector 应先扩展 external fields 提取，把 `提交记录` 原样落入 raw evidence，并按 CL 正则派生 `external_committed_cls`。
- 若 `提交记录` 为空，再考虑飞书评论/动态；在评论接口未验证前，`external_committed_cls` 允许为空并标记 `missing_external_cl`。

## 业务流程

### 1. 外部工单同步

Feishu Project sync 写入或更新 `feishu_project_issue_binding`：

- `work_item_type`
- `work_item_id`
- `external_identifier`
- `external_url`
- `external_status_label`
- `external_fields`
- `last_external_updated_at`
- `last_synced_at`

外部状态通过 integration 的 status mapping 映射到 Multica 状态。只有映射结果为 `done` 的 binding 进入 AI P4 assessment 候选池。判断时必须兼容 legacy `status_mapping` 和 `work_item_types[].status_mapping`，不能再用 `done/closed/完成` 这类字符串启发式替代 status mapping。

### 2. 生成 P4 AI assessment 候选

候选条件：

- 有 workspace、issue、feishu binding。
- 外部状态映射为 `done`。
- 没有有效 `agent_fix_p4_assessment`，或已有 assessment 已 `stale/failed`。
- 可按时间范围、项目、业务线、agent、外部字段筛选。

候选不是质量结论，只表示“需要 AI 先判断并填证据”。

第一版最小闭环先实现单条 trigger API，但服务边界按自动化设计：

- `AssessmentCandidateService` 负责判断 binding 是否 eligible。
- `AssessmentService.Trigger(binding_id, force)` 负责幂等创建/刷新 assessment 和 task。
- 手动 API、Feishu sync worker、后续定时 scanner 都调用同一个 trigger service。
- Feishu sync 自动触发只做 `force=false` 首次创建；`failed/stale/completed` 的重跑第一版走手动 `force=true`。
- 历史 done binding 的补跑不依赖单个 `syncWorkItem`，后续由 scanner 或手动 trigger 覆盖。
- UI 不直接决定一条记录是不是 P4 assessment；UI 只展示 assessment/review 事实并提供补跑入口。

### 3. 创建 AI P4 assessment task

复用 `agent_task_queue`，新增 task context。该 task 仍可绑定 `issue_id`，但必须与普通 issue 修单 task 隔离：

```json
{
  "type": "agent_fix_p4_assessment",
  "workspace_id": "uuid",
  "issue_id": "uuid",
  "feishu_binding_id": "uuid",
  "mode": "assess_only",
  "prompt_version": "p4-assessment-v1"
}
```

executor agent 第一版选择规则：

- 默认使用 issue 当前 assignee agent。
- 如果 issue 无 assignee、assignee 不是 agent、agent 已 archived、agent 无 runtime，则不创建 task，返回不可触发原因，并将该 binding 保留为 candidate。
- 不默认使用“最近修单 agent”，避免权限、runtime 和成本归属不清。

普通 task 隔离规则：

- Operations 的 latest agent run 不把 `context.type = agent_fix_p4_assessment` 的 task 当普通修单 run。
- session resume 查询不从 assessment task 继承 session/work_dir。
- task completion 的“没有 agent comment 则补评论”逻辑跳过 assessment task。
- manual rerun、issue executed analytics、普通 agent task 统计不得把 assessment task 当作普通修单完成。
- `CancelAgentTasksByIssue` / Feishu sync 终态取消逻辑必须排除 assessment task，避免 mapped done 的同步轮次取消正在运行的 assessment。
- 普通修单 dedup 查询（例如 issue+agent 是否已有 task）必须排除 assessment task，避免 assessment 任务阻止真正修单任务入队。

约束：

- 不自动改 `issue.status`。
- 不自动改飞书/Meego 状态。
- 不自动写人工验收。
- 不要求写 issue comment。
- 不提交、不修改 P4/Swarm。
- 输出必须是结构化 JSON。

### 4. Evidence API

后端提供只读 evidence 给 agent 和 UI：

- 外部工单：状态、URL、外部字段、最终 CL/分支/提交信息。
- Multica issue：标题、描述、内部状态、assignee、labels、metadata。
- Agent run：相关 task、status、result、error、failure_reason、usage。
- Agent comment：最近 agent comment 和可能的 shelve/review 线索。
- P4/Swarm：linked review、state、shelved CL、committed CL、close_intent。

授权边界：

- 用户访问 evidence：必须是当前 workspace member，且 binding 属于该 workspace。
- agent 访问 evidence：必须使用 task-scoped token；token 只能读取当前 task context 中的 `workspace_id + feishu_binding_id`。
- API 不暴露 Feishu plugin secret、P4 ticket、Swarm token 等密钥。
- agent 不能直接查 DB，不能使用飞书/P4 密钥，只能通过 Multica 受控 API/CLI 获取 evidence。

### 5. AI 前置判断输出

AI 输出包括归因预测、质量预测、证据和置信度：

```json
{
  "delivery_attribution_prediction": "ai_delivered",
  "quality_prediction": "likely_correct",
  "prediction_reasons": ["ai_shelve_committed", "review_closed"],
  "confidence": 0.82,
  "workstream": "rel_1.7.2/server",
  "swarm_reviews": [
    {
      "review_id": 456,
      "state": "needsReview",
      "changes": [123456, 123457],
      "commits": [],
      "author": "svr_ci"
    }
  ],
  "ai_shelved_cls": [123456],
  "swarm_change_cls": [123456, 123457],
  "swarm_committed_cls": [],
  "external_committed_cls": [],
  "summary": "AI 产生了 shelved CL 并发起 Swarm review，但 review 尚未提交。",
  "warnings": ["swarm_review_not_committed"]
}
```

AI 可以判断“看起来修对了/可能返工/方向可能错/证据不足”，但字段必须叫 `prediction`，不能叫 `review_outcome`，避免和人工最终事实混淆。

结果解析边界：

- 第一版不改 daemon complete 协议；现有 server 会把 complete request 的 `output` 存到 `agent_task_queue.result`。
- assessment parser 从 `result.output` 读取 agent 原始输出。
- prompt 要求 agent 最终输出单个 JSON object；parser 接受整个 output 是 JSON object，或唯一明确的 fenced `json` block。
- parser 只处理 `context.type = agent_fix_p4_assessment` 的 task。
- 找不到 JSON object、字段类型错误、CL 非整数数组、confidence 越界时，assessment 写 `failed` 并记录 parser warning。
- 缺 evidence 不算 parser 失败；agent 应输出 `unknown` 和 `warnings`。

### 6. 人工验收

人工验收统一写入 `agent_fix_review`：

```json
{
  "outcome": "accepted",
  "reasons": ["complete_usable"],
  "note": "AI shelve 最终被提交，验证通过。"
}
```

人工结果是最终事实，用于业务质量统计。

## 状态模型

| 状态 | 来源 | 含义 | 是否最终事实 |
|---|---|---|---|
| `issue.status` | `issue` | Multica 内部工作流 | 否 |
| `task.status` | `agent_task_queue` | agent 运行状态 | 否 |
| `external_status_label` | `feishu_project_issue_binding` | 外部工单状态 | 是，统计分母 |
| `assessment_status` | `agent_fix_p4_assessment` | AI P4 判断任务状态 | 否 |
| `delivery_attribution_prediction` | AI assessment | AI 判断谁修的 | 否 |
| `quality_prediction` | AI assessment | AI 判断修得怎样 | 否 |
| `review_outcome` | `agent_fix_review` | 人工最终验收 | 是，统计结论 |

## 新增存储

### `agent_fix_p4_assessment`

存 AI 前置判断和 P4 证据。

```sql
CREATE TABLE agent_fix_p4_assessment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    feishu_binding_id UUID NOT NULL REFERENCES feishu_project_issue_binding(id) ON DELETE CASCADE,
    assessment_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,

    assessment_status TEXT NOT NULL CHECK (assessment_status IN (
        'pending', 'running', 'completed', 'failed', 'stale'
    )),

    delivery_attribution_prediction TEXT NOT NULL DEFAULT 'unknown' CHECK (delivery_attribution_prediction IN (
        'ai_delivered',
        'ai_assisted',
        'human_delivered',
        'conflict',
        'unattributed',
        'unknown'
    )),

    quality_prediction TEXT NOT NULL DEFAULT 'unknown' CHECK (quality_prediction IN (
        'likely_correct',
        'likely_needs_changes',
        'likely_wrong',
        'unknown'
    )),

    prediction_reasons TEXT[] NOT NULL DEFAULT '{}',
    confidence NUMERIC(4,3),

    workstream TEXT NOT NULL DEFAULT '',
    swarm_reviews JSONB NOT NULL DEFAULT '[]'::jsonb,
    ai_shelved_cls INTEGER[] NOT NULL DEFAULT '{}',
    swarm_change_cls INTEGER[] NOT NULL DEFAULT '{}',
    swarm_committed_cls INTEGER[] NOT NULL DEFAULT '{}',
    external_committed_cls INTEGER[] NOT NULL DEFAULT '{}',
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary TEXT NOT NULL DEFAULT '',
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb,

    model TEXT,
    prompt_version TEXT NOT NULL DEFAULT 'p4-assessment-v1',
    assessed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, feishu_binding_id)
);
```

### `agent_fix_review`

存人工最终验收。

```sql
CREATE TABLE agent_fix_review (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    feishu_binding_id UUID NOT NULL REFERENCES feishu_project_issue_binding(id) ON DELETE CASCADE,
    p4_assessment_id UUID REFERENCES agent_fix_p4_assessment(id) ON DELETE SET NULL,

    outcome TEXT NOT NULL DEFAULT 'unreviewed' CHECK (outcome IN (
        'unreviewed',
        'accepted',
        'needs_changes',
        'rejected',
        'not_applicable'
    )),
    reasons TEXT[] NOT NULL DEFAULT '{}',
    note TEXT NOT NULL DEFAULT '',
    reviewer_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    reviewed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, feishu_binding_id)
);
```

## 枚举

### AI 归因预测

| 值 | 含义 |
|---|---|
| `ai_delivered` | AI 产物就是最终提交结果 |
| `ai_assisted` | AI 有贡献，人补充后提交 |
| `human_delivered` | 最终主要由人提交 |
| `conflict` | AI shelve/review 与最终 CL 不一致 |
| `unattributed` | 有结果但无法归因 |
| `unknown` | 证据不足 |

### AI 质量预测

| 值 | 含义 |
|---|---|
| `likely_correct` | AI 判断大概率修对 |
| `likely_needs_changes` | AI 判断可能还需返工 |
| `likely_wrong` | AI 判断方向可能错误 |
| `unknown` | 证据不足，不判断 |

### 人工验收

| 值 | 含义 |
|---|---|
| `unreviewed` | 待验收 |
| `accepted` | 通过 |
| `needs_changes` | 需返工 |
| `rejected` | 拒绝 |
| `not_applicable` | 不适用 |

## 展示状态优先级

派生 `display_result_status`：

1. 外部工单未完成：`not_in_stats`
2. 外部完成，无 assessment：`needs_ai_assessment`
3. assessment running：`ai_assessing`
4. assessment failed：`ai_assessment_failed`
5. assessment completed 且 `delivery_attribution_prediction = conflict`：`needs_review_conflict`
6. assessment completed 且人工未验收：`needs_human_review`
7. 人工已验收：展示 `accepted / needs_changes / rejected / not_applicable`

人工 outcome 覆盖最终展示结论，但不覆盖 AI 判断和 evidence。

## API 设计

### Operations feed

`GET /api/operations/agent-fixes`

真实主轴应从“外部完成 binding”起步，latest agent run 只是 evidence/display 字段。第一版可以兼容旧的“每个 issue 最新 agent run”feed，但旧 feed 是兼容层，不是 P4 assessment 事实来源，并且必须排除 `context.type = agent_fix_p4_assessment` 的 task。

```json
{
  "issue_id": "uuid",
  "issue_identifier": "WARPATH3-123",
  "issue_title": "...",
  "issue_status": "done",
  "external": {
    "binding_id": "uuid",
    "work_item_id": "...",
    "identifier": "...",
    "url": "...",
    "status_label": "已完成",
    "mapped_status": "done",
    "fields": {}
  },
  "latest_task": {
    "task_id": "uuid",
    "agent_id": "uuid",
    "agent_name": "...",
    "status": "completed"
  },
  "p4_assessment": {
    "status": "completed",
    "delivery_attribution_prediction": "ai_delivered",
    "quality_prediction": "likely_correct",
    "confidence": 0.82,
    "workstream": "rel_1.7.2/server",
    "swarm_reviews": [],
    "ai_shelved_cls": [],
    "external_committed_cls": [],
    "summary": "...",
    "warnings": []
  },
  "human_review": {
    "outcome": "unreviewed",
    "reasons": [],
    "note": "",
    "reviewer_id": null,
    "reviewed_at": null
  },
  "ai_judgement_eval": {
    "attribution_match": null,
    "quality_match": null,
    "mismatch_type": null
  },
  "display_result_status": "needs_human_review"
}
```

### 新增接口

```text
POST /api/operations/agent-fixes/p4-assessments
GET  /api/operations/agent-fixes/{binding_id}/p4-evidence
PATCH /api/operations/agent-fixes/{binding_id}/review
```

第一版建议先支持单条触发 assessment，跑通 warpath3 真实单后再做批量和自动 scanner。

`POST /api/operations/agent-fixes/p4-assessments`：

- 主请求字段是 `binding_id`。
- `issue_id` 只能作为兼容别名；如果一个 issue 对应多个 binding，必须返回 409 要求调用方传 `binding_id`。
- `pending/running` 不重复创建 task。
- `completed + force=false` 返回已有 assessment。
- `completed/failed/stale + force=true` 允许重跑。

## AI 判断准确性

人工验收后动态派生：

| AI 判断 | 人工结果 | 结论 |
|---|---|---|
| `likely_correct` | `accepted` | 判断准确 |
| `likely_correct` | `needs_changes/rejected` | AI 高估 |
| `likely_wrong` | `accepted` | AI 低估 |
| `ai_delivered` | 人工确认人提交 | 归因错误 |
| `conflict` | 人工确认确实冲突 | 冲突识别准确 |
| `unknown` | 任意 | 不计准确率，只计覆盖率 |

判断偏差不作为人工可编辑字段落库。它由 AI 前置判断和人工最终验收动态派生，例如 `likely_correct + accepted = 判断准确`、`likely_correct + needs_changes/rejected = AI 高估`。这样后续修改人工验收后，偏差结果会自动变化。

## 页面设计

### `/operations`

保留原运营明细，新增：

- 外部工单状态
- P4 Review
- Workstream / 分支
- AI Shelve
- 最终 CL
- AI 归因预判
- AI 质量预判
- 置信度
- 人工结果
- 判断偏差
- 编辑标注

分析报告：

- 外部完成数
- AI assessment 覆盖率
- AI 判断准确率
- AI 高估率/低估率
- 归因冲突数
- 人工通过率
- 需返工率
- 拒绝率
- Top reasons
- agent 维度统计
- P4 CL 匹配率

图表实现优先复用 Multica 现有图表栈：

- `packages/ui/components/ui/chart.tsx` 提供统一 `ChartContainer`、tooltip、legend 样式。
- `packages/views/runtimes/components/charts/*` 已有 Recharts 图表实现模式，可复用响应式容器、空态、tooltip 和颜色 token。
- `packages/views/dashboard/components/dashboard-page.tsx` 已有 Dashboard 级趋势图/统计卡片布局经验。

第一版建议在 `/operations` 分析报告里接入：

- 饼图或环图：人工 outcome 分布、AI 归因分布。
- 堆叠柱状图：按项目/agent 展示 accepted、needs_changes、rejected、not_applicable。
- 趋势柱状图：按天/周展示外部完成数、AI assessment 覆盖数、人工已验收数。
- 条形排行：Top reasons、AI 高估/低估 agent 排名、P4 CL 匹配率。

图表只消费 API 聚合结果或前端对当前筛选结果的派生数据，不新增一套图表专用状态；server state 仍由 React Query 管，图表视图选项再放 Zustand/client state。

### `/issues`

不新增 tab，只在卡片/详情融合：

- 外部工单完成态
- AI P4 预判 badge
- P4 CL/Swarm 简要证据
- 人工验收 badge
- 冲突提示
- 跳转 `/operations` 对应记录

显示规则：

- 主条件：存在 `agent_fix_p4_assessment` 或 `agent_fix_review` 记录。
- `metadata.demo`、`metadata.p4_assessment`、`metadata.swarm_review`、`metadata.p4_status`、title 包含 `p4 assessment/swarm` 只作为 demo/兼容兜底。
- metadata 兜底显示不能被当作真实 assessment 事实，也不能触发真实统计。

## 边界场景

- Multica 有 AI shelve，但飞书最终 CL 是别人提交：`conflict` 或 `human_delivered`，人工确认。
- agent task completed，但飞书未完成：不进统计。
- 飞书完成，但没有 AI task：assessment 可判断为 `human_delivered/unattributed`。
- 飞书完成，但 issue assignee agent 不可用：不创建 assessment task，保留 candidate 并返回不可触发原因。
- 飞书完成后同步逻辑取消普通 task：assessment task 必须不受这条取消逻辑影响。
- sync item 无变化走 watermark short-circuit：不触发 assessment；历史 done 补跑由 scanner/手动 trigger 解决。
- assessment task completed 但输出不是合法 JSON：assessment 标记 `failed`，记录 parser warning，不写人工 review。
- assessment task failed/cancelled：assessment 标记 `failed`，允许重跑。
- Swarm review 有 shelved CL 但没有 committed CL：AI 输出 warning，不判断最终成功。
- 飞书外部字段没有最终 CL：`external_committed_cls=[]`，标记 `missing_external_cl`。
- 多个 Swarm review/多个 CL：全部作为 evidence，AI 给 summary，人工最终确认。
- 飞书状态回退：`display_result_status=not_in_stats`，已有 review 保留但标记 stale/out_of_scope。
- AI 判断 unknown：不计入 AI 判断准确率分母，但计入 assessment 覆盖率。
- cross-workspace binding：trigger/evidence/review 必须返回 403/404，不泄露目标存在性。

## 第一版实施顺序

1. Migration：新增 `agent_fix_p4_assessment`、`agent_fix_review`。
2. 后端 service：candidate 判断、trigger 幂等、executor agent 选择。
3. Feishu sync：扩展 `external_fields` 同步 `提交记录`，并在 binding upsert 成功后 best-effort 自动触发 assessment。
4. 普通 task 隔离：终态取消、latest run、session resume、comment fallback、普通 task dedup 全部排除 assessment task。
5. 后端 query/API：按飞书 binding 查询候选、trigger、P4 evidence、review upsert。
6. Agent task：支持 `context.type = agent_fix_p4_assessment`。
7. Evidence API：只读返回飞书 + issue + task + comment + P4/Swarm 数据，并支持 user auth / task-scoped auth。
8. AI result parser：从 `agent_task_queue.result.output` 校验结构化 JSON，写入 `agent_fix_p4_assessment`。
9. API schema：扩展 `/operations/agent-fixes` response，新增 trigger/evidence/review mutation。
10. `/operations` UI：新增列、筛选、手动触发/重跑、编辑人工验收。
11. `/issues` UI：以 assessment/review record 为主信号，metadata 只做 demo 兜底。
12. 用 warpath3 真实单验证 `external_fields`、最终 CL 字段和 Swarm 关联形态。
13. 再做历史 done scanner、分析报告和导出。
