package l10n

// jaHelp renders `--help`, usage synopses and what a flag parse failure prints
// (internal/cmd/flagusage.go); the flag descriptions are jaFlags.
var jaHelp = map[string]string{
	"flag provided but not defined: %s":   "定義されていないフラグです: %s",
	"flag needs an argument: %s":          "フラグに値が指定されていません: %s",
	"invalid value %q for flag %s: %s":    "フラグ %[2]s の値 %[1]q は無効です: %[3]s",
	"invalid boolean value %q for %s: %s": "フラグ %[2]s の真偽値 %[1]q は無効です: %[3]s",
	"Usage of %s:":                        "%s の使い方:",
	"usage: %s":                           "使い方: %s",
	"usage: %s companion add <profile> <id> KEY=VALUE... (or one bare KEY for a token, value on stdin)": "使い方: %s companion add <profile> <id> KEY=VALUE... （トークンは KEY を 1 つだけ指定し、値は標準入力から渡します）",
	"usage: %s env set <tool> <account> KEY=VALUE... (or one bare KEY with the value on stdin)":         "使い方: %s env set <tool> <account> KEY=VALUE... （KEY を 1 つだけ指定し、値は標準入力から渡すこともできます）",
	"(default %s)": "（既定値: %s）",
}
