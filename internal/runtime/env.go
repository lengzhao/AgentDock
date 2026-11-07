package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/credentials"
)

// ResolveAgentEnvironment returns a separate in-memory map. Resolved values are
// never written back into Config, exposed through slash, or included in errors.
func ResolveAgentEnvironment(ctx context.Context, cfg config.Config, resolver *credentials.Resolver) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		env := make(map[string]string, len(agent.Env))
		for name, value := range agent.Env {
			if strings.HasPrefix(value, "keyring://") || strings.HasPrefix(value, "env:") {
				resolved, err := resolver.Resolve(ctx, value)
				if err != nil || resolved == "" {
					return nil, fmt.Errorf("agent %s environment %s is unavailable; check its credential reference and Keychain access", agent.ID, name)
				}
				value = resolved
			}
			env[name] = value
		}
		result[agent.ID] = env
	}
	return result, nil
}
