// Package credentials is AgentDock's credential slot.
//
// Slot contract (docs/research/2026-11-07-third-party-components.md §5): the
// interfaces here are AgentDock's own, small, and never mention a third-party
// type. Adapters such as keyring.go keep the backing library private so the
// implementation can be swapped without touching call sites.
package credentials

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound means the backend has no value for the requested account.
var ErrNotFound = errors.New("credential not found")

// Store reads and writes secrets. Implementations must not log values.
type Store interface {
	// Get returns the secret or ErrNotFound.
	Get(ctx context.Context, service, account string) (string, error)
	// Set writes the secret and verifies it reads back identically.
	Set(ctx context.Context, service, account, secret string) error
	// Delete removes the secret. Deleting a missing entry is not an error.
	Delete(ctx context.Context, service, account string) error
	// Status reports whether the backend looks usable right now.
	Status(ctx context.Context) Status
}

// Status is a backend health report safe to print.
type Status struct {
	// Backend names the implementation, e.g. "macos-keychain".
	Backend string
	// Available is false when the backend cannot be used at all.
	Available bool
	// Detail explains an unavailable or degraded state.
	Detail string
}

// ErrUnavailable wraps backend-level failures that are not about one secret,
// such as a locked keychain or a missing D-Bus session.
var ErrUnavailable = errors.New("credential backend unavailable")

// Resolver turns references into values. It is what the runtime wires into
// agent and platform config assembly.
type Resolver struct {
	store Store
	// env allows env: references; nil disables them.
	env func(string) (string, bool)
}

// NewResolver builds a Resolver over store.
func NewResolver(store Store, env func(string) (string, bool)) *Resolver {
	return &Resolver{store: store, env: env}
}

// Resolve returns the value for a reference string.
func (r *Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	parsed, err := ParseRef(ref)
	if err != nil {
		return "", err
	}
	switch parsed.Kind {
	case RefKeyring:
		if r.store == nil {
			return "", fmt.Errorf("%w: no credential store configured", ErrUnavailable)
		}
		value, err := r.store.Get(ctx, parsed.Service, parsed.Account)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", parsed.String(), err)
		}
		return value, nil
	case RefEnv:
		if r.env == nil {
			return "", fmt.Errorf("resolve %s: environment lookup disabled", parsed.String())
		}
		value, ok := r.env(parsed.Name)
		if !ok || value == "" {
			return "", fmt.Errorf("resolve %s: %w", parsed.String(), ErrNotFound)
		}
		return value, nil
	default:
		return "", fmt.Errorf("resolve %q: %w", ref, ErrNotFound)
	}
}

// Store returns the backing store (may be nil).
func (r *Resolver) Store() Store { return r.store }

// Fingerprint is a non-reversible, printable hint used to confirm that two
// values are the same without revealing either. It is only ever derived from
// the last characters of long secrets.
func Fingerprint(secret string) string {
	if secret == "" {
		return "(empty)"
	}
	runes := []rune(secret)
	if len(runes) <= 4 {
		return fmt.Sprintf("len=%d", len(runes))
	}
	return fmt.Sprintf("…%s (len=%d)", string(runes[len(runes)-4:]), len(runes))
}
