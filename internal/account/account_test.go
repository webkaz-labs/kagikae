package account

import (
	"os"
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
			"auth": {
				Kind: "file", Target: "/x/auth.json",
				SecretRef: SecretRef(tool, name, "auth"), Present: true,
			},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "codex", "main")
	if err := Save(dir, sample("codex", "main")); err != nil {
		t.Fatal(err)
	}
	acc, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if acc.Tool != "codex" || acc.Artifacts["auth"].SecretRef != "codex/main/auth" {
		t.Fatalf("round trip lost data: %+v", acc)
	}
}

// A snapshot written by an older kae carries keys this one no longer has —
// `keychain_account`, recorded through v0.15.3 and read nowhere, is the concrete
// case. Loading such a file must succeed and keep every key that is still
// modelled: a decoder that rejected the unknown one would make every account
// captured before the removal unreadable, which is a migration nobody asked for.
func TestLoadIgnoresRetiredKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "codex", "main")
	if err := Save(dir, sample("codex", "main")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "account.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const header = "[artifacts.auth]\n"
	aged := strings.Replace(string(data), header,
		header+"    keychain_account = \"cli|1111111111111111\"\n", 1)
	if aged == string(data) {
		t.Fatalf("could not place the retired key; the metadata layout changed:\n%s", data)
	}
	if err := os.WriteFile(filepath.Join(dir, "account.toml"), []byte(aged), 0o600); err != nil {
		t.Fatal(err)
	}

	acc, found, err := Load(dir)
	if err != nil || !found {
		t.Fatalf("a snapshot with a retired key must still load: found=%v err=%v", found, err)
	}
	if acc.Artifacts["auth"].SecretRef != "codex/main/auth" || !acc.Artifacts["auth"].Present {
		t.Fatalf("modelled keys lost alongside the retired one: %+v", acc.Artifacts["auth"])
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
	for _, pair := range [][2]string{{"agy", "main"}, {"claude", "zeta"}, {"claude", "alpha"}} {
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
	want := "claude/alpha claude/zeta agy/main"
	if strings.Join(got, " ") != want {
		t.Fatalf("ordering: got %v want %s", got, want)
	}
}

func TestArtifactNamesSorted(t *testing.T) {
	acc := sample("codex", "main")
	acc.Artifacts["zzz"] = Artifact{Kind: "file"}
	acc.Artifacts["aaa"] = Artifact{Kind: "file"}
	names := acc.ArtifactNames()
	if strings.Join(names, ",") != "aaa,auth,zzz" {
		t.Fatalf("not sorted: %v", names)
	}
}
