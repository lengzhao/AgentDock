// Package assembly constructs replaceable application dependencies through PluginKit.
// Plugins are registered by imports in the executable; consumers request a contract.
package assembly

import (
	"context"
	"fmt"

	"github.com/lengzhao/pluginkit/build"
)

// Open builds a registered leaf plugin and validates its consumer contract.
// Use runtime.BuildWithOverrides for plugins requiring a dependency graph.
// Leaf plugins must not own background resources requiring shutdown.
func Open[T any](ctx context.Context, kind string, config map[string]any) (T, error) {
	value, _, err := build.Build[T](ctx, map[string]any{
		"slot": map[string]any{"use": kind, "config": config},
	}, "slot")
	if err != nil {
		return value, fmt.Errorf("assemble %s: %w", kind, err)
	}
	return value, nil
}
