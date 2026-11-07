// Package agent detects and describes the agent adapters an installation uses.
//
// SET-03 requires distinguishing "not installed", "installed but broken" and
// "works". Detection here is deliberately honest about its limits: it resolves
// the executable and reads its version, and never claims authentication works.
// Only a real end-to-end message (`agentdock verify`) proves that.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lengzhao/AgentDock/config"
)

// State is the detected adapter state.
type State string

const (
	// StateMissing means the executable could not be resolved.
	StateMissing State = "missing"
	// StateBroken means it resolved but would not run.
	StateBroken State = "broken"
	// StateReady means it resolved and reported a version.
	StateReady State = "ready"
	// StateUnknown means it resolved but version detection was inconclusive.
	StateUnknown State = "unknown"
)

// Info is one agent's detection result.
type Info struct {
	ID      string
	State   State
	Path    string
	Version string
	// Command is the configured argv.
	Command []string
	// Detail explains a non-ready state.
	Detail string
	// Auth not verified by detection; kept explicit so callers cannot read
	// StateReady as "logged in".
	AuthVerified bool
}

// Detect probes every configured agent. It never blocks longer than Timeout per
// agent and never fails the whole call: a broken agent is reported, not fatal.
func Detect(ctx context.Context, cfg config.Config, timeout time.Duration) []Info {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	out := make([]Info, 0, len(cfg.Agents))
	for _, a := range cfg.Agents {
		out = append(out, detectOne(ctx, a, timeout))
	}
	return out
}

func detectOne(ctx context.Context, a config.Agent, timeout time.Duration) Info {
	info := Info{ID: a.ID, Command: a.Command}
	if len(a.Command) == 0 {
		info.State = StateMissing
		info.Detail = "no command configured"
		return info
	}
	resolved, err := resolveExecutable(a.Command[0])
	if err != nil {
		info.State = StateMissing
		info.Detail = fmt.Sprintf("%q not found: %v", a.Command[0], err)
		return info
	}
	info.Path = resolved

	// npx/uvx are launchers; the interesting binary is the pinned package, which
	// detection cannot see without running it. Report the launcher and say so.
	if base := filepath.Base(a.Command[0]); base == "npx" || base == "uvx" {
		info.State = StateUnknown
		info.Detail = "launcher found; the adapter package itself is fetched on first run"
		if version := launcherVersion(ctx, resolved, timeout); version != "" {
			info.Version = version
		}
		return info
	}

	version, err := runVersion(ctx, resolved, timeout)
	if err != nil {
		info.State = StateBroken
		info.Detail = err.Error()
		return info
	}
	info.Version = version
	info.State = StateReady
	return info
}

func resolveExecutable(name string) (string, error) {
	if strings.Contains(name, "/") {
		if !filepath.IsAbs(name) {
			return "", fmt.Errorf("relative path %q; use an absolute path", name)
		}
		return name, nil
	}
	return exec.LookPath(name)
}

var versionArgs = [][]string{{"--version"}, {"version"}, {"-v"}}

func runVersion(ctx context.Context, path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for _, args := range versionArgs {
		cmd := exec.CommandContext(ctx, path, args...)
		// Descendants may keep output pipes open after the probe is killed.
		cmd.WaitDelay = 100 * time.Millisecond
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			line := firstLine(stdout.String())
			if line == "" {
				line = firstLine(stderr.String())
			}
			return line, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", fmt.Errorf("version probe timed out after %s", timeout)
		}
	}
	return "", fmt.Errorf("version probe failed: %v", lastErr)
}

func launcherVersion(ctx context.Context, path string, timeout time.Duration) string {
	v, err := runVersion(ctx, path, timeout)
	if err != nil {
		return ""
	}
	return v
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len(trimmed) > 120 {
				trimmed = trimmed[:120]
			}
			return trimmed
		}
	}
	return ""
}

// DefaultAgent returns the info for the configured default agent.
func DefaultAgent(infos []Info, cfg config.Config) (Info, bool) {
	for _, info := range infos {
		if info.ID == cfg.Runtime.DefaultAgent {
			return info, true
		}
	}
	return Info{}, false
}

// ErrNoUsableAgent means setup cannot continue: every selected adapter is missing.
var ErrNoUsableAgent = errors.New("no usable agent adapter")
