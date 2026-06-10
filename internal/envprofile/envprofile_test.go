package envprofile

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memBackend struct{ values map[string][]byte }

func newMem() *memBackend { return &memBackend{values: map[string][]byte{}} }

func (m *memBackend) Name() string { return "mem" }

func (m *memBackend) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memBackend) Set(_ context.Context, key string, value []byte) error {
	m.values[key] = append([]byte(nil), value...)
	return nil
}

func (m *memBackend) Delete(_ context.Context, key string) error {
	delete(m.values, key)
	return nil
}

func TestValidVarName(t *testing.T) {
	for _, ok := range []string{"ANTHROPIC_API_KEY", "_X", "A1"} {
		if !ValidVarName(ok) {
			t.Fatalf("expected valid: %q", ok)
		}
	}
	for _, bad := range []string{"", "1A", "lower", "A-B", "A B", strings.Repeat("A", 129)} {
		if ValidVarName(bad) {
			t.Fatalf("expected invalid: %q", bad)
		}
	}
}

func TestSaveLoadListDelete(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	be := newMem()
	dir := filepath.Join(root, "claude", "ci")
	profile := Profile{
		Version: 1, Tool: "claude", Account: "ci",
		UpdatedAt: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		Vars:      []string{"B_KEY", "A_KEY"},
	}
	if err := Save(dir, profile); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if strings.Join(loaded.Vars, ",") != "A_KEY,B_KEY" {
		t.Fatalf("vars not sorted: %v", loaded.Vars)
	}
	profiles, err := List(root)
	if err != nil || len(profiles) != 1 || profiles[0].Account != "ci" {
		t.Fatalf("list: %+v %v", profiles, err)
	}

	be.Set(ctx, SecretRef("claude", "ci", "A_KEY"), []byte("aaa"))
	be.Set(ctx, SecretRef("claude", "ci", "B_KEY"), []byte("bbb"))
	pairs, err := EnvStrings(ctx, be, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pairs, ";") != "A_KEY=aaa;B_KEY=bbb" {
		t.Fatalf("unexpected pairs: %v", pairs)
	}

	if err := Delete(ctx, be, dir, loaded); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Load(dir); found {
		t.Fatal("profile not deleted")
	}
	if len(be.values) != 0 {
		t.Fatalf("secrets not deleted: %v", be.values)
	}
}

func TestEnvStringsMissingValue(t *testing.T) {
	profile := Profile{Tool: "claude", Account: "ci", Vars: []string{"GONE"}}
	if _, err := EnvStrings(context.Background(), newMem(), profile); err == nil {
		t.Fatal("expected error for missing secret")
	}
}
