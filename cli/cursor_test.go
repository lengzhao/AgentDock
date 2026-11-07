package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lengzhao/AgentDock/config"
)

func installFakeCursorCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 2026.11.05\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func TestAgentsAddCursorPreservesOtherAgentsAndPlatforms(t *testing.T) {
	installFakeCursorCLI(t)
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:APP_ID"
	cfg.Platform.AppSecretRef = "env:APP_SECRET"
	cfg.Platforms = []config.Platform{{Kind: "weixin", Credentials: map[string]string{"id": "env:WX_ID", "secret": "env:WX_TOKEN"}}}
	cfg.Agents = []config.Agent{{ID: "claude-acp", Command: []string{"npx", "claude"}, Env: map[string]string{"PATH": "keep-me"}}}
	cfg.Runtime.DefaultAgent = "claude-acp"
	if err := config.Save(rootEnv.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	cmd := newAgentsCmd()
	cmd.SetArgs([]string{"add", "cursor"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, err := config.Load(rootEnv.paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Agents) != 2 || actual.Runtime.DefaultAgent != "claude-acp" || actual.Agents[0].Env["PATH"] != "keep-me" || !reflect.DeepEqual(actual.Platforms, cfg.Platforms) || !reflect.DeepEqual(actual.Platform, cfg.Platform) {
		t.Fatal("adding Cursor replaced existing configuration")
	}
	cmd = newAgentsCmd()
	cmd.SetArgs([]string{"add", "cursor"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, err = config.Load(rootEnv.paths.ConfigFile)
	if err != nil || len(actual.Agents) != 2 {
		t.Fatal("duplicate agent added")
	}
}

func TestAgentsAddPreservesConcurrentConfigUpdate(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-started")
	release := filepath.Join(dir, "release")
	t.Setenv("CURSOR_TEST_MARKER", marker)
	t.Setenv("CURSOR_TEST_RELEASE", release)
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\n: > \"$CURSOR_TEST_MARKER\"\nwhile [ ! -e \"$CURSOR_TEST_RELEASE\" ]; do /bin/sleep 0.01; done\necho 2026.11.05\n"
	if err := os.WriteFile(filepath.Join(dir, "cursor-agent"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:ID"
	cfg.Platform.AppSecretRef = "env:SECRET"
	cfg.Agents = []config.Agent{{ID: "claude-acp", Command: []string{"npx"}}}
	cfg.Runtime.DefaultAgent = "claude-acp"
	if err := config.Save(rootEnv.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := newAgentsCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"add", "cursor"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	defer os.WriteFile(release, nil, 0600)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("probe did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cfg.Agents[0].Env = map[string]string{"NEW_SETTING": "latest"}
	if err := config.Save(rootEnv.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	actual, err := config.Load(rootEnv.paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Agents[0].Env["NEW_SETTING"] != "latest" {
		t.Fatal("agent probe overwrote concurrent environment update")
	}
}

func TestExistingCursorDefaultDoesNotDependOnCurrentPath(t *testing.T) {
	path := installFakeCursorCLI(t)
	t.Setenv("PATH", t.TempDir())
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	cfg := config.Default()
	cfg.Platform.AppIDRef = "env:ID"
	cfg.Platform.AppSecretRef = "env:SECRET"
	existing := config.Agent{ID: "cursor", Command: []string{path, "acp", "--custom"}, Env: map[string]string{"PATH": "preserve"}, AuthMethod: "cursor_login", Cwd: "custom-work"}
	cfg.Agents = []config.Agent{{ID: "claude-acp", Command: []string{"npx"}}, existing}
	cfg.Runtime.DefaultAgent = "claude-acp"
	if err := config.Save(rootEnv.paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}
	cmd := newAgentsCmd()
	cmd.SetArgs([]string{"add", "cursor-acp", "--default"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, err := config.Load(rootEnv.paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Runtime.DefaultAgent != "cursor" || !reflect.DeepEqual(actual.Agents[1], existing) {
		t.Fatal("saved Cursor configuration was replaced")
	}
}
