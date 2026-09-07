package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/preservation"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

var (
	errPreservationMappingChanged    = errors.New("preservation mapping changed")
	errPreservationCredentialChanged = errors.New("preservation credential changed")
)

type preservationObservation struct {
	origin preservation.Origin
	spec   artifact.Spec
	live   artifact.Value
}

func readPreservationObservation(ctx context.Context, origin preservation.Origin, spec artifact.Spec) (preservationObservation, error) {
	live, err := artifact.ReadLive(ctx, spec)
	return preservationObservation{origin: origin, spec: spec, live: live}, err
}

// Recheck the resolved mapping and raw observation without inferring ownership or
// freshness. Callers keep this immediately before their irreversible operation.
func (app *App) recheckPreservationObservation(ctx context.Context, observed preservationObservation) error {
	current, _, err := app.preservationOrigin(ctx, observed.origin.Directory, observed.origin.Tool)
	if err != nil || current != observed.origin {
		return errPreservationMappingChanged
	}
	latest, err := artifact.ReadLive(ctx, observed.spec)
	if err != nil || latest.Present != observed.live.Present || !bytes.Equal(latest.Data, observed.live.Data) {
		return errPreservationCredentialChanged
	}
	return nil
}

// CmdPreservation manages independently retained credential copies. Account
// labels describe bindings, never an attribution of the preserved payload.
func CmdPreservation(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return usageError("usage: %s preservation <list|restore|rm> [<id>]", toolName)
	}
	action := args[0]
	if action != "list" && action != "restore" && action != "rm" {
		return usageError("unknown preservation action %q", action)
	}
	flags, pos := splitArgs(args[1:])
	opts, ok := parseCommon("preservation "+action, flags, action != "list", nil)
	if !ok {
		return constants.ExitUsage
	}
	if (action == "list" && len(pos) != 0) || (action != "list" && len(pos) != 1) {
		return usageError("usage: %s preservation %s %s", toolName, action, "[<id>]")
	}
	id := ""
	if len(pos) > 0 {
		id = pos[0]
	}
	return runPreservation(ctx, newApp(opts.ConfigPath), opts, action, id)
}

type preservationItem struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Tool         string `json:"tool"`
	Directory    string `json:"directory"`
	BoundAccount string `json:"bound_account"`
	State        string `json:"state"`
	Identity     string `json:"identity"`
	SizeBytes    int64  `json:"size_bytes"`
}

type preservationReport struct {
	SchemaVersion  int    `json:"schema_version"`
	OK             bool   `json:"ok"`
	DryRun         bool   `json:"dry_run"`
	PreservationID string `json:"preservation_id,omitempty"`
}

type preservationListReport struct {
	SchemaVersion int                `json:"schema_version"`
	Preservations []preservationItem `json:"preservations"`
}

func (app *App) preservationStore(be secret.Backend) preservation.Store {
	return preservation.Store{Dir: app.Paths.PreservationsDir(), LockDir: app.Paths.LocksDir(), Backend: be, LimitBytes: app.Config.Security.PreservationMaxBytes}
}

// preservationError deliberately does not print backend errors, which may carry
// credential material. Only classified, payload-independent diagnoses escape.
func preservationError(err error) error {
	var commandError *cmdError
	if errors.As(err, &commandError) {
		return commandError
	}
	switch {
	case errors.Is(err, preservation.ErrNotFound):
		return errf(constants.ExitNotFound, "preservation ID was not found")
	case errors.Is(err, preservation.ErrInvalidID):
		return errf(constants.ExitUsage, "invalid preservation ID")
	case errors.Is(err, lock.ErrBusy):
		return errf(constants.ExitLockBusy, "another preservation operation is running; retry shortly")
	case errors.Is(err, preservation.ErrQuota):
		return errf(constants.ExitUnsafeRefused, "preservation capacity is full; list preserved copies and explicitly remove an unwanted ID before retrying")
	case errors.Is(err, preservation.ErrProtected):
		return errf(constants.ExitUnsafeRefused, "preserving the current credential would remove the selected restore ID; explicitly remove an unwanted other ID before retrying")
	case errors.Is(err, preservation.ErrIncomplete):
		return errf(constants.ExitUnsafeRefused, "preservation inventory is incomplete; inspect the listed records before retrying")
	default:
		return errf(constants.ExitSecretStore, "preservation storage operation failed")
	}
}

func runPreservation(ctx context.Context, app *App, opts commonOpts, action, id string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	store := app.preservationStore(nil)
	if action == "list" {
		records, err := store.List(ctx)
		if err != nil {
			return finish(opts, preservationError(err))
		}
		listing := preservationListReport{SchemaVersion: constants.SchemaVersion, Preservations: []preservationItem{}}
		for _, r := range records {
			listing.Preservations = append(listing.Preservations, preservationItem{ID: r.ID, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), Tool: r.Origin.Tool, Directory: r.Origin.Directory, BoundAccount: r.Origin.BoundAccount, State: r.State, Identity: constants.PreservationIdentityUnknown, SizeBytes: r.Size})
		}
		if opts.Format == formatJSON {
			return encodeJSON(listing)
		}
		rows := [][]string{}
		for _, r := range listing.Preservations {
			rows = append(rows, []string{r.ID, r.Tool, app.displayPath(r.Directory), r.BoundAccount, r.State, fmt.Sprint(r.SizeBytes)})
		}
		printTable([]string{"ID", "Tool", "Directory", "Binding account (owner unknown)", "State", "Bytes"}, rows)
		return constants.ExitOK
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	store.Backend = be
	if !preservation.ValidID(id) {
		return finish(opts, preservationError(preservation.ErrInvalidID))
	}
	report := preservationReport{SchemaVersion: constants.SchemaVersion, OK: true, DryRun: opts.DryRun, PreservationID: id}
	if action == "rm" {
		// List rather than Load: incomplete records must also be removable explicitly.
		records, err := store.List(ctx)
		if err != nil {
			return finish(opts, preservationError(err))
		}
		found := false
		for _, r := range records {
			if r.ID == id {
				found = true
				break
			}
		}
		if !found {
			return finish(opts, errf(constants.ExitNotFound, "preservation ID was not found"))
		}
		if !opts.DryRun {
			if !opts.Yes && (opts.Format == formatJSON || !stdinIsTTY() || !confirmPreservationRemoval(id)) {
				return finish(opts, errf(constants.ExitUnsafeRefused, "this may be the only surviving credential copy; deletion requires explicit confirmation (use --yes with this ID to acknowledge)"))
			}
			if opts.Yes {
				fmt.Fprintln(os.Stderr, "kae: warning: this may be the only surviving credential copy; deleting the explicitly selected ID")
			}
			if err := store.Remove(ctx, id); err != nil {
				return finish(opts, preservationError(err))
			}
		}
	} else {
		record, _, err := store.Load(ctx, id)
		if err != nil {
			return finish(opts, preservationError(err))
		}
		pinLock, err := app.acquirePinLock(record.Origin.Directory)
		if err != nil {
			return finish(opts, err)
		}
		defer pinLock.Release()
		err = store.WithRestore(ctx, id, func(session *preservation.RestoreSession) error {
			current, sp, err := app.preservationOrigin(ctx, record.Origin.Directory, record.Origin.Tool)
			if err != nil || current != record.Origin || session.Record.Origin != record.Origin {
				return errf(constants.ExitUnsafeRefused, "the original binding or credential location changed or cannot be confirmed; no credential was restored")
			}
			observed, err := readPreservationObservation(ctx, current, sp)
			if err != nil {
				return errf(constants.ExitUnsafeRefused, "cannot read the destination credential; no credential was restored")
			}
			if opts.DryRun {
				if observed.live.Present {
					return session.CheckCurrent(observed.live.Data)
				}
				return nil
			}
			if observed.live.Present {
				if _, err := session.SaveCurrent(observed.live.Data); err != nil {
					return err
				}
			}
			// Pin/global preservation locks exclude kae writers in those scopes, not an
			// upstream refresh. Recheck observations immediately before the live write.
			if err := app.recheckPreservationObservation(ctx, observed); err != nil {
				if errors.Is(err, errPreservationMappingChanged) {
					return errf(constants.ExitUnsafeRefused, "the binding changed during restoration; no credential was restored")
				}
				return errf(constants.ExitUnsafeRefused, "the destination credential changed during restoration; no credential was restored")
			}
			if err := artifact.ApplyLive(ctx, sp, artifact.Value{Present: true, Data: session.Payload}); err != nil {
				return errf(constants.ExitSecretStore, "credential restoration failed; preserved copies remain available")
			}
			return nil
		})
		if err != nil {
			return finish(opts, preservationError(err))
		}
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if opts.DryRun {
		fmt.Printf("Would %s preservation %s\n", action, id)
	} else {
		fmt.Printf("Completed preservation %s for %s\n", action, id)
	}
	return constants.ExitOK
}

func confirmPreservationRemoval(id string) bool {
	fmt.Fprintf(os.Stderr, "Preserved copy %s may be the only surviving credential copy. Permanently delete it? Type its ID to confirm: ", id)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return err == nil && strings.TrimSpace(line) == id
}

// preservationOrigin confirms the relevant literal env mapping, rather than
// trusting metadata comments alone. Dynamic mise expressions are not evidence of
// a fixed store and cannot authorize a restore.
func (app *App) preservationOrigin(ctx context.Context, dir, tool string) (preservation.Origin, artifact.Spec, error) {
	fail := func() (preservation.Origin, artifact.Spec, error) {
		return preservation.Origin{}, artifact.Spec{}, errf(constants.ExitUnsafeRefused, "cannot confirm the original bound credential mapping")
	}
	if tool != constants.ToolClaude && tool != constants.ToolCodex {
		return fail()
	}
	data, err := os.ReadFile(filepath.Join(dir, fragmentRelPath))
	if err != nil {
		return fail()
	}
	fragment := parseDirFragment(string(data))
	configDir, bound := app.boundStoreDir(paths.PinID(dir), tool, fragment)
	if !bound || fragment.Accounts[tool] == "" || !dirExists(configDir) {
		return fail()
	}
	var document struct {
		Env map[string]any `toml:"env"`
	}
	if _, err := toml.Decode(string(data), &document); err != nil {
		return fail()
	}
	envPath := func(key string) (string, bool) {
		value, present := document.Env[key]
		if !present {
			return "", true
		}
		s, ok := value.(string)
		return s, ok && s != "" && !strings.Contains(s, "{{") && filepath.IsAbs(s)
	}
	actualConfig, ok := envPath(isolationEnvVar(tool))
	if !ok || actualConfig != configDir {
		return fail()
	}
	actualCred := ""
	if key := credentialEnvVar(tool); key != "" {
		actualCred, ok = envPath(key)
		if !ok || actualCred != fragment.CredDirs[tool] {
			return fail()
		}
	}
	dirs := bindDirs{Config: configDir, Cred: actualCred}
	specs, err := app.dirSpecs(ctx, tool, dirs)
	if err != nil {
		return fail()
	}
	sp, ok := specByName(specs, credentialArtifactName(tool))
	if !ok {
		return fail()
	}
	if sp.Kind == constants.KindFile || sp.Kind == constants.KindJSONPointer {
		resolved, err := preservationFileTarget(sp.Target)
		if err != nil {
			return fail()
		}
		sp.Target = resolved
	}
	origin := preservation.Origin{Directory: dir, Tool: tool, BoundAccount: fragment.Accounts[tool], Mode: fragment.Mode, ConfigDir: configDir, CredDir: actualCred, Locator: preservation.Locator{Name: sp.Name, Kind: sp.Kind, Target: sp.Target, Pointer: sp.Pointer, KeychainAccount: sp.KeychainAccount, KeychainMatchAccount: sp.KeychainMatchAccount, JSONC: sp.JSONC}}
	return origin, sp, nil
}

// An absent file still has a verifiable parent. A dangling symlink does not:
// treating it as an absent ordinary file would authorize its unknown target.
func preservationFileTarget(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return "", err
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
