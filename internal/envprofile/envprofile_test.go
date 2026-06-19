package envprofile

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

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
	be := secrettest.NewMem()
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
	if len(be.Values) != 0 {
		t.Fatalf("secrets not deleted: %v", be.Values)
	}
}

func TestEnvStringsMissingValue(t *testing.T) {
	profile := Profile{Tool: "claude", Account: "ci", Vars: []string{"GONE"}}
	if _, err := EnvStrings(context.Background(), secrettest.NewMem(), profile); err == nil {
		t.Fatal("expected error for missing secret")
	}
}
