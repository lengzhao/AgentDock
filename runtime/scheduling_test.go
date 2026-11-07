package runtime_test

import (
	"github.com/lengzhao/AgentDock/runtime"
	"path/filepath"
	"testing"
)

func TestGraphWiresSharedTimersAndACPTools(t *testing.T) {
	cfg := withAdditionalWeixin()
	layout := runtime.Layout{Home: t.TempDir(), InstanceDir: t.TempDir(), StateDir: t.TempDir()}
	graph, _, err := runtime.Graph(cfg, layout, runtime.Secrets{AppID: "lark", AppSecret: "secret", Platforms: map[string]runtime.Secrets{"weixin": {AppID: "bot", AppSecret: "token"}}})
	if err != nil {
		t.Fatal(err)
	}
	root := graph["runner.default"].(runtime.Node)["deps"].(runtime.Node)
	if root["platform"] != "platform.audited" {
		t.Fatal("scheduled delivery is not observed")
	}
	schedules, ok := root["schedules"].([]any)
	if !ok || len(schedules) != 1 || schedules[0] != "schedule.runtime" {
		t.Fatal("shared timer runtime missing")
	}
	store, ok := graph["schedule.store"].(runtime.Node)
	if !ok || store["config"].(runtime.Node)["path"] != filepath.Join(layout.StateDir, "schedules.json") {
		t.Fatal("durable shared timer store missing")
	}
	agents := graph["loop.default"].(runtime.Node)["deps"].(runtime.Node)["agents"].([]any)
	for _, entry := range agents {
		if entry.(runtime.Node)["deps"].(runtime.Node)["sessionMcp"] != "schedule.mcp" {
			t.Fatal("ACP agent cannot use schedule tool")
		}
	}
	if graph["schedule.tool"] == nil || graph["schedule.toolRuntime"] == nil {
		t.Fatal("timer tool not exposed through runtime")
	}
}
func TestGraphCanDisableTimers(t *testing.T) {
	cfg := testConfig()
	cfg.Scheduling.Disabled = true
	graph, _, err := runtime.Graph(cfg, runtime.Layout{}, runtime.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	if graph["schedule.runtime"] != nil || graph["schedule.mcp"] != nil {
		t.Fatal("disabled timers still assembled")
	}
}
