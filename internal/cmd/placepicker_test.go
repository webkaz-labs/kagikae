package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/picker"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
	"github.com/webkaz-labs/kagikae/internal/textui"
)

// pickerStub stands in for the picker and the terminal it needs: it records
// exactly what it was offered and answers with choose (by default the first
// row).
type pickerStub struct {
	offered [][]picker.Item
	opts    []picker.Options
	choose  func(items []picker.Item) (value string, cancelled bool, err error)
}

func withPicker(app *App) *pickerStub {
	stub := &pickerStub{}
	app.openTerminal = func() (*textui.Terminal, bool) { return &textui.Terminal{}, true }
	app.pick = func(_ context.Context, items []picker.Item, opts picker.Options) (string, bool, error) {
		stub.offered = append(stub.offered, items)
		stub.opts = append(stub.opts, opts)
		if stub.choose != nil {
			return stub.choose(items)
		}
		for _, it := range items {
			if it.Kind == picker.Row {
				return it.Value, false, nil
			}
		}
		return "", true, nil
	}
	return stub
}

// itemsSummary is what the picker shows, one line per item: a heading as
// "# group", a row as "label -> value", indented one level per depth.
func itemsSummary(items []picker.Item) []string {
	var out []string
	for _, it := range items {
		if it.Kind == picker.Heading {
			out = append(out, "# "+it.Label)
			continue
		}
		out = append(out, strings.Repeat("  ", it.Depth)+it.Label+" -> "+it.Value)
	}
	return out
}

// offeredOnce runs the request and returns what the picker was offered (it must
// have been offered exactly once).
func offeredOnce(t *testing.T, app *App, stub *pickerStub, verb string, f lsFlags, pos ...string) []string {
	t.Helper()
	before := len(stub.offered)
	code, stdout, stderr := cdPathOrOpen(t, app, verb, navRequest(t, verb, f, pos...))
	if code != constants.ExitOK && code != constants.ExitCancelled {
		t.Fatalf("kae %s %v = %d:\n%s\n%s", verb, pos, code, stdout, stderr)
	}
	if len(stub.offered) != before+1 {
		t.Fatalf("kae %s %v opened the picker %d time(s), want once:\n%s", verb, pos, len(stub.offered)-before, stderr)
	}
	return itemsSummary(stub.offered[before])
}

// pickerRepo is a repository at root with claude project levels at root and at
// root/sub (the current directory), below levels under sub, a codex level at
// root, and the real ~/.claude; ~/.codex is missing.
func pickerRepo(t *testing.T) (app *App, root, sub string) {
	t.Helper()
	app = testApp(t, nil)
	root = chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	scratchGit(t, root, "init", "-q")
	sub = filepath.Join(root, "sub")
	mkdirs(t, filepath.Join(root, ".claude"), filepath.Join(root, ".codex"), filepath.Join(sub, ".claude"),
		filepath.Join(sub, "x", ".claude"), filepath.Join(sub, "y", ".claude"),
		filepath.Join(app.Env.Home, ".claude"), app.Paths.ConfigDir, app.Paths.DataDir)
	// Below levels are found through git, which lists files, not empty directories.
	for _, dir := range []string{sub, filepath.Join(sub, "x"), filepath.Join(sub, "y")} {
		writeFile(t, filepath.Join(dir, ".claude", "settings.json"), "{}\n")
	}
	chdirTo(t, sub)
	return app, root, sub
}

// Every request that names no single place, and --pick, offers the picker one
// list: groups relevant here first, project and below places as a root with its
// level beneath, only the roots with --root, and the places that exist.
func TestPickerOffersEachEntryPoint(t *testing.T) {
	app, root, sub := pickerRepo(t)
	stub := withPicker(app)
	d := app.displayPath
	home := app.Env.Home
	level := func(dir, name string) []string {
		// the level directory is its bare name beneath the root
		return []string{d(dir) + " -> " + dir, "  " + name + " -> " + filepath.Join(dir, name)}
	}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	user := []string{"~/.claude -> " + filepath.Join(home, ".claude")}
	claudeAll := join([]string{"# claude"}, user, level(sub, ".claude"), level(root, ".claude"),
		level(filepath.Join(sub, "x"), ".claude"), level(filepath.Join(sub, "y"), ".claude"))
	kaeRows := []string{"# kae", d(app.Paths.ConfigDir) + " -> " + app.Paths.ConfigDir, d(app.Paths.DataDir) + " -> " + app.Paths.DataDir}
	belowClaude := join([]string{"# claude"}, level(filepath.Join(sub, "x"), ".claude"), level(filepath.Join(sub, "y"), ".claude"))
	roots := func(dirs ...string) []string {
		var out []string
		for _, dir := range dirs {
			out = append(out, d(dir)+" -> "+dir)
		}
		return out
	}

	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"no target", lsFlags{}, nil, join(claudeAll, []string{"# codex"}, level(root, ".codex"),
			[]string{"# repo", d(root) + " -> " + root}, kaeRows)},
		{"several matches", lsFlags{}, []string{"kae"}, kaeRows},
		{"several below levels", lsFlags{below: true}, []string{"claude"}, belowClaude},
		{"level selector without a tool", lsFlags{below: true}, nil, belowClaude},
		{"level selector without a tool, project", lsFlags{project: true}, nil, join(
			[]string{"# claude"}, level(sub, ".claude"), level(root, ".claude"), []string{"# codex"}, level(root, ".codex"),
		)},
		{"--pick, no target", lsFlags{pick: true}, nil, join(claudeAll, []string{"# codex"}, level(root, ".codex"),
			[]string{"# repo", d(root) + " -> " + root}, kaeRows)},
		{"--pick, a group with a current place", lsFlags{pick: true}, []string{"repo"}, []string{"# repo", d(root) + " -> " + root}},
		{"--pick, a tool", lsFlags{pick: true}, []string{"claude"}, claudeAll},
		{"--pick, every project level", lsFlags{pick: true, project: true}, []string{"claude"}, join(
			[]string{"# claude"}, level(sub, ".claude"), level(root, ".claude"),
		)},
		{"--pick, level without a tool", lsFlags{pick: true, project: true}, nil, join(
			[]string{"# claude"}, level(sub, ".claude"), level(root, ".claude"), []string{"# codex"}, level(root, ".codex"),
		)},
		{"--pick --root", lsFlags{pick: true, root: true}, []string{"claude"}, join(
			[]string{"# claude"}, roots(sub, root, filepath.Join(sub, "x"), filepath.Join(sub, "y")),
		)},
		{"--pick --project --root", lsFlags{pick: true, project: true, root: true}, []string{"claude"}, join(
			[]string{"# claude"}, roots(sub, root),
		)},
		{"--pick, real home", lsFlags{pick: true, home: true}, []string{"claude"}, join([]string{"# claude"}, user)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := offeredOnce(t, app, stub, "cd", tc.f, tc.pos...)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("offered:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
	// open reaches the same set as cd.
	if got := offeredOnce(t, app, stub, "open", lsFlags{}, "kae"); !slices.Equal(got, kaeRows) {
		t.Fatalf("kae open kae offered %q, want %q", got, kaeRows)
	}
}

// A project root's filter text is what the operator can type to find it: the
// displayed path, the absolute path, the kind and the group.
func TestPickerRowsFilterOnPathKindAndGroup(t *testing.T) {
	app, _, sub := pickerRepo(t)
	stub := withPicker(app)
	offeredOnce(t, app, stub, "cd", lsFlags{pick: true, project: true}, "claude")
	items := stub.offered[0]
	// items: heading, root, level, root, level.
	root, child := items[1], items[2]
	if root.Kind != picker.Row || child.Depth != 1 || child.Parent != 1 || root.Parent != -1 {
		t.Fatalf("a level must sit beneath its root: %+v %+v", root, child)
	}
	for _, want := range []string{app.displayPath(sub), sub, constants.PlaceKindProject, constants.ToolClaude} {
		if !strings.Contains(root.Filter, want) {
			t.Fatalf("root filter text %q lacks %q", root.Filter, want)
		}
	}
	// The root line carries kind and number; the level directory line is its name
	// alone, and still filters on the whole path, kind and group.
	if root.Detail != constants.PlaceKindProject || root.Note != "#2" {
		t.Fatalf("root row %+v", root)
	}
	if child.Label != ".claude" || child.Detail != "" || child.Note != "" || !strings.Contains(child.Filter, filepath.Join(sub, ".claude")) ||
		!strings.Contains(child.Filter, constants.PlaceKindProject) {
		t.Fatalf("level row %+v", child)
	}
	// Choosing the root gives the root; choosing the level gives the level.
	if root.Value != sub || child.Value != filepath.Join(sub, ".claude") {
		t.Fatalf("values: root %q, level %q", root.Value, child.Value)
	}
}

// Groups that bear on the current directory come first, each in kae ls order;
// a pin group with no governing binding comes after them, with kae last.
func TestPickerOrdersRelevantGroupsFirst(t *testing.T) {
	app := overlayTestApp(t)
	captureClaude(t, app, "main", mainToken)
	stub := withPicker(app)
	outside := chdirTo(t, filepath.Join(t.TempDir(), "outside"))
	bound := chdirTo(t, filepath.Join(filepath.Dir(outside), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	mkdirs(t, app.Paths.ConfigDir)
	headings := func(where string) []string {
		chdirTo(t, where)
		var out []string
		for _, line := range offeredOnce(t, app, stub, "cd", lsFlags{}) {
			if strings.HasPrefix(line, "# ") {
				out = append(out, line[2:])
			}
		}
		return out
	}
	// Inside the bound directory the binding governs, so pin leads.
	if got := headings(bound); !slices.Equal(got, []string{"pin", "claude", "kae"}) {
		t.Fatalf("inside a bound directory: %q", got)
	}
	// Outside it, the pin is only somewhere else: claude (its project level) first.
	mkdirs(t, filepath.Join(outside, ".claude"))
	if got := headings(outside); !slices.Equal(got, []string{"claude", "pin", "kae"}) {
		t.Fatalf("outside every bound directory: %q", got)
	}
}

// The chosen path is what cd prints and what open opens; nothing else reaches
// stdout.
func TestPickerChoiceIsReported(t *testing.T) {
	app, root, sub := pickerRepo(t)
	stub := withPicker(app)
	stub.choose = func(items []picker.Item) (string, bool, error) { return filepath.Join(sub, ".claude"), false, nil }
	code, stdout, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}))
	if code != constants.ExitOK || stdout != filepath.Join(sub, ".claude") || stderr != "" {
		t.Fatalf("cd = %d %q %q", code, stdout, stderr)
	}
	app.Env.GOOS = "linux"
	withOpener(app, "xdg-open")
	stub.choose = func(items []picker.Item) (string, bool, error) { return root, false, nil }
	fake := &runnertest.Fake{}
	runner.With(fake, func() {
		code, stdout, stderr = captureBoth(t, func() int {
			return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{}))
		})
	})
	if code != constants.ExitOK || fake.Name != "xdg-open" || !slices.Equal(fake.Args, []string{root}) || stdout != "" || stderr != "" {
		t.Fatalf("open = %d, ran %s %v, out %q %q", code, fake.Name, fake.Args, stdout, stderr)
	}
}

// Backing out exits 130 with nothing on stdout or stderr, for cd and for open
// (which then opens nothing); a picker that fails exits 1.
func TestPickerCancelAndFailure(t *testing.T) {
	app, _, _ := pickerRepo(t)
	stub := withPicker(app)
	stub.choose = func([]picker.Item) (string, bool, error) { return "", true, nil }
	code, stdout, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}))
	if code != constants.ExitCancelled || stdout != "" || stderr != "" {
		t.Fatalf("cancelled cd = %d %q %q", code, stdout, stderr)
	}
	app.Env.GOOS = "linux"
	withOpener(app, "xdg-open")
	fake := &runnertest.Fake{}
	runner.With(fake, func() {
		code, stdout, stderr = captureBoth(t, func() int {
			return runOpen(context.Background(), app, commonOpts{Format: formatText}, navRequest(t, "open", lsFlags{}))
		})
	})
	if code != constants.ExitCancelled || stdout != "" || stderr != "" || fake.Name == "xdg-open" {
		t.Fatalf("cancelled open = %d %q %q, ran %q", code, stdout, stderr, fake.Name)
	}
	stub.choose = func([]picker.Item) (string, bool, error) { return "", false, errors.New("picker: no terminal") }
	code, stdout, stderr = cdPath(t, app, navRequest(t, "cd", lsFlags{}))
	if code != constants.ExitError || stdout != "" || !strings.Contains(stderr, "picker: no terminal") {
		t.Fatalf("failed picker = %d %q %q", code, stdout, stderr)
	}
	// The token is in the table.
	if constants.ErrorCode(constants.ExitCancelled) != "cancelled" || constants.ExitCancelled != 130 {
		t.Fatalf("cancelled is %d %q", constants.ExitCancelled, constants.ErrorCode(constants.ExitCancelled))
	}
}

// The picker opens even over one candidate.
func TestPickerOpensOverOneCandidate(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir())
	mkdirs(t, app.Paths.ConfigDir) // data and state are missing
	stub := withPicker(app)
	want := []string{"# kae", app.displayPath(app.Paths.ConfigDir) + " -> " + app.Paths.ConfigDir}
	if got := offeredOnce(t, app, stub, "cd", lsFlags{}, "kae"); !slices.Equal(got, want) {
		t.Fatalf("offered %q, want %q", got, want)
	}
}

// Without a terminal the list and a usage error stand in for the picker, for a
// request that names no single place and for --pick; a request with nothing to
// offer is not_found with or without one.
func TestNoTerminalKeepsTheList(t *testing.T) {
	app, root, _ := pickerRepo(t)
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want string
	}{
		{"no target", lsFlags{}, nil, "kae cd needs a target; choose one:"},
		{"--pick", lsFlags{pick: true}, []string{"claude"}, "kae cd claude --pick needs a terminal to open the picker; it lists"},
		{"--pick, no target", lsFlags{pick: true}, nil, "kae cd --pick needs a terminal to open the picker; it lists"},
		{"--pick with a current place", lsFlags{pick: true}, []string{"repo"}, "kae cd repo --pick needs a terminal to open the picker; it lists 1 place;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := cdPath(t, app, navRequest(t, "cd", tc.f, tc.pos...))
			if code != constants.ExitUsage || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d stdout %q:\n%s", code, stdout, stderr)
			}
			if strings.Contains(stderr, "  --pick") || strings.Contains(stderr, "(s)") {
				t.Fatalf("a doubled space or a place(s) in the reason:\n%s", stderr)
			}
			// The list itself: a line that reaches a place.
			if !strings.Contains(stderr, "\n  kae cd ") || !strings.Contains(stderr, " --at ") || !strings.Contains(stderr, "  "+root) {
				t.Fatalf("the candidate lines are missing:\n%s", stderr)
			}
		})
	}
	// A terminal that is there changes nothing about an empty set.
	stub := withPicker(app)
	chdirTo(t, t.TempDir())
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{pick: true}, "pin"))
	if code != constants.ExitNotFound || !strings.Contains(stderr, "kae cd pin --pick lists no place") || len(stub.offered) != 0 {
		t.Fatalf("--pick over nothing = %d %q, picker opened %d time(s)", code, stderr, len(stub.offered))
	}
}

// --pick over places that are all missing is "no existing place", with or
// without a terminal, and does not say a terminal is needed.
func TestPickOverMissingPlacesIsNotFound(t *testing.T) {
	app := testApp(t, nil)
	chdirTo(t, t.TempDir()) // none of kae's three directories exists
	for _, withTerminal := range []bool{false, true} {
		if withTerminal {
			withPicker(app)
		}
		code, stdout, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{pick: true}, "kae"))
		if code != constants.ExitNotFound || stdout != "" ||
			!strings.Contains(stderr, "kae cd kae --pick lists 3 places, and no existing place to choose") || strings.Contains(stderr, "needs a terminal") {
			t.Fatalf("terminal %v: %d %q %q", withTerminal, code, stdout, stderr)
		}
	}
}

// --pick is open and cd's alone, and it and --at are two ways to choose.
func TestPickFlagRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    lsFlags
		pos  []string
		want string
	}{
		{"pick and at", lsFlags{pick: true, at: atFlag{n: 1, set: true}}, []string{"claude"}, "--pick chooses a place in the picker and --at names one"},
		{"pick and at, no target", lsFlags{pick: true, at: atFlag{n: 1, set: true}}, nil, "--pick chooses a place"},
		{"account", lsFlags{pick: true}, []string{"account"}, "not places"},
		// --pick waives the level requirement of --root only where a level could
		// follow; every other --root misuse stays a usage error as without --pick.
		{"pick, home, root", lsFlags{pick: true, home: true, root: true}, []string{"claude"}, "add --project or --below"},
		{"pick, home, root, no tool", lsFlags{pick: true, home: true, root: true}, nil, "add --project or --below"},
		{"pick, repo, root", lsFlags{pick: true, root: true}, []string{"repo"}, "add --project or --below"},
		{"pick, kae, root", lsFlags{pick: true, root: true}, []string{"kae"}, "add --project or --below"},
		{"pick, pin, root", lsFlags{pick: true, root: true}, []string{"pin"}, "add --project or --below"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			_, stderr := captureStderr(t, func() int { _, code = parsePlaceArgs("open", tc.f, tc.pos); return code })
			if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d %q, want usage naming %q", code, stderr, tc.want)
			}
		})
	}
	// --pick with a selector, --root or no target parses; with --root it needs no level.
	for _, tc := range []struct {
		f   lsFlags
		pos []string
	}{
		{lsFlags{pick: true}, nil},
		{lsFlags{pick: true, root: true}, []string{"claude"}},
		{lsFlags{pick: true, below: true, root: true}, nil},
		{lsFlags{pick: true, home: true}, []string{"claude"}},
	} {
		if req, code := parsePlaceArgs("cd", tc.f, tc.pos); code != constants.ExitOK || !req.pick {
			t.Fatalf("%+v %v: %+v exit %d", tc.f, tc.pos, req, code)
		}
	}
	// ls does not pick, and open and cd take the flag on the command line.
	code, stderr := captureStderr(t, func() int { return Root([]string{"ls", "--pick"}) })
	if code != constants.ExitUsage || !strings.Contains(stderr, "pick") {
		t.Fatalf("kae ls --pick = %d %q", code, stderr)
	}
	for _, verb := range []string{"open", cdPathCommand} {
		code, stderr := captureStderr(t, func() int { return Root([]string{verb, "claude", "--pick", "--at", "1"}) })
		if code != constants.ExitUsage || !strings.Contains(stderr, "--pick chooses a place") {
			t.Fatalf("kae %s --pick --at = %d %q", verb, code, stderr)
		}
	}
}

// NO_COLOR and --no-color reach the picker; stdout being captured does not
// switch color off, since cd's stdout is always captured.
func TestPickerColorFollowsNoColor(t *testing.T) {
	app, _, _ := pickerRepo(t)
	stub := withPicker(app)
	run := func(noColor bool) {
		captureBoth(t, func() int {
			return runCdPath(context.Background(), app, commonOpts{Format: formatText, NoColor: noColor}, navRequest(t, "cd", lsFlags{}, "kae"))
		})
	}
	t.Setenv("NO_COLOR", "")
	run(false)
	run(true)
	t.Setenv("NO_COLOR", "1")
	run(false)
	if got := []bool{stub.opts[0].NoColor, stub.opts[1].NoColor, stub.opts[2].NoColor}; !slices.Equal(got, []bool{false, true, true}) {
		t.Fatalf("NoColor offered = %v, want [false true true]", got)
	}
}

// What the picker is offered is paths and words, never a credential.
func TestPickerNeverCarriesACredential(t *testing.T) {
	const canary = "sk-ant-oat01-PICKER-CANARY-ffff"
	app := overlayTestApp(t)
	captureClaude(t, app, "main", canary)
	bound := chdirTo(t, filepath.Join(t.TempDir(), "main-app"))
	if code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	}); code != constants.ExitOK {
		t.Fatalf("runPin: %s", out)
	}
	stub := withPicker(app)
	got := offeredOnce(t, app, stub, "cd", lsFlags{pick: true}, "claude")
	all := strings.Join(got, "\n")
	for _, it := range stub.offered[0] {
		all += "\n" + it.Detail + it.Note + it.Extra + it.Filter
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(bound), constants.ToolClaude, "main")
	if !strings.Contains(all, store) {
		t.Fatalf("the bound store %s is not offered; this test would pass vacuously:\n%s", store, all)
	}
	if strings.Contains(all, canary) {
		t.Fatalf("the picker was offered a credential:\n%s", all)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("fixture: %v", err)
	}
}
