# Agent 环境变量

在 Lark 私聊中，由已绑定 owner 使用统一 `/env` 命令：

```text
/env list claude-acp
/env set claude-acp CLAUDE_CODE_AUTO_MODE_SERVER=0
/env set claude-acp ANTHROPIC_AUTH_TOKEN=<值>
/env check claude-acp ANTHROPIC_AUTH_TOKEN
/env unset claude-acp ANTHROPIC_AUTH_TOKEN
```

将 `claude-acp` 换成 `codex-acp` 可管理另一个 Agent。变量名按插件文档填写；同一个 Agent 加载的插件共享其进程环境。值中的空格、等号和引号按原样保存，不需要加 shell 引号。

所有通过 `/env set` 写入的值都存入当前用户 Keychain。只有成功写入并回读验证后，配置文件才会原子更新为 `keyring://...` 引用。每次更新使用新的凭据账户，以免配置保存失败时覆盖正在使用的旧值。

`list` 仅显示名称，`check` 只报告可读取状态，不显示值或引用账户。所有命令参数在 AgentDock 分发日志中脱敏。群聊、非 owner、尚未确认是私聊的会话均不能使用该命令。Keychain 不会删除原始 Lark 消息：通过聊天发送的值仍受 Lark 消息记录策略影响。

修改后执行：

```bash
agentdock service restart
```

正在运行的 Agent 不会热更新。启动时解析配置中的凭据引用，在内存中注入对应 Agent 子进程，不改写全局环境；显式配置覆盖登录 shell 继承的同名值。读取失败会阻止启动并指出 Agent 和变量名，不显示凭据值或底层错误内容。

`unset` 只移除该 Agent 的显式配置，保留 Keychain 旧值作为回滚数据；重启后，如果登录 shell 导出了同名变量，子进程仍会继承它。旧版非敏感字面量配置继续兼容；敏感变量必须使用凭据引用。
