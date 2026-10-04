package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/picker"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The candidate list's reason is a message, localized; the candidate lines are
// commands and stay verbatim. These tests select Japanese process-wide, so none
// of them may call t.Parallel.

func TestCandidateReasonsAreLocalizedAndLinesVerbatim(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(cwd, ".claude"), filepath.Join(cwd, ".codex"), filepath.Join(cwd, "a", ".claude"), filepath.Join(cwd, "b", ".claude"),
		app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir)
	l10ntest.UseJapanese(t)
	for _, tc := range []struct {
		name string
		verb string
		f    lsFlags
		pos  []string
		want []string
	}{
		{"no target", "open", lsFlags{}, nil, []string{
			"kae open には対象が必要です。1 つ選んでください:\n  kae open ",
			"\n  kae open kae --at 2  " + app.Paths.DataDir + "\n",
		}},
		{"several below", "cd", lsFlags{below: true}, []string{"claude"}, []string{
			"kae cd claude --below に一致する場所が 2 件あります。1 つ選んでください:\n" +
				"  kae cd claude --at 4  " + filepath.Join(cwd, "a", ".claude") + "\n" +
				"  kae cd claude --at 5  " + filepath.Join(cwd, "b", ".claude") + "\n",
		}},
		{"level without a bound tool", "cd", lsFlags{project: true}, nil, []string{
			"kae cd --project にはツールが必要です: ここで固定しているツールはありません。1 つ選んでください:\n",
			"  kae cd codex --at 2  " + filepath.Join(cwd, ".codex") + "\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := cdPathOrOpen(t, app, tc.verb, navRequest(t, tc.verb, tc.f, tc.pos...))
			if code != constants.ExitUsage || stdout != "" {
				t.Fatalf("exit %d stdout %q:\n%s", code, stdout, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}
	// --pick with no terminal: the count chooses the English wording only.
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{pick: true}, "kae"))
	if code != constants.ExitUsage || !strings.Contains(stderr, "kae cd kae --pick がピッカーを開くには端末が必要です。対象の場所は 3 件です。1 つ選んでください:\n") {
		t.Fatalf("--pick without a terminal = %d %q", code, stderr)
	}
}

// The not_found of an empty set keeps its exit code in both languages.
func TestEmptyCandidateSetIsLocalizedNotFound(t *testing.T) {
	run := func(t *testing.T) (int, string) {
		app := testApp(t, nil)
		chdirTo(t, t.TempDir()) // none of kae's three directories exists
		code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{pick: true}, "kae"))
		return code, stderr
	}
	code, stderr := run(t)
	if code != constants.ExitNotFound || stderr != "kae: kae cd kae --pick lists 3 places, and no existing place to choose\n" {
		t.Fatalf("english = %d %q", code, stderr)
	}
	l10ntest.UseJapanese(t)
	code, stderr = run(t)
	if code != constants.ExitNotFound || stderr != "kae: kae cd kae --pick に該当する場所は 3 件ですが、存在していて選べる場所はありません\n" {
		t.Fatalf("japanese = %d %q", code, stderr)
	}
}

// A picker failure is an external error: quoted verbatim after the prefix, exit 1.
func TestPickerFailureIsQuotedVerbatim(t *testing.T) {
	app := testApp(t, nil)
	cwd := chdirTo(t, t.TempDir())
	mkdirs(t, filepath.Join(cwd, ".claude"), app.Paths.ConfigDir, app.Paths.DataDir, app.Paths.StateDir)
	stub := withPicker(app)
	stub.choose = func([]picker.Item) (string, bool, error) {
		return "", false, os.ErrDeadlineExceeded
	}
	l10ntest.UseJapanese(t)
	code, _, stderr := cdPath(t, app, navRequest(t, "cd", lsFlags{}))
	if code != constants.ExitError || stderr != "kae: "+os.ErrDeadlineExceeded.Error()+"\n" {
		t.Fatalf("picker failure = %d %q", code, stderr)
	}
}

// The listing issues end on a remedy that follows the language; the code and the
// hashed entry are tokens.
func TestListIssueGuidanceIsLocalized(t *testing.T) {
	d := listDiagnostics{Complete: true, Issues: []listIssue{}, Warnings: []string{}}
	d.add("backups/x", constants.ListIssueRead)
	entry := d.Issues[0].Entry
	run := func(t *testing.T) string {
		_, stderr := captureStderr(t, func() int { d.print(); return 0 })
		return stderr
	}
	en := "kae: metadata listing is incomplete; readable records are shown\n" +
		"kae: " + constants.ListIssueRead + " " + entry + "; check metadata file and parent-directory permissions; keep the entry while investigating\n"
	if got := run(t); got != en {
		t.Errorf("english:\n got %q\nwant %q", got, en)
	}
	l10ntest.UseJapanese(t)
	ja := "kae: メタデータの一覧が不完全です。読み取れた記録だけを表示しています。\n" +
		"kae: " + constants.ListIssueRead + " " + entry + "。メタデータのファイルと親ディレクトリの権限を確認してください。調べている間はその項目を残してください。\n"
	if got := run(t); got != ja {
		t.Errorf("japanese:\n got %q\nwant %q", got, ja)
	}
	for _, code := range []string{constants.ListIssueEnumeration, constants.ListIssueInvalid, constants.ListIssueEntry, "unknown"} {
		if got := listIssueGuidance(code); got.Error() == "" || strings.ContainsAny(got.Error(), "。、") {
			t.Errorf("guidance for %s: English %q", code, got.Error())
		}
	}
}
