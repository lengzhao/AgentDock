package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/internal/config"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not portable to windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectReady(t *testing.T) {
	t.Parallel()
	path := script(t, `case "$1" in --version) echo "fake-agent 1.2.3"; exit 0;; *) exit 1;; esac`)
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "fake", Command: []string{path}}}
	cfg.Runtime.DefaultAgent = "fake"

	infos := Detect(context.Background(), cfg, 5*time.Second)
	if len(infos) != 1 {
		t.Fatalf("infos = %d", len(infos))
	}
	info := infos[0]
	if info.State != StateReady {
		t.Fatalf("state = %s (%s)", info.State, info.Detail)
	}
	if info.Version != "fake-agent 1.2.3" {
		t.Fatalf("version = %q", info.Version)
	}
	if info.AuthVerified {
		t.Fatal("detection must never claim authentication")
	}
}

func TestDetectBrokenWhenVersionFails(t *testing.T) {
	t.Parallel()
	path := script(t, `echo "boom" >&2; exit 3`)
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "fake", Command: []string{path}}}
	cfg.Runtime.DefaultAgent = "fake"

	info := Detect(context.Background(), cfg, 5*time.Second)[0]
	if info.State != StateBroken {
		t.Fatalf("state = %s, want broken", info.State)
	}
	if info.Detail == "" {
		t.Fatal("broken state needs a detail")
	}
}

func TestDetectMissing(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "nope", Command: []string{"definitely-not-a-real-binary-xyz"}}}
	cfg.Runtime.DefaultAgent = "nope"
	info := Detect(context.Background(), cfg, 2*time.Second)[0]
	if info.State != StateMissing {
		t.Fatalf("state = %s, want missing", info.State)
	}
}

func TestDetectRejectsRelativePath(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "rel", Command: []string{"./agent"}}}
	cfg.Runtime.DefaultAgent = "rel"
	info := Detect(context.Background(), cfg, 2*time.Second)[0]
	if info.State != StateMissing {
		t.Fatalf("state = %s, want missing", info.State)
	}
}

func TestDetectLauncherIsUnknownNotReady(t *testing.T) {
	t.Parallel()
	path := script(t, `case "$1" in --version) echo "10.0.0"; exit 0;; *) exit 1;; esac`)
	dir := t.TempDir()
	link := filepath.Join(dir, "npx")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "claude-acp", Command: []string{link, "-y", "pkg@1"}}}
	cfg.Runtime.DefaultAgent = "claude-acp"
	info := Detect(context.Background(), cfg, 5*time.Second)[0]
	if info.State != StateUnknown {
		t.Fatalf("state = %s, want unknown (launcher cannot prove the package)", info.State)
	}
	if info.Version == "" {
		t.Fatal("launcher version should still be reported")
	}
}

func TestDetectTimesOut(t *testing.T) {
	t.Parallel()
	path := script(t, `sleep 5`)
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "slow", Command: []string{path}}}
	cfg.Runtime.DefaultAgent = "slow"
	start := time.Now()
	info := Detect(context.Background(), cfg, 200*time.Millisecond)[0]
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("detection ignored the timeout: %s", elapsed)
	}
	if info.State != StateBroken {
		t.Fatalf("state = %s", info.State)
	}
}
