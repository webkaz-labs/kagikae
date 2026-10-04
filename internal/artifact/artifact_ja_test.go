package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// Every error the primitives build is kae's message: Japanese for a person, its
// English text unchanged for JSON and errors.Is (ErrUnsafe still selects exit code
// 10). An OS or patch cause stays verbatim inside it. None of these cases reaches
// a runner: the guards refuse before the keychain is touched.
func TestArtifactErrorsRenderInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	ctx := context.Background()
	// ApplyLive resolves a JSON pointer target's links before it names the target,
	// so the fixtures live under the resolved temporary directory.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// A target that is a directory fails to read; a non-empty one fails to remove.
	asDir := filepath.Join(dir, "as-dir")
	if err := os.MkdirAll(filepath.Join(asDir, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A regular file where a parent directory belongs makes MkdirAll fail.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	underFile := filepath.Join(blocker, "sub", "auth.json")
	// Two links that point at each other never resolve.
	loopA, loopB := filepath.Join(dir, "loop-a"), filepath.Join(dir, "loop-b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	_, loopErr := filepath.EvalSymlinks(loopA)
	// A document that is not a JSON object fails the patch primitives, whose own
	// error is the cause.
	notObject := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(notObject, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, getErr := patch.GetPointer([]byte(`{`), "/oauthAccount")
	_, setErr := patch.SetPointer([]byte(`{`), "/oauthAccount", []byte(`{}`))
	if getErr == nil || setErr == nil || loopErr == nil {
		t.Fatalf("fixtures did not fail: %v, %v, %v", getErr, setErr, loopErr)
	}

	keychainSpec := Spec{Name: "x", Kind: constants.KindKeychain, Target: "Example Service"}
	matchAccount := keychainSpec
	matchAccount.KeychainMatchAccount = true
	pointerSpec := keychainSpec
	pointerSpec.Pointer = "/claudeAiOauth"

	cases := []struct {
		name   string
		err    error
		unsafe bool
		en, ja string // "$CAUSE" stands for the text of the one cause the error wraps
	}{
		{
			name:   "keychain record without account",
			err:    ApplyLive(ctx, matchAccount, Value{Data: []byte("t"), Present: true}),
			unsafe: true,
			en: `unsafe operation refused: keychain item "Example Service" is identified by service and account,` +
				` but this record carries no account; refusing to touch the service as a whole`,
			ja: `安全でない操作を拒否しました: キーチェーン項目 "Example Service" はサービスとアカウントで識別されますが、` +
				`この記録にはアカウントがありません。サービス全体には触れません`,
		},
		{
			name:   "empty keychain payload",
			err:    ApplyLive(ctx, keychainSpec, Value{Data: []byte(""), Present: true}),
			unsafe: true,
			en:     `unsafe operation refused: keychain item "Example Service" payload is empty`,
			ja:     `安全でない操作を拒否しました: キーチェーン項目 "Example Service" の内容が空です`,
		},
		{
			name:   "multi-line keychain payload",
			err:    ApplyLive(ctx, keychainSpec, Value{Data: []byte("a\nb"), Present: true}),
			unsafe: true,
			en:     `unsafe operation refused: keychain item "Example Service" payload is not a single line`,
			ja:     `安全でない操作を拒否しました: キーチェーン項目 "Example Service" の内容が 1 行ではありません`,
		},
		{
			name:   "keychain payload without the pointer",
			err:    ApplyLive(ctx, pointerSpec, Value{Data: []byte(`{}`), Present: true}),
			unsafe: true,
			en:     `unsafe operation refused: keychain item "Example Service" payload is not the expected JSON shape`,
			ja:     `安全でない操作を拒否しました: キーチェーン項目 "Example Service" の内容が想定した JSON の形ではありません`,
		},
		{
			name: "document is not a JSON object",
			err: func() error {
				_, err := ReadLive(ctx, Spec{Kind: constants.KindJSONPointer, Target: notObject, Pointer: "/oauthAccount"})
				return err
			}(),
			unsafe: true,
			en:     "unsafe operation refused: " + notObject + " is not a JSON object (" + getErr.Error() + ")",
			ja:     "安全でない操作を拒否しました: " + notObject + " が JSON オブジェクトではありません（" + l10n.Render(getErr) + "）",
		},
		{
			name:   "rewrite refused",
			err:    ApplyLive(ctx, Spec{Kind: constants.KindJSONPointer, Target: notObject, Pointer: "/oauthAccount"}, Value{Data: []byte(`{}`), Present: true}),
			unsafe: true,
			en:     "unsafe operation refused: refusing to rewrite " + notObject + " (" + setErr.Error() + ")",
			ja:     "安全でない操作を拒否しました: " + notObject + " を書き換えません（" + l10n.Render(setErr) + "）",
		},
		{
			name:   "unresolvable symlink",
			err:    ApplyLive(ctx, Spec{Kind: constants.KindJSONPointer, Target: loopA, Pointer: "/oauthAccount"}, Value{Data: []byte(`{}`), Present: true}),
			unsafe: true,
			en:     "unsafe operation refused: refusing to touch " + loopA + " (unresolvable symlink: " + loopErr.Error() + ")",
			ja:     "安全でない操作を拒否しました: " + loopA + " には触れません（解決できない symlink: " + loopErr.Error() + "）",
		},
		{
			name: "unknown kind on read",
			err: func() error {
				_, err := ReadLive(ctx, Spec{Kind: "bogus"})
				return err
			}(),
			en: `unknown artifact kind "bogus"`,
			ja: `不明な認証要素の種類です: "bogus"`,
		},
		{
			name: "unknown kind on apply",
			err:  ApplyLive(ctx, Spec{Kind: "bogus"}, Value{}),
			en:   `unknown artifact kind "bogus"`,
			ja:   `不明な認証要素の種類です: "bogus"`,
		},
		{
			name: "file unreadable",
			err: func() error {
				_, err := ReadLive(ctx, Spec{Kind: constants.KindFile, Target: asDir})
				return err
			}(),
			en: "read " + asDir + ": $CAUSE",
			ja: asDir + " を読み取れません: $CAUSE",
		},
		{
			name: "document unreadable on read",
			err: func() error {
				_, err := ReadLive(ctx, Spec{Kind: constants.KindJSONPointer, Target: asDir, Pointer: "/x"})
				return err
			}(),
			en: "read " + asDir + ": $CAUSE",
			ja: asDir + " を読み取れません: $CAUSE",
		},
		{
			name: "document unreadable on apply",
			err:  ApplyLive(ctx, Spec{Kind: constants.KindJSONPointer, Target: asDir, Pointer: "/x"}, Value{Data: []byte(`{}`), Present: true}),
			en:   "read " + asDir + ": $CAUSE",
			ja:   asDir + " を読み取れません: $CAUSE",
		},
		{
			name: "file not removable",
			err:  ApplyLive(ctx, Spec{Kind: constants.KindFile, Target: asDir}, Value{}),
			en:   "remove " + asDir + ": $CAUSE",
			ja:   asDir + " を削除できません: $CAUSE",
		},
		{
			name: "file directory not creatable",
			err:  ApplyLive(ctx, Spec{Kind: constants.KindFile, Target: underFile}, Value{Data: []byte(`{}`), Present: true}),
			en:   "create dir for " + underFile + ": $CAUSE",
			ja:   underFile + " のディレクトリを作成できません: $CAUSE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("expected an error")
			}
			en, ja := tc.en, tc.ja
			if strings.Contains(en, "$CAUSE") {
				cause := l10ntest.WrappedCause(t, tc.err)
				en, ja = strings.ReplaceAll(en, "$CAUSE", cause.Error()), strings.ReplaceAll(ja, "$CAUSE", l10n.Render(cause))
			}
			if got := tc.err.Error(); got != en {
				t.Errorf("Error() = %q, want %q", got, en)
			}
			if got := l10n.Render(tc.err); got != ja {
				t.Errorf("Render = %q, want %q", got, ja)
			}
			if got := errors.Is(tc.err, ErrUnsafe); got != tc.unsafe {
				t.Errorf("errors.Is(err, ErrUnsafe) = %v, want %v", got, tc.unsafe)
			}
		})
	}
}
