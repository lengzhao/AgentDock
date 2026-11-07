package runtime_test

import (
	"context"
	"github.com/lengzhao/AgentDock/credentials"
	"github.com/lengzhao/AgentDock/runtime"
	"github.com/lengzhao/agentkit/cap/telemetry"
	"github.com/lengzhao/pluginkit"
	"testing"
)

func TestGraphReplacement(t *testing.T) {
	override := map[string]runtime.Node{
		"telemetry.default": {"use": "test/telemetry"},
		"extra":             {"use": "test/extra"},
	}
	graph, _, err := runtime.GraphWithOverrides(testConfig(), runtime.Layout{}, runtime.Secrets{}, override)
	if err != nil {
		t.Fatal(err)
	}
	if graph["telemetry.default"].(runtime.Node)["use"] != "test/telemetry" || graph["extra"] == nil {
		t.Fatal("replacement missing")
	}
	original, _, err := runtime.Graph(testConfig(), runtime.Layout{}, runtime.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	if original["telemetry.default"].(runtime.Node)["use"] != "telemetry/none" {
		t.Fatal("default modified")
	}
}
func TestGraphRejectsInvalidReplacement(t *testing.T) {
	for _, overrides := range []map[string]runtime.Node{
		{"": {"use": "test/extra"}},
		{"telemetry.default": {}},
	} {
		if _, _, err := runtime.GraphWithOverrides(testConfig(), runtime.Layout{}, runtime.Secrets{}, overrides); err == nil {
			t.Fatal("invalid replacement accepted")
		}
	}
}

// A registered alternate implementation must assemble through the real graph.
func TestBuildWithAlternateTelemetry(t *testing.T) {
	pluginkit.Register("test/alternate-telemetry", func() (telemetry.Exporter, error) { return telemetry.Noop, nil })
	cfg := testConfig()
	store := credentials.NewMemoryStore()
	ctx := context.Background()
	for account, value := range map[string]string{"app-id": "cli_x", "app-secret": "secret"} {
		if err := store.Set(ctx, "platform", account, value); err != nil {
			t.Fatal(err)
		}
	}
	runner, result, err := runtime.BuildWithOverrides(ctx, cfg,
		runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()},
		credentials.NewResolver(store, nil),
		map[string]runtime.Node{"telemetry.default": {"use": "test/alternate-telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil || result == nil {
		t.Fatal("runner not assembled")
	}
}

func TestExplicitEnvStoreDoesNotRequireResolverStore(t *testing.T) {
	cfg := testConfig()
	cfg.Platform.AppIDRef = "env:APP_ID"
	cfg.Platform.AppSecretRef = "env:APP_SECRET"
	layout := runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}
	graph, _, err := runtime.Graph(cfg, layout, runtime.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	node := graph["env.default"].(runtime.Node)
	node["deps"].(runtime.Node)["store"] = runtime.Node{"use": "agentdock/credentials-memory"}
	resolver := credentials.NewResolver(nil, func(string) (string, bool) { return "value", true })
	if _, _, err := runtime.BuildWithOverrides(context.Background(), cfg, layout, resolver,
		map[string]runtime.Node{"env.default": node}); err != nil {
		t.Fatal(err)
	}
}
