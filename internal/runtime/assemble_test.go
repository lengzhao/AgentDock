package runtime_test

import (
	"context"
	"testing"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/credentials"
	"github.com/lengzhao/AgentDock/internal/runtime"
)

func testConfig() config.Config {
	cfg := config.Default()
	// Services are bare names; the keyring store adds the install prefix, so the
	// reference stays stable if the prefix ever changes.
	cfg.Platform.AppIDRef = "keyring://platform/app-id"
	cfg.Platform.AppSecretRef = "keyring://platform/app-secret"
	cfg.Platform.AllowFrom = "ou_owner"
	cfg.Agents = []config.Agent{
		{ID: "claude", Command: []string{"npx", "-y", "pkg@1.0.0"}, Cwd: "work"},
		{ID: "codex", Command: []string{"npx", "-y", "pkg2@2.0.0"}, Cwd: "work"},
	}
	cfg.Runtime.DefaultAgent = "claude"
	return cfg
}

func TestGraphContainsResolvedSecretsAndAgents(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	graph, rootID, err := runtime.Graph(cfg, runtime.Layout{Home: "/tmp/ad", InstanceDir: "/tmp/ad/instance", StateDir: "/tmp/ad/instance/state"},
		runtime.Secrets{AppID: "cli_x", AppSecret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if rootID != "runner.default" {
		t.Fatalf("root = %q", rootID)
	}
	platform, ok := graph["platform.main"].(map[string]any)
	if !ok {
		t.Fatal("platform.main missing")
	}
	if platform["use"] != "platform/lark" {
		t.Fatalf("platform kind = %v", platform["use"])
	}
	platformCfg := platform["config"].(map[string]any)
	if platformCfg["appId"] != "cli_x" || platformCfg["appSecret"] != "s3cret" {
		t.Fatalf("secrets not injected: %v", platformCfg)
	}
	if platformCfg["domain"] != "https://open.larksuite.com" {
		t.Fatalf("domain = %v", platformCfg["domain"])
	}
	if platformCfg["dmThreadMode"] != true {
		t.Fatal("private messages must use Lark topics by default")
	}
	loopCfg := graph["loop.default"].(map[string]any)["config"].(map[string]any)
	if loopCfg["defaultAgent"] != "claude" {
		t.Fatalf("defaultAgent = %v", loopCfg["defaultAgent"])
	}
	agents := graph["loop.default"].(map[string]any)["deps"].(map[string]any)["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("agents = %d", len(agents))
	}
}

func TestGraphWiresUnifiedEnvProvider(t *testing.T) {
	cfg := testConfig()
	graph, _, err := runtime.Graph(cfg, runtime.Layout{Home: "/tmp/ad", StateDir: "/tmp/ad/state"}, runtime.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := graph["env.default"].(map[string]any)
	if !ok || provider["use"] != "agentdock/env" {
		t.Fatal("env provider missing")
	}
	platform := graph["platform.main"].(map[string]any)
	if platform["deps"].(map[string]any)["inboundPolicy"] != "env.default" {
		t.Fatal("env provider must receive trusted conversation facts")
	}
}

func TestGraphUsesFeishuDomainForFeishu(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Platform.Kind = "feishu"
	graph, _, err := runtime.Graph(cfg, runtime.Layout{Home: "/tmp/ad", InstanceDir: "/tmp/ad/i", StateDir: "/tmp/ad/i/state"},
		runtime.Secrets{AppID: "a", AppSecret: "b"})
	if err != nil {
		t.Fatal(err)
	}
	platform := graph["platform.main"].(map[string]any)
	if platform["use"] != "platform/feishu" {
		t.Fatalf("kind = %v", platform["use"])
	}
	if got := platform["config"].(map[string]any)["domain"]; got != "https://open.feishu.cn" {
		t.Fatalf("domain = %v", got)
	}
}

func TestGraphRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.Runtime.DefaultAgent = "missing"
	if _, _, err := runtime.Graph(cfg, runtime.Layout{}, runtime.Secrets{}); err == nil {
		t.Fatal("invalid config must not produce a graph")
	}
}

// TestBuildAssemblesRunner is the regression test that keeps the AgentKit wiring
// honest: if an AgentKit upgrade renames a dep or a kind, this fails here rather
// than at a user's first start.
func TestBuildAssemblesRunner(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	store := credentials.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "platform", "app-id", "cli_x"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "platform", "app-secret", "s3cret"); err != nil {
		t.Fatal(err)
	}
	resolver := credentials.NewResolver(store, nil)

	runner, result, err := runtime.Build(ctx, cfg, runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}, resolver)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if runner == nil {
		t.Fatal("nil runner")
	}
	if len(result.Instances) < 10 {
		t.Fatalf("instances = %d, want the full graph", len(result.Instances))
	}
}
