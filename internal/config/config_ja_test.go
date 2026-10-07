package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// TestLoadErrorsRenderInJapanese pins each load and validation error twice: its
// English Error(), which JSON, doctor's JSON and the tests that match on it see,
// and its Japanese rendering on a human sink.
func TestLoadErrorsRenderInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	cases := []struct {
		name, content, en, ja string
	}{
		{
			"newer version", "version = 2\n",
			"config version 2 is newer than supported 1",
			"設定ファイルのバージョン 2 は、対応しているバージョン 1 より新しいです",
		},
		{
			"preservation bytes", "[security]\npreservation_max_bytes = 0\n",
			"security.preservation_max_bytes must be >= 1",
			"security.preservation_max_bytes は 1 以上にしてください",
		},
		{
			"backup keep", "[security]\nbackup_keep = 0\n",
			"security.backup_keep must be >= 1",
			"security.backup_keep は 1 以上にしてください",
		},
		{
			"unknown tool", "[tools.nope]\nenabled = true\n",
			`unknown tool "nope" in [tools]`,
			`[tools] に不明なツール "nope" があります`,
		},
		{
			"denylist path", "[tools.claude]\nshared_denylist_extra = [\"a/b\"]\n",
			`tools.claude.shared_denylist_extra item "a/b" is not a bare file name`,
			`tools.claude.shared_denylist_extra の項目 "a/b" は、ディレクトリを含まないファイル名ではありません`,
		},
		{
			"denylist hard-coded", "[tools.claude]\nshared_denylist_extra = [\".credentials.json\"]\n",
			`tools.claude.shared_denylist_extra: ".credentials.json" is already on the hard-coded denylist`,
			`tools.claude.shared_denylist_extra: ".credentials.json" は組み込みの除外リストにすでに含まれています`,
		},
		{
			"shared item path", "[tools.codex]\nisolated_shared_items = [\"..\"]\n",
			`tools.codex.isolated_shared_items item ".." is not a bare file name`,
			`tools.codex.isolated_shared_items の項目 ".." は、ディレクトリを含まないファイル名ではありません`,
		},
		{
			"shared identity cache", "[tools.claude]\nisolated_shared_items = [\".claude.json\"]\n",
			`tools.claude.isolated_shared_items must not share the identity cache ".claude.json"; remove it — ` +
				"kae keeps that file private to the directory so it can be a different account than the real home",
			`tools.claude.isolated_shared_items でログイン中アカウントの記録（".claude.json"）は共有できません。` +
				"kae はこのファイルをディレクトリ専用に保ち、実ホームとは別のアカウントにできるようにしているため、この項目を取り除いてください",
		},
		{
			"shared credential", "[tools.codex]\nisolated_shared_items = [\"auth.json\"]\n",
			`tools.codex.isolated_shared_items must not share the auth credential "auth.json"; remove it — ` +
				"kae keeps that file private to the directory so it can be a different account than the real home",
			`tools.codex.isolated_shared_items で認証情報（"auth.json"）は共有できません。` +
				"kae はこのファイルをディレクトリ専用に保ち、実ホームとは別のアカウントにできるようにしているため、この項目を取り除いてください",
		},
		{
			"driver tool", "[tools.codex]\ndriver = \"file\"\n",
			"tools.codex.driver is only valid for claude",
			"tools.codex.driver は claude にだけ指定できます",
		},
		{
			"driver value", "[tools.claude]\ndriver = \"x\"\n",
			`tools.claude.driver "x" is invalid (only "file" is supported)`,
			`tools.claude.driver の値 "x" は不正です（対応しているのは "file" だけです）`,
		},
		{
			"profile name", "[profiles.\"bad name\"]\nlabel = \"x\"\n",
			`invalid profile name "bad name"`,
			`プロファイル名が不正です: "bad name"`,
		},
		{
			"profile tool", "[profiles.main.accounts]\nnope = \"main\"\n",
			`profile "main" maps unknown tool "nope"`,
			`プロファイル "main" が不明なツール "nope" を割り当てています`,
		},
		{
			"profile account", "[profiles.main.accounts]\nclaude = \"bad name\"\n",
			`profile "main" maps tool "claude" to invalid account name "bad name"`,
			`プロファイル "main" がツール "claude" に不正なアカウント名 "bad name" を割り当てています`,
		},
		{
			"profile companion", "[profiles.main.companions]\nnope.X = \"y\"\n",
			`profile "main" maps unknown companion "nope"`,
			`プロファイル "main" が不明な周辺ツール "nope" を割り当てています`,
		},
		{
			"knob name", "[profiles.main.companions]\ngit.\"bad knob\" = \"y\"\n",
			`profile "main" companion "git" has invalid knob name "bad knob"`,
			`プロファイル "main" の周辺ツール "git" に不正な設定項目名 "bad knob" があります`,
		},
		{
			"knob value", "[profiles.main.companions]\ngit.name = \"a\\nb\"\n",
			`profile "main" companion "git" knob "name" value has a newline or NUL`,
			`プロファイル "main" の周辺ツール "git" の設定項目 "name" の値に改行か NUL が含まれています`,
		},
		{
			"default profile", "default_profile = \"side\"\n",
			`default_profile "side" is not defined under [profiles]`,
			`default_profile に指定した "side" は [profiles] に定義されていません`,
		},
		{
			"renamed key", "[tools.claude]\nbond_denylist_extra = [\"x\"]\n",
			`config key "tools.claude.bond_denylist_extra" was renamed to "shared_denylist_extra" in v0.8.0 (pre-1.0 hard break; rename it)`,
			`設定ファイルのキー "tools.claude.bond_denylist_extra" は v0.8.0 で "shared_denylist_extra" に名前が変わりました（1.0 より前の互換性のない変更です）。キーの名前を変えてください`,
		},
		{
			"removed key", "[tools.claude]\nhome_mode_enabled = false\n",
			`config key "tools.claude.home_mode_enabled" was removed in v0.8.0; to bind directories instead, run: kae pin -s|-i`,
			`設定ファイルのキー "tools.claude.home_mode_enabled" は v0.8.0 で削除されました。代わりにディレクトリを固定する場合は kae pin -s|-i を実行してください`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Load(writeConfig(t, tc.content))
			if err == nil {
				t.Fatal("expected a load error")
			}
			l10ntest.ErrorText(t, tc.name, err, tc.en, tc.ja, false)
		})
	}
}

// TestLoadCauseErrorsKeepTheirCause pins the wrappers whose cause is an external
// error: the kae prose is Japanese, the cause stays verbatim English, and
// errors.Is still reaches it.
func TestLoadCauseErrorsKeepTheirCause(t *testing.T) {
	l10ntest.UseJapanese(t)
	t.Run("read", func(t *testing.T) {
		_, _, err := Load(t.TempDir()) // a directory cannot be read as a file
		if err == nil {
			t.Fatal("expected a read error")
		}
		var cause *fs.PathError
		if !errors.As(err, &cause) {
			t.Fatalf("the OS cause is not reachable: %v", err)
		}
		if !strings.HasSuffix(err.Error(), cause.Error()) {
			t.Errorf("English %q does not end in the OS cause %q", err.Error(), cause.Error())
		}
		l10ntest.ErrorText(t, "read", err, "read config: ", "設定ファイルを読み取れません: ", true)
	})
	t.Run("parse", func(t *testing.T) {
		_, _, err := Load(writeConfig(t, "version = \n"))
		if err == nil {
			t.Fatal("expected a parse error")
		}
		l10ntest.ErrorText(t, "parse", err, "parse config: the document has a syntax error at line 1", "設定ファイルを解析できません: ドキュメントの 1 行目に構文エラーがあります", false)
	})
	t.Run("editor", func(t *testing.T) {
		_, err := NewEditor([]byte("[profiles\n"))
		if err == nil {
			t.Fatal("expected a parse error")
		}
		l10ntest.ErrorText(t, "editor", err, "parse config for editing: the document has an invalid value or syntax", "編集する設定ファイルを解析できません: ドキュメントの値または構文が不正です", false)
	})
}

// TestPrivateBindKindsAreMessages fails when constants.PrivateBindItems gains a
// kind privateBindKindMessage does not carry as a message, which a Japanese
// line would print in English.
func TestPrivateBindKindsAreMessages(t *testing.T) {
	for _, item := range constants.PrivateBindItems {
		if _, ok := privateBindKindMessage(item.Kind).(l10n.Msg); !ok {
			t.Errorf("kind %q of %s is not a message", item.Kind, item.Name)
		}
	}
}
