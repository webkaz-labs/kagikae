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

func placeUsage(verb string) string {
	return "usage: kae " + verb + " [pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--project|--below|--home] [--root] [--at N]"
}

func CmdOpen(ctx context.Context, args []string) int {
	opts, req, code := parseNavigateArgs("open", args)
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

func CmdCdPath(ctx context.Context, args []string) int {
	opts, req, code := parseNavigateArgs("cd", args)
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
		flagWords := []string{shellWord(arg)}
		if !attached && slices.Contains(valued, arg) && i+1 < len(args) {
			i++
			flagWords = append(flagWords, shellWord(args[i]))
		}
		switch strings.TrimLeft(flagName, "-") {
		case "json", "format":
			continue
		case "at", "current":
			chooses = true
		}
		words = append(words, flagWords...)
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
func parseNavigateArgs(verb string, args []string) (commonOpts, lsRequest, int) {
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
	req, code := parsePlaceArgs(verb, f, positionals)
	return opts, req, code
}

// navigatePath resolves an open or cd request to one existing directory. A
// request that names no single place — no target, a level selector with zero or
// several bound tools, a selection matching several places or none — lists its
// candidates with a usage error (reportCandidates), the picker's no-terminal case.
func (app *App) navigatePath(ctx context.Context, opts commonOpts, req lsRequest) (string, int) {
	if req.target == "" && req.level == "" {
		return "", app.reportAllPlaces(ctx, opts, req)
	}
	var (
		choice placeChoice
		code   int
	)
	if req.target == "" {
		choice, code = app.pickLevelOfBoundTool(ctx, opts, &req)
	} else {
		choice, code = app.pickPlace(ctx, opts, req)
	}
	if code != constants.ExitOK {
		return "", code
	}
	switch choice.kind {
	case choiceSeveral:
		return "", reportCandidates(opts, req, func(n int) string {
			return fmt.Sprintf("kae %s %s matches %d places", req.verb, requestWords(req, false), n)
		}, choice.candidates)
	case choiceNone:
		return "", app.reportNoCurrentPlace(opts, req, choice)
	}
	path, code := placePath(choice.row, req)
	if code != constants.ExitOK {
		return "", code
	}
	if !dirExists(path) {
		return "", finish(opts, errf(constants.ExitNotFound, "%s does not exist (kae ls marks it (missing))", path))
	}
	return path, constants.ExitOK
}

// reportNoCurrentPlace is a target with no current place here: its places are
// the candidates (pin's listed here, see currentPinPlace), with --root only the
// ones that have a root. A target with no place at all is not_found.
func (app *App) reportNoCurrentPlace(opts commonOpts, req lsRequest, choice placeChoice) int {
	rows := choice.candidates
	if req.target == constants.PlaceGroupPin {
		var err error
		if rows, err = app.pinGroupPlaces(nil); err != nil {
			return finish(opts, err)
		}
	}
	if len(rows) == 0 {
		return finish(opts, choice.noneError(req.verb, req))
	}
	var candidates []placeRow
	for _, row := range rows {
		if !req.root || row.Root != "" {
			candidates = append(candidates, row)
		}
	}
	reason := fmt.Sprintf("kae %s %s has no current place here", req.verb, requestWords(req, false))
	return reportCandidates(opts, req, func(int) string { return reason }, candidates)
}

// pickLevelOfBoundTool is a level selector without a tool: it applies to the one
// place tool the current directory's bindings bind, and req.target becomes that
// tool. None or several lists the tools.
func (app *App) pickLevelOfBoundTool(ctx context.Context, opts commonOpts, req *lsRequest) (placeChoice, int) {
	pc, err := app.newPlaceContext(ctx, nil)
	if err != nil {
		return placeChoice{}, finish(opts, err)
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
		return placeChoice{}, reportChoices(reason+"; choose one", lines)
	}
	req.target = bound[0]
	return app.selectToolPlace(ctx, opts, pc, *req)
}

// reportAllPlaces is open or cd with no target: every place bare `kae ls`
// lists is a candidate.
func (app *App) reportAllPlaces(ctx context.Context, opts commonOpts, req lsRequest) int {
	g := app.collectPlaceGroups(ctx, app.readState(), warnGroupOnce())
	return reportCandidates(opts, req, func(int) string { return fmt.Sprintf("kae %s needs a target", req.verb) }, g.places())
}

// reportCandidates lists, one per line, the command that reaches each candidate,
// under a usage error whose reason is given the number listed. A place whose
// directory does not exist is left out, since choosing it exits 7, and the rest
// keep `kae ls`'s numbers. With none left it is not_found, and the reason is
// given the number of rows instead.
func reportCandidates(opts commonOpts, req lsRequest, reason func(listed int) string, rows []placeRow) int {
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
		return finish(opts, errf(constants.ExitNotFound, "%s, and no existing place to choose", reason(len(rows))))
	}
	return reportChoices(reason(len(lines))+"; choose one", lines)
}

// reportChoices prints a usage error and its candidates on stderr. On a
// terminal the picker takes its place (ROADMAP § Place navigation and the
// tree-shared mode).
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

// openPlace opens path with the platform's opener. Without one, the path goes
// to stdout and a warning to stderr, and the exit code stays 0: the place was
// resolved, and the path is what a caller needs.
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
	// pipe, and kae would wait for the file manager to exit.
	code, err := runner.Launch(ctx, opener, path)
	switch {
	case err != nil:
		return finish(opts, errf(constants.ExitError, "%s %s: %v", opener, path, err))
	case code != 0:
		return finish(opts, errf(constants.ExitError, "%s %s exited %d", opener, path, code))
	}
	return constants.ExitOK
}
