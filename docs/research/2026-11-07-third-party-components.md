# 第三方组件选型调研（插件化槽位）

调研日期：2026-11-07。对应的上游决策：AgentDock 基于 AgentKit（作者维护，可直接复用），插件装配沿用 `pluginkit` 的实例图模型，**每个可替换能力都收敛成一个"槽位（slot）"，槽位下挂多个 `kind`，按配置选择**。

数据来源与口径：

- 仓库指标（★、最近提交、License）取自 GitHub REST API，抓取时间 2026-11-07。
- npm 月下载量取自 npm registry `last-month`（窗口 2026-10-07 ~ 2026-11-05）。
- 最新可解析版本取自 `proxy.golang.org/@latest`。
- ACP 适配器能力取自官方 registry 的 `.protocol-matrix/latest.md`（生成于 2026-11-06，39 个 agent 实测）。
- 未做安装、真实授权、压测与安全审计。**本文所有"可用"结论都是"上游声明 + 有人用过"级别，不是 AgentDock 实测。**

## 1. 结论

1. **选型门槛**（本文的准入线，写死以便后续复用）：
   - 通用库：★ ≥ 1000 且近 6 个月有提交；或 ★ 500+ 但被 AgentKit / cc-connect / Zed / JetBrains 等真实项目依赖；
   - 平台/协议 SDK：官方维护优先，官方无 Go SDK 时接受社区实现但必须标注版本漂移风险；
   - 拒绝：已归档（archived）、近 18 个月无提交、无 License、或需要 cgo 才能完成首版核心路径。
   - "有人用过"的证据按强度排序：被主流项目依赖 > 官方 registry/CI 探测 > npm/Go 下载量 > ★。
2. **真正需要新选的槽位只有 6 个**：凭据存储、后台服务、引导 TUI、二维码、本地状态库、ACP 适配器目录。其余（IM 平台、ACP 客户端、MCP、工具、日志、重试）AgentKit 已经带实现，AgentDock 只做适配，不重复建设。
3. **ACP 生态是本轮最大的收获**：官方 registry 已有 44 个 agent 声明、39 个实测 `initialize` 通过（38 成功），并且每个条目带**各平台二进制的下载地址与 sha256**。这意味着 `Claude/Codex/Pi/Grok/Kimi/OpenCode/Goose/Cursor/Copilot` 的接入可以做成"目录驱动"的插件，而不是逐个硬编码。
4. **两个许可待核实点**（不阻塞设计，阻塞发布）：
   - `claude-agent-acp`：registry 标 `proprietary`，仓库根 `LICENSE` 是 Apache-2.0；它打包的 `claude` CLI 是专有物。分发方式需确认。
   - `codex-acp`：GitHub API license 为 `NOASSERTION`，registry 标 Apache-2.0。
5. 结论方向：**槽位接口由 AgentDock 定义（小、稳定、无第三方类型泄漏），第三方库只出现在适配器内部**。这样换库不改接口，也不让 `pluginkit` 的 `kind` 名字绑死到某个 SDK 的版本上。

## 2. 槽位总览

| # | 槽位 | 推荐 kind | 底层第三方 | 成熟度证据 | 是否 AgentKit 已有 |
|---|---|---|---|---|---|
| S1 | Agent 接入（ACP 客户端） | `agentdock/agent-acp` | `coder/acp-go-sdk` | 250★，2026-11-05 提交，Apache-2.0；被 AgentKit 直接依赖 | 有（`plugins/agent/acpremote`） |
| S2 | Agent 适配器目录 | `agentdock/catalog-acp-registry` | ACP registry JSON（CDN） | 44 个 agent / 39 个实测，官方 CI 每小时更新版本 | 无 |
| S3 | IM 平台 | `platform/lark` / `platform/feishu` | `larksuite/oapi-sdk-go/v3` | 624★，MIT，v3.12.2（2026-10），官方维护 | 有（`runtime/platform/feishu`） |
| S4 | 凭据存储 | `agentdock/credentials-keyring` | `zalando/go-keyring` | 1361★，v0.2.8（2026-03），MIT；agent-harness 已生产使用 | 无（AgentKit 是 AES 加密文件 + env） |
| S5 | 后台服务 | `agentdock/service-launchd` | 自写薄封装（±`kardianos/service`） | `kardianos/service` 4853★，Zlib，v1.3.0 | 无 |
| S6 | 引导 UI | `agentdock/setup-tui` | `spf13/cobra` + `charmbracelet/huh` | 44789★ / 7241★，均在 2026 有提交 | 无 |
| S7 | 二维码 | `agentdock/qrcode` | `skip2/go-qrcode` + `mdp/qrterminal/v3` | 3019★（PNG）/ 559★（终端）；cc-connect 用 qrterminal | 无 |
| S8 | 本地状态库 | `agentdock/store-sqlite` | `modernc.org/sqlite`（纯 Go） | AgentKit 已依赖；无 cgo，交叉编译友好 | 间接（`modernc.org/sqlite` 在 go.mod） |
| S9 | 配置装载 | `agentdock/config-koanf` | `knadh/koanf/v2` | 4231★，v2.4.1，2026-11 提交，MIT | 无（AgentKit 用 yaml.v3 直读） |
| S10 | 单实例锁 | `agentdock/lock-flock` | `gofrs/flock` | 767★，v0.13.1，BSD-3 | 无 |
| S11 | 日志与轮转 | `agentdock/log-slog` | stdlib `slog` + `lmittmann/tint` + `lumberjack` | 1371★ / 5481★ | 无 |
| S12 | 重试退避 | `agentdock/retry-backoff` | `cenkalti/backoff/v5` | 4093★，v5.0.3，AgentKit 已间接依赖 | 间接 |
| S13 | 附件类型判定 | `agentdock/media-mimetype` | `gabriel-vasile/mimetype` | 2028★，2026-11 提交，MIT | 无（AgentKit 里是自己 detect） |
| S14 | 版本约束 | `agentdock/semver` | `Masterminds/semver/v3` | 1444★，2026-08 提交 | 无 |

P2（首版不做，仅登记）：桌面通知 `gen2brain/beeep`（1774★）、托盘 `fyne-io/systray`（365★，活跃；`getlantern/systray` 3728★ 但 2024-07 后无提交）、外部进程插件 `hashicorp/go-plugin`（6117★，MPL-2.0）。

## 3. S1/S2：Agent 接入与适配器目录

### 3.1 ACP 客户端（S1）

`coder/acp-go-sdk` v0.13.5，250★。★ 不高，但它是 **ACP 官方组织的 Go SDK**，且 AgentKit 已在 `plugins/agent/acpremote` 上跑通（连接、`session/new`、权限请求、steering、`session/resume` 恢复）。够准入：官方身份 + 真实依赖。

注意：版本号还是 `v0.x`，`agentkit v0.4.45` 锁在 `v0.13.5`。**AgentDock 不要直接 import 它**，只依赖 AgentKit 的 `agent/acp-remote` 插件；这样 SDK 破坏性变更由 AgentKit 承担一次。

### 3.2 适配器目录（S2）——本轮最值得做的插件

官方 registry（`agentclientprotocol/registry`，449★）发布 `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`，每个 agent 条目含：`id`、`version`、`license`、`distribution.{npx|binary|uvx}`，binary 条目按 `darwin-aarch64` / `darwin-x86_64` / `linux-x86_64` … 给出 `archive` + `cmd` + `args` + `sha256`。这正好满足"引导时自动下载并校验适配器"的需求，且 sha256 由上游签名维护。

已确认存在且与 AgentDock 目标相关的条目（版本为 registry 2026-11-06 快照）：

| registry id | 产品 | 版本 | 分发 | License | npm 月下载 | 协议矩阵能力 |
|---|---|---|---|---|---|---|
| `claude-acp` | Claude Agent | 0.101.0 | npx `@agentclientprotocol/claude-agent-acp` | 见 §1 待核实 | **3,940,278** | loadSession, session/list, fork, resume |
| `codex-acp` | Codex | 2.5.0 | npx `@agentclientprotocol/codex-acp` | Apache-2.0（NOASSERTION） | **2,765,239** | loadSession, list, fork, resume |
| `pi-acp` | pi | 0.0.34 | npx `pi-acp` | MIT | **772,164** | loadSession, session/list |
| `gemini` | Gemini CLI | 0.67.0 | npx `@google/gemini-cli --acp` | Apache-2.0 | 1,577,084 | loadSession |
| `qwen-code` | Qwen Code | 0.26.8 | npx `@qwen-code/qwen-code --acp` | Apache-2.0 | 362,989 | loadSession, list, resume |
| `opencode` | OpenCode | 2.2.5 | binary | MIT | — | loadSession, list, fork, resume |
| `kimi` | Kimi CLI | 1.52.0 | binary | MIT | — | loadSession, list, resume |
| `goose` | goose | 1.57.0 | binary | Apache-2.0 | — | loadSession, list |
| `grok-build` | Grok Build | 1.0.78 | npx `@xai-official/grok` | proprietary | — | loadSession, list, resume |
| `cursor` | Cursor | 2026.11.05 | binary（`cursor-agent acp`） | proprietary | — | loadSession, list |
| `github-copilot-cli` | GitHub Copilot | 1.0.106 | npx `@github/copilot --acp` | proprietary | — | loadSession, list |

不同 agent 的原生 CLI 体量（说明"用户可能已装"）：`anthropics/claude-code` 154,774★、`openai/codex` 133,462★、`google-gemini/gemini-cli` 108,754★、`QwenLM/qwen-code` 29,047★、`MoonshotAI/kimi-cli` 11,419★、`aaif-goose/goose` 56,299★、`anomalyco/opencode` 224,983★、`cline/cline` 71,396★。

**关键设计约束**（来自协议矩阵，非推测）：`session/stop` 在 39 个实现里 0 个支持、35 个 `method not found`。所以 AGT-02（取消与终态）**不能依赖 ACP 的 stop**，只能走客户端侧取消（AgentKit 的 turn abort）或杀子进程组。`session/fork` 只有 13 个支持、`session/resume` 17 个——这直接决定了 AGT-03（会话恢复）必须按适配器能力降级。

插件职责建议：

```yaml
agentdock.catalog.acp:
  use: agentdock/catalog-acp-registry
  config:
    indexUrl: https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json
    cacheTtl: 24h
    allowIds: [claude-acp, codex-acp, pi-acp, kimi, gemini]   # 首版白名单
    requireSha256: true
```

输出的是"可安装适配器清单"，交给 S1 的 `agent/acp-remote` 消费（`command: [npx, -y, pkg@ver]` 或本地解包路径）。**不要**让 catalog 插件自己 spawn 进程。

## 4. S3：IM 平台

首版 Lark，用官方 `larksuite/oapi-sdk-go/v3`（624★，MIT，最新 v3.12.2，**AgentKit 锁 v3.11.0**，其中已含 `scene/registration` 一键注册包）。AgentKit 已封装成 `platform/lark` 与 `platform/feishu` 两个 kind（长连接走 `ws.NewClient`，另有卡片/流式/回传等大量能力），AgentDock **直接用，不重复封装**。

候选对比：

| 库 | ★ | 状态 | 判断 |
|---|---|---|---|
| `larksuite/oapi-sdk-go` | 624 | 官方，2026-10 提交，v3.12.2 | **推荐**。含 `registration.RegisterApp`（RFC 8628 设备授权 + 二维码回调），正好对应 LARK-01 |
| `chyroc/lark` | 476 | 社区，2026-03 提交 | 备选。声称覆盖全部 OpenAPI，但长连接/卡片能力需自行验证，换代成本高于收益 |
| `slack-go/slack` | 4972 | 2026-11 提交，BSD-2 | Slack 槽位首选 |
| `go-telegram/bot` | 1875 | 2026-11 提交 | Telegram 首选；`go-telegram-bot-api`（6407★）2024-08 后无提交，不选 |
| `bwmarrin/discordgo` | 5996 | 2026-02 提交 | Discord 首选 |
| `open-dingtalk/dingtalk-stream-sdk-go` | 48 | 2026-07 提交 | 钉钉 stream 模式唯一 Go 选项但★极低，标"待验证"，不进首版 |

扩展姿态：AgentKit 平台侧已经是 `pluginkit` 的 `platform/*` kind 族，新增 IM = 新增一个 kind + 一份验证矩阵，不需要改 AgentDock 骨架。

## 5. S4：凭据存储（新选）

| 库 | ★ | 版本/时间 | License | 判断 |
|---|---|---|---|---|
| `zalando/go-keyring` | 1361 | v0.2.8 / 2026-03 | MIT | **推荐**。需求文档已定；macOS 走 `/usr/bin/security`；agent-harness 已生产使用（`go.mod` 直接依赖） |
| `99designs/keyring` | 659 | 2024-05 后无提交 | MIT | 不选。功能更全但维护停滞 |
| `keybase/go-keychain` | 673 | 2026-11 提交 | MIT | 仅 macOS，作为后端参考；不单独用 |

要点：`go-keyring` 的 macOS 后端是调用 `security(1)`，**在 LaunchAgent 身份下首次读取可能触发系统授权弹窗**，SRV-02 "实际服务身份可读凭据" 必须专门验证，不能靠前台 CLI 测通就算过（这条需求文档里已有，此处补上"为什么"）。

接口建议（AgentDock 自己的端口，不暴露 `keyring` 类型）：

```go
type Store interface {
    Get(ctx context.Context, ns, account string) (string, error)  // ErrNotFound
    Set(ctx context.Context, ns, account, secret string) error
    Delete(ctx context.Context, ns, account string) error
}
```

## 6. S5：后台服务（新选，最需要谨慎）

| 方案 | ★ | 判断 |
|---|---|---|
| 自写 launchd 薄封装（模板化 plist + `launchctl bootstrap/bootout`） | — | **首版推荐**。SRV-01/02/03 要求幂等 install、退避、单实例、无双重 daemonize，`launchctl` 语义必须精确可控，包一层反而更难调 |
| `kardianos/service` | 4853，Zlib，v1.3.0 | P1 跨平台候选（Linux systemd / Windows 服务）。注意它有自己的 install/run 语义与日志重定向，和严格 plist 控制会打架 |
| `coreos/go-systemd` | 2719，Apache-2.0 | Linux 阶段用 |

保守做法：先定义 `ServiceManager` 端口（`Install/Uninstall/Start/Stop/Restart/Status`），首版只有一个 `agentdock/service-launchd` 实现；P1 再加 `agentdock/service-kardianos`。这样"换实现"是加文件，不是改调用点。

## 7. S6/S7：引导 UI 与二维码

| 库 | ★ | 版本/时间 | 判断 |
|---|---|---|---|
| `spf13/cobra` | 44,789 | v1.10.2 / 2026-07 | **推荐** 命令骨架（`setup/status/doctor/service`） |
| `charmbracelet/huh` | 7,241 | v1.0.0 / 2026-10 | **推荐** 表单式引导（v1.0 已发布，API 稳定） |
| `charmbracelet/bubbletea` | 45,623 | 2026-11 提交 | 交互式状态页/进度用；与 huh 同栈 |
| `AlecAivazis/survey` | 4,097 | **archived**，2024-04 | 不选（归档） |
| `skip2/go-qrcode` | 3,019 | 2024-03 后无提交，MIT | 生成 PNG；稳定但久未动，功能已冻结，可接受 |
| `mdp/qrterminal/v3` | 559 | v3.2.1（v4 已有） | 终端渲染二维码；cc-connect 用同款，交互实证 |
| `yeqown/go-qrcode` | 866 | 2026-08 提交 | 需要样式定制时替换 |

`scene/registration` 的 `OnQRCode` 回调给出授权 URL，直接交给上面两个 QR 实现；不要自己拼 URL。

## 8. S8~S14：基础设施

| 槽位 | 推荐 | 备选与理由 |
|---|---|---|
| 状态库 | `modernc.org/sqlite`（纯 Go，AgentKit 已依赖，无 cgo） | `etcd-io/bbolt` 9804★ 更适合纯 KV；AgentDock 要按 task/delivery 状态查询 + 去重键，SQL 更省事。迁移用 `pressly/goose`（11,660★）或自写（首版表少，自写更可控） |
| 配置 | `knadh/koanf/v2` 4231★（分层 + env 覆盖，无全局单例） | `spf13/viper` 30,500★ 功能全但依赖重、全局状态多；AgentKit 现状是 `yaml.v3` 直读 |
| 单实例 | `gofrs/flock` 767★ BSD-3 | 或用 launchd 自身保证；两者都做（前台/服务并存场景需要真实锁） |
| 日志 | stdlib `slog` + `lmittmann/tint`（TTY 着色）+ `lumberjack`（轮转） | `uber-go/zap` 24,690★ / `rs/zerolog` 12,529★ 更快但引入自有类型；AgentKit 已用 `slog`，保持一致 |
| 重试 | `cenkalti/backoff/v5` 4093★ | `hashicorp/go-retryablehttp`（2358★，MPL-2.0）适合 HTTP 层；AgentKit 已间接依赖 backoff v5 |
| 附件判定 | `gabriel-vasile/mimetype` 2028★ | 纯 magic 检测，无 cgo，用于 IO-01 的白名单校验 |
| 版本约束 | `Masterminds/semver/v3` 1444★ | `hashicorp/go-version` 1777★ 亦可，但 semver 语义更贴适配器版本 |

## 9. 插件化落地：槽位与 kind

沿用 `pluginkit` 的"实例图 + kind 注册"模型（`init()` 只注册类型，装配由配置决定，见 `pluginkit/docs/2026-08-20-minimal-plugin-module-design.md`）。

约定：

1. **kind 前缀**：AgentKit 已占 `platform/*`、`agent/*`、`tool/*`、`credentials/*` 等。AgentDock 新增能力统一用 `agentdock/*`，避免命名撞车，也便于在配置里一眼分辨"谁提供的能力"。
2. **槽位接口归 AgentDock**：`setup.Step`、`credentials.Store`、`service.Manager`、`store.DB`、`catalog.Catalog` 都定义在 AgentDock 自己的包里，方法集尽量小。第三方库类型不出现在接口签名里。
3. **一个槽位至少两个 kind 才算插件化**：只有单一实现时先写死，别为了"插件化"造空壳——但每加第二个实现时不再改调用点。
4. **配置即装配**，用户不编辑实例图，由 setup 生成：

```yaml
agentdock:                # root
  use: agentdock/app
  config:
    installId: <ulid>
  deps:
    credentials: creds
    service: svc
    store: db
    catalog: cat
    platform: lark
    agent: claude

creds:
  use: agentdock/credentials-keyring
  config: { servicePrefix: io.ambr.agentdock }

svc:
  use: agentdock/service-launchd
  config: { label: io.ambr.agentdock.agent, keepAlive: true, throttle: 10s }

db:
  use: agentdock/store-sqlite
  config: { path: state.db }

cat:
  use: agentdock/catalog-acp-registry
  config: { indexUrl: "...registry.json", allowIds: [claude-acp, codex-acp] }

lark:
  use: platform/lark           # AgentKit 提供
  config: { appIdRef: keyring://io.ambr.agentdock/lark-app-id, ... }

claude:
  use: agent/acp-remote        # AgentKit 提供
  config:
    id: claude
    command: [npx, -y, "@agentclientprotocol/claude-agent-acp@0.101.0"]
```

`appIdRef`/`secretRef` 之类只在配置里放引用，实际值进 Keychain（SEC-01/SEC-02 已定）。

## 10. 明确不引入

| 项 | 原因 |
|---|---|
| `hashicorp/go-plugin`（外部进程插件） | 需要独立 RPC 契约与版本协商，跨进程也拿不到 `pluginkit` 的 Go 类型。P2 再议；首版"插件"= 编译进二进制的 kind |
| `traefik/yaegi`（运行期解释 Go） | 运行时加载未编译代码，与 SEC/发布可验证性冲突 |
| `AlecAivazis/survey`、`getlantern/systray`、`99designs/keyring`、`inconshreveable/go-update`、`mitchellh/gon`、`json-iterator/go`、`opencode-ai/opencode` | 已归档或长期无提交（前五个）、已被取代（opencode→`anomalyco/opencode`） |
| `mattn/go-sqlite3` | 需要 cgo，PKG-01 "不要求 Go 开发环境"和交叉编译都会被拖累 |
| 自建 IM/Agent 路由、自建记忆、模型供应商平台 | 需求文档 §4 已排除；AgentKit 已具备 |

## 11. 待验证清单（按阻塞程度排序）

1. **`claude-agent-acp` 再分发许可**：registry 标 proprietary、仓库 LICENSE 为 Apache-2.0、内嵌专有 `claude` CLI。AgentDock 是"引导用户 npm 安装/下载官方二进制"还是"自行打包"，法务结论不同。→ 决定 PKG-01 的发布形态。
2. **`codex-acp` license 归属**（NOASSERTION）。
3. **LaunchAgent 身份下 `security(1)` 读 Keychain** 是否免交互、锁屏/未解锁时的错误码。→ SRV-02 / SEC-01。
4. **`session/resume` 与 `fork` 的实际语义**在 claude-acp 0.101 / codex-acp 2.5 上的表现（矩阵只说明"支持"）。→ AGT-03。
5. **取消路径**：既然 `session/stop` 普遍不支持，验证客户端 abort + 子进程组回收能否得到确定终态。→ AGT-02。
6. **`npx -y pkg@ver` 冷启动耗时**（Claude/Codex 适配器），决定是否存在"引导期预装 + 固定版本"的必要。
7. **`larksuite/oapi-sdk-go` v3.11.0 → v3.12.2 差异**，特别是 `ws` 与 `scene/registration` 的变更；升级要回到 AgentKit 做，不在 AgentDock 单独 pin。
8. `mdp/qrterminal` v3 vs v4 API 差异；`skip2/go-qrcode` 久未维护是否需要换 `yeqown/go-qrcode`。

## 12. 来源

- ACP 协议与 registry：https://github.com/agentclientprotocol/agent-client-protocol；https://github.com/agentclientprotocol/registry ；index `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`；协议矩阵 `.protocol-matrix/latest.md`
- 适配器：https://github.com/agentclientprotocol/claude-agent-acp（v0.101.0，2026-11-05）；https://github.com/agentclientprotocol/codex-acp（v2.5.0，2026-10-29）；https://github.com/coder/acp-go-sdk
- Lark：https://github.com/larksuite/oapi-sdk-go（v3.12.2）；`scene/registration` 设备授权注册
- 凭据：https://github.com/zalando/go-keyring
- 服务：https://github.com/kardianos/service ；https://github.com/coreos/go-systemd
- 存储/配置/日志/重试/锁：`modernc.org/sqlite`、`knadh/koanf`、`lmittmann/tint`、`natefinch/lumberjack`、`cenkalti/backoff`、`gofrs/flock`
- 本仓库既有结论：`docs/requirements.md`、`docs/architecture.md`、`docs/research/2026-11-07-agent-landscape.md`
- AgentKit 现有能力（源码核对，`v0.4.45`）：`plugins/agent/acpremote`、`runtime/platform/feishu`、`runtime/platform/slack`、`plugins/credentials`、`go.mod`（`coder/acp-go-sdk v0.13.5`、`larksuite/oapi-sdk-go/v3 v3.11.0`、`modernc.org/sqlite v1.36.2`、`cenkalti/backoff/v5 v5.0.3`）