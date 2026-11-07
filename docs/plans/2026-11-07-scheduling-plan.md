# Scheduling Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** AgentDock 支持持久化一次性和周期任务，聊天管理，原平台回复。
**Architecture:** 复用 AgentKit schedule engine/runtime/tool 与 ACP session MCP runtime；schedule 适配层提供身份隔离、时区、并发保存及触发/投递审计。runtime 装配仅共享一个调度器，供所有平台使用。
**Tech Stack:** Go, AgentKit/PluginKit, SQLite state, local JSON registry.

1. schedule registry/工具：先测并发保存、隔离、时区、重启和取消，再实现串行后端和受限工具视图。
2. 审计适配：包装 schedule.Runtime 与 Platform，触发写独立 turn ID；记录 execution 与 delivery，测试失败不重发和重启中断。
3. runtime/config：添加可关闭的 schedule 配置，装配 schedule/store/scoped/tool/cron 和 ACP session MCP tools/runtime；验证单/多平台、/cron 与 MCP 上下文。
4. 文档与端到端模拟：真实 fake ACP/MCP 一次性创建触发并投递；全量/race/vet/build，独立规格/质量审查。未授权时不创建真实提醒或外发消息。
