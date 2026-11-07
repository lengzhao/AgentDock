# 同类产品与 Agent 能力调研

调研日期：2026-11-07。范围：官方文档、维护者仓库 README 和协议说明。未进行产品安装、真实账号授权、性能测试或安全审计。

## 1. 结论

“已有 Agent + 多 IM + 后台运行”已经存在直接同类产品，不能单独作为 AgentDock 的独特优势。尤其 cc-connect 与本项目目标高度重合，OpenClaw 也已声明可替换模型和 Agent harness。

建议 AgentDock 的产品重点是：**让用户通过可恢复的引导，把已有 Agent 连接到 IM，并能判断它是否真正可用。** 竞争力需要用安装成功、服务身份下凭据可读、任务终态明确、重启后状态准确和修复步骤清晰来验证；这些是拟投入的方向，尚不是相对其他产品已证实的优势。

首版 macOS + Lark 可以验证这一完整体验。通用接口仍应支持未来扩展多个 Agent、IM 和操作系统。

## 2. 同类产品比较

以下“优势”为从上游设计判断的适用点，“代价/局限”为对 AgentDock 目标的适配判断，不是跑分或缺陷结论。

| 项目 | 基本功能 / 上游声明 | 优势 / 借鉴点 | 代价、局限或待验证 | 对 AgentDock 的意义 |
|---|---|---|---|---|
| [cc-connect](https://github.com/chenhg5/cc-connect) | 本地 Agent 接入多 IM；ACP；会话、模型和权限命令；定时、附件回传、多项目 | 与目标最接近；已有 Go 实现；按平台公开能力差异 | 组合多，版本和平台差异需要持续验证；本轮未核实 keyring 与完整引导的实现，不能断言没有 | 首要对标对象；避免只重复“多 Agent、多 IM” |
| [OpenClaw](https://github.com/openclaw/openclaw) | Gateway、安装引导、daemon、渠道、控制 UI、工具、技能；声明模型和 harness 可替换 | 完整的安装到运维体验；配对机制；控制面与执行能力分工 | 产品面广，依赖与配置范围大；选择它意味着接受更完整的平台体系 | 借鉴 onboarding、doctor、渠道状态与版本化恢复 |
| [Hermes Agent](https://github.com/NousResearch/hermes-agent) | 多 IM Gateway、长期记忆、技能学习、定时任务、多种执行后端 | 长期助手能力完整；任务主动触达 | 自有 Agent 体系较重；直接复用用户多个现成 CLI 并非 README 的核心定位 | 记忆和自动化可后续做，不作为首版前置 |
| [nanobot](https://github.com/HKUDS/nanobot) | Python 自托管助手；WebUI、IM、模型接入、MCP、记忆和自动化 | 核心可读、模型选择灵活、用户引导完整 | 偏自有运行框架；接入现成 CLI 需另行确认；README 明确区分源码与发行版 | 借鉴小核心及发布版/源码能力分层 |
| [NanoClaw](https://github.com/nanocoai/nanoclaw) | Agent 容器运行、消息平台、记忆和定时；默认 Claude Agent SDK；其他 Provider 按技能安装 | 隔离边界具体；安装流程脚本化；Slack 应用配置自动化 | 容器增加依赖；定制依赖代码/技能变更；其他 Agent 路径不能按默认路径等同评价 | 评估可选隔离；避免首版强制引入容器 |
| [LangBot](https://github.com/langbot-app/LangBot) | 多 IM、模型/工作流集成、RAG、插件、Web 管理与监控 | IM 覆盖广，管理面完整 | 更接近机器人开发平台；现成 coding CLI 托管不是其 README 首要定位 | 借鉴平台插件边界和配置可视化 |
| [acpx](https://github.com/openclaw/acpx) | ACP 无界面客户端；持久会话、权限和机器可读输出 | 统一 Agent 操作语义，协议适配值得参考 | 是协议客户端，不是完整 IM 产品；上游标注 pre-1.0，接口仍演进 | 用于验证协议行为；不必为首版增加额外运行进程 |

来源：[cc-connect README](https://raw.githubusercontent.com/chenhg5/cc-connect/main/README.md)、[OpenClaw README](https://raw.githubusercontent.com/openclaw/openclaw/main/README.md)、[Hermes README](https://raw.githubusercontent.com/NousResearch/hermes-agent/main/README.md)、[nanobot README](https://raw.githubusercontent.com/HKUDS/nanobot/main/README.md)、[NanoClaw README](https://raw.githubusercontent.com/nanocoai/nanoclaw/main/README.md)、[LangBot README](https://raw.githubusercontent.com/langbot-app/LangBot/master/README.md)、[acpx README](https://raw.githubusercontent.com/openclaw/acpx/main/README.md)。每个项目的描述均为上游声明，未实测。

### 不应据此做出的结论

- 不比较模型智能或宣称某 Agent 编程能力最强；需要相同任务、权限与模型条件下测试。
- 不以仓库星数、宣传的代码行数代表稳定性或维护成本。
- 不把竞争项目 README 中对其他产品的安全评价当作独立审计结果。
- 不因文档未提到某能力就认定该能力不存在。

## 3. 已有 Agent 的接入能力

共性是工具执行、工作目录和会话；差异主要在协议、登录、权限请求、终态和恢复。TUI 支持某能力，不等于无界面接口或 ACP 适配器也完整暴露。

| Agent | 已核实的程序化入口 | 可复用能力 | 接入优势 | 代价 / 待验证 |
|---|---|---|---|---|
| Claude Code | `claude -p`；结构化输出；Claude Agent SDK；`claude-agent-acp` 适配器 | 文件/命令、工具、会话、权限交互；具体取决于 SDK/适配器 | 可复用成熟执行体系，ACP 便于统一 | SDK、CLI 与 ACP 是不同层；登录、配置加载与权限行为要按选定版本验证 |
| Codex | 原生 app-server；`codex-acp` 将 ACP 转换为 app-server | 流式事件、会话、审批、账号状态 | 原生结构化控制接口，适合深度集成 | 适配器可能带自己的 Codex；必须明确所用二进制与版本；扩展不都属于 ACP 基线 |
| Pi | 原生 `pi --mode rpc`、JSON 模式、TypeScript SDK；ACP 需另选适配器 | 流式事件、工作目录、会话与扩展 | RPC 适合 Go 父进程；核心可定制 | RPC 接收成功不等于执行完成；上游要求按稳定终态判断；不要假设统一审批语义 |
| Grok Build | 原生 `grok agent stdio` ACP；`grok -p` 和流式 JSON | 工具、会话、流式输出；文档提供已登录或 API Key 路径 | 有直接 ACP 入口 | ACP 扩展、终态、账号可用性和后台任务需测；不能把 API 模型服务当作 CLI 支持 |
| Kimi Code CLI | 原生 `kimi acp`；README 声明复用已有登录 | 文件/命令、会话、MCP、插件与子 Agent | 无需另外安装 ACP 转换层 | 老 `kimi-cli` 仓库已归档；应以 `kimi-code` 为对象锁定版本，核对权限矩阵 |
| OpenCode | 原生 `opencode acp` | ACP 会话、工具及权限等能力按版本核对 | 原生 ACP，便于加入统一目录 | Provider 配置、登录与恢复行为需要实测 |
| Gemini CLI | 官方提供 ACP 开发文档 | 工具、模型与 IDE 集成 | 可作为后续原生协议扩展目标 | 入口、实验标记、认证与支持矩阵在下一轮核实，不把 IDE 集成说明直接当作生产兼容 |

主要依据：

- [Claude 程序化运行](https://code.claude.com/docs/en/headless)、[Claude ACP 维护者 README](https://raw.githubusercontent.com/agentclientprotocol/claude-agent-acp/main/README.md)。其中 bare 模式会跳过已有 OAuth/Keychain 和若干配置加载；不能为追求启动速度无条件启用。
- [Codex App Server 官方文档](https://learn.chatgpt.com/docs/app-server)、[Codex ACP README](https://raw.githubusercontent.com/agentclientprotocol/codex-acp/main/README.md)。使用 app-server 与使用模型 API 是不同集成路径。
- [Pi README](https://raw.githubusercontent.com/badlogic/pi-mono/main/packages/coding-agent/README.md)、[Pi RPC](https://raw.githubusercontent.com/badlogic/pi-mono/main/packages/coding-agent/docs/rpc.md)。当前 RPC 文档区分 prompt 接收、`agent_end` 与 `agent_settled`；发布版本必须重新确认。
- [Grok Headless / ACP 官方文档](https://docs.x.ai/build/cli/headless-scripting)。
- [Kimi Code README](https://raw.githubusercontent.com/MoonshotAI/kimi-code/main/README.md)、[旧仓库归档说明](https://github.com/MoonshotAI/kimi-cli)。
- [OpenCode ACP 官方文档](https://opencode.ai/docs/acp/)、[Gemini CLI IDE 文档](https://geminicli.com/docs/ide-integration/)。

建议首批验证 Claude Code 和 Codex：它们覆盖 SDK 转 ACP、app-server 转 ACP 两种常见路径。Pi、Grok、Kimi 保持在明确的后续接入列表；如首批登录或协议验证失败，应记录阻塞原因再决定替换，不直接宣称兼容。

## 4. 主流 IM 的接入规划

下表是接入方向，不是 AgentDock 支持矩阵。未获得官方页面正文的条目明确留待核实。

| 平台 | 接入方向 | 引导和凭据特点 | 规划 |
|---|---|---|---|
| Lark | 官方 SDK 的 WebSocket/Channel | SDK 默认分支声明扫码授权注册应用并返回 ID/Secret；国际站适用性、发布版和权限需实测 | 首版 |
| 飞书 | 与 Lark 共用平台抽象，区域独立 | 不以“同 SDK”推断同域名、同租户或同发布流程 | 后续单独认证 |
| Slack | 官方 Socket Mode；Manifest | 官方允许 Manifest 创建应用，但需要配置令牌；Socket Mode 仍需应用令牌和安装授权 | 第一扩展批 |
| Telegram | Bot API，优先长轮询 | 标准 BotFather 创建流程；现有 Managed Bots 能力另评估，不将 token 自动获取写成全平台承诺 | 第一扩展批 |
| Discord | Gateway + REST | Bot Token、安装权限和 Message Content Intent 条件需验证 | 第一扩展批 |
| 钉钉 | Stream 作为候选 | 本轮官方概述正文抓取失败；按官方 SDK 和真实机器人验证后确定 | 第二扩展批 |
| 企业微信 | 智能机器人长连接 / 应用回调分别评估 | 官方长连接页面本轮无法读取；不要沿用“一律需要公网”的旧假设 | 第二扩展批 |
| Teams | 官方 Agent/Bot 接入 | 租户、应用安装、权限和网络要求需要专项验证 | 第二扩展批 |
| LINE | Messaging API Webhook | 官方文档明确接收 webhook 并验签；需要可访问的回调基础设施 | 第三扩展批 |
| WhatsApp | 优先评估官方 Business 接口 | 业务账号、回调、会话窗口和费用条件待专项核实；本轮官方页面 429 | 第三扩展批 |
| Matrix | 评估客户端同步与机器人账号 | 加密房间、密钥恢复需单独验证 | 第三扩展批 |
| 微信个人号、QQ、Signal、iMessage | 分别评估官方、设备桥接或社区协议 | 不把所有路径归为同一可靠性等级；标注账号、设备和平台限制 | 扩展候选 |

来源：[Lark SDK README](https://raw.githubusercontent.com/larksuite/oapi-sdk-go/v3_main/README.md)、[Lark Channel](https://raw.githubusercontent.com/larksuite/oapi-sdk-go/v3_main/doc/channel.md)、[Slack Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/)、[Slack Manifest](https://api.slack.com/reference/manifests)、[Telegram Bot Features](https://core.telegram.org/bots/features)、[Telegram Bot API](https://core.telegram.org/bots/api)、[Discord Gateway](https://github.com/discord/discord-api-docs/blob/main/developers/events/gateway.mdx)、[LINE Webhook](https://developers.line.biz/en/docs/messaging-api/receiving-messages/)、[Teams 官方入口](https://learn.microsoft.com/en-us/microsoftteams/platform/)。其他行仅为规划候选，不能引用同类产品支持列表作为官方协议可用性证明。

## 5. Lark 自动获取凭据的关键发现

[官方 Go SDK README](https://raw.githubusercontent.com/larksuite/oapi-sdk-go/v3_main/README.md) 当前说明 `registration.RegisterApp` 使用 Device Authorization Grant：用户打开验证链接或扫码确认后，创建应用并返回 App ID、App Secret；文档也提到权限、事件、回调增量和已有应用更新。

这意味着应优先验证官方注册流程，而非默认让用户复制 Secret。需要核实：SDK 发布 tag、Lark 国际站域名与租户适用性、扫码身份、管理员授权、应用发布、卡片回调、权限模板，以及取消/超时/创建结果不确定后的恢复。**扫码成功、Secret 已返回、机器人可收发消息是三个不同状态。**

验证失败时保留“接入已有应用”方式，记录具体限制。禁止在首次注册后超时就盲目重建应用，也不把 Secret 或带授权能力的临时 URL 写入诊断包。

## 6. 通用能力分工

| 能力 | AgentDock 必须承担 | 尽量由已有 Agent 承担 |
|---|---|---|
| 安装与登录 | 检测、安装引导、版本管理、健康验证 | 原生登录与账号凭据维护 |
| 消息与会话 | IM 身份、线程路由、去重、排队、结果投递 | 推理上下文、Agent 原生会话 |
| 工具与权限 | 将请求准确呈现给有权用户，防止跨会话误批 | 工具执行与原生沙箱/权限执行 |
| 长期运行 | 服务生命周期、连接恢复、准确状态 | Agent 子进程内的任务执行 |
| 技能与 MCP | 呈现能力及明确的配置入口 | 原生技能、工具和 MCP 加载 |
| 记忆与定时 | 后续按场景扩展，避免首版重复建设 | 优先保留 Agent 已有能力 |

## 7. 竞争验证与命名

建议在实现前，用同一台 Mac、同一个 Lark 租户、相同 Agent 版本，对 AgentDock 候选流程与 cc-connect 记录：首次配置的人工操作数、到首条成功回复的耗时、断网恢复、服务重启、凭据过期和失败修复步骤。账号审批等待时间单列，不伪装成软件性能差异。本轮未做该对比，暂无定量优势结论。

GitHub 已存在 [AgentDock/AgentDock](https://github.com/AgentDock/AgentDock) 和 [AgentDock 组织](https://github.com/AgentDock)。即使方向不同，也存在搜索与品牌混淆。保留当前工作目录与名称，发布包名、域名和对外品牌前再确认；这不是商标法律结论。

## 8. 证据缺口

本轮 Lark 官方长连接页面、企业微信长连接页面无法读取，钉钉概述返回 502，WhatsApp 官方页面返回 429，Teams 旧深链重定向至平台首页。没有把这些失败页面当作已核实依据。Lark 使用维护者 SDK 正文补充了证据；其余平台留到对应验证阶段。

所有首版能力仍需锁定依赖版本并真实验证；当前没有 E2E 通过、服务稳定性或跨平台兼容性的实测结论。
