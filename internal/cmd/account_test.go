package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// captureClaude seeds and captures a claude account, leaving it active.
func captureClaude(t *testing.T, app *App, accountName, token string) {
	t.Helper()
	seedClaude(t, app, token, accountName+"-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, "claude", accountName)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// writeConfigFile writes config.toml to the app's config path and reloads it
// into app.Config so the in-memory and on-disk views agree.
func writeConfigFile(t *testing.T, app *App, content string) {
	t.Helper()
	writeFile(t, app.ConfigPath, content)
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatalf("load config fixture: %v", err)
	}
	// Keep the isolated file secret backend testApp set up; the fixture content
	// focuses on profiles, not the security section.
	cfg.Security.SecretBackend = secret.BackendFile
	app.Config = cfg
}

type accountOperationBackend struct {
	secret.Backend
	ops []string
}

func (b *accountOperationBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b.ops = append(b.ops, "get "+key)
	return b.Backend.Get(ctx, key)
}

func (b *accountOperationBackend) Set(ctx context.Context, key string, value []byte) error {
	b.ops = append(b.ops, "set "+key)
	return b.Backend.Set(ctx, key, value)
}

func (b *accountOperationBackend) Delete(ctx context.Context, key string) error {
	b.ops = append(b.ops, "delete "+key)
	return b.Backend.Delete(ctx, key)
}

func accountBackendFixture(t *testing.T, app *App, tool, accountName string) (account.Account, *secrettest.MemBackend, *accountOperationBackend) {
	t.Helper()
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil || !found {
		t.Fatalf("load account backend fixture: found=%v err=%v", found, err)
	}
	source := testBackend(t, app)
	mem := secrettest.NewMem()
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		if !art.Present {
			continue
		}
		payload, found, err := source.Get(context.Background(), art.SecretRef)
		if err != nil || !found {
			t.Fatalf("load fixture payload %s: found=%v err=%v", art.SecretRef, found, err)
		}
		mem.Values[art.SecretRef] = append([]byte(nil), payload...)
	}
	recorder := &accountOperationBackend{Backend: mem}
	app.backendForTest = recorder
	return acc, mem, recorder
}

func accountSecretValues(values map[string][]byte) map[string]string {
	got := make(map[string]string, len(values))
	for key, value := range values {
		got[key] = string(value)
	}
	return got
}

func assertAccountRefusalLeftFilesUnchanged(t *testing.T, app *App, accountDir, configBefore, stateBefore, accountBefore string) {
	t.Helper()
	if got := readFile(t, app.ConfigPath); got != configBefore {
		t.Fatalf("refusal changed config.toml:\n%s", got)
	}
	if got := readFile(t, app.Paths.StateFile()); got != stateBefore {
		t.Fatalf("refusal changed state.json:\n%s", got)
	}
	if got := readFile(t, filepath.Join(accountDir, "account.toml")); got != accountBefore {
		t.Fatalf("refusal changed account.toml:\n%s", got)
	}
}

func TestAccountRmRemovesSnapshotAndSecrets(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken) // side now active
	// Switch active to main so side is removable without --force.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	acc, found, _ := account.Load(app.Paths.AccountDir("claude", "side"))
	if !found {
		t.Fatal("side not captured")
	}
	be, _ := app.secretBackend()
	ref := acc.Artifacts[acc.ArtifactNames()[0]].SecretRef

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "side", false)
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if report.SecretsRemoved == 0 {
		t.Fatalf("expected secrets removed: %+v", report)
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "side")); !os.IsNotExist(err) {
		t.Fatalf("snapshot dir not removed: %v", err)
	}
	if _, ok, _ := be.Get(ctx, ref); ok {
		t.Fatal("secret item not deleted")
	}
}

func TestAccountRmRefusesActiveWithoutForce(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken) // main active

	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("expected exit %d, got %v", constants.ExitUnsafeRefused, err)
	}

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", true)
	if err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	if !report.ActiveCleared {
		t.Fatal("active not cleared with --force")
	}
	st, _ := app.loadState()
	if _, ok := st.Active["claude"]; ok {
		t.Fatalf("active claude not dropped from state: %+v", st.Active)
	}
}

func TestAccountRmUnknownExitsNotFound(t *testing.T) {
	app := testApp(t, nil)
	if _, err := buildAccountRm(context.Background(), app, commonOpts{Format: formatText}, "claude", "ghost", false); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
}

func TestAccountRmDropsProfileReference(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"side\"\ncodex = \"main\"\n")

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "side", false)
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if len(report.ProfilesUpdated) != 1 || report.ProfilesUpdated[0] != "alt" {
		t.Fatalf("profile not named in report: %+v", report.ProfilesUpdated)
	}
	cfg, _, _ := config.Load(app.ConfigPath)
	if _, ok := cfg.Profiles["alt"].Accounts["claude"]; ok {
		t.Fatalf("profile claude reference not dropped: %+v", cfg.Profiles["alt"])
	}
	if cfg.Profiles["alt"].Accounts["codex"] != "main" {
		t.Fatalf("sibling profile key lost: %+v", cfg.Profiles["alt"])
	}
}

func TestAccountRmDryRunWritesNothing(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if _, err := buildAccountRm(ctx, app, commonOpts{DryRun: true, Format: formatText}, "claude", "side", false); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "side")); err != nil {
		t.Fatalf("dry-run removed the snapshot dir: %v", err)
	}
}

func TestAccountMutationsRefuseInconsistentSecretRefsBeforeMutation(t *testing.T) {
	corruptions := []struct {
		name string
		ref  func(string) string
	}{
		{name: "foreign", ref: func(artifactName string) string {
			return account.SecretRef(constants.ToolClaude, "side", artifactName)
		}},
		{name: "malformed", ref: func(string) string { return "malformed" }},
	}
	operations := []struct {
		name          string
		run           func(context.Context, *App) error
		assertRefusal func(*testing.T, *App)
	}{
		{
			name: "remove",
			run: func(ctx context.Context, app *App) error {
				_, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main", true)
				return err
			},
		},
		{
			name: "rename",
			run: func(ctx context.Context, app *App) error {
				_, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main", "side")
				return err
			},
			assertRefusal: func(t *testing.T, app *App) {
				t.Helper()
				if _, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "side")); err != nil || found {
					t.Fatalf("refusal created the destination snapshot: found=%v err=%v", found, err)
				}
			},
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, corruption := range corruptions {
				t.Run(corruption.name, func(t *testing.T) {
					app := testApp(t, nil)
					captureClaude(t, app, "main", mainToken)
					writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n")
					acc, mem, recorder := accountBackendFixture(t, app, constants.ToolClaude, "main")

					// Corrupt the last artifact so the validator must inspect the whole set,
					// not just the credential-first entry.
					names := acc.ArtifactNames()
					artifactName := names[len(names)-1]
					art := acc.Artifacts[artifactName]
					art.SecretRef = corruption.ref(artifactName)
					acc.Artifacts[artifactName] = art
					mem.Values[art.SecretRef] = []byte("foreign-fixture-payload")
					accountDir := app.Paths.AccountDir(constants.ToolClaude, "main")
					if err := account.Save(accountDir, acc); err != nil {
						t.Fatal(err)
					}

					configBefore := readFile(t, app.ConfigPath)
					stateBefore := readFile(t, app.Paths.StateFile())
					accountBefore := readFile(t, filepath.Join(accountDir, "account.toml"))
					secretsBefore := accountSecretValues(mem.Values)

					err := operation.run(context.Background(), app)
					if exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "inconsistent secret_ref") {
						t.Fatalf("inconsistent ref was not refused: exit=%d err=%v", exitOf(err), err)
					}
					if len(recorder.ops) != 0 {
						t.Fatalf("refusal reached the credential backend: %v", recorder.ops)
					}
					if got := accountSecretValues(mem.Values); !reflect.DeepEqual(got, secretsBefore) {
						t.Fatalf("refusal changed secrets: got=%v want=%v", got, secretsBefore)
					}
					assertAccountRefusalLeftFilesUnchanged(t, app, accountDir, configBefore, stateBefore, accountBefore)
					if operation.assertRefusal != nil {
						operation.assertRefusal(t, app)
					}
				})
			}
		})
	}
}

func TestAccountRmAcceptsConsistentSecretRefs(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	acc, mem, recorder := accountBackendFixture(t, app, constants.ToolClaude, "main")

	if _, err := buildAccountRm(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", true); err != nil {
		t.Fatalf("remove with consistent refs: %v", err)
	}
	if len(recorder.ops) == 0 {
		t.Fatal("positive control never reached the credential backend")
	}
	for _, name := range acc.ArtifactNames() {
		if _, found := mem.Values[account.SecretRef(constants.ToolClaude, "main", name)]; found {
			t.Fatalf("positive control left secret %s", name)
		}
	}
}

func TestAccountSecretRefRefusalDoesNotLeakMetadata(t *testing.T) {
	const (
		artifactCanary  = "fixture-artifact-canary"
		secretRefCanary = "fixture-secret-ref-canary"
	)
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !found {
		t.Fatalf("load account: found=%v err=%v", found, err)
	}
	names := acc.ArtifactNames()
	art := acc.Artifacts[names[0]]
	delete(acc.Artifacts, names[0])
	art.SecretRef = secretRefCanary
	acc.Artifacts[artifactCanary] = art
	if err := account.Save(app.Paths.AccountDir(constants.ToolClaude, "main"), acc); err != nil {
		t.Fatal(err)
	}

	_, refusal := buildAccountRm(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", true)
	if exitOf(refusal) != constants.ExitUnsafeRefused {
		t.Fatalf("inconsistent ref was not refused: exit=%d err=%v", exitOf(refusal), refusal)
	}
	for _, format := range []string{formatText, formatJSON} {
		t.Run(format, func(t *testing.T) {
			var code int
			var output string
			if format == formatText {
				code, output = captureStderr(t, func() int { return finish(commonOpts{Format: format}, refusal) })
			} else {
				code, output = captureStdout(t, func() int { return finish(commonOpts{Format: format}, refusal) })
			}
			if code != constants.ExitUnsafeRefused {
				t.Fatalf("exit=%d output=%s", code, output)
			}
			if strings.Contains(output, artifactCanary) || strings.Contains(output, secretRefCanary) {
				t.Fatalf("refusal leaked untrusted account metadata: %s", output)
			}
		})
	}
}

func TestAccountMutationsRecheckSecretRefsAfterTakingLocks(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *App) error
	}{
		{
			name: "remove",
			run: func(ctx context.Context, app *App) error {
				_, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main", true)
				return err
			},
		},
		{
			name: "rename",
			run: func(ctx context.Context, app *App) error {
				_, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main", "side")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			captureClaude(t, app, "main", mainToken)
			writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n")
			_, mem, recorder := accountBackendFixture(t, app, constants.ToolClaude, "main")
			foreignRef := account.SecretRef(constants.ToolClaude, "side", "oauth_account")
			mem.Values[foreignRef] = []byte("foreign-fixture-payload")
			app.beforeAccountMutationLocksForTest = func() {
				acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
				if err != nil || !found {
					t.Fatalf("load racing account fixture: found=%v err=%v", found, err)
				}
				art := acc.Artifacts["oauth_account"]
				art.SecretRef = foreignRef
				acc.Artifacts["oauth_account"] = art
				if err := account.Save(app.Paths.AccountDir(constants.ToolClaude, "main"), acc); err != nil {
					t.Fatal(err)
				}
			}

			configBefore := readFile(t, app.ConfigPath)
			stateBefore := readFile(t, app.Paths.StateFile())
			secretsBefore := accountSecretValues(mem.Values)
			err := tc.run(context.Background(), app)
			if exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "inconsistent secret_ref") {
				t.Fatalf("racing inconsistent ref was not refused: exit=%d err=%v", exitOf(err), err)
			}
			if len(recorder.ops) != 0 {
				t.Fatalf("refusal reached the credential backend: %v", recorder.ops)
			}
			if got := accountSecretValues(mem.Values); !reflect.DeepEqual(got, secretsBefore) {
				t.Fatalf("refusal changed secrets: got=%v want=%v", got, secretsBefore)
			}
			if got := readFile(t, app.ConfigPath); got != configBefore {
				t.Fatalf("refusal changed config.toml:\n%s", got)
			}
			if got := readFile(t, app.Paths.StateFile()); got != stateBefore {
				t.Fatalf("refusal changed state.json:\n%s", got)
			}
			if tc.name == "rename" {
				if _, found, loadErr := account.Load(app.Paths.AccountDir(constants.ToolClaude, "side")); loadErr != nil || found {
					t.Fatalf("refusal created the destination snapshot: found=%v err=%v", found, loadErr)
				}
			}
		})
	}
}

func TestAccountRmRefusesGloballyIsolatedTargetEvenWithForce(t *testing.T) {
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		t.Run(tool, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			if tool == constants.ToolClaude {
				captureClaude(t, app, "main", mainToken)
			} else {
				seedCodex(t, app, "codex-main-token")
				code, out := captureStdout(t, func() int {
					return runCapture(ctx, app, commonOpts{Format: formatText}, tool, "main")
				})
				mustExit(t, constants.ExitOK, code, out)
			}
			st, err := app.loadState()
			if err != nil {
				t.Fatal(err)
			}
			st.Synced = map[string]string{tool: "main"}
			if err := state.Save(app.Paths.StateFile(), st); err != nil {
				t.Fatal(err)
			}
			if err := app.regenGlobalFragment(st.Synced); err != nil {
				t.Fatal(err)
			}

			for _, tc := range []struct {
				dryRun bool
				force  bool
			}{{false, false}, {false, true}, {true, true}} {
				_, err := buildAccountRm(ctx, app,
					commonOpts{Format: formatText, DryRun: tc.dryRun}, tool, "main", tc.force)
				if exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "--force does not bypass") {
					t.Fatalf("dry_run=%v force=%v: exit=%d err=%v", tc.dryRun, tc.force, exitOf(err), err)
				}
				if _, found, loadErr := account.Load(app.Paths.AccountDir(tool, "main")); loadErr != nil || !found {
					t.Fatalf("refusal removed snapshot: found=%v err=%v", found, loadErr)
				}
			}
		})
	}
}

func TestAccountRmIsolationRefusalUsesJSONContract(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Synced = map[string]string{"claude": "main"}
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	if err := app.regenGlobalFragment(st.Synced); err != nil {
		t.Fatal(err)
	}
	_, rmErr := buildAccountRm(ctx, app, commonOpts{Format: formatJSON}, "claude", "main", true)
	code, textOut := captureStderr(t, func() int { return finish(commonOpts{Format: formatText}, rmErr) })
	if code != constants.ExitUnsafeRefused || !strings.Contains(textOut, "--force does not bypass this isolation guard") {
		t.Fatalf("unexpected text refusal: exit=%d output=%q", code, textOut)
	}
	code, out := captureStdout(t, func() int { return finish(commonOpts{Format: formatJSON}, rmErr) })
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	var report errorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.ErrorCode != constants.CodeUnsafeRefused || !strings.Contains(report.Message, "kae use -s claude main") {
		t.Fatalf("unexpected JSON refusal: %+v", report)
	}
}

func TestAccountRmRefusesWhileRunIsolatedHoldsLifecycleLock(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	held, err := lock.AcquireShared(app.Paths.LocksDir(), isolationLifecycleLockName(constants.ToolClaude))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	_, err = buildAccountRm(context.Background(), app, commonOpts{Format: formatText}, "claude", "main", true)
	if exitOf(err) != constants.ExitLockBusy {
		t.Fatalf("force bypassed the run-i lifecycle lock: exit=%d err=%v", exitOf(err), err)
	}
}

func TestAccountRmRechecksActiveAfterTakingLocks(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	app.beforeAccountMutationLocksForTest = func() {
		st, err := app.loadState()
		if err != nil {
			t.Fatal(err)
		}
		st.Active["claude"] = "main"
		if err := state.Save(app.Paths.StateFile(), st); err != nil {
			t.Fatal(err)
		}
	}

	_, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false)
	if exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("locked active recheck did not refuse: exit=%d err=%v", exitOf(err), err)
	}
	if _, found, loadErr := account.Load(app.Paths.AccountDir("claude", "main")); loadErr != nil || !found {
		t.Fatalf("active refusal removed snapshot: found=%v err=%v", found, loadErr)
	}
}

func TestAccountRmStateBusyHappensBeforeConfigMutation(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	const configText = "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n"
	writeConfigFile(t, app, configText)
	held, err := lock.Acquire(app.Paths.LocksDir(), lockNameState)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	_, err = buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false)
	if exitOf(err) != constants.ExitLockBusy {
		t.Fatalf("state contention exit=%d err=%v", exitOf(err), err)
	}
	if got := readFile(t, app.ConfigPath); got != configText {
		t.Fatal("config changed before the busy state lock was reported")
	}
	if _, found, loadErr := account.Load(app.Paths.AccountDir("claude", "main")); loadErr != nil || !found {
		t.Fatalf("state contention removed snapshot: found=%v err=%v", found, loadErr)
	}
}

func TestAccountRmKeepsStateLockAcrossConfigEditAndSave(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n")
	sawBusy := false
	app.afterAccountRmConfigEditForTest = func() {
		other, err := lock.Acquire(app.Paths.LocksDir(), lockNameState)
		if other != nil {
			other.Release()
		}
		if !errors.Is(err, lock.ErrBusy) {
			t.Fatalf("state lock became available between config edit and state save: lock=%v err=%v", other, err)
		}
		sawBusy = true
	}

	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); err != nil {
		t.Fatal(err)
	}
	if !sawBusy {
		t.Fatal("post-config state-lock probe did not run")
	}
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Profiles["alt"].Accounts["claude"]; exists {
		t.Fatal("profile reference was not removed")
	}
}

func TestAccountRmDeleteFailureLeavesSnapshotForRetry(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	realBackend := testBackend(t, app)
	app.backendForTest = deleteFailBackend{Backend: realBackend, failFor: "/main/"}

	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); err == nil {
		t.Fatal("account rm succeeded despite injected secret deletion failure")
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "main")); err != nil || !found {
		t.Fatalf("retry metadata was removed before secret cleanup completed: found=%v err=%v", found, err)
	}
	app.backendForTest = realBackend
	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); err != nil {
		t.Fatalf("retry after backend recovery failed: %v", err)
	}
}

func TestAccountRmRefusesFragmentMismatchUntilUseSharedReconcilesIt(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaude(t, app, "main", mainToken)
	if code := runUseIsolated(ctx, app, opts, "claude", "main"); code != constants.ExitOK {
		t.Fatalf("use -i exit %d", code)
	}
	home := app.Paths.GlobalIsolatedHomeDir("claude", "main")
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	delete(st.Synced, "claude")
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	if _, err := buildAccountRm(ctx, app, opts, "claude", "main", true); exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("fragment mismatch was not refused: exit=%d err=%v", exitOf(err), err)
	}
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("use -s exit=%d output=%s", code, out)
	}
	if _, err := buildAccountRm(ctx, app, opts, "claude", "main", true); err != nil {
		t.Fatalf("rm remained blocked after reconciliation: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("account rm removed the retained isolated home: %v", err)
	}
}

func TestAccountRmPreservesOtherSyncedToolAndFragment(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Synced = map[string]string{"codex": "main"}
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	if err := app.regenGlobalFragment(st.Synced); err != nil {
		t.Fatal(err)
	}
	fragmentBefore := readFile(t, app.Paths.MiseGlobalFragmentFile())

	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); err != nil {
		t.Fatal(err)
	}
	after, err := app.loadState()
	if err != nil || after.Synced["codex"] != "main" {
		t.Fatalf("other synced tool changed: state=%+v err=%v", after, err)
	}
	if got := readFile(t, app.Paths.MiseGlobalFragmentFile()); got != fragmentBefore {
		t.Fatal("account rm rewrote another tool's global fragment")
	}
}

func TestAccountRmRecomputesProfilesUnderConfigLock(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	writeConfigFile(t, app, "version = 1\n[security]\nsecret_backend = \"file\"\n")
	app.beforeAccountMutationLocksForTest = func() {
		writeFile(t, app.ConfigPath, "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n")
	}

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ProfilesUpdated) != 1 || report.ProfilesUpdated[0] != "alt" {
		t.Fatalf("concurrent profile reference was not reported: %v", report.ProfilesUpdated)
	}
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Profiles["alt"].Accounts["claude"]; exists {
		t.Fatalf("concurrent profile reference survived: %+v", cfg.Profiles["alt"])
	}
}

func TestAccountRmRechecksSnapshotUnderToolLock(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	be := testBackend(t, app)
	extraRef := account.SecretRef("claude", "main", "extra")
	before, found, err := account.Load(app.Paths.AccountDir("claude", "main"))
	if err != nil || !found {
		t.Fatalf("load initial snapshot: found=%v err=%v", found, err)
	}
	wantRemoved := len(before.Artifacts) + 1
	app.beforeAccountMutationLocksForTest = func() {
		acc, found, err := account.Load(app.Paths.AccountDir("claude", "main"))
		if err != nil || !found {
			t.Fatalf("load concurrent snapshot: found=%v err=%v", found, err)
		}
		art := acc.Artifacts[acc.ArtifactNames()[0]]
		art.SecretRef = extraRef
		acc.Artifacts["extra"] = art
		if err := be.Set(ctx, extraRef, []byte("extra-payload")); err != nil {
			t.Fatal(err)
		}
		if err := account.Save(app.Paths.AccountDir("claude", "main"), acc); err != nil {
			t.Fatal(err)
		}
	}

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.SecretsRemoved != wantRemoved {
		t.Fatalf("secrets_removed=%d want %d", report.SecretsRemoved, wantRemoved)
	}
	if _, ok, err := be.Get(ctx, extraRef); err != nil || ok {
		t.Fatalf("concurrently captured secret survived: present=%v err=%v", ok, err)
	}
}

func TestAccountRmRechecksUseIsolatedRaceUnderLifecycleLock(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	app.beforeAccountMutationLocksForTest = func() {
		if code := runUseIsolated(ctx, app, commonOpts{Format: formatText}, "claude", "main"); code != constants.ExitOK {
			t.Fatalf("racing use -i exit %d", code)
		}
	}

	_, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", true)
	if exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("use-i race was not rechecked: exit=%d err=%v", exitOf(err), err)
	}
	if _, found, loadErr := account.Load(app.Paths.AccountDir("claude", "main")); loadErr != nil || !found {
		t.Fatalf("use-i race refusal removed snapshot: found=%v err=%v", found, loadErr)
	}
}
