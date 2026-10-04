package l10n

// jaOther renders the human output no other area owns: prompts, confirmations
// and the picker's text.
var jaOther = map[string]string{
	// completion_install.go: the registration menu and the shell steps after it.
	"Register kae %s completion:":                                                       "kae の %s 補完の登録先:",
	"  1) completion file in the shell's standard dir (%s) [default]":                   "  1) シェルの標準ディレクトリの補完ファイル（%s）[既定]",
	"  2) global mise [hooks.enter] (opt-in, experimental)":                             "  2) グローバル mise の [hooks.enter]（明示的に選んだ場合のみ、実験的）",
	"  2) global mise [hooks.enter] (opt-in, experimental) — mise not detected on PATH": "  2) グローバル mise の [hooks.enter]（明示的に選んだ場合のみ、実験的。PATH に mise が見つかりません）",
	"  3) print the script only":                                                        "  3) スクリプトを表示するだけ",
	"Choice [1]: ":                                                                      "選択 [1]: ",
	"zsh: if completion does not update, rebuild the cache:\n%s":                        "zsh: 補完が更新されない場合は、キャッシュを作り直してください:\n%s",
	"Open a new shell. If completion does not appear, your zsh completion\ncache is stale — remove your compdump and rebuild it:\n%s":       "新しいシェルを開いてください。補完が出ない場合は zsh の補完キャッシュが古くなっています。\ncompdump を削除して作り直してください:\n%s",
	"Ensure this is on your fpath, e.g. add to ~/.zshrc:\n  fpath=(%s $fpath)\n  autoload -Uz compinit && compinit\nThen open a new shell.": "次の行が fpath に入っていることを確認してください（例: ~/.zshrc に追加する）:\n  fpath=(%s $fpath)\n  autoload -Uz compinit && compinit\nそのあと新しいシェルを開いてください。",
	"Open a new shell to load it.": "読み込むには新しいシェルを開いてください。",

	// internal/picker: the empty result, the footer and the empty filter's placeholder.
	"no matching place": "一致する場所がありません",
	"up/down move   enter choose   esc clear or cancel": "上下 移動   Enter 決定   Esc 絞り込み解除またはキャンセル",
	"type to filter": "入力して絞り込み",

	// uninstall.go.
	"Apply this exact removal plan? Type uninstall to confirm: ": "この削除計画をそのまま適用する場合は uninstall と入力してください: ",
	// preservation.go. The answer is the ID itself, in every language.
	"Preserved copy %s may be the only surviving credential copy. Permanently delete it? Type its ID to confirm: ": "保全したコピー %s は、残っている唯一の認証情報のコピーかもしれません。完全に削除する場合は、確認のためその ID を入力してください: ",
}
