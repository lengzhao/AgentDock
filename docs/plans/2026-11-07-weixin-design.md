# 微信 ClawBot / iLink Go 平台插件

用户已确认直接实现 Go 平台插件。新增 platform/weixin，注册 platform/weixin kind；不依赖 OpenClaw 服务，不修改当前本地 Lark 安装。

边界：HTTP 协议客户端、二维码登录、私聊文本收发、带取消的长轮询与退避、游标持久化、凭据引用和扫码身份绑定。AgentKit Platform 是运行边界，继续复用 runner、ACP、会话与准入策略。不同账号通过 bot ID 和安装目录隔离。平台能力差异必须显式处理：首版不接收媒体，回复最终文本，审批通过绑定用户的明确指令；未验证的能力不能声称可用。

登录凭据进入 Store；base URL、bot ID、扫码用户 ID 为非敏感配置。登录结果使用独立平台凭据命名空间；重新登录不擅自替换已有 owner。只访问 HTTPS 官方 iLink 域，测试通过注入 HTTP client/endpoint 模拟。轮询失败不推进游标，成功消费批次后原子保存；未知发送结果不自动重发。

平台引导与 owner 解析按平台注册接口分离，Lark 逻辑保留；配置校验接受 weixin，doctor/status 不调用 Lark 接口。模拟协议、权限和装配测试通过后交付；真实扫码及 E2E 单独标明结果。

追加确认：所有平台登录通过 platform.Authenticator 插件，SaveLogin 公共层统一持久化、回读和引用生成。CLI 仅编排，Lark 公共便利 API 同样委托统一保存。

后续用户确认：同一安装保留 Lark 并追加微信、同时在线；独立 home 不作为不同平台的前置条件。实施详见 2026-11-07-multi-platform.md，替代上文同平台隔离策略中对跨平台安装的限制。
