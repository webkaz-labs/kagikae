package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// `kae open` and `kae cd` reach one place (docs/CLI.md § kae open and kae cd
// Semantics). They share `kae ls`'s targets, explicit resolution and selectors
// (parsePlaceArgs) and its place resolution (pickPlace); what they add is the
// current place as the default, what to do when a request names no single place,
// and the last step: the platform file manager, or the shell function's `cd`.

// cdPathCommand is the hidden entry the kae shell function calls for `kae cd`:
// it resolves exactly as `kae open` does and prints the path. Hidden like
// __complete: not in `kae help` and not in completionCommands.
const cdPathCommand = "__cd"

const (
	openUsage = "usage: kae open [pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--project|--below|--home] [--root] [--at N]"
	cdUsage   = "usage: kae cd [pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--project|--below|--home] [--root] [--at N]"
)

// CmdOpen is `kae open`: open one place in the platform file manager.
func CmdOpen(ctx context.Context, args []string) int {
	opts, req, code := parseNavigateArgs("open", openUsage, args)
	if code != constants.ExitOK {
		return code
	}
	return runOpen(ctx, newApp(opts.ConfigPath), opts, req)
}

func runOpen(ctx context.Context, app *App, opts commonOpts, req lsRequest) int {
	path, code := app.navigatePath(ctx, opts, req)
	if code != constants.ExitOK {
		return code
	}
	return app.openPlace(ctx, opts, path)
}

// CmdCdPath is the hidden `kae __cd`: the path `kae cd` moves to, on stdout.
func CmdCdPath(ctx context.Context, args []string) int {
	opts, req, code := parseNavigateArgs("cd", cdUsage, args)
	if code != constants.ExitOK {
		return code
	}
	return runCdPath(ctx, newApp(opts.ConfigPath), opts, req)
}

func runCdPath(ctx context.Context, app *App, opts commonOpts, req lsRequest) int {
	path, code := app.navigatePath(ctx, opts, req)
	if code != constants.ExitOK {
		return code
	}
	fmt.Println(path)
	return constants.ExitOK
}

// CmdCd is `kae cd` reaching the binary, which cannot move its parent shell:
// the kae shell function never passes cd here, so its absence is the only way
// in. It refuses and names the command that does the same without the function.
func CmdCd(args []string) int {
	return usageError("kae cd moves the shell only through the kae shell function, which "+
		"eval \"$(kae completion zsh)\" (bash likewise; fish: kae completion fish | source) or the mise hook defines; without it run: cd \"$(kae ls %s)\"",
		cdSuggestionWords(args))
}

// cdSuggestionWords echoes the words `kae cd` was given as `kae ls` arguments
// that print the same path: --current unless --at chose a number, and a
// placeholder when no target was typed. A valued flag keeps its value (the same
// arity splitArgs and the completion scripts use), so `--at 2` is not a target;
// --json and --format are dropped, since the suggestion prints a path.
func cdSuggestionWords(args []string) string {
	valued := valuedFlagCompletions("cd")
	var words []string
	hasTarget, chooses := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			hasTarget = true
			words = append(words, shellWord(arg))
			continue
		}
		flagName, _, attached := strings.Cut(arg, "=")
		name := strings.TrimLeft(flagName, "-")
		value := []string{}
		if !attached && slices.Contains(valued, arg) && i+1 < len(args) {
			i++
			value = append(value, args[i])
		}
		switch name {
		case "json", "format":
			continue
		case "at", "current":
			chooses = true
		}
		words = append(words, shellWord(arg))
		for _, v := range value {
			words = append(words, shellWord(v))
		}
	}
	if !hasTarget {
		words = append([]string{"<target>"}, words...)
	}
	if !chooses {
		words = append(words, "--current")
	}
	return strings.Join(words, " ")
}

// plainShellWord is a word a POSIX shell reads as itself.
var plainShellWord = regexp.MustCompile(`^[A-Za-z0-9._/=:@%+-]+$`)

func shellWord(s string) string {
	if plainShellWord.MatchString(s) {
		return s
	}
	return shellSingleQuote(s)
}

// parseNavigateArgs parses `kae open` and `kae cd` (and `kae __cd`, as cd). They
// print no report, so --json is refused rather than ignored: the path is `kae ls
// <target> --current --json`'s to publish.
func parseNavigateArgs(verb, usage string, args []string) (commonOpts, lsRequest, int) {
	flags, positionals := splitArgs(args, "--at")
	var f lsFlags
	opts, ok := parseCommon(verb, flags, false, func(fs *flag.FlagSet) {
		registerPlaceFlags(fs, &f)
	})
	if !ok {
		return opts, lsRequest{}, constants.ExitUsage
	}
	if opts.Format == formatJSON {
		return opts, lsRequest{}, usageError("kae %s prints no report; for a place's path as JSON use kae ls <target> --current --json", verb)
	}
	req, code := parsePlaceArgs(verb, usage, f, positionals)
	return opts, req, code
}

// navigatePath resolves an open or cd request to one existing directory. A
// request that names no single place — no target, a level selector with zero or
// several bound tools, a selector matching several places — is where the picker
// goes; until it exists, each is the picker's no-terminal case: the candidates
// and a usage error on stderr (reportCandidates).
func (app *App) navigatePath(ctx context.Context, opts commonOpts, req lsRequest) (string, int) {
	if req.target == "" && req.level == "" {
		return "", app.reportAllPlaces(ctx, opts, req)
	}
	var (
		row     placeRow
		several []placeRow
		code    int
	)
	if req.target == "" {
		row, several, code = app.pickLevelOfBoundTool(ctx, opts, &req)
	} else {
		row, several, code = app.pickPlace(ctx, opts, req)
	}
	if several != nil && code == constants.ExitNotFound {
		return "", app.reportNoCurrentPlace(opts, req, several)
	}
	if several != nil {
		return "", reportCandidates(opts, req, fmt.Sprintf("kae %s %s matches %d places", req.verb, requestWords(req, false), len(several)), several)
	}
	if code != constants.ExitOK {
		return "", code
	}
	path, code := placePath(row, req)
	if code != constants.ExitOK {
		return "", code
	}
	if !dirExists(path) {
		return "", finish(opts, errf(constants.ExitNotFound, "%s does not exist (kae ls marks it (missing))", path))
	}
	return path, constants.ExitOK
}

// reportNoCurrentPlace is a target with no current place here: the picker's case
// over the target's places, which with --root are the ones that have a root.
// Only a target with no candidate at all is not_found (reportCandidates).
func (app *App) reportNoCurrentPlace(opts commonOpts, req lsRequest, rows []placeRow) int {
	var candidates []placeRow
	for _, row := range rows {
		if !req.root || row.Root != "" {
			candidates = append(candidates, row)
		}
	}
	return reportCandidates(opts, req, fmt.Sprintf("kae %s %s has no current place here", req.verb, requestWords(req, false)), candidates)
}

// pickLevelOfBoundTool is a level selector without a tool: it applies to the one
// place tool the current directory's bindings bind, and req.target becomes that
// tool. None or several is the picker's case over the tools.
func (app *App) pickLevelOfBoundTool(ctx context.Context, opts commonOpts, req *lsRequest) (placeRow, []placeRow, int) {
	pc, err := app.newPlaceContext(ctx, nil)
	if err != nil {
		return placeRow{}, nil, finish(opts, err)
	}
	var bound []string
	for _, tool := range placeTools() {
		if pc.toolBinding(tool) != nil {
			bound = append(bound, tool)
		}
	}
	if len(bound) != 1 {
		candidates := bound
		reason := fmt.Sprintf("kae %s --%s needs a tool: %d tools are bound here", req.verb, req.level, len(bound))
		if len(bound) == 0 {
			candidates = placeTools()
			reason = fmt.Sprintf("kae %s --%s needs a tool: no tool is bound here", req.verb, req.level)
		}
		lines := make([]string, 0, len(candidates))
		for _, tool := range candidates {
			line := fmt.Sprintf("kae %s %s --%s", req.verb, tool, req.level)
			if req.root {
				line += " --root"
			}
			lines = append(lines, line)
		}
		return placeRow{}, nil, reportChoices(reason+"; choose one", lines)
	}
	req.target = bound[0]
	rows, err := app.toolPlaces(ctx, pc, req.target)
	if err != nil {
		return placeRow{}, nil, finish(opts, err)
	}
	return selectPlace(app, opts, *req, rows)
}

// reportAllPlaces is open or cd with no target: every place bare `kae ls`
// lists is a candidate.
func (app *App) reportAllPlaces(ctx context.Context, opts commonOpts, req lsRequest) int {
	warned := map[string]bool{}
	g := app.collectPlaceGroups(ctx, app.readState(), func(group string, err error) {
		if !warned[err.Error()] {
			warned[err.Error()] = true
			fmt.Fprintf(os.Stderr, "kae: warning: the %s group is not listed: %v\n", group, err)
		}
	})
	return reportCandidates(opts, req, fmt.Sprintf("kae %s needs a target", req.verb), g.places())
}

// reportCandidates is the picker's no-terminal case over place rows: a usage
// error and, one per line, the command that reaches each candidate. A place
// whose directory does not exist is left out, since choosing it exits 7; the
// numbers stay `kae ls`'s. With no candidate left it is not_found.
func reportCandidates(opts commonOpts, req lsRequest, reason string, rows []placeRow) int {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		if !row.Exists {
			continue
		}
		words := row.Group
		if req.target == row.Group {
			words = requestWords(req, true)
		}
		cmd := fmt.Sprintf("kae %s %s --at %d", req.verb, words, row.Number)
		path := row.Path
		if req.root {
			cmd += " --root"
			if row.Root != "" {
				path = row.Root // what the command reaches
			}
		}
		lines = append(lines, cmd+"  "+path)
	}
	if len(lines) == 0 {
		return finish(opts, errf(constants.ExitNotFound, "%s, and no existing place to choose", reason))
	}
	return reportChoices(reason+"; choose one", lines)
}

// reportChoices prints a usage error and its candidates on stderr. It is the
// interim for the picker (ROADMAP § Place navigation and the tree-shared mode),
// which replaces it on a terminal.
func reportChoices(reason string, lines []string) int {
	var b strings.Builder
	b.WriteString(reason + ":")
	for _, line := range lines {
		b.WriteString("\n  " + line)
	}
	return usageError("%s", b.String())
}

// requestWords is the target as typed, with its explicit resolution, and —
// unless targetOnly — its level selector.
func requestWords(req lsRequest, targetOnly bool) string {
	words := []string{req.target}
	if req.explicit != nil {
		if req.explicit.isolated {
			words = []string{"-i", req.target, req.explicit.account}
		} else {
			words = []string{"-s", req.target}
		}
	}
	if !targetOnly && req.level != "" {
		words = append(words, "--"+req.level)
	}
	return strings.Join(words, " ")
}

// openers is the file manager command per platform (docs/CLI.md § kae open and
// kae cd Semantics).
var openers = map[string]string{"darwin": "open", "linux": "xdg-open"}

// openPlace opens path with the platform's opener through the runner seam.
// Without one, the path goes to stdout and a warning to stderr, and the exit
// code stays 0: the place was resolved, and the path is what a caller needs.
func (app *App) openPlace(ctx context.Context, opts commonOpts, path string) int {
	opener := openers[app.Env.GOOS]
	found := opener != ""
	if found && app.Env.LookPath != nil {
		_, err := app.Env.LookPath(opener)
		found = err == nil
	}
	if !found {
		what := opener
		if what == "" {
			what = "file manager opener on " + app.Env.GOOS
		}
		fmt.Fprintf(os.Stderr, "kae: warning: no %s found, so the place is printed instead\n", what)
		fmt.Println(path)
		return constants.ExitOK
	}
	// Launched, not run: xdg-open can leave the file manager holding a captured
	// pipe, and kae would wait for the file manager to exit. The opener's own
	// error output reaches stderr directly.
	code, err := runner.Launch(ctx, opener, path)
	switch {
	case err != nil:
		return finish(opts, errf(constants.ExitError, "%s %s: %v", opener, path, err))
	case code != 0:
		return finish(opts, errf(constants.ExitError, "%s %s exited %d", opener, path, code))
	}
	return constants.ExitOK
}
