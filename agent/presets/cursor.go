package presets

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/lengzhao/AgentDock/config"
)

// cursorPreset selects the user's installed native Cursor CLI. The ACP registry
// archive's relative cmd is not an installed executable and must not replace it.
func cursorPreset() (config.Agent, error) {
	executable, err := exec.LookPath("cursor-agent")
	if err != nil {
		executable = "cursor-agent"
		if candidate, err := exec.LookPath("agent"); err == nil && isCursorACP(candidate) {
			executable = candidate
		}
	}
	return config.Agent{ID: "cursor", Command: []string{executable, "acp"}, AuthMethod: "cursor_login"}, nil
}

func isCursorACP(executable string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "acp", "--help")
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	help := strings.ToLower(string(out))
	return err == nil && strings.Contains(help, "cursor") && strings.Contains(help, "acp")
}
