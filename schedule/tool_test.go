package schedule

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/access"
	"github.com/lengzhao/agentkit"
	capschedule "github.com/lengzhao/agentkit/cap/schedule"
)

func TestToolNormalizesLocalAtAndPreservesCronCommands(t *testing.T) {
	store, _, engine := testStore(t, 32)
	path := filepath.Join(t.TempDir(), "access.json")
	if _, err := access.NewStore(path).Update(func(st *access.State) error { st.OwnerOpenID = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	scoped, err := NewScoped(ScopedConfig{AccessPaths: map[string]string{"lark": path}}, ScopedDeps{Inner: store})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewTool(ToolConfig{}, ToolDeps{Schedule: scoped, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "schedule" || tool.InputSchema().Type != "object" {
		t.Fatal("schema/name changed")
	}
	ctx := trustedContext("lark", "owner", "chat", "thread")
	_, err = tool.Call(ctx, json.RawMessage(`{"op":"add","kind":"at","at":"2035-01-02T09:30","prompt":"remind"}`))
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %d: %v", len(jobs), err)
	}
	expected := time.Date(2035, 1, 2, 1, 30, 0, 0, time.UTC)
	if !jobs[0].FireAt.Equal(expected) {
		t.Fatalf("wrong local timezone: %s", jobs[0].FireAt)
	}
	provider, ok := tool.(agentkit.CommandProvider)
	if !ok {
		t.Fatal("lost CommandProvider")
	}
	commands := provider.Commands()
	if len(commands) != 1 || commands[0].Name() != "cron" {
		t.Fatal("lost cron command")
	}
	if _, err := commands[0].CommandExec(ctx, "list"); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"op":"list"}`)); err == nil {
		t.Fatal("tool bypassed scope authorization")
	}
	if _, err := tool.Call(ctx, json.RawMessage(`{"op":"add","kind":"delay","in":"1m","prompt":"later"}`)); err != nil {
		t.Fatal(err)
	}
	jobs, _ = store.List(context.Background())
	if len(jobs) != 2 || jobs[1].Kind != capschedule.KindDelay {
		t.Fatal("delay not persisted")
	}
}
