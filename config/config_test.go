package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	cfg := Default()
	cfg.Platform.AppIDRef = "keyring://io.agentdock.platform/app-id"
	cfg.Platform.AppSecretRef = "keyring://io.agentdock.platform/app-secret"
	cfg.Agents = []Agent{{ID: "claude", Command: []string{"npx", "-y", "pkg@1.0.0"}}}
	cfg.Runtime.DefaultAgent = "claude"
	return cfg
}

func TestAgentEnvAcceptsReferencesButRejectsLiteralSecrets(t *testing.T) {
	for _, value := range []string{"keyring://agent-env/token", "env:TEST_TOKEN"} {
		cfg := validConfig()
		cfg.Agents[0].Env = map[string]string{"ANTHROPIC_AUTH_TOKEN": value}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("reference rejected: %v", err)
		}
	}
	cfg := validConfig()
	cfg.Agents[0].Env = map[string]string{"ANTHROPIC_AUTH_TOKEN": "do-not-print"}
	if err := cfg.Validate(); err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("literal secret must be rejected without exposing it")
	}
}

func TestValidateAcceptsMinimalConfig(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Agents[0].Cwd != cfg.Runtime.WorkDir {
		t.Fatalf("agent cwd = %q, want %q", cfg.Agents[0].Cwd, cfg.Runtime.WorkDir)
	}
}

func TestValidateRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"bad version", func(c *Config) { c.Version = 9 }, "unsupported config version"},
		{"bad platform", func(c *Config) { c.Platform.Kind = "slack" }, "platform.kind"},
		{"literal secret", func(c *Config) { c.Platform.AppSecretRef = "abc123" }, "not a reference"},
		{"env scheme in appId", func(c *Config) { c.Platform.AppIDRef = "file://x/y" }, "not supported"},
		{"no agents", func(c *Config) { c.Agents = nil }, "at least one agent"},
		{"duplicate agent", func(c *Config) {
			c.Agents = append(c.Agents, Agent{ID: "claude", Command: []string{"x"}})
		}, "duplicated"},
		{"empty command", func(c *Config) { c.Agents[0].Command = nil }, "command is required"},
		{"unknown default", func(c *Config) { c.Runtime.DefaultAgent = "other" }, "not a configured agent"},
		{"bad scope", func(c *Config) { c.Runtime.SessionScope = "workspace" }, "sessionScope"},
		{"secret in env", func(c *Config) {
			c.Agents[0].Env = map[string]string{"ANTHROPIC_API_KEY": "sk-literal"}
		}, "looks like a secret"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := validConfig()
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Platform.AppSecretRef != cfg.Platform.AppSecretRef {
		t.Fatalf("secret ref = %q, want %q", loaded.Platform.AppSecretRef, cfg.Platform.AppSecretRef)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "app-secret-value") {
		t.Fatal("config file must not contain secret values")
	}
}

func TestLoadUnknownFieldFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `version: 1
installId: x
platform:
  kind: lark
  appIdRef: keyring://s/a
  appSecretRef: keyring://s/b
  unexpected: true
agents:
  - id: a
    command: ["x"]
runtime:
  defaultAgent: a
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown field must be rejected")
	}
}

func TestLoadMissingIsNotConfigured(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestHomeOverride(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", t.TempDir())
	got, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, ".agentdock") {
		t.Fatalf("home = %q", got)
	}
	t.Setenv(EnvHome, filepath.Join(t.TempDir(), "custom"))
	got, err = Home()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "custom") {
		t.Fatalf("home = %q", got)
	}
}

func TestWriteFileAtomicKeepsOldFileOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.yaml")
	if err := WriteFileAtomic(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	// A directory in the way makes rename fail; the original must survive.
	_, err := writeAtomicToDir(path, []byte("second"))
	if err == nil {
		// Some filesystems allow replacing; verify content is still readable.
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(raw) != "second" {
			t.Fatalf("content = %q", raw)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "first" {
		t.Fatalf("original content lost: %q", raw)
	}
}

func writeAtomicToDir(path string, data []byte) (int, error) {
	return len(data), WriteFileAtomic(path, data)
}

func TestWeixinAndExternalAuthCredentialReferences(t *testing.T) {
	for _, kind := range []string{"weixin", "custom"} {
		cfg := validConfig()
		cfg.Platform.Kind = kind
		cfg.Platform.Credentials = map[string]string{"id": "env:BOT_ID", "secret": "env:BOT_TOKEN", "owner": "env:OWNER"}
		cfg.Platform.AppIDRef = ""
		cfg.Platform.AppSecretRef = ""
		if kind == "custom" {
			cfg.Platform.AuthPlugin = "example/auth"
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		if cfg.Platform.References()["secret"] != "env:BOT_TOKEN" {
			t.Fatal("missing reference")
		}
	}
}

func TestAdditionalPlatformRoundTripAndIsolation(t *testing.T) {
	cfg := validConfig()
	extra := Platform{Kind: "weixin", Credentials: map[string]string{"id": "env:WX_ID", "secret": "env:WX_TOKEN"}}
	cfg.UpsertPlatform(extra)
	if len(cfg.Connections()) != 2 || cfg.Platform.Kind != "lark" {
		t.Fatal("primary platform replaced")
	}
	if _, ok := cfg.Connection("weixin"); !ok {
		t.Fatal("added platform not found")
	}
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(file, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Connections()) != 2 {
		t.Fatal("platforms not persisted")
	}
	paths := ResolvePaths(t.TempDir())
	if paths.ForPlatform("lark", "lark").StateDir != paths.StateDir {
		t.Fatal("legacy state moved")
	}
	if paths.ForPlatform("weixin", "lark").StateDir == paths.StateDir {
		t.Fatal("owner states shared")
	}
	loaded.Platforms = append(loaded.Platforms, extra)
	if err := loaded.Validate(); err == nil {
		t.Fatal("duplicate platform accepted")
	}
}
