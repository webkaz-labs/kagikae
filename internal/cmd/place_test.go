package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// chdirTo moves the test into dir and returns dir as cwdAbs spells it (on macOS a
// temp dir's realpath differs from t.TempDir's answer).
func chdirTo(t *testing.T, dir string) string {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return mustCwdAbs(t)
}

// scratchGit runs real git in dir with an identity and no hooks, so a host
// gitconfig cannot change the fixture.
func scratchGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	full := append([]string{
		"-C", dir, "-c", "user.email=you@example.com", "-c", "user.name=kae test",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = envWithoutGitRepositoryOverrides(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// placesOf runs `kae ls <tool> --json` (or a group) and returns its places.
func placesOf(t *testing.T, app *App, req lsRequest) []placeRow {
	t.Helper()
	code, out := captureStdout(t, func() int {
		return runLsRequest(context.Background(), app, commonOpts{Format: formatJSON}, req)
	})
	mustExit(t, constants.ExitOK, code, out)
	var report struct {
		Places []placeRow `json:"places"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if report.Places == nil {
		t.Fatalf("places must be [] not null: %s", out)
	}
	return report.Places
}

func kindsAndPaths(rows []placeRow) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Kind+" "+row.Path)
	}
	return out
}

func pickPath(t *testing.T, app *App, req lsRequest) (int, string) {
	t.Helper()
	req.current = req.current || req.at == 0
	code, out := captureStdout(t, func() int {
		return runLsRequest(context.Background(), app, commonOpts{Format: formatText}, req)
	})
	return code, strings.TrimSuffix(out, "\n")
}

// The root is derived from cwd's spelling and git's prefix, and the argv is part of
// the contract: without --show-prefix the derivation has nothing to strip.
func TestGitRepositoryRootAsksGitForTheRootAndPrefix(t *testing.T) {
	root := chdirTo(t, t.TempDir())
	cwd := chdirTo(t, filepath.Join(root, "sub", "deeper"))
	fake := &runnertest.Fake{Stdout: root + "\nsub/deeper/\n"}
	var got string
	runner.With(fake, func() { got = gitRepositoryRoot(context.Background(), cwd) })
	if got != root {
		t.Fatalf("root = %q, want %q", got, root)
	}
	if fake.Name != "git" || !slices.Equal(fake.Args, []string{"rev-parse", "--show-toplevel", "--show-prefix"}) {
		t.Fatalf("argv = %s %v", fake.Name, fake.Args)
	}
	for name, reply := range map[string]*runnertest.Fake{
		"not a repository":  {Code: 128},
		"truncated answer":  {Stdout: root + "\n"},
		"relative toplevel": {Stdout: "repo\nsub/deeper/\n"},
		"missing toplevel":  {Stdout: filepath.Join(root, "gone") + "\n\n"},
	} {
		runner.With(reply, func() { got = gitRepositoryRoot(context.Background(), cwd) })
		if got != "" {
			t.Errorf("%s: root = %q, want none", name, got)
		}
	}
}

// Below the current directory, a repository is searched through git's tracked and
// non-ignored listing, and only levels strictly below cwd count.
func TestGitListedFilesArgvAndBelowLevels(t *testing.T) {
	root := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(root, "a", ".claude"), filepath.Join(root, "b", "c", ".codex"))
	fake := &runnertest.Fake{Stdout: "a/.claude/settings.json\x00b/c/.codex/config.toml\x00.claude/settings.json\x00gone/.claude/x\x00"}
	pc := &placeContext{cwd: root, repoRoot: root}
	var below map[string][]string
	runner.With(fake, func() { below = pc.belowLevels(context.Background()) })
	if !slices.Equal(fake.Args, []string{
		"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
		":(glob)**/.claude/**", ":(glob)**/.codex/**",
	}) {
		t.Fatalf("argv = %v", fake.Args)
	}
	if want := []string{filepath.Join(root, "a", ".claude")}; !slices.Equal(below[constants.ToolClaude], want) {
		t.Fatalf("claude below = %v, want %v (cwd's own and a vanished level are not below levels)", below[constants.ToolClaude], want)
	}
	if want := []string{filepath.Join(root, "b", "c", ".codex")}; !slices.Equal(below[constants.ToolCodex], want) {
		t.Fatalf("codex below = %v, want %v", below[constants.ToolCodex], want)
	}

	// A failing listing warns and yields nothing; it never fails the command.
	pc = &placeContext{cwd: root, repoRoot: root}
	_, stderr := captureStderr(t, func() int {
		runner.With(&runnertest.Fake{Code: 128, Stderr: "fatal: boom"}, func() { below = pc.belowLevels(context.Background()) })
		return 0
	})
	if len(below) != 0 || !strings.Contains(stderr, "could not list the repository's files") {
		t.Fatalf("below = %v, stderr = %q", below, stderr)
	}
}

// The discovery rules, against a real repository: claude counts every .claude/
// from cwd to the root and, above the root, one holding instructions, and says
// what applies at each; codex reads every .codex/ from cwd up to the root; an
// ignored directory's level is not found below.
func TestToolPlacesInARepositoryFollowTheMeasuredRules(t *testing.T) {
	app := testApp(t, nil)
	outer := chdirTo(t, t.TempDir())
	writeFile(t, filepath.Join(outer, ".claude", "CLAUDE.md"), "# outer\n")
	writeFile(t, filepath.Join(outer, "plain", ".claude", "settings.json"), "{}\n")
	root := filepath.Join(outer, "plain", "repo")
	mkdirs(t, root)
	scratchGit(t, root, "init", "-q")
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	for _, f := range []string{
		".claude/settings.local.json", ".codex/config.toml",
		"sub/.claude/settings.json", "sub/.claude/skills/demo/SKILL.md", "sub/.codex/config.toml",
		"sub/mid/.codex/config.toml", "sub/mid/.claude/settings.json",
		"sub/mid/deeper/.claude/settings.json",
		"sub/mid/ignored/.claude/settings.json",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(f)), "{}\n")
	}
	cwd := chdirTo(t, filepath.Join(root, "sub", "mid"))
	root = filepath.Dir(filepath.Dir(cwd)) // in cwd's spelling
	outer = filepath.Dir(filepath.Dir(root))
	realClaude := filepath.Join(app.Env.Home, ".claude")
	realCodex := filepath.Join(app.Env.Home, ".codex")

	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	want := []string{
		"user " + realClaude,
		"project " + filepath.Join(cwd, ".claude"),
		"project " + filepath.Join(root, "sub", ".claude"),
		"project " + filepath.Join(root, ".claude"),
		// Above the root only a level holding instructions counts: outer/plain's
		// settings-only .claude/ does not.
		"project " + filepath.Join(outer, ".claude"),
		"below " + filepath.Join(cwd, "deeper", ".claude"),
	}
	if got := kindsAndPaths(claude); !slices.Equal(got, want) {
		t.Fatalf("claude places:\n got %v\nwant %v", got, want)
	}
	for i, applies := range [][]string{
		nil,
		{constants.PlaceAppliesSettings},      // cwd's settings.json
		{constants.PlaceAppliesSkillsAgents},  // sub's settings.json does not apply from sub/mid
		{constants.PlaceAppliesLocalSettings}, // the root's settings.local.json
		{constants.PlaceAppliesInstructions},
		nil,
	} {
		if !slices.Equal(claude[i].Applies, applies) {
			t.Fatalf("row %d (%s) applies %v, want %v", i+1, claude[i].Path, claude[i].Applies, applies)
		}
	}
	if claude[0].Source != constants.PlaceSourceGlobal || claude[0].Mode != constants.ModeAuth || !claude[0].InEffect {
		t.Fatalf("an unbound user level is the real home, applied globally: %+v", claude[0])
	}
	if claude[3].Root != root || !claude[3].InEffect || claude[5].InEffect || claude[5].Root != filepath.Join(cwd, "deeper") {
		t.Fatalf("project/below rows: %+v", claude[1:])
	}
	for i, row := range claude {
		if row.Number != i+1 || row.Group != constants.ToolClaude {
			t.Fatalf("row %d numbered %d in group %q", i, row.Number, row.Group)
		}
	}

	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	want = []string{
		"user " + realCodex,
		"project " + filepath.Join(cwd, ".codex"),
		"project " + filepath.Join(root, "sub", ".codex"),
		"project " + filepath.Join(root, ".codex"),
	}
	if got := kindsAndPaths(codex); !slices.Equal(got, want) {
		t.Fatalf("codex places:\n got %v\nwant %v", got, want)
	}

	repo := placesOf(t, app, lsRequest{target: constants.PlaceGroupRepo})
	if len(repo) != 1 || repo[0].Path != root || repo[0].Kind != constants.PlaceKindRepositoryRoot {
		t.Fatalf("repo places = %+v, want the root %s", repo, root)
	}

	// --current: the user level; --project: the nearest ancestor; --root its holder.
	if code, got := pickPath(t, app, lsRequest{target: constants.ToolCodex, current: true, level: constants.PlaceKindProject}); code != 0 || got != filepath.Join(cwd, ".codex") {
		t.Fatalf("codex --current --project = %d %q", code, got)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.ToolCodex, current: true, level: constants.PlaceKindProject, root: true}); code != 0 || got != cwd {
		t.Fatalf("codex --current --project --root = %d %q", code, got)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true}); code != 0 || got != realClaude {
		t.Fatalf("claude --current = %d %q", code, got)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true, level: constants.PlaceKindProject}); code != 0 || got != filepath.Join(cwd, ".claude") {
		t.Fatalf("claude --current --project = %d %q", code, got)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.PlaceGroupRepo, current: true}); code != 0 || got != root {
		t.Fatalf("repo --current = %d %q", code, got)
	}
	// --at N is the number the listing shows, for every row.
	for _, row := range codex {
		if code, got := pickPath(t, app, lsRequest{target: constants.ToolCodex, at: row.Number}); code != 0 || got != row.Path {
			t.Fatalf("codex --at %d = %d %q, want %q", row.Number, code, got, row.Path)
		}
	}
	code, stderr := captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.ToolCodex, at: len(codex) + 1})
		return c
	})
	if code != constants.ExitUsage || !strings.Contains(stderr, "there is no place") {
		t.Fatalf("--at past the end = %d %q", code, stderr)
	}
	// Bare ls shows both tools: each has an effective project level here.
	pc, err := app.newPlaceContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pc.toolRelevant(constants.ToolClaude) || !pc.toolRelevant(constants.ToolCodex) {
		t.Fatalf("both tools have a project level in effect here")
	}
}

// Outside a repository both tools read cwd's level only, and the search below is a
// depth-limited walk.
func TestToolPlacesOutsideARepository(t *testing.T) {
	app := testApp(t, nil)
	parent := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(parent, ".codex"), filepath.Join(parent, ".claude"))
	cwd := chdirTo(t, filepath.Join(parent, "work"))
	mkdirs(
		t,
		filepath.Join(cwd, ".codex"),
		filepath.Join(cwd, "a", "b", "c", ".claude"),
		filepath.Join(cwd, "a", "b", "c", "d", ".claude"), // four levels down: past the walk
		filepath.Join(cwd, ".hidden", "x", ".claude"),     // hidden directories are not entered
	)
	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	if got, want := kindsAndPaths(codex), []string{
		"user " + filepath.Join(app.Env.Home, ".codex"),
		"project " + filepath.Join(cwd, ".codex"),
	}; !slices.Equal(got, want) {
		t.Fatalf("codex places:\n got %v\nwant %v (the parent's .codex/ is not read outside a repository)", got, want)
	}
	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if got, want := kindsAndPaths(claude), []string{
		"user " + filepath.Join(app.Env.Home, ".claude"),
		"below " + filepath.Join(cwd, "a", "b", "c", ".claude"),
	}; !slices.Equal(got, want) {
		t.Fatalf("claude places:\n got %v\nwant %v", got, want)
	}
	// Outside a repository an ancestor claude level counts when it holds
	// instructions.
	writeFile(t, filepath.Join(parent, ".claude", "AGENTS.md"), "# parent\n")
	claude = placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if len(claude) != 3 || claude[1].Path != filepath.Join(parent, ".claude") ||
		!slices.Equal(claude[1].Applies, []string{constants.PlaceAppliesInstructions}) {
		t.Fatalf("claude places with the parent's AGENTS.md: %v %+v", kindsAndPaths(claude), claude)
	}
	if code, out := captureStdout(t, func() int {
		return runLsRequest(context.Background(), app, commonOpts{Format: formatText}, lsRequest{target: constants.ToolClaude})
	}); code != 0 || !strings.Contains(out, "Applies") || !strings.Contains(out, constants.PlaceAppliesInstructions) {
		t.Fatalf("the human table says what applies = %d %q", code, out)
	}
	if code, out := captureStdout(t, func() int {
		return runLsRequest(context.Background(), app, commonOpts{Format: formatText}, lsRequest{target: constants.PlaceGroupRepo})
	}); code != 0 || !strings.Contains(out, "not in a Git repository") {
		t.Fatalf("repo outside a repository = %d %q", code, out)
	}
	code, stderr := captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.PlaceGroupRepo, current: true})
		return c
	})
	if code != constants.ExitNotFound || !strings.Contains(stderr, "no current place") {
		t.Fatalf("repo --current outside a repository = %d %q", code, stderr)
	}
}

// Ancestors stop before HOME: HOME's own .claude/ and .codex/ are the real homes,
// including in a repository rooted at HOME.
func TestProjectLevelsStopBeforeHome(t *testing.T) {
	app := testApp(t, nil)
	home := chdirTo(t, app.Env.Home)
	app.Env.Home = home
	mkdirs(t, filepath.Join(home, ".claude"), filepath.Join(home, ".codex"))
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if rows := placesOf(t, app, lsRequest{target: tool}); len(rows) != 1 || rows[0].Kind != constants.PlaceKindUser {
			t.Fatalf("%s at HOME lists only its user level: %v", tool, kindsAndPaths(rows))
		}
	}
	scratchGit(t, home, "init", "-q")
	writeFile(t, filepath.Join(home, ".claude", "settings.local.json"), "{}\n")
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "\n")
	chdirTo(t, filepath.Join(home, "proj"))
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		if rows := placesOf(t, app, lsRequest{target: tool}); len(rows) != 1 {
			t.Fatalf("%s in a repository rooted at HOME has no project level from HOME: %v", tool, kindsAndPaths(rows))
		}
	}
}

// The nearest ancestor bound directory's recorded binding decides, not the
// environment: from a subdirectory the user level is that binding's store, and the
// real home is listed beside it. -s and -i name the user level explicitly.
func TestUserLevelFollowsTheGoverningBinding(t *testing.T) {
	app := overlayTestApp(t)
	captureClaude(t, app, "main", mainToken)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	chdirTo(t, filepath.Join(bound, "sub"))
	store := app.Paths.IsolatedConfigDir(paths.PinID(bound), constants.ToolClaude, "main")

	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if claude[0].Path != store || claude[0].Source != constants.PlaceSourcePin || claude[0].Mode != constants.ModeIsolated || claude[0].Account != "main" {
		t.Fatalf("user level = %+v, want the bound store %s", claude[0], store)
	}
	last := claude[len(claude)-1]
	if last.Kind != constants.PlaceKindHome || last.Path != filepath.Join(app.Env.Home, ".claude") || last.InEffect {
		t.Fatalf("the real home is listed, not in effect, when another level is: %+v", last)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true, level: constants.PlaceKindHome}); code != 0 || got != last.Path {
		t.Fatalf("--current --home = %d %q", code, got)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.PlaceGroupPin, current: true}); code != 0 || got != bound {
		t.Fatalf("pin --current = %d %q, want %s", code, got, bound)
	}

	// --pins keeps Current's exact-match meaning and adds governing.
	code, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var pins pinsReport
	if err := json.Unmarshal([]byte(out), &pins); err != nil {
		t.Fatal(err)
	}
	if len(pins.BoundDirectories) != 1 || pins.BoundDirectories[0].Current || !pins.BoundDirectories[0].Governing {
		t.Fatalf("from a subdirectory the binding governs but is not current: %s", out)
	}

	// Explicit resolution overrides the binding.
	isolated := placesOf(t, app, lsRequest{target: constants.ToolClaude, explicit: &explicitUserLevel{tool: constants.ToolClaude, isolated: true, account: "side"}})
	if isolated[0].Path != app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side") || isolated[0].Source != constants.PlaceSourceExplicit || isolated[0].Mode != constants.ModeSync {
		t.Fatalf("-i claude side user level = %+v", isolated[0])
	}
	shared := placesOf(t, app, lsRequest{target: constants.ToolClaude, explicit: &explicitUserLevel{tool: constants.ToolClaude}})
	if shared[0].Path != filepath.Join(app.Env.Home, ".claude") || shared[0].Source != constants.PlaceSourceExplicit {
		t.Fatalf("-s claude user level = %+v", shared[0])
	}
	if shared[len(shared)-1].Kind == constants.PlaceKindHome {
		t.Fatalf("with -s the user level is the real home, so it is not listed twice: %v", kindsAndPaths(shared))
	}

	// Bare ls shows claude (bound) and not codex (neither bound nor a project level).
	pc, err := app.newPlaceContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pc.toolRelevant(constants.ToolClaude) || pc.toolRelevant(constants.ToolCodex) {
		t.Fatalf("relevance: claude %v codex %v", pc.toolRelevant(constants.ToolClaude), pc.toolRelevant(constants.ToolCodex))
	}
}

// Without a binding, what applies globally decides: kae use -i's home.
func TestUserLevelFollowsGlobalIsolation(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir())
	st := state.New()
	st.Synced = map[string]string{constants.ToolCodex: "side"}
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	if got, want := kindsAndPaths(codex), []string{
		"user " + app.Paths.GlobalIsolatedHomeDir(constants.ToolCodex, "side"),
		"home " + filepath.Join(app.Env.Home, ".codex"),
	}; !slices.Equal(got, want) {
		t.Fatalf("codex places:\n got %v\nwant %v", got, want)
	}
	if codex[0].Source != constants.PlaceSourceGlobal || codex[0].Mode != constants.ModeSync || codex[0].Account != "side" || codex[0].Exists {
		t.Fatalf("global isolated user level = %+v", codex[0])
	}
}

// Several matches under --current is a usage error naming them with their numbers.
func TestCurrentWithSeveralMatchesNamesThem(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(cwd, "a", ".claude"), filepath.Join(cwd, "b", ".claude"))
	code, stderr := captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true, level: constants.PlaceKindBelow})
		return c
	})
	if code != constants.ExitUsage {
		t.Fatalf("exit = %d, want usage: %s", code, stderr)
	}
	for _, want := range []string{"2 " + filepath.Join(cwd, "a", ".claude"), "3 " + filepath.Join(cwd, "b", ".claude")} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the error must name %q:\n%s", want, stderr)
		}
	}
	code, stderr = captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.PlaceGroupKae, current: true})
		return c
	})
	if code != constants.ExitUsage || !strings.Contains(stderr, app.Paths.DataDir) {
		t.Fatalf("kae --current = %d %q", code, stderr)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.PlaceGroupKae, at: 2}); code != 0 || got != app.Paths.DataDir {
		t.Fatalf("kae --at 2 = %d %q", code, got)
	}
}

// The command-line refusals docs/CLI.md § kae ls Semantics names.
func TestLsRequestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags lsFlags
		pos   []string
		want  string
	}{
		{"-i with --home", lsFlags{isolated: true, current: true, home: true}, []string{"claude", "side"}, "-i resolves the user level"},
		{"account without -i", lsFlags{}, []string{"claude", "side"}, "needs -i"},
		{"account with -s", lsFlags{shared: true}, []string{"claude", "side"}, "with -i"},
		{"-i without account", lsFlags{isolated: true}, []string{"claude"}, "kae ls -i <tool> <account>"},
		{"-s and -i", lsFlags{shared: true, isolated: true}, []string{"claude"}, "mutually exclusive"},
		{"-s on a group", lsFlags{shared: true}, []string{"repo"}, "not a tool"},
		{"selector without --current", lsFlags{project: true}, []string{"claude"}, "with --current"},
		{"two selectors", lsFlags{current: true, project: true, below: true}, []string{"claude"}, "give one"},
		{"selector on a group", lsFlags{current: true, project: true}, []string{"repo"}, "needs a tool target"},
		{"--root without a level", lsFlags{current: true, root: true}, []string{"claude"}, "add --project or --below"},
		{"--root with --home", lsFlags{current: true, root: true, home: true}, []string{"claude"}, "add --project or --below"},
		{"--root alone", lsFlags{root: true}, []string{"claude"}, "with --current or --at"},
		{"--current and --at", lsFlags{current: true, at: atFlag{n: 1, set: true}}, []string{"claude"}, "give one"},
		{"--current without target", lsFlags{current: true}, nil, "need a target"},
		{"account is not a place", lsFlags{current: true}, []string{"account"}, "not places"},
		{"--pins with another target", lsFlags{pins: true}, []string{"repo"}, "--pins"},
		{"ambiguous prefix", lsFlags{}, []string{"c"}, "ambiguous"},
		{"unknown target", lsFlags{}, []string{"repos"}, "unknown ls target"},
		{"invalid account", lsFlags{isolated: true}, []string{"claude", "../x"}, "invalid account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			_, stderr := captureStderr(t, func() int {
				_, code = parseLsRequest(tc.flags, tc.pos)
				return code
			})
			if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q; want usage naming %q", code, stderr, tc.want)
			}
		})
	}
	// Exact words first, then prefixes against tool names only: `a` is agy, not
	// account, and `cl` is claude.
	for word, want := range map[string]string{"account": "account", "pin": "pin", "a": constants.ToolAgy, "cl": constants.ToolClaude, "codex": constants.ToolCodex} {
		req, code := parseLsRequest(lsFlags{}, []string{word})
		if code != constants.ExitOK || req.target != want {
			t.Fatalf("%q resolved to %q (exit %d), want %q", word, req.target, code, want)
		}
	}
	if req, code := parseLsRequest(lsFlags{pins: true}, nil); code != 0 || req.target != constants.PlaceGroupPin {
		t.Fatalf("--pins is kae ls pin: %+v %d", req, code)
	}
	if req, code := parseLsRequest(lsFlags{isolated: true, current: true}, []string{"cl", "side"}); code != 0 || req.explicit == nil || req.explicit.tool != constants.ToolClaude || req.explicit.account != "side" {
		t.Fatalf("-i cl side: %+v %d", req, code)
	}
	// --at takes a number from 1; the flag parse refuses anything else before any
	// environment is read.
	for _, bad := range []string{"0", "-1", "x"} {
		var a atFlag
		if err := a.Set(bad); err == nil {
			t.Fatalf("--at %s accepted", bad)
		}
	}
}

// A config error reaches only the rows that need config: human output still shows
// the other groups and exits 2; a request needing no config exits 0; --json keeps
// the JSON error object.
func TestLsConfigErrorLeavesThePlaces(t *testing.T) {
	app := testApp(t, nil)
	app.ConfigErr = errors.New("boom")
	chdirTo(t, t.TempDir())
	ctx := context.Background()

	code, stdout, stderr := captureBoth(t, func() int { return runLs(ctx, app, commonOpts{Format: formatText}) })
	if code != constants.ExitInvalidConfig || !strings.Contains(stderr, "boom") {
		t.Fatalf("bare ls = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"Bound directories:", "Repository:", "kae directories:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("bare ls must still show %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Accounts:") {
		t.Fatalf("the account group needs config:\n%s", stdout)
	}

	code, stdout, stderr = captureBoth(t, func() int {
		return runLsRequest(ctx, app, commonOpts{Format: formatText}, lsRequest{target: constants.ToolClaude})
	})
	if code != constants.ExitInvalidConfig || !strings.Contains(stderr, "boom") || !strings.Contains(stdout, "claude places:") {
		t.Fatalf("ls claude = %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	for _, req := range []lsRequest{
		{target: constants.PlaceGroupPin},
		{target: constants.PlaceGroupRepo},
		{target: constants.PlaceGroupKae},
		{target: constants.ToolClaude, current: true},
		{target: constants.PlaceGroupKae, at: 1},
	} {
		if code, out := captureStdout(t, func() int { return runLsRequest(ctx, app, commonOpts{Format: formatText}, req) }); code != constants.ExitOK {
			t.Fatalf("%+v needs no config, exit %d: %s", req, code, out)
		}
	}

	for _, req := range []lsRequest{{}, {target: constants.ToolClaude}, {target: constants.PlaceGroupAccount}} {
		code, out := captureStdout(t, func() int { return runLsRequest(ctx, app, commonOpts{Format: formatJSON}, req) })
		var report errorReport
		if err := json.Unmarshal([]byte(out), &report); err != nil || code != constants.ExitInvalidConfig ||
			report.OK || report.ErrorCode != constants.ErrorCode(constants.ExitInvalidConfig) {
			t.Fatalf("%+v --json = %d %s", req, code, out)
		}
	}
}

// Bare `kae ls --json` keeps accounts and profiles and adds the places beside
// them, in group order.
func TestBareLsJSONAddsPlacesBesideAccounts(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(cwd, ".claude"))
	code, out := captureStdout(t, func() int { return runLs(context.Background(), app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "accounts", "profiles", "places"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("bare ls --json lacks %q: %s", key, out)
		}
	}
	var report bareLsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	var groups []string
	for _, row := range report.Places {
		if len(groups) == 0 || groups[len(groups)-1] != row.Group {
			groups = append(groups, row.Group)
		}
	}
	// claude has a project level here; codex has none and is not bound; no
	// repository; kae always.
	if want := []string{constants.ToolClaude, constants.PlaceGroupKae}; !slices.Equal(groups, want) {
		t.Fatalf("groups = %v, want %v: %s", groups, want, out)
	}
	if !strings.Contains(out, `"kind": "project"`) || strings.Contains(out, `"config-dir"`) {
		t.Fatalf("place kinds: %s", out)
	}
	code, text := captureStdout(t, func() int { return runLs(context.Background(), app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, text)
	for _, want := range []string{"Accounts:", "Profiles:", "Bound directories:", "claude places:", "Repository:", "kae directories:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bare ls text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "codex places:") {
		t.Fatalf("codex is not relevant here:\n%s", text)
	}
}

// Every new output path — bare ls, a tool group, --current and --at, in text and
// JSON — names places and never a credential, though the bound store holds one.
func TestLsPlacesNeverCarryACredential(t *testing.T) {
	const canary = "sk-ant-oat01-PLACES-CANARY-dddd"
	app := overlayTestApp(t)
	captureClaude(t, app, "main", canary)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	stored := dirCredFile(app, constants.ToolClaude, "main",
		app.Paths.IsolatedConfigDir(paths.PinID(bound), constants.ToolClaude, "main"))
	if !strings.Contains(readFile(t, stored), canary) {
		t.Fatalf("fixture does not place the canary in the bound store (%s); this test would pass vacuously", stored)
	}
	for _, req := range []lsRequest{
		{},
		{target: constants.ToolClaude},
		{target: constants.ToolClaude, current: true},
		{target: constants.ToolClaude, at: 1},
		{target: constants.PlaceGroupPin, current: true},
	} {
		for _, format := range []string{formatText, formatJSON} {
			code, stdout, stderr := captureBoth(t, func() int {
				return runLsRequest(context.Background(), app, commonOpts{Format: format}, req)
			})
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			if !strings.Contains(stdout, "main") {
				t.Fatalf("%+v %s: the binding must be reported, or this canary proves nothing:\n%s", req, format, stdout)
			}
			if strings.Contains(stdout+stderr, canary) {
				t.Fatalf("%+v %s leaked a credential value:\n%s\n%s", req, format, stdout, stderr)
			}
		}
	}
}

// An unreadable fragment on the way up is warned about and passed over; the
// warning does not change the exit code.
func TestUnreadableAncestorFragmentWarnsWithoutFailing(t *testing.T) {
	app := testApp(t, nil)
	broken := chdirTo(t, filepath.Join(t.TempDir(), "broken"))
	mkdirs(t, filepath.Join(broken, fragmentRelPath))
	chdirTo(t, filepath.Join(broken, "sub"))
	code, stdout, stderr := captureBoth(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true})
		return c
	})
	if code != constants.ExitOK || !strings.Contains(stderr, "fragment could not be read") || !strings.Contains(stderr, broken) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// kae's global mise fragment sits where HOME's own fragment would, with the
// default XDG layout; it is not a binding of HOME.
func TestGlobalFragmentIsNotABindingOfHome(t *testing.T) {
	app := testApp(t, nil)
	global := app.Paths.MiseGlobalFragmentFile()
	if global != filepath.Join(app.Env.Home, fragmentRelPath) {
		t.Fatalf("fixture assumption: the global fragment %s is HOME's fragment path", global)
	}
	writeFile(t, global, fragModePrefix+modeIsolated+"\n"+fragAccountPrefix+"claude=main\n")
	chdirTo(t, filepath.Join(app.Env.Home, "code", "side-project"))
	rows := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if rows[0].Source != constants.PlaceSourceGlobal || rows[0].Path != filepath.Join(app.Env.Home, ".claude") {
		t.Fatalf("the global fragment was read as a binding: %+v", rows[0])
	}
	if code, _ := captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.PlaceGroupPin, current: true})
		return c
	}); code != constants.ExitNotFound {
		t.Fatalf("pin --current under HOME with only the global fragment = %d, want not_found", code)
	}
}

// chdirLogical moves into dir and makes cwdAbs spell it as given, the way a shell
// that followed a symlink reports $PWD.
func chdirLogical(t *testing.T, dir string) string {
	t.Helper()
	chdirTo(t, dir)
	t.Setenv("PWD", dir)
	if got := mustCwdAbs(t); got != dir {
		t.Fatalf("fixture: cwdAbs = %q, want the logical %q", got, dir)
	}
	return dir
}

// A repository rooted at HOME still counts every level between cwd and HOME as
// inside it, and claude's settings.local.json then stays in cwd.
func TestProjectLevelsInARepositoryRootedAtHome(t *testing.T) {
	app := testApp(t, nil)
	home := chdirTo(t, app.Env.Home)
	app.Env.Home = home
	scratchGit(t, home, "init", "-q")
	writeFile(t, filepath.Join(home, "proj", ".codex", "config.toml"), "\n")
	writeFile(t, filepath.Join(home, "proj", ".claude", "settings.json"), "{}\n")
	writeFile(t, filepath.Join(home, "proj", ".claude", "skills", "demo", "SKILL.md"), "# demo\n")
	writeFile(t, filepath.Join(home, "proj", "sub", ".claude", "settings.local.json"), "{}\n")
	cwd := chdirTo(t, filepath.Join(home, "proj", "sub"))

	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	if got, want := kindsAndPaths(codex), []string{
		"user " + filepath.Join(home, ".codex"),
		"project " + filepath.Join(home, "proj", ".codex"),
	}; !slices.Equal(got, want) {
		t.Fatalf("codex places:\n got %v\nwant %v", got, want)
	}
	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if got, want := kindsAndPaths(claude), []string{
		"user " + filepath.Join(home, ".claude"),
		"project " + filepath.Join(cwd, ".claude"),
		"project " + filepath.Join(home, "proj", ".claude"),
	}; !slices.Equal(got, want) {
		t.Fatalf("claude places:\n got %v\nwant %v", got, want)
	}
	if !slices.Equal(claude[1].Applies, []string{constants.PlaceAppliesLocalSettings}) ||
		!slices.Equal(claude[2].Applies, []string{constants.PlaceAppliesSkillsAgents}) {
		t.Fatalf("applies: %v / %v", claude[1].Applies, claude[2].Applies)
	}
	repo := placesOf(t, app, lsRequest{target: constants.PlaceGroupRepo})
	if len(repo) != 1 || !samePath(repo[0].Path, home) {
		t.Fatalf("repo = %+v, want HOME", repo)
	}
}

// A cwd reached through a symlink into a repository walks the physical parents,
// so the tool groups agree with the repo group.
func TestProjectLevelsThroughASymlinkIntoARepository(t *testing.T) {
	app := testApp(t, nil)
	base := chdirTo(t, t.TempDir())
	repo := filepath.Join(base, "w", "r")
	mkdirs(t, repo)
	scratchGit(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, ".codex", "config.toml"), "\n")
	writeFile(t, filepath.Join(repo, ".claude", "settings.local.json"), "{}\n")
	writeFile(t, filepath.Join(repo, "sub", ".codex", "config.toml"), "\n")
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(repo, "sub"), link); err != nil {
		t.Fatal(err)
	}
	chdirLogical(t, link)

	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	if got, want := kindsAndPaths(codex), []string{
		"user " + filepath.Join(app.Env.Home, ".codex"),
		"project " + filepath.Join(link, ".codex"),
		"project " + filepath.Join(repo, ".codex"),
	}; !slices.Equal(got, want) {
		t.Fatalf("codex places:\n got %v\nwant %v", got, want)
	}
	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if len(claude) != 2 || claude[1].Path != filepath.Join(repo, ".claude") ||
		!slices.Equal(claude[1].Applies, []string{constants.PlaceAppliesLocalSettings}) {
		t.Fatalf("claude places: %v %+v", kindsAndPaths(claude), claude)
	}
	rows := placesOf(t, app, lsRequest{target: constants.PlaceGroupRepo})
	if len(rows) != 1 || rows[0].Path != repo {
		t.Fatalf("repo = %+v, want %s", rows, repo)
	}
}

// A binding reached under another spelling of its directory is the recorded
// binding: its store, and its numbered row in the pin group.
func TestBindingUnderAnotherSpellingUsesItsBreadcrumb(t *testing.T) {
	app := overlayTestApp(t)
	captureClaude(t, app, "main", mainToken)
	base := chdirTo(t, t.TempDir())
	bound := chdirTo(t, filepath.Join(base, "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	link := filepath.Join(base, "alias")
	if err := os.Symlink(bound, link); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, filepath.Join(bound, "sub"))
	chdirLogical(t, filepath.Join(link, "sub"))

	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if want := app.Paths.IsolatedConfigDir(paths.PinID(bound), constants.ToolClaude, "main"); claude[0].Path != want || !claude[0].Exists {
		t.Fatalf("user level = %+v, want the recorded binding's store %s", claude[0], want)
	}
	code, out := captureStdout(t, func() int {
		return runLsRequest(context.Background(), app, commonOpts{Format: formatJSON}, lsRequest{target: constants.PlaceGroupPin, current: true})
	})
	mustExit(t, constants.ExitOK, code, out)
	var report placeReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Place.Number != 1 || report.Path != bound {
		t.Fatalf("pin --current = %s, want row 1 at %s", out, bound)
	}
}

// One walk, one warning: `kae ls pin --current` under an unreadable fragment.
func TestPinCurrentWarnsOnce(t *testing.T) {
	app := testApp(t, nil)
	broken := chdirTo(t, filepath.Join(t.TempDir(), "broken"))
	mkdirs(t, filepath.Join(broken, fragmentRelPath))
	chdirTo(t, filepath.Join(broken, "sub"))
	_, _, stderr := captureBoth(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.PlaceGroupPin, current: true})
		return c
	})
	if n := strings.Count(stderr, "fragment could not be read"); n != 1 {
		t.Fatalf("warned %d times:\n%s", n, stderr)
	}
}

// An unreadable state.json fails only what needs it: bare ls keeps the config
// error's message and exit, and repo/kae need no state.
func TestUnreadableStateFailsOnlyUserLevels(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir())
	writeFile(t, app.Paths.StateFile(), "{not json")
	ctx := context.Background()
	for _, req := range []lsRequest{{target: constants.PlaceGroupRepo}, {target: constants.PlaceGroupKae}, {target: constants.PlaceGroupKae, at: 1}} {
		if code, out := captureStdout(t, func() int { return runLsRequest(ctx, app, commonOpts{Format: formatText}, req) }); code != constants.ExitOK {
			t.Fatalf("%+v needs no state, exit %d: %s", req, code, out)
		}
	}
	if code, _ := captureStderr(t, func() int {
		c, _ := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true})
		return c
	}); code == constants.ExitOK {
		t.Fatalf("a user level resolved without its state")
	}
	app.ConfigErr = errors.New("boom")
	code, stdout, stderr := captureBoth(t, func() int { return runLs(ctx, app, commonOpts{Format: formatText}) })
	if code != constants.ExitInvalidConfig || !strings.Contains(stderr, "boom") || !strings.Contains(stdout, "kae directories:") {
		t.Fatalf("bare ls = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// The governing binding is per tool: an inner bound directory binding only codex
// leaves the outer one's claude binding in effect, as mise merges the two
// fragments per variable; the pin group's current place is the nearest of any.
func TestNestedBindingsGovernPerTool(t *testing.T) {
	app := testApp(t, nil)
	outer := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	inner := filepath.Join(outer, "side-project")
	writeFile(t, filepath.Join(outer, fragmentRelPath),
		fragModePrefix+modeIsolated+"\n"+fragAccountPrefix+"claude=main\n"+fragAccountPrefix+"codex=main\n")
	writeFile(t, filepath.Join(inner, fragmentRelPath),
		fragModePrefix+modeShared+"\n"+fragAccountPrefix+"codex=side\n")
	chdirTo(t, filepath.Join(inner, "sub"))

	claude := placesOf(t, app, lsRequest{target: constants.ToolClaude})
	if want := app.Paths.IsolatedConfigDir(paths.PinID(outer), constants.ToolClaude, "main"); claude[0].Path != want ||
		claude[0].Mode != constants.ModeIsolated || claude[0].Account != "main" {
		t.Fatalf("claude user level = %+v, want the outer binding's store %s", claude[0], want)
	}
	codex := placesOf(t, app, lsRequest{target: constants.ToolCodex})
	if want := app.Paths.SharedDir(paths.PinID(inner), constants.ToolCodex); codex[0].Path != want ||
		codex[0].Mode != constants.ModeShared || codex[0].Account != "side" {
		t.Fatalf("codex user level = %+v, want the inner binding's store %s", codex[0], want)
	}
	if code, got := pickPath(t, app, lsRequest{target: constants.PlaceGroupPin, current: true}); code != 0 || got != inner {
		t.Fatalf("pin --current = %d %q, want the nearest bound directory %s", code, got, inner)
	}
	pc, err := app.newPlaceContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pc.toolRelevant(constants.ToolClaude) || !pc.toolRelevant(constants.ToolCodex) {
		t.Fatalf("both tools are bound here, by different directories")
	}
}

// In bare ls a group other than the account group that fails is left out with a
// warning, in both formats, and the exit code does not change; the one-group
// request for it still fails.
func TestBareLsLeavesAFailingGroupOutWithAWarning(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir())
	// A file where the isolation directory belongs: the breadcrumb index cannot
	// be read.
	writeFile(t, app.Paths.IsolationDir(), "not a directory\n")
	ctx := context.Background()

	code, stdout, stderr := captureBoth(t, func() int { return runLs(ctx, app, commonOpts{Format: formatText}) })
	if code != constants.ExitOK || !strings.Contains(stderr, "warning: the pin group is not listed") {
		t.Fatalf("bare ls = %d, stderr %q", code, stderr)
	}
	if strings.Contains(stdout, "Bound directories") || !strings.Contains(stdout, "Accounts:") || !strings.Contains(stdout, "kae directories:") {
		t.Fatalf("only the pin group is left out:\n%s", stdout)
	}
	code, stdout, stderr = captureBoth(t, func() int { return runLs(ctx, app, commonOpts{Format: formatJSON}) })
	if code != constants.ExitOK || !strings.Contains(stderr, "the pin group is not listed") {
		t.Fatalf("bare ls --json = %d, stderr %q", code, stderr)
	}
	var report bareLsReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil || report.Accounts == nil || report.Profiles == nil {
		t.Fatalf("bare ls --json keeps accounts and profiles: %v %s", err, stdout)
	}
	for _, row := range report.Places {
		if row.Group == constants.PlaceGroupPin {
			t.Fatalf("a pin row survived the failed index: %s", stdout)
		}
	}
	if len(report.Places) == 0 {
		t.Fatalf("the other groups are still reported: %s", stdout)
	}
	for _, req := range []lsRequest{{target: constants.PlaceGroupPin}, {target: constants.PlaceGroupPin, at: 1}} {
		code, _, _ := captureBoth(t, func() int { return runLsRequest(ctx, app, commonOpts{Format: formatText}, req) })
		if code == constants.ExitOK {
			t.Fatalf("%+v: the pin group's own request still fails", req)
		}
	}
}
