package credentials

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MemoryStore is an in-memory Store for tests and for dry runs. It is never
// selected by default: SEC-01 forbids silently falling back to something that
// does not persist.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[string]string
	// FailSet, when set, is returned by Set. Used by tests to exercise partial
	// failure paths.
	FailSet error
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{values: map[string]string{}}
}

func memKey(service, account string) string { return service + "\x00" + account }

// Get implements Store.
func (m *MemoryStore) Get(_ context.Context, service, account string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.values[memKey(service, account)]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrNotFound, service, account)
	}
	return value, nil
}

// Set implements Store.
func (m *MemoryStore) Set(_ context.Context, service, account, secret string) error {
	if m.FailSet != nil {
		return m.FailSet
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[memKey(service, account)] = secret
	return nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(_ context.Context, service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, memKey(service, account))
	return nil
}

// Status implements Store.
func (m *MemoryStore) Status(context.Context) Status {
	return Status{Backend: "memory", Available: true, Detail: "in-memory test store; values do not persist"}
}

// Keys lists the accounts currently held, for test assertions.
func (m *MemoryStore) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.values))
	for k := range m.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
