# 交接：workflow Critic 改造 + 运行时交付链路

分支 `claude/unruffled-kapitsa-602661`（multica）、`claude/add-image-pipeline`（cloud-runtime）。
全部已提交推送。

---

## 一、必须先做的三件事

### 1. 轮换 OSS AK/SK（安全）

会话中有两对阿里云 AK/SK 被粘贴进对话记录，**必须轮换**。当前在用的那对有
`lilith-multica` **整桶读写删**权限，而各处实际只需要很小的范围。

换 key 时**三处都要改**，漏一处就会静默失效：

| 位置 | 实际需要的权限 |
| --- | --- |
| multica CI 变量 | `downloads/*` 写 |
| cloud-runtime CI 变量 | `multica-cloud-runtime-tools/*` 写 |
| `mrt-w3-test-bed314a3/runtime-tools-config` Secret | `multica-cloud-runtime-tools/*` **只读** |

节点那处只读即可。窄权限的 RAM policy 我起草并用 `srt admin ram-policy validate` 验证过。

### 2. 部署验证（唯一能证伪的假设）

三条 Critic 改造**只跑过单元测试，没在测试环境实跑**。要验的核心假设是：

> 提示词给了命令，模型会不会真的调用 `multica workflow review`？

**这个假设尚未被证实。** 已知的反面证据：同一个模型在三次评审中都无视了协议里
明确给出的 JSON 格式要求，自己发明了 `verdict`/`reason`/`blocking_findings`。
这次给的是命令而不是格式，是否更有效**完全未经验证**。

如果它仍然不调用命令，现在的行为是：追问一次 → 节点挂
`verdict_not_declared` 等人。**不会再有裁决被伪造出来**——这是散文兜底删除后
的既定行为，比之前安全，但整条链路会更频繁地需要人工介入。

验证方法见第四节。

### 3. 两个 MR 待合

- [multica](https://gitlab.lilithgame.com/devops/multica/-/merge_requests/new?merge_request%5Bsource_branch%5D=claude%2Funruffled-kapitsa-602661)
- [cloud-runtime !19](https://gitlab.lilithgame.com/devops/cloud-runtime/-/merge_requests/19)

---

## 二、Critic 改造：从推断到声明

### 起因

线上跑缺陷修复工作流时，Critic 判了一个**正确的 reject**（修复把「新增域名」
按钮删掉了事），写成 `{"verdict":"reject",...}`，服务端读不懂，节点阻塞等人。
一次判断正确的评审，因为字段名对不上被丢弃。

深挖发现问题不止于此：日志显示该 Critic 曾**对着裁决接口猜了 14 种请求体**，
全部 `invalid request body`，耗掉整个运行——因为那个接口对智能体是**故意拒绝**
的，而拒绝的判断排在请求体校验之后，所以它永远看到 400「格式不对，再试一个」，
从没看到 409「你走错门了」。

### 改造后的模型

```
Critic 执行中
  └─ multica workflow review --decision pass|fail|blocked --reason "..."
        └─ 参数解析器当场校验，写入 .multica/review_decision.json
             └─ daemon 在任务结束时读取（带 node_instance_id 防止跨尝试串用）
                  └─ 完成回调带上 decision/reason
                       └─ 服务端在原有事务里落库
```

四条设计原则，每条都对应一个真实故障：

| 原则 | 对应的故障 |
| --- | --- |
| 决策必须被声明，不能被推断 | 三次写错词汇，一次差点让坏修复通过 |
| 身份先于请求体校验 | 智能体猜了 14 次请求体 |
| 「没报告」≠「报告了阻塞」 | 解析失败被伪造成 `blocked` 裁决 |
| 裁决绑定它评审的那个提交 | 裁决被挂到 Critic 从没看过的版本上 |

### 关键提交

| 提交 | 内容 |
| --- | --- |
| `cdfd4571` | 声明式裁决通道（CLI + daemon + 服务端） |
| `6956b31d` | 裁决绑定评审的提交版本 + `submission_id` 提列 |
| `e996ad28` | 评审不再占用 `workflow_node_task` |
| `4a57cc16` | 删除散文兜底与伪造的 `blocked` 裁决 |
| `7a60d1df` | 迁移编号撞号（挡合并的硬阻塞） |

---

## 三、几个不容易看出来的坑

### 「评审是一条 task」这个建模假设藏在识别层

```go
// 改造前
if ... || !task.WorkflowNodeTaskID.Valid || ... {
    return WorkflowNodeTaskContext{}, false   // 不认这是工作流任务
}
```

**这才是评审当初必须被塞进 task 表的根本原因**：不给它一条记录，系统根本不认
它是工作流任务。这个假设写在识别函数里，从症状（UI 要求分配执行人）完全看不出来。

代价是六处代码要记得「critic 除外」，其中两处忘了，就是那两个 bug。现在评审
指向节点，**六处例外全部删除**。

### `COALESCE($3, $4)` 的类型推断冲突

Postgres 无法同时从「插入到某列」和「COALESCE 参与者」两处推断同一个参数的类型：

```
ERROR: inconsistent types deduced for parameter $3 (SQLSTATE 42P08)
```

**它不以查询错误的形式出现**，而是节点物化失败、停在
`direct_execution_not_dispatched`，看起来像调度压根没触发。错误被吞在
`workflow_node_task.last_error` 里。解法是显式 `::uuid`。

### `runtime-tools-sync` 的软失败会掩盖一切

设计上任何失败都回退到镜像自带工具并正常退出。好处是节点永远起得来，代价是
**失败完全静默**：CI 全绿、Pod 健康、CLI 是旧的、没有任何一处报错。

我踩过两次（403 签名、URL 未签名）。所以 CI 里加了「上传后 `ls` 校验」和
「签名后真的 `curl` 一次」——这类校验不是多余的，是唯一能发现问题的手段。

### 迁移撞号必须整块平移

`202_workflow_domain` 建表，`230_workflow_node_instance_primary_index` 给这些表
建索引。**只移撞号的那些会打断依赖顺序**。57 个一起移到 263-319。

记录修正做成了迁移 `262_workflow_migration_renumber`，每个环境自愈。
**注意它只跑一次**——迁移被记为已应用与语句有没有匹配到行无关。现存的库都是
改名前状态，单次足够；第三种状态的库需要照着迁移里的映射表手工改。

---

## 四、运行时交付链路（已打通）

### 为什么存在

节点上跑的 `multica` CLI **不来自 server 镜像**。改了 `server/internal/daemon/`
或 `server/cmd/multica/` 之后，`deploy-test.sh` 只更新 server 和 web，**节点毫无
变化**——拿服务端部署去论证智能体行为是无效的。

### 现在的链路

```
git push
  → multica CI publish-cli        → OSS downloads/ + latest-cli-test.txt
  → sync-multica-cli.sh --channel test  → catalog 钉版本（5 增 5 删的可审 diff）
  → cloud-runtime CI publish-runtime-tools  → 工具包传 OSS（约 2 分钟）
  → patch 节点 initContainer 的三个环境变量 + 重启 Pod
```

节点 PATH 把 `$HOME/.runtime-tools/current/bin` 排在 `/usr/local/bin` 之前，
所以同步下来的 CLI 覆盖镜像自带的。**改一行 Go 代码不再需要重建镜像**
（15～20 分钟 → 2 分钟）。

### 陷阱

- **`resolve` 必须加 `--pinned`**。不加会去 curl `latest-cli.txt`（**正式发布
  通道**），把测试构建悄悄换成线上版。
- **不要用 `$HOME/bin/multica` 手工覆盖**。它在持久卷上、PATH 优先，会盖住
  镜像和工具包，导致更新后节点仍跑旧二进制且无从察觉。
- **`build-runtime-image` 已改为按需触发**（仅 `Dockerfile` / `scripts/` 变化）。
  这台 runner 并发为 1，一次多余的镜像构建会把 2 分钟的发包堵在后面；而且
  取消它要 15 分钟才生效（shell executor 上的 `docker build` 收不到终止信号，
  最后是 ssh 上去 `pkill` 的）。

### 基础设施

- cloud-runtime 专用 runner **3308**（10.104.15.172，Rocky 9.4，tag
  `cloud-runtime-build`）。不要共用 multica 的：3256 的 podman rootless 缺
  subuid/subgid，解层必失败。
- ACR 登录必须在 **`gitlab-runner` 用户**名下，登在 root 下会报
  `insufficient_scope`，看起来像凭证错误。
- 集群 kubeconfig 在 srt `cloud-runtime-kubeconfig`（字段 `content`）。
- **容器产物属主**：CI 里跑容器写挂载目录会留下 root 文件，下一条流水线的
  `git clean -ffdx` 删不掉，**在拉代码阶段就挂、日志里没有任何线索**。已在
  容器内和 `after_script` 两处归还属主。

---

## 五、只接了一个节点

`mrt-w3-test-bed314a3/node-86b8f9b3` 已接入（标签 + initContainer + Secret）。

**其余四个节点**（`ai-infra`、`frontend-agents-team`、`smoke-test-2`、`wanli`）
仍跑旧镜像，`runtime-tools-sync` 都不存在，接不进来。这是舰队变更，需要产品/
运维决定节奏。

---

## 六、验证方法

```bash
# 1. 起一轮缺陷修复工作流（w3-test 工作区）
#    宿主 issue WTE-14841 已有 8 次历史运行，是宿主状态振荡的复现条件

# 2. 看节点上评审任务的实际行为
kubectl -n mrt-w3-test-bed314a3 exec node-86b8f9b3-0 -c runtime -- \
  grep -E "agent component=daemon" \
  /workspace/bed314a3-*/.multica/daemon.log | tail -10

# 3. 关键判据
#    - 日志里出现 `multica workflow review --decision` → 假设成立
#    - 节点挂 verdict_not_declared → 假设不成立，需要换方案
```

**如果假设不成立**，下一步不是继续改提示词（已证明无效两轮），而是运行时
强制 schema（provider 级结构化输出）。但那是 provider 相关的，Multica 是多
运行时，不能作为唯一机制——命令仍然是跨运行时的那一层。

---

## 七、已知坏但与本次改动无关

用干净树对照确认过，不是这次弄坏的：

- `TestReusedIsolatedCheckoutRepairsPromisorConfig`（repocache）——分支基线就有
- `TestInFlightOldHeadKeepsTrailingRefresh`（ghsnapshot）——不稳定，干净树上同样时好时坏
- cloud-runtime 的 `pi` profile 同步测试——`home-pi` 相关断言基线就失败

## 八、当前测试状态

- Go：44 包通过，2 个失败（上面两条）
- 前端 `pnpm typecheck`：6/6
- 未跑：E2E、`make check` 全量
