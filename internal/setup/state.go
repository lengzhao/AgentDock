// Package setup drives the guided configuration flow.
//
// The flow is a sequence of steps whose progress is persisted after each one, so
// cancelling, timing out or re-running never loses valid work and never creates a
// second platform application (SET-02).
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lengzhao/AgentDock/internal/config"
	"github.com/lengzhao/AgentDock/internal/credentials"
)

// Step identifies one unit of work in the flow.
type Step string

const (
	// StepAgents records which adapters the user selected.
	StepAgents Step = "agents"
	// StepAgentLogin records that the selected agents were verified.
	StepAgentLogin Step = "agent-login"
	// StepPlatform records the platform application and its credential references.
	StepPlatform Step = "platform"
	// StepVerify records that credentials were read back and the bot answered.
	StepVerify Step = "verify"
	// StepOwner records that an owner was bound.
	StepOwner Step = "owner"
	// StepService records that the background service was installed.
	StepService Step = "service"
)

// Order is the canonical step order. Progress is a prefix of this list.
var Order = []Step{StepAgents, StepAgentLogin, StepPlatform, StepVerify, StepOwner, StepService}

// State is persisted setup progress. It holds no secrets: only step names,
// non-secret ids and timestamps.
type State struct {
	Version int `json:"version"`
	// Completed lists steps finished so far, in order.
	Completed []Step `json:"completed"`
	// PendingPlatform is true when an application registration was started but its
	// outcome is unknown. A re-run must reconcile before creating another app.
	PendingPlatform bool `json:"pendingPlatform,omitempty"`
	// PendingAppID is the app id from an unconfirmed registration attempt.
	PendingAppID string `json:"pendingAppId,omitempty"`
	// UpdatedAt is the last write time.
	UpdatedAt time.Time `json:"updatedAt"`
}

// StateFile is where progress lives.
func StateFile(p config.Paths) string { return filepath.Join(p.StateDir, "setup.json") }

// LoadState reads progress; a missing file is an empty state.
func LoadState(p config.Paths) (State, error) {
	raw, err := os.ReadFile(StateFile(p))
	if err != nil {
		if os.IsNotExist(err) {
			return State{Version: 1}, nil
		}
		return State{}, fmt.Errorf("read setup state: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse setup state: %w", err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	return st, nil
}

// Save writes progress atomically.
func Save(p config.Paths, st State) error {
	st.UpdatedAt = time.Now().UTC()
	if st.Version == 0 {
		st.Version = 1
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(StateFile(p), raw)
}

// Done reports whether step is complete.
func (s State) Done(step Step) bool {
	for _, done := range s.Completed {
		if done == step {
			return true
		}
	}
	return false
}

// MarkDone records a step as complete exactly once and persists.
func (s *State) MarkDone(p config.Paths, step Step) error {
	if s.Done(step) {
		return nil
	}
	s.Completed = append(s.Completed, step)
	return Save(p, *s)
}

// NextStep returns the first incomplete step in canonical order, or "".
func (s State) NextStep() Step {
	for _, step := range Order {
		if !s.Done(step) {
			return step
		}
	}
	return ""
}

// Reset clears progress but leaves config and credentials untouched, so a user
// can restart the flow without losing an already-working install.
func Reset(p config.Paths) error {
	if err := os.Remove(StateFile(p)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ServiceName is the credential-store service name for the platform app.
const ServiceName = "platform"

// Keyring accounts for the platform app credentials.
const (
	AccountAppID     = "app-id"
	AccountAppSecret = "app-secret"
)

// PlatformRefs returns the credential references a configured install uses.
func PlatformRefs() (appIDRef, appSecretRef string) {
	appIDRef = credentials.KeyringRef(ServiceName, AccountAppID).String()
	appSecretRef = credentials.KeyringRef(ServiceName, AccountAppSecret).String()
	return appIDRef, appSecretRef
}

// StorePlatformCredentials writes both halves of the platform app credential and
// verifies each reads back. When the second write fails the first is left in
// place on purpose: a half-written pair is repairable, while deleting a
// credential that may still be in use is not reversible.
func StorePlatformCredentials(ctx context.Context, store credentials.Store, appID, appSecret string) error {
	if appID == "" || appSecret == "" {
		return fmt.Errorf("app id and app secret are both required")
	}
	if err := store.Set(ctx, ServiceName, AccountAppID, appID); err != nil {
		return fmt.Errorf("store app id: %w", err)
	}
	if err := store.Set(ctx, ServiceName, AccountAppSecret, appSecret); err != nil {
		return fmt.Errorf("store app secret: %w", err)
	}
	want := map[string]string{AccountAppID: appID, AccountAppSecret: appSecret}
	for _, account := range []string{AccountAppID, AccountAppSecret} {
		got, err := store.Get(ctx, ServiceName, account)
		if err != nil {
			return fmt.Errorf("read back %s: %w", account, err)
		}
		if got != want[account] {
			return fmt.Errorf("read back %s returned a different value", account)
		}
	}
	return nil
}
