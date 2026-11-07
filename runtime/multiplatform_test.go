package runtime_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/envslash"
	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/agentkit/runtime/platform/common"
	"github.com/lengzhao/agentkit/runtime/platform/multiplex"
	"github.com/lengzhao/agentkit/runtime/rctx"
	"github.com/lengzhao/pluginkit/build"
)

func withAdditionalWeixin() config.Config {
	cfg := testConfig()
	cfg.Platforms = []config.Platform{{Kind: "weixin", Credentials: map[string]string{"id": "env:WX_ID", "secret": "env:WX_TOKEN", "owner": "env:WX_OWNER"}, Settings: map[string]string{"baseUrl": "https://ilinkai.weixin.qq.com"}}}
	return cfg
}

func TestGraphAddsWeixinWithoutReplacingPrimaryOrSharedRuntime(t *testing.T) {
	cfg := withAdditionalWeixin()
	layout := runtime.Layout{Home: "/tmp/ad", InstanceDir: "/tmp/ad/instance", StateDir: "/tmp/ad/instance/state"}
	graph, _, err := runtime.Graph(cfg, layout, runtime.Secrets{AppID: "primary-app", AppSecret: "primary-secret", Platforms: map[string]runtime.Secrets{"weixin": {AppID: "wx-bot", AppSecret: "wx-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	root := graph["runner.default"].(runtime.Node)["deps"].(runtime.Node)
	if root["platform"] != "platform.audited" || graph["platform.audited"].(runtime.Node)["deps"].(runtime.Node)["inner"] != "platform.multiplex" {
		t.Fatal("runner does not observe multiplex delivery")
	}
	aggregator := graph["platform.multiplex"].(runtime.Node)
	if aggregator["use"] != "platform/multiplex" {
		t.Fatal("not a multiplex platform")
	}
	if !reflect.DeepEqual(aggregator["deps"].(runtime.Node)["platforms"], []any{"platform.main", "platform.weixin"}) {
		t.Fatalf("platform refs: %#v", aggregator)
	}
	primary := graph["platform.main"].(runtime.Node)
	if primary["use"] != "platform/lark" || primary["config"].(runtime.Node)["appId"] != "primary-app" {
		t.Fatal("primary connection replaced")
	}
	extra := graph["platform.weixin"].(runtime.Node)
	if extra["use"] != "platform/weixin" || extra["config"].(runtime.Node)["botId"] != "wx-bot" || extra["config"].(runtime.Node)["token"] != "wx-token" {
		t.Fatalf("extra platform: %#v", extra)
	}
	if extra["deps"].(runtime.Node)["commands"] != "commands.weixin" || extra["deps"].(runtime.Node)["inboundPolicy"] != "env.weixin" {
		t.Fatal("extra dependencies are unscoped")
	}
	scoped := filepath.Join(layout.StateDir, "platforms", "weixin")
	if extra["config"].(runtime.Node)["cursorPath"] != filepath.Join(scoped, "weixin-cursor.json") {
		t.Fatal("cursor path unscoped")
	}
	env := graph["env.weixin"].(runtime.Node)
	if env["config"].(runtime.Node)["accessPath"] != filepath.Join(scoped, "access.json") {
		t.Fatal("access path unscoped")
	}
	verify := env["deps"].(runtime.Node)["inner"].(runtime.Node)
	if verify["config"].(runtime.Node)["stateDir"] != scoped {
		t.Fatal("verify state unscoped")
	}
	record := verify["deps"].(runtime.Node)["inner"].(runtime.Node)
	if record["config"].(runtime.Node)["dbPath"] != filepath.Join(layout.StateDir, "state.db") {
		t.Fatal("task records separated from operational DB")
	}
	if graph["commands.weixin"].(runtime.Node)["deps"].(runtime.Node)["sessionCommands"] != "sessionCommands.default" {
		t.Fatal("command graph did not share session store")
	}
	if !reflect.DeepEqual(root["init"], []any{"env.default", "env.weixin", "schedule.records"}) {
		t.Fatalf("init: %#v", root["init"])
	}
	for _, id := range []string{"runner.weixin", "loop.weixin", "workspace.weixin", "sessionStore.weixin", "sessionCommands.weixin"} {
		if graph[id] != nil {
			t.Fatalf("unexpected duplicate shared node %s", id)
		}
	}
}

func TestBuildAssemblesMultiplexRunnerAndResolvesAllCredentials(t *testing.T) {
	cfg := withAdditionalWeixin()
	store := credentials.NewMemoryStore()
	ctx := context.Background()
	for key, value := range map[string]string{"app-id": "primary-app", "app-secret": "primary-secret"} {
		if err := store.Set(ctx, "platform", key, value); err != nil {
			t.Fatal(err)
		}
	}
	resolver := credentials.NewResolver(store, func(name string) (string, bool) {
		switch name {
		case "WX_ID":
			return "wx-bot", true
		case "WX_TOKEN":
			return "wx-token", true
		case "WX_OWNER":
			return "wx-owner", true
		}
		return "", false
	})
	layout := runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}
	if err := config.Save(filepath.Join(layout.Home, "config.yaml"), cfg); err != nil {
		t.Fatal(err)
	}
	runner, result, err := runtime.Build(ctx, cfg, layout, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("nil runner")
	}
	platform, ok := build.GetByID[agentkit.Platform](result, "platform.multiplex")
	if !ok {
		t.Fatal("multiplex instance missing")
	}
	if _, ok := platform.(*multiplex.Platform); !ok {
		t.Fatalf("not multiplex: %T", platform)
	}

	providers := []agentkit.CommandProvider{}
	for _, instance := range build.CollectInstances[agentkit.CommandProvider](result) {
		providers = append(providers, agentkit.CommandProviderInstance{CommandProvider: instance.Value, ID: instance.ID})
	}
	for _, collector := range build.CollectInstances[agentkit.CommandCollector](result) {
		if err := collector.Value.SetCommands(providers); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"commands.default", "commands.weixin"} {
		commands, ok := build.GetByID[agentkit.Commands](result, id)
		if !ok {
			t.Fatalf("missing collector %s", id)
		}
		counts := map[string]int{}
		for _, command := range commands.List(ctx) {
			counts[command.Name()]++
		}
		if counts["env"] != 1 || counts["plugin"] != 1 {
			t.Fatalf("commands for %s: %#v", id, counts)
		}
		if _, err := commands.Dispatch(ctx, "env", "list claude"); err != agentkit.ErrCommandForbidden {
			t.Fatalf("env handler for %s bypassed auth: %v", id, err)
		}
	}
	// Providers are separate even though they edit the same global config.
	primaryEnv, _ := build.GetByID[*envslash.Provider](result, "env.default")
	extraEnv, _ := build.GetByID[*envslash.Provider](result, "env.weixin")
	if primaryEnv == nil || extraEnv == nil || primaryEnv == extraEnv {
		t.Fatal("env providers are not scoped")
	}

	for _, connection := range []struct {
		kind, id, path string
		provider       *envslash.Provider
	}{
		{"lark", "commands.default", layout.StateDir, primaryEnv},
		{"weixin", "commands.weixin", filepath.Join(layout.StateDir, "platforms", "weixin"), extraEnv},
	} {
		user := "owner-" + connection.kind
		_, err := access.NewStore(filepath.Join(connection.path, "access.json")).Update(func(st *access.State) error { st.OwnerOpenID = user; return nil })
		if err != nil {
			t.Fatal(err)
		}
		decision, err := connection.provider.Evaluate(ctx, ingress.Input{
			Platform: connection.kind, InstanceID: connection.kind, Kind: ingress.Message, ID: "env-check-" + connection.kind,
			ChatID: user, UserID: user, ConversationKind: "direct", Raw: []byte("hello"),
			Default: ingress.Plan{Agent: ingress.Submit, Handlers: true},
		})
		if err != nil || decision.Reject {
			t.Fatalf("admit %s owner: %v %+v", connection.kind, err, decision)
		}
		commands, _ := build.GetByID[agentkit.Commands](result, connection.id)
		route := rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: connection.kind, Instance: connection.kind, ChannelID: user, DeliveryID: rctx.BuildDeliverySessionID(connection.kind, user, "", "")})
		outcome, err := common.ProcessSlash(ctx, commands, common.SlashContext{Route: route, SessionScope: rctx.ParseScope("thread"), UserID: user}, "/env list claude")
		if err != nil || outcome.Kind != common.SlashHandled {
			t.Fatalf("scoped env command %s failed: %v %+v", connection.kind, err, outcome)
		}
	}
}

func TestBuildRejectsMissingAdditionalCredentials(t *testing.T) {
	cfg := withAdditionalWeixin()
	resolver := credentials.NewResolver(credentials.NewMemoryStore(), func(string) (string, bool) { return "", false })
	// Resolve primary through environment so the extra credential is the failure.
	cfg.Platform.AppIDRef = "env:PRIMARY_ID"
	cfg.Platform.AppSecretRef = "env:PRIMARY_TOKEN"
	resolver = credentials.NewResolver(credentials.NewMemoryStore(), func(name string) (string, bool) {
		if name == "PRIMARY_ID" || name == "PRIMARY_TOKEN" {
			return "primary", true
		}
		return "", false
	})
	if _, _, err := runtime.Build(context.Background(), cfg, runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}, resolver); err == nil {
		t.Fatal("missing extra credentials accepted")
	}
}
