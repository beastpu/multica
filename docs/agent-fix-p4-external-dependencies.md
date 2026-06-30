# AI 修单 P4/Swarm 外部依赖与验证清单

> Status: Draft
> Last updated: 2026-06-29
> Related design: `docs/agent-fix-prefill-flow.md`
> API/workflow: `docs/agent-fix-p4-assessment-api-workflow.md`

本文档记录 AI 修单 P4/Swarm assessment 依赖的外部系统事实、已落地边界和仍待验证的问题。主流程不能依赖未验证的外部假设；如果必须先兼容，应在这里明确标成“待验证”或“兼容兜底”。

## 外部依赖状态总览

外部依赖没有完全消失，但第一版依赖面已经收窄：

| 外部系统 | 当前是否是主流程依赖 | Multica server 是否主动调用 | 当前用途 |
|---|---:|---:|---|
| Feishu/Meego Project sync | 是 | 是，沿用既有同步链路 | 写入 binding、外部状态、external fields；提供 mapped done 候选 |
| Feishu/Meego comments/activity | 否 | 否 | 第一版不依赖；若 final CL 只在评论里，当前只能作为 evidence 缺口 |
| P4/Swarm webhook | 是，作为已入库 evidence | 被动接收 webhook，不回查 | 保存 review state、`changes[]`、`commits[]`、branch/event/sent_at、受限 raw payload |
| P4/Swarm 实时查询 | 是，但只在 agent 侧 | 否 | assessment agent 在内网只读查询，用于补齐 server 无法访问的判断 |
| GitHub PR | 否 | 否 | 本阶段不接入、不抽象成统一 PR/CL 模型 |

因此当前依赖模型是：

1. Multica server 依赖既有 Feishu/Meego sync 产出的 DB binding。
2. Multica server 只被动接收 P4/Swarm webhook，不主动访问内网 Swarm/P4。
3. assessment agent 依赖内网只读 P4/Swarm 访问能力。
4. 人工 review 和 UI/export 都只依赖 Multica API response，不新增外部写入。

### 变化点

相比早期方案，以下外部依赖已经被移出主流程：

- 不主动拉 Feishu/Meego comments/activity。
- 不靠 Feishu/Meego 外部状态文案猜测 done，只使用 integration status mapping。
- 不要求 Multica server 访问内网 P4/Swarm。
- 不把 GitHub PR 纳入 P4/Swarm assessment。
- 不由 AI assessment 写 `agent_fix_review` 或修改 issue status。

以下外部事实仍需要验证或持续观察：

- 真实 done work item 的 `提交记录` 是否稳定包含 final CL。
- Swarm webhook 的 `review.commits[]` 是否稳定完整。
- Swarm `changes[]` 中 AI shelve CL、companion CL、人类继续修改 CL 的区分是否足够稳定。
- assessment runtime 是否具备内网只读 Swarm/P4 查询能力。

## 当前设计边界

- 真实主流程从 Feishu/Meego external done binding 出发。
- `agent_fix_p4_assessment` 是 assessment 存在性的事实来源。
- `agent_fix_review` 的人工 review 主键语义是 `workspace_id + feishu_binding_id`。
- `issue.metadata.p4_assessment`、`metadata.demo`、title 中的 `p4 assessment/swarm` 只允许作为 demo/legacy UI fallback，不能作为触发、统计、review 写入或 assessment 存在性事实。
- Evidence API 只读，不调用 Feishu/Meego、P4 或 Swarm 写接口。
- AI assessment 不写 `agent_fix_review`，不改 issue status，不改 Feishu/Meego，不改 P4/Swarm。

## Feishu / Meego

### 状态映射

已实现事实：

- 统计分母是 Feishu/Meego binding，并且外部状态必须通过 integration status mapping 映射到 Multica local status `done`。
- 候选选择必须使用配置映射，不能用外部状态文案启发式，例如 `done`、`closed`、`完成`。
- 当前代码支持新的 `work_item_types[].status_mapping` 模型。
- 旧数据中仍可能存在顶层 `status_mapping`，候选查询和 mapped status 计算必须继续兼容。
- 定时同步只拉取配置映射范围内的外部状态；按 work item ID 的 targeted sync 会绕过 status 和 updated_at 过滤。

仍待注意：

- 如果某个 integration 的 status mapping 配置缺失或配置错误，P4 assessment 不能靠 label 猜测 done。
- 多 work item type 共用项目时，需要确认每个 type 的 mapping 都覆盖 done 状态。

### Binding 同步数据

当前 Feishu sync 会保存 binding 事实：

- `workspace_id`
- `integration_id`
- `issue_id`
- `project_key`
- `work_item_type`
- `work_item_id`
- `external_identifier`
- `external_url`
- `external_status_label`
- `last_external_updated_at`
- `last_synced_at`
- `external_fields`

同步还会更新 Multica issue 的 title、description、status、priority、assignee、project、attachments、labels 和 subscribers。

### External Fields

warpath3 生产 workspace 已知字段：

| Field key | Display name | Type | Use |
|---|---|---|---|
| `field_6e908d` | `提交记录` | `text` | final CL / `external_committed_cls` 的候选来源 |
| `field_d7788a` | `开发分支（QA不用手动改，这个字段QA不用维护）` | `multi_select` | workstream / branch evidence |

当前实现：

- OpenAPI parser 已按 field key 和 display name 同时索引 field values。
- `external_fields` 已保留 `提交记录`，并把 `field_6e908d` 或包含 `提交记录` 的字段名归一为 `提交记录`。
- `external_fields` 继续保留 `提交分支` 和 `开发分支`。
- Evidence API 会把 binding 的 `external_fields` 原样作为只读 evidence 返回给 assessment。

仍待外部验证：

- 真实 done work item 的 `提交记录` 是否稳定包含 final CL。
- `field_6e908d` 是否在所有目标项目中稳定；如果不稳定，需要引入 per-project field mapping 策略。
- `提交记录` 的真实内容格式，以及 CL 提取规则是否能稳定覆盖：
  - 单个 CL。
  - 多个 CL。
  - 带链接、中文说明、换行或富文本残留的 CL。
  - 非 CL 内容或空字段。

数据缺口：

- 新 whitelist 只影响后续同步或重新同步。历史 binding 如果没有重新同步，`external_fields` 里可能仍缺 `提交记录`。
- 历史样本要验证 final CL 时，需要 targeted sync、manual resync 或一次性数据修复策略，不能假设 backfill scanner 会自动刷新外部字段。

### Comments / Activity

当前事实：

- Multica 当前没有 Feishu Project comments/activity 代理。
- 第一版 assessment 不依赖 Feishu comments/activity 推断 final CL。
- 如果 final CL 只存在于 Feishu comments/activity，当前 evidence 可能只能得到 `external_committed_cls=[]`，并由 AI 输出 `missing_external_cl` 一类 warning。

待验证：

- Feishu/Meego comments/activity 是否有稳定 API、权限和字段能返回 final CL。
- 如果后续接入 comments/activity，需要先定义只读 evidence 边界和 secret 过滤策略。

## Feishu Sync Effects

已实现事实：

- Feishu sync 默认每 5 分钟扫描 enabled integrations。
- `syncWorkItem` 可以创建或更新 Multica issue，并 upsert `feishu_project_issue_binding`。
- 当同步后的 issue 达到 local `done` 或 `cancelled`，普通 active issue tasks 会被取消。
- assessment task 已通过 `context.type = "agent_fix_p4_assessment"` 与普通 issue task 隔离，终态取消、普通查询、统计、resume 和 completion comment fallback 都不应混用。
- binding upsert 后，如果 status mapping 判断为 local `done`，sync 会 best-effort 调用 P4 assessment trigger，`force=false`。
- 历史 done binding scanner 已接入 Feishu Project sync worker，在同一 integration advisory lock 下运行。
- 历史 scanner 从 `feishu_project_issue_binding` 出发，使用 integration `status_mapping/work_item_types` 判断 mapped done。
- 历史 scanner 只补没有 `agent_fix_p4_assessment` 记录的 binding，不自动重跑 failed/stale/completed。
- 历史 scanner 不使用 metadata/demo/title 作为触发事实。

仍待注意：

- Watermark short-circuit 会让未变化的 item 返回 `skipped`，不会刷新 binding 内容；历史字段缺失时需要主动 targeted sync 或数据修复。
- 自动 trigger 是 best-effort，失败只记录日志，不应阻断 Feishu sync 主流程。

## P4 / Swarm

已确认事实：

- Swarm review state 必须保存原始 Swarm state，例如 `needsReview`。
- 不能虚构 `committed` 这类 pseudo state。是否提交必须来自 `commits[]`、`committed_cl` 或其他 submitted CL evidence。
- `swarm_reviews[].changes` 可能包含 Swarm companion CL，不能把每个 change 都当成 AI shelved CL。
- 当前 Multica `perforce_review` 已保留兼容投影 `shelved_cl` / `committed_cl`，并新增保存 webhook payload 中的 `changes[]`、`commits[]`、Swarm branch、event type、sent_at 和受限 raw payload。
- 当前 Evidence API 只聚合 DB 内已有 `perforce_review` / `issue_perforce_review`，不会由 server 实时查询 P4/Swarm；实时 P4/Swarm 判断由 assessment agent 在内网只读完成。

已确认样本：

- `WAR-7392 / BUG-7008945446`：completed agent run 有 pending P4 CL `280825`，无 Swarm review，无 final CL。
- `WAR-7512 / BUG-7010257927`：agent comment 记录 shelved CL `267639` 和 Swarm review `267641`；Swarm state 是 `needsReview`，changes 包含 `[267639,267642]`，commits 为空。CL `267642` 是 Swarm companion CL。

仍待外部验证：

- Swarm webhook payload 是否能稳定提供完整 `review.commits[]`。
- 是否存在“只有 final CL、没有 Swarm review”的真实样本。
- 是否存在 AI shelve 被人类继续修改或提交的真实样本。
- Swarm companion CL、AI 原始 shelve CL、人类提交 CL 的 owner/user/description 组合是否足够稳定，可用于 attribution。
- 多个 Swarm review 或多个 CL 关联同一个 issue 时，当前单 `perforce_review` 投影是否足够，还是需要更丰富的 evidence 结构。

## Runtime / P4 Client 限制

- 部分 runtime 可能只能产出 pending/shelved CL evidence，因为 P4 client 无法 submit 或无法创建完整 Swarm 流程。
- 有 pending CL 但没有 Swarm/final CL 时，只能视为 evidence incomplete，不能直接判断为 AI delivery success。
- 缺少 P4/Swarm evidence 时，assessment 应输出 `unknown` prediction 和 warning，而不是猜测 attribution。

## final CL 提取边界

当前阶段：

- 后端已保存和暴露 `提交记录` 原文。
- assessment parser 只从 `agent_task_queue.result.output` 读取 AI 输出，并写入 `external_committed_cls`。
- 当前还没有后端 deterministic CL extractor 从 `提交记录` 派生结构化 `external_committed_cls`。

后续可选方向：

- 保持第一阶段策略：Evidence 暴露 `提交记录` 原文，由 AI 提取 CL，人工 review 兜底。
- 或新增后端 deterministic extractor：从 `提交记录`、Swarm commits、P4 submitted evidence 中提取 CL，作为结构化 evidence 输入给 AI。

在没有真实样本验证前，不应把某个正则规则当成最终事实来源。

## 安全边界

- Evidence API 不得暴露 Feishu plugin secret、P4 ticket、Swarm token 或其他 credential。
- User access 必须校验 workspace membership 和 binding workspace scope。
- Agent access 必须使用 task-scoped token，并且只能读取 task context 中记录的同一个 binding evidence。
- Cross-workspace 或 cross-binding 访问必须 fail closed，不能泄露目标是否存在。
- Evidence task projection 不暴露 task `result/context/session_id/work_dir/runtime_id`。

## Internal-only API/UI Work

binding-id human review API 和 review UI 合并不新增 Feishu、Meego、P4 或 Swarm 外部依赖。

- `PATCH /api/operations/agent-fixes/{binding_id}/review` 只写 Multica `agent_fix_review`。
- handler 通过 Multica DB row 校验 workspace membership 和 binding workspace scope。
- API 不调用 Feishu/Meego、P4 或 Swarm，也不能修改这些系统。
- Operations 和 Issues 共用人工 review editor，这是基于已有 Multica API response 的前端 DRY 清理，不是新的外部数据源。
- Issue metadata/title fallback 只是 legacy/demo 显示信号，不能用于触发 assessment、写 review 事实或决定统计分母。

## 验证清单

代码已覆盖：

- `提交记录` / `field_6e908d` 会进入 `external_fields`。
- Evidence task projection 不暴露 raw task internals。
- Evidence review projection 保留 Swarm review id/state/shelved CL/committed CL。
- Evidence SQL 按 workspace + issue 限定并限制返回数量。
- Backfill scanner 从 binding + status mapping 出发，不使用 metadata/demo/title。
- binding-id review SQL 以 binding 作为写入主轴，不依赖 metadata/demo/title。

外部仍待验证：

- 真实 done work item 中 `提交记录` 字段内容。
- `提交记录` CL 提取规则。
- `field_6e908d` 在目标项目中的稳定性，或 per-project mapping 策略。
- pending CL without Swarm 的更多样本。
- Swarm companion CL 样本。
- final CL from Swarm commits/webhook 的样本。
- AI shelve 被人类继续修改或提交的样本。
- 历史 binding 缺 `提交记录` 时的 resync / backfill 数据修复流程。
