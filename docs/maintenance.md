# 架构与维护约定

采用模块化单体，支持个人本地模式和中央单实例模式。SQLite 为权威状态；Meilisearch 为可重建的派生索引。`App.Open` 对状态库持有进程锁，同一状态库一次只允许一个服务或本机 CLI 会话。运行 `serve` 时用 HTTP 客户端操作；本机维护命令须先停止服务。

## 模块职责

- `internal/cli`：命令参数、信号上下文和结果输出。
- `internal/runtime`：协调 HTTP、采集、文件监听、配置监听、定期维护及托管搜索进程。任一 worker 退出时取消其他 worker，等待全部退出后再关闭数据库。
- `internal/app`：依赖组装、配置与状态查询用例。
- `internal/access`：不依赖 HTTP 框架的权限规则；`auth` 负责 OIDC、凭据和 HTTP 适配。
- `internal/web`：请求解析、权限入口及错误响应。
- `internal/registry`：项目、类型、挂载点的期望状态与变更计划。
- `internal/syncer`：采集提交与 outbox 投递；查询和可选向量同步分别维护。
- `internal/store`：按 registry、memory、runs、index、identity、audit、maintenance 划分 SQLite 事务。
- `internal/search`：接收类型化 MemoryUnit 的搜索适配器，序列化和向量服务调用留在适配器。所有网络写入及任务等待传递 context。
- `pkg/collector`：独立的外部采集协议。

来源执行参数存储在 `Source.Execution`，上传者存储在 `Source.Writer`。`Options` 只用于 collector 扩展参数。纠错关系记录 `owner_object_id`：更新目标文档时保留其他文档声明的关系，删除单元时清理失效关联。

outbox 按事件顺序投递，远端完成后才确认并释放 payload。失败重试保持幂等。重建索引暂停本实例的采集提交，重放当前数据及待投递事件后校验数量。搜索结果需与 SQLite 当前记录匹配，避免把删除、撤回或尚未更新的旧索引记录作为有效结果。

## 配置生效边界

项目、类型和挂载点由 registry 热应用。`server`、`auth`、`meilisearch`、`sync`、`maintenance`、`state_path` 和 `log_level` 的变更按需要重启处理。全局同步参数的部分值可能影响新生成的来源，但不表示整个配置分组已经热生效。

`Load` 与内存 `Parse` 共用默认值、环境变量和校验规则。配置文件路径解析符号链接，保存实际目标而不替换链接。配置协调器保留启动时的配置快照；API 的 `restartRequired` 只返回字段名，不返回密钥差值。

Web 保存和文件重载串行执行，客户端通过 `expectedHash` 检测旧版本编辑。配置 watcher 在自身循环中执行重载，退出前等待回调结束。collector 提交也会校验采集时的来源配置，拒绝覆盖已经修改的来源。

文件原子替换和 SQLite 事务属于两个资源：保存后若应用失败，错误会说明文件已保存，后续 reload 可以重试。外部编辑器不参与实例锁，仍应避免并发手工编辑。

## 数据、备份与保留策略

正式程序只包含当前 **schema 9** 的建表 SQL 和版本校验，不包含历史升级器或迁移分支。现存 v8 数据已执行一次性迁移，迁移代码已删除；完整备份与逐表验证报告保存在部署的数据目录，不进入源码仓库。

停止服务后执行：

```bash
./ohyeah --config /path/to/config.yaml state check
./ohyeah --config /path/to/config.yaml state backup /backups/new-snapshot.db
./ohyeah --config /path/to/config.yaml state prune
./ohyeah --config /path/to/config.yaml state prune --dry-run=false
```

`backup` 使用 SQLite 一致性快照并检查完整性，权限为 0600，拒绝覆盖已有文件。`prune` 默认只预览。服务启动后及每隔 `maintenance.interval` 也执行保留策略：

| 数据 | 默认策略 |
|---|---|
| 已确认 outbox | payload 立即释放，元数据保留 7 天 |
| 已结束同步记录 | 保留 30 天 |
| 审计记录 | 永久保留；管理员可配置期限 |
| 上传回执 | 永久保留，保证旧批次重试的幂等性 |
| 待投递 outbox、运行中同步、未完成审计 | 不按期限清理 |

保留期限为 0 表示永久保留。清理会释放可重用页面，不承诺立刻缩小 SQLite 文件；备份生成紧凑的独立数据库。

恢复时先停止服务，保留当前数据库及可能存在的 WAL/SHM 文件，再将已验证备份复制到新的路径，用临时配置指向它运行 `state check`。校验成功后切换正式 `state_path`；如搜索索引不匹配，停服执行 `index rebuild`，再启动服务。恢复验证不需要连接或修改生产索引。旧 schema 的备份只用于保留原始数据和回滚到匹配的旧程序，不可直接用新程序打开。

## 接口与前端依赖

`make contracts` 从 Go 响应结构生成 `src/generated/contracts.ts` 与 `docs/api.schema.json`。不要手改生成文件；`make check` 会检测漂移。JSON Schema 描述数据结构，不替代 HTTP 权限测试与 Python/Node 客户端契约测试。

前端 API 共用请求及 CSRF 处理，页面按需加载。轮询状态集中在 hook 中，取消过时请求，后台标签不持续轮询；非管理员同步历史由服务端按权限批量过滤。

shadcn 组件仍由本仓库维护。构建只需要静态 CSS，已将带 MIT 许可的 4.23.0 样式快照保存在 `src/styles/vendor`，移除整个 CLI 依赖树。新增组件时可显式运行 shadcn CLI；更新样式快照时记录来源版本并验证界面。不要因自动审计建议而把 CLI 降级到不兼容版本。

## 构建与验证

- 工具链以 `go.mod`、`.node-version`、`package.json` 的 `packageManager` 为准；使用 `npm ci` 安装锁定依赖。
- `make check`：生成契约检查、TypeScript、Go vet、竞态测试及 Python/Node 契约测试。
- `make audit`：npm 漏洞检查与固定版本 govulncheck。
- `make build VERSION=<version>`：重建嵌入式前端，构建带版本号的二进制。前端修改必须同步提交 `internal/web/dist`。
- `npx playwright install chromium` 后执行 `make e2e`：使用独立临时数据库和真实 Meilisearch 验证搜索、provenance、非法参数、配置保存、重启提示及桌面/移动端渲染。

E2E 默认通过 managed 模式下载并校验固定版本 Meilisearch；可设置 `OHYEAH_E2E_MEILISEARCH=/path/to/binary` 使用现有二进制启动隔离的外部实例。测试从不复用现有服务、数据库或索引，退出后清理临时进程与目录。

CI 检查依赖、生成契约、构建资源和浏览器回归，失败时保存浏览器证据。Dependabot 分别跟踪 Go、npm 和 Actions；依赖升级仍须经过这些验证。
