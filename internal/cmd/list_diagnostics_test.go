package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestDiagnosticListsSeparateConfigFromCompleteness(t *testing.T) {
	for _, kind := range []string{"backup", "preservation"} {
		for _, broken := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-complete", true: "-incomplete"}[broken], func(t *testing.T) {
				app, _, saved, _ := preservationFixture(t)
				id := backup.NewID(app.Paths.BackupsDir(), app.Now())
				if err := backup.Save(app.Paths.BackupsDir(), backup.Meta{SchemaVersion: constants.SchemaVersion, ID: id, CreatedAt: app.Now(), Reason: constants.BackupReasonSwitch, Tools: []string{constants.ToolClaude}}); err != nil {
					t.Fatal(err)
				}
				app.backendForTest = nil
				app.Env.GOOS = "linux"
				app.Config.Security.SecretBackend = "keychain"
				cfgPath := filepath.Join(t.TempDir(), "bad.toml")
				writeFile(t, cfgPath, "[invalid "+mainToken)
				_, _, app.ConfigErr = config.Load(cfgPath)
				if app.ConfigErr == nil {
					t.Fatal("fixture needs config error")
				}
				dir := app.Paths.BackupsDir()
				if kind == "preservation" {
					dir = app.Paths.PreservationsDir()
				}
				badName := "secret-" + mainToken + "\n.json"
				if broken {
					writeFile(t, filepath.Join(dir, badName), "invalid "+mainToken)
				}
				code, out := captureStdout(t, func() int {
					if kind == "backup" {
						return runBackupList(context.Background(), app, commonOpts{Format: formatJSON})
					}
					return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
				})
				want := constants.ExitOK
				if broken {
					want = constants.ExitError
				}
				mustExit(t, want, code, out)
				var r struct {
					listDiagnostics
					Backups       []backupItem       `json:"backups"`
					Preservations []preservationItem `json:"preservations"`
				}
				if err := json.Unmarshal([]byte(out), &r); err != nil {
					t.Fatal(err)
				}
				if r.Complete == broken || len(r.Warnings) != 1 || r.Warnings[0] != constants.ListWarningConfig {
					t.Fatalf("wrong completeness/config report: %s", out)
				}
				if kind == "backup" && (len(r.Backups) != 1 || r.Backups[0].ID != id) {
					t.Fatalf("lost readable backup: %s", out)
				}
				if kind == "preservation" && (len(r.Preservations) != 1 || r.Preservations[0].ID != saved.ID) {
					t.Fatalf("lost readable preservation: %s", out)
				}
				if broken && (len(r.Issues) != 1 || r.Issues[0].Code != constants.ListIssueInvalid || !strings.HasPrefix(r.Issues[0].Entry, "sha256:")) {
					t.Fatalf("wrong issue: %s", out)
				}
				if strings.Contains(out, mainToken) || strings.Contains(out, "invalid metadata") {
					t.Fatal("diagnostics exposed raw private data")
				}
				var diagnostics string
				code, out = captureStdout(t, func() int {
					var exit int
					exit, diagnostics = captureStderr(t, func() int {
						if kind == "backup" {
							return runBackupList(context.Background(), app, commonOpts{Format: formatText})
						}
						return runPreservation(context.Background(), app, commonOpts{Format: formatText}, "list", "")
					})
					return exit
				})
				mustExit(t, want, code, out)
				visibleID := id
				if kind == "preservation" {
					visibleID = saved.ID
				}
				if !strings.Contains(out, visibleID) || !strings.Contains(diagnostics, "config is invalid or unreadable") || strings.Contains(out+diagnostics, mainToken) {
					t.Fatalf("unsafe or missing text diagnostics: %s %s", out, diagnostics)
				}
				if broken && (!strings.Contains(diagnostics, "listing is incomplete") || !strings.Contains(diagnostics, constants.ListIssueInvalid)) {
					t.Fatalf("text did not disclose incomplete inventory: %s", diagnostics)
				}
				code, out = captureStdout(t, func() int {
					if kind == "backup" {
						return runRollback(context.Background(), app, commonOpts{Format: formatJSON}, id)
					}
					return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "rm", saved.ID)
				})
				mustExit(t, constants.ExitInvalidConfig, code, out)
			})
		}
	}
}

func TestDiagnosticListsReportEnumerationFailure(t *testing.T) {
	for _, kind := range []string{"backup", "preservation"} {
		t.Run(kind, func(t *testing.T) {
			app := testApp(t, nil)
			dir := app.Paths.BackupsDir()
			if kind == "preservation" {
				dir = app.Paths.PreservationsDir()
			}
			writeFile(t, dir, "not a directory")
			code, out := captureStdout(t, func() int {
				if kind == "backup" {
					return runBackupList(context.Background(), app, commonOpts{Format: formatJSON})
				}
				return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
			})
			mustExit(t, constants.ExitError, code, out)
			var r listDiagnostics
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatal(err)
			}
			if r.Complete || len(r.Issues) != 1 || r.Issues[0].Code != constants.ListIssueEnumeration {
				t.Fatalf("wrong enumeration report: %s", out)
			}
		})
	}
}

func TestDiagnosticListsDoNotFollowMetadataSymlinks(t *testing.T) {
	app := testApp(t, nil)
	outside := filepath.Join(t.TempDir(), "private")
	writeFile(t, outside, mainToken)
	for _, dir := range []string{app.Paths.BackupsDir(), app.Paths.PreservationsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "entry.json")); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []func() int{func() int { return runBackupList(context.Background(), app, commonOpts{Format: formatJSON}) }, func() int {
		return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
	}} {
		code, out := captureStdout(t, run)
		mustExit(t, constants.ExitError, code, out)
		var r listDiagnostics
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Issues) != 1 || r.Issues[0].Code != constants.ListIssueEntry || strings.Contains(out, mainToken) {
			t.Fatalf("wrong symlink diagnostic: %s", out)
		}
	}
}

func TestDiagnosticListsUnreadableMetadata(t *testing.T) {
	for _, kind := range []string{"backup", "preservation"} {
		t.Run(kind, func(t *testing.T) {
			app := testApp(t, nil)
			dir := app.Paths.BackupsDir()
			if kind == "preservation" {
				dir = app.Paths.PreservationsDir()
			}
			path := filepath.Join(dir, "unreadable.json")
			writeFile(t, path, "private")
			if err := os.Chmod(path, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			if _, err := os.ReadFile(path); err == nil {
				t.Skip("OS permits reading mode-000 file")
			}
			code, out := captureStdout(t, func() int {
				if kind == "backup" {
					return runBackupList(context.Background(), app, commonOpts{Format: formatJSON})
				}
				return runPreservation(context.Background(), app, commonOpts{Format: formatJSON}, "list", "")
			})
			mustExit(t, constants.ExitError, code, out)
			var r listDiagnostics
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatal(err)
			}
			if r.Complete || len(r.Issues) != 1 || r.Issues[0].Code != constants.ListIssueRead {
				t.Fatalf("wrong read diagnostic: %s", out)
			}
		})
	}
}

func TestBackupDiagnosticDoesNotChangeRollbackSelection(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, 0, code, out)
	before := readFile(t, app.Env.Home+"/.claude/.credentials.json")
	writeFile(t, filepath.Join(app.Paths.BackupsDir(), "99999999T999999Z.json"), "broken")
	code, out = captureStdout(t, func() int { return runBackupList(ctx, app, opts) })
	mustExit(t, constants.ExitError, code, out)
	code, out = captureStdout(t, func() int { return runRollback(ctx, app, opts, "") })
	if code == 0 {
		t.Fatalf("rollback skipped damaged newest metadata: %s", out)
	}
	if readFile(t, app.Env.Home+"/.claude/.credentials.json") != before {
		t.Fatal("rollback changed live store")
	}
}

func TestDiagnosticListRecoveryGuidance(t *testing.T) {
	for _, kind := range []string{"backup", "preservation"} {
		for _, problem := range []struct{ code, advice string }{
			{constants.ListIssueEnumeration, "resolved state directory"},
			{constants.ListIssueRead, "parent-directory permissions"},
			{constants.ListIssueInvalid, "docs/DATA-MODEL.md"},
			{constants.ListIssueEntry, "without following symlinks"},
		} {
			t.Run(kind+"/"+problem.code, func(t *testing.T) {
				app := testApp(t, nil)
				app.Config.Security.SecretBackend = "unavailable"
				dir := app.Paths.BackupsDir()
				if kind == "preservation" {
					dir = app.Paths.PreservationsDir()
				}
				path := filepath.Join(dir, mainToken+".json")
				switch problem.code {
				case constants.ListIssueEnumeration:
					writeFile(t, dir, mainToken)
				case constants.ListIssueRead:
					writeFile(t, path, mainToken)
					if err := os.Chmod(path, 0); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
					if _, err := os.ReadFile(path); err == nil {
						t.Skip("OS permits reading mode-000 file")
					}
				case constants.ListIssueInvalid:
					writeFile(t, path, mainToken)
				case constants.ListIssueEntry:
					writeFile(t, path, mainToken)
					if err := os.Symlink(path, filepath.Join(dir, "link.json")); err != nil {
						t.Fatal(err)
					}
				}
				for _, format := range []string{formatText, formatJSON} {
					code, out, diagnostic := captureBoth(t, func() int {
						opts := commonOpts{Format: format}
						if kind == "backup" {
							return runBackupList(context.Background(), app, opts)
						}
						return runPreservation(context.Background(), app, opts, "list", "")
					})
					mustExit(t, constants.ExitError, code, out)
					if strings.Contains(out+diagnostic, mainToken) {
						t.Fatal("secret-bearing entry leaked")
					}
					if format == formatText {
						if !strings.Contains(diagnostic, problem.advice) || strings.Contains(out, problem.advice) {
							t.Fatalf("guidance delivery: %q %q", out, diagnostic)
						}
					} else {
						var report map[string]json.RawMessage
						if err := json.Unmarshal([]byte(out), &report); err != nil {
							t.Fatal(err)
						}
						if len(report) != 5 || diagnostic != "" || !strings.Contains(out, problem.code) {
							t.Fatalf("JSON contract changed: %s %s", out, diagnostic)
						}
					}
				}
			})
		}
	}
}
