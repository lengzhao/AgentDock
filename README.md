# AgentDock

**Run your favorite agents as always-on assistants.**

English | [简体中文](README.zh-CN.md)

AgentDock aims to make existing agents easy to use from your messaging platforms. Choose an agent, connect a platform through guided setup, and keep it running in the background.

Bring the agent you already use—Claude Code, Codex, Pi, Grok, Kimi, or another supported agent—and let AgentDock handle platform connections, local service management, and credential storage.

> **Status: early development.** The first code milestone is in place: a working `agentdock` CLI (guided setup, foreground and launchd-backed background run, layered diagnostics, ACP catalog, owner binding, operational task record). There is no release package yet, and no verified end-to-end run against a real Lark tenant. Claims below mark what is implemented (`docs/development.md`) versus what is still planned.

## Why AgentDock?

Using an agent through a messaging platform often means wiring together a CLI, platform credentials, configuration files, and a background service. AgentDock aims to turn that work into one guided setup.

- **Use existing agents.** Reuse their execution capabilities and supported authentication flows.
- **Connect messaging platforms.** Set up platform access, validate credentials, and verify the connection with a test message.
- **Keep running.** Manage a local background service with startup, shutdown, restart, and status inspection.
- **Store secrets in the system credential store.** Keep API keys, app secrets, and tokens out of ordinary configuration files.
- **Diagnose setup problems.** Explain missing dependencies, expired credentials, and connection failures with actionable recovery steps.

## Intended setup experience

1. Install AgentDock.
2. Choose an agent; detect its installation and complete any required login.
3. Choose a messaging platform; follow the authorization or application setup flow.
4. Store credentials securely and validate the connection.
5. Enable the background service and send a test message.

The goal is a working assistant without requiring users to assemble runtime configuration by hand. Guided setup will still surface any required platform permissions, administrator approval, or agent login.

## Architecture

```text
Messaging platforms
        │
        ▼
AgentDock
  Guided setup · Credentials · Service lifecycle · Diagnostics
        │
        ▼
AgentKit
  Platform integration · Sessions · Routing · Agent adapters
        │
        ▼
Existing agents
  Claude Code · Codex · Pi · Grok · Kimi · …
```

AgentDock is a standalone product built on [AgentKit](https://github.com/lengzhao/agentkit). AgentKit provides reusable runtime capabilities; AgentDock owns the installation and day-to-day operating experience. AgentDock does not depend on agent-harness.

Agent adapters should prefer ACP where supported. Other integration mechanisms will be evaluated per agent. Streaming output, permission requests, cancellation, and session recovery depend on each adapter and will be documented explicitly. The agents listed above are integration targets, not a compatibility guarantee.

## Platform onboarding

Platform setup should automate the steps that a platform officially supports:

- Use authorization flows or application provisioning APIs where available.
- Guide users through application creation and permission approval when manual steps are required.
- Obtain credentials through the supported flow, store them securely, and verify access.
- Check connection readiness before reporting setup as complete.

Automatic credential acquisition is a goal where platform capabilities allow it; it does not imply every platform exposes an API to create apps or retrieve secrets.

## Credentials and background services

The planned credential backend is [`zalando/go-keyring`](https://github.com/zalando/go-keyring), using the operating system credential store. Configuration files should contain credential references rather than secret values. Logs and diagnostic output must redact sensitive values.

Existing agents should retain ownership of their authentication where possible. AgentDock should reuse supported agent login flows rather than copy their credentials into a separate store.

Background operation must be tested under the actual service identity, including executable discovery and credential access. User-login requirements, locked credential stores, and headless Linux environments need explicit handling; unattended operation will only be claimed for verified configurations. A missing or locked credential store must produce an actionable error rather than silently save secrets in plaintext.

## Roadmap

- [ ] Define the first supported operating system, messaging platform, and agent adapters.
- [ ] Deliver one complete setup-to-test-message flow.
- [ ] Add system credential storage and credential validation.
- [ ] Add background service installation and lifecycle management.
- [ ] Verify restart behavior and document session recovery limits.
- [ ] Provide status inspection, diagnostics, and credential repair flows.
- [ ] Publish installation packages and an upgrade path.
- [ ] Expand the verified agent and platform compatibility matrix.

The first milestone prioritizes a small, verified set of combinations before expanding support.

## Contributing

The project is in its design phase. Issues discussing concrete setup workflows, agent integration requirements, and platform onboarding limitations are welcome. Please distinguish proposed behavior from verified upstream capabilities, and never include credentials in issues or logs.

## License

[MIT](LICENSE)

## Weixin ClawBot / iLink

A native Go platform plugin and shared authentication plugins support private text conversations. Live account verification still requires QR login. See [Weixin setup and extension guide](docs/weixin.md) for usage and current limitations.
