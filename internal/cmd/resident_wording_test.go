package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// Every restart-failure warning, for each command and each way the restart
// fails, in English and in Japanese: restartFailed splices the cause and the kept
// result into one sentence, so each part's catalog entry is checked here.
func TestRestartFailureWording(t *testing.T) {
	const manual = "codex app-server daemon restart"
	ops := []struct {
		op           residentOp
		kept, keptJA string
	}{
		{residentOpSwitch, "the switch is kept", "切替はそのまま有効です"},
		{residentOpLogin, "kae add's result is kept", "kae add の結果はそのまま有効です"},
		{residentOpRollback, "kae rollback's result is kept", "kae rollback の結果はそのまま有効です"},
	}
	causes := []struct {
		name           string
		build          func(op residentOp) message
		cause, causeJA string
	}{
		{
			"timed out", func(op residentOp) message { return op.restartTimedOut(30*time.Second, manual) },
			"codex app-server daemon restart did not finish within 30s; it may be waiting for running tasks to finish, and the managed daemon may restart after they do",
			"codex app-server daemon restart が 30s 以内に終わりませんでした。実行中のタスクの終了を待っている可能性があり、その後に管理デーモンが再起動されることがあります",
		},
		{
			"not run", func(op residentOp) message { return op.restartNotRun(errors.New("boom"), manual) },
			"could not run codex app-server daemon restart (boom)", "codex app-server daemon restart を実行できませんでした（boom）",
		},
		{
			"exited", func(op residentOp) message { return op.restartExited(3, manual) },
			"codex app-server daemon restart failed (exit 3)", "codex app-server daemon restart に失敗しました（終了コード 3）",
		},
	}
	// Error() stays English whatever the language; Render follows it.
	l10ntest.UseJapanese(t)
	for _, o := range ops {
		for _, c := range causes {
			m := c.build(o.op)
			english := "codex: " + c.cause + "; " + o.kept +
				", and the managed daemon may not be using the live account yet; to retry, run: " + manual
			if got := m.Error(); got != english {
				t.Errorf("%s, %s: English = %q, want %q", o.kept, c.name, got, english)
			}
			japanese := "codex: " + c.causeJA + "。" + o.keptJA +
				"が、管理デーモンは現在有効なアカウントをまだ使っていない可能性があります。再試行するには、" +
				manual + " を実行してください。"
			if got := l10n.Render(m); got != japanese {
				t.Errorf("%s, %s: Japanese = %q, want %q", o.kept, c.name, got, japanese)
			}
		}
	}
}

// The quit-timeout warning, in English and in Japanese: an app still running at
// the deadline may be showing its quit confirmation dialog, and kae does not
// relaunch it once it quits (docs/CLI.md § kae use Semantics, step 5).
func TestQuitTimeoutWording(t *testing.T) {
	l10ntest.UseJapanese(t)
	m := desktopQuitTimeoutMessage()
	const english = "codex: the ChatGPT app did not quit in time and kae left it running; it may be asking you to confirm the quit; kae does not relaunch it, so after it quits, open it again yourself; until then it keeps the codex account it started with"
	if got := m.Error(); got != english {
		t.Errorf("English = %q, want %q", got, english)
	}
	const japanese = "codex: ChatGPT アプリが時間内に終了しなかったため、kae は起動したままにしました。アプリが終了の確認を求めている可能性があります。kae は起動し直さないので、終了した後にアプリを手動で起動してください。それまでは起動時の codex アカウントを使い続けます。"
	if got := l10n.Render(m); got != japanese {
		t.Errorf("Japanese = %q, want %q", got, japanese)
	}
}
