package cli

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// userLoginShell follows the account setting, which is more reliable than
// SHELL when installation is started outside the user's terminal.
func userLoginShell() string {
	if runtime.GOOS == "darwin" {
		if u, err := user.Current(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/dscl", ".", "-read", "/Users/"+u.Username, "UserShell")
			cmd.WaitDelay = time.Second
			if out, err := cmd.Output(); err == nil {
				for _, line := range strings.Split(string(out), "\n") {
					if value, ok := strings.CutPrefix(strings.TrimSpace(line), "UserShell:"); ok {
						if shell := strings.TrimSpace(value); usableShell(shell) {
							return shell
						}
					}
				}
			}
		}
	}
	if shell := os.Getenv("SHELL"); usableShell(shell) {
		return shell
	}
	return "/bin/sh"
}

func usableShell(shell string) bool {
	if !filepath.IsAbs(shell) || strings.ContainsAny(shell, "\r\n") {
		return false
	}
	info, err := os.Stat(shell)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

// Arguments are shell literals, never source. exec gives launchd the actual
// daemon PID and forwards lifecycle signals without an intermediate shell.
func loginShellArgs(command []string) []string {
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
	}
	return []string{"-l", "-i", "-c", "exec " + strings.Join(quoted, " ")}
}
