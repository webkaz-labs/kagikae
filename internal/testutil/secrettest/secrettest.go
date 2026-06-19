// Package secrettest provides the in-memory secret.Backend test double
// shared by packages that exercise secret storage without a real keychain.
package secrettest

import "context"

// MemBackend is an in-memory secret backend. Values is exported so tests can
// assert on stored payloads directly.
type MemBackend struct {
	Values map[string][]byte
}

func NewMem() *MemBackend { return &MemBackend{Values: map[string][]byte{}} }

func (m *MemBackend) Name() string { return "mem" }

func (m *MemBackend) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.Values[key]
	return v, ok, nil
}

func (m *MemBackend) Set(_ context.Context, key string, value []byte) error {
	m.Values[key] = append([]byte(nil), value...)
	return nil
}

func (m *MemBackend) Delete(_ context.Context, key string) error {
	delete(m.Values, key)
	return nil
}
