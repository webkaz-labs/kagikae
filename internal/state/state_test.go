package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingYieldsEmpty(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion != 1 || st.Active == nil || len(st.Active) != 0 {
		t.Fatalf("unexpected: %+v", st)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	st := New()
	st.Active["claude"] = "main"
	st.ActiveProfile = "main"
	st.UpdatedAt = time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode: %v", info.Mode())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active["claude"] != "main" || got.ActiveProfile != "main" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoadCorruptFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}
