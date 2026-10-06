package cmd

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// residentDaemonError is a daemon answer kae cannot read an account from, so
// the probe reads unknown; its message carries personal data that must not leak.
var residentDaemonError = `{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"` + probeEmail + " " + probeOtherAcct + `"}}`

// residentDriftRows returns the resident_drift checks of a doctor report.
func residentDriftRows(report *doctorReport) []adapter.Check {
	var rows []adapter.Check
	for _, c := range report.Checks {
		if c.Code == constants.CheckResidentDrift {
			rows = append(rows, c)
		}
	}
	return rows
}

// The four observations through the registered check: differs (another account,
// or none) and unknown warn on codex and name the restart; absent and matches
// are silent.
func TestDoctorResidentDriftByObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string // "" serves no daemon at all
		want   string // substring of the one warning; "" means no row
	}{
		{"absent", "", ""},
		{"matches", accountReadReply(probeAccount), ""},
		{"differs", accountReadReply(probeOtherAcct), "is not using the live credential's account"},
		{"differs, no account", noAccountReply, "is not using the live credential's account"},
		{"unknown", residentDaemonError, "kae cannot read which account codex's managed daemon holds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _, h := probeFixture(t, probeAccount)
			if tc.answer != "" {
				linkSocket(t, app, h, startFakeDaemon(t, tc.answer).socket)
			}
			rows := residentDriftRows(buildDoctor(context.Background(), app, "", false))
			if tc.want == "" {
				if len(rows) != 0 {
					t.Fatalf("%s must be silent, got %+v", tc.name, rows)
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("want one resident_drift row, got %+v", rows)
			}
			row := rows[0]
			if row.Tool != constants.ToolCodex || row.Status != constants.StatusWarn {
				t.Errorf("row = %+v, want a codex warning", row)
			}
			msg := row.Message.Error()
			if !strings.Contains(msg, tc.want) || !strings.HasSuffix(msg, "run: codex app-server daemon restart") {
				t.Errorf("message = %q", msg)
			}
		})
	}
}

// A filter naming another tool skips the probe; naming codex keeps it.
func TestDoctorResidentDriftHonorsTheToolFilter(t *testing.T) {
	app, _, h := probeFixture(t, probeAccount)
	d := startFakeDaemon(t, accountReadReply(probeOtherAcct))
	linkSocket(t, app, h, d.socket)
	if rows := residentDriftRows(buildDoctor(context.Background(), app, constants.ToolClaude, false)); len(rows) != 0 {
		t.Errorf("doctor claude must not report codex's daemon: %+v", rows)
	}
	if n := d.accepted.Load(); n != 0 {
		t.Errorf("doctor claude connected to the daemon %d time(s)", n)
	}
	if rows := residentDriftRows(buildDoctor(context.Background(), app, constants.ToolCodex, false)); len(rows) != 1 {
		t.Errorf("doctor codex must report the daemon: %+v", rows)
	}
}

// Inside a bound directory the check still probes the real codex home's daemon,
// compared with the real home's credential, not the bound home's, and names the
// restart with the real home, since the bare command typed in that shell would
// restart the bound home's daemon (docs/CLI.md § kae use Semantics, The manual
// step). Outside one, the bare command is ByObservation's suffix.
func TestDoctorResidentDriftProbesTheRealHomeInsideABoundDirectory(t *testing.T) {
	app, _, h := probeFixture(t, probeAccount)
	linkSocket(t, app, h, startFakeDaemon(t, accountReadReply(probeOtherAcct)).socket)
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
	rows := residentDriftRows(buildDoctor(context.Background(), app, "", false))
	if len(rows) != 1 || !strings.Contains(rows[0].Message.Error(), "is not using the live credential's account") {
		t.Fatalf("want the real home's differs warning, got %+v", rows)
	}
	want := "; to make it use the live account, run: CODEX_HOME='" + realHome + "' codex app-server daemon restart"
	if msg := rows[0].Message.Error(); !strings.HasSuffix(msg, want) {
		t.Errorf("message = %q, want suffix %q", msg, want)
	}
	if got := app.Env.Getenv("CODEX_HOME"); got != bound {
		t.Errorf("doctor must not mask the binding for its other checks: CODEX_HOME = %q", got)
	}
}

// The check runs no subprocess of the tool — the `daemon version` half stays
// off, even with codex on PATH — and connects only to the daemon's Unix socket.
// The fixture's credential is auth.json, so no keychain reader runs either and
// any subprocess at all is a finding here.
func TestResidentDriftRunsNoToolSubprocessAndDialsOnlyTheSocket(t *testing.T) {
	envRunnerCalls := 0
	savedRun, savedLaunch, savedInteractive := runner.RunWithEnv, runner.LaunchWithEnv, runner.RunInteractive
	t.Cleanup(func() {
		runner.RunWithEnv, runner.LaunchWithEnv, runner.RunInteractive = savedRun, savedLaunch, savedInteractive
	})
	runner.RunWithEnv = func(context.Context, []string, string, ...string) (string, string, int) {
		envRunnerCalls++
		return "", "", 1
	}
	runner.LaunchWithEnv = func(context.Context, []string, string, ...string) (int, error) {
		envRunnerCalls++
		return 1, nil
	}
	runner.RunInteractive = func(context.Context, []string, string, ...string) (int, error) {
		envRunnerCalls++
		return 1, nil
	}
	for _, answer := range []string{accountReadReply(probeOtherAcct), residentDaemonError, ""} {
		app, _, h := probeFixture(t, probeAccount)
		if answer != "" {
			linkSocket(t, app, h, startFakeDaemon(t, answer).socket)
		}
		app.Env.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
		var dials []string
		app.dialUnix = func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials = append(dials, network)
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		envRunnerCalls = 0
		fake := &runnertest.Fake{}
		runner.With(fake, func() { app.residentDriftChecks(context.Background(), "") })
		if fake.Name != "" || envRunnerCalls != 0 {
			t.Errorf("answer %q: ran %q %v and %d env-runner call(s)", answer, fake.Name, fake.Args, envRunnerCalls)
		}
		for _, network := range dials {
			if network != "unix" {
				t.Errorf("dialed %q", network)
			}
		}
		if answer != "" && len(dials) != 1 {
			t.Errorf("answer %q: %d dial(s), want 1", answer, len(dials))
		}
	}
}

// Neither account, the email or the plan reaches the message, the rendered
// Japanese text or the JSON report.
func TestResidentDriftPrintsNoPersonalData(t *testing.T) {
	for _, answer := range []string{accountReadReply(probeOtherAcct), residentDaemonError} {
		app, _, h := probeFixture(t, probeAccount)
		linkSocket(t, app, h, startFakeDaemon(t, answer).socket)
		_, stdout, stderr := captureBoth(t, func() int {
			return runDoctor(context.Background(), app, commonOpts{Format: formatJSON}, constants.ToolCodex)
		})
		var raw struct {
			Checks []struct{ Tool, Code, Status, Message string } `json:"checks"`
		}
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			t.Fatalf("doctor --json: %v\n%s", err, stdout)
		}
		found := false
		for _, c := range raw.Checks {
			if c.Code == constants.CheckResidentDrift {
				found = c.Tool == constants.ToolCodex && c.Status == constants.StatusWarn &&
					strings.HasSuffix(c.Message, "run: codex app-server daemon restart")
			}
		}
		if !found {
			t.Errorf("JSON lacks the codex resident_drift warning: %s", stdout)
		}
		rows := residentDriftRows(buildDoctor(context.Background(), app, constants.ToolCodex, false))
		texts := []string{stdout, stderr}
		for _, row := range rows {
			texts = append(texts, row.Message.Error(), l10n.Render(row.Message))
		}
		for _, out := range texts {
			for _, pii := range []string{probeEmail, probeAccount, probeOtherAcct, "plus"} {
				if strings.Contains(out, pii) {
					t.Errorf("output %q contains %q", out, pii)
				}
			}
		}
	}
}

// The two warnings render in Japanese while JSON keeps English.
func TestResidentDriftMessagesInJapanese(t *testing.T) {
	for _, tc := range []struct{ answer, ja string }{
		{accountReadReply(probeOtherAcct), "codex の管理デーモンは現在の認証情報のアカウントを使っておらず、デーモンに接続したセッションも同じです。現在のアカウントを使わせるには codex app-server daemon restart を実行してください。"},
		{residentDaemonError, "kae は codex の管理デーモンが使っているアカウントを読み取れないため、デーモンが現在のアカウントを使っているか判断できません。使っていない場合は codex app-server daemon restart を実行してください。"},
	} {
		app, _, h := probeFixture(t, probeAccount)
		linkSocket(t, app, h, startFakeDaemon(t, tc.answer).socket)
		rows := residentDriftRows(buildDoctor(context.Background(), app, constants.ToolCodex, false))
		if len(rows) != 1 {
			t.Fatalf("rows = %+v", rows)
		}
		english := rows[0].Message.Error()
		l10ntest.UseJapanese(t)
		if got := l10n.Render(rows[0].Message); got != tc.ja {
			t.Errorf("Render = %q, want %q", got, tc.ja)
		}
		if got := rows[0].Message.Error(); got != english {
			t.Errorf("Error() under Japanese = %q, must stay English %q", got, english)
		}
	}
}
