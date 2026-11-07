package cli

import (
	"context"
	"github.com/lengzhao/AgentDock/access"
	"io"
	"path/filepath"
	"testing"

	"github.com/lengzhao/AgentDock/config"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/platform"
	"github.com/lengzhao/AgentDock/setup"
	"github.com/lengzhao/pluginkit"
)

type fakePlatformAuth struct{}

func (fakePlatformAuth) Login(context.Context, platform.LoginRequest) (platform.LoginResult, error) {
	return platform.LoginResult{Credentials: map[string]string{"id": "fake-id", "secret": "fake-secret", "owner": "fake-owner"}, Settings: map[string]string{"region": "test"}}, nil
}
func (fakePlatformAuth) Owner(context.Context, map[string]string, map[string]string) (string, error) {
	return "fake-owner", nil
}
func TestPlatformLoginUsesSharedPersistenceAndOwnerBinding(t *testing.T) {
	pluginkit.Register("test/platform-auth", func() (platform.Authenticator, error) { return fakePlatformAuth{}, nil })
	old := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = old })
	if err := rootEnv.paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Platform.Kind = "custom"
	cfg.Platform.AuthPlugin = "test/platform-auth"
	store := credentials.NewMemoryStore()
	state := setup.State{}
	if err := stepPlatform(context.Background(), io.Discard, &cfg, &setupFlags{platformKind: "custom"}, store, &state); err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Settings["region"] != "test" || len(cfg.Platform.Credentials) != 3 {
		t.Fatal("generic login result missing")
	}
	resolver := credentials.NewResolver(store, nil)
	if err := autoBindApplicationOwner(context.Background(), io.Discard, cfg, resolver); err != nil {
		t.Fatal(err)
	}
	for _, ref := range cfg.Platform.Credentials {
		if _, err := resolver.Resolve(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSetupAddsPlatformWithoutReplacingExisting(t *testing.T) {
	pluginkit.Register("test/add-auth", func() (platform.Authenticator, error) { return fakePlatformAuth{}, nil })
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	if err := rootEnv.paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:LARK_ID"
	cfg.Platform.AppSecretRef = "env:LARK_SECRET"
	cfg.Agents = []config.Agent{{ID: "custom-agent", Command: []string{"custom"}, Env: map[string]string{"PATH": "custom-path"}}}
	cfg.Runtime.DefaultAgent = "custom-agent"
	if err := stepAgents(context.Background(), io.Discard, &cfg, &setupFlags{}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.DefaultAgent != "custom-agent" || cfg.Agents[0].Env["PATH"] != "custom-path" {
		t.Fatal("platform setup replaced agents")
	}
	state := setup.State{}
	if err := stepPlatform(context.Background(), io.Discard, &cfg, &setupFlags{platformKind: "weixin", authPlugin: "test/add-auth"}, credentials.NewMemoryStore(), &state); err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Kind != "lark" || cfg.Platform.AppIDRef != "env:LARK_ID" {
		t.Fatal("Lark replaced")
	}
	wx, ok := cfg.Connection("weixin")
	if !ok || len(wx.Credentials) == 0 {
		t.Fatal("Weixin not appended")
	}
}

func TestAddedPlatformOwnerDoesNotReusePrimaryBinding(t *testing.T) {
	pluginkit.Register("test/owner-auth", func() (platform.Authenticator, error) { return fakePlatformAuth{}, nil })
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:LARK_ID"
	cfg.Platform.AppSecretRef = "env:LARK_SECRET"
	primary := access.NewStore(filepath.Join(rootEnv.paths.StateDir, "access.json"))
	if _, err := primary.Update(func(st *access.State) error { st.OwnerOpenID = "lark-owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	memory := credentials.NewMemoryStore()
	progress := setup.State{}
	if err := stepPlatform(context.Background(), io.Discard, &cfg, &setupFlags{platformKind: "weixin", authPlugin: "test/owner-auth"}, memory, &progress); err != nil {
		t.Fatal(err)
	}
	if err := autoBindApplicationOwner(context.Background(), io.Discard, cfg, credentials.NewResolver(memory, nil)); err != nil {
		t.Fatal(err)
	}
	got, err := primary.Owner()
	if err != nil || got != "lark-owner" {
		t.Fatal("primary owner changed")
	}
	scoped := rootEnv.paths.ForPlatform("weixin", "lark")
	owner, err := access.NewStore(filepath.Join(scoped.StateDir, "access.json")).Owner()
	if err != nil || owner != "fake-owner" {
		t.Fatalf("additional owner %q %v", owner, err)
	}
}
