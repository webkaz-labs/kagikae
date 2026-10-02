package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/installation"
	"github.com/webkaz-labs/kagikae/internal/integration"
	"github.com/webkaz-labs/kagikae/internal/state"
)

type uninstallItem struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type uninstallReport struct {
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	DryRun        bool            `json:"dry_run"`
	Discovery     string          `json:"discovery"`
	Integrations  string          `json:"integrations"`
	Binary        string          `json:"binary"`
	Items         []uninstallItem `json:"items"`
	Retained      []string        `json:"retained"`
	Manual        []string        `json:"manual_actions"`
}

type uninstallOperation struct {
	item      uninstallItem
	file      integration.File
	remainder []byte
	directory string
	synced    map[string]string
}

type uninstallPlan struct {
	report     uninstallReport
	operations []uninstallOperation
	receipt    *installation.Receipt
	executable string
	dirs       []string
}

func registerUninstallFlags(fs *flag.FlagSet, dirs *[]string) {
	fs.Func("dir", "additional project directory to inspect (repeatable)", func(value string) error {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("directory must be a nonempty single-line path")
		}
		*dirs = append(*dirs, value)
		return nil
	})
}

func CmdUninstall(ctx context.Context, args []string) int {
	flags, pos := splitArgs(args, "--dir")
	var dirs []string
	opts, ok := parseCommon("uninstall", flags, true, func(fs *flag.FlagSet) {
		registerUninstallFlags(fs, &dirs)
	})
	if !ok || len(pos) != 0 {
		return usageError("usage: kae uninstall [--dry-run] [--yes] [--json] [--dir <path> ...]")
	}
	executable, err := os.Executable()
	if err != nil {
		return finish(opts, err)
	}
	return runUninstall(ctx, newApp(opts.ConfigPath), opts, dirs, executable)
}

func runUninstall(_ context.Context, app *App, opts commonOpts, dirs []string, executable string) int {
	plan := app.planUninstall(opts, dirs, executable)
	if opts.DryRun {
		return printUninstall(opts, plan.report, constants.ExitOK)
	}
	if !opts.Yes {
		if opts.Format == formatJSON || !stdinIsTTY() {
			return finish(opts, errf(constants.ExitUsage, "uninstall requires --yes outside an interactive terminal; inspect with --dry-run first"))
		}
		printUninstall(commonOpts{Format: formatText}, plan.report, constants.ExitOK)
		fmt.Fprint(os.Stderr, "Apply this exact removal plan? Type uninstall to confirm: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "uninstall" {
			return finish(opts, errf(constants.ExitUnsafeRefused, "uninstall was not confirmed"))
		}
	}
	exit := constants.ExitOK
	for i, operation := range plan.operations {
		if operation.item.Outcome == constants.UninstallUnresolved {
			exit = constants.ExitUnsafeRefused
			continue
		}
		if err := app.applyUninstall(operation); err != nil {
			plan.report.Items[i].Outcome = constants.UninstallFailed
			plan.report.Items[i].Reason = constants.UninstallWriteFailed
			if errors.Is(err, integration.ErrChanged) {
				plan.report.Items[i].Reason = constants.UninstallChanged
			}
			exit = exitOf(err)
			if exit == constants.ExitOK {
				exit = constants.ExitError
			}
		} else {
			plan.report.Items[i].Outcome = constants.UninstallRemoved
		}
	}
	// Discovery is repeated before binary removal: a new observable registration
	// was not in the consented plan and must not be removed by this invocation.
	remaining := app.planUninstall(opts, dirs, executable)
	plan.report.Discovery = remaining.report.Discovery
	binaryItem := plan.report.Items[len(plan.report.Items)-1]
	plan.report.Items = plan.report.Items[:len(plan.report.Items)-1]
	for _, op := range remaining.operations {
		alreadyReported := false
		for _, item := range plan.report.Items {
			if item.Path == op.item.Path && item.Outcome != constants.UninstallRemoved {
				alreadyReported = true
				break
			}
		}
		if !alreadyReported {
			item := op.item
			item.Reason = constants.UninstallChanged
			plan.report.Items = append(plan.report.Items, item)
		}
	}
	plan.report.Items = append(plan.report.Items, binaryItem)
	if len(remaining.operations) != 0 || remaining.report.Discovery != constants.UninstallBounded {
		if exit == constants.ExitOK {
			exit = constants.ExitUnsafeRefused
		}
		plan.report.Integrations = constants.UninstallIncomplete
	} else {
		plan.report.Integrations = constants.UninstallComplete
	}
	if exit == constants.ExitOK && plan.receipt != nil {
		removed, err := installation.Remove(app.Paths.InstallationsDir(), *plan.receipt)
		last := len(plan.report.Items) - 1
		if removed {
			plan.report.Binary = constants.UninstallRemoved
			plan.report.Items[last].Outcome = constants.UninstallRemoved
		}
		if err != nil {
			exit = exitOf(err)
			plan.report.Items[last].Reason = constants.UninstallWriteFailed
			if removed {
				plan.report.Manual = append(plan.report.Manual, "Executable removed; inspect retained installation metadata to finalize removal history.")
			}
		}
	}
	if plan.report.Binary != constants.UninstallRemoved && exit == constants.ExitOK {
		exit = constants.ExitUnsafeRefused
	}
	plan.report.OK = exit == constants.ExitOK
	return printUninstall(opts, plan.report, exit)
}

func printUninstall(opts commonOpts, report uninstallReport, exit int) int {
	if opts.Format == formatJSON {
		if code := encodeJSON(report); code != constants.ExitOK {
			return code
		}
		return exit
	}
	fmt.Printf("Uninstall: integrations %s; executable %s; discovery %s\n", report.Integrations, report.Binary, report.Discovery)
	for _, item := range report.Items {
		fmt.Printf("  %s %s: %s (%s)\n", item.Action, item.Path, item.Outcome, item.Reason)
	}
	fmt.Println("Retained data:")
	for _, path := range report.Retained {
		fmt.Printf("  %s\n", path)
	}
	for _, note := range report.Manual {
		fmt.Println(note)
	}
	return exit
}

func (app *App) planUninstall(opts commonOpts, dirs []string, executable string) uninstallPlan {
	p := uninstallPlan{executable: executable, report: uninstallReport{
		SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun,
		Discovery: constants.UninstallBounded, Integrations: constants.UninstallPending,
		Binary: constants.UninstallPending, Items: []uninstallItem{},
		Retained: []string{app.ConfigPath, app.Paths.DataDir, app.Paths.StateDir},
		Manual: []string{
			"Discovery covers known bound directories, supplied --dir paths and supported global completion locations; unregistered projects and custom shell code need manual inspection.",
			"Exit existing tool processes and open a new shell after cleanup; current-shell exports, functions and completion caches are not changed.",
			"Account snapshots, credentials, backups, preservation records, working stores, breadcrumbs and installation history are retained.",
		},
	}}
	if app.ConfigErr != nil {
		p.unresolved(constants.UninstallDiscovery, app.ConfigPath, constants.UninstallInvalidData)
	}
	index := app.boundDirectoryIndex()
	if !index.complete || index.err != nil {
		p.report.Discovery = constants.UninstallIncomplete
		p.unresolved(constants.UninstallDiscovery, app.Paths.IsolationDir(), constants.UninstallUnreadable)
	}
	for _, observation := range index.directories {
		dirs = append(dirs, observation.Dir)
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			p.unresolved(constants.UninstallDiscovery, dir, constants.UninstallUnreadable)
			continue
		}
		if !seen[abs] {
			seen[abs] = true
			p.dirs = append(p.dirs, abs)
		}
	}
	sort.Strings(p.dirs)
	for _, dir := range p.dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			p.unresolved(constants.UninstallDiscovery, dir, constants.UninstallUnreadable)
			continue
		}
		p.inspectFile(constants.UninstallDirectory, filepath.Join(dir, fragmentRelPath), dir, func(data string) ([]byte, bool) {
			return nil, app.ownedUninstallFragment(dir, data)
		})
		p.inspectFile(constants.UninstallProject, filepath.Join(dir, ".mise.toml"), dir, app.uninstallProjectRemainder)
	}
	for shell, candidates := range uninstallCompletionPaths(app) {
		script, _ := completionScript(shell)
		for _, path := range candidates {
			p.inspectFile(constants.UninstallCompletion, path, "", func(data string) ([]byte, bool) { return nil, data == script })
		}
	}
	app.inspectUninstallGlobal(&p)
	// The installation recipe is user-owned: it must be removed deliberately so a
	// later package-manager install cannot silently recreate the integrations.
	recipe := filepath.Join(filepath.Dir(app.Paths.MiseGlobalFragmentFile()), "kagikae-install.toml")
	if _, err := os.Lstat(recipe); err == nil || !os.IsNotExist(err) {
		p.unresolved(constants.UninstallRecipe, recipe, constants.UninstallCustom)
	}
	sort.Slice(p.operations, func(i, j int) bool { return p.operations[i].item.Path < p.operations[j].item.Path })
	for _, operation := range p.operations {
		p.report.Items = append(p.report.Items, operation.item)
	}
	binary := uninstallItem{
		Kind: constants.UninstallBinary, Path: executable,
		Action: constants.UninstallManual, Outcome: constants.UninstallPending, Reason: constants.UninstallNoReceipt,
	}
	if receipt, err := installation.InspectRemoval(app.Paths.InstallationsDir(), executable); err == nil {
		p.receipt = &receipt
		binary.Action, binary.Outcome, binary.Reason = constants.UninstallRemove, constants.UninstallPlanned, constants.UninstallReceipt
	} else {
		switch {
		case errors.Is(err, installation.ErrReceiptInvalid):
			binary.Reason = constants.UninstallInvalidReceipt
			p.report.Manual = append(p.report.Manual, "Retain and inspect the installation receipt at "+installation.ReceiptPath(app.Paths.InstallationsDir(), executable)+". An unsupported schema requires a compatible installer; repair invalid metadata explicitly before retrying. Reinstalling with this version also refuses an invalid receipt.")
		case errors.Is(err, installation.ErrReceiptIncomplete):
			binary.Reason = constants.UninstallPendingReceipt
			p.report.Manual = append(p.report.Manual, "The retained receipt records an incomplete or removed installation. Reinstall the same direct destination with the supported installer to establish a new active receipt, then preview again.")
		case errors.Is(err, installation.ErrImageMismatch):
			binary.Reason = constants.UninstallImageMismatch
			p.report.Manual = append(p.report.Manual, "The executable or its directory differs from the retained receipt. Inspect the current file, links and owner before choosing its installation manager; automatic removal is refused.")
		case os.IsNotExist(err):
			p.report.Manual = append(p.report.Manual, app.managedUninstallGuidance(executable, p.dirs)...)
		default:
			binary.Reason = constants.UninstallUnreadable
			p.report.Manual = append(p.report.Manual, "Installation ownership could not be inspected. Resolve access to the retained receipt and executable before retrying.")
		}
	}
	p.report.Items = append(p.report.Items, binary)
	return p
}

func (p *uninstallPlan) unresolved(kind, path, reason string) {
	p.operations = append(p.operations, uninstallOperation{item: uninstallItem{
		Kind: kind, Path: path, Action: constants.UninstallManual,
		Outcome: constants.UninstallUnresolved, Reason: reason,
	}})
}

func (p *uninstallPlan) inspectFile(kind, path, directory string, recognize func(string) ([]byte, bool)) {
	f, err := integration.Read(path)
	if err != nil {
		p.unresolved(kind, path, constants.UninstallUnreadable)
		return
	}
	if !f.Exists {
		return
	}
	remainder, owned := recognize(string(f.Content))
	if !owned {
		if kind == constants.UninstallLegacy && !strings.Contains(string(f.Content), miseBlockStart) && !strings.Contains(string(f.Content), "kae completion") {
			return
		}
		// A project file without any kae marker is outside our deletion set.
		if kind == constants.UninstallProject && !strings.Contains(string(f.Content), "kagikae") && !strings.Contains(string(f.Content), "kae ") {
			return
		}
		p.unresolved(kind, path, constants.UninstallCustom)
		return
	}
	action := constants.UninstallRemove
	if len(remainder) > 0 {
		action = constants.UninstallEdit
	}
	p.operations = append(p.operations, uninstallOperation{
		item: uninstallItem{Kind: kind, Path: path, Action: action, Outcome: constants.UninstallPlanned, Reason: constants.UninstallOwned},
		file: f, remainder: remainder, directory: directory,
	})
}

func (app *App) applyUninstall(op uninstallOperation) error {
	if op.item.Kind == constants.UninstallGlobal {
		return app.applyUninstallGlobal(op)
	}
	if op.directory != "" {
		l, err := app.acquirePinLock(op.directory)
		if err != nil {
			return err
		}
		defer l.Release()
		return op.file.Apply(op.remainder)
	}
	name := "completion"
	if op.item.Kind == constants.UninstallLegacy {
		name = lockNameState
	}
	l, err := app.acquireNamedLock(name, "another kae process is updating this integration; retry shortly")
	if err != nil {
		return err
	}
	defer l.Release()
	return op.file.Apply(op.remainder)
}

func (app *App) applyUninstallGlobal(op uninstallOperation) error {
	lifecycle, err := app.acquireIsolationLifecycleWriters(constants.Tools)
	if err != nil {
		return err
	}
	defer releaseLocks(lifecycle)
	tools, err := app.acquireLocks(constants.Tools)
	if err != nil {
		return err
	}
	defer releaseLocks(tools)
	prepare := func() error {
		if err := op.file.Recheck(); err != nil {
			return err
		}
		st, err := app.loadState()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(st.Synced, op.synced) {
			return integration.ErrChanged
		}
		journal := filepath.Join(app.Paths.StateDir, "mise-completion-migration.json")
		if _, err := os.Lstat(journal); !os.IsNotExist(err) {
			return integration.ErrChanged
		}
		return nil
	}
	_, err = app.mutateSyncedWithRegenerator(prepare, func(st *state.State) bool {
		st.Synced = map[string]string{}
		return true
	}, func(map[string]string) error { return op.file.Apply(nil) })
	return err
}
