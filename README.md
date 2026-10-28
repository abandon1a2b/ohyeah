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

项目尚未正式发布，暂不提供历史数据库迁移。新数据库直接创建完整 schema；已有数据库只接受当前 schema 版本。不支持的版本会明确报错，请使用新的 `state.path` 并重新采集，程序不会自动删除旧数据。

采集逻辑与 `ohyeah` 二进制解耦。任何受信任的可执行文件都可以实现版本化 collector 协议，并通过 `command` driver 注册。

`ohyeah` 有意不内置特定内容的 collector。Markdown 整理、Codex 会话选择、内部系统接入以及未来的来源逻辑，都应放在独立维护的 collector 扩展中。核心只负责校验输出、保存同步状态和游标、投递索引 outbox，并应用协议中声明的纠错与 provenance 关系。

## CLI

构建前端并生成包含 Web UI 的单一二进制文件：

```bash
make build
```

已提交的 `internal/web/dist` 允许直接执行 `go build -o ohyeah .`；发布构建应使用 `make build`，确保前端源码与嵌入资源一致。

Web UI 使用 shadcn/ui 的 Nova 风格组件和 `dashboard-01` 模板布局。组件源码位于 `src/components/ui`，主题变量在 `src/styles.css`，通过显式运行 shadcn CLI 按需添加组件。构建所需的静态样式已随许可存入 `src/styles/vendor`，构建不依赖 CLI。修改页面后运行 `make build`，让 `./ohyeah serve` 使用最新嵌入式资源。

第一次使用时，直接启动完整服务：

```bash
./ohyeah serve
```

没有显式配置 Meilisearch 地址时，默认采用 `managed` 模式：首次运行会下载固定版本的官方二进制并校验 SHA-256，保存随机主密钥，启动本地子进程，等待健康检查并初始化索引。正常退出会停止子进程；异常退出后，操作系统会释放服务锁。如果遗留进程仍占用端口，下一次启动会明确报告端口冲突。

Meilisearch 仅监听 loopback 地址，默认 `http://127.0.0.1:7700`。二进制、密钥、日志和版本独立的数据目录默认保存在用户配置目录下的 `ohyeah/meilisearch`；可通过 `meilisearch.directory` 设置绝对路径。日志位于该目录的 `meilisearch.log`。目前固定版本为 `v1.54.3`，支持 macOS/Linux amd64、arm64 和 Windows amd64；Linux 官方二进制要求 glibc 2.35+。下载和运行不依赖 Docker 或 sudo，不自动升级版本。首次下载需要访问 GitHub Releases。

已有配置显式设置 `meilisearch.url` 且未指定 `mode` 时，保留旧行为，使用外部实例。要切换为服务管理的实例，请明确设置：

```yaml
meilisearch:
  mode: managed
  url: http://127.0.0.1:7700
  api_key: ""  # 自动生成并持久化；不需要在配置文件中保存密钥
```

外部实例则使用 `mode: external`，并配置 `url` 和 `api_key`。managed 模式不会接管已有进程：端口被占用或同一数据目录已有 `serve` 时，会拒绝启动。启动时发现主索引数量与 SQLite 不一致，会从 SQLite 重建索引。不同版本使用独立的 Meilisearch 数据目录；可选向量索引需重新运行 `index vectors`。修改进程配置后需要重启 `serve`。

本地 CLI 仍可直接操作状态库和索引。Meilisearch 已运行后，可以显式初始化：

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

请在项目目录使用 `./ohyeah serve`。直接运行 `ohyeah serve` 可能调用 PATH 中的旧安装版本；如果启动日志只有 `ohyeah synchronization started`，该版本不会启动 Web UI。先运行 `make build` 更新项目内的二进制，再用 `./ohyeah serve` 启动。

`serve` 是完整的本地服务入口。一个命令会同时启动：

- Fiber HTTP API 与嵌入式 Web UI
- 启动时同步和周期同步
- 动态文件系统 watcher
- 配置文件 watcher 与自动 apply
- Meilisearch outbox 投递
- managed 模式下的 Meilisearch 下载、校验和进程管理

```bash
./ohyeah --config /path/to/config.yaml serve
```

默认访问地址是 [http://127.0.0.1:8787](http://127.0.0.1:8787)，可以通过 `server.address` 修改。Web UI 提供状态总览、记忆搜索与完整详情、数据来源与手动同步、结构化配置和高级 YAML 编辑、配置校验/计划/保存，以及索引诊断和重建。

前端开发时，先启动后端，再在另一个终端运行 `npm run dev`。Vite 固定监听 `http://localhost:5173`，并将 `/api` 请求代理到 `http://127.0.0.1:8787`。如果修改后端监听端口，也要同步修改 `vite.config.ts` 中的代理地址。

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

### Qwen 向量与混合检索

启动本机 Ollama 并下载 `qwen3-embedding:0.6b` 后，在配置中启用
`meilisearch.embedding.enabled: true`，其他参数参考 `config.example.yaml`。
向量能力是可选增强。默认搜索始终为全文检索，初始化、同步、主索引重建
和诊断均不依赖 Ollama。`ohyeah index vectors` 增量维护
`<主索引名>_vectors_<配置哈希>`：只为新增或变化的记忆生成向量，并移除已删除记录。
`ohyeah index vectors --dry-run` 只报告待更新数量，`--rebuild` 才清空并全量重算。
更换模型、维度或输入长度后会使用新的向量索引，不会误用旧向量。
应用先调用 Ollama，再将向量以 userProvided 方式提交，
Meilisearch 索引任务不调用模型服务。向量构建失败不影响主全文索引或 outbox。

CLI 可通过 `--mode hybrid|semantic` 主动使用向量；Web UI 搜索页也可选择
关键词、混合或语义模式，HTTP 搜索接口接受同名 `mode` 参数。当前只返回检索
结果，不生成回答。向量未启用、索引缺失或模型不可用时自动回退全文；查询模型的
等待上限为 2 秒，普通全文查询完全不调用模型。更新或删除后的旧向量结果会
通过主索引的记录、contentHash 和元数据检查过滤。同步新数据后再次运行
`index vectors` 即可增量更新向量；当前尚未在普通同步中自动更新向量。

修改 embedding 配置后需要重启 ohyeah 服务。向量化输入为标题与正文，
最长 12000 字节，超过时裁剪；当前 Ollama 上下文为 4096 token，长文档尚未
增加语义切块。可选向量构建失败会明确报错，模型恢复后重跑 `index vectors`。

## Codex Skill

仓库根目录同时也是一个 Codex skill。[SKILL.md](SKILL.md) 保持精简：CLI 负责操作细节和结构化输出，skill 只规定路由、provenance 和时效性边界。

### HTTP 客户端与 Skill 下载

`serve` 同时提供 Python 和 Node.js 两种轻量 HTTP 客户端，无第三方依赖，不分发 Go 客户端。它们通过 `/api/v1` 访问服务，不打开本地 SQLite，也不直接访问 Meilisearch。

- [Python 下载包](http://127.0.0.1:8787/downloads/python.zip)：Python 3.9+。
- [Node.js 下载包](http://127.0.0.1:8787/downloads/node.zip)：Node.js 18+，使用内置 fetch。
- [下载清单](http://127.0.0.1:8787/downloads)：版本、运行时要求和各文件地址。

每个 ZIP 包包含 `ohyeah/SKILL.md`、`ohyeah/README.md` 和 `ohyeah/scripts/` 下的客户端。将整个 `ohyeah` 目录放到 Agent 的 skill 目录即可，也可以直接运行脚本。两个包是同一 Skill 的可选实现，选择符合当前环境的一种安装。单文件地址分别为 `/downloads/python/ohyeah.py`、`/downloads/node/ohyeah.mjs`；配套 Skill 地址为 `/downloads/python/SKILL.md` 和 `/downloads/node/SKILL.md`。

```bash
python3 scripts/ohyeah.py projects --json
python3 scripts/ohyeah.py search "缓存 失效 最终结论" --project example --json
node scripts/ohyeah.mjs get <memory-id> --json
node scripts/ohyeah.mjs status --json
```

下载脚本的默认地址来自服务配置；OIDC 模式使用 `auth.public_url`，本地模式使用监听地址。`--server URL` 或环境变量 `OHYEAH_SERVER` 可覆盖地址，命令行优先。不会从不受信任的请求 Host 推断地址。

两种客户端都提供 `projects`、`search`、`get`、`status`，始终输出 JSON，支持 `--help`、`--json` 和 `--timeout`（默认 10 秒）。`search` 要求 `--project`，支持 `--type`、`--mount`、`--workspace`、`--kind`、`--mode` 和 `--limit`。退出码统一为：0 成功，1 HTTP/网络错误，2 参数错误；错误 JSON 写到 stderr。

中央多人部署使用 `auth.mode: oidc`：服务端通过 OIDC 登录验证身份，按项目授权，向 CLI 签发只保存哈希的有期限、可撤销令牌。个人模式默认 `auth.mode: local`，只允许绑定 loopback IP。OIDC 模式的 Web UI 支持登录、退出和权限控制。配置、接入身份服务、客户端授权、机器账号和权限边界见 [Authorization](docs/authorization.md)。

```bash
python3 scripts/ohyeah.py login
node scripts/ohyeah.mjs login --no-browser
python3 scripts/ohyeah.py whoami
python3 scripts/ohyeah.py tokens
python3 scripts/ohyeah.py revoke <credential-id>
python3 scripts/ohyeah.py sync <project/type/mount>
python3 scripts/ohyeah.py logout
```

两个客户端共享按服务地址隔离的凭据文件；无人值守账号可通过 `OHYEAH_TOKEN` 提供管理员签发的令牌。登录命令不会打印令牌。令牌过期后重新登录，当前没有自动刷新或 OIDC provider refresh token 存储。

### 远程采集上传

中央服务同时支持执行本机 command collector，以及接收客户端 collector 结果。上传来源使用 `collector.mode: upload`、明确的 `writer` 和 `upload_sources` 权限；周期同步不会执行上传来源。客户端提供 `upload-state`、`prepare-upload` 和 `upload`，提交支持固定批次 ID 重试、版本冲突检查、原子游标更新和来源内快照删除。配置与操作示例见 [Remote Collector Uploads](docs/uploads.md)。

OIDC 模式下，Web UI 的“访问管理”提供账号权限在线编辑、凭据签发/轮换/撤销和持久化审计查询。权限变更立即影响已有令牌。来源页支持上传准备好的 JSON 批次，配置页可选择中央执行或远程上传。没有现成 SSO 时，可用 [本地 Keycloak 中央服务示例](deploy/oidc-demo/README.md) 试用完整登录流程；它是开发示例，不是生产部署。

### 验证

```bash
go test -race ./...
go vet ./...
npm run check
```

客户端契约测试会实际执行 Python 和 Node.js 脚本，包含中文与特殊字符编码、参数校验、错误响应、截断响应、慢速流式响应和总请求超时。相应运行时缺失时该客户端测试会跳过。Meilisearch 管理测试覆盖下载校验、断点续传、服务器忽略 Range、失败清理、并发密钥生成和进程生命周期。

macOS arm64 上可使用已经下载的固定版本社区版 Meilisearch，运行真实服务集成测试（全部数据放在独立临时目录）：

```bash
go build -o ohyeah .
OHYEAH_TEST_MEILISEARCH=/absolute/path/to/meilisearch-v1.54.3 \
OHYEAH_TEST_BINARY="$PWD/ohyeah" \
go test ./internal/cli -run TestRealServiceLifecycle -v -count=1
```

该测试实际启动服务和 Meilisearch，通过 collector 采集记录，验证搜索、项目隔离、provenance、两种下载客户端、更新、采集失败保留数据、快照删除、SQLite 重建索引、正常退出与子进程异常退出。默认测试不执行网络下载，也不修改已有实例。

## 当前状态

目前的第一条 vertical slice 已支持：外部 command collector、版本化请求、snapshot/delta 完成语义、持久化游标、source 原子提交、可恢复的 Meilisearch 索引、纠错抑制、project/type/mount 配置、dry-run、隔离的 mount 失败、持续调用，以及保留 provenance 的裁剪搜索结果。

下一里程碑是建立历史问题评测集。后续新增抽取或排序功能，只有在不增加误导性结果的前提下，能够用数据证明其改善了正确最终结论的检索效果，才应被接受。

## 维护与可复现构建

构建工具链以 `go.mod`、`.node-version` 和 `package.json` 的 `packageManager` 为准。首次构建前运行 `npm ci`，使用已提交的锁文件安装依赖。`make check`（也可使用 `make test`）统一运行 TypeScript 检查、Go 静态检查和竞态测试。CI 重新构建前端，并检查嵌入资源与已提交源码是否一致。

项目、类型和挂载点变更无需重启；服务、认证、搜索引擎及全局同步设置需要重启。配置 API 的 `restartRequired` 返回待重启的配置分组，Web UI 持续显示提示。保存文件与 SQLite 应用不是跨资源事务：保存成功后若应用失败，接口明确报错，后续 reload 可重试应用。

模块边界、配置生效语义与后续数据迁移约定见[维护指南](docs/maintenance.md)。

当前状态库为 schema 9，正式程序不保留迁移代码。离线备份与完整性检查使用 `state backup` / `state check`；`state prune` 默认预览清理，设置 `--dry-run=false` 才执行。服务按配置定期清理已完成 outbox 和同步历史，审计与上传回执默认永久保留。`serve` 运行时使用 HTTP 客户端，本机 Go CLI 的状态操作需要先停止服务。

`make contracts` 生成前端类型及 JSON Schema；`make audit` 检查依赖；`make e2e` 使用独立临时数据库和真实 Meilisearch 运行浏览器回归。完整流程见维护指南。
