package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func baseSpec(t *testing.T) Spec {
	t.Helper()
	return Spec{
		Label:           "io.agentdock.agent",
		Executable:      "/usr/local/bin/agentdock",
		Args:            []string{"run", "--home", "/Users/x/.agentdock"},
		WorkingDir:      "/Users/x/.agentdock",
		LogDir:          "/Users/x/.agentdock/logs",
		HOME:            "/Users/x",
		KeepAlive:       true,
		ThrottleSeconds: 10,
	}
}

func TestRenderPlist(t *testing.T) {
	t.Parallel()
	plist, err := RenderPlist(baseSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	body := string(plist)
	for _, want := range []string{
		"<key>Label</key>", "io.agentdock.agent",
		"<key>ProgramArguments</key>", "/usr/local/bin/agentdock", "<string>run</string>",
		"<key>KeepAlive</key>", "<true/>",
		"<key>ThrottleInterval</key>\n  <integer>10</integer>",
		"<key>EnvironmentVariables</key>",
		"service.err.log",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("plist missing %q:\n%s", want, body)
		}
	}
}

func TestRenderPlistRejectsSecrets(t *testing.T) {
	t.Parallel()
	spec := baseSpec(t)
	spec.Env = map[string]string{"LARK_APP_SECRET": "abc"}
	if _, err := RenderPlist(spec); err == nil {
		t.Fatal("a secret in the plist env must be rejected")
	}
}

func TestSpecValidateRequiresAbsolutePaths(t *testing.T) {
	t.Parallel()
	spec := baseSpec(t)
	spec.Executable = "agentdock"
	if err := spec.Validate(); err == nil {
		t.Fatal("relative executable must be rejected")
	}
	spec = baseSpec(t)
	spec.Executable = ""
	if err := spec.Validate(); err == nil {
		t.Fatal("empty executable must be rejected")
	}
}

func TestRenderPlistEscapesXML(t *testing.T) {
	t.Parallel()
	spec := baseSpec(t)
	spec.Args = []string{"--note", `a&b<c>"d"`}
	plist, err := RenderPlist(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plist), `a&b<c>`) {
		t.Fatalf("unescaped XML in plist:\n%s", plist)
	}
}

// fakeLaunchctl returns canned output per call.
type fakeLaunchctl struct {
	calls  [][]string
	stdout string
	stderr string
	err    error
}

func (f *fakeLaunchctl) run(_ context.Context, args ...string) (string, string, error) {
	f.calls = append(f.calls, args)
	return f.stdout, f.stderr, f.err
}

func TestStatusStates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plistPath := filepath.Join(dir, "io.agentdock.agent.plist")

	t.Run("not installed", func(t *testing.T) {
		f := &fakeLaunchctl{}
		l := &Launchd{Label: "io.agentdock.agent", PlistPath: plistPath, Domain: "gui/501", Run: f.run}
		st, err := l.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.State != StateNotInstalled {
			t.Fatalf("state = %s", st.State)
		}
		if len(f.calls) != 0 {
			t.Fatalf("launchctl must not be called for a missing plist: %v", f.calls)
		}
	})

	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("running", func(t *testing.T) {
		f := &fakeLaunchctl{stdout: "gui/501/io.agentdock.agent = {\n\tstate = running\n\tpid = 4242\n\tlast exit code = 0\n}\n"}
		l := &Launchd{Label: "io.agentdock.agent", PlistPath: plistPath, Domain: "gui/501", Run: f.run}
		st, err := l.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.State != StateRunning || st.PID != 4242 {
			t.Fatalf("status = %+v", st)
		}
	})

	t.Run("not loaded", func(t *testing.T) {
		f := &fakeLaunchctl{stderr: "Could not find service \"io.agentdock.agent\" in domain for user gui: 501", err: errors.New("exit 113")}
		l := &Launchd{Label: "io.agentdock.agent", PlistPath: plistPath, Domain: "gui/501", Run: f.run}
		st, err := l.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.State != StateStopped {
			t.Fatalf("status = %+v", st)
		}
	})
}

func TestStartIsIdempotentWhenRunning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plistPath := filepath.Join(dir, "io.agentdock.agent.plist")
	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeLaunchctl{stdout: "state = running\npid = 7\n"}
	l := &Launchd{Label: "io.agentdock.agent", PlistPath: plistPath, Domain: "gui/501", Run: f.run}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0][0] != "print" {
		t.Fatalf("calls = %v, want a single status probe", f.calls)
	}
}

func TestStartWithoutPlistFails(t *testing.T) {
	t.Parallel()
	f := &fakeLaunchctl{}
	l := &Launchd{Label: "io.agentdock.agent", PlistPath: filepath.Join(t.TempDir(), "x.plist"), Domain: "gui/501", Run: f.run}
	if err := l.Start(context.Background()); err == nil {
		t.Fatal("start without an installed definition must fail")
	}
}

func TestBootoutTreatsMissingJobAsSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plistPath := filepath.Join(dir, "io.agentdock.agent.plist")
	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeLaunchctl{stderr: "Could not find service", err: errors.New("exit 3")}
	l := &Launchd{Label: "io.agentdock.agent", PlistPath: plistPath, Domain: "gui/501", Run: f.run}
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("stop = %v", err)
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatal("stop must not remove the plist")
	}
	if err := l.Uninstall(context.Background()); err != nil {
		t.Fatalf("uninstall = %v", err)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatal("uninstall must remove the plist")
	}
}

func TestInstallDoesNotUnloadAnUnloadedDefinition(t *testing.T) {
	l := &Launchd{Label: "io.agentdock.agent", PlistPath: filepath.Join(t.TempDir(), "agent.plist"), Domain: "gui/501"}
	l.Run = func(_ context.Context, args ...string) (string, string, error) {
		if args[0] != "print" {
			t.Errorf("unexpected %s for unloaded job", args[0])
		}
		return "", "Could not find service", errors.New("exit 113")
	}
	if err := l.Install(context.Background(), baseSpec(t)); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReloadsLoadedStoppedJob(t *testing.T) {
	l := &Launchd{Label: "io.agentdock.agent", PlistPath: filepath.Join(t.TempDir(), "agent.plist"), Domain: "gui/501"}
	bootouts := 0
	l.Run = func(_ context.Context, args ...string) (string, string, error) {
		if args[0] == "bootout" {
			bootouts++
			return "", "", nil
		}
		return "state = waiting\nlast exit code = 1\n", "", nil
	}
	if err := l.Install(context.Background(), baseSpec(t)); err != nil {
		t.Fatal(err)
	}
	if bootouts != 1 {
		t.Fatalf("bootout count = %d, want 1", bootouts)
	}
}
