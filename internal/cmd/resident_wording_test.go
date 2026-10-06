package cmd

import (
	"errors"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The warnings after the transaction, for each command, in English and in
// Japanese: a failed restart (restartFailed splices the cause and the kept result
// into one sentence, so each part's catalog entry is checked here), a restart kae
// left running (restart_pending), and the app still running at the quit deadline,
// which reads the same for every command (docs/CLI.md § kae use Semantics,
// steps 4 and 5).
func TestResidentWarningWording(t *testing.T) {
	const manual = "codex app-server daemon restart"
	ops := []struct {
		op           residentOp
		kept, keptJA string
	}{
		{residentOpSwitch, "the switch is kept", "切替はそのまま有効です"},
		{residentOpLogin, "kae add's result is kept", "kae add の結果はそのまま有効です"},
		{residentOpRollback, "kae rollback's result is kept", "kae rollback の結果はそのまま有効です"},
	}
	failed := func(cause string) func(kept string) string {
		return func(kept string) string {
			return "codex: " + cause + "; " + kept +
				", and the managed daemon may not be using the live account yet; to retry, run: " + manual
		}
	}
	failedJA := func(cause string) func(kept string) string {
		return func(kept string) string {
			return "codex: " + cause + "。" + kept +
				"が、管理デーモンは現在有効なアカウントをまだ使っていない可能性があります。再試行するには、" +
				manual + " を実行してください。"
		}
	}
	cases := []struct {
		name              string
		build             func(op residentOp) message
		english, japanese func(kept string) string
	}{
		{
			"not run", func(op residentOp) message { return op.restartNotRun(errors.New("boom"), manual) },
			failed("could not run codex app-server daemon restart (boom)"),
			failedJA("codex app-server daemon restart を実行できませんでした（boom）"),
		},
		{
			"exited", func(op residentOp) message { return op.restartExited(3, manual) },
			failed("codex app-server daemon restart failed (exit 3)"),
			failedJA("codex app-server daemon restart に失敗しました（終了コード 3）"),
		},
		{
			"pending", func(op residentOp) message { return op.restartPending(manual) },
			func(kept string) string {
				return "codex: codex app-server daemon restart did not finish in time, so kae left it running and stopped waiting; " +
					"it may be waiting for running tasks to finish before it restarts the managed daemon; " +
					kept + "; if the managed daemon does not restart, run: " + manual
			},
			func(kept string) string {
				return "codex: codex app-server daemon restart が時間内に終わらなかったため、kae は実行したままにして待つのをやめました。" +
					"実行中のタスクの終了を待ってから管理デーモンを再起動する可能性があります。" +
					kept + "。管理デーモンが再起動されない場合は、" + manual + " を実行してください。"
			},
		},
		{
			"quit timeout", func(residentOp) message { return desktopQuitTimeoutMessage() },
			func(string) string {
				return "codex: the ChatGPT app did not quit in time and kae left it running; " +
					"it may be asking you to confirm the quit, and kae will not reopen it, so open it yourself after it quits; " +
					"until then it keeps the codex account it started with"
			},
			func(string) string {
				return "codex: ChatGPT アプリが時間内に終了しなかったため、kae は起動したままにしました。" +
					"アプリが終了の確認を求めている可能性があり、kae は起動し直さないので、終了した後に手動で起動してください。" +
					"それまでは起動時の codex アカウントを使い続けます。"
			},
		},
	}
	// Error() stays English whatever the language; Render follows it.
	l10ntest.UseJapanese(t)
	for _, o := range ops {
		for _, c := range cases {
			m := c.build(o.op)
			if got, want := m.Error(), c.english(o.kept); got != want {
				t.Errorf("%s, %s: English = %q, want %q", o.kept, c.name, got, want)
			}
			if got, want := l10n.Render(m), c.japanese(o.keptJA); got != want {
				t.Errorf("%s, %s: Japanese = %q, want %q", o.kept, c.name, got, want)
			}
		}
	}
}
