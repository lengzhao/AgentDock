package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Launchd manages a per-user LaunchAgent.
//
// launchctl semantics are the reason this is a hand-written wrapper instead of a
// cross-platform service library: `bootstrap` takes a domain and a plist path,
// `bootout` needs the same pair, and "already bootstrapped" is an error code, not
// a state. Getting those exactly right matters more than covering systemd here.
type Launchd struct {
	// Label is used for bootout and status lookups.
	Label string
	// PlistPath is where the definition lives, normally
	// ~/Library/LaunchAgents/<label>.plist.
	PlistPath string
	// Domain is normally gui/<uid>.
	Domain string
	// Run executes launchctl; injected for tests.
	Run func(ctx context.Context, args ...string) (stdout string, stderr string, err error)
	// Load returns the plist bytes for Status; injected for tests.
}

// NewLaunchd returns a launcher for the current user.
func NewLaunchd(label string) (*Launchd, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	if label == "" {
		return nil, errors.New("launchd label is required")
	}
	l := &Launchd{
		Label:     label,
		PlistPath: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		Domain:    "gui/" + strconv.Itoa(os.Getuid()),
	}
	l.Run = execLaunchctl
	return l, nil
}

func execLaunchctl(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// RenderPlist renders the LaunchAgent definition. Secrets are not accepted here:
// Spec.Validate rejects them, and the process resolves them at runtime.
func RenderPlist(spec Spec) ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	var envKeys []string
	for k := range spec.Env {
		envKeys = append(envKeys, k)
	}
	sortStrings(envKeys)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	writeKey := func(k, v string) {
		b.WriteString("  <key>" + xmlEscape(k) + "</key>\n")
		b.WriteString("  <string>" + xmlEscape(v) + "</string>\n")
	}
	writeBool := func(k string, v bool) {
		b.WriteString("  <key>" + xmlEscape(k) + "</key>\n")
		if v {
			b.WriteString("  <true/>\n")
		} else {
			b.WriteString("  <false/>\n")
		}
	}
	writeKey("Label", spec.Label)
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range append([]string{spec.Executable}, spec.Args...) {
		b.WriteString("    <string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("  </array>\n")
	if spec.WorkingDir != "" {
		writeKey("WorkingDirectory", spec.WorkingDir)
	}
	writeBool("RunAtLoad", spec.KeepAlive)
	writeBool("KeepAlive", spec.KeepAlive)
	if spec.ThrottleSeconds > 0 {
		b.WriteString("  <key>ThrottleInterval</key>\n")
		b.WriteString("  <integer>" + strconv.Itoa(spec.ThrottleSeconds) + "</integer>\n")
	}
	b.WriteString("  <key>ProcessType</key>\n  <string>Interactive</string>\n")
	if spec.LogDir != "" {
		writeKey("StandardOutPath", filepath.Join(spec.LogDir, "service.out.log"))
		writeKey("StandardErrorPath", filepath.Join(spec.LogDir, "service.err.log"))
	}
	if len(spec.Env) > 0 || spec.HOME != "" {
		b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		if spec.HOME != "" {
			b.WriteString("    <key>HOME</key>\n    <string>" + xmlEscape(spec.HOME) + "</string>\n")
		}
		for _, k := range envKeys {
			b.WriteString("    <key>" + xmlEscape(k) + "</key>\n    <string>" + xmlEscape(spec.Env[k]) + "</string>\n")
		}
		b.WriteString("  </dict>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String()), nil
}

// Install writes the plist and, when already loaded, reloads it so the new
// definition takes effect. Repeated calls are idempotent.
func (l *Launchd) Install(ctx context.Context, spec Spec) error {
	if spec.Label != "" && spec.Label != l.Label {
		return fmt.Errorf("spec label %q does not match manager label %q", spec.Label, l.Label)
	}
	spec.Label = l.Label
	plist, err := RenderPlist(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.PlistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(l.PlistPath, plist, 0o600); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	st, err := l.Status(ctx)
	if err != nil {
		return err
	}
	if st.State == StateRunning || st.State == StateStopped {
		// Already bootstrapped: reload so the definition we just wrote is the one
		// launchd uses. Failure to bootout a loaded job is reported, not ignored.
		if err := l.bootout(ctx); err != nil {
			return fmt.Errorf("reload service definition: %w", err)
		}
	}
	return nil
}

// Uninstall stops the job and removes the plist. Missing files are not errors.
func (l *Launchd) Uninstall(ctx context.Context) error {
	if err := l.bootout(ctx); err != nil {
		return err
	}
	if err := os.Remove(l.PlistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// Start bootstraps the job. Bootstrapping an already-running job is not an error.
func (l *Launchd) Start(ctx context.Context) error {
	if _, err := os.Stat(l.PlistPath); err != nil {
		return fmt.Errorf("service is not installed: %w", err)
	}
	st, err := l.Status(ctx)
	if err != nil {
		return err
	}
	if st.State == StateRunning {
		return nil
	}
	stdout, stderr, err := l.Run(ctx, "bootstrap", l.Domain, l.PlistPath)
	if err != nil {
		// 37 == "service already loaded" on current macOS.
		if strings.Contains(stdout+stderr, "Bootstrap failed: 37") || strings.Contains(stdout+stderr, "already loaded") {
			_, _, kerr := l.Run(ctx, "kickstart", "-k", l.Domain+"/"+l.Label)
			return kerr
		}
		return fmt.Errorf("launchctl bootstrap: %v: %s%s", err, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	}
	return nil
}

// Stop boots the job out without removing the plist.
func (l *Launchd) Stop(ctx context.Context) error {
	return l.bootout(ctx)
}

// Restart performs a stop/start cycle.
func (l *Launchd) Restart(ctx context.Context) error {
	if err := l.Stop(ctx); err != nil {
		return err
	}
	if err := l.Start(ctx); err != nil {
		return err
	}
	_, err := WaitRunning(ctx, l, 15*time.Second)
	return err
}

// Status parses `launchctl print` for the job.
func (l *Launchd) Status(ctx context.Context) (Status, error) {
	if _, err := os.Stat(l.PlistPath); err != nil {
		if os.IsNotExist(err) {
			return Status{State: StateNotInstalled, Detail: "no plist at " + l.PlistPath}, nil
		}
		return Status{}, fmt.Errorf("stat plist: %w", err)
	}
	stdout, stderr, err := l.Run(ctx, "print", l.Domain+"/"+l.Label)
	if err != nil {
		if strings.Contains(stdout+stderr, "Could not find service") {
			return Status{State: StateStopped, Detail: "installed but not loaded"}, nil
		}
		return Status{State: StateUnknown, Detail: strings.TrimSpace(stderr)}, nil
	}
	st := Status{State: StateStopped}
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "pid = ") {
			if pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "pid = "))); err == nil {
				st.PID = pid
				st.State = StateRunning
			}
			continue
		}
		if strings.HasPrefix(trimmed, "last exit code = ") {
			if code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "last exit code = "))); err == nil {
				st.LastExitCode = code
			}
			continue
		}
		if strings.HasPrefix(trimmed, "state = ") {
			if st.State != StateRunning {
				st.Detail = strings.TrimSpace(strings.TrimPrefix(trimmed, "state = "))
				if st.Detail != "running" {
					st.State = StateStopped
				}
			}
		}
	}
	return st, nil
}

func (l *Launchd) bootout(ctx context.Context) error {
	_, stderr, err := l.Run(ctx, "bootout", l.Domain, l.PlistPath)
	if err != nil {
		// Not loaded is the desired state, so it is not an error here.
		if strings.Contains(stderr, "Could not find service") || strings.Contains(stderr, "No such process") {
			return nil
		}
		// bootout by label also works when the plist on disk was replaced.
		_, stderr2, err2 := l.Run(ctx, "bootout", l.Domain+"/"+l.Label)
		if err2 != nil && !strings.Contains(stderr2, "Could not find service") {
			return fmt.Errorf("launchctl bootout: %v: %s", err, strings.TrimSpace(stderr))
		}
	}
	return nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
