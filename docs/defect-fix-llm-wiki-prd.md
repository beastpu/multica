# Defect Fix LLM Wiki PRD

> Status: Draft
> Owner: TBD
> Last updated: 2026-06-23

## TL;DR

- **目标**: 建一个私有 LLM wiki,让 bugfix agent 修新 issue 前能先查询过往正确修复经验,降低重复踩坑和架构性误修。
- **理论基础**: 参考 Karpathy 的 LLM Wiki 模式: GitHub PR / issue / code 是只读事实源,wiki 是 LLM 维护的结构化知识层。
- **MVP 只验证最小闭环**: scanner 生成 source record + candidate 骨架 + 人工/LLM 辅助审核 10 个 case + `multica-defect-fix` 修复前查询 + 跑通 3 个真实 issue。
- **这一版不做规模化系统**: 30-50 个案例、100 条 source record、symbol 索引、多语言 parser、自动 drift 检测、信用分、自动发布 case、产品 UI、公开引用全部不进入本版验收。
- **核心原则**: 案例库是内部辅助参考。agent 只在内部工作记录引用案例,不在 PR 描述、GitHub issue comment 或 Multica issue comment 暴露私有 case id / wiki 内容。

---

## 1. 背景

`multica-defect-fix` skill 已经能约束缺陷修复流程,但它主要回答"修复时该遵守什么流程",还不能稳定回答"历史上类似问题是怎么正确修的"。

现状风险:

- Agent 每次接 bug 都重新读 issue、搜代码、猜根因,历史经验没有复利。
- Reviewer 重复指出相同架构坑,例如 React Query / Zustand 边界、workspace scope、desktop routing、daemon/runtime 流程。
- 历史 PR 虽然在 GitHub 里,但以 diff、review、comment 的碎片形态存在,agent 难以稳定复用。
- 文档和代码会脱节:文档说的文件、函数、测试可能已改名、删除或重构。

目标不是做一个"PR 摘要列表",而是先验证一条轻量闭环:从 GitHub 已合并 PR 抽取可靠案例,在新 issue 修复前被 agent 用上,并观察是否真的提升修复质量。

本版必须守住一个边界:只证明"历史案例能被沉淀、检索并辅助修复",不证明"案例库已经规模化"、"召回质量已经稳定"或"文档能自动保持最新"。

## 2. 参考原则

### 2.1 Karpathy LLM Wiki 模式

吸收点:

- **Raw sources immutable**: GitHub PR、issue、review、代码提交是事实源,只读不改。
- **Wiki as compiled layer**: wiki 是由 LLM 从 sources 编译出的结构化知识层,负责总结、互链、冲突标注和持续更新。
- **Agent-owned maintenance**: wiki 主要由专职 wiki-librarian 维护,普通 bugfix agent 默认只读。
- **Query / ingest / lint 分离**: 查询、纳入、体检是不同工作流,不能混在普通修复流程里随手改文档。

### 2.2 代码上下文工具的启发

可借鉴方向:

- **Aider repo map**: 用紧凑 repo map 给 LLM 足够上下文,避免直接塞整个代码库。
- **Repomix / Gitingest**: 用 LLM-friendly 格式打包代码和元信息,并控制 include / exclude / token budget。
- **DeepWiki / OpenDeepWiki**: 从 Git 仓库自动生成结构化文档和问答索引。

MVP 不直接引入这些系统,只借鉴两个原则:结构化输出、双层兜底。先查 wiki,缺关键事实再回 source PR / issue / 当前代码。

### 2.3 内部 Feishu Wiki 参考

辅助参考文档: [llm-wiki: AI自动维护更新知识库](https://lilithgames.feishu.cn/wiki/EJfVwzSdZi4NW9kA1iCcf0VSndh?fromScene=spaceOverview)

吸收点:

- 三层分工: sources 只读,wiki 由资料管理员写,规则由人维护。
- 页面分类: `pattern`、`antipattern`、`concept`、`entity`、`playbook`、`synthesis`、`automation`。
- 双层查询: 先查 wiki,缺关键事实再回到 source PR / source code。
- ID 约束: 稳定 id 比标题和文件名更适合作为跨页引用键。

## 3. 产品目标

### 3.1 要解决的问题

- Agent 修新 issue 前能先查历史 defect pattern,形成更靠谱的根因假设。
- 历史 PR 的经验被抽象成可复用 pattern / antipattern / playbook。
- 案例与 source PR、linked issue、关键文件建立可追溯关系。
- 案例库保持私有,不污染公开 PR 和用户可见评论。

### 3.2 本版不解决的问题

- 不自动判定所有 closed PR 都是有效案例。
- 不自动判定文档是否随代码重构而过期。
- 不做 symbol 级代码图谱、多语言 parser 或 `symbols.index.jsonl`。
- 不做信用分、validated 记账或长期排序系统。
- 不把案例库做成 Multica 产品 UI。
- 不把 30-50 个案例、100 条 source record、5 个真实 issue 作为本版目标。

### 3.3 本版收敛口径

为避免 MVP 自相矛盾,以下口径是本版唯一验收边界:

- Source record: 只要求证明 scanner 能稳定产出一批事实记录。验收使用"至少 20 条"作为冒烟门槛,不是案例库规模目标。
- Official case: 只要求人工审核发布 10 个 `medium` 以上 case。30-50 个案例属于后续扩容,不进入本版。
- Real issue trial: 只要求 3 个真实 defect issue 跑通 query-before-fix,记录 hit / no hit,不要求证明质量指标显著改善。
- Parser: 只用 `rg`、path、content hash 和 frontmatter parser。Go parser、ts-morph、tree-sitter、SQL parser 都不在本版做。
- Ranking: 只用模块匹配、路径匹配、文本相似、status、confidence。信用分和 read / applied / validated 记账不在本版做。
- Drift: 只做 schema / link / source / secret lint。代码 drift 由使用者和 wiki-librarian 人工发现,自动 drift 不在本版做。
- Candidate: scanner 只能给事实骨架和硬筛选结果。根因、正确修法、不要照搬等高价值正文必须由 LLM 辅助生成并经 wiki-librarian 审核,不能视为全自动产物。

## 4. 核心用户与权限

| 角色 | 权限 | 说明 |
| --- | --- | --- |
| Bugfix agent | 读 wiki,内部引用 case,生成候选草稿 | 修 issue 时使用案例,但不直接改正式库 |
| Wiki librarian | 审核候选,写正式 wiki,重建索引,跑 lint | 唯一正式写入角色 |
| Maintainer | 改规则,处理争议,决定高风险 case 是否入库 | 人类拥有规则层最终解释权 |
| Scanner job | 读 GitHub PR / issue / diff,生成 source record | 自动任务,不发布正式案例 |

写入纪律:

- `wiki/` 正式页只能由 wiki-librarian 写。
- `sources/` 和 `candidates/` 可以由 scanner 生成。
- `AGENTS.md`、schema、ingestion rules 只能由人 review 后改。

## 5. 信息架构

MVP 存储结构:

```text
defect-fix-wiki/
├─ AGENTS.md
├─ schema/
│  ├─ case.schema.json
│  └─ source.schema.json
├─ sources/
│  ├─ prs/
│  │  └─ pr-4462.yaml
│  └─ issues/
│     └─ mul-3570.yaml
├─ candidates/
│  └─ pr-4462.candidate.md
├─ wiki/
│  ├─ patterns/
│  ├─ antipatterns/
│  ├─ concepts/
│  ├─ playbooks/
│  └─ synthesis/
├─ indexes/
│  ├─ cases.index.jsonl
│  └─ tags.index.json
├─ reports/
│  ├─ lint-report.json
│  └─ ingestion-report.json
└─ wiki-log.jsonl
```

明确不在 MVP 建:

- `symbols.index.jsonl`
- `credit.index.json`
- `code-map/`
- `drift-report.json`

页面分类:

| Kind | 用途 |
| --- | --- |
| `pattern` | 已发生 defect 的触发条件、形成机制、正确修法 |
| `antipattern` | 明确不要做的写法、替代 API、实证来源 |
| `concept` | 架构原则,例如 server state vs client state |
| `playbook` | 可执行排查或验证步骤 |
| `synthesis` | 跨多个案例总结出的高阶规律 |

`entity` 和 `automation` 后续再加。MVP 先不建全量实体库,避免范围膨胀。

## 6. 案例格式

正式案例是 Markdown 文件,关键字段必须放进 YAML frontmatter,保证程序可快速解析。

```yaml
---
id: pattern.runtime.antigravity_hidden_print_timeout
kind: pattern
title: "Antigravity turns die at hidden print-timeout"
status: active
confidence: medium
visibility: private

repo: multica-ai/multica
source_pr: "https://github.com/multica-ai/multica/pull/4462"
source_pr_number: 4462
source_issue_refs:
  - "MUL-3570"
merged_at: "2026-06-23T10:57:51Z"
merge_commit: "<sha>"
first_ingested_at: "2026-06-23"
last_reviewed_at: "2026-06-23"

modules:
  - server
  - daemon
signals:
  - "Antigravity task dies around 5 minutes"
code_refs:
  - repo: multica-ai/multica
    path: server/internal/daemon/...
    line_hint: 120
    ref_kind: root_cause
    verified_commit: "<sha>"
    content_hash: "<hash>"
fix_pattern: "Detect the runtime's hidden print-timeout and keep output flowing or change execution strategy."
verification:
  - "manual runtime smoke test"
gotchas:
  - "Do not treat the absence of a server timeout as proof there is no runtime timeout."
related:
  - concept.runtime.task_lifecycle
tags:
  - runtime
  - daemon
  - timeout
---
```

正文模板:

```md
## 症状

## 根因

## 正确修复模式

## 为什么这个修法正确

## 验证方式

## 适用边界

## 不要照搬
```

规则:

- `source_pr`、`merge_commit`、`code_refs.path`、`code_refs.content_hash` 是 MVP 必填。
- `line_hint` 只是人类跳转提示,不能作为唯一锚点。
- MVP 不要求 `symbol`、`span_hash`、`blob_sha`。
- 跨页引用用稳定 id,例如 `[[concept.runtime.task_lifecycle]]`。

## 7. 自动扫描 closed PR

### 7.1 数据源

默认扫描官方仓库:

```text
https://github.com/multica-ai/multica
```

扫描对象:

- GitHub closed PR,包括 merged 和 closed-unmerged。
- 只有 `mergedAt != null` 的 PR 能成为成功修复候选。
- closed-unmerged PR 暂不入库,只保留 source record 作为后续反例研究材料。

### 7.2 候选筛选

MVP 使用硬标准,避免关键词噪声。这里的 `candidate` 只表示"值得人工审核",不表示系统已经自动判定它是 defect case。

默认候选必须同时满足:

1. PR 已合并。
2. PR 关联一个明确 issue,例如 GitHub issue、`MUL-xxxx`、或 PR body 中的 close/fix/resolve 引用。
3. Diff 包含测试文件变更,例如 `*.test.ts`、`*.test.tsx`、`*_test.go`、`e2e/*.spec.ts`。

弱信号只用于排序和人工查看顺序,不能单独入选:

- title / branch / body 包含 `fix`、`bug`、`regression`、`crash`。
- review / PR body 中出现 root cause、repro、timeout、permission、stale 等关键词。

人工可 override:

- 对于无法写自动测试但有明确复现和人工验证的 PR,wiki-librarian 可以手动标为候选,但必须在 candidate 中写明原因。

排除:

- docs-only、changelog-only、release-only。
- 纯 feature / refactor。
- 未合并 PR。
- revert PR 不作为成功修复 case。

### 7.3 Source Record

每个 PR 先生成 source record:

```yaml
id: source.github.pr.4462
repo: multica-ai/multica
pr_number: 4462
url: "https://github.com/multica-ai/multica/pull/4462"
state: merged
title: "MUL-3570: fix(agent): stop Antigravity turns dying at agy's hidden 5m print-timeout"
author: "Bohan-J"
base_ref: main
head_ref: agent/j/b48829ab
merged_at: "2026-06-23T10:57:51Z"
merge_commit: "<sha>"
linked_issues:
  - "MUL-3570"
changed_files:
  - path: "server/internal/daemon/..."
    additions: 10
    deletions: 2
test_files_changed:
  - path: "server/internal/daemon/..._test.go"
classification:
  candidate: true
  reasons:
    - "merged"
    - "linked issue detected"
    - "test file changed"
```

### 7.4 候选 Case 草稿

Scanner 只负责收集事实和生成骨架。高价值正文由 LLM 辅助 + wiki-librarian 审核完成,本版不要求 scanner 自动写出可发布正文。

流程:

1. Scanner 拉取 PR metadata、linked issue、changed files、test files、PR body、关键 review comments。
2. Scanner 生成 `candidates/pr-xxxx.candidate.md` 骨架,标注 source、相关文件、测试文件、候选原因和 `needs_review`。
3. Wiki-librarian 使用固定 prompt 调用 LLM 或人工补齐正文:
   - 根因是否明确。
   - 修复模式是否可复用。
   - 验证方式是否真实。
   - 哪些部分不要照搬。
4. 审核通过后移动到 `wiki/patterns/` 或其他正式目录。

Draft prompt 需强制回答:

```text
From the source PR and linked issue, extract:
1. user-visible symptom
2. confirmed root cause
3. reusable fix pattern
4. why this fix is correct in Multica architecture
5. verification evidence
6. gotchas and non-applicable details

If root cause or verification is unclear, mark the candidate as needs_review.
Do not invent facts. Keep source URLs and file refs.
```

这是系统价值的核心步骤,不能只生成 frontmatter。没有完成 LLM / 人工审核的 candidate 不能计入 10 个正式 case。

## 8. 代码关联与时效性

MVP 承认代码时效性问题,但不做自动 drift 系统。

MVP 只保存:

- `path`
- `line_hint`
- `verified_commit`
- `content_hash`

使用规则:

- Agent 使用案例时必须重新阅读当前代码,不能只相信 case。
- 如果 `path` 不存在或附近内容明显不匹配,agent 应将 case 标为"仅弱参考"。
- Wiki-librarian 人工审核时可以更新 `last_reviewed_at` 或废弃 case。

自动 drift 检测、symbol 追踪、span hash、文件移动追踪全部后移到后续独立 PRD。

## 9. 快速解析格式

MVP 只建一个机器可读索引: `indexes/cases.index.jsonl`。

每行一个 case:

```json
{"id":"pattern.runtime.antigravity_hidden_print_timeout","kind":"pattern","status":"active","confidence":"medium","repo":"multica-ai/multica","modules":["server","daemon"],"tags":["runtime","timeout"],"signals":["Antigravity task dies around 5 minutes"],"files":["server/internal/daemon/..."],"source_pr_number":4462,"last_reviewed_at":"2026-06-23"}
```

召回排序 MVP 只用:

- 模块匹配。
- 文件路径匹配。
- 文本相似度。
- `confidence`。
- `status`。

不做:

- `symbols.index.jsonl`
- 多语言 parser。
- credit score。
- 自动 drift score。

### Parser 策略

MVP 只用:

- `rg` 查文件路径和关键词。
- 文件内容 hash 判断案例当时引用片段。
- YAML frontmatter parser 生成索引。

Go parser、ts-morph、tree-sitter、SQL parser 都推迟到确认召回价值之后。

## 10. 修复新 issue 时的使用流程

`multica-defect-fix` workflow 插入以下步骤:

1. 读取当前 issue / bug report / comments。
2. 提取症状、错误、平台、模块、疑似文件、关键词。
3. 查询 `cases.index.jsonl` 和正式 wiki 页面。
4. 输出内部工作记录:

```text
Historical cases checked:
- pattern.xxx: similar because ..., reusable ..., not applicable ...
- No exact match for ...

Current investigation plan:
- hypothesis
- files to inspect
- verification target
```

5. 读取当前代码和必要 source PR。
6. 实施修复和测试。
7. 只在内部记录引用案例。面向用户的 PR 描述、issue comment、最终结果不暴露私有 case id 或案例库内容。
8. PR 合并后,scanner 可生成候选 case。未合并前不发布正式案例。

## 11. 文档腐化治理

MVP 只做轻量 lint,不做自动 drift 和信用机制。

### 11.1 腐化类型

| 类型 | 例子 | MVP 处理 |
| --- | --- | --- |
| Schema drift | frontmatter 缺字段、字段名变形 | schema lint |
| Link rot | `[[id]]` 指向不存在页面 | link lint |
| Source missing | `source_pr` 无法访问或 source record 缺失 | source lint |
| Security rot | 页面含 token、cookie、私密日志 | secret scan |
| Code drift | 文件改名、函数删除、内容变化 | MVP 人工发现,后续独立 PRD 再自动化 |
| Confidence rot | 后续同类回归 | MVP 人工降级,后续独立 PRD 再评估 |

### 11.2 Lint

`wiki lint` MVP 至少检查:

- YAML frontmatter 是否符合 schema。
- `id` 是否唯一、稳定、全小写点分段。
- `source_pr`、`merge_commit`、`code_refs.path`、`code_refs.content_hash` 是否存在。
- `related` 指向的 id 是否存在。
- `visibility` 是否为 `private`。
- 是否含密钥、token、cookie、长日志、用户隐私。
- `cases.index.jsonl` 是否由当前页面重新生成。

## 12. 隐私与引用边界

案例库是私有的。

规则:

- Agent 可以在内部 scratchpad、run log 或 task metadata 中记录 case id。
- Agent 不在公开 PR 描述、GitHub issue comment、Multica issue comment 中写"参考了内部案例 X"。
- PR 描述只写当前问题的 root cause、fix、test。
- Wiki 页面不得包含未脱敏用户日志、token、cookie、私钥、内部人员敏感讨论。
- 如需要向外部解释,只能描述当前 PR 自身事实,不能泄露案例库结构和私有链接。

## 13. MVP 范围

### 13.1 必做

- 建立私有 `defect-fix-wiki` 最小目录结构和 schema。
- 实现 GitHub closed PR scanner:
  - 扫描 `multica-ai/multica` closed PR。
  - 生成 source record。
  - 按硬标准筛选候选: merged + linked issue + test diff。
- 生成 candidate case 草稿:
  - scanner 收集事实并生成骨架。
  - wiki-librarian 使用固定 prompt 让 LLM 或人工补齐正文。
  - wiki-librarian 人工审核后才发布正式 case。
- 人工审核发布 10 个 `medium` 以上 case。
- 生成 `cases.index.jsonl` 和 `tags.index.json`。
- 实现基础 lint:
  - schema
  - broken related
  - missing source
  - secret scan
- 更新 `multica-defect-fix` skill,要求修复前 query wiki,但只在内部引用。
- 用 3 个真实 defect issue 跑通 query-before-fix 流程。

### 13.2 明确不做

- 30-50 个案例。
- 100 条 source record 作为验收门槛。
- `symbols.index.jsonl`。
- 多语言 parser / AST / symbol map。
- 自动 drift 检测。
- credit score / read-applied-validated 记账。
- 自动发布所有候选 case。
- Feishu wiki 镜像页。
- OpenDeepWiki / Repomix / Gitingest PoC。
- 在 Multica 产品 UI 里展示案例库。
- 在外部 PR / issue 中引用内部 case id。

## 14. 验收标准

MVP 完成需要满足:

- Scanner 能扫描最近已关闭 PR,并生成至少 20 条 source record 作为冒烟门槛。
- Scanner 能用硬标准筛出待审核候选: merged + linked issue + test diff。
- 至少 10 个 candidate 经 wiki-librarian 审核后进入正式 wiki。
- 每个正式 case 都有:
  - `source_pr`
  - `merge_commit`
  - `source_issue_refs`
  - `code_refs.path`
  - `code_refs.content_hash`
  - `fix_pattern`
  - `verification`
  - `gotchas`
- `wiki lint` 能发现 schema 错误、坏链、缺 source、明显敏感信息。
- `multica-defect-fix` 修复新 issue 时能内部输出:
  - 查询词或召回输入
  - 命中 case
  - 适用 / 不适用判断
  - 当前修复计划
- 至少 3 个真实 defect issue 使用该流程,记录 case hit / no hit。
- 面向用户输出不暴露私有案例库。

## 15. MVP 观测指标

MVP 不追求长期质量结论,只回答"这个机制是否值得继续投"。

需要记录:

- 3 个真实 issue 中有几个命中可用 case。
- 命中 case 是否帮助形成根因假设。
- Agent 是否因为 case 避开了已知错误修法。
- Case 草稿人工审核成本。
- Scanner 假阳 / 假阴样例。
- 哪些字段最难自动生成: 根因、验证、不要照搬、架构解释。

长期指标如首轮 PR 接受率、30 天回归率、review 轮次,等案例库和调用量足够后再统计。

## 16. 分阶段计划

### Phase 0: 对齐基础

- 确认私有 wiki 存放位置。
- 确认 `multica-defect-fix` 权威 skill 路径。
- 确认 GitHub scanner 权限和官方仓库配置。
- 定义 MVP schema 和 lint 规则。

### Phase 1: MVP 闭环

- 扫描最近 closed PR。
- 生成 source records。
- 生成 candidate case 骨架。
- 人工审核 10 个 case。
- 生成 `cases.index.jsonl`。
- 接入 `multica-defect-fix` query-before-fix。
- 跑通 3 个真实 issue。

### Phase 2+: 后续候选,不进入本版验收

- 扩容到 30-50 个案例,但必须基于 Phase 1 试点证明值得继续投入。
- 改进候选筛选规则和 wiki-librarian 审核 prompt。
- 建立周期性人工 review。
- 探索 repo code map、symbol / span hash / blob sha、`symbols.index.jsonl`、drift report。
- 召回量级足够后,另起 PRD 评估是否需要 read / applied / validated 记账或信用分。

## 17. 当前发现的前置问题

之前本地 `.claude/skills/multica-defect-fix/SKILL.md` 指向 `.agents/skills/multica-defect-fix/SKILL.md`,但权威文件缺失。现在已从 `wenxue` 工作区下载并恢复:

- `.agents/skills/multica-defect-fix/SKILL.md`
- `docs/agent-skills/go-backend-quality/SKILL.md`

落地本 PRD 前仍需确认:

- Codex / Claude / Multica runtime 实际读取的是哪一个 skill 来源。
- 这个 skill 是否应该进入仓库、私有 skill repo,还是只留在 workspace skill 数据库。

## 18. Open Questions

- 私有 wiki 最终放在 repo 内、单独私有 repo,还是 Feishu wiki + repo mirror?
- GitHub scanner 使用 `gh`、GitHub API,还是 Multica 内部 GitHub integration?
- Case 审核本版默认由 maintainer 兼任 wiki-librarian; 后续是否独立成专职 agent 再评估。
- closed-unmerged PR 是否要在后续沉淀为 `antipattern` 或 rejected-fix 资料?
- 3 个真实 issue 的试点样本从哪里选: GitHub open issues、Multica issue,还是人工指定?

## 19. References

- Karpathy LLM Wiki gist: https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f
- Aider repository map: https://aider.chat/docs/repomap.html
- Aider tree-sitter repo map notes: https://aider.chat/2023/10/22/repomap.html
- Repomix: https://repomix.com/
- Gitingest: https://gitingest.com/
- OpenDeepWiki: https://github.com/AIDotNet/OpenDeepWiki
- Internal Feishu reference: https://lilithgames.feishu.cn/wiki/EJfVwzSdZi4NW9kA1iCcf0VSndh?fromScene=spaceOverview
