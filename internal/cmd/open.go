package cmd

import (
	"context"
	"flag"
	"fmt"
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
	return "kae " + verb + " [pin|repo|kae|<tool> | -s <tool> | -i <tool> <account>] [--project|--below|--home] [--root] [--at N | --pick]"
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
		"eval \"$(kae completion zsh)\" (bash likewise; fish: kae completion fish | source) or the mise hook defines; without it, run: cd \"$(kae ls %s)\"",
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
		case "json", "format", "pick": // no `kae ls` spelling picks
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
		registerNavigateFlags(fs, &f)
	})
	if !ok {
		return opts, lsRequest{}, constants.ExitUsage
	}
	if opts.Format == formatJSON {
		return opts, lsRequest{}, usageError("kae %s prints no report; for a place's path as JSON, run: kae ls <target> --current --json", verb)
	}
	req, code := parsePlaceArgs(verb, f, positionals)
	return opts, req, code
}

// navigatePath resolves an open or cd request to one existing directory. A
// request that names no single place — no target, a level selector with zero or
// several bound tools, a selection matching several places or none — has a
// candidate set (resolveNavigation) that offerCandidates puts to the user.
func (app *App) navigatePath(ctx context.Context, opts commonOpts, req lsRequest) (string, int) {
	path, set, code := app.resolveNavigation(ctx, opts, req)
	if code != constants.ExitOK {
		return "", code
	}
	if set != nil {
		return app.offerCandidates(ctx, opts, req, *set)
	}
	return path, constants.ExitOK
}

// resolveNavigation is what an open or cd request comes to: one existing
// directory, or the candidate set of a request that names no single place.
// A failure has been reported and its exit code is returned.
func (app *App) resolveNavigation(ctx context.Context, opts commonOpts, req lsRequest) (string, *placeCandidates, int) {
	if req.pick {
		set, code := app.pickCandidates(ctx, opts, req)
		return "", set, code
	}
	if req.target == "" && req.level == "" {
		set := app.allPlaceCandidates(ctx, req)
		return "", &set, constants.ExitOK
	}
	var (
		choice placeChoice
		code   int
	)
	if req.target == "" {
		var set *placeCandidates
		if choice, set, code = app.pickLevelOfBoundTool(ctx, opts, &req); set != nil {
			return "", set, constants.ExitOK
		}
	} else {
		choice, code = app.pickPlace(ctx, opts, req)
	}
	if code != constants.ExitOK {
		return "", nil, code
	}
	switch choice.kind {
	case choiceSeveral:
		set := newPlaceCandidates(req, func(n int) message {
			if n == 1 {
				return msgf("kae %s %s matches %d place", req.verb, requestWords(req, false), n)
			}
			return msgf("kae %s %s matches %d places", req.verb, requestWords(req, false), n)
		}, candidateGroup{Heading: req.target, Rows: choice.candidates})
		return "", &set, constants.ExitOK
	case choiceNone:
		set, code := app.noCurrentPlaceCandidates(opts, req, choice)
		return "", set, code
	}
	path, code := placePath(choice.row, req)
	if code != constants.ExitOK {
		return "", nil, code
	}
	if !dirExists(path) {
		return "", nil, finish(opts, errf(constants.ExitNotFound, "%s does not exist (kae ls marks it (missing))", path))
	}
	return path, nil, constants.ExitOK
}

// noCurrentPlaceCandidates is a target with no current place here: its places
// are the candidates (pin's listed here, see currentPinPlace). A target with no
// place at all is not_found.
func (app *App) noCurrentPlaceCandidates(opts commonOpts, req lsRequest, choice placeChoice) (*placeCandidates, int) {
	rows := choice.candidates
	if req.target == constants.PlaceGroupPin {
		var err error
		if rows, err = app.pinGroupPlaces(nil); err != nil {
			return nil, finish(opts, err)
		}
	}
	if len(rows) == 0 {
		return nil, finish(opts, choice.noneError(req.verb, req))
	}
	reason := msgf("kae %s %s has no current place here", req.verb, requestWords(req, false))
	set := newPlaceCandidates(req, constReason(reason), candidateGroup{Heading: req.target, Rows: rows})
	return &set, constants.ExitOK
}

// boundPlaceTools is the place tools the current directory's bindings bind.
func boundPlaceTools(pc *placeContext) []string {
	var bound []string
	for _, tool := range placeTools() {
		if pc.toolBinding(tool) != nil {
			bound = append(bound, tool)
		}
	}
	return bound
}

// levelGroups is a heading per tool with that level's places beneath.
func (app *App) levelGroups(ctx context.Context, pc *placeContext, tools []string, level string) ([]candidateGroup, error) {
	var groups []candidateGroup
	for _, tool := range tools {
		rows, err := app.levelPlaces(ctx, pc, tool, level)
		if err != nil {
			return nil, err
		}
		groups = append(groups, candidateGroup{Heading: tool, Rows: rows})
	}
	return groups, nil
}

// pickLevelOfBoundTool is a level selector without a tool: it applies to the one
// place tool the current directory's bindings bind, and req.target becomes that
// tool. None or several gives the candidate set of that level's places under a
// heading per tool: the bound tools, or both place tools when none is bound.
func (app *App) pickLevelOfBoundTool(ctx context.Context, opts commonOpts, req *lsRequest) (placeChoice, *placeCandidates, int) {
	pc, err := app.newPlaceContext(ctx, nil)
	if err != nil {
		return placeChoice{}, nil, finish(opts, err)
	}
	bound := boundPlaceTools(pc)
	if len(bound) == 1 {
		req.target = bound[0]
		choice, code := app.selectToolPlace(ctx, opts, pc, *req)
		return choice, nil, code
	}
	tools := bound
	reason := msgf("kae %s --%s needs a tool: %d tools are bound here", req.verb, req.level, len(bound))
	if len(bound) == 0 {
		tools = placeTools()
		reason = msgf("kae %s --%s needs a tool: no tool is bound here", req.verb, req.level)
	}
	groups, err := app.levelGroups(ctx, pc, tools, req.level)
	if err != nil {
		return placeChoice{}, nil, finish(opts, err)
	}
	set := newPlaceCandidates(*req, constReason(reason), groups...)
	return placeChoice{}, &set, constants.ExitOK
}

// requestWords is the target as typed, with its explicit resolution, and —
// unless targetOnly — its level selector.
func requestWords(req lsRequest, targetOnly bool) string {
	var words []string
	if req.target != "" {
		words = append(words, req.target)
	}
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
		if opener == "" {
			warnf("no file manager opener on %s found, so the place is printed instead", app.Env.GOOS)
		} else {
			warnf("no %s found, so the place is printed instead", opener)
		}
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
