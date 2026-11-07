# Modular Plugins Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 移除 internal，开放模块并让应用装配可替换。
**Architecture:** 顶层公开包定义契约，plugin 注册默认实现；统一装配入口通过 PluginKit 构造契约，runtime 提供实例图覆盖。
**Tech Stack:** Go, AgentKit, PluginKit, Cobra.

### Task 1: 迁移公开包
将 * 移到顶层，更新 Go imports、文档、发布 ldflags。运行 go test ./...。
### Task 2: 应用插件装配
新增 assembly/slots.go 与 slots_test.go；先验证自定义 Store、未知 kind 和接口不匹配失败，再提供 Credentials、Service、Catalog 泛型装配封装。CLI 提供 kind 选择参数，服务操作使用 ServiceManager。
### Task 3: 运行图替换
runtime 添加 GraphWithOverrides/BuildWithOverrides，现有入口委托默认选项。测试覆盖节点替换、新增依赖、空 use 拒绝和输入不被修改。
### Task 4: 文档与检查
更新 architecture/development 及扩展示例，运行 go test ./...、go vet ./...、go build ./cmd/agentdock、git diff --check。不启动或替换用户后台服务。
