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

type instanceIDProvider struct {
	id   string
	name string
}

func (p instanceIDProvider) CommandInstanceID() string { return p.id }
func (p instanceIDProvider) Commands() []agentkit.Command {
	return []agentkit.Command{namedTestCommand(p.name)}
}

func TestScopedCommandsPrivateFilter(t *testing.T) {
	registry, err := command.NewFromProviders(command.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &scopedCommands{
		config: scopedCommandsConfig{
			Provider:        "env.weixin",
			ScopedProviders: []string{"env.default", "env.weixin"},
		},
		registry: registry,
	}
	err = s.SetCommands([]agentkit.CommandProvider{
		instanceIDProvider{id: "env.default", name: "env"},
		instanceIDProvider{id: "env.weixin", name: "env"},
		instanceIDProvider{id: "runner.default", name: "me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	envCount := 0
	for _, cmd := range s.List(ctx) {
		if cmd.Name() == "env" {
			envCount++
		}
	}
	if envCount != 1 {
		t.Fatalf("env commands: want 1, got %d", envCount)
	}
	got, err := s.Dispatch(ctx, "env", "")
	if err != nil || got != "env:" {
		t.Fatalf("env dispatch: %q, %v", got, err)
	}
	if _, err := s.Dispatch(ctx, "me", ""); err != nil {
		t.Fatalf("shared command: %v", err)
	}
	if counts := len(s.List(ctx)); counts < 2 {
		t.Fatalf("list too short: %d", counts)
	}
}
