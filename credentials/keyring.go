package credentials

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	gokeyring "github.com/zalando/go-keyring"
)

// KeyringStore is the default Store backed by the OS credential store through
// zalando/go-keyring. On macOS that is /usr/bin/security, so a LaunchAgent may
// be prompted on first access (see docs research §5 and SRV-02).
type KeyringStore struct {
	// ServicePrefix is prepended to every service name so one AgentDock install
	// cannot collide with unrelated entries.
	ServicePrefix string

	mu sync.Mutex
}

// NewKeyringStore returns a keyring-backed store.
func NewKeyringStore(servicePrefix string) *KeyringStore {
	if servicePrefix == "" {
		servicePrefix = "io.agentdock"
	}
	return &KeyringStore{ServicePrefix: servicePrefix}
}

func (s *KeyringStore) service(service string) string {
	if s.ServicePrefix == "" {
		return service
	}
	return s.ServicePrefix + "." + service
}

// Get implements Store.
func (s *KeyringStore) Get(_ context.Context, service, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := gokeyring.Get(s.service(service), account)
	if err != nil {
		if errors.Is(err, gokeyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s/%s", ErrNotFound, service, account)
		}
		return "", fmt.Errorf("%w: read %s/%s: %v", ErrUnavailable, service, account, err)
	}
	return value, nil
}

// Set implements Store. It verifies the value reads back unchanged so a silent
// keychain failure cannot be mistaken for a saved credential (SEC-01).
func (s *KeyringStore) Set(_ context.Context, service, account, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := gokeyring.Set(s.service(service), account, secret); err != nil {
		return fmt.Errorf("%w: write %s/%s: %v", ErrUnavailable, service, account, err)
	}
	got, err := gokeyring.Get(s.service(service), account)
	if err != nil {
		return fmt.Errorf("%w: read back %s/%s: %v", ErrUnavailable, service, account, err)
	}
	if got != secret {
		return fmt.Errorf("%w: read back %s/%s returned a different value", ErrUnavailable, service, account)
	}
	return nil
}

// Delete implements Store.
func (s *KeyringStore) Delete(_ context.Context, service, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := gokeyring.Delete(s.service(service), account)
	if err != nil && !errors.Is(err, gokeyring.ErrNotFound) {
		return fmt.Errorf("%w: delete %s/%s: %v", ErrUnavailable, service, account, err)
	}
	return nil
}

// Status implements Store by doing a write/read/delete round trip on a probe
// entry. The probe value is a fixed string, never a real secret.
func (s *KeyringStore) Status(ctx context.Context) Status {
	backend := "go-keyring"
	switch runtime.GOOS {
	case "darwin":
		backend = "macos-keychain"
	case "linux":
		backend = "secret-service"
	case "windows":
		backend = "windows-credential-manager"
	}
	const (
		probeService = "diagnostics"
		probeAccount = "probe"
		probeValue   = "agentdock-probe"
	)
	if err := s.Set(ctx, probeService, probeAccount, probeValue); err != nil {
		return Status{Backend: backend, Available: false, Detail: err.Error()}
	}
	got, err := s.Get(ctx, probeService, probeAccount)
	if err != nil || got != probeValue {
		_ = s.Delete(ctx, probeService, probeAccount)
		return Status{Backend: backend, Available: false, Detail: "probe read-back failed"}
	}
	if err := s.Delete(ctx, probeService, probeAccount); err != nil {
		return Status{Backend: backend, Available: true, Detail: "unable to delete probe entry: " + err.Error()}
	}
	return Status{Backend: backend, Available: true}
}
