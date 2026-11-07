# 定时任务设计

用户已批准一次性/周期任务、原会话投递、持久化与独立投递记录。复用 AgentKit cron engine/runtime 与 schedule 工具，通过 ACP session MCP 的 runtime 模式提供自然语言入口；/cron 提供明确查看/取消。

新增公开 schedule 包作为适配边界：串行化持久任务表操作，避免并发读改写丢任务；任务工具视图按平台、会话和用户过滤并重新检查当前准入，调度器使用全局视图。默认 Asia/Shanghai；持久任务只保存稳定路由和身份，不保存微信 context token。一次性任务重启时五分钟内可补触发，过期任务不补；周期任务跳过错过的边界，不积压补跑。

每次触发写任务记录；Agent 执行状态与平台投递状态分开，发送失败或结果不确定不重跑 Agent。崩溃中的执行标记 interrupted，不自动重放副作用。调度、注册表与工具都保留 PluginKit 可替换节点。ACP MCP 使用 turn-scoped runtime 绑定，防止上下文串线。微信重启后缺少 context token 时明确失败并留记录。

用户补充：对照 AgentKit 与 agent-harness，尽量注入可用 [meta]。当前启用全部内置字段及非敏感精确键，不注入原始 header/凭据；字段缺失保持省略。审计增加 loop 返回值边界，覆盖 ACP 终态不带 Failed 的执行错误。
