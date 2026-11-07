# Cursor Agent

AgentDock 使用 Cursor CLI 原生 ACP，不需要第三方适配器。优先选择本机 cursor-agent，写入绝对路径及 acp 参数；若仅存在 agent 别名，先检查其 ACP help 确认它属于 Cursor。不会使用 ACP registry 解压目录中的相对路径当作已安装程序。

## 添加到已有安装

```sh
agent login
agentdock agents add cursor
agentdock service restart
```

该操作保留其他 Agent、默认 Agent、飞书/微信连接及凭据。重复添加不生成副本；需要设为默认时，使用 `agentdock agents add cursor --default`。可在聊天中通过 `/agent use cursor` 选择 Cursor。

新安装也可通过 `agentdock setup --agent cursor --platform lark` 选择；兼容 cursor-acp 别名，保存的规范 ID 是 cursor。

认证仍由 Cursor 自己管理。AgentDock 配置只保存 `authMethod: cursor_login`；可以使用既有 /env 机制为 Cursor 设置 CURSOR_API_KEY/CURSOR_AUTH_TOKEN 引用，不应把令牌写进启动 argv。

```yaml
agents:
  - id: cursor
    command: [/absolute/path/to/cursor-agent, acp]
    authMethod: cursor_login
    cwd: work
```

Cursor 复用现有 ACP 执行、标准 permission 请求、MCP 定时工具和 [meta] 注入。是否支持会话恢复、配置选项、HTTP MCP，以安装版本的 initialize 能力为准。Cursor 专属 cursor/ask_question、cursor/create_plan 等扩展尚未在本项目适配，不宣称与 Cursor 完整 UI 等价。

本轮对本机 CLI 做了只读 initialize 探测，确认 ACP v1、HTTP/SSE MCP、loadSession 和 cursor_login；未调用真实账号模型。fake Cursor ACP 子进程测试覆盖 cursor_login 认证、会话创建、HTTP MCP 创建定时器以及触发结果投递。

来源：[Cursor 官方 ACP](https://prod.cursor.com/docs/cli/acp)、[ACP registry Cursor 条目](https://github.com/agentclientprotocol/registry/blob/main/cursor/agent.json)。
