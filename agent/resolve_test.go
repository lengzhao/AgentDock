package agent_test

import (
	"reflect"
	"testing"

	"github.com/lengzhao/AgentDock/agent"

	_ "github.com/lengzhao/AgentDock/agent/presets"
)

func TestCursorUsesInstalledNativeACPAndAuthentication(t *testing.T) {
	want := []string{"cursor-agent", "acp"}
	for _, id := range []string{"cursor", "cursor-acp"} {
		entry, err := agent.Resolve(id)
		if err != nil {
			t.Fatal(err)
		}
		if entry.ID != "cursor" || !reflect.DeepEqual(entry.Command, want) || entry.AuthMethod != "cursor_login" {
			t.Fatalf("Cursor preset %+v", entry)
		}
	}
	if agent.LoginHint("cursor") != "agent login (Cursor CLI)" {
		t.Fatal("missing Cursor login hint")
	}
}

func TestResolveOfflinePreset(t *testing.T) {
	got, err := agent.Resolve("claude")
	if err != nil || got.ID != "claude" || got.Command[0] != "npx" {
		t.Fatalf("claude: %+v, %v", got, err)
	}
	got, err = agent.Resolve("claude-acp")
	if err != nil || got.ID != "claude" {
		t.Fatalf("alias: %+v, %v", got, err)
	}
}
