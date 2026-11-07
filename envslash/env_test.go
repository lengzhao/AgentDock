package envslash

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	rtcommand "github.com/lengzhao/agentkit/runtime/command"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/agentkit/runtime/rctx"
)

func TestPlatformSlashDispatchRedactsValues(t *testing.T) {
	p, _, ctx := fixture(t)
	registry, err := rtcommand.NewFromProviders(rtcommand.Config{}, []agentkit.CommandProvider{p})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	value := "private-test-value"
	env := rctx.EnvelopeFromContext(ctx)
	out, err := common.ProcessPlatformSlash(ctx, common.AgentRoutingConfig{}, registry, common.SlashContext{Route: env.Route, UserID: "ou_owner", SessionScope: "thread"}, "/env set claude-acp AUTH_TOKEN="+value)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != common.SlashHandled {
		t.Fatal("slash not handled")
	}
	if strings.Contains(logs.String(), value) || strings.Contains(out.Reply, value) {
		t.Fatal("slash dispatch leaked value")
	}
	if !strings.Contains(logs.String(), "[REDACTED]") {
		t.Fatal("registry did not redact arguments")
	}
}

type unreadableStore struct{ credentials.Store }

func (unreadableStore) Get(context.Context, string, string) (string, error) {
	return "", errors.New("backend-secret-details")
}
func TestReadbackFailureDoesNotSaveReference(t *testing.T) {
	p, store, ctx := fixture(t)
	before, _ := os.ReadFile(p.cfg.Path)
	p.deps.Store = unreadableStore{store}
	_, err := p.Commands()[0].CommandExec(ctx, "set claude-acp TOKEN=private-value")
	if err == nil || strings.Contains(err.Error(), "backend-secret-details") {
		t.Fatal("must report safe readback error")
	}
	after, _ := os.ReadFile(p.cfg.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("reference saved without readback")
	}
}

func fixture(t *testing.T) (*Provider, *credentials.MemoryStore, context.Context) {
	t.Helper()
	home := t.TempDir()
	paths := config.ResolvePaths(home)
	cfg := config.Default()
	cfg.Platform.AppIDRef = "keyring://platform/app-id"
	cfg.Platform.AppSecretRef = "keyring://platform/app-secret"
	cfg.Agents = []config.Agent{{ID: "claude-acp", Command: []string{"npx"}}, {ID: "codex-acp", Command: []string{"npx"}}}
	cfg.Runtime.DefaultAgent = "claude-acp"
	if err := config.Save(paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	ownerPath := filepath.Join(paths.StateDir, "access.json")
	if _, err := access.NewStore(ownerPath).Update(func(s *access.State) error { s.OwnerOpenID = "ou_owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	store := credentials.NewMemoryStore()
	p, err := New(Config{Path: paths.ConfigFile, AccessPath: ownerPath, Platform: "lark"}, Deps{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx := rctx.ApplyEnvelopeToContext(context.Background(), agentkit.TurnEnvelope{Actor: agentkit.ActorRef{UserID: "ou_owner"}, Route: rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "lark", ChannelID: "oc_private", ScopeUserID: "ou_owner"})})
	if _, err := p.Evaluate(ctx, ingress.Input{Platform: "lark", Kind: ingress.Message, ChatID: "oc_private", UserID: "ou_owner", ConversationKind: "direct"}); err != nil {
		t.Fatal(err)
	}
	return p, store, ctx
}

func TestEnvSlashLifecycle(t *testing.T) {
	p, store, ctx := fixture(t)
	c := p.Commands()[0]
	value := "opaque = value ' \" $HOME"
	for _, args := range []string{"set claude-acp AUTH_TOKEN=" + value, "list claude-acp", "check claude-acp AUTH_TOKEN"} {
		out, err := c.CommandExec(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, value) {
			t.Fatal("value leaked in reply")
		}
	}
	cfg, err := config.Load(p.cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	ref := cfg.Agents[0].Env["AUTH_TOKEN"]
	parsed, err := credentials.ParseRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, parsed.Service, parsed.Account)
	if err != nil || got != value {
		t.Fatal("opaque value not stored exactly")
	}
	if len(cfg.Agents[1].Env) != 0 {
		t.Fatal("other agent changed")
	}
	raw, _ := os.ReadFile(p.cfg.Path)
	if strings.Contains(string(raw), value) {
		t.Fatal("secret saved in YAML")
	}
	if c.(agentkit.CommandLogSanitizer).SanitizeArgsForLog("set claude-acp AUTH_TOKEN="+value) != "[REDACTED]" {
		t.Fatal("args not redacted")
	}
	if _, err := c.CommandExec(ctx, "unset claude-acp AUTH_TOKEN"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(p.cfg.Path)
	if _, ok := cfg.Agents[0].Env["AUTH_TOKEN"]; ok {
		t.Fatal("reference not removed")
	}
}

func TestEnvSlashRejectsUnauthorizedAndUnknownConversation(t *testing.T) {
	p, store, ctx := fixture(t)
	c := p.Commands()[0]
	for _, tc := range []struct{ user, platform, chat, kind string }{{"ou_stranger", "lark", "oc_private", "direct"}, {"ou_owner", "lark", "oc_group", "group"}, {"ou_owner", "lark", "oc_unknown", ""}, {"ou_owner", "feishu", "oc_private", "direct"}} {
		other := rctx.ApplyEnvelopeToContext(ctx, agentkit.TurnEnvelope{Actor: agentkit.ActorRef{UserID: tc.user}, Route: rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: tc.platform, ChannelID: tc.chat, ScopeUserID: tc.user})})
		_, _ = p.Evaluate(other, ingress.Input{Platform: tc.platform, Kind: ingress.Message, ChatID: tc.chat, UserID: tc.user, ConversationKind: tc.kind})
		if _, err := c.CommandExec(other, "set claude-acp TOKEN=do-not-save"); !errors.Is(err, agentkit.ErrCommandForbidden) {
			t.Fatalf("not forbidden: %v", err)
		}
	}
	if len(store.Keys()) != 0 {
		t.Fatal("unauthorized Keychain write")
	}
}

func TestFailedEnvWriteLeavesConfigUnchanged(t *testing.T) {
	p, store, ctx := fixture(t)
	before, _ := os.ReadFile(p.cfg.Path)
	store.FailSet = errors.New("sensitive-backend-detail")
	out, err := p.Commands()[0].CommandExec(ctx, "set claude-acp TOKEN=do-not-save")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(out+err.Error(), "sensitive-backend-detail") || strings.Contains(out+err.Error(), "do-not-save") {
		t.Fatal("error leaks value")
	}
	after, _ := os.ReadFile(p.cfg.Path)
	if string(before) != string(after) {
		t.Fatal("failed write modified config")
	}
}

func TestEnvSlashRejectsInvalidAgentAndNames(t *testing.T) {
	p, store, ctx := fixture(t)
	for _, args := range []string{"set missing TOKEN=value", "set claude-acp BAD-NAME=value", "set claude-acp TOKEN=", "set claude-acp TOKEN=bad\x00value"} {
		_, _ = p.Commands()[0].CommandExec(ctx, args)
	}
	if len(store.Keys()) != 0 {
		t.Fatal("invalid input wrote credentials")
	}
}
