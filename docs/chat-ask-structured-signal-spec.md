# Spec: 智能体聊天确认交互的结构化信号（`multica chat ask`）

- Status: Draft
- Date: 2026-07-08
- Owner: TBD
- Related: 飞书确认卡片误判修复（confirmation_action.go 信息征询守卫）、「给过期/越权的飞书确认卡点击加反馈」后续任务

## 1. Problem Statement

智能体在聊天（飞书/Slack/Web）里需要用户介入时，只能回传一段自然语言文本；飞书渠道用关键词启发式（"请确认"/"确认后"等）从文本反推"这是不是确认请求"，猜中才渲染确认/取消按钮。启发式把**信息征询**（"请提供完整域名，确认后我再查询"）误判为**审批决策**，渲染出无法推进任务的死按钮（线上已发生，OPS-18 场景）；即使猜对，按钮语义也只能靠再解析文本获得。

不解决的代价：每一次误判都是一次死胡同交互（用户点了确认，智能体收到的却不是它等的东西）；启发式规则会随智能体措辞多样化持续膨胀、持续误判；Slack/Web 渠道想复用确认交互时只能各自再猜一遍。

业界调研结论（2026-07 完成，覆盖 Slack/Teams/飞书/Telegram/Discord 平台原语、MCP elicitation、Claude AskUserQuestion、OpenAI needsApproval、LangGraph interrupt、Linear Agent Interaction SDK、Devin/Cursor/Coze/Dify 实践、Hermes 格式与 Hermes Agent）：**没有任何产品从回复文本推断按钮**；标准模式是智能体发出类型化信号（区分"审批决策"与"信息征询"两个原语），由平台渲染 UI。

## 2. Goals

1. 确认/取消按钮 100% 来自智能体的显式声明，渠道侧零文本猜测——误判类缺陷在结构上不再可能。
2. 区分三种交互类型（confirm / choice / input），"需要用户补充信息"的场景不再出现按钮。
3. 信号一次定义，三渠道复用：飞书先行，Web 与 Slack 使用同一服务端数据渲染各自 UI。
4. 用户点击后的回应作为结构化答案回到同一会话，智能体 resume 后拿到的就是它等的东西。
5. 启发式按计划退役，`confirmation_action.go` 的猜测代码最终删除。

## 3. Non-Goals

- **不做阻塞式 CLI 等待**（agent 进程挂起长轮询等待答案，类似 Claude Code AskUserQuestion 的宿主回调）。现有"结束本轮 → 用户回复触发会话 resume"的任务模型已满足需要，阻塞式引入 daemon 超时/进程管理复杂度，列为 P2。
- **不做高危动作元数据强制确认**（Salesforce `isConfirmationRequired` / OpenAI `x-openai-isConsequential` 模式，在技能/工具定义上强制拦截）。与本方案互补而非竞争，需要技能系统配合，单独立项。
- **不改 issue 收件箱确认流**（`multica.issue.confirmation`）。它已是结构化的（评论内容 + 卡片 value），只在渲染层与本方案共享回执卡代码。
- **不做回复文本内嵌标记通道**（Hermes 式 `<user_confirm>` 标签）。调研结论是 CLI 动词可靠性更高；内嵌标记仅当出现"无法执行 CLI 的 runtime"时再评估。
- **不覆盖 mobile**。移动端只消费 Web 同款数据，不在本期验收内。

## 4. User Stories

- 作为**在飞书里使唤运维智能体的用户**，当智能体准备好一个待执行动作（触发流水线）时，我看到一张带确认/取消按钮的卡片，点确认后动作执行、卡片变为已确认回执，这样我不需要打字也能安全授权。
- 作为**提供了不完整输入的用户**（如 "mulitica域名ip是"），当智能体缺少必要参数时，我收到一条**普通文本**提问（"请提供完整域名，例如 example.com"），直接打字回答即可——不会看到任何点了也没用的按钮。
- 作为**被智能体给出多个可选项的用户**（如"检测到 3 条流水线，选哪条？"），我看到选项按钮，点选即回答，不需要照抄选项名。
- 作为**智能体**（提示词视角），我在需要授权时执行一条 CLI 命令声明确认请求，而不需要琢磨措辞来"触发"平台出按钮。
- 作为**发起任务的用户以外的群成员**，我点击确认卡片按钮无效（保持现状的 AllowedOpenID 门禁），这样别人不能替我授权。
- 作为**没有及时处理确认的用户**，超过 TTL 后卡片显示已过期，我知道需要重新发起，而不是点一个永远没反应的按钮。

## 5. Requirements

### P0 — Must Have

**R1. CLI 契约：`multica chat ask`**

```
multica chat ask --type confirm --message <text> --action <text>
multica chat ask --type choice  --message <text> --option <label> [--option <label> ...]
multica chat ask --type input   --message <text> [--hint <text>]
```

- `--type confirm`：**强制要求 `--action`**——批准后将执行的动作的完整描述（如 "触发流水线 私服更新重启-main"）。填不出 action 就无法发起 confirm，这是防止智能体在缺信息时误用 confirm 的结构性约束（设计依据：MCP elicitation 的受限 schema、调研关键发现 #1）。
- `--type choice`：2–6 个 `--option`，超限报错。
- `--type input`：仅声明"我在等自由文本输入"；渠道**不渲染按钮**。
- 命令只在 chat 任务上下文内可用（同 `chat history`/`chat thread`），使用 daemon 任务的 `mat_` token 鉴权，fail-closed，不回退用户 PAT（沿用既有 CLI 鉴权边界）。
- 服务端校验后立即通过渠道桥投递（不等 ChatDone）；stdout 返回 ask id 与投递结果 JSON。
- 每个 chat session 同时至多一个 pending ask：新 ask 自动将旧 ask 置为 `superseded`（旧卡片更新为已失效态）。

**R2. 服务端数据模型**

- 新增 chat ask 记录（session 内）：`id, session_id, task_id, type, message, action, options[], status(pending|answered|expired|superseded), answer, answered_by, created_at, expires_at`。
- TTL 默认 30 分钟（与现有确认卡一致）；到期由状态查询惰性判定或后台批量置为 `expired`，多副本安全（状态迁移用条件 UPDATE，幂等）。
- 回答写入是幂等的：重复点击/重复回调只生效一次（唯一约束 + 条件更新）。

**R3. 渠道渲染规则（飞书先行）**

| type | 飞书渲染 | 点击/回答后 |
|---|---|---|
| confirm | 卡片：message + action 摘要 + 确认/取消按钮（value 带 ask id） | 卡片替换为已确认（绿）/已取消回执卡，按钮消失（复用本次修复引入的 resolved-card ACK 机制） |
| choice | 卡片：message + 选项按钮 | 卡片替换为回执卡，显示所选项 |
| input | **普通文本消息**（markdown），可附 hint | 无卡片状态，用户直接打字 |

- 按钮 value 只带 `ask_id + action + allowed_open_id + 时间戳`，不再内嵌截断内容（回执卡内容从服务端 ask 记录取，解决 value 尺寸与截断问题）。
- 点击门禁沿用现状：仅任务发起人（AllowedOpenID）可操作。
- 按钮标签与回执文案**按渠道语言适配**（飞书中文，Slack 英文；文案 key 定义在服务端，渠道桥选择语言）。
- 发卡时持久化渠道 message_id 到 ask 记录：`superseded` 与 `expired` 状态**主动 PATCH** 旧卡为失效回执态（尽力而为）；点击时的状态检查作为正确性兜底（PATCH 失败或时序窗口内的点击返回"已失效/已过期"提示，衔接已挂的"过期点击反馈"任务）。
- choice 卡片**不设默认"以上都不是"逃逸按钮**；卡片 footer 固定提示"选项不合适可直接回复"。选项无法收敛的场景应使用 `input` 而非 `choice`。

**R4. 回应模型**

- 三态语义对齐 MCP elicitation：确认点击 = `accept`（confirm 附 action；choice 附所选项），取消点击 = `decline`，TTL 过期/被取代 = `cancel` 类终态。
- 答案作为用户消息进入同一 chat session（现有 resume 机制），消息体为规范化文本（如 "确认：触发流水线 私服更新重启-main" / 所选项 label），同时 ask 记录持有结构化 answer 供审计。
- 用户不点按钮而直接打字回答同样有效（对齐 Bot Framework ConfirmPrompt 接受打字 yes 的行业惯例）：**不做内容匹配**——只要存在 pending ask，发起人在该会话的下一条文本消息即终结该 ask（`answered(by_text)`），卡片 PATCH 为"已以文字回复"回执；文本照常进入会话由智能体解读，若仍需授权则重新发起 ask。语义上 pending ask 是一次性 nonce：任何后续发言都使旧授权作废，杜绝"对话语境已漂移但过时的确认按钮仍可执行"的风险。

**R5. 提示词契约与技能文档**

- `buildChatPrompt`（server/internal/daemon/prompt.go）新增 ask 使用契约：何时用 confirm（动作完备只差授权）/ choice / input（缺参数），并明确"不要用文本措辞诱导按钮"。
- 契约注入双重门控：会话渠道有渲染器（今天仅飞书）**且** daemon 在 claim 请求体自报 `supports_chat_ask` 能力（旧 CLI 不带 → 不教命令，避免教旧客户端执行不存在的子命令；旧 server 忽略该字段）。CLI 随 desktop 发版打包 + daemon auto-update 分发。
- 同 PR 更新内置技能 `SKILL.md` 及 `references/*-source-map.md`（仓库规则：CLI 变更必须同步内置技能；当前无技能覆盖 `multica chat`，随首个覆盖它的技能一并补齐）。

**R6. 启发式退役计划**

1. ✅（PR-3 已实施）**chat 路径移除 prompt-cue 层**：`chatConfirmationReplyMessage` 只保留显式层（quoted 回复"确认X"、独立"确认X"行）。cue 层及其信息征询守卫**为 issue 收件箱确认流保留**（`confirmationReplyMessage` 三层不变）——该流在 Non-Goals 中明确不改，且尚无结构化替代。
2. 观察一个版本周期（老智能体会话/未升级 prompt 的存量任务仍可能用旧措辞），确认 ask 采用率达标后，**删除 chat 路径的 quoted/standalone 显式层**，`chatReplyNeedsConfirmationAction` 整体退役。issue 收件箱流的启发式退役依赖 ask 扩展到 issue 场景，单独立项。
3. 保留并复用的部分：resolved-card 回执渲染、AllowedOpenID 门禁、卡片 ACK 通道。

**验收标准（P0 摘要）**

- [ ] 智能体执行 `chat ask --type confirm` 后，飞书 3 秒内出现确认卡；点击确认 → 卡片变绿色回执且按钮消失 → 智能体下一轮收到规范化答案。
- [ ] `--type confirm` 缺 `--action` 时 CLI 报错并给出用法提示。
- [ ] `--type input` 场景（域名补全用例原文回归）全程无按钮。
- [ ] 非发起人点击无效；过期后点击得到过期提示。
- [ ] 同 session 发第二个 ask 后，第一张卡自动变为失效态。
- [ ] 重复点击/回调重放只产生一条答案（幂等测试）。
- [ ] 恶意/畸形 ask 参数（超长 message、7 个 option、空 action）全部 400，不产生半成品卡片。

### P1 — Nice to Have

- Web 渠道渲染：chat 会话流内显示 confirm/choice 组件（同一服务端数据）。
- Slack 渠道渲染：Block Kit buttons（对齐 Slack agent 设计指南"只对高影响动作出确认"）。
- 回执卡附"在 Multica 中查看"链接（Claude Code in Slack 的逃逸模式）。
- `--timeout` 覆盖默认 TTL；ask 记录进入任务审计时间线。

### P2 — Future Considerations

- 阻塞式 `ask --wait`（CLI 长轮询直接返回答案），供单轮内需多次征询的场景。
- 技能/工具级 `requires_confirmation` 元数据强制拦截（高危动作不依赖智能体自觉）。
- input 类型的 schema 化字段（对齐 MCP elicitation requestedSchema），Web 端渲染表单。
- 设计数据模型时预留：`answer` 用 JSONB、`type` 可扩展，避免为上述项返工。

## 6. Success Metrics

- **误判死按钮缺陷数**：结构化信号覆盖的会话中为 0（上线后按 channel_inbound 审计与用户反馈统计）。
- **ask 采用率**（领先指标）：上线 30 天内，带确认语义的智能体回复中 ≥80% 经由 `chat ask` 而非文本措辞（对比 ChatDone 内容命中旧启发式的比例）。
- **确认交互完成率**：pending → answered 比例 ≥90%（expired 比例可衡量 TTL 是否合理）。
- **启发式代码退役**（滞后指标）：两个版本周期内 `chatReplyNeedsConfirmationAction` 及其测试全部删除。

## 7. Resolved Questions（2026-07-08 评审定稿）

- **渠道语言适配**：需要。按钮标签与回执文案按渠道语言渲染，落入 R3。
- **superseded/expired 旧卡处理**：主动 PATCH 为失效态（尽力而为）+ 点击时状态检查兜底，两者并存；message_id 随 ask 记录持久化。落入 R3。
- **文本抢答判定**：不做内容匹配。pending ask 语义为一次性 nonce，发起人的下一条消息无论内容一律终结它；副作用（反问也终结 ask）由智能体重新 ask 消化。落入 R4。
- **choice 逃逸项**：不设默认"以上都不是"按钮，footer 提示可直接打字回复（打字即 Q3 规则的天然逃逸通道）。落入 R3。

## 8. Timeline / Phasing

无硬截止。建议三个 PR 切分，每个可独立合入：

1. **PR-1 服务端 + CLI**：ask 数据模型、REST 端点、`multica chat ask`、幂等与门禁测试（Go 质量门：先写失败测试）。
2. **PR-2 飞书渲染 + 回应闭环**：卡片渲染、点击→answer→session resume、回执/过期/取代状态；复用并收编现有 resolved-card 代码。
3. **PR-3 提示词 + 退役第一步**：buildChatPrompt 契约、内置技能文档、删除 prompt-cue 启发层。

依赖：无外部团队。与已挂的"过期/越权点击反馈"任务在 PR-2 汇合。
