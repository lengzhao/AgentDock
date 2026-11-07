package runtime

import (
	"context"
	"testing"

	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/runtime/command"
)

type namedTestCommand string

func (c namedTestCommand) Name() string      { return string(c) }
func (namedTestCommand) Alias() string       { return "" }
func (namedTestCommand) Description() string { return "test" }
func (c namedTestCommand) CommandExec(_ context.Context, args string) (string, error) {
	return string(c) + ":" + args, nil
}
func (namedTestCommand) SanitizeArgsForLog(string) string { return "redacted" }

type testACPProvider struct{}

func (testACPProvider) Commands() []agentkit.Command {
	return []agentkit.Command{namedTestCommand("acp-claude-acp"), namedTestCommand("acp-codex-acp"), namedTestCommand("acp-cursor")}
}

func TestShortACPCommandsDispatch(t *testing.T) {
	registry, err := command.NewFromProviders(command.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &scopedCommands{registry: registry}
	if err := s.SetCommands([]agentkit.CommandProvider{testACPProvider{}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, original := range map[string]string{"acp-claude": "acp-claude-acp", "acp-codex": "acp-codex-acp", "acp-cursor": "acp-cursor"} {
		got, err := s.Dispatch(ctx, name, "model")
		if err != nil || got != original+":model" {
			t.Fatalf("/%s: got %q, %v", name, got, err)
		}
	}
	for _, cmd := range s.List(ctx) {
		if cmd.Name() == "acp-claude-acp" || cmd.Name() == "acp-codex-acp" {
			t.Fatalf("old command in help: %s", cmd.Name())
		}
		if cmd.Name() == "acp-claude" && cmd.(agentkit.CommandLogSanitizer).SanitizeArgsForLog("secret") != "redacted" {
			t.Fatal("lost log redaction")
		}
	}
}
