package runtime_test

import (
	"context"
	"errors"
	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/envslash"
	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/cap/ingress"
	"github.com/lengzhao/agentkit/runtime/rctx"
	"github.com/lengzhao/pluginkit/build"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/runtime"
)

func TestBuiltEnvCommandSharesResolverStore(t *testing.T) {
	cfg := testConfig()
	paths := config.ResolvePaths(t.TempDir())
	if err := config.Save(paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := access.NewStore(filepath.Join(paths.StateDir, "access.json")).Update(func(s *access.State) error { s.OwnerOpenID = "ou_owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	store := credentials.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "platform", "app-id", "test-app"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "platform", "app-secret", "test-secret"); err != nil {
		t.Fatal(err)
	}
	resolver := credentials.NewResolver(store, nil)
	_, result, err := runtime.Build(ctx, cfg, runtime.Layout{Home: paths.Home, StateDir: paths.StateDir, InstanceDir: paths.InstanceDir}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := build.GetByID[*envslash.Provider](result, "env.default")
	if !ok {
		t.Fatal("provider missing")
	}
	_, err = p.Evaluate(ctx, ingress.Input{Platform: "lark", Kind: ingress.Message, ChatID: "oc_test", UserID: "ou_owner", ConversationKind: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = rctx.ApplyEnvelopeToContext(ctx, agentkit.TurnEnvelope{Route: rctx.BuildSessionRoute(agentkit.SessionRouteInput{Platform: "lark", ChannelID: "oc_test", ScopeUserID: "ou_owner"}), Actor: agentkit.ActorRef{UserID: "ou_owner"}})
	_, err = p.Commands()[0].CommandExec(ctx, "set claude TEST_ENV=stored-in-same-backend")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	env, err := runtime.ResolveAgentEnvironment(ctx, cfg, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if env["claude"]["TEST_ENV"] != "stored-in-same-backend" {
		t.Fatal("command and runtime use different credential stores")
	}
}

func TestResolveAgentEnvironment(t *testing.T) {
	cfg := testConfig()
	ref := "keyring://test-env/token"
	cfg.Agents[0].Env = map[string]string{"AUTH_TOKEN": ref, "MODE": "0", "SOURCE": "env:TEST_SOURCE"}
	store := credentials.NewMemoryStore()
	if err := store.Set(context.Background(), "test-env", "token", "test-only-secret"); err != nil {
		t.Fatal(err)
	}
	resolver := credentials.NewResolver(store, func(k string) (string, bool) { return "from-shell", k == "TEST_SOURCE" })
	envs, err := runtime.ResolveAgentEnvironment(context.Background(), cfg, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if envs["claude"]["AUTH_TOKEN"] != "test-only-secret" || envs["claude"]["MODE"] != "0" || envs["claude"]["SOURCE"] != "from-shell" {
		t.Fatal("environment not resolved")
	}
	if cfg.Agents[0].Env["AUTH_TOKEN"] != ref {
		t.Fatal("original config mutated")
	}
	graph, _, err := runtime.Graph(cfg, runtime.Layout{Home: t.TempDir()}, runtime.Secrets{AppID: "a", AppSecret: "b", AgentEnv: envs})
	if err != nil {
		t.Fatal(err)
	}
	loop := graph["loop.default"].(map[string]any)
	agents := loop["deps"].(map[string]any)["agents"].([]any)
	got := agents[0].(map[string]any)["config"].(map[string]any)["env"].(map[string]any)
	if got["AUTH_TOKEN"] != "test-only-secret" {
		t.Fatal("resolved value not injected into ACP config")
	}
	if _, _, err = runtime.Graph(cfg, runtime.Layout{}, runtime.Secrets{}); err == nil {
		t.Fatal("unresolved reference must not reach child")
	}
}

type failingEnvStore struct{ credentials.Store }

func (failingEnvStore) Get(context.Context, string, string) (string, error) {
	return "", errors.New("backend could leak do-not-print")
}

func TestUnavailableAgentEnvDoesNotLeakBackendErrors(t *testing.T) {
	cfg := testConfig()
	cfg.Agents[0].Env = map[string]string{"AUTH_TOKEN": "keyring://test-env/token"}
	_, err := runtime.ResolveAgentEnvironment(context.Background(), cfg, credentials.NewResolver(failingEnvStore{}, nil))
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("must fail safely")
	}
}
