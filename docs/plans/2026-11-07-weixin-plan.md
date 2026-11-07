# Weixin Platform Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 直接用 Go 实现微信 ClawBot / iLink 平台并接入 AgentDock。
**Architecture:** platform/weixin 协议客户端与 AgentKit 平台插件分层；平台 setup/owner 通过注册接口选择，密钥保存在现有 Store。
**Tech Stack:** Go net/http, AgentKit, PluginKit, httptest.

### Task 1: 协议客户端
新增 platform/weixin/client.go、client_test.go；先测试请求头、QR、长轮询、业务错误、取消和发送协议，再实现。使用腾讯公开协议作为依据，HTTP 错误不泄漏 token/响应内容。
### Task 2: 平台适配
新增 platform/weixin/platform.go、platform_test.go；先测试收发、owner 准入、游标、重复消息和明确审批回复，再实现 AgentKit Platform；注册 kind。
### Task 3: 应用集成
修改 config/config.go、runtime/assemble.go、cli/setup.go、cli/owner.go、envslash/env.go；新增平台引导契约，weixin QR 凭据与身份保存；测试 lark 不回归及 weixin 图真实装配。
### Task 4: 审查与验证
更新用户文档；go test ./...、go vet ./...、go build ./cmd/agentdock、race 定向检查、git diff --check；独立审查 spec 和代码质量。保留现有 Lark 本地服务。
