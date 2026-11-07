package runtime

import (
	"context"

	"github.com/lengzhao/agentkit"
	"github.com/lengzhao/agentkit/runtime/command"
	"github.com/lengzhao/pluginkit"
)

const scopedCommandsKind = "agentdock/commands-scoped"

// scopedCommandsConfig identifies private providers by exact graph instance ID.
// Shared providers such as runner, ACP and session commands remain available.
type scopedCommandsConfig struct {
	Provider        string   `json:"provider"`
	ScopedProviders []string `json:"scopedProviders"`
}
type scopedCommandsDeps struct {
	SessionCommands agentkit.CommandProvider `json:"sessionCommands,omitempty"`
}

// scopedCommands keeps each connection's /env handler local even though AgentKit
// contributes all providers in the build graph to every command collector.
// It deliberately does not implement CommandProvider: /plugin is added once to
// its internal registry rather than contributing one copy per connection.
type scopedCommands struct {
	config   scopedCommandsConfig
	registry *command.Registry
}

func init() {
	pluginkit.Register(scopedCommandsKind, func(cfg scopedCommandsConfig, _ scopedCommandsDeps) (agentkit.Commands, error) {
		registry, err := command.NewFromProviders(command.Config{}, nil)
		if err != nil {
			return nil, err
		}
		return &scopedCommands{config: cfg, registry: registry}, nil
	})
}

func (s *scopedCommands) SetCommands(providers []agentkit.CommandProvider) error {
	private := make(map[string]bool, len(s.config.ScopedProviders))
	for _, id := range s.config.ScopedProviders {
		private[id] = true
	}
	selected := make([]agentkit.CommandProvider, 0, len(providers)+1)
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		id := ""
		if identified, ok := provider.(interface{ CommandInstanceID() string }); ok {
			id = identified.CommandInstanceID()
		}
		if private[id] && id != s.config.Provider {
			continue
		}
		selected = append(selected, agentkit.CommandProviderInstance{
			CommandProvider: shortACPCommands{provider}, ID: id,
		})
	}
	selected = append(selected, pluginHelpProvider{})
	return s.registry.SetCommands(selected)
}
func (s *scopedCommands) Dispatch(ctx context.Context, name, args string) (string, error) {
	return s.registry.Dispatch(ctx, name, args)
}
func (s *scopedCommands) List(ctx context.Context) []agentkit.Command { return s.registry.List(ctx) }

type pluginHelpProvider struct{}

func (pluginHelpProvider) Commands() []agentkit.Command {
	return []agentkit.Command{command.PluginHelpCommand()}
}

var _ agentkit.CommandCollector = (*scopedCommands)(nil)

// Keep catalog adapter IDs stable while exposing concise user commands.
type shortACPCommands struct{ agentkit.CommandProvider }

func (p shortACPCommands) Commands() []agentkit.Command {
	commands := append([]agentkit.Command(nil), p.CommandProvider.Commands()...)
	for i, cmd := range commands {
		if cmd == nil {
			continue
		}
		name := ""
		switch cmd.Name() {
		case "acp-claude-acp":
			name = "acp-claude"
		case "acp-codex-acp":
			name = "acp-codex"
		}
		if name != "" {
			commands[i] = shortACPCommand{Command: cmd, name: name}
		}
	}
	return commands
}

type shortACPCommand struct {
	agentkit.Command
	name string
}

func (c shortACPCommand) Name() string { return c.name }
func (c shortACPCommand) SanitizeArgsForLog(args string) string {
	if sanitizer, ok := c.Command.(agentkit.CommandLogSanitizer); ok {
		return sanitizer.SanitizeArgsForLog(args)
	}
	return args
}
