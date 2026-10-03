// Package l10n selects the language of kae's human output and renders kae's
// English message formats in it. docs/CLI.md § Localization is the contract and
// docs/VALIDATION.md § Output language in tests how it is tested.
//
// English is normative: a message is written at its call site as an English
// format string, and the Japanese catalog maps that exact string to its
// Japanese rendering. A format the catalog lacks renders in English.
package l10n

import (
	"strings"
	"sync/atomic"
)

// Language is the language kae writes its human output in.
type Language int32

const (
	English Language = iota
	Japanese
)

// EnvVar forces the language whatever the locale (`en` or `ja`).
const EnvVar = "KAE_LANG"

// selectionVars are consulted in this order; the first one set to a non-empty
// value decides, even when it selects English.
var selectionVars = []string{EnvVar, "LC_ALL", "LC_MESSAGES", "LANG"}

// SelectionVars returns the variables that select the language, in precedence
// order. Test entrypoints clear exactly these to pin English.
func SelectionVars() []string { return append([]string(nil), selectionVars...) }

// Detect returns the language the environment selects. getenv is os.Getenv in
// production; tests pass their own.
func Detect(getenv func(string) string) Language {
	for _, name := range selectionVars {
		if value := getenv(name); value != "" {
			return languageOf(value)
		}
	}
	return English
}

// languageOf maps one variable's value to a language: `ja`, or a locale name
// starting `ja_`, `ja-`, `ja.` or `ja@`, compared case-sensitively.
func languageOf(value string) Language {
	if value == "ja" {
		return Japanese
	}
	for _, prefix := range []string{"ja_", "ja-", "ja.", "ja@"} {
		if strings.HasPrefix(value, prefix) {
			return Japanese
		}
	}
	return English
}

// current is the process's language. It is English until Select or Set runs, so
// code that renders before the command line is seen renders English.
var current atomic.Int32

// Select decides the process's language once, at the start of the command line:
// English in JSON mode (nothing a JSON-mode process writes is localized),
// otherwise what the environment selects.
func Select(jsonMode bool, getenv func(string) string) Language {
	lang := English
	if !jsonMode {
		lang = Detect(getenv)
	}
	Set(lang)
	return lang
}

// Set fixes the process's language. Production code calls Select; tests use Set
// to select Japanese explicitly and to restore English afterwards.
func Set(lang Language) { current.Store(int32(lang)) }

// Current returns the process's language.
func Current() Language { return Language(current.Load()) }
