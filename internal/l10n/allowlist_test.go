package l10n

// machineOutput is a permanent allowlist: functions whose literal prints are
// machine lines, never localized (docs/CLI.md § Localization, "Machine lines"):
// generated files and blocks, the `# warning:` comments written into them, and
// the `export` lines printed for a shell to run. Keyed "file:function"; an entry
// that matches no literal print fails the test.
var machineOutput = map[string]bool{
	"internal/config/config.go:InitialContent":                 true, // config.toml kae init writes
	"internal/cmd/completion_install.go:renderMiseHookBlock":   true, // global mise hook block
	"internal/cmd/fragment.go:renderDirFragment":               true, // per-directory mise fragment
	"internal/cmd/fragment.go:ensureGitExcluded":               true, // info/exclude entry
	"internal/cmd/fragment.go:exportFallback":                  true, // export lines
	"internal/cmd/global_fragment.go:App.renderGlobalFragment": true, // global mise fragment
	"internal/cmd/global_fragment.go:App.globalExportFallback": true, // export lines
	"internal/cmd/miseinit.go:App.miseBlock":                   true, // .mise.toml block and tasks
	"internal/cmd/miseinit.go:writeEnvEntries":                 true, // [env] and its # warning: comments
}

// notLocalized is the permanent allowlist of errors kae never shows a person, so
// they are not messages and stay English (docs/CLI.md § Localization, "Where
// messages are composed"). Keyed "file" or "file:function", with the reason;
// an entry that matches no `fmt.Errorf` or `errors.New` call fails the test.
// Their calls do not count in unmigrated.
var notLocalized = map[string]string{
	"internal/preservation/store.go": "every error reaches a person only through cmd's preservationError, " +
		"which maps the sentinels to its own messages and never prints the rest: a backend error may carry " +
		"credential material, so translating one would imply it may be shown; drop this entry if cmd ever shows a store error other than through preservationError",
	"internal/companion/companion.go:Spec.validate": "called only by Register, which panics at init on a " +
		"programmer error in the companion registry",
}

// unmigrated is the second allowlist: per file, the human sinks, flag
// registrations and errors in internal/cmd or below whose text is not in the
// catalog yet. The test requires each count to equal the source, so the list only
// shrinks: migrating a call fails the test until its count is lowered, and a new
// unmigrated call fails it until its message is in the catalog. Done when empty
// (docs/ROADMAP.md, localization stage 5).
var unmigrated = map[string]pendingCounts{
	"internal/cmd/app.go":       {flag: 12},
	"internal/cmd/flagspec.go":  {flag: 30},
	"internal/cmd/install.go":   {flag: 3},
	"internal/cmd/uninstall.go": {flag: 1},
}
