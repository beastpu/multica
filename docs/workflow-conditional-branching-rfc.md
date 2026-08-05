---
title: Workflow 条件分支重设计 — 结构化输出变量 + 多 case Gateway
type: feat
status: implemented
date: 2026-08-05
topic: workflow
artifact_readiness: approved
execution: authorized
---

# Workflow 条件分支重设计 — 结构化输出变量 + 多 case Gateway

> 本文取代现有 `submission.choice` 分支机制。设计基于对 n8n / Windmill / Conductor /
> Argo / Dify / Flowise / LangGraph / CrewAI 八个开源工作流与 Agent 编排项目的横向调研
> （§4），落到 Multica 的开发与缺陷修复场景，并与 Run 一等对象架构
> （[workflow-architecture-rfc §0.0](./workflow-architecture-rfc.md)）对齐（§7）。
>
> **状态：已实现并部署至 `multica-test`。** 落地顺序 1–5 步全部完成，6–7 步按 §6.3 的
> 更正调整后落地；实现与设计的每一处偏差、以及实现证伪的两条论断，都记在 §9 的实施状态块
> 和对应章节的更正框里。

## TL;DR

- **问题**：现在的分支机制要求干活的 Agent 在交付时声明「我走哪条边」（`submission.choice`）。
  这把编排层的实现细节泄漏给了执行者 —— 加一条分支要改所有上游 Agent 的认知，改分支名
  要改上游，值还是没有 schema 的自由字符串。而且执行链是断的（§1.2 ③）。
- **方案**：拆成三层。① 节点声明**输出字段 schema**；② Agent 交付**结构化字段**（不是分支名）；
  ③ Gateway 基于字段配 **if / elseif / else 多 case，每个 case 一个输出端口**。
- **核心原则**：**Agent 报告领域事实，编排者决定流程走向。** Agent 交付
  `{is_bug: false}`，它不知道 `end` 节点的存在，也不知道任何下游节点的名字。
- **业界印证**：本方案的 `switch` / `filter` 双模式与 Windmill 的 BranchOne / BranchAll
  一一对应；有序求值 + 强制兜底是 Dify IF/ELIF/ELSE 与 Windmill 的共同选择；节点级
  失败出口对应 Dify v0.14 的 Error Handling 三策略。没有一家让干活的节点声明流程意图。
- **废弃**：`workflow_node_submission.choice` 列、`ChoiceBranchesForNode`、
  `branch-choice.ts`、Agent 提示里的「这个节点要选一条分支」段落、condition DSL 的
  `node_choice` 与 `node_verdict` source（verdict 统一进变量池，§7.3）。
- **必须同时补齐三个缺口**（§6）：`mode: filter`（附加门控）、返工轮次上限、
  节点级失败/超时出口。前两个已实现；第三个的定位在实现后下调（见 §6.3 更正）。

## 1. 现状与问题

### 1.1 现有模型

分支值来自上游 activity 交付时写的 `workflow_node_submission.choice`
（migration 246，字段定义 [`workflow_node.go:284`](../server/internal/handler/workflow_node.go)）。
Gateway 节点读它做路由：

- Gateway 无 executor / issue / agent / 交付，算完 target 直接置 `completed`，只落一条
  `node.routed` 事件（[`workflow_graph_runtime.go:158`](../server/internal/handler/workflow_graph_runtime.go)）。
- 出边的 `condition` + `default` 只有 gateway 能带（[`definition.go:1120`](../server/internal/workflow/definition.go)）。
- 校验要求：至少 2 条出边、恰好 1 条 `default`、非 default 边必须带 condition
  （[`definition.go:1056`](../server/internal/workflow/definition.go)）。

### 1.2 具体问题

**① 语义错位：Agent 被要求声明流程意图**

`choice` 的值是「走哪条边」，不是「我的结论是什么」。后果：

- 新增一条分支 → 所有上游 Agent 的提示词要跟着改
- 分支重命名 → 上游 Agent 的认知失效
- Agent 需要理解整个流程拓扑才能正确交付

业界共识与此相反：决策应外化到编排层，Agent 只报告事实
（[Mastra](https://mastra.ai/articles/ai-agent-workflows)、[Orkes](https://orkes.io/blog/agentic-ai-explained-agents-vs-workflows)；
§4 调研的八个项目没有一家让干活的节点声明目标边）。

**② 无 schema、无类型、无校验**

`choice` 是自由字符串。前后端可接受范围还不一致：

- 后端接受**任意可达节点 key**（[`workflow_node.go:2131`](../server/internal/handler/workflow_node.go)）
- 前端下拉只列条件里 `eq` 字面匹配到的值（[`branch-choice.ts:60`](../packages/views/workflows/branch-choice.ts)）

API 直调传一个没有任何 gateway 条件读取的值会被静默接受，然后 gateway 落 default。

**③ 执行链断裂**

Agent 提示明确指示执行 `multica workflow submit --summary "<结论>" --choice <值>`
（[`workflow_context.go:259`](../server/internal/daemon/execenv/workflow_context.go)），
但 CLI 从未注册 `--choice` flag（[`cmd_workflow.go:82-88`](../server/cmd/multica/cmd_workflow.go)），
`runWorkflowSubmit` 提交时也只发 `summary`。Agent 照提示执行会被 cobra 以
`unknown flag` 拒绝，分支选择静默丢失，流程落 default。

**④ 运行时才暴露的配置错误**

多条 case 同时命中直接报错（[`workflow_graph_runtime.go:406`](../server/internal/handler/workflow_graph_runtime.go)）。
编排者必须自己保证条件互斥，而这个约束只在运行时才被检查。

**⑤ 与真实场景错配**

Multica 的主场景是开发和缺陷修复。这类流程里的分支实际只有四种形态：

| 形态 | 例子 | 现有 gateway 表达 |
| --- | --- | --- |
| **提前终止** | 不是 bug / 重复单 / 不修 | 别扭：菱形 + 一条指向 end 的边 |
| **返工环路** | 评审不过打回重做 | 走 rework 机制重建节点（图强制 acyclic，边不能回指），无轮次保护 |
| **附加门控** | 碰了 DB → 加 DBA 评审，主线继续 | 表达不了（XOR 必须选一条） |
| **真·多路分流** | 三类问题走三条完全不同的路 | 合适，但开发场景里最少见 |

Gateway 最适配的恰好是出现频率最低的那一种。

## 2. 设计原则

1. **Agent 报告领域事实，编排者决定流程走向。** Agent 的交付里不出现任何下游节点名或分支概念。
2. **概念数量最小。** 只引入两个用户可见概念：**输出字段**、**条件 case**。两者都是用户在
   Dify / n8n / Langflow 里见过的心智，不发明新词。
3. **职责分离。** Activity 只管产出，Gateway 只管路由，语义分类是独立的 classifier 节点
   （Dify 与 Flowise 的共同选择，§4.2）。
4. **配置错误在保存时暴露，不在运行时。** 有序求值 + 自动 else，从结构上消灭「无兜底」和
   「多条命中」两类运行时错误。
5. **失败是一等状态。** Agent 跑飞、超时、字段不合法都必须有明确去向，不能静默落默认分支。

## 3. 三层设计

### 3.1 变量层：节点输出字段

节点配置里新增「输出字段」面板，与 executor 平级：

```yaml
node: triage
executor: { agent: triage-bot }
outputs:
  - { key: is_bug,     type: bool,   required: true,  desc: "是否为真实缺陷" }
  - { key: category,   type: enum,   values: [bug, duplicate, feature_request, works_as_intended], required: true }
  - { key: severity,   type: enum,   values: [low, medium, high, critical] }
  - { key: root_cause, type: string, max_len: 500 }
```

**类型只给五种**：`bool` / `enum` / `number` / `string` / `string[]`。

不给自由类型，因为每种类型对应一组确定的比较运算符，这是 Gateway 条件编辑器能做成
「三个下拉」而不是「写 JSON」的前提。

**作用域规则 —— 声明是节点级的，引用默认全局，冲突时强制限定：**

- 只有一个节点声明了 `is_bug` → 条件直接写 `is_bug`
- 两个节点都声明了 `done` → **保存时校验报错**，必须写 `fix.done` 才能通过

全局池最省事，但节点一多必然出现同名字段互相覆盖。上述规则让常见情况保持轻量，
冲突情况在编译期就挡住，不会到运行时才发现读错了节点。这与 Windmill 的
`results.<step>.<field>` / `flow_input.<field>` 命名空间设计同构（§4.1），只是常见
情况免写前缀。

**保留命名空间**：`issue.*` 留给宿主 Issue 字段（§7.2），`error_type` / `error_message`
留给失败分支注入（§6.3）。node key 禁用 `issue`（加入 definition 校验）。

**可见性**：Gateway 只能读**上游可达节点**的字段（沿用现有 `workflowPathExists` 约束，
[`definition.go:1076`](../server/internal/workflow/definition.go)）。条件编辑器的字段下拉只列这些，
物理上写不出引用旁支节点的条件。

### 3.2 产出层：Agent 交付契约

三个环节缺一不可。**这一层决定整套方案在真实 Agent 上的可靠性。**

#### ① 注入 schema 到 task context

删除 `renderWorkflowChoice`（[`workflow_context.go:241`](../server/internal/daemon/execenv/workflow_context.go)）
整段，换成「你要交付这些字段」：

    ### 这个节点要交付的结构化字段

    | 字段 | 类型 | 必填 | 说明 |
    | --- | --- | --- | --- |
    | is_bug | 布尔 | 是 | 是否为真实缺陷 |
    | category | 枚举: bug / duplicate / feature_request / works_as_intended | 是 | |
    | severity | 枚举: low / medium / high / critical | 否 | |

    提交：

        multica workflow submit --summary "<结论>" --set is_bug=false --set category=works_as_intended

**这段提示里不出现任何下游节点名、不出现分支概念。** 这是与 `choice` 的根本差别。

#### ② CLI 契约

```bash
multica workflow submit --summary "..." --set is_bug=false --set severity=high
multica workflow submit --summary "..." --json '{"is_bug": false, "category": "duplicate"}'
```

- `--set key=value`：给人和简单 Agent 用，可重复。
- `--json`：给能生成结构化输出的 Agent 用。

两个都要有 —— 只给 `--json` 的话 Agent 拼 JSON 容易出转义错误；只给 `--set` 的话
复杂值（数组、长文本）不好传。

#### ③ 校验失败必须能让 Agent 自己修

服务端按 schema 校验，失败返回**结构化、可操作**的错误：

```json
{
  "error": "output_validation_failed",
  "fields": [
    { "key": "severity", "problem": "invalid_enum", "got": "urgent",
      "expected": ["low", "medium", "high", "critical"] },
    { "key": "is_bug", "problem": "missing_required" }
  ]
}
```

CLI 把它渲染成人话打到 stderr，Agent 看到就能自己重交一次。

**这一步不做，整套方案的可靠性就退化回 `choice` 的水平** —— 字段静默为空，Gateway 静默落 else。

**有 outputs 声明的节点拒绝 summary-only 提交**：现有纯 `--summary` 提交在这类节点上
返回校验错误（错误体带完整 schema 表格，等同于把提示重发一遍）。宁可打断也不静默 ——
缺字段落 else 正是 choice 的失效模式。无 outputs 声明的节点行为不变。

**未声明字段的处理**：宽松接受 —— 存进 submission、打警告日志，但不进变量池、不出现在
条件编辑器下拉。便于调试 prompt 漂移，同时不给条件系统引入未声明的自由变量。
（n8n / Windmill 对多余字段同样是存而不理。）

#### 关于 function calling

能力更强的 runtime 可以把 outputs schema 直接转成 tool definition 让模型调用
（对齐 LangGraph 的 `with_structured_output`）。但 CLI 是所有 runtime 的通用底座，
**先做 CLI，function calling 作为 runtime 层的优化后加**。两者写进同一张 submission 表，
对 Gateway 无差别。

### 3.3 路由层：多 case Gateway

```yaml
node: after_triage
kind: gateway
mode: switch          # switch(XOR，对应 Windmill BranchOne) | filter(多命中都走，对应 BranchAll)
cases:
  - { id: c1,   label: "非缺陷",   when: is_bug == false }
  - { id: c2,   label: "重复单",   when: category == "duplicate" }
  - { id: c3,   label: "高危",     when: severity in ["high", "critical"] }
  - { id: else, label: "继续修复" }        # 自动生成，不可删，永远排最后
```

**求值规则：**

- 每个 case 一个输出端口，端口上连一条边，画布上端口标 `label`。
- **有序求值，第一个命中的赢**（Dify IF/ELIF/ELSE 与 Windmill BranchOne 的共同语义）。
  编排者不需要保证条件互斥，也不会有「多条同时命中」这种只在运行时爆的错误。
  这是相对现有实现最大的体验提升。
- `else` 端口**自动存在、不可删除**。n8n 的 Switch 把兜底做成三选一
  （忽略 / 额外端口 / 并入第一端口，§4.1）—— 数据管道里「忽略不匹配的 item」是合理选项，
  但流程节点会建 issue、派任务，忽略等于流程静默死，所以这里强制 else。
- case 可上下拖动排序，排序即优先级，所见即所得。

**条件编辑器**：选字段 → 选运算符 → 填值，三个下拉。运算符按字段类型自动收窄：

| 类型 | 运算符 |
| --- | --- |
| `bool` | 是 / 否 |
| `enum` | 等于 / 不等于 / 属于 / 不属于 |
| `number` | = / ≠ / > / ≥ / < / ≤ |
| `string` | 等于 / 包含 / 开头是 / 结尾是 / 匹配正则 / 为空 / 非空 |
| `string[]` | 包含 / 不包含 / 长度 |

多条件用 AND / OR 拼，一个 case 内最多一层嵌套。需要更深嵌套的切「高级模式」写文本
表达式（`fix.touched_db == true && (severity == "high" || issue.priority >= 2)`），
编译成同一个 AST。

**表达式能力边界（已定案）**：高级模式只是同一个 AST 的文本语法 —— 表达能力严格等于
「AND / OR / NOT 任意嵌套 + 五类型运算符」，**不做**算术、字符串拼接、函数调用、自定义
脚本。Windmill 给完整 JS 表达式是因为它有执行沙箱；这里不引入第二个需要沙箱和版本管理
的 DSL。AST 表达不了的判断说明它是语义判断，下沉为一个 classifier 节点（§7.3）——
让 Agent 判，产出一个 enum 字段，gateway 照常读。

### 3.4 结构化输出是独立组件

outputs schema 不是 workflow 的私有细节，按独立组件设计，分三层复用：

**代码层**

- Go：schema 定义、校验、结构化错误生成收敛为独立包（如 `server/internal/structout`）。
  workflow 的 submission 校验、task context 渲染、表单元数据接口都调它，不各写一份。
- TS：`SchemaFieldsEditor`（编排者定义字段）与 `SchemaForm`（按 schema 生成交付表单，
  §7.5）两个共享组件放 `packages/views`，类型放 `packages/core`。

**产品层：workspace 级 schema 库**

- 命名 schema（如 `bug-triage`）作为 workspace 资源，节点编辑时从库里引用，也可以内联
  一次性定义。同类节点跨模板共用一套字段（所有分诊节点交同一组字段），不必每个模板重配。
- **引用在模板保存时快照进 template version** —— 运行中的模板不受库后续修改影响，与
  template version 既有的不可变语义一致。库是编辑期的复用来源，不是运行期依赖。

**消费方**

workflow 节点交付（本文）、聊天结构化确认
（[chat-ask-structured-signal-spec](./chat-ask-structured-signal-spec.md) 要解决的
「Agent 回传结构化信号」是同构需求）、未来 Agent 任务的验收字段。第 1 步就按独立包实现，
避免先长在 workflow 里再拆。

## 4. 业界方案调研

八个项目的分支机制横向对比。选取标准：有画布的低代码工作流（n8n / Windmill / Dify /
Flowise）、代码优先的编排引擎（Conductor / Argo）、Agent 框架（LangGraph / CrewAI）。

### 4.1 逐家结论

| 项目 | 分支机制 | 兜底 | 多命中 / 并行 | 失败处理 | 环路 |
| --- | --- | --- | --- | --- | --- |
| **n8n** | Switch 节点多 rule → 多输出端口；IF 节点双端口 | Fallback Output 三选一：忽略 / 额外端口 / 并入第一端口 | 每条 rule 独立端口 | Error workflow / 节点 error output | 无原生环路 |
| **Windmill** | **BranchOne**：谓词按序求值，第一个 true 的分支执行 | default 分支必有 | **BranchAll**：所有分支并行执行 | 每步 retry + error handler | for-loop 节点 |
| **Conductor** | SWITCH task：evaluator + case 映射 + defaultCase | defaultCase | FORK_JOIN + JOIN（等指定任务列表） | 任务级 retryCount / 超时策略 | **DO_WHILE：每轮输出按迭代号 `__i` 索引存档，条件可引用任意一轮的输出** |
| **Argo** | DAG `when` 条件 + `depends` 表达式 | 无强制兜底 | depends 带状态选择器 `A.Succeeded \|\| A.Failed / Omitted` | retryStrategy(limit)；**skipped 被下游视作 Succeeded 的传播坑（#3378）** | 无原生环路 |
| **Dify** | If-Else（IF/ELIF/ELSE 有序求值）+ Question Classifier（LLM 分类 → category_id 绑边） | ELSE 必有 | 不支持多命中 | **v0.14 节点级三策略：默认值 / 失败分支 / 无；失败分支注入 `error_type` / `error_message` 变量；retry(max_retry, interval)** | 迭代节点 |
| **Flowise V2** | Condition 节点（确定性）与 Condition Agent 节点（LLM 语义路由）**分成两个节点** | else 端口 | 不支持 | 节点级 | Loop 节点 |
| **LangGraph** | router 纯函数读 state 返回节点名；`path_map` 解耦判断值与节点名 | router 自己写 default 返回 | **router 返回 list = 并行激活** | 代码层 try/except | 图允许环，`recursion_limit` 兜底 |
| **CrewAI Flows** | `@router` 装饰器返回字符串标签，`@listen` 监听标签 | 代码层 | 多 listener | 代码层 | 代码层 |

### 4.2 对本设计的直接印证

1. **`switch` / `filter` 双模式 = Windmill BranchOne / BranchAll 的精确对应。**
   这不是本文发明的语义，是已被验证的一对原语。
2. **有序求值 + 强制兜底**是 Dify、Windmill 的共同选择；n8n 给了三种兜底但也默认提供。
   Argo 是反例 —— 没有强制兜底，社区里 skipped/omitted 状态传播的 issue（#3378、#8654、
   #13498）长年不断。**兜底不强制，坑就转移给用户。**
3. **确定性路由和语义路由分成两个节点**：Dify（If-Else vs Question Classifier）和
   Flowise（Condition vs Condition Agent）独立演化出同一结论。语义分类的 prompt、模型、
   置信度需要独立配置和调优，塞进通用路由节点两头做不好。→ 印证 §7.3 的 classifier 定位。
4. **失败处理是节点属性，不是路由条件**：Dify v0.14 专门为此发版（默认值 / 失败分支 / 无
   + retry 参数），且失败分支注入 `error_type` / `error_message` 变量供下游条件使用。
   → 直接采纳进 §6.3。
5. **环路历史值有成熟先例**：Conductor DO_WHILE 把每轮输出按迭代号索引存档
   （`taskRef__2` 引用第二轮）。→ 解决原开放问题「环路中变量取哪轮」（§10 已收敛）。
6. **没有一家让干活的节点声明目标边。** 最接近的是 LangGraph 的 `Command(goto=...)`，
   但它要求 `Command[Literal[...]]` 静态标注可达集合，图校验强制通过 —— 恰好反衬
   `choice` 的问题：声明目标却无静态约束。

## 5. 场景库

### 5.1 缺陷分诊：提前终止 + 返工环路

```
triage ──→ after_triage ┬─[非缺陷]──→ end(not_a_bug)
                        ├─[重复单]──→ end(duplicate)
                        └─[继续]────→ reproduce ──→ after_repro ┬─[无法复现]→ end(needs_info)
                                                                 └─[已复现]──→ fix ──→ review
                                                                                        │
                          ┌──────────── rework(max_passes: 3) ───────────────────────────┤
                          ↓                                                              │
                         fix                                    after_review ────────────┘
                                                                     ├─[置信度低]→ human_review
                                                                     ├─[要求修改]→ rework → fix
                                                                     └─[通过]────→ end(fixed)
```

节点定义：

```yaml
- node: triage
  outputs:
    - { key: is_bug,   type: bool, required: true }
    - { key: category, type: enum, values: [bug, duplicate, feature_request, works_as_intended], required: true }
    - { key: severity, type: enum, values: [low, medium, high, critical] }
  on_failure: { then: suspend, notify: [reporter] }

- node: after_triage
  kind: gateway
  cases:
    - { id: c1,   label: "非缺陷", when: is_bug == false }
    - { id: c2,   label: "重复单", when: category == "duplicate" }
    - { id: else, label: "继续修复" }

- node: fix
  outputs:
    - { key: done,        type: bool,   required: true }
    - { key: touched_db,  type: bool }
    - { key: touched_api, type: bool }
    - { key: pr_url,      type: string }
  on_timeout: { after: 2h, then: suspend }

- node: review
  reviewer: { agent: reviewer-bot }        # verdict/confidence/reason 是系统内建 outputs，见 §7.3

- node: after_review
  kind: gateway
  cases:
    - { id: c1,   label: "置信度低",  when: review.confidence < 0.7 }
    - { id: c2,   label: "要求修改",  when: review.verdict == "changes_requested" }
    - { id: c3,   label: "需 DBA",   when: fix.touched_db == true }
    - { id: else, label: "通过" }
```

注意 `fix.touched_db` —— 条件可引用**任意上游可达节点**的字段，不限于紧邻上游。

### 5.2 快修 / 标准修：按类型与紧急程度选修复链路

第二类分支不是「要不要修」，而是「走哪条修复链路」。triage 的 outputs 加一个
`urgency` 字段即可驱动：

```yaml
- node: triage
  outputs:
    - { key: category, type: enum, values: [crash, data_loss, ui, perf, logic], required: true }
    - { key: severity, type: enum, values: [low, medium, high, critical], required: true }
    - { key: urgency,  type: enum, values: [immediate, normal], required: true, desc: "线上是否在流血" }

- node: route_fix
  kind: gateway
  cases:
    - { id: c1,   label: "快修", when: urgency == "immediate" && severity in ["high", "critical"] }
    - { id: else, label: "标准修" }
```

```
route_fix ┬─[快修]───→ hotfix ──→ 冒烟验证 ──→ 发布 ──→ 事后评审 ──→ end
          └─[标准修]─→ reproduce ──→ fix ──→ review ──→ 回归 ──→ 发布 ──→ end
```

要点：**快修不是跳过评审，而是把评审移到发布后** —— 事后评审也是一个普通 activity。
链路差异完全由编排表达，triage Agent 的交付在两条链路下没有任何区别。

### 5.3 功能开发：按规模与影响面分级开发链路

```yaml
- node: spec                      # 需求分析
  outputs:
    - { key: size,         type: enum, values: [small, medium, large], required: true }
    - { key: blast_radius, type: enum, values: [isolated, module, cross_module], required: true, desc: "影响面" }
    - { key: touches_db,   type: bool }
    - { key: touches_api,  type: bool }

- node: route_dev
  kind: gateway
  cases:
    - { id: c1,   label: "重链路", when: size == "large" || blast_radius == "cross_module" }
    - { id: c2,   label: "轻链路", when: size == "small" && blast_radius == "isolated" }
    - { id: else, label: "标准链路" }

- node: extra_gates               # 附加门控，与 route_dev 并挂在 spec 之后
  kind: gateway
  mode: filter
  cases:
    - { id: g1,   label: "需 DBA",      when: touches_db == true }
    - { id: g2,   label: "需兼容评审",  when: touches_api == true }
    - { id: else, label: "无附加门控" }
```

```
route_dev ┬─[重链路]───→ 设计评审 ──→ 拆分子任务 ──→ 开发 ──→ 双人评审 ──→ 回归 ──→ 合并
          ├─[轻链路]───→ 开发 ──→ 自检 ──→ 合并
          └─[标准链路]─→ 开发 ──→ review ──→ 合并
```

要点：三条链路由 `spec` 一个节点的字段组合驱动，条件是多字段 AND / OR（三下拉编辑器
+ 一层嵌套即可表达，不需要高级模式）；DBA / API 兼容评审用 `filter` gateway 并联表达
（§6.1），与主链路选择互不干扰。

## 6. 三个必须补的缺口

这三个不是可选优化，缺任何一个都会在开发场景里踩坑。

### 6.1 `mode: filter` —— 附加门控

「改动碰了 DB → 加一个 DBA 评审，但主线继续走」。`if / elseif / else` 是 XOR，表达不了。

加一个 mode 开关：

- `switch`（默认）：有序求值，第一个命中的赢，else 兜底。
- `filter`：**所有命中的 case 端口都激活**，else 只在一个都没命中时走。

一个 radio 的成本，换回 inclusive gateway 语义（Windmill BranchAll、LangGraph router
返回 list 的既有语义）。

**join 语义**：下游节点默认**等所有被激活的入边**到齐；未被激活（skipped）的入边不计入
等待集合 —— 这是现有 skipped 传播逻辑
（[`workflow_graph_runtime.go:99`](../server/internal/handler/workflow_graph_runtime.go)）的自然延伸。
Argo 的教训（skipped 被当作 Succeeded 传播，#3378）说明这里必须显式定义。
`join: any` 是真实需求（竞速）但先不做，等场景出现再加。

**环路重入时的激活集合刷新（提案，实现前需验证）**：rework 把流程打回上游后，同一个
gateway 会重算路由，激活集合可能与上一轮不同（第一轮碰了 DB 激活 DBA 评审，第二轮
没碰就不该再激活）。规则：gateway 第 N 轮完成时以**本轮** `selected_targets` 为准 ——
上一轮激活、本轮未激活的下游子树：未开始的节点实例作废（skipped），已完成的保留记录
但不再计入 join 等待集合。需结合现有 rework 的 attempt 重置逻辑验证可行性，这是
filter × rework 组合的唯一空白区。

### 6.2 返工轮次上限

评审不过打回重做必须有上限，否则 Agent 和 reviewer 会互相无限打回。

> **本节原方案已被实现证伪并修正。** 原文写的是「case 边指回上游表达环路，
> 在边上加 `max_passes`」。实际上图有三处 `acyclic` 强制校验，**边不可能指回上游**；
> 返工是 rework 机制重建节点实例（attempt+1）实现的，不经过边。边级上限没有管辖对象。
>
> 真实缺口是 rework 本身完全没有上限。改为**活动级 `completion.max_attempts`**：
> 手动回滚超限返回 409 并说明原因；Critic 打回超限时把节点置为 `blocked` 而非返回
> 错误 —— 返回错误会被投递 verdict 的 daemon 重试，而「停下来交给人」才是目的。
> LangGraph 的对应物是图级 `recursion_limit`，活动级比它更精细。

**环路中的变量取值**：变量池默认取该节点**最新一次 valid submission** 的字段。
每轮 submission 本来就按 attempt 分行存储，历史轮次数据天然保留（对齐 Conductor
DO_WHILE 的按迭代号存档）。轮次限定语法（如 `fix.done@1`）**暂不做** —— 数据模型
已支持，等真实场景出现再加语法，避免提前复杂化。

**变量池是 submission 级整体替换，不做字段级 merge**：第二轮交付缺了某个非必填字段，
该字段在变量池中即为缺失（条件 fail-closed 落 else），不保留上一轮旧值。字段级 merge
会产生幽灵旧值 —— 第二轮改动根本没碰 DB，条件却按第一轮的 `touched_db=true` 路由。

### 6.3 节点级失败 / 超时出口 —— 不经过 Gateway

> **本节对危险性的判断已被实现证伪，保留原文并在此更正。**
>
> 原文断言：Agent 崩溃 → 字段不存在 → 条件 fail-closed → 静默落 else → 流程当正常继续，
> 并称这是「最危险的失效模式」。**这条不成立**：Agent 失败时节点没有 valid submission，
> 不满足完成条件，会停在 `active`/`blocked`；gateway 的入边始终不会 settled，根本不会被激活。
> 架构本身已经挡住了这条路径。
>
> 真正会导致静默落 else 的是另一条路径，实现期间发现并已修复：节点若未设
> `submission_schema`（**默认值**），必需 issue 完成后引擎会自动合成一个 valid
> submission，其 payload 不含节点声明的 outputs 字段 —— 节点因此 completed、变量池为空、
> 网关 fail-closed 落 else。触发它不需要 Agent 崩溃，默认配置即可。修法是：声明了必填
> 输出的节点不能被合成交付顶替，缺字段时挂起并逐个列出。
>
> 因此本节的 `on_failure` / `on_timeout` **不是防静默路由的手段**（架构已防），而是给
> 「已经停下来的节点」一个自动处置（通知 / 转人工 / 重试）。价值仍在，但优先级低于原文
> 判断。另注：`timeout_minutes` 已存在于现有实现，只产生 `node_timeout` 标记、不改流程。
>
> **实际落地的是三者中的「通知」一项**，且没有做成节点上的声明式出口：跑飞的节点转入
> `blocked` 并进收件箱。自动重试与 `goto` 失败分支未做 —— 见 §9 实施状态的说明。

解法：activity 节点声明失败 / 超时策略，**不经过 gateway**（采纳 Dify v0.14 的三策略框架，
去掉不适用的「默认值」—— 流程节点伪造一份成功交付比失败更危险）：

```yaml
node: triage
on_failure: { retry: 1, then: suspend, notify: [reporter] }
on_timeout: { after: 30m, then: suspend }
```

- `retry: n`：先原地重试 n 次（Dify 的 max_retry 对应物）。
- `then` 的取值：`suspend`（挂起等人，**默认**）/ `goto(node)`（失败分支）。
- 走 `goto` 失败分支时，向变量池注入该节点的 `error_type` / `error_message`
  （抄 Dify 的失败分支变量），下游可以基于错误类型再路由。

正常交付才进 gateway，异常走这些出口。画布上画在节点下方，红色虚线。

**超时拆成两段计时**（已定案）：单一计时器两头顾不好 —— 从 `active` 起算会误杀
「排队 30 分钟 + 执行 5 分钟」的正常任务，从开跑起算则「executor 一直没就绪」永远不超时。

- `on_timeout.after`：**只算执行时间**，从 Agent Task 真正开始运行起算。
- `on_stall.after`：**排队超时**，从节点进入 `active` 到 Agent Task 开跑为止；
  executor 解析失败、runtime 离线、任务排队都算 stall。默认 `then: suspend` 并通知。

```yaml
node: fix
on_timeout: { after: 2h,  then: suspend }     # 开跑后 2 小时没交付
on_stall:   { after: 15m, then: suspend }     # 15 分钟还没开跑
```

实现依据 daemon 的任务生命周期事件（任务 created → running 的时间戳已有记录），
不需要新增打点。`on_stall` **不新建就绪性判断**：executor 解析失败已有
`blocked / executor_needs_setup` 状态（`createWorkflowNodeActivationRecords`），
stall 计时器挂在这些既有状态上加超时动作，不做第二套状态机。

## 7. 与 Multica 产品架构的整合

本节把方案对齐到 [workflow-architecture-rfc §0.0](./workflow-architecture-rfc.md) 的
Run 一等对象模型。

### 7.1 变量池挂在 Run 上

运行时变量池 = 该 `workflow_instance` 下所有已完成节点最新 valid submission 的
`outputs` 聚合，带 `node_key` 溯源。与宿主 Issue 无关 —— 独立 Run（无宿主 Issue）
的变量池同样成立。

### 7.2 宿主 Issue 字段统一为 `issue.*` 命名空间

废弃 condition DSL 的 `host_issue` / `host_property` 两种 source，统一为变量引用：
`issue.status`、`issue.priority`、`issue.property.<key>`。

- `issue` 是保留命名空间，node key 禁用（加入 definition 校验，与现有 reserved-slug
  机制同思路）。
- **模板校验**：条件引用了 `issue.*` 的模板，以独立 Run 方式启动（无宿主 Issue）时，
  引用求值为「不存在」→ fail-closed 落 else。保存时对这类模板加警告标记
  「此模板包含依赖宿主 Issue 的条件」，启动独立 Run 时提示。

### 7.3 Reviewer verdict 统一进变量池；语义分类 = classifier 节点

废弃 `node_verdict` source。带 reviewer 的节点自动获得一组**系统内建输出字段**：
`verdict`（enum: approved / changes_requested）、`confidence`（number）、
`reason`（string），进变量池，条件写 `review.verdict == "approved"`。
用户声明的 outputs 与内建字段同名时保存报错。

语义路由（「判断这个 issue 属于哪类问题」这种没法归约成确定性字段的判断）不塞进
gateway，用一个**只产出 outputs 的 activity**（classifier）表达：executor 是 Agent，
outputs 是一个 enum 字段，下游接普通 gateway。这与 Dify Question Classifier /
Flowise Condition Agent 的「专职分类节点」结论一致（§4.2 ③），且在本模型里
**不需要新增节点类型** —— classifier 就是一个最小 activity。

### 7.4 两种活动形态共用同一注入口

`issue_policy=none` 的纯 Agent 活动（Node Task → Agent Task）与 Issue 型活动，
task context 都经 [`workflow_task_context.go`](../server/internal/handler/workflow_task_context.go)
组装。`WorkflowChoiceDuty` 换成 `WorkflowOutputsDuty`（schema 表格 + 提交命令），
两种形态自动一致。daemon 侧渲染在
[`workflow_context.go`](../server/internal/daemon/execenv/workflow_context.go)，同一处替换。

### 7.5 人工节点：schema 驱动表单

工作台交付面板（[`workflow-workbench.tsx` SubmissionPanel](../packages/views/workflows/workflow-workbench.tsx)）
从「选择分支」下拉改为按 outputs schema 自动生成表单：

| 类型 | 控件 |
| --- | --- |
| `bool` | Switch |
| `enum` | Select（显示 value + desc） |
| `number` | 数字输入 |
| `string` | 文本框（max_len 限长） |
| `string[]` | Tag 输入 |

人和 Agent 走同一套 schema、同一个提交端点、同一套校验 —— 没有第二条真相路径。

### 7.6 Enum 的 value 与 label

条件比较、校验、CLI 提交都只认 value（英文 key）。画布 case label、表单选项文案用中文。
Agent 提示里 value 和 desc 都给（表格两列），杜绝 Agent 交付中文 label 导致校验失败。

## 8. 数据模型

```
node definition (JSON)
  outputs:     [{ key, type, values?, required, desc, max_len? }]
  on_failure:  { retry?, then, notify? }
  on_timeout:  { after, then }               # 执行超时：从 Agent Task 开跑起算
  on_stall:    { after, then }               # 排队超时：从节点 active 到开跑
  cases:       [{ id, label, when }]     # gateway 专有
  mode:        switch | filter           # gateway 专有

edge
  from_case_id  string?     # gateway 出边绑 case，替代现有 condition / default
  max_passes    int?

workflow_node_submission
  outputs jsonb             # 替代 choice 列（migration 246 的 choice 列删除）

workspace 级 schema 库（§3.4，第 5 步）
  output_schema: { key, name, fields jsonb }
  # 编辑期复用来源；节点引用在模板保存时快照进 template version，无运行期依赖
```

`node.routed` 事件保留，payload 从 `{selected_target}` 改成
`{case_id, case_ids[], selected_targets[], matched{}, evidence{}}`（filter 模式下
`case_ids` / `selected_targets` 是数组）。下游判断入边是否被选中的逻辑
（[`workflow_graph_runtime.go`](../server/internal/handler/workflow_graph_runtime.go)）
从比对单个 target 改成检查是否在数组里。

`matched` 与 `evidence` 是决策的审计面：前者记每个求过值的 case 命中与否（`switch`
下没轮到的 case 不出现，与「求了值但没命中」区分），后者记每个条件读到的
`node.field → 值`，缺失的字段记为空。**这两项必须随决策一起落盘，不能事后从
submission 回读** —— submission 会被返工改写，回读得到的是「现在是什么」，而问题
问的是「当时是什么」。运行页的路由面板直接读这份 payload。

```json
{"case_id": "c2", "case_ids": ["c2"], "selected_targets": ["hotfix"],
 "matched": {"c1": false, "c2": true},
 "evidence": {"triage.is_bug": true, "triage.severity": "high"}}
```

**删除项：**

- `workflow_node_submission.choice` 列
- `ChoiceBranchesForNode`（[`condition.go:372`](../server/internal/workflow/condition.go)）
- `workflowChoiceDuty` / `WorkflowChoiceDuty`（[`workflow_task_context.go:395`](../server/internal/handler/workflow_task_context.go)）
- `branch-choice.ts` 全文件
- `renderWorkflowChoice`（[`workflow_context.go:241`](../server/internal/daemon/execenv/workflow_context.go)）
- 工作台交付表单的「选择分支」下拉（[`workflow-workbench.tsx:255`](../packages/views/workflows/workflow-workbench.tsx)）
- condition DSL 的 `node_choice` / `node_verdict` / `host_issue` / `host_property`
  四种 source（后两者换 `issue.*` 命名空间表达，§7.2；verdict 进变量池，§7.3）

## 9. 落地顺序

| 步 | 内容 | 做完能干什么 | 状态 |
| --- | --- | --- | --- |
| 1 | outputs schema 独立包（§3.4 代码层）+ `submission.outputs` + 服务端校验 + 结构化错误 | Agent 交付可校验，脱离自由文本 | ✅ |
| 2 | CLI `--set` / `--json` + task context 注入 schema（§7.4） | Agent 能真的交付字段（顺带解掉 `--choice` 断链） | ✅ |
| 3 | gateway cases + 端口 + 有序求值 + else 自动生成（仅 node outputs 一种 source） | **分诊 → 非 bug 直接 end 的场景通了（§5.1）** | ✅ |
| 4 | `issue.*` / verdict 统一进变量池（§7.2 / §7.3） | 条件可读宿主字段与评审结论，四种 source 归一 | ✅ |
| 5 | 条件编辑器（三下拉 + 类型收窄）+ 人工节点 schema 表单（§7.5）+ workspace schema 库（§3.4） | 编排者不写 JSON，人工节点同轨，字段跨模板复用 | 编辑器与表单 ✅；schema 库暂缓 |
| 6 | `on_failure` / `on_timeout` / `on_stall` + `error_type` / `error_message` 注入 | Agent 跑飞不再静默走错路 | 改为「停下来就通知」，见 §9 实施状态 |
| 7 | `mode: filter` + join 规则 + 重入刷新 + `max_passes` | 附加门控和返工环路（§5.3 全场景通） | ✅（`join: any` 未做） |

**1–3 是最小可用集。** 删除 choice 相关代码（§8）在第 3 步一起做；第 4 步拆出来单做，
因为它牵动 condition 全部 source、校验器和前端条件编辑器，不该和最小可用集捆在一起。

> 下面是按实现先后记的流水账，读到后面的条目会覆盖前面的中间态。
>
> **第 1–3 步已实现**，另提前带上了输出字段声明编辑器与
> 工作台的最小 schema 表单（原第 5 步的一部分）。实现与本文的偏差：
> ① outputs 值复用既有 `submission.payload` 列存储（该列自用户自定义字段废弃后闲置），
> 不新增 `outputs` 列，迁移 258 只删 `choice`；
> ② auto reviewer 获得 `node_submission` source，可读**已声明的** outputs 字段，
> 接替原 node_choice 自引用能力（§7.3 的 verdict 统一仍留在第 4 步）；
> ③ 条件编辑器此时仍是 when 表达式文本输入（三下拉编辑器随第 5 步落地，见下）。
>
> **测试环境验证（2026-08-05，`multica-test`）**：分诊 → 非缺陷直接结束这条
> 路径已端到端跑通，路由事件为 `{"case_id":"c1","selected_targets":["end_1"]}`，
> 兜底分支正确置 `skipped`。部署前核查确认 `submission.choice` 全库为空、生产库
> 尚无 workflow 表，故删列无数据损失（§10 两条行动项就此关闭）；测试库仅存一条
> 旧格式 gateway 模板，其模板页与所属已完成实例在新代码下无法解析，列表页不受影响。
>
> 验证中发现并修复的四处交互问题：交付被拒时前端丢弃了服务端的字段级明细、
> 保存校验错误使用内部 key 而非作者可见名称、输出字段以英文 key 作表单标签、
> 以及「新建工作流」因固定默认名触发 409 且失败被静默吞掉。
>
> **Agent 交付链路已实测通过**（§10 行动项 2 关闭）。真实 cloud runtime 上的
> agent 执行一个 `issue_policy=none` 的分诊节点，一次提交即通过校验：
> `{"is_bug": false, "category": "works_as_intended"}`，enum 值精确落在声明范围内，
> submission 只有 revision 1 且 status 为 `valid`，未触发重交。网关随即路由到
> `{"case_id":"c1","selected_targets":["end_ok"]}`。全过程 agent 的提示中不出现
> 任何下游节点名或分支概念 —— 它只报告领域事实，路由由编排层完成，这正是本设计
> 相对 `choice` 的核心差别，现已在真实 agent 上得到验证。
>
> **第 4–5 步已实现**：`issue.*` 与 reviewer verdict 统一进变量池（§7.2 / §7.3），
> 条件编辑器从 when 文本升级为三下拉（字段 / 运算符 / 值，运算符按字段类型收窄，
> §7.5）。不认识或有歧义的表达式回落文本模式，不阻塞编辑；`category` 这类省略
> 节点名的写法在展示时补全为它实际指向的 `triage.category`。
> **workspace schema 库未做** —— 跨模板复用字段的痛点尚未出现，等它出现再做。
>
> **第 6–7 步按 §6.3 的更正调整后实现**：`mode: filter`（附加门控，含下游 join 等待
> 全部激活分支）与返工上限（活动级 `completion.max_attempts`，见 §6.2 的更正）已实现
> 并通过集成测试。
>
> `on_failure` / `on_timeout` **没有按原设计做成节点出口**。§6.3 的更正已经说明它不是
> 防静默路由的手段（架构本身挡住了那条路径），剩下的价值是「已经停下来的节点要有人知道」。
> 实现落在这一点上：Agent 跑飞时 readiness 早已记录 `direct_execution_failed`，但节点
> 停在 `waiting` —— 那是「还在走」的状态，于是一个活已经停了的 run 看上去和所有进行中的
> run 一模一样，且没有任何人被通知。现在它进 `blocked`，并沿用 sweeper 已有的通知链进
> 收件箱，触达发起人、需求负责人、节点 owner 与工作区管理员，只报状态跨越的那一次，
> 不在每轮 reconcile 上重复打扰。自动重试与 `goto` 失败分支仍未做，`on_stall` 亦然。
>
> **路由决策留存求值依据**：`node.routed` 事件原先只写 `case_id` / `selected_targets`。
> 事后问「为什么走这条」只能回读上游 submission —— 那回答的是「现在是什么」，不是
> 「当时是什么」，中间一次返工就把已发生的决策解释改写了。事件现在同时写下每个求过值的
> case 是否命中（`matched`）与每个条件读到的字段值（`evidence`）。没命中的 case 一并记录
> ——「为什么没走那条」和「为什么走这条」是同一个问题的两半；`switch` 模式下没轮到求值的
> case 与「求了值但没命中」区分开；提交时缺失的字段记为显式空缺，而不是从依据里消失。
> 这两项**不追溯**：此前跑过的 gateway 事件里没有它们，面板对这类历史决策只能显示
> 走了哪条、没命中的一律标「未判断」、不显示判断依据。这是诚实的降级 —— 补一份事后
> 回读的依据，正是这条改动要杜绝的事。
>
> **运行页节点面板按类型分流**：面板此前对所有节点用同一套「活动」布局，于是一个已完成的
> Decision 会显示「需所有必需 issue 完成」和「该活动没有配置制品」—— 都是在说另一种节点，
> 而它自己做了什么反倒不在屏幕上。Gateway 现在默认打开**路由**页：每个 case 连同条件与
> 目标节点、命中的高亮、没命中的保留、`switch` 下没轮到的标「未判断」，下面列出判断依据。
> Gateway 未执行时同样渲染这张表，只标「还没执行」。同时：issue 列表与完成规则只留给
> activity，gateway 与控制节点只剩阻塞项和回滚控制；「节点事件」页原先渲染的是整个 run 的
> 事件流却挂在节点标题下，现已按节点过滤 —— gateway 自己的 `node.routed` 此前正是被埋在
> 那条全量流里。activity 的交付卡片补上了声明的输出字段值：它们是下游 gateway 的路由依据，
> 此前在分支上看得到、在产出它的节点上反而看不到。

## 10. 决策记录与行动项

原开放问题已全部定案，去向如下：

| 问题 | 决策 | 所在章节 |
| --- | --- | --- |
| 环路中变量取哪轮 | 取最新 valid submission；历史按 attempt 分行天然保留；轮次语法暂缓 | §6.2 |
| filter 模式下游 join | 等所有被激活入边；skipped 不计入；`join: any` 暂缓 | §6.1 |
| 未声明字段 | 宽松存 + 警告，不进变量池 | §3.2 |
| enum label / value | 校验与条件只认 value；提示里 value + desc 都给 | §7.6 |
| 宿主 Issue 字段 | 统一 `issue.*` 保留命名空间；独立 Run 引用时保存警告 | §7.2 |
| 人工节点表单 | schema 驱动表单，并入落地第 4 步 | §7.5 |
| 超时计时起点 | 拆两段：`on_timeout` 只算执行，`on_stall` 算排队 | §6.3 |
| choice 迁移 | 直接删除，不做兼容层（产品未正式发布，符合仓库「不加兼容层」规则） | §8 |
| 高级表达式边界 | 严格等于 AST 能力，不做算术 / 函数 / 脚本；复杂判断下沉 classifier | §3.3 |
| summary-only 提交 | 有 outputs 声明的节点拒绝，返回带 schema 的校验错误 | §3.2 |
| 变量池更新语义 | submission 级整体替换，不做字段级 merge | §6.2 |
| 环路 × filter 重入 | 以本轮 selected_targets 为准，旧激活子树作废（**提案，待验证**） | §6.1 |
| `on_stall` 挂点 | 复用 `blocked / executor_needs_setup` 状态机加计时器，不做第二套 | §6.3 |
| schema 库版本语义 | 编辑期引用、模板保存时快照，无运行期依赖 | §3.4 |

**实施前行动项（两项均已关闭，见 §9 实施状态）：**

1. ~~查生产库是否存在包含 gateway 的模板与 `choice` 非空的行~~ —— 已核查：`choice`
   全库为空，生产库尚无 workflow 表，删列无数据损失。
2. ~~实测 Agent 交付契约~~ —— 真实 cloud runtime agent 一次提交即通过校验，
   提示中不出现任何下游节点名。

**已知未做（不是遗漏，是判断）：**

| 项 | 为什么不做 |
| --- | --- |
| workspace schema 库（§3.4） | 跨模板复用字段的痛点尚未出现；先做等于凭空多一层版本语义 |
| `on_failure` 自动重试 / `goto` 失败分支、`on_stall` | 停下来的节点已经会通知到人（§9）。自动跳转的风险大于收益：跳错分支比停着更难发现 |
| `join: any`（§6.1） | 现有 join 语义（等所有被激活入边）够用，没有场景要求先到先得 |
| 环路 × filter 重入刷新 | §10 里仍标「提案，待验证」；缺少能触发它的真实模板，验证不了就不实现 |

## 11. 参考

**画布型工作流**

- [n8n — Switch 节点](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.switch)（多 rule 多端口、Fallback Output 三选一）
- [Windmill — Branches](https://www.windmill.dev/docs/flows/flow_branches)（BranchOne 有序谓词 + default / BranchAll 全并行）
- [Dify — If-Else 节点](https://docs.dify.ai/en/use-dify/nodes/ifelse)（IF/ELIF/ELSE 有序求值、按类型收窄的运算符）
- [Dify — Question Classifier](https://legacy-docs.dify.ai/guides/workflow/node/question-classifier)（专职语义分类节点，category_id 绑边）
- [Dify — Error Handling](https://dify.ai/blog/boost-ai-workflow-resilience-with-error-handling)（节点级三策略、失败分支注入 error_type/error_message、retry 参数）
- [Flowise — Agentflow V2](https://docs.flowiseai.com/using-flowise/agentflowv2)（Condition 与 Condition Agent 分离）
- [Langflow — If-Else 组件](https://docs.langflow.org/if-else)（轻量条件路由、true/false 输出端口）

**编排引擎**

- [Conductor — Do-While](https://conductor.netflix.com/documentation/configuration/workflowdef/operators/do-while-task.html)（每轮输出按迭代号索引存档）
- [Argo Workflows — skipped 传播 issue #3378](https://github.com/argoproj/argo/issues/3378)（无强制兜底导致的状态传播坑）
- [Camunda 8 — Gateways](https://docs.camunda.io/docs/components/modeler/bpmn/gateways/)（XOR / OR / AND / event-based 四类语义）
- [Jmix — Sequence Flows](https://docs.jmix.io/jmix/bpm/bpmn/bpmn-sequence-flow.html)（不推荐把条件挂在 activity 出边）

**Agent 框架**

- [LangGraph — Graph API](https://docs.langchain.com/oss/python/langgraph/graph-api)（conditional edges、router 返回 list、`Command` 的静态可达集合标注）
- [CrewAI — Flows](https://docs.crewai.com/en/concepts/flows)（`@router` 标签路由）

**内部**

- [workflow-architecture-rfc.md](./workflow-architecture-rfc.md)（Run 一等对象、issue_policy、Node Task / Agent Task）
- [workflow-node-model-subtraction.md](./workflow-node-model-subtraction.md)

## 附录 A. 被否方案存档

**A. 取消 gateway，把出口挂在 activity 节点上（outcome 模型）**

即节点声明 `outcomes: [{when, then: end/goto/rework/gate}]`，无 gateway 节点。

不选的理由：把五种流程动作塞进节点，概念重且是新发明的词，用户没有现成心智。
Gateway + case 只需要「变量」「条件」两个已知概念。职责也更浑浊 —— activity 同时管产出和路由。

**B. Tekton / GitHub Actions 式的 `when` 表达式挂在下游节点入口**

不选的理由：那是 YAML 世界的模型，没有画布。BPMN 规范明确不推荐把条件挂在 activity 出边上，
理由就是图会变得不可读、有歧义（Jmix）。Multica 有画布，条件应该是画布上的一等视觉元素
（端口 + label），而不是藏在节点属性里。

另外 `when` 模型下没有天然的 else —— 用反向条件模拟兜底，遇上 fail-closed 求值会两条
都不成立，流程静默卡死。Argo 的 skipped 传播 issue 群（#3378、#8654、#13498）是这个
模型长期成本的实证。

**C. 保留 choice，只补 CLI flag**

不选的理由：§1.2 的问题①②④与 flag 无关，补 flag 只解决③。
