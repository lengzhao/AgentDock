// Package envslash owns the per-agent /env command. Values live in Keychain;
// the configuration contains references only. Changes take effect after restart.
package envslash

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/lengzhao/AgentDock/internal/access"
	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/credentials"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/agentkit/runtime/rctx"
	"github.com/lengzhao/pluginkit"
)

const Kind = "agentdock/env"
const help = "/env list <agent>\n/env set <agent> KEY=VALUE\n/env check <agent> KEY\n/env unset <agent> KEY\n仅 owner 私聊可用；所有值存入 Keychain，不回显。修改后重启 AgentDock 生效。"

type Config struct {
	Path       string `json:"path"`
	AccessPath string `json:"accessPath"`
	Platform   string `json:"platform"`
}
type Deps struct {
	Store credentials.Store `json:"store"`
	Inner ingress.Policy    `json:"inner,omitempty"`
}
type Provider struct {
	cfg     Config
	deps    Deps
	private sync.Map // trusted ingress facts only; no message bodies or values
	mu      sync.Mutex
}

func init() { pluginkit.Register(Kind, New) }
func New(cfg Config, deps Deps) (*Provider, error) {
	if !filepath.IsAbs(cfg.Path) || !filepath.IsAbs(cfg.AccessPath) || deps.Store == nil || (cfg.Platform != "lark" && cfg.Platform != "feishu") {
		return nil, errors.New("env command requires local config, access state, platform and credential store")
	}
	return &Provider{cfg: cfg, deps: deps}, nil
}
func (*Provider) InitApp(context.Context) error { return nil }

// UseCredentialStore binds the command to the runtime resolver's backend.
// Call during assembly, before the runner starts accepting messages.
func (p *Provider) UseCredentialStore(store credentials.Store) error {
	if store == nil {
		return errors.New("env command credential store is unavailable")
	}
	p.deps.Store = store
	return nil
}
func (p *Provider) Commands() []agentkit.Command    { return []agentkit.Command{&command{p: p}} }
func privacyKey(platform, user, chat string) string { return platform + "\x00" + user + "\x00" + chat }
func (p *Provider) Evaluate(ctx context.Context, in ingress.Input) (ingress.Decision, error) {
	var decision ingress.Decision
	if p.deps.Inner != nil {
		var err error
		decision, err = p.deps.Inner.Evaluate(ctx, in)
		if err != nil || decision.Reject {
			return decision, err
		}
	}
	if in.Kind == ingress.Message && in.Platform == p.cfg.Platform && in.ChatID != "" && in.UserID != "" {
		p.private.Store(privacyKey(in.Platform, in.UserID, in.ChatID), in.ConversationKind == "direct")
	}
	return decision, nil
}

type command struct{ p *Provider }

func (*command) Name() string  { return "env" }
func (*command) Alias() string { return "" }
func (*command) Description() string {
	return "管理 Agent 环境变量：list | set | check | unset（Keychain 存储）"
}
func (*command) SanitizeArgsForLog(string) string { return "[REDACTED]" }
func (c *command) authorize(ctx context.Context) error {
	platform, user := rctx.PlatformFromContext(ctx), rctx.UserIDFromContext(ctx)
	owner, err := access.NewStore(c.p.cfg.AccessPath).Owner()
	if err != nil || owner == "" || user != owner || platform != c.p.cfg.Platform {
		return agentkit.ErrCommandForbidden
	}
	target, ok := rctx.DecodeSessionRoute(rctx.EnvelopeFromContext(ctx).Route)
	if !ok {
		return agentkit.ErrCommandForbidden
	}
	private, ok := c.p.private.Load(privacyKey(platform, user, target.ChannelID))
	if !ok || private != true {
		return agentkit.ErrCommandForbidden
	}
	return nil
}
func takeToken(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\r\n")
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i], strings.TrimLeft(s[i:], " \t\r\n")
	}
	return s, ""
}
func (c *command) CommandExec(ctx context.Context, args string) (string, error) {
	if err := c.authorize(ctx); err != nil {
		return "", err
	}
	sub, rest := takeToken(args)
	if sub == "" || sub == "help" {
		return help, nil
	}
	id, rest := takeToken(rest)
	if id == "" {
		return help, nil
	}
	key, value := "", ""
	switch sub {
	case "list":
		if rest != "" {
			return help, nil
		}
	case "set":
		var ok bool
		key, value, ok = strings.Cut(rest, "=")
		if !ok || !credentials.ValidEnvName(key) || value == "" || len(value) > 16384 || strings.ContainsRune(value, 0) {
			return "变量名必须合法，值不能为空、超过 16 KiB 或包含 NUL。", nil
		}
	case "check", "unset":
		key = rest
		if !credentials.ValidEnvName(key) {
			return help, nil
		}
	default:
		return help, nil
	}
	p := c.p
	p.mu.Lock()
	defer p.mu.Unlock()
	lock, err := os.OpenFile(p.cfg.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", errors.New("无法锁定配置文件")
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return "", errors.New("无法锁定配置文件")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(p.cfg.Path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("配置必须是现有普通文件")
	}
	original, err := os.ReadFile(p.cfg.Path)
	if err != nil {
		return "", errors.New("无法读取配置文件")
	}
	cfg, err := config.Load(p.cfg.Path)
	if err != nil {
		return "", errors.New("配置无效，请在本机运行 config validate")
	}
	index := -1
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return "未找到该 Agent。", nil
	}
	agent := &cfg.Agents[index]
	switch sub {
	case "list":
		keys := make([]string, 0, len(agent.Env))
		for k := range agent.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return "该 Agent 尚未配置环境变量。", nil
		}
		return "环境变量（仅名称）：\n" + strings.Join(keys, "\n"), nil
	case "check":
		ref, ok := agent.Env[key]
		if !ok {
			return "未配置该变量。", nil
		}
		if strings.HasPrefix(ref, "keyring://") || strings.HasPrefix(ref, "env:") {
			resolver := credentials.NewResolver(p.deps.Store, os.LookupEnv)
			value, err := resolver.Resolve(ctx, ref)
			if err != nil || value == "" {
				return "变量不可读取，请检查引用及 Keychain 解锁和授权状态。", nil
			}
		}
		return "变量已配置且可读取（不显示值）。", nil
	case "set":
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return "", errors.New("无法生成凭据账户")
		}
		// A fresh account preserves the active value if config save fails. Never
		// write through an arbitrary preexisting reference to another credential.
		scope := sha256.Sum256([]byte(p.cfg.Path + "\x00" + cfg.InstallID + "\x00" + id))
		service := "agent-env-" + hex.EncodeToString(scope[:8])
		account := key + "-" + hex.EncodeToString(buf)
		if err := p.deps.Store.Set(ctx, service, account, value); err != nil {
			return "", errors.New("Keychain 写入失败，配置未修改")
		}
		got, err := p.deps.Store.Get(ctx, service, account)
		if err != nil || got != value {
			return "", errors.New("Keychain 回读校验失败，配置未修改")
		}
		if agent.Env == nil {
			agent.Env = map[string]string{}
		}
		agent.Env[key] = credentials.KeyringRef(service, account).String()
	case "unset":
		if _, ok := agent.Env[key]; !ok {
			return "未配置该变量。", nil
		}
		delete(agent.Env, key)
	}
	current, err := os.ReadFile(p.cfg.Path)
	if err != nil || !bytes.Equal(original, current) {
		return "", errors.New("配置已被其他操作修改，请重试")
	}
	if err := config.Save(p.cfg.Path, cfg); err != nil {
		return "", errors.New("配置保存失败；原配置保留，请检查本机文件权限")
	}
	if sub == "unset" {
		return "已移除该 Agent 的变量配置，Keychain 旧值保留。重启 AgentDock 后生效；若 shell 中仍有同名变量，会重新继承。", nil
	}
	return "已存入 Keychain，配置仅保留引用。重启 AgentDock 后生效。", nil
}

var _ agentkit.AppInitializer = (*Provider)(nil)
var _ agentkit.CommandProvider = (*Provider)(nil)
var _ ingress.Policy = (*Provider)(nil)
var _ agentkit.CommandLogSanitizer = (*command)(nil)
