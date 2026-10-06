package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

const daemonVersionMoved = "has changed where it puts the socket"

// queryCall is one runner.QueryWithEnv invocation a test recorded.
type queryCall struct {
	env  []string
	name string
	args []string
}

// daemonVersionFixture is probeFixture with codex (only) on PATH and
// runner.QueryWithEnv answering reply; it returns the recorded calls.
// enabled sets residentDaemonVersionEnabled for the test.
func daemonVersionFixture(t *testing.T, enabled bool,
	reply func(ctx context.Context) (string, int),
) (*App, adapter.ResidentHolder, *[]queryCall) {
	t.Helper()
	savedEnabled, savedQuery := residentDaemonVersionEnabled, runner.QueryWithEnv
	t.Cleanup(func() { residentDaemonVersionEnabled, runner.QueryWithEnv = savedEnabled, savedQuery })
	residentDaemonVersionEnabled = enabled
	calls := &[]queryCall{}
	runner.QueryWithEnv = func(ctx context.Context, env []string, name string, args ...string) (string, int) {
		*calls = append(*calls, queryCall{env: slices.Clone(env), name: name, args: slices.Clone(args)})
		return reply(ctx)
	}
	app, _, h := probeFixture(t, probeAccount)
	app.Env.LookPath = func(name string) (string, error) {
		if name == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	return app, h, calls
}

// statusReply answers `daemon version` with output and exit code 0.
func statusReply(output string) func(context.Context) (string, int) {
	return func(context.Context) (string, int) { return output, 0 }
}

func runningAt(socket string) string {
	return `{"cliVersion":"0.160.0","status":"running","socketPath":"` + socket + `"}` + "\n"
}

// doctorDaemonVersionRows runs doctor with codex's `--version` failing (so
// upstream_version stays out of it) and returns its resident_drift rows.
func doctorDaemonVersionRows(t *testing.T, app *App, filter string) []adapter.Check {
	t.Helper()
	var rows []adapter.Check
	runner.With(&runnertest.Fake{Code: 1}, func() {
		rows = residentDriftRows(buildDoctor(context.Background(), app, filter, false))
	})
	return rows
}

// The outcomes of `daemon version`: only a running daemon away from the declared
// socket, or with no declared socket at all, warns. The declared socket is a
// symlink to a live fake daemon holding the live account, so the socket half is
// silent throughout.
func TestDoctorDaemonVersionOutcomes(t *testing.T) {
	other := filepath.Join(t.TempDir(), "moved.sock")
	for _, tc := range []struct {
		name     string
		declared bool // link the declared socket to a fake daemon
		reply    func(target, declared string) func(context.Context) (string, int)
		warn     bool
	}{
		{"reports the declared path", true, func(_, declared string) func(context.Context) (string, int) {
			return statusReply(runningAt(declared))
		}, false},
		{"reports the symlink's target", true, func(target, _ string) func(context.Context) (string, int) {
			return statusReply(runningAt(target))
		}, false},
		{"reports another path", true, func(string, string) func(context.Context) (string, int) {
			return statusReply(runningAt(other))
		}, true},
		{"declared socket is absent", false, func(string, string) func(context.Context) (string, int) {
			return statusReply(runningAt(other))
		}, true},
		{"not running", false, func(string, string) func(context.Context) (string, int) {
			return statusReply(`{"status":"stopped"}`)
		}, false},
		{"unparseable", false, func(string, string) func(context.Context) (string, int) {
			return statusReply("codex-cli 0.160.0\n")
		}, false},
		{"exits non-zero", false, func(string, string) func(context.Context) (string, int) {
			return func(context.Context) (string, int) { return runningAt(other), 2 }
		}, false},
		{"killed at the deadline", false, func(string, string) func(context.Context) (string, int) {
			return func(context.Context) (string, int) { return runningAt(other), -1 }
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var target, declared string
			app, h, calls := daemonVersionFixture(t, true, func(ctx context.Context) (string, int) {
				return tc.reply(target, declared)(ctx)
			})
			declared = h.ResidentDaemon(app.Env).Socket
			if tc.declared {
				target = startFakeDaemon(t, accountReadReply(probeAccount)).socket
				linkSocket(t, app, h, target)
			}
			rows := doctorDaemonVersionRows(t, app, "")
			if len(*calls) != 1 {
				t.Fatalf("daemon version ran %d time(s), want 1", len(*calls))
			}
			if !tc.warn {
				if len(rows) != 0 {
					t.Fatalf("want silence, got %+v", rows)
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("want one resident_drift row, got %+v", rows)
			}
			row := rows[0]
			msg := row.Message.Error()
			if row.Tool != constants.ToolCodex || row.Status != constants.StatusWarn ||
				!strings.Contains(msg, daemonVersionMoved) ||
				!strings.HasSuffix(msg, "run: codex app-server daemon restart") {
				t.Errorf("row = %+v", row)
			}
			for _, path := range []string{other, declared, target} {
				if path != "" && strings.Contains(msg, path) {
					t.Errorf("message %q names the path %q", msg, path)
				}
			}
		})
	}
}

// The command is the adapter's status argv with CODEX_HOME set to the real codex
// home, even when kae runs inside a bound directory that exports another one;
// the restart it names carries the real home for that shell.
func TestDoctorDaemonVersionRunsAgainstTheRealHome(t *testing.T) {
	app, h, calls := daemonVersionFixture(t, true, statusReply(runningAt(filepath.Join(t.TempDir(), "moved.sock"))))
	realHome := strings.TrimPrefix(h.ResidentDaemon(app.Env).Env[0], "CODEX_HOME=")
	bound := app.Paths.IsolatedConfigDir("abcdef0123456789", constants.ToolCodex, "main")
	inner, innerLookup := app.Env.Getenv, app.Env.LookupEnv
	app.Env.Getenv = func(key string) string {
		if key == "CODEX_HOME" {
			return bound
		}
		return inner(key)
	}
	app.Env.LookupEnv = func(key string) (string, bool) {
		if key == "CODEX_HOME" {
			return bound, true
		}
		return innerLookup(key)
	}
	rows := doctorDaemonVersionRows(t, app, "")
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
	call := (*calls)[0]
	if call.name != "codex" || !slices.Equal(call.args, []string{"app-server", "daemon", "version"}) {
		t.Errorf("ran %s %q", call.name, call.args)
	}
	if !slices.Equal(call.env, []string{"CODEX_HOME=" + realHome}) {
		t.Errorf("env = %q, want only the real home %q", call.env, realHome)
	}
	want := "run: CODEX_HOME='" + realHome + "' codex app-server daemon restart"
	if len(rows) != 1 || !strings.HasSuffix(rows[0].Message.Error(), want) {
		t.Errorf("rows = %+v, want one ending %q", rows, want)
	}
}

// Off by default: doctor never runs `daemon version`, whatever the filter.
func TestDoctorDaemonVersionDisabledByDefault(t *testing.T) {
	if residentDaemonVersionEnabled {
		t.Fatal("the daemon version half is enabled before the acceptance records that it is safe")
	}
	app, _, calls := daemonVersionFixture(t, false, statusReply(runningAt("/elsewhere.sock")))
	for _, filter := range []string{"", constants.ToolCodex} {
		if rows := doctorDaemonVersionRows(t, app, filter); len(rows) != 0 {
			t.Errorf("filter %q: rows = %+v", filter, rows)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("daemon version ran while disabled: %+v", *calls)
	}
}

// A filter naming another tool, or codex missing from PATH, runs nothing.
func TestDoctorDaemonVersionSkips(t *testing.T) {
	app, _, calls := daemonVersionFixture(t, true, statusReply(runningAt("/elsewhere.sock")))
	if rows := doctorDaemonVersionRows(t, app, constants.ToolClaude); len(rows) != 0 || len(*calls) != 0 {
		t.Errorf("doctor claude: rows %+v, calls %+v", rows, *calls)
	}
	app.Env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if rows := doctorDaemonVersionRows(t, app, ""); len(rows) != 0 || len(*calls) != 0 {
		t.Errorf("no codex on PATH: rows %+v, calls %+v", rows, *calls)
	}
}

// `daemon version` runs in the `--version` round, concurrently with the probes
// and under its deadline: a probe that blocks is cut at the deadline.
func TestDoctorDaemonVersionSharesTheProbeRound(t *testing.T) {
	saved := upstreamVersionProbeDeadline
	upstreamVersionProbeDeadline = 200 * time.Millisecond
	t.Cleanup(func() { upstreamVersionProbeDeadline = saved })
	hadDeadline := false
	app, _, calls := daemonVersionFixture(t, true, func(ctx context.Context) (string, int) {
		_, hadDeadline = ctx.Deadline()
		<-ctx.Done()
		return "", -1
	})
	start := time.Now()
	rows := doctorDaemonVersionRows(t, app, "")
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("doctor took %v with a blocking daemon version", elapsed)
	}
	if len(*calls) != 1 || !hadDeadline || len(rows) != 0 {
		t.Errorf("calls %d, deadline %v, rows %+v", len(*calls), hadDeadline, rows)
	}
}

// The warning renders in Japanese while JSON and Error() keep English.
func TestDoctorDaemonVersionMessageInJapanese(t *testing.T) {
	app, _, _ := daemonVersionFixture(t, true, statusReply(runningAt("/elsewhere.sock")))
	rows := doctorDaemonVersionRows(t, app, constants.ToolCodex)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	english := rows[0].Message.Error()
	l10ntest.UseJapanese(t)
	want := "codex の管理デーモンは起動中と報告していますが、kae が探すソケットにはありません。codex がソケットの置き場所を変えたため、切替でデーモンを見つけて再起動できなくなっています。codex を切り替えるたびに codex app-server daemon restart を実行してください。"
	if got := l10n.Render(rows[0].Message); got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if got := rows[0].Message.Error(); got != english {
		t.Errorf("Error() under Japanese = %q, must stay English %q", got, english)
	}
}

// socketMoved compares resolved paths: a reported path under a symlinked
// directory equals its target, and a declared socket that is a dangling link is
// absent.
func TestSocketMovedResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(real, "s.sock")
	writeFile(t, sock, "")
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if socketMoved(filepath.Join(alias, "s.sock"), sock) {
		t.Error("a path under a symlinked directory read as moved")
	}
	if socketMoved(sock, filepath.Join(alias, "s.sock")) {
		t.Error("a declared path under a symlinked directory read as moved")
	}
	dangling := filepath.Join(dir, "dangling.sock")
	if err := os.Symlink(filepath.Join(dir, "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	if !socketMoved(sock, dangling) {
		t.Error("a dangling declared socket did not read as absent")
	}
	if !socketMoved(filepath.Join(real, "other.sock"), sock) {
		t.Error("another socket in the same directory did not read as moved")
	}
}
