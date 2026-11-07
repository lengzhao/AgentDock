# AgentDock 架构方案

日期：2026-11-07；状态：已存在实现，具体交付状态见 development.md。当前实现面向 macOS，支持 Lark/飞书及微信 iLink 私聊文本；微信真实账号 E2E 尚待验证。

## 1. 方案选择

可选路径：

1. **独立产品 + AgentKit 运行底座（建议）**：新项目承担引导、凭据、服务与发布，复用 AgentKit 的会话、平台和 ACP 能力。满足当前边界，但需先验证库接口与版本。
2. 在新项目内独立实现 IM 路由与 ACP：自主性高，但重复建设会话、权限、事件和恢复逻辑，首版维护成本较高。
3. 扩展/派生已有同类产品：可较快获得覆盖面，但需要接受其配置和发布体系；作为比较方案保留，不是本次采用方案。

不依赖、不复制 agent-harness 的实现。本轮调研不对其他项目做二次代码分发；后续若引入依赖，核对许可证并固定版本。

## 2. 组件职责

```text
CLI / TUI setup, status, doctor, service
                  │
           AgentDock 应用层
  引导状态 ─ 配置引用 ─ 凭据库 ─ 服务管理
                  │
           AgentKit 运行适配层
  平台 ─ 路由 ─ 会话 ─ 队列 ─ 权限 ─ 事件
      │                              │
  Lark 官方 SDK                ACP Agent 子进程
                           Claude / Codex 适配器
```

- **Setup**：步骤、校验、取消与恢复；禁止直接承担长期消息消费。
- **Config**：面向用户的最小配置、版本迁移与原子写入；内部生成 AgentKit 所需装配，不要求用户编辑实例图。
- **Credentials**：`go-keyring` 存取、命名空间、回读检查、脱敏和轮换；与非敏感配置隔离。
- **Service**：用户级 LaunchAgent、日志、单实例、启动环境和优雅退出。
- **Runtime adapter**：连接 AgentKit 可公开使用的接口；不在 AgentDock 再建另一套消息/权限循环。
- **Capabilities**：按实际适配器声明输入、审批、取消、恢复、模型选择等；无法支持时明确反馈。
- **Diagnostics**：读取统一状态，按需做探测，提供退出码和结构化报告。

Lark SDK 的高层 Channel 已声明消息归一化和流式发送能力，但是否由 AgentKit 封装必须先核实。**同一安装只允许一个 Lark 消息消费者、一套会话路由和一套重试策略。** 不同时在 AgentDock 与 AgentKit 各建一个客户端抢收事件。

## 3. 配置与凭据

建议安装数据根目录 `~/.agentdock/`，包括配置、引导进度、会话映射、任务/投递状态和轮转日志。最终目录按 macOS 安装规范确定，不能依赖源码 checkout 的路径。

配置只包含安装 ID、区域、App ID、凭据引用、Agent 绝对路径/版本、工作目录和授权身份。Keychain service 使用 AgentDock 命名空间，account 包含安装与平台连接标识，避免测试/正式或多个安装相互覆盖。

Secret、刷新令牌以及敏感恢复令牌进入 Keychain；临时 access token 尽量驻内存。Agent 自有认证仍交由 Agent 管理，不扫描复制其凭据。通过子进程环境注入必要密钥时，仅向目标子进程提供，避免持久保存完整 shell 环境。

`go-keyring` 官方说明 macOS 使用 `/usr/bin/security`，Linux 依赖 D-Bus Secret Service，Windows 有相应后端。跨平台库不等于所有后台身份都能访问凭据；首版必须验证当前用户登录/解锁和首次系统授权条件。[来源](https://raw.githubusercontent.com/zalando/go-keyring/master/README.md)

## 4. Lark 接入与所有者绑定

1. 优先调用选定发布版本中的官方注册能力，展示一次性授权链接/二维码。
2. 用户在 Lark 完成确认；取得 App ID/Secret 后先写 Keychain 并回读，再写配置引用。
3. 校验应用发布、scope、事件订阅、机器人和卡片回调条件。
4. 使用当前应用凭据查询应用 owner（`user_id_type=open_id`），未绑定时自动保存；已有绑定保持不变。查询失败时回退本机发起的 15 分钟一次性配对挑战；超时和重复使用失效。setup 完成和 run 启动均尝试自动绑定，已有安装也可执行 `agentdock access bind --auto`。
5. 完成真实收消息、Agent 执行、回复和审批闭环。
6. 服务安装后在实际 LaunchAgent 身份下重复就绪验证。

国际版 Lark 与中国版飞书必须有独立区域配置，不静默尝试其他区域。App ID/Secret 返回不代表发布、安装或权限已生效。注册接口能力来自官方 SDK 默认分支说明，实施前固定发布版本并验证。[来源](https://raw.githubusercontent.com/larksuite/oapi-sdk-go/v3_main/README.md)

## 5. 消息、任务与投递

路由身份至少包含 `installation + platform connection + tenant + chat + canonical thread`；会话中记录选定 Agent 与工作目录。用户身份用于授权，不能只以聊天显示名判断。私聊、群聊、线程和不同安装都必须隔离。

入站：身份/触发校验 → 持久去重 → 有界排队 → 会话串行 → 获取 Agent 会话 → 执行。不能假设平台事件 ID 全局唯一，去重键带连接作用域。

任务状态建议为 accepted、queued、running、awaiting_approval、completed、failed、cancelled、interrupted/unknown；结果投递另有 pending、sent、failed、unknown。Agent 完成但消息未发出时，不能把任务重新执行一遍。

流式事件节流后更新回复；最终结果独立落盘。发送采用平台明确支持的幂等参数或查询方式；无法确认超时发送结果时标记 unknown，禁止无界重复创建。状态不确定时允许用户查询/明确重试，不声称 exactly-once。

崩溃恢复：恢复空闲会话映射和未发送结果；执行中任务先核对 Agent 能力与实际状态。不能推断仍在运行，也不能自动重放命令、发布等外部副作用。

## 6. Agent 与权限

首版建议使用 AgentKit 的 ACP 客户端能力，对接固定版本的 Claude/Codex ACP 适配器。原生 SDK/app-server 仅在 ACP 缺失关键功能且验证收益明确时另设适配，不同时维护两条默认路径。

每个 Agent 记录：安装来源、二进制路径、适配器版本、运行版本、认证方式和已验证能力。适配器附带 CLI 时需明确展示，不能检测用户 CLI 后实际启动另一版本而不说明。

工具审批绑定具体请求与授权人。取消、超时、子进程退出都应撤销待审批请求。卡片点击必须验证用户、租户、会话和有效期；通过 SDK 接收不等于业务授权通过。不支持交互审批的适配器不能用自动批准冒充支持。

排队为默认运行中追加消息策略；仅适配器验证支持 steering 时再允许中途引导。请求已接收、另起一轮、工具结束、后台任务结束与当前回合完成须区分，不能按某一条成功响应提前收尾。

## 7. 服务与诊断

单个 AgentDock 用户级后台进程管理平台连接与 Agent 子进程；由 launchd 负责监督，不再自行双重 daemonize。固定可执行文件路径和工作目录，按明确列表传递必要非敏感环境变量。

服务安装与启动分开；幂等操作不能覆盖其他安装。停止时撤销审批、停止接收新任务、有限等待当前操作并回收子进程；超时后标记中断。崩溃循环退避并暴露诊断，不能刷日志无限重启。

状态至少区分：服务注册、进程存活、Keychain 可读、Lark 连接、Agent 可启动/登录、最近 E2E 结果。历史 E2E 通过要带时间，不能用旧结果表示当前健康。

## 8. 后续扩展

- OSService：LaunchAgent → Linux systemd → Windows 服务/用户启动方案；分别验证身份与凭据。
- Platform：Lark → Slack/Telegram/Discord → 钉钉/企业微信/Teams → 其他平台。
- Agent：Claude/Codex → Pi/Grok/Kimi/OpenCode/Gemini；维护版本化能力矩阵。
- 新增能力通过接口与验证复用，不为尚未交付的平台引入微服务、插件商店或动态二进制加载。

上述扩展顺序为建议，可根据实际需求调整，不扩大首版发布门槛。

## 9. 公开模块与可替换装配

不使用 internal 目录，各职责包位于模块顶层。CLI 使用 assembly.Open[T] 从已注册 PluginKit kind 构造 credentials.Store、service.ServiceManager 和 catalog.Source；默认实现由 plugin 包注册。所有服务操作只依赖 ServiceManager。

选择入口为 --credential-store（keyring / memory / 注册 kind）、--service-plugin、--catalog-plugin；服务安装保存这些启动参数。自定义插件必须编译进所运行的可执行文件，构造参数契约分别是 servicePrefix（默认 Keyring）、label（服务）和 cacheDir（目录源）。memory 仅适合测试。

runtime.GraphWithOverrides 和 BuildWithOverrides 接受按实例 ID 提供的完整节点，支持替换平台、Agent 所在 loop、会话、工作区、文件系统、遥测、命令及准入策略，也可新增依赖节点。节点的 use/config/deps 一并替换，PluginKit 在构造时验证依赖接口。默认图不变，默认所有者和去重链继续启用。替换准入策略的扩展须自行保持授权语义。

纯数据与工具包保持普通公开 Go 包；可替换的有状态依赖使用小接口。当前 setup 和 owner 查询仍针对 Lark/飞书，新增 IM 需要相应引导适配；运行图覆盖不代表该平台已获得完整产品支持。此版本不提供动态二进制加载或热替换。

## 10. 平台登录插件

平台运行插件与登录插件分离：Authenticator.Login 执行外部授权并返回统一 LoginResult；公共 SaveLogin 完成存储和回读验证，返回只含引用的 SavedLogin；CLI 原子写入配置并通过 Authenticator.Owner 解析可信身份。Lark、飞书、微信均复用该流程，credentials.Store 后端独立替换。新增平台不再扩展 CLI 登录 switch。

Weixin 实现直接使用 iLink HTTP API，通过 platform/weixin 接入同一个 AgentKit runner。一个安装可同时连接多个不同平台，经 AgentKit multiplex 进入共享 runner；同种平台的多个账号仍需独立 home 和服务 label。当前微信仅支持私聊文本；真实账号验证和媒体功能的状态见 docs/weixin.md。

## 11. 同时连接多个平台

保留 platform 主连接字段及其原状态目录，附加连接保存到 platforms 列表。setup --platform 按 kind 追加或更新，未指定 --agent 时保留 Agent 配置。所有连接共享 Agent、loop、会话底座与任务数据库；附加连接的 owner、验证 marker、游标使用独立子目录。主连接仍可读取旧凭据引用，不因新增平台而迁移。

运行装配通过 platform/multiplex 聚合，回复沿原连接路由。access 与 verify 可用 --platform 选择目标；doctor/status 遍历全部连接。setup 后需要重启已安装服务以加载新增连接。
