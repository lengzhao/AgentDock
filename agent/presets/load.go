package presets

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lengzhao/AgentDock/agent"
)

type document struct {
	Version int           `json:"version"`
	Presets []presetEntry `json:"presets"`
}

type presetEntry struct {
	ID             string   `json:"id"`
	Aliases        []string `json:"aliases,omitempty"`
	LoginHint      string   `json:"loginHint,omitempty"`
	OfflineCommand []string `json:"offlineCommand,omitempty"`
	Resolver       string   `json:"resolver,omitempty"`
	AuthMethod     string   `json:"authMethod,omitempty"`
}

func init() {
	if err := loadBytes(defaultJSON); err != nil {
		panic("agent presets: " + err.Error())
	}
}

// DefaultBytes returns the embedded preset document for seeding a new install.
func DefaultBytes() []byte { return append([]byte(nil), defaultJSON...) }

// LoadFile merges presets from path into the registry (later entries override ids).
func LoadFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return loadBytes(raw)
}

// EnsureUserFile writes the embedded default when path does not exist.
func EnsureUserFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, defaultJSON, 0o644)
}

func loadBytes(raw []byte) error {
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse agent presets: %w", err)
	}
	if doc.Version != 1 {
		return fmt.Errorf("unsupported agent presets version %d", doc.Version)
	}
	for _, entry := range doc.Presets {
		if entry.ID == "" {
			return fmt.Errorf("agent preset entry missing id")
		}
		if entry.Resolver != "" {
			return fmt.Errorf("agent preset %q: resolver %q was removed; use offlineCommand (see default.json)", entry.ID, entry.Resolver)
		}
		p := agent.Preset{
			ID:             entry.ID,
			Aliases:        entry.Aliases,
			LoginHint:      entry.LoginHint,
			OfflineCommand: entry.OfflineCommand,
			AuthMethod:     entry.AuthMethod,
		}
		if err := agent.RegisterOrReplace(p); err != nil {
			return err
		}
	}
	return nil
}
