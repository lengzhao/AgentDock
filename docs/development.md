# AgentDock 开发现状

日期：2026-11-07。本文件记录**代码实际做到了什么**，与需求/计划文档中的目标区分开。规划和提案见 `docs/README.md` 的阅读顺序。

## 1. 快速开始（开发者）

```bash
# 需要 Go 1.26+
git clone git@github.com:lengzhao/AgentDock.git
cd AgentDock
go build -o bin/agentdock ./cmd/agentdock

# 引导：选 Agent + 存平台凭据（secret 从 stdin 或交互输入读取）
bin/agentdock setup --agent claude-acp --app-id cli_xxx --app-secret-stdin

# 前台运行 / 后台运行
bin/agentdock run
bin/agentdock service install
```

数据目录默认 `~/.agentdock`（可用 `--home` 或 `AGENTDOCK_HOME` 覆盖）：

```
~/.agentdock/
├── config.yaml          # 只含引用，无明文 secret
├── cache/               # ACP registry 缓存（24h）
├── instance/
│   ├── state/           # setup.json、access.json、state.db、verify-ok
│   └── work/            # Agent 工作目录
└── logs/                # service.out.log / service.err.log
```

## 2. 已实现

| 模块 | 位置 | 说明 |
|---|---|---|
| CLI 命令树 | `cli` | `setup` / `run` / `service` / `status` / `doctor` / `verify` / `catalog` / `agents` / `tasks` / `access` / `config` / `version` |
| 配置 | `config` | 模型 + 校验 + 原子写（0600）；`KnownFields` 拒绝未知字段；secret 只能写引用，裸值直接报错 |
| 凭据槽位 | `credentials` | `Store` 接口 + keyring（`zalando/go-keyring`，写后强制回读）+ memory；`Resolver` 支持 `keyring://svc/account` 与 `env:NAME` |
| 服务槽位 | `service` | `ServiceManager` 接口 + launchd 实现（plist 渲染、`bootstrap/bootout/kickstart/print` 语义归一化、拒绝把 secret 写进 plist） |
| 后台环境 | `cli/service_shell.go` | 查询用户账户登录 shell，用 `-l -i -c` 加载导出环境后 `exec` AgentDock；不把环境凭据或 PATH 快照写入 plist，参数按 shell 字面量转义 |
| 运行装配 | `runtime` | AgentDock 配置 → AgentKit 实例图；**唯一**出现 AgentKit kind 名的地方 |
| 私聊话题 | `runtime` | 默认启用 Lark/飞书 `dmThreadMode`：主界面新消息开启独立话题，话题内追问延续同一会话；默认 `sessionScope: thread` |
| 适配器目录槽位 | `catalog` | ACP registry：磁盘缓存、离线降级、binary 优先 npm、许可告警、白名单封装 |
| 平台注册 | `platform/lark` | 官方 `scene/registration` 扫码建应用（LARK-01），凭据入库并回读；拒绝/超时/取消给出可操作提示 |
| 入站准入链 | `access` → `record` → `verify` | 三层 `ingress.Policy` 装饰：先判所有者，再落任务记录（重复投递在此拒绝），最后记录校验 token |
| 所有者绑定 | `access`、`platform/lark/owner.go`、`cli/owner.go` | setup/run 首次查询当前应用 owner 的 open_id 并自动绑定；保留已有绑定；失败提示手动 `access bind`（15 分钟一次性 challenge）；未绑定期间拒绝消息。已有安装可用 `access bind --auto` |
| 环境变量 slash | `envslash`、`runtime/env.go` | `/env list/set/check/unset <agent>`；仅 owner 私聊；值存 Keychain、配置只保存引用；命令日志脱敏；启动时解析并注入对应 Agent，重启生效 |
| 端到端校验 | `verify` | ingress 策略装饰器：只有被 owner 策略接受的入站消息里的 `AGENTDOCK-…` token 才会写 marker，连接成功但没收到消息不会误判为通过 |
| Agent 检测 | `agent` | 解析可执行文件 + 版本探测；区分 missing / broken / ready / unknown，**不声称登录有效** |
| 运行记录 | `state` + `record` | SQLite（纯 Go `modernc.org/sqlite`）：任务状态、投递状态、启动时把非终态标为 interrupted、30 天清理。入站消息在 ingress 链上落 `accepted` 记录，键为 `安装\|平台\|实例\|事件 ID`，进程重启后仍能去重 |
| 单实例 | `lock` | flock；进程崩溃由内核释放，不留脏锁 |
| 诊断 | `doctor` | 分层检查 + 退出码 0/2/1 + `--json` |

测试：`go test ./...` 覆盖配置校验与原子写、凭据引用解析、launchd plist 与状态解析、图装配（真实 `build.Build[agentkit.Runner]`）、owner 绑定、verify 策略、agent 检测超时、任务状态机与投递生命周期。

## 3. 尚未实现

| 项 | 现状 | 相关需求 |
|---|---|---|
| 真实租户端到端验证 | 无。`verify` 逻辑就绪，但未在真实 Lark 应用上跑通 | LARK-03、T0.1 |
| 扫码注册的真实性验证 | 代码按官方 SDK 写，未在真实租户点过；注册中断后的核对流程只记录了 `PendingPlatform` 标记 | SET-02、T0.1 |
| 工具审批卡片闭环 | 未实现。`autoApprove` 默认为 false，审批相关需求仍是目标 | ACL-02 |
| 会话恢复 / 取消终态 | 依赖 ACP 能力；未做能力矩阵降级 | AGT-02、AGT-03 |
| 平台投递幂等与 unknown 处理 | 数据模型就绪（`deliveries`），但未接入真实发送路径 | MSG-04 |
| 任务终态写入 | 入站已记录（`record` 在 ingress 链上写 `accepted`），但 running/completed/failed 尚无调用点：AgentKit 的平台层会消费 `turn/end`，没有对外的终态回调，需要在平台侧加钩子 | MSG-03、MSG-04 |
| 投递结果写入 | `deliveries` 表与状态机就绪，尚未接入发送路径 | MSG-04 |
| 群聊、线程隔离策略 | 依赖 AgentKit 平台配置项，已透传但未验证 | MSG-01 |
| 打包发行 | 无 CI、无签名/公证、无安装包 | PKG-01 |
| 图形化配置 | 无 | UX-01 |
| 跨平台服务 | 仅 launchd | SRV-01 扩展 |

## 4. 与 AgentKit 的关系

- 复用：`runner`、`platform/lark|feishu`、`loop/default`、`agent/acp-remote`、`session/*`、`workspace/default`、`filesystem/local`、`cap/ingress`。
- 不复制：不在 AgentDock 里再建一套消息循环、会话存储或权限判定。
- 装配集中在 `runtime/assemble.go`；`TestBuildAssemblesRunner` 会真实构造 runner，AgentKit 升级导致的 dep/kind 变更会在这里先失败。
- 固定依赖：`agentkit v0.4.45`、`pluginkit v0.1.6`、`larksuite/oapi-sdk-go v3.11.0`（与 AgentKit 一致，避免同一进程两个版本）。

## 5. 槽位一览

| 槽位 | 接口位置 | kind | 当前实现 |
|---|---|---|---|
| 凭据 | `credentials.Store` | `agentdock/credentials-keyring` / `-memory` | keyring / memory |
| 后台服务 | `service.ServiceManager` | `agentdock/service-launchd` | launchd |
| 适配器目录 | `catalog.Source` | `agentdock/catalog-acp-registry` | 官方 registry |
| 所有者策略 | AgentKit `ingress.Policy` | `agentdock/policy-owner` | access.Store |
| 记录策略 | AgentKit `ingress.Policy` | `agentdock/policy-record` | record.Policy |
| 校验策略 | AgentKit `ingress.Policy` | `agentdock/policy-verify` | verify.Policy |

一个槽位只有单一实现时不造第二套抽象层；接口存在是因为它已经在配置里作为可替换点出现（例如 keyring 与 memory、lark 与 feishu）。

## 模块扩展

公开包位于项目顶层，应用叶子依赖通过 assembly.Open[T] 装配；默认 kind 位于 plugin 包。外部项目可导入契约，实现接口并注册唯一 kind，再从自定义 main 导入 cli.Execute。

例如自定义凭据后端注册：

```go
pluginkit.Register("example/credentials", func() (credentials.Store, error) {
    return credentials.NewMemoryStore(), nil // 示例；生产环境替换为自己的后端
})
```

自定义入口编译后使用 --credential-store example/credentials。凭据插件不能打印 secret，必须遵循 Store 的缺失、删除幂等与写后回读契约。叶子依赖不得持有需要关闭的后台资源；需要依赖图和生命周期的插件通过 runtime.BuildWithOverrides 装配。

运行层示例：

```go
runner, result, err := runtime.BuildWithOverrides(ctx, cfg, layout, resolver,
    map[string]runtime.Node{
        "telemetry.default": {"use": "example/telemetry"}, // 注册并实现 telemetry.Exporter,
    })
```

替换节点时显式提供完整 use/config/deps；依赖可以引用新增节点。调用方负责 runner.Run/Stop 生命周期。CLI 的 --service-plugin 和 --catalog-plugin 分别接收已注册的服务及目录 kind；默认启动参数会随服务安装保存。更换插件参数后需重新安装服务以更新启动参数。

默认适配器清单内嵌于 `agent/presets/default.json`；首次 CLI 启动会在安装目录（默认 `~/.agentdock/agent-presets.json`）写入该文件，可直接编辑以新增适配器或钉死版本，无需改代码。`setup` / `agents add` 只读该预设（及内置 `resolver`），不再从 ACP registry 拉取启动命令。`agentdock catalog` 仍可浏览官方 registry 作参考。字段：`id`、`aliases`、`loginHint`、`offlineCommand`、`resolver`（内置如 `cursor`）、`authMethod`。CLI 入参 `*-acp` 在存在短名预设时自动归一。

## 微信平台与统一认证

- platform.Authenticator：登录与 owner 解析的插件契约；Login 不负责存储。
- platform.SaveLogin：统一保存、独立回读验证，按安装/平台/批次隔离引用。
- platform/lark.Auth、platform/weixin.Auth：各平台授权流程；CLI 不再写平台分支。
- platform/weixin：原生 Go iLink 客户端、AgentKit Platform、owner 准入、游标保存、文本回复及请求绑定审批。
- config.Platform.Credentials：通用角色引用；兼容旧 appIdRef/appSecretRef。新登录不能与旧字段混用。
- 旧 Lark RegisterApp 便利 API 也走 SaveLogin；需要 Installation 参数，返回 References。固定全局凭据写入 helper 已移除。
- 能力边界及操作步骤见 [weixin.md](weixin.md)。媒体、群聊、选择题审批未实现；模拟测试不是实际账号 E2E 证据。

## 多平台并行连接

Config.Platform 保留主连接；Config.Platforms 是附加连接列表。Connections/Connection/UpsertPlatform 是统一访问边界。Paths.ForPlatform 保留主连接状态路径，并给新增平台分配独立状态目录。runtime 使用 AgentKit platform/multiplex；共享 Agent、会话底座和根任务库。setup --platform weixin 会追加连接，且不指定 --agent 时保留已有 Agent 配置。access --platform 和 verify --platform 管理指定连接。

## 定时任务与 [meta]

schedule 包提供固定文件持久表、并发/文件锁、平台/会话/用户授权视图、时区适配、触发审计与执行/投递独立记录。runtime 装配一个共享 cron，通过 turn-scoped ACP session MCP runtime 暴露 schedule 工具；审计 loop 以实际 Dispatch 返回值确认执行成败，审计 platform 处理真实 ACP MessageEnd 及不确定投递。默认配置与验证边界见 [scheduling.md](scheduling.md)。

按用户要求对照 AgentKit 与 agent-harness 扩展 [meta]：全部内置 token 与精确非敏感 metadata 键，有值才注入。运行时真实注入及 fake ACP/MCP 触发均有集成测试。

## Cursor 原生 ACP

Cursor 使用本机 CLI acp 子命令与 cursor_login，不使用 registry 未安装的相对 archive cmd。agents add 只追加 Agent，可显式设默认；不会重新执行平台登录。CLI 与配置保留测试、fake Cursor auth+MCP 定时器端到端测试覆盖接入边界，详见 [cursor.md](cursor.md)。
