package l10n

// jaHelp renders `--help`, usage synopses and the flag descriptions, and what a
// flag parse failure prints (internal/cmd/flagusage.go).
var jaHelp = map[string]string{
	"flag provided but not defined: %s":   "定義されていないフラグです: %s",
	"flag needs an argument: %s":          "フラグに値が指定されていません: %s",
	"invalid value %q for flag %s: %s":    "フラグ %[2]s の値 %[1]q は無効です: %[3]s",
	"invalid boolean value %q for %s: %s": "フラグ %[2]s の真偽値 %[1]q は無効です: %[3]s",
	"Usage of %s:":                        "%s の使い方:",
	"(default %s)":                        "（既定値: %s）",
}
