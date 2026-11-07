package agent

import (
	"fmt"

	"github.com/lengzhao/AgentDock/config"
)

// Resolve maps a preset id to config.Agent using agent-presets and built-in resolvers.
func Resolve(id string) (config.Agent, error) {
	id = NormalizeID(id)
	p, ok := presetsByID[id]
	if !ok {
		return config.Agent{}, fmt.Errorf("unknown agent %q: add it to agent-presets.json", id)
	}
	if p.Resolve != nil {
		return p.Resolve()
	}
	if len(p.OfflineCommand) > 0 {
		return config.Agent{ID: id, Command: p.OfflineCommand, AuthMethod: p.AuthMethod}, nil
	}
	return config.Agent{}, fmt.Errorf("agent %q has no command in agent-presets.json", id)
}
