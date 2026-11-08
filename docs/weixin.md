# 微信 ClawBot / iLink

AgentDock 提供 Go 平台插件 `platform/weixin`，直接访问 iLink HTTPS API，不需要 OpenClaw Gateway。当前只支持私聊文本收发；媒体与群聊尚未实现。协议按腾讯公开客户端的 2.4.8 行为固定，未完成真实账号 E2E 前不能将模拟测试视为微信联通成功。

## 登录与启动

已有 Lark/飞书安装时，直接追加微信连接；原连接和 Agent 配置保持不变：

```sh
agentdock setup --platform weixin
agentdock service restart   # 已有后台服务时，重新加载全部平台
agentdock status
```

在终端显示的二维码上用微信扫码并确认；如微信要求验证码，在终端输入手机显示的代码。二维码过期、验证被封锁或账号已绑定到其他实例时，setup 会明确报错。Ctrl-C 取消网络等待。setup 成功后只追加或更新所选平台。已有服务需 restart 后加载新连接；未安装服务时执行 agentdock run。

凭据保存在所选 credentials.Store（默认系统 Keychain），使用安装 ID、平台名及随机登录批次隔离。配置保存 `platform.credentials` 引用和非敏感 `platform.settings.baseUrl`；扫码用户身份也以凭据引用保存，只有验证过的扫码身份参与自动 owner 绑定。主连接的 owner 保持原路径；附加平台的 owner 保存在 state/platforms/<kind>/access.json。每种平台当前支持一个账号，同种平台切换到其他账号仍需独立 --home。

只保存 `get_updates_buf` 游标及 bot ID（0600），不落盘 Bot token 或会话 context token。成功处理完整批次后才提交游标；授权/去重/验证沿用默认 ingress 链。发送失败不会自动重试；发送状态不确定时不要通过重放整个 Agent 任务来补发。

登录失效（iLink -14）会返回明确错误，要求再次 setup。旧凭据批次不会被自动删除，当前配置引用始终指向最后一次完整保存成功的批次。

## 审批与命令

仅被准入策略允许的私聊用户可以调用命令。`/env` 沿用 owner-only 规则，凭据仍经统一 Store 保存。

- 工具审批：按消息里的请求 ID 发送 `/approve <id>` 或 `/deny <id>`。
- 自由文本问题：`/answer <id> <回答>`。
- 回答绑定具体请求、用户、会话和有效期；一次消费。回合结束、超时或 resolved 后不再接受。
- 首版不支持选项题、多选、交互卡片或群聊审批，相关请求会明确返回不支持。

会话 context token 仅驻内存。重启后先从微信发送一条新消息，再回复或发起审批；不能把缺失上下文时的发送当成成功。

## 后台服务与多平台

一套后台服务通过 AgentKit multiplex 同时处理配置中的所有平台，复用 Agent/会话底座，按来源路由回复。全部连接写在 `platforms` 中，由 `primaryKind` 指定主连接；setup 按 kind 追加/更新，不替换其他连接。主连接状态目录不迁移；附加连接的 owner、校验 marker 和微信游标独立，任务记录共用原 state.db，并通过平台和实例区分键。

管理或验证微信连接：

```sh
agentdock access --platform weixin status
agentdock verify --platform weixin
agentdock doctor
```

已安装服务时执行 agentdock service restart。尚未安装时，用实际二进制安装：

```sh
# 使用已安装的实际二进制路径运行，勿用 go run 的临时可执行文件安装服务。
/path/to/agentdock service install
```

## 扩展登录插件

`platform.Authenticator` 定义 `Login` 和 `Owner`。登录插件只执行平台授权，返回 `LoginResult.Credentials`（`id`、`secret` 及可选平台角色）和非敏感 Settings，不接收持久凭据库。公共 `platform.SaveLogin` 负责命名空间、保存、回读校验及生成引用；CLI 负责原子保存配置与 owner 状态。

默认登录 kind 为 `platform/auth-lark`、`platform/auth-feishu`、`platform/auth-weixin`，可通过 `setup --platform-auth <registered-kind>` 替换。底层凭据库通过全局 `--credential-store` 选择，和登录插件独立。

旧 `appIdRef` / `appSecretRef` 配置继续可读，新登录统一写 credentials map。两种形式不能混用。公共 Lark RegisterApp 便利 API 也委托 SaveLogin，调用方必须传 Installation，并使用返回的 References；不再使用全局固定服务/account。

协议参考：[腾讯 iLink 协议](https://github.com/Tencent/openclaw-weixin/blob/main/docs/protocol_zh_CN.md)。该文档描述客户端行为，未承诺完整的服务端兼容契约。
