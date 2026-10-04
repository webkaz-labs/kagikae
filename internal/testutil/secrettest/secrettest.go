// Package secrettest provides the in-memory secret.Backend test double
// shared by packages that exercise secret storage without a real keychain.
package secrettest

import (
	"context"
	"errors"
)

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

// ErrBackendDown is the external cause FailingBackend tests inject.
var ErrBackendDown = errors.New("backend down")

// FailingBackend is a MemBackend whose Get and Delete fail with the error set for
// them; an unset error falls through to the embedded backend (which may be nil
// when the field is never reached).
type FailingBackend struct {
	*MemBackend
	GetErr, DeleteErr error
}

func (f FailingBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if f.GetErr != nil {
		return nil, false, f.GetErr
	}
	return f.MemBackend.Get(ctx, key)
}

func (f FailingBackend) Delete(ctx context.Context, key string) error {
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	return f.MemBackend.Delete(ctx, key)
}
