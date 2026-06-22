package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// companionCLIApp writes a config.toml defining profile "main" and loads it, so
// the editConfig path (which reads/writes app.ConfigPath) has a real file.
func companionCLIApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	writeFile(t, app.ConfigPath, "[security]\nsecret_backend = \"file\"\n\n[profiles.main.accounts]\nclaude = \"main\"\n")
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	app.Config = cfg
	return app
}

func withStdin(t *testing.T, input string, run func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	go func() {
		_, _ = w.WriteString(input)
		_ = w.Close()
	}()
	run()
}

func TestCompanionAddNonSecretInline(t *testing.T) {
	app := companionCLIApp(t)
	code := runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "git", "email=you@example.com", "name=You"})
	if code != constants.ExitOK {
		t.Fatalf("add git: exit %d", code)
	}
	got := app.Config.Profiles["main"].Companions["git"]
	if got["email"] != "you@example.com" || got["name"] != "You" {
		t.Fatalf("git knobs not written: %v", got)
	}
	// Inline (non-secret) values land in config.toml verbatim.
	if raw := readFile(t, app.ConfigPath); !strings.Contains(raw, "you@example.com") {
		t.Fatalf("config.toml missing inline value:\n%s", raw)
	}
}

func TestCompanionAddTokenFromStdin(t *testing.T) {
	app := companionCLIApp(t)
	withStdin(t, "ghp_secret_token_123\n", func() {
		code := runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "gh", "GH_TOKEN"})
		if code != constants.ExitOK {
			t.Fatalf("add gh token: exit %d", code)
		}
	})
	// config.toml carries only the empty marker, never the token.
	if v := app.Config.Profiles["main"].Companions["gh"]["GH_TOKEN"]; v != "" {
		t.Fatalf("token marker = %q, want empty", v)
	}
	if raw := readFile(t, app.ConfigPath); strings.Contains(raw, "ghp_secret_token_123") {
		t.Fatalf("token must not be written to config.toml:\n%s", raw)
	}
	// The value lives in the secret backend and round-trips via the helper.
	code, out := captureStdout(t, func() int {
		return companionToken(context.Background(), app, []string{"main", "gh", "GH_TOKEN"})
	})
	if code != constants.ExitOK || out != "ghp_secret_token_123" {
		t.Fatalf("token helper: exit %d out %q", code, out)
	}
}

func TestCompanionTokenHelperMissing(t *testing.T) {
	app := companionCLIApp(t)
	code, out := captureStdout(t, func() int {
		return companionToken(context.Background(), app, []string{"main", "gh", "GH_TOKEN"})
	})
	if code != constants.ExitNotFound {
		t.Fatalf("missing token: exit %d", code)
	}
	if out != "" {
		t.Fatalf("missing token must print nothing to stdout, got %q", out)
	}
}

func TestCompanionListHidesSecretValues(t *testing.T) {
	app := companionCLIApp(t)
	_ = runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "git", "email=you@example.com"})
	_ = runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "kubectl", "KUBECONFIG=/home/me/.kube/config"})
	withStdin(t, "ghp_secret_token_123", func() {
		_ = runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "gh", "GH_TOKEN"})
	})
	code, out := captureStdout(t, func() int {
		return runCompanionList(context.Background(), app, commonOpts{Format: formatJSON})
	})
	if code != constants.ExitOK {
		t.Fatalf("list: exit %d", code)
	}
	if strings.Contains(out, "ghp_secret_token_123") {
		t.Fatalf("list leaked the token value:\n%s", out)
	}
	if !strings.Contains(out, "you@example.com") || !strings.Contains(out, "/home/me/.kube/config") {
		t.Fatalf("list should show non-secret values:\n%s", out)
	}
	if !strings.Contains(out, "\"secret\": true") {
		t.Fatalf("list should mark the token knob secret:\n%s", out)
	}
}

func TestCompanionRmKnobAndWhole(t *testing.T) {
	app := companionCLIApp(t)
	_ = runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "git", "email=you@example.com", "name=You"})

	// Remove a single knob.
	if code := runCompanionRm(context.Background(), app, commonOpts{}, []string{"main", "git", "name"}); code != constants.ExitOK {
		t.Fatalf("rm knob: exit %d", code)
	}
	git := app.Config.Profiles["main"].Companions["git"]
	if _, ok := git["name"]; ok {
		t.Fatalf("name knob not removed: %v", git)
	}
	if git["email"] != "you@example.com" {
		t.Fatalf("email knob wrongly dropped: %v", git)
	}

	// Remove the whole companion.
	if code := runCompanionRm(context.Background(), app, commonOpts{}, []string{"main", "git"}); code != constants.ExitOK {
		t.Fatalf("rm whole: exit %d", code)
	}
	if _, ok := app.Config.Profiles["main"].Companions["git"]; ok {
		t.Fatalf("git companion not removed")
	}
}

func TestCompanionRmTokenDeletesSecret(t *testing.T) {
	app := companionCLIApp(t)
	withStdin(t, "ghp_secret_token_123", func() {
		_ = runCompanionAdd(context.Background(), app, commonOpts{}, []string{"main", "gh", "GH_TOKEN"})
	})
	if code := runCompanionRm(context.Background(), app, commonOpts{}, []string{"main", "gh"}); code != constants.ExitOK {
		t.Fatalf("rm gh: exit %d", code)
	}
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := be.Get(context.Background(), companion.SecretRef("main", "gh", "GH_TOKEN")); found {
		t.Fatalf("token secret not deleted on rm")
	}
}

func TestParseCompanionKnobsErrors(t *testing.T) {
	git, _ := companion.For(constants.CompanionGit)
	gh, _ := companion.For(constants.CompanionGH)
	cases := []struct {
		name string
		spec companion.Spec
		args []string
	}{
		{"unknown knob", git, []string{"nope=x"}},
		{"non-secret needs value", git, []string{"email"}},
		{"token must be bare", gh, []string{"GH_TOKEN=leaked"}},
		{"no knobs", git, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseCompanionKnobs(tc.spec, tc.args, strings.NewReader("")); err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
		})
	}
}
