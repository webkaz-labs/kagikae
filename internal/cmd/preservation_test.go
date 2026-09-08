package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/preservation"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

func preservationFixture(t *testing.T) (*App, preservation.Store, preservation.Record, string) {
	t.Helper()
	app := overlayTestApp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	dir, _, credFile := boundStoreForClaudeMain(t, app)
	origin, sp, err := app.preservationOrigin(context.Background(), dir, constants.ToolClaude)
	if err != nil {
		t.Fatal(err)
	}
	store := app.preservationStore(testBackend(t, app))
	live, err := artifact.ReadLive(context.Background(), sp)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(context.Background(), origin, live.Data, "")
	if err != nil {
		t.Fatal(err)
	}
	return app, store, saved.Record, credFile
}

func TestPreservationRestoreKeepsDisplacedCopyWithoutSnapshotAdoption(t *testing.T) {
	app, store, saved, credFile := preservationFixture(t)
	ctx := context.Background()
	original := readFile(t, credFile)
	displaced := claudeOAuthPayload("sk-ant-oat01-preservation-side", app.Now().Add(9*time.Hour))
	writeFile(t, credFile, displaced)
	be := testBackend(t, app)
	snapshot := snapshotPayload(t, app, be, constants.ToolClaude, "main")
	code, out := captureStdout(t, func() int { return runPreservation(ctx, app, commonOpts{Format: formatJSON}, "restore", saved.ID) })
	mustExit(t, constants.ExitOK, code, out)
	if readFile(t, credFile) != original {
		t.Fatal("original bytes not restored")
	}
	if snapshotPayload(t, app, be, constants.ToolClaude, "main") != snapshot {
		t.Fatal("restore adopted an account snapshot")
	}
	records, err := store.List(ctx)
	if err != nil || len(records) != 2 {
		t.Fatalf("displaced copy missing: %v %v", records, err)
	}
	found := false
	for _, r := range records {
		_, data, err := store.Load(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-ant-oat01-preservation-side") {
			found = true
		}
	}
	if !found {
		t.Fatal("destination bytes not preserved")
	}
	if strings.Contains(out, "sk-ant-") {
		t.Fatal("restore output leaked credential")
	}
}

func TestPreservationRestoreRefusesMappingChanges(t *testing.T) {
	for _, change := range []string{"account", "config", "credential", "dynamic", "missing", "driver", "irrelevant"} {
		t.Run(change, func(t *testing.T) {
			app, store, saved, credFile := preservationFixture(t)
			before := readFile(t, credFile)
			path := filepath.Join(saved.Origin.Directory, fragmentRelPath)
			fragment := readFile(t, path)
			switch change {
			case "account":
				fragment = strings.ReplaceAll(fragment, "# kae:account:claude=main", "# kae:account:claude=side")
			case "config":
				fragment = strings.ReplaceAll(fragment, saved.Origin.ConfigDir, t.TempDir())
			case "credential":
				fragment = strings.ReplaceAll(fragment, saved.Origin.CredDir, t.TempDir())
			case "dynamic":
				fragment = strings.ReplaceAll(fragment, saved.Origin.ConfigDir, "{{env.HOME}}")
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "driver":
				previous := app.Env.Getenv
				app.Env.Getenv = func(key string) string {
					if key == "KAE_CLAUDE_DRIVER" {
						return "keychain"
					}
					return previous(key)
				}
			case "irrelevant":
				fragment += "\n# an unrelated comment\n"
			}
			if change != "missing" {
				writeFile(t, path, fragment)
			}
			code, out, stderr := captureBoth(t, func() int {
				return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "restore", saved.ID)
			})
			want := constants.ExitUnsafeRefused
			if change == "irrelevant" {
				want = constants.ExitOK
			}
			mustExit(t, want, code, out+stderr)
			if readFile(t, credFile) != before {
				t.Fatal("mapping test altered store")
			}
			records, err := store.List(context.Background())
			if err != nil || len(records) != 1 {
				t.Fatalf("refusal added record: %v %v", records, err)
			}
		})
	}
}

func TestPreservationDryRunAndConfirmedDelete(t *testing.T) {
	app, store, saved, credFile := preservationFixture(t)
	ctx := context.Background()
	writeFile(t, credFile, `{"claudeAiOauth":{"unrecognized":"payload"}}`)
	for _, action := range []string{"restore", "rm"} {
		code, out := captureStdout(t, func() int {
			return runPreservation(ctx, app, commonOpts{Format: formatJSON, DryRun: true}, action, saved.ID)
		})
		mustExit(t, constants.ExitOK, code, out)
		if readFile(t, credFile) != `{"claudeAiOauth":{"unrecognized":"payload"}}` {
			t.Fatal("dry run wrote destination")
		}
		if records, err := store.List(ctx); err != nil || len(records) != 1 {
			t.Fatalf("dry run changed records: %v %v", records, err)
		}
	}
	code, out, stderr := captureBoth(t, func() int { return runPreservation(ctx, app, commonOpts{Format: formatJSON}, "rm", saved.ID) })
	mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
	code, out = captureStdout(t, func() int {
		return runPreservation(ctx, app, commonOpts{Format: formatJSON, Yes: true}, "rm", saved.ID)
	})
	mustExit(t, constants.ExitOK, code, out)
	if records, err := store.List(ctx); err != nil || len(records) != 0 {
		t.Fatalf("explicit deletion failed: %v %v", records, err)
	}
	if readFile(t, credFile) != `{"claudeAiOauth":{"unrecognized":"payload"}}` {
		t.Fatal("delete changed live credential")
	}
}

func TestPreservationListDoesNotClaimIdentityOrExposePayload(t *testing.T) {
	app, store, saved, _ := preservationFixture(t)
	// A platform-incompatible selection must not block metadata-only recovery.
	app.backendForTest = nil
	app.Env.GOOS = "linux"
	app.Config.Security.SecretBackend = secret.BackendKeychain
	if _, err := app.secretBackend(); err == nil {
		t.Fatal("expected unavailable backend selection")
	}
	code, out := captureStdout(t, func() int {
		return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
	})
	mustExit(t, constants.ExitOK, code, out)
	var report preservationListReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Preservations) != 1 || report.Preservations[0].ID != saved.ID || report.Preservations[0].Identity != "unknown" || report.Preservations[0].BoundAccount != "main" {
		t.Fatalf("wrong list %s", out)
	}
	if strings.Contains(out, mainToken) || strings.Contains(out, "refreshToken") {
		t.Fatal("list exposed payload")
	}
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("wrong schema: %d", report.SchemaVersion)
	}
	for _, action := range []string{"restore", "rm"} {
		for _, id := range []string{saved.ID, "invalid"} {
			code, out := captureStdout(t, func() int {
				return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, action, id)
			})
			mustExit(t, constants.ExitSecretStore, code, out)
		}
	}
	metadataPath := filepath.Join(store.Dir, saved.ID+".json")
	for _, state := range []string{constants.PreservationStatePending, constants.PreservationStateDeleting} {
		saved.State = state
		data, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, metadataPath, string(data))
		code, out := captureStdout(t, func() int {
			return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
		})
		mustExit(t, constants.ExitOK, code, out)
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Preservations) != 1 || report.Preservations[0].State != state {
			t.Fatalf("incomplete record disappeared: %s", out)
		}
	}
	writeFile(t, metadataPath, "invalid metadata "+mainToken)
	code, out = captureStdout(t, func() int {
		return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
	})
	mustExit(t, constants.ExitError, code, out)
	if strings.Contains(out, mainToken) {
		t.Fatal("metadata error exposed payload")
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, metadataPath, string(data))
	if err := store.Remove(context.Background(), saved.ID); err != nil {
		t.Fatal(err)
	}
	code, out = captureStdout(t, func() int {
		return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
	})
	mustExit(t, constants.ExitOK, code, out)
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Preservations == nil || len(report.Preservations) != 0 {
		t.Fatalf("empty list must be an array: %s", out)
	}
}

func TestReloginPreservationCapacityRefusesBeforeFlow(t *testing.T) {
	app, store, _, credFile := preservationFixture(t)
	original := claudeOAuthPayload("sk-ant-oat01-full-new", app.Now().Add(9*time.Hour))
	writeFile(t, credFile, original)
	app.Config.Security.PreservationMaxBytes = 1
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		t.Fatal("full capacity launched login")
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(context.Background(), app, commonOpts{Format: formatJSON}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
	records, err := store.List(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatal("full capacity evicted old copy")
	}
	if readFile(t, credFile) != original {
		t.Fatal("full capacity touched live store")
	}
}

func TestPreservationRestoreAdmissionMatchesDryRun(t *testing.T) {
	for _, reason := range []string{"quota", "selected-oldest"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%v", reason, dry), func(t *testing.T) {
				app, store, saved, credFile := preservationFixture(t)
				ctx := context.Background()
				if reason == "quota" {
					app.Config.Security.PreservationMaxBytes = 1
				} else {
					for _, payload := range []string{"second", "third"} {
						if _, err := store.Save(ctx, saved.Origin, []byte(payload), ""); err != nil {
							t.Fatal(err)
						}
					}
				}
				live := claudeOAuthPayload("sk-ant-oat01-fourth", app.Now().Add(8*time.Hour))
				writeFile(t, credFile, live)
				before, err := store.List(ctx)
				if err != nil {
					t.Fatal(err)
				}
				code, out, stderr := captureBoth(t, func() int {
					return runPreservation(ctx, app, commonOpts{Format: formatJSON, DryRun: dry}, "restore", saved.ID)
				})
				mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
				if readFile(t, credFile) != live {
					t.Fatal("refused restore wrote credential")
				}
				after, err := store.List(ctx)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("refused restore changed history")
				}
			})
		}
	}
}

type preservationWriteObserver struct {
	secret.Backend
	observe func()
}

func (b preservationWriteObserver) Set(ctx context.Context, key string, data []byte) error {
	if strings.HasPrefix(key, secret.NSPreservation+"/") {
		b.observe()
	}
	return b.Backend.Set(ctx, key, data)
}

func TestPreservationRestoreRechecksAndHoldsInventoryLock(t *testing.T) {
	for _, change := range []string{"credential", "mapping", "absent", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			app, store, saved, credFile := preservationFixture(t)
			ctx := context.Background()
			live := claudeOAuthPayload("sk-ant-oat01-displaced", app.Now().Add(8*time.Hour))
			writeFile(t, credFile, live)
			raced := claudeOAuthPayload("sk-ant-oat01-raced", app.Now().Add(9*time.Hour))
			observed := false
			app.backendForTest = preservationWriteObserver{Backend: store.Backend, observe: func() {
				observed = true
				if err := store.Remove(ctx, saved.ID); !errors.Is(err, lock.ErrBusy) {
					t.Fatalf("restore did not own inventory lock: %v", err)
				}
				switch change {
				case "credential":
					writeFile(t, credFile, raced)
				case "mapping":
					fragment := filepath.Join(saved.Origin.Directory, fragmentRelPath)
					writeFile(t, fragment, strings.ReplaceAll(readFile(t, fragment), "# kae:account:claude=main", "# kae:account:claude=side"))
				default:
					if err := os.Remove(credFile); err != nil {
						t.Fatal(err)
					}
					if change == "unreadable" {
						if err := os.Mkdir(credFile, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				}
			}}
			code, out, stderr := captureBoth(t, func() int { return runPreservation(ctx, app, commonOpts{Format: formatJSON}, "restore", saved.ID) })
			mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
			if !observed {
				t.Fatal("displaced copy was not preserved")
			}
			want := live
			if change == "credential" {
				want = raced
			}
			if change == "absent" {
				if _, err := os.Lstat(credFile); !os.IsNotExist(err) {
					t.Fatalf("refusal recreated credential: %v", err)
				}
			} else if change == "unreadable" {
				if info, err := os.Stat(credFile); err != nil || !info.IsDir() {
					t.Fatalf("refusal replaced unreadable destination: %v", err)
				}
			} else if readFile(t, credFile) != want {
				t.Fatal("restore overwrote concurrent change")
			}
			records, err := store.List(ctx)
			if err != nil || len(records) != 2 {
				t.Fatalf("failed restoration lost preserved copies: %v %v", records, err)
			}
		})
	}
}

func TestReloginRefusesChangesDuringPreservation(t *testing.T) {
	for _, change := range []string{"credential", "mapping", "absent", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			app, store, saved, credFile := preservationFixture(t)
			ctx := context.Background()
			live := claudeOAuthPayload("sk-ant-oat01-before-preservation", app.Now().Add(8*time.Hour))
			writeFile(t, credFile, live)
			raced := claudeOAuthPayload("sk-ant-oat01-after-preservation", app.Now().Add(9*time.Hour))
			observed := false
			app.backendForTest = preservationWriteObserver{Backend: store.Backend, observe: func() {
				observed = true
				switch change {
				case "credential":
					writeFile(t, credFile, raced)
				case "mapping":
					path := filepath.Join(saved.Origin.Directory, fragmentRelPath)
					writeFile(t, path, strings.ReplaceAll(readFile(t, path), "# kae:account:claude=main", "# kae:account:claude=side"))
				default:
					if err := os.Remove(credFile); err != nil {
						t.Fatal(err)
					}
					if change == "unreadable" {
						if err := os.Mkdir(credFile, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				}
			}}
			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				t.Fatal("flow overwrites unpreserved change")
				return 0, nil
			})
			code, out, stderr := captureBoth(t, func() int { return runRelogin(ctx, app, commonOpts{Format: formatJSON}, constants.ToolClaude) })
			mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
			if !observed {
				t.Fatal("preservation hook not exercised")
			}
			want := live
			if change == "credential" {
				want = raced
			}
			if change == "absent" {
				if _, err := os.Lstat(credFile); !os.IsNotExist(err) {
					t.Fatalf("refusal recreated credential: %v", err)
				}
			} else if change == "unreadable" {
				if info, err := os.Stat(credFile); err != nil || !info.IsDir() {
					t.Fatalf("refusal replaced unreadable destination: %v", err)
				}
			} else if readFile(t, credFile) != want {
				t.Fatal("refusal changed live store")
			}
		})
	}
}

func TestPreservationRestoreRefusesSymlinkRetarget(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(fmt.Sprint(ancestor), func(t *testing.T) {
			app, _, saved, credFile := preservationFixture(t)
			// Replace the original path by a symlink to a separate existing store. The
			// metadata binding and lexical adapter target stay unchanged.
			original := readFile(t, credFile)
			outside := t.TempDir()
			other := filepath.Join(outside, filepath.Base(credFile))
			writeFile(t, other, original)
			if ancestor {
				dir := filepath.Dir(credFile)
				if err := os.Rename(dir, dir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(credFile); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, credFile); err != nil {
					t.Fatal(err)
				}
			}
			code, out, stderr := captureBoth(t, func() int {
				return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "restore", saved.ID)
			})
			mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
			if readFile(t, other) != original {
				t.Fatal("restore touched substituted store")
			}
		})
	}
}

func TestPreservationUnknownFormatInterruptedLoginAndRecovery(t *testing.T) {
	for name, payload := range map[string]string{
		"unknown shape":       `{"futureCredential":"synthetic-secret"}`,
		"missing deadline":    `{"accessToken":"synthetic-secret","refreshToken":"synthetic-refresh"}`,
		"nonnumeric deadline": `{"accessToken":"synthetic-secret","expiresAt":"unknown","refreshToken":"synthetic-refresh"}`,
	} {
		t.Run(name, func(t *testing.T) {
			app, store, _, credFile := preservationFixture(t)
			ctx := context.Background()
			document := `{"claudeAiOauth":` + payload + `,"unrelated":"keep"}`
			writeFile(t, credFile, document)
			snapshot := snapshotPayload(t, app, store.Backend, constants.ToolClaude, "main")
			calls := 0
			withInteractive(t, func(_ context.Context, env []string, name string, args ...string) (int, error) {
				calls++
				if name != "claude" || !reflect.DeepEqual(args, []string{"/login"}) {
					t.Fatalf("unexpected login command: %s %v", name, args)
				}
				if !strings.Contains(strings.Join(env, "\n"), "CLAUDE_SECURESTORAGE_CONFIG_DIR="+filepath.Dir(credFile)) {
					t.Fatal("login destination differs from the preserved store")
				}
				return 130, nil
			})
			var selected string
			for attempt := 0; attempt < 2; attempt++ {
				code, out, stderr := captureBoth(t, func() int {
					return runRelogin(ctx, app, commonOpts{Format: formatJSON}, constants.ToolClaude)
				})
				mustExit(t, constants.ExitAuthUnchanged, code, out+stderr)
				if strings.Contains(out+stderr, "synthetic-secret") || strings.Contains(out+stderr, payload) {
					t.Fatal("relogin exposed raw credential")
				}
				records, err := store.List(ctx)
				if err != nil || len(records) != 2 {
					t.Fatalf("retry did not deduplicate: %v", err)
				}
				for _, r := range records {
					_, raw, err := store.Load(ctx, r.ID)
					if err != nil {
						t.Fatal(err)
					}
					if string(raw) == payload {
						if selected != "" && selected != r.ID {
							t.Fatal("retry replaced preservation ID")
						}
						selected = r.ID
					}
				}
			}
			if calls != 2 || selected == "" || readFile(t, credFile) != document {
				t.Fatal("interruption did not retain original bytes")
			}
			displaced := `{"futureCredential":"displaced-secret"}`
			writeFile(t, credFile, `{"claudeAiOauth":`+displaced+`,"unrelated":"keep"}`)
			code, out, stderr := captureBoth(t, func() int {
				return runPreservation(ctx, app, commonOpts{Format: formatJSON}, "restore", selected)
			})
			mustExit(t, constants.ExitOK, code, out+stderr)
			if readFile(t, credFile) != document || snapshotPayload(t, app, store.Backend, constants.ToolClaude, "main") != snapshot {
				t.Fatal("recovery lost raw bytes or adopted a snapshot")
			}
			records, err := store.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range records {
				_, raw, err := store.Load(ctx, r.ID)
				if err != nil {
					t.Fatal(err)
				}
				found = found || string(raw) == displaced
			}
			if !found || strings.Contains(out+stderr, payload) || strings.Contains(out+stderr, displaced) {
				t.Fatal("restore lost displaced bytes or exposed a payload")
			}
		})
	}
}

func TestReloginPreservationWriteFailureAndExplicitRetry(t *testing.T) {
	app, store, _, credFile := preservationFixture(t)
	ctx := context.Background()
	original := `{"claudeAiOauth":{"futureCredential":"synthetic-secret"}}`
	writeFile(t, credFile, original)
	app.backendForTest = setFailBackend{Backend: store.Backend, failFor: secret.NSPreservation + "/"}
	calls := 0
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		calls++
		return 130, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatJSON}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitSecretStore, code, out+stderr)
	if calls != 0 || readFile(t, credFile) != original {
		t.Fatal("preservation failure started login or touched original")
	}
	app.backendForTest = store.Backend
	code, out, stderr = captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatJSON}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
	if calls != 0 || !strings.Contains(out+stderr, "inventory is incomplete") {
		t.Fatal("retry bypassed incomplete inventory")
	}
	records, err := store.List(ctx)
	if err != nil || len(records) != 2 {
		t.Fatalf("pending record not visible: %v", err)
	}
	var pending string
	for _, r := range records {
		if r.State == constants.PreservationStatePending {
			pending = r.ID
		}
	}
	if pending == "" {
		t.Fatal("failed write has no pending record")
	}
	code, out, stderr = captureBoth(t, func() int {
		return runPreservation(ctx, app, commonOpts{Format: formatJSON, Yes: true}, "rm", pending)
	})
	mustExit(t, constants.ExitOK, code, out+stderr)
	code, out, stderr = captureBoth(t, func() int {
		return runRelogin(ctx, app, commonOpts{Format: formatJSON}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitAuthUnchanged, code, out+stderr)
	if calls != 1 || readFile(t, credFile) != original {
		t.Fatal("retry did not reach the interrupted flow with original intact")
	}
}

func TestReloginRefusesUnreadableAndMalformedCredentialBeforeFlow(t *testing.T) {
	for _, kind := range []string{"malformed", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			app, store, _, credFile := preservationFixture(t)
			before, err := store.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "malformed" {
				writeFile(t, credFile, "malformed-synthetic-secret")
			} else {
				if err := os.Remove(credFile); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(credFile, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				t.Fatal("unpreservable input launched login")
				return 0, nil
			})
			code, out, stderr := captureBoth(t, func() int {
				return runRelogin(context.Background(), app, commonOpts{Format: formatJSON}, constants.ToolClaude)
			})
			mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
			if !strings.Contains(out+stderr, "cannot preserve") || strings.Contains(out+stderr, "malformed-synthetic-secret") {
				t.Fatal("refusal lost its observation or exposed input")
			}
			after, err := store.List(context.Background())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("unreadable input changed preservation history")
			}
			if kind == "malformed" {
				if readFile(t, credFile) != "malformed-synthetic-secret" {
					t.Fatal("malformed input was rewritten")
				}
			} else if info, err := os.Stat(credFile); err != nil || !info.IsDir() {
				t.Fatal("unreadable destination was replaced")
			}
		})
	}
}

func TestPreservationRestoreRefusesMovedDirectoryUntilOriginalPathReturns(t *testing.T) {
	app, _, saved, credFile := preservationFixture(t)
	ctx := context.Background()
	original := readFile(t, credFile)
	moved := saved.Origin.Directory + "-moved"
	if err := os.Rename(saved.Origin.Directory, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, saved.Origin.Directory) })
	for _, dry := range []bool{true, false} {
		code, out, stderr := captureBoth(t, func() int {
			return runPreservation(ctx, app, commonOpts{Format: formatJSON, DryRun: dry}, "restore", saved.ID)
		})
		mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
		if readFile(t, credFile) != original {
			t.Fatal("moved-directory refusal wrote credential")
		}
	}
	if err := os.Rename(moved, saved.Origin.Directory); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := captureBoth(t, func() int {
		return runPreservation(ctx, app, commonOpts{Format: formatJSON}, "restore", saved.ID)
	})
	mustExit(t, constants.ExitOK, code, out+stderr)
}
