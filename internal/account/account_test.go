package account

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(tool, name string) Account {
	return Account{
		Version: 1, Tool: tool, Name: name, Driver: "codex-auth-json",
		CapturedAt: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		Artifacts: map[string]Artifact{
			"auth": {Kind: "file", Target: "/x/auth.json",
				SecretRef: SecretRef(tool, name, "auth"), Present: true},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "codex", "work")
	if err := Save(dir, sample("codex", "work")); err != nil {
		t.Fatal(err)
	}
	acc, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if acc.Tool != "codex" || acc.Artifacts["auth"].SecretRef != "codex/work/auth" {
		t.Fatalf("round trip lost data: %+v", acc)
	}
}

func TestLoadMissing(t *testing.T) {
	_, found, err := Load(filepath.Join(t.TempDir(), "none"))
	if err != nil || found {
		t.Fatalf("expected not found: %v %v", found, err)
	}
}

func TestListCanonicalOrder(t *testing.T) {
	root := t.TempDir()
	for _, pair := range [][2]string{{"gemini", "work"}, {"claude", "zeta"}, {"claude", "alpha"}} {
		if err := Save(filepath.Join(root, pair[0], pair[1]), sample(pair[0], pair[1])); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, acc := range accounts {
		got = append(got, acc.Tool+"/"+acc.Name)
	}
	want := "claude/alpha claude/zeta gemini/work"
	if strings.Join(got, " ") != want {
		t.Fatalf("ordering: got %v want %s", got, want)
	}
}

func TestArtifactNamesSorted(t *testing.T) {
	acc := sample("codex", "work")
	acc.Artifacts["zzz"] = Artifact{Kind: "file"}
	acc.Artifacts["aaa"] = Artifact{Kind: "file"}
	names := acc.ArtifactNames()
	if strings.Join(names, ",") != "aaa,auth,zzz" {
		t.Fatalf("not sorted: %v", names)
	}
}
