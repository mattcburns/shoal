package secrets

import (
	"context"
	"sync"
)

// Memory is an in-process Backend for tests and lab stubs.
type Memory struct {
	mu    sync.RWMutex
	store map[string]Credential
}

// NewMemory returns an empty in-memory secret backend.
func NewMemory() *Memory {
	return &Memory{store: make(map[string]Credential)}
}

// Put stores cred under ref.
func (m *Memory) Put(_ context.Context, ref string, cred Credential) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[ref] = cred
	return nil
}

// Get resolves ref.
func (m *Memory) Get(_ context.Context, ref string) (Credential, error) {
	if err := ValidateRef(ref); err != nil {
		return Credential{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.store[ref]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return c, nil
}

// Delete removes ref.
func (m *Memory) Delete(_ context.Context, ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, ref)
	return nil
}
