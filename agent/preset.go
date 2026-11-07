package agent

import (
	"fmt"
	"strings"

	"github.com/lengzhao/AgentDock/config"
)

// Preset describes a built-in adapter: login hint, offline argv, or custom resolve.
type Preset struct {
	ID             string
	Aliases        []string
	LoginHint      string
	OfflineCommand []string
	AuthMethod string
	Resolve    func() (config.Agent, error)
}

var (
	presetsByID    = map[string]Preset{}
	presetAliasToID = map[string]string{}
)

// Register adds a preset. Panics on duplicate ids; call from init only.
func Register(p Preset) {
	if err := RegisterOrReplace(p); err != nil {
		panic(err)
	}
}

// RegisterOrReplace adds or updates a preset by id.
func RegisterOrReplace(p Preset) error {
	if p.ID == "" {
		return fmt.Errorf("agent preset: id required")
	}
	if old, exists := presetsByID[p.ID]; exists {
		for _, alias := range old.Aliases {
			if presetAliasToID[alias] == p.ID {
				delete(presetAliasToID, alias)
			}
		}
	}
	presetsByID[p.ID] = p
	for _, alias := range p.Aliases {
		if alias == "" {
			continue
		}
		if other, taken := presetAliasToID[alias]; taken && other != p.ID {
			return fmt.Errorf("agent preset: alias %q already maps to %s", alias, other)
		}
		presetAliasToID[alias] = p.ID
	}
	return nil
}

func presetFor(id string) (Preset, bool) {
	id = NormalizeID(id)
	p, ok := presetsByID[id]
	return p, ok
}

// NormalizeID maps CLI input to the canonical preset id stored in config.
func NormalizeID(id string) string {
	if canon, ok := presetAliasToID[id]; ok {
		return canon
	}
	if strings.HasSuffix(id, "-acp") {
		short := strings.TrimSuffix(id, "-acp")
		if _, ok := presetsByID[short]; ok {
			return short
		}
	}
	return id
}

// LoginHint names the upstream login command without running it.
func LoginHint(id string) string {
	if p, ok := presetFor(id); ok && p.LoginHint != "" {
		return p.LoginHint
	}
	return "follow the adapter's own login flow"
}
