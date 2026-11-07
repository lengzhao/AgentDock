// Package service manages AgentDock as a user-level background service.
//
// The slot contract is the ServiceManager interface. The only first-version
// implementation is launchd (agentdock/service-launchd in the docs' slot
// table); a systemd or Windows implementation can be added later behind the
// same interface without touching call sites.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// State is the observed service state, deliberately coarser than launchd's own
// vocabulary so other backends can map onto it.
type State string

const (
	// StateUnknown means the backend could not tell.
	StateUnknown State = "unknown"
	// StateNotInstalled means no service definition exists.
	StateNotInstalled State = "not-installed"
	// StateStopped means installed but not running.
	StateStopped State = "stopped"
	// StateRunning means installed and running.
	StateRunning State = "running"
)

// Status is a snapshot of the service.
type Status struct {
	State State
	// PID is the running process id, 0 when not running.
	PID int
	// LastExitCode is the last exit status launchd recorded, when known.
	LastExitCode int
	// Detail carries backend-specific diagnostics, already redacted.
	Detail string
}

// Spec is everything needed to render a service definition.
type Spec struct {
	// Label is the service label, e.g. io.agentdock.agent.
	Label string
	// Executable is the absolute path of the agentdock binary.
	Executable string
	// Args are passed to the executable.
	Args []string
	// WorkingDir is the process working directory.
	WorkingDir string
	// LogDir receives stdout/stderr logs.
	LogDir string
	// HOME is exported to the process so the keychain and agent CLIs resolve the
	// same user identity as the foreground run.
	HOME string
	// Env holds extra non-secret environment variables.
	Env map[string]string
	// KeepAlive restarts the process when it exits.
	KeepAlive bool
	// ThrottleSeconds is the minimum interval between restarts.
	ThrottleSeconds int
}

// Validate rejects specs that would produce a broken or unsafe definition.
func (s Spec) Validate() error {
	if s.Label == "" {
		return errors.New("service label is required")
	}
	if s.Executable == "" || s.Executable[0] != '/' {
		return fmt.Errorf("service executable must be an absolute path, got %q", s.Executable)
	}
	if s.WorkingDir != "" && s.WorkingDir[0] != '/' {
		return fmt.Errorf("service working directory must be absolute, got %q", s.WorkingDir)
	}
	// SEC-02: secrets must never reach the plist, which is world-readable.
	for k, v := range s.Env {
		if looksSecret(k) && v != "" {
			return fmt.Errorf("service env %q looks like a secret; secrets belong in the credential store", k)
		}
	}
	return nil
}

// ServiceManager installs and controls the background service.
type ServiceManager interface {
	Install(ctx context.Context, spec Spec) error
	Uninstall(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
}

// ErrNotSupported is returned by a backend that cannot run on this platform.
var ErrNotSupported = errors.New("service backend not supported on this platform")

// WaitRunning polls Status until the service reports running or the timeout
// expires. launchd returns from bootstrap before the process is necessarily up.
func WaitRunning(ctx context.Context, mgr ServiceManager, timeout time.Duration) (Status, error) {
	deadline := time.Now().Add(timeout)
	var last Status
	for {
		st, err := mgr.Status(ctx)
		if err != nil {
			return last, err
		}
		last = st
		if st.State == StateRunning {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("service did not become running within %s (state=%s)", timeout, st.State)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

var secretHints = []string{"secret", "token", "password", "api_key", "apikey", "credential"}

func looksSecret(name string) bool {
	lower := strings.ToLower(name)
	for _, hint := range secretHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
