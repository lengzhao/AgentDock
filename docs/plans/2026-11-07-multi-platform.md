# Multi-platform Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** setup --platform weixin 在现有 Lark 安装追加微信并同时运行。
**Architecture:** 配置使用 `platforms` + `primaryKind`；Connections 遍历全部连接，AgentKit multiplex 装配。主连接 owner/state 路径保持不变，附加连接 owner/marker/cursor 独立，任务记录共用原数据库。
**Tech Stack:** Go, AgentKit multiplex, PluginKit, Cobra.

1. config: 测试追加平台验证、去重、序列化、状态目录隔离，再实现 Connections / WithPlatform / StatePaths。
2. runtime: 测试两平台图与真实 runner 构造，再合并各连接的平台/命令/准入节点和 multiplex；共享 loop/session/Agent，注入各连接的凭据。
3. CLI: setup 无 --agent 时保留已有 Agent，按 kind 追加/更新连接；owner/run/status/doctor/verify/access 使用对应连接和状态。测试 Lark 不变、新连接成功与失败不写配置。
4. 文档、全量/race/vet/build 验证及独立审查；更新用户实际调用的本地二进制，保留配置，扫码由用户完成。
