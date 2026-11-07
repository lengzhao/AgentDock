package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/service"
)

func TestServiceSpecUsesLoginShellWithoutCopyingCredentials(t *testing.T) {
	previous := rootEnv
	rootEnv = &env{paths: config.ResolvePaths(t.TempDir())}
	t.Cleanup(func() { rootEnv = previous })
	t.Setenv("LARK_APP_SECRET", "test-secret-not-for-plist")
	spec, err := serviceSpec(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Args) != 4 || strings.Join(spec.Args[:3], " ") != "-l -i -c" || !strings.HasPrefix(spec.Args[3], "exec ") {
		t.Fatalf("service is not using a login interactive shell: %v", spec.Args)
	}
	if len(spec.Env) != 0 {
		t.Fatal("service must load exported environment from shell startup, without environment snapshots")
	}
	plist, err := service.RenderPlist(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plist), "test-secret-not-for-plist") {
		t.Fatal("secret copied into plist")
	}
}

func TestLoginShellLoadsStartupEnvironmentAndPreservesLiteralArguments(t *testing.T) {
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("zsh unavailable")
	}
	home := t.TempDir()
	for name, value := range map[string]string{".zprofile": "export AD_TEST_LOGIN=login\n", ".zshrc": "export AD_TEST_INTERACTIVE=interactive\n"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(home, "agent with ' quote.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s|%s|%s' \"$AD_TEST_LOGIN\" \"$AD_TEST_INTERACTIVE\" \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	literal := "$(touch unexpected); 'quoted' $HOME"
	args := loginShellArgs([]string{script, literal})
	cmd := exec.Command("/bin/zsh", args...)
	cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + home, "PATH=/usr/bin:/bin"}
	cmd.Dir = home
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "login|interactive|"+literal {
		t.Fatalf("unexpected output %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, "unexpected")); !os.IsNotExist(err) {
		t.Fatal("argument executed as shell code")
	}
}
