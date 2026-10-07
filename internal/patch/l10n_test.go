package patch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The patch errors are message values: Error() keeps the English text, and a
// human sink renders the catalog's Japanese, nested kae causes included, while an
// external cause (an OS error) stays verbatim as the tail. A parse error is
// always one of the package's fixed reasons, never the parser's own text, which
// quotes the document.
func TestPatchErrorsRenderInJapanese(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(occupied, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := SyncFile
	t.Cleanup(func() { SyncFile = saved })
	injected := errors.New("injected sync failure")

	set := func(doc, pointer, value string) error {
		_, err := SetPointer([]byte(doc), pointer, json.RawMessage(value))
		return err
	}
	setJSONC := func(doc, pointer, value string) error {
		_, err := SetPointerJSONC([]byte(doc), pointer, json.RawMessage(value))
		return err
	}
	get := func(doc, pointer string) error {
		_, _, err := GetPointer([]byte(doc), pointer)
		return err
	}
	cases := []struct {
		name string
		err  func() error
		// english and ja are the kae text; an external cause follows both
		// verbatim, so the test compares the rest of the line as is.
		english, ja string
		external    bool
	}{
		{"create temp file", func() error {
			return WriteFileAtomic(filepath.Join(dir, "missing", "x"), nil, 0o600)
		}, "create temp file: ", "一時ファイルを作成できません: ", true},
		{"sync temp file", func() error {
			SyncFile = func(*os.File) error { return injected }
			defer func() { SyncFile = saved }()
			return WriteFileAtomic(filepath.Join(dir, "x"), nil, 0o600)
		}, "sync temp file: injected sync failure", "一時ファイルをディスクに同期できません: injected sync failure", false},
		{"rename temp file", func() error {
			return WriteFileAtomic(occupied, nil, 0o600)
		}, "rename temp file: ", "一時ファイルの名前を変更できません: ", true},
		{
			"not a directory", func() error { return MkdirAllDurable(file, 0o700) },
			"not a directory: " + file, "ディレクトリではありません: " + file, false,
		},
		{
			"trailing value", func() error { return get(`{} 1`, "/a") },
			"parse json: unexpected value after top-level value", "JSON を解析できません: 最上位の値の後に余分な値があります", false,
		},
		{
			"duplicate member", func() error { return set(`{"a":1,"a":2}`, "/a", `1`) },
			"parse json: duplicate object member", "JSON を解析できません: オブジェクトのメンバーが重複しています", false,
		},
		{
			"invalid pointer", func() error { return get(`{}`, "a") },
			`invalid json pointer "a"`, `JSON ポインター "a" が不正です`, false,
		},
		{
			"invalid escape", func() error { return get(`{}`, "/~2") },
			`invalid json pointer escape in "/~2"`, `JSON ポインター "/~2" のエスケープが不正です`, false,
		},
		{
			"pointer value", func() error { return set(`{}`, "/a", `{"x":1,"x":2}`) },
			`pointer value: parse json: duplicate object member`,
			`ポインターに設定する値が不正です: JSON を解析できません: オブジェクトのメンバーが重複しています`, false,
		},
		{
			"root not an object", func() error { return set(`[]`, "/a", `1`) },
			"document root is not a json object", "ドキュメントの最上位が JSON オブジェクトではありません", false,
		},
		{
			"missing parent", func() error { return setJSONC(`{}`, "/a/b", `1`) },
			"pointer /a/b parent does not exist", "ポインター /a/b の親が存在しません", false,
		},
		{
			"non-object parent", func() error { return set(`{"a":1}`, "/a/b", `1`) },
			"pointer /a/b traverses a non-object", "ポインター /a/b がオブジェクトでない値をたどっています", false,
		},
		{
			"jsonc syntax", func() error { return setJSONC(`{`, "/a", `1`) },
			"parse jsonc: the document ends before its value is complete", "JSONC を解析できません: ドキュメントが値の途中で終わっています", false,
		},
		{
			// encoding/json's own message would quote the first byte, 'S'.
			"json syntax", func() error { return get(`SYNTHSECRET`, "/a") },
			"parse json: the document has a syntax error", "JSON を解析できません: ドキュメントに構文エラーがあります", false,
		},
		{
			"json truncated", func() error { return get(`{"a":"SYNTH`, "/a") },
			"parse json: the document ends before its value is complete", "JSON を解析できません: ドキュメントが値の途中で終わっています", false,
		},
		{
			// hujson's own message would quote the whole literal.
			"jsonc literal", func() error { return setJSONC(`{"a":SYNTHSECRET}`, "/a", `1`) },
			"parse jsonc: the document has a syntax error", "JSONC を解析できません: ドキュメントに構文エラーがあります", false,
		},
		{
			"jsonc duplicate", func() error { return setJSONC(`{"a":1,"a":2}`, "/a", `1`) },
			`parse jsonc: parse json: duplicate object member`,
			`JSONC を解析できません: JSON を解析できません: オブジェクトのメンバーが重複しています`, false,
		},
		{
			"jsonc pointer value", func() error { return setJSONC(`{}`, "/a", `{"x":1,"x":2}`) },
			`pointer value: parse json: duplicate object member`,
			`ポインターに設定する値が不正です: JSON を解析できません: オブジェクトのメンバーが重複しています`, false,
		},
	}
	l10ntest.UseJapanese(t)
	for _, tc := range cases {
		l10ntest.ErrorText(t, tc.name, tc.err(), tc.english, tc.ja, tc.external)
	}
}
