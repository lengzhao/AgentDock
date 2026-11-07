# 定时任务与上下文

AgentDock 通过 AgentKit 的 cron 运行时和 ACP session MCP 工具提供定时能力；所有平台共用一个持久任务表。Claude/Codex ACP 适配器必须支持 HTTP MCP，才能在自然语言对话中调用 schedule 工具。没有该能力时不能声称任务已保存。

## 聊天使用

- “10 分钟后提醒我查看构建结果” → 一次性 delay。
- “明早 9 点检查项目状态并报告” → 一次性 at。
- “每周一早上 9 点整理本周待办” → cron。
- `/cron` 或 `/cron list`：当前平台、聊天/话题和用户的未完成计划。
- `/cron list all`：包含已经领取/触发的一次性任务。
- `/cron remove <id>`：删除计划，停止后续触发；不终止已经领取或正在执行的回合。

模型通过 schedule 工具创建并读取 ID 后才算安排成功；文字答应“稍后提醒”不等于已经创建。定时任务记录创建者、选定 Agent、原会话路由和工作区；执行采用独立回合，输出沿原平台/会话返回。聊天列表与删除操作重新检查准入，只能访问自身作用域的计划；触发前也重新检查授权。

## 默认配置

旧配置无需手动新增字段，默认启用：

```yaml
schedule:
  timezone: Asia/Shanghai
  pollSeconds: 1
  maxJobs: 32
  missedGraceSeconds: 300
  disabled: false
```

无偏移的绝对时间按配置时区解释；明确的 RFC3339 偏移保持原意。cron 按配置时区计算，不修改进程全局时区。每个用户在每个平台/聊天/话题最多有 32 个活跃计划，持久表原子检查限额。任务保存在 `<stateDir>/schedules.json`（0600），原子替换并加进程/文件锁。

可通过 PluginKit 覆盖 schedule.engine、schedule.store、schedule.scoped、schedule.tool、schedule.runtime 等节点替换实现。ACP 只暴露定时工具，并使用每回合绑定的 MCP runtime 还原身份、路由、Agent 和工作区；不依赖模型传入这些字段。

## 重启与失败

- 一次性任务先持久领取再提交，领取后不自动重放，避免崩溃后重复执行外部副作用。
- 重启时尚未领取的一次任务，延迟不超过 5 分钟可触发；更早的过期任务跳过。
- 周期任务推进到本次检查时间，不逐条回放离线期间积累的历史边界。
- 崩溃中的执行由启动恢复标记为 interrupted；一次性计划的 fired 代表已领取/消费，不代表执行或投递成功。
- 执行状态和投递状态分开保存。`agentdock tasks` / `agentdock tasks --json` 可以查看任务及投递记录。
- 返回失败或发送状态不确定时保留 failed/unknown，不通过重跑 Agent 或盲目重发来补偿。
- 微信 context token 仅驻内存。重启后没有新私聊消息、或微信登录失效时，定时任务的投递会失败并保留记录；先发送新消息/重新登录才能恢复后续投递。

## [meta] 上下文

对照 AgentKit v0.4.45 和 agent-harness 后，启用所有 11 个内置字段：sender_id、sender_name、sender_email、mentions、platform、chat_id、thread_id、timestamp、task_id、trace_id、language。timestamp 同时带 timezone，默认采用配置的上海时区。

另外允许精确的非敏感元数据键：chat_type、message_timestamp、sessionScope、project、cwd、workspace、session、session_id、conversation_id、agent、agent_id。仅上游确实提供值时注入，未知姓名/邮箱/线程等不伪造。微信已知私聊类型和消息创建时间会补入元数据。

不使用 metadata 通配符，不注入 token、header、凭据或完整环境。ctx:* 是 MCP 绑定配置中的上下文来源语法，不是 runner [meta] 注入语法。结构化上下文仍由实际执行绑定维护，文字 [meta] 不能作为工具授权依据。

当前通过本地 fake ACP 子进程 → 实际 HTTP MCP 创建 → 调度触发 → ACP 输出 → 原路由模拟投递验证成功与失败路径；真实 Claude/Codex 账号和微信平台 E2E 未在此轮执行。
