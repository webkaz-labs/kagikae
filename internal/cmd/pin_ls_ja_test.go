package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The pin, unpin, rebind and ls reports under the real Japanese catalog.
// Machine tokens (mode, tool:account, paths) stay verbatim inside the Japanese
// sentences and the JSON stays English.

func TestPinUnpinRebindReportsInJapanese(t *testing.T) {
	app := overlayTestApp(t)
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	chdirTemp(t)
	l10ntest.UseJapanese(t)
	opts := commonOpts{Format: formatText}

	code, out = captureStdout(t, func() int {
		return runPin(context.Background(), app, opts, "main", modeShared, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{
		"このディレクトリを固定しました: プロファイル main（shared）",
		"を書き込みました",
		"mise.toml は変更していません。",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pin output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Bound this directory") || strings.Contains(out, "left unchanged") {
		t.Errorf("pin output has English prose under Japanese:\n%s", out)
	}

	code, out = captureStdout(t, func() int {
		return runRebind(context.Background(), app, opts, constants.ToolClaude, "main", false)
	})
	mustExit(t, constants.ExitOK, code, out)
	if want := "claude をアカウント main に固定し直しました（shared。セッションと設定は変更していません）"; !strings.Contains(out, want) {
		t.Errorf("rebind output missing %q:\n%s", want, out)
	}

	code, out = captureStdout(t, func() int { return runUnpin(context.Background(), app, opts, false) })
	mustExit(t, constants.ExitOK, code, out)
	if want := "を削除しました"; !strings.Contains(out, want) || strings.Contains(out, "Removed") {
		t.Errorf("unpin output is not Japanese:\n%s", out)
	}
}

// The constant formats keep the English bytes the plural-by-argument code wrote.
func TestStoreLinkReportsKeepEnglishBytes(t *testing.T) {
	app := testApp(t, nil)
	_, out := captureStdout(t, func() int {
		app.reportStoreLinks([]string{".config/claude"}, nil, "")
		app.reportStoreLinks([]string{".config/claude", ".config/codex"}, []string{".config/agy"}, "")
		app.reportStoreLinks(nil, []string{".config/a", ".config/b"}, "")
		return 0
	})
	want := "Linked .config/claude to this directory's store.\n" +
		"Linked .config/claude, .config/codex to this directory's stores.\n" +
		"Removed the store link .config/agy.\n" +
		"Removed the store links .config/a, .config/b.\n"
	if out != want {
		t.Errorf("store link report =\n%s\nwant\n%s", out, want)
	}
}

func TestStoreLinkReportsInJapanese(t *testing.T) {
	app := testApp(t, nil)
	l10ntest.UseJapanese(t)
	_, out := captureStdout(t, func() int {
		app.reportStoreLinks([]string{".config/claude"}, nil, "")
		app.reportStoreLinks([]string{".config/claude", ".config/codex"}, []string{".config/agy"}, "")
		app.reportStoreLinks(nil, []string{".config/a", ".config/b"}, "")
		return 0
	})
	want := ".config/claude をこのディレクトリのストアにリンクしました。\n" +
		".config/claude, .config/codex をこのディレクトリのストアにリンクしました。\n" +
		"ストアへのリンク .config/agy を削除しました。\n" +
		"ストアへのリンク .config/a, .config/b を削除しました。\n"
	if out != want {
		t.Errorf("store link report =\n%s\nwant\n%s", out, want)
	}
}

func TestPrunedAndHandoffReportsInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	_, out := captureStdout(t, func() int {
		reportPruned([]message{
			msgf("Removed the superseded per-directory %s credential (%s)", "claude", "/work/main-app"),
		})
		return 0
	})
	if want := "置き換え済みの、ディレクトリごとの claude の認証情報を削除しました（/work/main-app）\n"; out != want {
		t.Errorf("pruned report = %q, want %q", out, want)
	}
	app := testApp(t, map[string]string{"MISE_SHELL": "zsh"})
	_, out = captureStdout(t, func() int {
		app.reportMiseHandoff(func() string { return "export X=1\n" })
		return 0
	})
	if want := "mise が次のプロンプトで反映します。今すぐ bash か zsh に反映する場合は、eval \"$(mise env)\" を実行してください\n"; out != want {
		t.Errorf("handoff = %q, want %q", out, want)
	}
}

func TestLsReportsInJapanese(t *testing.T) {
	app := overlayTestApp(t)
	withTerminalColumns(t, 0)
	chdirTemp(t)
	l10ntest.UseJapanese(t)

	_, out := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatText}) })
	if want := "固定したディレクトリ: なし。kae pin <profile> を実行してください\n"; out != want {
		t.Errorf("empty ls --pins = %q, want %q", out, want)
	}

	code, out := captureStdout(t, func() int {
		return runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeIsolated, false)
	})
	mustExit(t, constants.ExitOK, code, out)
	_, out = captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatText}) })
	header := strings.Split(out, "\n")[1]
	for _, want := range []string{"ディレクトリ", "現在", "プロファイル", "モード", "アカウント"} {
		if !strings.Contains(header, want) {
			t.Errorf("ls --pins header missing %q: %s", want, header)
		}
	}
	for _, want := range []string{"固定したディレクトリ:", "claude:main", "isolated"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls --pins missing %q:\n%s", want, out)
		}
	}

	// JSON keeps its English bytes whatever the language.
	_, js := captureStdout(t, func() int { return runLsPins(app, commonOpts{Format: formatJSON}) })
	if !json.Valid([]byte(js)) || strings.Contains(js, "固定") || !strings.Contains(js, "bound_directories") {
		t.Errorf("ls --pins --json is not plain English JSON: %s", js)
	}

	// The profile list and the empty account list.
	_, out = captureStdout(t, func() int {
		printProfileList([]profileStatus{{Name: "main", Accounts: map[string]string{"claude": "main"}, Active: true}})
		printProfileList(nil)
		printAccountItems(app, nil, "kae add <tool>", commonOpts{Format: formatText})
		return 0
	})
	for _, want := range []string{
		"プロファイル:\n  main", "claude:main", "（有効）",
		"プロファイル: 未定義。kae edit を実行してください",
		"アカウント: なし。kae add <tool> を実行してください",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("profile/account output missing %q:\n%s", want, out)
		}
	}
}

func TestLsPlaceReportsInJapanese(t *testing.T) {
	app := testApp(t, nil)
	withTerminalColumns(t, 0)
	l10ntest.UseJapanese(t)

	_, out := captureStdout(t, func() int {
		printToolPlaces(app, "agy", nil, false)
		printRepoPlaces(app, nil, false)
		return 0
	})
	want := "agy の場所: なし（kae が場所を解決するのは claude と codex だけです）\n" +
		"リポジトリ: なし（現在のディレクトリは Git リポジトリの中にありません）\n"
	if out != want {
		t.Errorf("empty place reports =\n%s\nwant\n%s", out, want)
	}

	rows := []placeRow{{Number: 1, Kind: "user", Path: "/home/x/.claude", Exists: false, InEffect: true, Source: "env", Mode: "isolated", Account: "main"}}
	_, out = captureStdout(t, func() int {
		printToolPlaces(app, "claude", rows, false)
		printRepoPlaces(app, rows, false)
		printKaePlaces(app, rows, false)
		return 0
	})
	// The Account header is columnHeader's (the status slice words it), so it is
	// not asserted here.
	header := strings.Join(strings.Fields(strings.Split(out, "\n")[1]), " ")
	if want := "# レベル パス 有効 取得元 モード"; !strings.HasPrefix(header, want) || !strings.HasSuffix(header, " 適用先") {
		t.Errorf("tool places header = %q, want %q ... 適用先", header, want)
	}
	for _, want := range []string{"claude の場所:", "(missing)", "リポジトリ:", "ルート", "kae のディレクトリ:", "種別"} {
		if !strings.Contains(out, want) {
			t.Errorf("place tables missing %q:\n%s", want, out)
		}
	}
}
