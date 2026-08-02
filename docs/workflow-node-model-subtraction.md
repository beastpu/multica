---
title: Workflow 节点模型收敛 — 九种写法减到三种
type: refactor
status: draft
date: 2026-08-02
topic: workflow
artifact_readiness: review
execution: not-authorized
---

# Workflow 节点模型收敛 — 九种写法减到三种

> 评审稿。评审通过前不创建分支、不做数据库迁移、不改生产代码。

## 0. 一句话

节点上表达「谁来做」有五种写法、「谁来审」有四种，配置者搞不清哪个生效。本方案把它们收敛成
`executor` / `reviewer` / `owner` 三个字段，并去掉不干活的验收节点——`bug_fix` 从 6 个节点减到 4 个。

## 1. 为什么现在做

不是为了好看。这次在测试环境实跑两条流水线时，我配一个节点同时写了
`owner_role`、`executor.strategies`、`issue_templates[].assignee_id` 三处，**并不知道哪个说了算**，
跑起来看执行者解析记录才知道：

```text
"reason": "Resolved from the issue template direct assignee"
```

赢的是第三个，前两个白写。写模板的人搞不清优先级，是节点配置面板"很乱"的根源——
面板乱是症状，模型里有九种写法是病。

## 2. 现状盘点

### 2.1 表达「谁来做」的五种写法

| 写法 | 22 个模板版本中的使用率 |
|---|---|
| `node.owner_role` | 100% |
| `node.executor.strategies[]` | 100% |
| `node.issue_templates[].assignee_id` | 55% |
| `node.issue_templates[].assignee_role` | 32% |
| `node.participant_roles` | 9% |
| `role.default_actor_type/id` | **0%（从未被使用）** |

### 2.2 表达「谁来审」的四种写法

| 写法 | 使用率 |
|---|---|
| `completion.confirmation`（`owner_any` / `owner_all`） | 55% |
| `node.verdict.evaluator`（`member` / `api`） | 见 §2.3 |
| `completion.verdict_required` | 同上 |
| 独立的审查节点（`回归验证`、`Review`） | 内置模板与实跑模板都用了 |

### 2.3 样本说明

统计口径是测试环境 22 个模板版本、78 个活动节点，其中包含为验证造的模板，存在偏差。
`default_actor` 使用率为 0 这条最硬——它在任何版本里都没出现过。

`participant_roles` 那 9% 逐条看过，是两个版本的同一个模板：

```text
review     participant_roles=['product_owner']   owner_role=product_owner
implement  participant_roles=['developer']       owner_role=developer
```

**participant 和 owner 填的是同一个角色，没有表达任何 owner 之外的信息。**
且这两个版本来自内置模板导入，不是用户配的。删除无损。

## 3. 目标模型

### 3.1 节点字段

```jsonc
{
  "key": "fix",
  "kind": "activity",
  "name": "缺陷修复",
  "description": "...",
  "timeout_minutes": 1440,

  // 谁来做。唯一入口。
  "executor": {
    "kind": "role",              // role | actor | manual
    "role": "fixer",
    "fallback": { "kind": "manual" }
  },

  // 谁来审。可选；不写就是做完即准出。
  "reviewer": {
    "kind": "role",              // role | actor | api | owner
    "role": "qa",
    "api_url": null,             // kind=api 时必填，且只能来自模板
    "required": true
  },

  // 谁对这个节点负责。只管权限，不再兼职表达执行者。
  "owner_role": "fixer",

  "issue_policy": "fixed",
  "issue_templates": [
    { "key": "fix_bug", "title": "修复：{{host.title}}", "description": "..." }
    // 注意：不再有 assignee_* —— 由 node.executor 决定
  ],
  "artifacts": [],
  "completion": {
    "required_issue_outcome": "done",
    "handoff_required": true,
    "submission_required": false
  }
}
```

### 3.2 删除清单

| 删除 | 去向 | 依据 |
|---|---|---|
| `role.default_actor_type/id` | 直接删 | 使用率 0% |
| `node.participant_roles` | 直接删 | 仅有的用例里它和 `owner_role` 填的是同一个角色（§2.3） |
| `acceptance` 活动节点 | 降为 End 上的门 | 它不干活、无执行者、无交付物（§6） |
| `node.executor.strategies[]` | 塌缩成 `executor` + 单层 `fallback` | 策略链从未超过两级 |
| `issue_templates[].assignee_role/type/id` | 由 `node.executor` 统一决定 | 消除"三处写、第三处赢"的坑 |
| `node.verdict` | 并入 `reviewer` | `evaluator: member/api` 就是"谁审" |
| `completion.confirmation` | 并入 `reviewer`（`kind: "owner"`） | 它是"负责人来审"的特例 |
| `completion.verdict_required` | 由 `reviewer.required` 表达 | 同一件事的第二个开关 |

**九种写法 → 三个字段（`executor` / `reviewer` / `owner_role`）。**

### 3.3 为什么保留 `owner_role`

它有一个 `executor` 和 `reviewer` 都没有的职责：**决定谁能强制完成、跳过、回滚这个节点**。
这是权限，不是执行也不是评审。`completion.authorized_roles` 继续作为它的扩展。

## 4. 内置模板改写

### 4.1 `bug_fix`：6 → 4 个节点

```text
改前  start → 分诊 → 修复 → 回归验证 → 验收 → end
改后  start → 分诊 → 修复[reviewer=qa] → end[验收门]
```

`回归验证` 收成 `修复` 上的 reviewer 槽位，`验收` 降为 End 上的门。

### 4.2 `requirement_delivery`：6 → 5 个节点

```text
改前  start → 需求评审 → 方案设计 → 代码实施 → 验收 → end
改后  start → 需求评审 → 方案设计[reviewer=owner] → 代码实施 → end[验收门]

方案设计   completion.confirmation: "owner_any"   →  reviewer: { kind: "owner" }
```

## 5. 什么情况下审仍然应该是独立节点

**槽位不是万能的。** 这次实跑的两次"审"，重量完全不同：

| | 实际产出 | 适合 |
|---|---|---|
| 回归验证 | 重跑复现脚本 + 全量 14 项测试，贴真实输出，产出 Submission | **节点** |
| 验收 | `status=approved`，reason 空，evidence 空 | **槽位** |

判据：**审本身需要干活、需要留痕、需要交付物 → 节点；只是通过/驳回加一句话 → 槽位。**

所以本方案是「槽位做默认，重的审仍可拆成节点」，不是「一律收成槽位」。
§4.1 把 `回归验证` 收成槽位，是因为内置模板要覆盖的是常见情况；
真需要跑回归的团队仍可以把它建成节点。

## 6. 验收：保留概念，去掉节点

验收现在是一个 `activity` 节点，但它**不干活、没有执行者、没有交付物、不产生 issue**。
它存在的唯一目的是在画布上占一个位置。

RFC §0.1 给的理由是可见性——"必须表现为画布上的显式验收 Activity……不制造 End 之后的隐藏阶段"。
但可见性是渲染问题，不是模型问题：在 End 节点上画一个门标记同样可见，不需要一个空的活动节点。

```text
删掉   acceptance 作为 activity 节点
保留   definition.acceptance          流程级配置（approver_role / rework_targets）
保留   workflow_acceptance 记录       谁批的、理由、证据、退回目标
渲染   End 节点上的门标记
```

### 6.1 验收记录必须留在实例级，不能并进节点 reviewer

两者判的不是同一件事：

```text
节点 reviewer   这个节点的产出行不行
实例 Acceptance 这个需求做完了没有
```

后者的主体是宿主 Issue，一次运行只发生一次，且带 `rework_target_node_key`——
能定点退回任意上游节点。实测依据：测试环境一次真实回滚，从验收位置把流程退回 `fix`，
而不是退回验收自己。

节点 reviewer 也应当获得"指定退回目标"的能力（不再局限于打回本节点），
但它产生的是节点级判定记录，不是需求级的验收记录。

## 7. 状态机变化

节点状态增加一个：

```text
现有  pending / active / waiting / blocked / completed / skipped / superseded
新增  in_review   —— 执行者交付完成，等 reviewer 判定
```

画布必须能区分渲染 `in_review`，否则"卡在评审"会看不见——这是本方案唯一新增的
可见性风险，且是渲染问题不是模型问题。

## 8. 兼容与迁移

模板版本不可变，**运行中的实例不受影响**，这是前提。

| 对象 | 处理 |
|---|---|
| 已发布的旧版本 | 保持原样运行，不迁移。引擎需同时理解新旧两种 schema |
| 新建/编辑模板 | 只能写新 schema |
| 旧模板"再发一版" | 由用户显式点击「升级到新格式」，看到差异后再发布 |

### 8.1 转换规则

老模板存的是老写法，新引擎认的是新写法，"转换"就是把前者机械翻译成后者：

```text
issue_templates[].assignee_id      → executor { kind: "actor", ... }
issue_templates[].assignee_role    → executor { kind: "role", ... }
executor.strategies[0]             → executor（其余进 fallback）
completion.confirmation            → reviewer { kind: "owner" }
verdict.evaluator = member|api     → reviewer { kind: "member"|"api" }
participant_roles                  → 丢弃（§2.3 已确认与 owner_role 重复，无损）
activity_mode: "acceptance" 节点    → 删除该节点，其入边改指 End
```

全部可机械推导，**没有有损项**。

### 8.2 为什么不自动转

打开编辑器就静默改写用户没看的字段，正是本轮验证一直在批评的那类行为——
`timeout_minutes` 默认 0、`handoff_required` 默认 false、分支不选走默认，
三个问题的共同点都是"系统替用户做了决定但没说"。转换应当是用户的一次显式动作。

## 9. 本方案不做什么

- 不引入 Askhz 那种 `format_checking` 三段状态机——格式校验用 artifact + reviewer 表达即可
- 不给节点加"多执行者"——需要并行就用多个 issue_template 或多个节点
- 不改 gateway / node_choice / 控制节点
- 不改 rework、handoff 这些本轮刚验证过的机制
- 不改验收的**语义**——`rework_targets`、验收记录、定点返工全部照旧，动的只是它在画布上
  是否占一个活动节点

## 10. 待确认

1. `in_review` 状态在画布上怎么渲染——用颜色、徽标还是拆成两个半格
2. End 上的验收门怎么渲染，才能让人看出"这里还需要一次人工决策"
3. 节点 reviewer 拿到"指定退回目标"能力后，可选范围是否与 `acceptance.rework_targets` 一致

## 11. 已排除的顾虑

- **与并行开发冲突**：查过 `codex/workflow-mvp` 的提交历史，`refactor(workflows)` 系列是该分支
  自身的演进，没有并行在改这块的第二条线。此前担心的排期冲突不存在。
- **`participant_roles` 有损**：§2.3 已确认它与 `owner_role` 重复，删除无损。
