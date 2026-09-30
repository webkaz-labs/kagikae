package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// A place is a directory a user wants to reach (docs/CONTEXT.md § Surface terms):
// where a tool reads its settings and sessions, a bound directory, the repository
// root, or kae's own directories. This file resolves them; ls.go lists them. It
// reads directories, fragments, state.json and git's answers, and never a
// credential: a place row carries a path and how it was decided, nothing else.

// placeRow is one place as `kae ls --json` publishes it (docs/CLI.md § `kae ls
// --json`). Number is the `--at` number `kae ls <group>` shows for it.
type placeRow struct {
	Group    string `json:"group"`
	Number   int    `json:"number"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Root     string `json:"root,omitempty"` // project/below: the directory holding .claude/ or .codex/
	Exists   bool   `json:"exists"`
	InEffect bool   `json:"in_effect"`
	Source   string `json:"source,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Account  string `json:"account,omitempty"`
	// Applies is, for a claude project level, what claude reads there
	// (PlaceApplies* tokens); absent on every other row.
	Applies []string `json:"applies,omitempty"`
}

// governingBinding is the nearest ancestor bound directory of the current
// directory: the binding that applies there, read from its fragment rather than
// from the shell's environment, which may be stale or not activated.
type governingBinding struct {
	dir   string
	pinID string
	info  fragmentInfo
}

// explicitUserLevel is `-s <tool>` or `-i <tool> <account>`: the user level named
// on the command line instead of resolved from a binding.
type explicitUserLevel struct {
	tool     string
	isolated bool
	account  string
}

// placeContext is everything place resolution observed once for one command.
// It is operation-local, like boundDirectoryIndex.
type placeContext struct {
	cwd      string
	home     string
	repoRoot string              // "" outside a Git repository
	binding  *governingBinding   // the nearest ancestor bound directory, of any tool
	bindings []*governingBinding // every ancestor bound directory, nearest first
	synced   map[string]string
	stateErr error // state.json could not be read; only user levels need it
	explicit *explicitUserLevel
	below    map[string][]string   // tool -> project levels below cwd (the .claude/.codex dirs)
	project  map[string][]placeRow // tool -> projectLevels, memoized
	belowSet bool
	// physicalCwd is the working directory as the kernel names it (physicalWd),
	// which is what claude names its session directory from; read at most once.
	physicalCwd func() (string, error)
	cwdWarned   bool
}

// placeTools are the tools kae resolves places for: the ones with a home it can
// name (realToolHome). Other tools list their accounts and no places.
func placeTools() []string {
	out := []string{}
	for _, tool := range constants.Tools {
		if projectLevelName(tool) != "" {
			out = append(out, tool)
		}
	}
	return out
}

// projectLevelName is the directory a tool reads project settings from, or ""
// for a tool without a documented or measured discovery rule (docs/CLI.md § kae
// ls Semantics).
func projectLevelName(tool string) string {
	switch tool {
	case constants.ToolClaude:
		return ".claude"
	case constants.ToolCodex:
		return ".codex"
	}
	return ""
}

// newPlaceContext observes the current directory, its repository, the governing
// binding and the global isolation state. A state.json that cannot be read is
// kept as stateErr rather than failing the context: only a tool's user level
// needs it (guessing the real home for a globally isolated tool would name the
// wrong place with confidence), and the repo, kae and pin groups do not.
func (app *App) newPlaceContext(ctx context.Context, explicit *explicitUserLevel) (*placeContext, error) {
	return app.newPlaceContextWith(ctx, explicit, app.readState())
}

// newPlaceContextWith is newPlaceContext over a state.json read the caller
// shares with the account rows.
func (app *App) newPlaceContextWith(ctx context.Context, explicit *explicitUserLevel, loaded loadedState) (*placeContext, error) {
	cwd, err := cwdAbs()
	if err != nil {
		return nil, fmt.Errorf("resolve the current directory: %w", err)
	}
	pc := &placeContext{cwd: cwd, home: app.Env.Home, explicit: explicit}
	pc.physicalCwd = sync.OnceValues(func() (string, error) { return physicalWd() })
	pc.repoRoot = gitRepositoryRoot(ctx, cwd)
	pc.bindings = app.bindingsAt(cwd)
	if len(pc.bindings) > 0 {
		pc.binding = pc.bindings[0]
	}
	if loaded.err != nil {
		pc.stateErr = loaded.err
	} else {
		pc.synced = loaded.st.Synced
	}
	return pc, nil
}

// ancestorSpellings is dir and every ancestor up to the filesystem root, nearest
// first, walked through the **physical** parents: a cwd reached through a symlink
// (`~/link` → `~/w/repo/sub`) has the repository, its levels and its bindings
// above the link's target, not above the link. Each entry keeps dir's own
// (logical) spelling for as long as the logical parent names the same directory,
// so the common case — no symlink, or one above everything — prints as the user
// typed it.
func ancestorSpellings(dir string) []string {
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		physical = dir
	}
	var out []string
	logical, keepLogical := dir, true
	for p := physical; ; {
		if keepLogical && !samePath(logical, p) {
			keepLogical = false
		}
		if keepLogical {
			out = append(out, logical)
		} else {
			out = append(out, p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return out
		}
		p = parent
		if keepLogical {
			if lp := filepath.Dir(logical); lp != logical {
				logical = lp
			} else {
				keepLogical = false
			}
		}
	}
}

// gitRepositoryRoot asks git for the repository containing cwd and returns its
// root in cwd's own spelling, or "" outside a repository (or without git). It
// never assumes a `.git` layout: the root and cwd's repository-relative prefix
// both come from `git rev-parse`, and the answer is used only when it names an
// existing directory.
//
// The root is derived from cwd and the prefix so that a cwd reached through a
// symlink keeps its spelling (git prints the resolved toplevel). When the two do
// not name the same directory — cwd reached through a symlink inside the
// repository — git's own toplevel is used instead.
func gitRepositoryRoot(ctx context.Context, cwd string) string {
	toplevel, prefixLine, _, _, ok := gitRevParsePair(ctx, "--show-toplevel", "--show-prefix")
	if !ok {
		return ""
	}
	prefix := strings.TrimSuffix(prefixLine, "/")
	if !filepath.IsAbs(toplevel) || !dirExists(toplevel) {
		return ""
	}
	derived := cwd
	if prefix != "" {
		for range strings.Split(prefix, "/") {
			derived = filepath.Dir(derived)
		}
	}
	if samePath(derived, toplevel) {
		return derived
	}
	return toplevel
}

// samePath reports whether a and b name one file or directory, lexically or by
// inode.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}

// bindingsAt walks from dir up to the filesystem root and returns every
// directory with a kae fragment, nearest first. An unreadable fragment is warned
// about and passed over — the directory's binding cannot be read, so it cannot
// decide — and the warning never changes the exit code.
//
// kae's global mise fragment is not a binding even where its path is some
// directory's fragment path: with the default XDG layout it sits at
// ~/.config/mise/conf.d/kagikae.toml, exactly where HOME's would be.
func (app *App) bindingsAt(dir string) []*governingBinding {
	global := app.Paths.MiseGlobalFragmentFile()
	var out []*governingBinding
	for _, d := range ancestorSpellings(dir) {
		if samePath(filepath.Join(d, fragmentRelPath), global) {
			continue
		}
		info, exists, err := readFragmentAt(d)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "kae: warning: %s is bound but its fragment could not be read (%v), so its binding is not applied here\n", d, err)
		case exists:
			out = append(out, app.bindingFor(d, info))
		}
	}
	return out
}

// governingBindingAt is the nearest ancestor bound directory of dir, of any
// tool: the pin group's current place.
func (app *App) governingBindingAt(dir string) *governingBinding {
	if bindings := app.bindingsAt(dir); len(bindings) > 0 {
		return bindings[0]
	}
	return nil
}

// governingBindingNow is governingBindingAt the current directory. A cwd that
// cannot be resolved only costs the governing marker, so it is not an error for
// the pin listing.
func (app *App) governingBindingNow() *governingBinding {
	cwd, _ := cwdAbs()
	return app.governingBindingAt(cwd)
}

// toolBinding is tool's governing binding: the nearest ancestor bound directory
// whose fragment binds tool. mise merges nested fragments per variable, so an
// inner directory binding only codex leaves an outer directory's claude binding
// in effect (measured 2026-09-30, mise 2026.9.17).
func (pc *placeContext) toolBinding(tool string) *governingBinding {
	for _, b := range pc.bindings {
		if b.info.Accounts[tool] != "" {
			return b
		}
	}
	return nil
}

// bindingFor names the binding at d by the directory its breadcrumb records when
// one names d under another spelling (`/tmp/x` vs `/private/tmp/x`, a symlink):
// the store is filed under the pin-id of the recorded path, and a pin-id derived
// from this spelling would name a store that does not exist.
func (app *App) bindingFor(d string, info fragmentInfo) *governingBinding {
	if _, err := os.Stat(app.Paths.PinRecordFile(paths.PinID(d))); err == nil {
		return &governingBinding{dir: d, pinID: paths.PinID(d), info: info}
	}
	if pins, _, err := app.pinnedDirsComplete(); err == nil {
		for _, pin := range pins {
			if samePath(pin.Dir, d) {
				return &governingBinding{dir: pin.Dir, pinID: pin.PinID, info: info}
			}
		}
	}
	return &governingBinding{dir: d, pinID: paths.PinID(d), info: info}
}

// ancestorChain is cwd and its ancestors, nearest first (ancestorSpellings),
// stopping before HOME (HOME's own `.claude/` and `.codex/` are the real homes).
// repoUpTo is the last chain index inside the repository: the root's index, the
// whole chain when the root is HOME or above it, or -1 outside a repository.
// localAt is where claude reads settings.local.json from: the root's index when
// the root is in the chain, cwd (0) when the root is HOME or cwd is outside a
// repository, and -1 when the root lies above HOME.
func (pc *placeContext) ancestorChain() (chain []string, repoUpTo, localAt int) {
	all := ancestorSpellings(pc.cwd)
	homeAt, rootAt := -1, -1
	for i, d := range all {
		if homeAt < 0 && pc.home != "" && samePath(d, pc.home) {
			homeAt = i
		}
		if rootAt < 0 && pc.repoRoot != "" && samePath(d, pc.repoRoot) {
			rootAt = i
		}
	}
	chain = all
	if homeAt >= 0 {
		chain = all[:homeAt]
	}
	switch {
	case pc.repoRoot == "" || rootAt < 0:
		return chain, -1, 0
	case homeAt < 0 || rootAt < homeAt:
		return chain, rootAt, rootAt
	case rootAt == homeAt:
		return chain, len(chain) - 1, 0
	default:
		return chain, len(chain) - 1, -1
	}
}

// projectLevels lists tool's effective ancestor project levels, nearest first
// (docs/CLI.md § kae ls Semantics owns the rules and their sources):
//   - codex reads `.codex/` from cwd up to the repository root, or cwd's alone
//     outside a repository;
//   - claude counts cwd's own `.claude/`, every `.claude/` from cwd up to the
//     repository root, and above the root or outside a repository a `.claude/`
//     holding CLAUDE.md or AGENTS.md. Each claude row says what applies there.
func (pc *placeContext) projectLevels(tool string) []placeRow {
	if rows, ok := pc.project[tool]; ok {
		return rows
	}
	rows := pc.findProjectLevels(tool)
	if pc.project == nil {
		pc.project = map[string][]placeRow{}
	}
	pc.project[tool] = rows
	return rows
}

func (pc *placeContext) findProjectLevels(tool string) []placeRow {
	name := projectLevelName(tool)
	if name == "" {
		return nil
	}
	chain, repoUpTo, localAt := pc.ancestorChain()
	rows := []placeRow{}
	for i, dir := range chain {
		level := filepath.Join(dir, name)
		if !dirExists(level) {
			continue
		}
		inRepo := i <= repoUpTo
		row := placeRow{Group: tool, Kind: constants.PlaceKindProject, Path: level, Root: dir, Exists: true, InEffect: true}
		switch tool {
		case constants.ToolCodex:
			if !inRepo && i > 0 {
				continue
			}
		case constants.ToolClaude:
			row.Applies = claudeApplies(level, i, repoUpTo, localAt)
			if i > 0 && !inRepo && !slices.Contains(row.Applies, constants.PlaceAppliesInstructions) {
				continue
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// claudeApplies is what claude reads from the `.claude/` at chain[i], by the
// documented rules (code.claude.com/docs/en/settings, /skills, /sub-agents and
// /memory, read 2026-09-30), limited to what is there:
//   - settings: settings.json, from cwd only (measured 2026-09-30);
//   - local-settings: settings.local.json, from localAt (ancestorChain): the
//     repository root when cwd is inside a repository whose root is not HOME,
//     else cwd (measured 2026-09-30 for the root case);
//   - skills-agents: skills/, agents/ or commands/, from cwd up to the repository
//     root, or cwd's alone outside a repository;
//   - instructions: CLAUDE.md or AGENTS.md, from cwd and every directory above.
//
// The documented ownership exception for settings.local.json (a root or entry not
// owned by the user) is not modelled.
func claudeApplies(level string, i, repoUpTo, localAt int) []string {
	exists := func(names ...string) bool {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(level, name)); err == nil {
				return true
			}
		}
		return false
	}
	var applies []string
	if i == 0 && exists("settings.json") {
		applies = append(applies, constants.PlaceAppliesSettings)
	}
	if i == localAt && exists("settings.local.json") {
		applies = append(applies, constants.PlaceAppliesLocalSettings)
	}
	if (i <= repoUpTo || i == 0) && exists("skills", "agents", "commands") {
		applies = append(applies, constants.PlaceAppliesSkillsAgents)
	}
	if exists("CLAUDE.md", "AGENTS.md") {
		applies = append(applies, constants.PlaceAppliesInstructions)
	}
	return applies
}

// belowLevels finds the project levels below cwd once for every place tool:
// through git in a repository (tracked and non-ignored files), else by a
// depth-limited walk.
func (pc *placeContext) belowLevels(ctx context.Context) map[string][]string {
	if pc.belowSet {
		return pc.below
	}
	pc.belowSet = true
	names := map[string]string{}
	for _, tool := range placeTools() {
		names[projectLevelName(tool)] = tool
	}
	found := map[string]map[string]bool{}
	record := func(tool, dir string) {
		if found[tool] == nil {
			found[tool] = map[string]bool{}
		}
		found[tool][dir] = true
	}
	if pc.repoRoot != "" {
		for _, rel := range gitListedFiles(ctx) {
			parts := strings.Split(rel, "/")
			// The last part is the file itself; a level at index 0 is cwd's own,
			// which projectLevels already covers.
			for i := 1; i < len(parts)-1; i++ {
				if tool, ok := names[parts[i]]; ok {
					record(tool, filepath.Join(pc.cwd, filepath.FromSlash(strings.Join(parts[:i+1], "/"))))
					break
				}
			}
		}
	} else {
		pc.walkBelow(names, record)
	}
	pc.below = map[string][]string{}
	for tool, set := range found {
		for dir := range set {
			if dirExists(dir) {
				pc.below[tool] = append(pc.below[tool], dir)
			}
		}
		sort.Strings(pc.below[tool])
	}
	return pc.below
}

// gitListedFiles is the repository's tracked and non-ignored files under cwd,
// relative to cwd. A failure is warned about and yields nothing: the listing is
// then missing its below rows, which a warning says, and no exit code changes.
func gitListedFiles(ctx context.Context) []string {
	// Pathspecs keep a large repository's listing to the files under a level.
	args := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}
	for _, tool := range placeTools() {
		args = append(args, ":(glob)**/"+projectLevelName(tool)+"/**")
	}
	out, stderr, code := runner.Run(ctx, "git", args...)
	if code != 0 {
		fmt.Fprintf(os.Stderr, "kae: warning: could not list the repository's files (%s), so project levels below this directory are not shown\n",
			strings.TrimSpace(runner.Snippet(stderr)))
		return nil
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// belowWalkDepth bounds the walk outside a repository: a level is found in a
// directory at most this many levels below cwd. belowWalkBudget bounds the
// directories visited, so a walk from a large tree (HOME) stays quick.
const (
	belowWalkDepth  = 3
	belowWalkBudget = 5000
)

// walkBelow is the outside-a-repository search. It follows no symlink, does not
// descend into hidden directories (a level is itself one, and is checked as a
// child), and never reports the real home's own level.
func (pc *placeContext) walkBelow(names map[string]string, record func(tool, dir string)) {
	visited := 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if visited >= belowWalkBudget {
				return
			}
			if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
				continue
			}
			name := entry.Name()
			child := filepath.Join(dir, name)
			if tool, ok := names[name]; ok {
				if depth > 0 && !samePath(dir, pc.home) {
					record(tool, child)
				}
				continue
			}
			if strings.HasPrefix(name, ".") || depth >= belowWalkDepth {
				continue
			}
			visited++
			walk(child, depth+1)
		}
	}
	walk(pc.cwd, 0)
}

// userLevel resolves tool's effective user level: explicit arguments first, then
// the governing binding, then what applies globally (kae use -i's home, else the
// real home). Only that last step reads state.json, so only it fails on an
// unreadable one.
func (app *App) userLevel(pc *placeContext, tool string) (placeRow, error) {
	row := placeRow{Group: tool, Kind: constants.PlaceKindUser, InEffect: true}
	b := pc.toolBinding(tool)
	switch {
	case pc.explicit != nil && pc.explicit.tool == tool && pc.explicit.isolated:
		row.Path = app.Paths.GlobalIsolatedHomeDir(tool, pc.explicit.account)
		row.Source, row.Mode, row.Account = constants.PlaceSourceExplicit, constants.ModeSync, pc.explicit.account
	case pc.explicit != nil && pc.explicit.tool == tool:
		row.Path = app.realToolHome(tool)
		row.Source, row.Mode = constants.PlaceSourceExplicit, constants.ModeAuth
	case b != nil:
		if dir, ok := app.boundStoreDir(b.pinID, tool, b.info); ok {
			row.Path = dir
			row.Source, row.Mode, row.Account = constants.PlaceSourcePin, b.info.Mode, b.info.Accounts[tool]
			break
		}
		fallthrough
	default:
		if pc.stateErr != nil {
			return row, pc.stateErr
		}
		if account, ok := pc.synced[tool]; ok {
			row.Path = app.Paths.GlobalIsolatedHomeDir(tool, account)
			row.Source, row.Mode, row.Account = constants.PlaceSourceGlobal, constants.ModeSync, account
		} else {
			row.Path = app.realToolHome(tool)
			row.Source, row.Mode = constants.PlaceSourceGlobal, constants.ModeAuth
		}
	}
	row.Exists = dirExists(row.Path)
	return row, nil
}

// toolPlaces lists tool's places in list order: the effective user level, claude's
// session row, the effective ancestor project levels (nearest first), the project
// levels below cwd, and the real home when another user level is in effect.
func (app *App) toolPlaces(ctx context.Context, pc *placeContext, tool string) ([]placeRow, error) {
	rows, err := app.leadingToolPlaces(pc, tool)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	for _, level := range pc.belowLevels(ctx)[tool] {
		rows = append(rows, placeRow{Group: tool, Kind: constants.PlaceKindBelow, Path: level, Root: filepath.Dir(level), Exists: true})
	}
	if home := app.realToolHome(tool); home != "" && !samePath(home, rows[0].Path) {
		rows = append(rows, placeRow{Group: tool, Kind: constants.PlaceKindHome, Path: home, Exists: dirExists(home)})
	}
	return numberPlaces(rows), nil
}

// leadingToolPlaces is the start of toolPlaces' list: the effective user level,
// claude's session row and the ancestor project levels, numbered as toolPlaces
// numbers them.
func (app *App) leadingToolPlaces(pc *placeContext, tool string) ([]placeRow, error) {
	if projectLevelName(tool) == "" {
		return []placeRow{}, nil
	}
	user, err := app.userLevel(pc, tool)
	if err != nil {
		return nil, err
	}
	rows := []placeRow{user}
	if session, ok := app.sessionRow(pc, tool, user, true); ok {
		rows = append(rows, session)
	}
	return numberPlaces(append(rows, pc.projectLevels(tool)...)), nil
}

// physicalWd is the process's working directory by the kernel's own answer
// (syscall.Getwd), never a spelling: os.Getwd trusts $PWD and filepath.Abs the
// path as typed, and either keeps a symlink or a case the filesystem does not
// have, which would name a session directory claude never writes.
var physicalWd = syscall.Getwd

// sessionRow is claude's session row: `<user level>/projects/<name>`, the
// directory holding the transcripts of the current directory (docs/CLI.md § kae
// ls Semantics). It follows the user level, so an explicit -s or -i and a binding
// both move it. It is always listed and marked missing until claude has written
// there. When the current directory cannot be resolved (and no override applies)
// the row is left out; warn says whether that is worth a warning, once per
// command, which the caller decides by whether the group is being shown.
func (app *App) sessionRow(pc *placeContext, tool string, user placeRow, warn bool) (placeRow, bool) {
	if tool != constants.ToolClaude {
		return placeRow{}, false
	}
	name, err := claude.SessionDirName(app.Env.Getenv(claude.ProjectDirNameEnv), app.launchEnvHasConfigDir(user), pc.physicalCwd)
	if err != nil {
		if warn && !pc.cwdWarned {
			pc.cwdWarned = true
			fmt.Fprintf(os.Stderr, "kae: warning: the current directory could not be resolved (%v), so the claude session row is not listed\n", err)
		}
		return placeRow{}, false
	}
	row := user
	row.Kind = constants.PlaceKindSession
	row.Path = filepath.Join(user.Path, "projects", name)
	row.Exists = dirExists(row.Path)
	return row, true
}

// launchEnvHasConfigDir says whether the environment claude is launched in holds
// CLAUDE_CONFIG_DIR, the only case claude honours CLAUDE_CODE_PROJECT_DIR_NAME. A
// binding, a global `kae use -i` home and an explicit -i export it; the real home
// does only when the user's own variable named it (realToolHome), so the variable
// a binding exported does not count under an explicit -s.
func (app *App) launchEnvHasConfigDir(user placeRow) bool {
	if user.Mode != constants.ModeAuth {
		return true
	}
	_, ok := app.userToolHomeEnv(constants.ToolClaude)
	return ok
}

// hasSession says whether tool's session directory for the current directory
// exists, which makes its group relevant to bare `kae ls` and the picker.
func (app *App) hasSession(pc *placeContext, tool string) bool {
	if tool != constants.ToolClaude {
		return false
	}
	user, err := app.userLevel(pc, tool)
	if err != nil {
		return false
	}
	session, ok := app.sessionRow(pc, tool, user, false)
	return ok && session.Exists
}

// levelPlaces is every place of one level of tool, with `kae ls <tool>`'s
// numbers. The ancestor project levels come first in list order, so they are
// found without looking for the below levels.
func (app *App) levelPlaces(ctx context.Context, pc *placeContext, tool, level string) ([]placeRow, error) {
	var rows []placeRow
	var err error
	if level == constants.PlaceKindProject {
		rows, err = app.leadingToolPlaces(pc, tool)
	} else {
		rows, err = app.toolPlaces(ctx, pc, tool)
	}
	if err != nil {
		return nil, err
	}
	return app.placesOfLevel(tool, rows, level), nil
}

// placesOfLevel picks the rows of a tool's list that a level selector names:
// every effective ancestor project level, every level below, or the real home.
func (app *App) placesOfLevel(tool string, rows []placeRow, level string) []placeRow {
	var matches []placeRow
	for _, row := range rows {
		switch level {
		case constants.PlaceKindProject, constants.PlaceKindBelow:
			if row.Kind == level {
				matches = append(matches, row)
			}
		case constants.PlaceKindHome:
			if (row.Kind == constants.PlaceKindHome || row.Kind == constants.PlaceKindUser) && samePath(row.Path, app.realToolHome(tool)) {
				return []placeRow{row}
			}
		}
	}
	return matches
}

// toolRelevant says whether a governing binding (toolBinding) or an effective
// project level at cwd makes tool relevant to bare `kae ls`. It is not the whole
// condition: claude is also relevant while its session directory exists
// (hasSession), which collectPlaceGroups adds.
func (pc *placeContext) toolRelevant(tool string) bool {
	if pc.toolBinding(tool) != nil {
		return true
	}
	return len(pc.projectLevels(tool)) > 0
}

// repoPlaces is the repo group: the repository root, when cwd is in one.
func (pc *placeContext) repoPlaces() []placeRow {
	if pc.repoRoot == "" {
		return []placeRow{}
	}
	return numberPlaces([]placeRow{{
		Group: constants.PlaceGroupRepo, Kind: constants.PlaceKindRepositoryRoot,
		Path: pc.repoRoot, Exists: dirExists(pc.repoRoot), InEffect: true,
	}})
}

// kaePlaces is the kae group: kae's config, data and state directories. The
// credential store and file-backend secrets live under data and get no row.
func (app *App) kaePlaces() []placeRow {
	rows := []placeRow{}
	for _, p := range []struct{ kind, path string }{
		{constants.PlaceKindKaeConfig, app.Paths.ConfigDir},
		{constants.PlaceKindKaeData, app.Paths.DataDir},
		{constants.PlaceKindKaeState, app.Paths.StateDir},
	} {
		rows = append(rows, placeRow{Group: constants.PlaceGroupKae, Kind: p.kind, Path: p.path, Exists: dirExists(p.path), InEffect: true})
	}
	return numberPlaces(rows)
}

// pinPlaces turns the bound-directory listing into place rows, in its order.
func pinPlaces(dirs []boundDir) []placeRow {
	rows := []placeRow{}
	for _, dir := range dirs {
		rows = append(rows, placeRow{
			Group: constants.PlaceGroupPin, Kind: constants.PlaceKindBoundDirectory,
			Path: dir.Directory, Exists: true, InEffect: dir.Governing,
			Source: constants.PlaceSourcePin, Mode: dir.Mode,
		})
	}
	return numberPlaces(rows)
}

// numberPlaces assigns the `--at` numbers: per group, from 1, in list order.
func numberPlaces(rows []placeRow) []placeRow {
	for i := range rows {
		rows[i].Number = i + 1
	}
	return rows
}
