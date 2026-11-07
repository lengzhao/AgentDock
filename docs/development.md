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
| CLI 命令树 | `internal/cli` | `setup` / `run` / `service` / `status` / `doctor` / `verify` / `catalog` / `agents` / `tasks` / `access` / `config` / `version` |
| 配置 | `internal/config` | 模型 + 校验 + 原子写（0600）；`KnownFields` 拒绝未知字段；secret 只能写引用，裸值直接报错 |
| 凭据槽位 | `internal/credentials` | `Store` 接口 + keyring（`zalando/go-keyring`，写后强制回读）+ memory；`Resolver` 支持 `keyring://svc/account` 与 `env:NAME` |
| 服务槽位 | `internal/service` | `ServiceManager` 接口 + launchd 实现（plist 渲染、`bootstrap/bootout/kickstart/print` 语义归一化、拒绝把 secret 写进 plist） |
| 运行装配 | `internal/runtime` | AgentDock 配置 → AgentKit 实例图；**唯一**出现 AgentKit kind 名的地方 |
| 适配器目录槽位 | `internal/catalog` | ACP registry：磁盘缓存、离线降级、binary 优先 npm、许可告警、白名单封装 |
| 平台注册 | `internal/platform/lark` | 官方 `scene/registration` 扫码建应用（LARK-01），凭据入库并回读；拒绝/超时/取消给出可操作提示 |
| 所有者绑定 | `internal/access` | 实现 AgentKit `cap/ingress.Policy`；未绑定期间一律拒绝，绑定靠一次性 challenge + 有效期 + 单次使用 |
| 端到端校验 | `internal/verify` | ingress 策略装饰器：只有被 owner 策略接受的入站消息里的 `AGENTDOCK-…` token 才会写 marker，连接成功但没收到消息不会误判为通过 |
| Agent 检测 | `internal/agent` | 解析可执行文件 + 版本探测；区分 missing / broken / ready / unknown，**不声称登录有效** |
| 运行记录 | `internal/state` | SQLite（纯 Go `modernc.org/sqlite`）：任务状态、投递状态、启动时把非终态标为 interrupted、30 天清理 |
| 单实例 | `internal/lock` | flock；进程崩溃由内核释放，不留脏锁 |
| 诊断 | `internal/doctor` | 分层检查 + 退出码 0/2/1 + `--json` |

测试：`go test ./...` 覆盖配置校验与原子写、凭据引用解析、launchd plist 与状态解析、图装配（真实 `build.Build[agentkit.Runner]`）、owner 绑定、verify 策略、agent 检测超时、任务状态机与投递生命周期。

## 3. 尚未实现

| 项 | 现状 | 相关需求 |
|---|---|---|
| 真实租户端到端验证 | 无。`verify` 逻辑就绪，但未在真实 Lark 应用上跑通 | LARK-03、T0.1 |
| 扫码注册的真实性验证 | 代码按官方 SDK 写，未在真实租户点过；注册中断后的核对流程只记录了 `PendingPlatform` 标记 | SET-02、T0.1 |
| 工具审批卡片闭环 | 未实现。`autoApprove` 默认为 false，审批相关需求仍是目标 | ACL-02 |
| 会话恢复 / 取消终态 | 依赖 ACP 能力；未做能力矩阵降级 | AGT-02、AGT-03 |
| 平台投递幂等与 unknown 处理 | 数据模型就绪（`deliveries`），但未接入真实发送路径 | MSG-04 |
| 群聊、线程隔离策略 | 依赖 AgentKit 平台配置项，已透传但未验证 | MSG-01 |
| 打包发行 | 无 CI、无签名/公证、无安装包 | PKG-01 |
| 图形化配置 | 无 | UX-01 |
| 跨平台服务 | 仅 launchd | SRV-01 扩展 |

## 4. 与 AgentKit 的关系

- 复用：`runner`、`platform/lark|feishu`、`loop/default`、`agent/acp-remote`、`session/*`、`workspace/default`、`filesystem/local`、`cap/ingress`。
- 不复制：不在 AgentDock 里再建一套消息循环、会话存储或权限判定。
- 装配集中在 `internal/runtime/assemble.go`；`TestBuildAssemblesRunner` 会真实构造 runner，AgentKit 升级导致的 dep/kind 变更会在这里先失败。
- 固定依赖：`agentkit v0.4.45`、`pluginkit v0.1.6`、`larksuite/oapi-sdk-go v3.11.0`（与 AgentKit 一致，避免同一进程两个版本）。

## 5. 槽位一览

| 槽位 | 接口位置 | kind | 当前实现 |
|---|---|---|---|
| 凭据 | `internal/credentials.Store` | `agentdock/credentials-keyring` / `-memory` | keyring / memory |
| 后台服务 | `internal/service.ServiceManager` | `agentdock/service-launchd` | launchd |
| 适配器目录 | `internal/catalog.Source` | `agentdock/catalog-acp-registry` | 官方 registry |
| 所有者策略 | AgentKit `ingress.Policy` | `agentdock/policy-owner` | access.Store |
| 校验策略 | AgentKit `ingress.Policy` | `agentdock/policy-verify` | verify.Policy |

一个槽位只有单一实现时不造第二套抽象层；接口存在是因为它已经在配置里作为可替换点出现（例如 keyring 与 memory、lark 与 feishu）。
