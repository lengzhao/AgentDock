package presets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lengzhao/AgentDock/agent"
	"github.com/lengzhao/AgentDock/agent/presets"
)

func TestUserPresetJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-presets.json")
	custom := []byte(`{
  "version": 1,
  "presets": [
    {
      "id": "demo-acp",
      "loginHint": "demo login",
      "offlineCommand": ["echo", "demo"]
    }
  ]
}`)
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := presets.LoadFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := agent.Resolve("demo-acp")
	if err != nil || got.ID != "demo-acp" || got.Command[0] != "echo" {
		t.Fatalf("resolve: %+v, %v", got, err)
	}
	if agent.LoginHint("demo-acp") != "demo login" {
		t.Fatal("login hint")
	}
}
