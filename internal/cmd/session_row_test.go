package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/picker"
)

// claude's session row is `<user level>/projects/<name>` (docs/CLI.md § kae ls
// Semantics). These tests pin where it sits, what decides its name and when it is
// a candidate; the name rule itself is claude.ProjectDirName's.

// physicalName is the session directory name for the test's current directory.
func physicalName(t *testing.T) string {
	t.Helper()
	cwd, err := syscall.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return claude.ProjectDirName(cwd)
}

// sessionOf is the session row of a `kae ls claude` listing, or fails.
func sessionOf(t *testing.T, rows []placeRow) placeRow {
	t.Helper()
	for _, row := range rows {
		if row.Kind == constants.PlaceKindSession {
			return row
		}
	}
	t.Fatalf("no session row in %v", kindsAndPaths(rows))
	return placeRow{}
}

func claudeRows(t *testing.T, app *App, explicit *explicitUserLevel) []placeRow {
	t.Helper()
	return placesOf(t, app, lsRequest{target: constants.ToolClaude, explicit: explicit})
}

// The row sits directly after the user level and is always listed, marked missing
// until claude has written there; it is not on the real-home row of another
// level, and codex has none.
func TestSessionRowFollowsTheUserLevelAndTracksExistence(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	name := physicalName(t)
	home := filepath.Join(app.Env.Home, ".claude")

	rows := claudeRows(t, app, nil)
	if len(rows) != 2 || rows[0].Kind != constants.PlaceKindUser || rows[1].Kind != constants.PlaceKindSession {
		t.Fatalf("rows = %v, want the user level then its session row", kindsAndPaths(rows))
	}
	want := filepath.Join(home, "projects", name)
	if rows[1].Path != want || rows[1].Exists || rows[1].Number != 2 || rows[1].Group != constants.ToolClaude || rows[1].Root != "" {
		t.Fatalf("session row = %+v, want a missing row at %s", rows[1], want)
	}
	if rows[1].Source != rows[0].Source || rows[1].Mode != rows[0].Mode || !rows[1].InEffect {
		t.Fatalf("the session row reports how its user level was decided: %+v vs %+v", rows[1], rows[0])
	}
	// The real home listed beside another user level (an isolated one) has no row.
	isolated := claudeRows(t, app, &explicitUserLevel{tool: constants.ToolClaude, isolated: true, account: "side"})
	if got, want := kindsAndPaths(isolated), []string{
		"user " + app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side"),
		"session " + filepath.Join(app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side"), "projects", name),
		"home " + home,
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("-i claude side rows:\n got %v\nwant %v", got, want)
	}

	mkdirs(t, want)
	if row := sessionOf(t, claudeRows(t, app, nil)); !row.Exists {
		t.Fatalf("the directory exists now: %+v", row)
	}
	// A file where the directory should be is not a session directory.
	if err := os.RemoveAll(want); err != nil {
		t.Fatal(err)
	}
	writeFile(t, want, "x")
	if row := sessionOf(t, claudeRows(t, app, nil)); row.Exists {
		t.Fatalf("a file is not a session directory: %+v", row)
	}

	for _, row := range placesOf(t, app, lsRequest{target: constants.ToolCodex}) {
		if row.Kind == constants.PlaceKindSession {
			t.Fatalf("codex stores sessions by date and has no session row: %+v", row)
		}
	}
}

// A bound directory's store holds the sessions: an -i bind's private projects/,
// and an -s bind's link into the real home's, whose existence is seen through the
// link. An explicit -i or -s moves the row with the user level.
func TestSessionRowFollowsBindingsAndExplicitLevels(t *testing.T) {
	app := overlayTestApp(t)
	captureClaude(t, app, "main", mainToken)
	realProjects := filepath.Join(app.Env.Home, ".claude", "projects")
	mkdirs(t, realProjects)
	base := chdirTo(t, t.TempDir())

	isolated := chdirTo(t, filepath.Join(base, "iso-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin -i: %s", out)
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(isolated), constants.ToolClaude, "main")
	name := physicalName(t)
	row := sessionOf(t, claudeRows(t, app, nil))
	if row.Path != filepath.Join(store, "projects", name) || row.Exists || row.Source != constants.PlaceSourcePin || row.Mode != constants.ModeIsolated {
		t.Fatalf("-i bind session row = %+v, want the store's projects/%s", row, name)
	}
	mkdirs(t, row.Path)
	if row := sessionOf(t, claudeRows(t, app, nil)); !row.Exists {
		t.Fatalf("the store's session directory exists: %+v", row)
	}

	shared := chdirTo(t, filepath.Join(base, "shared-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin -s: %s", out)
	}
	link := filepath.Join(app.Paths.SharedDir(paths.PinID(shared), constants.ToolClaude), "projects")
	if target, err := os.Readlink(link); err != nil || target != realProjects {
		t.Fatalf("fixture: the shared store must link projects/ into the real home (%q, %v)", target, err)
	}
	sname := physicalName(t)
	row = sessionOf(t, claudeRows(t, app, nil))
	if row.Path != filepath.Join(filepath.Dir(link), "projects", sname) || row.Exists {
		t.Fatalf("-s bind session row = %+v", row)
	}
	mkdirs(t, filepath.Join(realProjects, sname))
	if row := sessionOf(t, claudeRows(t, app, nil)); !row.Exists || row.Mode != constants.ModeShared {
		t.Fatalf("the directory exists in the real home and is seen through the store's link: %+v", row)
	}

	// Explicit levels win over the binding.
	side := sessionOf(t, claudeRows(t, app, &explicitUserLevel{tool: constants.ToolClaude, isolated: true, account: "side"}))
	if side.Path != filepath.Join(app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "side"), "projects", sname) || side.Source != constants.PlaceSourceExplicit {
		t.Fatalf("-i claude side session row = %+v", side)
	}
	real := sessionOf(t, claudeRows(t, app, &explicitUserLevel{tool: constants.ToolClaude}))
	if real.Path != filepath.Join(realProjects, sname) || real.Source != constants.PlaceSourceExplicit || !real.Exists {
		t.Fatalf("-s claude session row = %+v", real)
	}
}

// CLAUDE_CODE_PROJECT_DIR_NAME replaces the derived name only where claude would
// honour it: with a valid value and CLAUDE_CONFIG_DIR in the launching
// environment. Bindings, `kae use -i` homes and an explicit -i export the latter;
// the real home does only when the user's own variable named it, so the variable
// a binding exported does not count under an explicit -s.
func TestSessionRowHonoursTheProjectDirNameOverrideOnlyWhereClaudeDoes(t *testing.T) {
	const override = "work"
	for _, tc := range []struct {
		name     string
		env      func(app *App, own string) map[string]string
		explicit *explicitUserLevel
		bound    bool
		honoured bool
	}{
		{"no CLAUDE_CONFIG_DIR", func(*App, string) map[string]string { return nil }, nil, false, false},
		{"the user's own CLAUDE_CONFIG_DIR", func(_ *App, own string) map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": own} }, nil, false, true},
		{
			"explicit -i exports it", func(*App, string) map[string]string { return nil },
			&explicitUserLevel{tool: constants.ToolClaude, isolated: true, account: "side"}, false, true,
		},
		{"a pin exports it", func(*App, string) map[string]string { return nil }, nil, true, true},
		{"-s inside a bind that exported CLAUDE_CONFIG_DIR", func(app *App, _ string) map[string]string {
			return map[string]string{"CLAUDE_CONFIG_DIR": app.Paths.IsolatedConfigDir("0123456789abcdef", constants.ToolClaude, "main")}
		}, &explicitUserLevel{tool: constants.ToolClaude}, false, false},
		{
			"-s with the user's own CLAUDE_CONFIG_DIR", func(_ *App, own string) map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": own} },
			&explicitUserLevel{tool: constants.ToolClaude}, false, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{claude.ProjectDirNameEnv: override}
			app := testApp(t, env)
			for k, v := range tc.env(app, filepath.Join(t.TempDir(), "own-config")) {
				env[k] = v
			}
			cwd := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
			if tc.bound {
				writeFile(t, filepath.Join(cwd, fragmentRelPath), fragModePrefix+modeShared+"\n"+fragAccountPrefix+"claude=main\n")
			}
			got := filepath.Base(sessionOf(t, claudeRows(t, app, tc.explicit)).Path)
			want := physicalName(t)
			if tc.honoured {
				want = override
			}
			if got != want {
				t.Fatalf("session name = %q, want %q", got, want)
			}
		})
	}
}

// An override claude ignores is ignored here: the derived name stands.
func TestSessionRowIgnoresAnInvalidOverride(t *testing.T) {
	for _, value := range []string{"a b", strings.Repeat("x", 65), "", "con", "COM1", "lpt9"} {
		env := map[string]string{"CLAUDE_CONFIG_DIR": "", claude.ProjectDirNameEnv: value}
		app := testApp(t, env)
		own := filepath.Join(t.TempDir(), "own-config")
		env["CLAUDE_CONFIG_DIR"] = own
		chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
		if got := sessionOf(t, claudeRows(t, app, nil)).Path; got != filepath.Join(own, "projects", physicalName(t)) {
			t.Errorf("override %q: session row %s, want the derived name", value, got)
		}
	}
	env := map[string]string{claude.ProjectDirNameEnv: "work"}
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(t.TempDir(), "own-config")
	app := testApp(t, env)
	chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	if got := filepath.Base(sessionOf(t, claudeRows(t, app, nil)).Path); got != "work" {
		t.Errorf("a valid override = %q, want work", got)
	}
}

// The name is the physical directory's: through a symlink the target's, and in
// another case the on-disk one, each as the kernel says it and not as typed.
func TestSessionRowNamesThePhysicalDirectory(t *testing.T) {
	app := testApp(t, nil)
	base := chdirTo(t, t.TempDir())
	target := filepath.Join(base, "Real-Dir")
	mkdirs(t, target)
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	chdirLogical(t, link)
	physical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got := filepath.Base(sessionOf(t, claudeRows(t, app, nil)).Path)
	if want := claude.ProjectDirName(physical); got != want {
		t.Fatalf("through a symlink: session name %q, want the target's %q", got, want)
	}

	if _, err := os.Stat(filepath.Join(base, "real-dir")); err != nil {
		t.Skip("the filesystem is case-sensitive: a directory has one spelling")
	}
	if err := os.Chdir(filepath.Join(base, "REAL-DIR")); err != nil {
		t.Fatal(err)
	}
	got = filepath.Base(sessionOf(t, claudeRows(t, app, nil)).Path)
	if want := claude.ProjectDirName(physical); got != want {
		t.Fatalf("after cd in another case: session name %q, want the on-disk %q", got, want)
	}
}

// A subdirectory and a linked worktree have transcripts of their own, so their rows
// differ from the repository root's.
func TestSessionRowDiffersPerDirectoryAndWorktree(t *testing.T) {
	app := testApp(t, nil)
	root := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	scratchGit(t, root, "init", "-q")
	scratchGit(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	scratchGit(t, root, "worktree", "add", "-q", "-b", "side", filepath.Join(filepath.Dir(root), "side-app"))
	rootName := filepath.Base(sessionOf(t, claudeRows(t, app, nil)).Path)
	names := map[string]string{"root": rootName}
	for label, dir := range map[string]string{
		"subdirectory": filepath.Join(root, "sub"),
		"worktree":     filepath.Join(filepath.Dir(root), "side-app"),
	} {
		chdirTo(t, dir)
		names[label] = filepath.Base(sessionOf(t, claudeRows(t, app, nil)).Path)
		if names[label] == rootName || names[label] != physicalName(t) {
			t.Fatalf("%s session name %q (root's %q, own %q)", label, names[label], rootName, physicalName(t))
		}
	}
	if names["subdirectory"] == names["worktree"] {
		t.Fatalf("names must be distinct: %v", names)
	}
}

// When the current directory cannot be resolved only this row is left out, with a
// warning that does not change the exit code.
func TestSessionRowIsOmittedWithAWarningWhenTheDirectoryCannotBeResolved(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	prev := physicalWd
	physicalWd = func() (string, error) { return "", errors.New("getcwd failed") }
	t.Cleanup(func() { physicalWd = prev })
	var rows []placeRow
	code, stderr := captureStderr(t, func() int {
		rows = claudeRows(t, app, nil)
		return constants.ExitOK
	})
	if code != constants.ExitOK || len(rows) != 1 || rows[0].Kind != constants.PlaceKindUser {
		t.Fatalf("rows = %v", kindsAndPaths(rows))
	}
	if strings.Count(stderr, "kae: warning:") != 1 || !strings.Contains(stderr, "session row is not listed") {
		t.Fatalf("want one warning naming the row:\n%s", stderr)
	}
}

// A missing session directory is not a candidate and cannot be reached; no selector
// chooses the row; --root has no root to reach; --current is still the user level.
func TestSessionRowIsListedButNotReachedUnlessItExists(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	mkdirs(t, filepath.Join(cwd, ".claude"), filepath.Join(app.Env.Home, ".claude"), app.Paths.ConfigDir)
	stub := withPicker(app)
	session := sessionOf(t, claudeRows(t, app, nil))

	items := stub.offeredValues(t, app, "cd", lsFlags{pick: true}, "claude")
	if items[session.Path] {
		t.Fatalf("a missing session directory must not be a candidate: %v", items)
	}
	code, out, stderr := cdPathOrOpen(t, app, "open", navRequest(t, "open", lsFlags{at: atFlag{n: session.Number, set: true}}, "claude"))
	if code != constants.ExitNotFound {
		t.Fatalf("open claude --at %d = %d, want 7 for a missing directory:\n%s%s", session.Number, code, out, stderr)
	}

	mkdirs(t, session.Path)
	if items := stub.offeredValues(t, app, "cd", lsFlags{pick: true}, "claude"); !items[session.Path] {
		t.Fatalf("an existing session directory is a candidate: %v", items)
	}
	if code, got, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{at: atFlag{n: session.Number, set: true}}, "claude")); code != constants.ExitOK || got != session.Path {
		t.Fatalf("cd claude --at %d = %d %q %s", session.Number, code, got, stderr)
	}

	// With --root the row has no root, so it is not a candidate and --at is refused.
	if items := stub.offeredValues(t, app, "cd", lsFlags{pick: true, root: true, project: true}, "claude"); items[session.Path] {
		t.Fatalf("--root offers only places with a root: %v", items)
	}
	if code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{at: atFlag{n: session.Number, set: true}, root: true}, "claude")); code != constants.ExitUsage || !strings.Contains(stderr, "--root applies to a project level") {
		t.Fatalf("cd claude --at %d --root = %d %s", session.Number, code, stderr)
	}

	// No selector chooses the row, and --current is the user level as before.
	user := filepath.Join(app.Env.Home, ".claude")
	for _, f := range []lsFlags{{}, {project: true}, {home: true}} {
		req := navRequest(t, "cd", f, "claude")
		code, got, stderr := cdPath(t, app, req)
		if code != constants.ExitOK || got == session.Path {
			t.Fatalf("%+v chose the session row: %d %q %s", f, code, got, stderr)
		}
		if f == (lsFlags{}) && got != user {
			t.Fatalf("--current = %q, want the user level %q", got, user)
		}
	}
	if _, current := pickPath(t, app, lsRequest{target: constants.ToolClaude, current: true}); current != user {
		t.Fatalf("ls claude --current = %q, want the user level", current)
	}
}

// offeredValues runs the request and returns the set of values the picker offered.
func (s *pickerStub) offeredValues(t *testing.T, app *App, verb string, f lsFlags, pos ...string) map[string]bool {
	t.Helper()
	before := len(s.offered)
	code, stdout, stderr := cdPathOrOpen(t, app, verb, navRequest(t, verb, f, pos...))
	if code != constants.ExitOK && code != constants.ExitCancelled && code != constants.ExitNotFound {
		t.Fatalf("kae %s %v = %d:\n%s\n%s", verb, pos, code, stdout, stderr)
	}
	values := map[string]bool{}
	if len(s.offered) > before {
		for _, it := range s.offered[before] {
			if it.Kind == picker.Row {
				values[it.Value] = true
			}
		}
	}
	return values
}

// The numbers `kae ls claude` shows, the ones the candidate lists print and the
// picker's dim numbers are one numbering, with the session row in it.
func TestSessionRowNumberingIsOneAcrossLsAndPicker(t *testing.T) {
	app, root, sub := pickerRepo(t)
	rows := claudeRows(t, app, nil)
	session := sessionOf(t, rows)
	mkdirs(t, session.Path)
	rows = claudeRows(t, app, nil)

	// Without a terminal the candidate list prints the same numbers.
	code, _, list := cdPath(t, app, navRequest(t, "cd", lsFlags{pick: true}, "claude"))
	if code != constants.ExitUsage || !strings.Contains(list, "kae cd claude --at 2  "+session.Path) {
		t.Fatalf("candidate list = %d:\n%s", code, list)
	}

	stub := withPicker(app)
	offeredOnce(t, app, stub, "cd", lsFlags{pick: true}, "claude")
	notes := map[string]string{}
	for _, it := range stub.offered[0] {
		if it.Kind == picker.Row && it.Note != "" {
			notes[it.Value] = it.Note
		}
	}
	for _, row := range rows {
		value := row.Path
		if row.Root != "" {
			value = row.Root
		}
		if !row.Exists {
			continue
		}
		if got, want := notes[value], "#"+strconv.Itoa(row.Number); got != want {
			t.Fatalf("picker number for %s %s = %q, want %q (ls: %v)", row.Kind, value, got, want, kindsAndPaths(rows))
		}
	}
	if session.Number != 2 || rows[2].Root != sub || rows[3].Root != root {
		t.Fatalf("the session row sits right after the user level, before the project levels: %v", kindsAndPaths(rows))
	}
}

// Bare ls and the picker show claude's group when its session directory exists,
// even with no binding and no project level; a missing one does not make it
// relevant.
func TestSessionDirectoryMakesClaudeRelevant(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, filepath.Join(t.TempDir(), "side-project"))
	mkdirs(t, app.Paths.ConfigDir)
	groups := func() []string {
		g := app.collectPlaceGroups(context.Background(), app.readState(), warnGroupOnce())
		return g.tools
	}
	if got := groups(); len(got) != 0 {
		t.Fatalf("no binding, no project level and no session directory: tool groups %v", got)
	}
	mkdirs(t, filepath.Join(app.Env.Home, ".claude", "projects", physicalName(t)))
	if got := groups(); len(got) != 1 || got[0] != constants.ToolClaude {
		t.Fatalf("an existing session directory makes claude relevant: %v", got)
	}
}

// The row is a path and how it was decided; a credential in the store or in the
// session directory never reaches any output.
func TestSessionRowNeverCarriesACredential(t *testing.T) {
	const canary = "sk-ant-oat01-SESSION-CANARY-eeee"
	app := testApp(t, nil)
	chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	session := filepath.Join(app.Env.Home, ".claude", "projects", physicalName(t))
	writeFile(t, filepath.Join(session, "a.jsonl"), `{"token":"`+canary+`"}`+"\n")
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"`+canary+`"}}`)
	for _, req := range []lsRequest{{}, {target: constants.ToolClaude}, {target: constants.ToolClaude, at: 2}} {
		for _, format := range []string{formatText, formatJSON} {
			code, stdout, stderr := captureBoth(t, func() int {
				return runLsRequest(context.Background(), app, commonOpts{Format: format}, req)
			})
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			if !strings.Contains(stdout, session) && !strings.Contains(stdout, app.displayPath(session)) {
				t.Fatalf("%+v %s: the session row must be listed, or this canary proves nothing:\n%s", req, format, stdout)
			}
			if strings.Contains(stdout+stderr, canary) {
				t.Fatalf("%+v %s leaked a credential value:\n%s\n%s", req, format, stdout, stderr)
			}
			if format == formatJSON && req.at == 0 && !regexp.MustCompile(`"kind":\s*"`+constants.PlaceKindSession+`"`).MatchString(stdout) {
				t.Fatalf("the JSON kind token is %q:\n%s", constants.PlaceKindSession, stdout)
			}
		}
	}
}

// An unreadable current directory is worth a warning only when claude's group is
// shown anyway; deciding relevance from the session directory stays silent.
func TestUnresolvableDirectoryWarnsOnlyWhereTheClaudeGroupIsShown(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, filepath.Join(t.TempDir(), "side-project"))
	mkdirs(t, app.Paths.ConfigDir)
	prev := physicalWd
	physicalWd = func() (string, error) { return "", errors.New("getcwd failed") }
	t.Cleanup(func() { physicalWd = prev })
	var tools []string
	_, stderr := captureStderr(t, func() int {
		tools = app.collectPlaceGroups(context.Background(), app.readState(), warnGroupOnce()).tools
		return constants.ExitOK
	})
	if len(tools) != 0 || strings.Contains(stderr, "warning") {
		t.Fatalf("tool groups %v, stderr %q; want no group and no warning", tools, stderr)
	}
}
