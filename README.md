# ohyeah

`ohyeah` 是面向编码 Agent 的本地工作记忆与证据检索服务。

它用于可靠地找回已经发生过的工作：已确认的事实、决策、纠错、调查结果、未完成事项，以及支撑这些结论的原始证据。它的目标是减少重复调查，但不会取代代码、Git、数据库、日志或其他拥有当前事实的工具。

## 目标

针对一个工作问题，`ohyeah` 应快速找出最相关的历史上下文，优先返回后续纠错和最终结论而不是已经被推翻的猜测，并为每条重要结果保留可打开的原始来源。

典型流程如下：

```text
问题
  -> 检索相关工作历史
  -> 识别结论、纠错和证据
  -> 对需要时效性的事实回到当前来源复核
  -> 继续工作
```

## 记忆来源

当前协议可以承载以下类型的来源，具体采集逻辑由独立的 collector 扩展实现：

- Codex 对话及其任务关系
- 项目文档和个人文档
- 需求分析、事故报告和运维记录
- 内部开发工具产出的已确认结果

## 设计原则

- 检索结果必须比现有工作流明显更有用。
- 搜索结果是从来源推导出的历史记忆，不是事实源本身。
- 每条结果都必须保留 provenance（来源信息），并支持打开原始来源。
- 后续的纠错和撤回优先于更早的结论。
- 代码、Git 状态、数据库、日志和外部系统等时效性事实，必须回到当前来源复核。
- 同步应自动完成，不要求用户手动标记、总结或维护记忆。
- 搜索存储和索引实现应可替换。
- 不同 workspace 是相互独立的数据源，服务不能绑定到单一项目。

## 非目标

- 替代 IDE 索引、语言服务器或符号跳转
- 替代 `rg` 进行精确、实时的代码搜索
- 构建完整的代码调用图
- 把历史日志或数据库查询结果当作永久有效的当前事实
- 要求用户手动总结、标注或发布工作内容
- 在本地工作流得到验证前，扩展成通用团队知识管理产品

## 成功标准

第一版应使用真实的历史问题进行评估，并满足以下标准：

- 高召回地找回正确的历史任务或文档。
- 在存在错误结论时优先返回最终有效结论。
- 为每条重要主张返回精确的来源引用。
- 清楚区分历史事实与当前环境中刚刚复核的事实。
- 实质性减少重复的代码、数据库、日志和对话调查。
- 在同步事件丢失或搜索索引重建后仍能安全恢复。

## 架构

```text
外部 collector -> 版本化 NDJSON 协议 -> ohyeah core
                                              |
                    SQLite 状态 + outbox -> Meilisearch
                    |                                |
                    +-------- 搜索结果补全 --------+
```

SQLite 负责保存 project/type/mount 层级、解析后的 memory unit、纠错关系、同步运行记录，以及可恢复的索引 outbox。Meilisearch 只是可替换的检索后端，可以完全从本地 SQLite 数据重建。

采集逻辑与 `ohyeah` 二进制解耦。任何受信任的可执行文件都可以实现版本化 collector 协议，并通过 `command` driver 注册。

`ohyeah` 有意不内置特定内容的 collector。Markdown 整理、Codex 会话选择、内部系统接入以及未来的来源逻辑，都应放在独立维护的 collector 扩展中。核心只负责校验输出、保存同步状态和游标、投递索引 outbox，并应用协议中声明的纠错与 provenance 关系。

## CLI

构建前端并生成包含 Web UI 的单一二进制文件：

```bash
make build
```

已提交的 `internal/web/dist` 允许直接执行 `go build -o ohyeah .`；发布构建应使用 `make build`，确保前端源码与嵌入资源一致。

初始化本地状态库和 Meilisearch 索引：

```bash
./ohyeah init
```

生产环境中的挂载点通过配置声明：

```yaml
projects:
  example-project:
    workspace: /absolute/path/to/workspace
    types:
      markdown:
        collector:
          command: /path/to/markdown-collector
          args: [collect]
          revision: 1
        mounts:
          team-docs:
            root: /absolute/path/to/workspace/doc
            revision: 1
            options:
              profile: team
```

检查配置及其变更计划：

```bash
./ohyeah config validate
./ohyeah config tree
./ohyeah config plan
./ohyeah config apply
```

`config apply` 保留给脚本和显式运维操作。运行 `serve` 时，程序会在启动阶段自动应用配置，并持续监听配置文件变化；有效变更会自动应用，无效配置不会破坏当前运行状态。

运行时 mount ID 的格式是 `project/type/mount`，例如 `example-project/markdown/team-docs`。`source add` 仅用于临时的 collector 测试。

`ohyeah` 直接启动可执行文件，不经过 shell 解释。collector 从 stdin 接收一个 JSON 请求，并向 stdout 输出 NDJSON envelope；日志应写到 stderr。完整协议见 [Collector Protocol](docs/collector-protocol.md)。

同步和搜索：

```bash
./ohyeah sync --dry-run
./ohyeah sync
./ohyeah search "OrderService 路由修改 最终结论" --project example-project --json
./ohyeah get <memory-id> --json
./ohyeah status --json
```

`search` 只接收一个 `<query>` 参数，但这个参数可以、也通常应该包含多个相关关键词。请用引号把整组关键词作为一个参数传入：

```bash
./ohyeah search "缓存 失效 用户配置" --project example-project --json
./ohyeah search "事故 根因 修复 验证" --project example-project --kind conclusion,verification --json
```

建议一次提供 2～8 个有区分度的关键词；Meilisearch 最多处理查询中的前 10 个词。搜索会优先返回同时匹配更多关键词的记录，结果不足时再按相关度放宽，因此它不是严格的布尔 `AND/OR/NOT` 查询。若要缩小范围，请组合使用 `--project`、`--type`、`--mount` 和 `--kind`。

`sync --dry-run` 会实际执行 collector，并报告预计的文档、memory unit、新增或更新以及删除数量，但不会修改 SQLite、游标、outbox 或 Meilisearch。

检查和恢复派生的搜索索引：

```bash
./ohyeah index doctor
./ohyeah index rebuild
```

`index rebuild` 会删除派生索引，再从 SQLite 重放所有 memory unit；恢复索引不需要执行 collector。

持续同步：

```bash
./ohyeah serve
```

`serve` 是完整的本地服务入口。一个命令会同时启动：

- Fiber HTTP API 与嵌入式 Web UI
- 启动时同步和周期同步
- 动态文件系统 watcher
- 配置文件 watcher 与自动 apply
- Meilisearch outbox 投递

```bash
./ohyeah --config /path/to/config.yaml serve
```

默认访问地址是 [http://127.0.0.1:8787](http://127.0.0.1:8787)，可以通过 `server.address` 修改。Web UI 提供状态总览、记忆搜索与完整详情、数据来源与手动同步、结构化配置和高级 YAML 编辑、配置校验/计划/保存，以及索引诊断和重建。

collector 默认只按计划运行；设置 `watch=true` 后，还可以监听文件系统变化，并通过 `watch_extensions=md,json` 限制扩展名。每个 collector 都必须声明一次完成运行是完整快照（snapshot）还是增量变更（delta）。

collector 有全局超时 `sync.collector_timeout`，数据类型可以通过 `collector.timeout` 覆盖。各 mount 独立同步：某个 mount 失败会被报告，但不会阻止后续 mount 运行并投递成功结果。

## 配置

配置由 Viper 读取。默认配置文件是操作系统用户配置目录下 `ohyeah/config.yaml`。也可以使用 `--config` 指定文件，或使用带 `OHYEAH_` 前缀的环境变量，例如：

```text
OHYEAH_MEILISEARCH_URL
OHYEAH_MEILISEARCH_API_KEY
OHYEAH_MEILISEARCH_INDEX
OHYEAH_STATE_PATH
```

可用配置项见 [config.example.yaml](config.example.yaml)。没有配置文件时，程序会使用适合本地运行的安全默认值。

## Codex Skill

仓库根目录同时也是一个 Codex skill。[SKILL.md](SKILL.md) 保持精简：CLI 负责操作细节和结构化输出，skill 只规定路由、provenance 和时效性边界。

## 当前状态

目前的第一条 vertical slice 已支持：外部 command collector、版本化请求、snapshot/delta 完成语义、持久化游标、source 原子提交、可恢复的 Meilisearch 索引、纠错抑制、project/type/mount 配置、dry-run、隔离的 mount 失败、持续调用，以及保留 provenance 的裁剪搜索结果。

下一里程碑是建立历史问题评测集。后续新增抽取或排序功能，只有在不增加误导性结果的前提下，能够用数据证明其改善了正确最终结论的检索效果，才应被接受。
