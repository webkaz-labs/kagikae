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
	"internal/account/account.go":           {error: 3},
	"internal/adapter/adapter.go":           {error: 1},
	"internal/adapter/agy/agy.go":           {error: 3},
	"internal/adapter/claude/claude.go":     {error: 7},
	"internal/adapter/codex/codex.go":       {error: 10},
	"internal/adapter/copilot/copilot.go":   {error: 5},
	"internal/adapter/cursor/cursor.go":     {error: 4},
	"internal/adapter/opencode/opencode.go": {error: 3},
	"internal/artifact/artifact.go":         {error: 15},
	"internal/backup/backup.go":             {error: 3},
	"internal/cmd/account.go":               {sink: 4},
	"internal/cmd/app.go":                   {flag: 12},
	"internal/cmd/backup.go":                {sink: 3},
	"internal/cmd/cmd.go":                   {sink: 1},
	"internal/cmd/companion.go":             {sink: 4, print: 1},
	"internal/cmd/completion.go":            {sink: 2},
	"internal/cmd/completion_install.go":    {print: 16},
	"internal/cmd/dircred.go":               {error: 4},
	"internal/cmd/dircred_harvest.go":       {print: 1},
	"internal/cmd/dircred_identity.go":      {error: 6},
	"internal/cmd/dircred_store.go":         {error: 1},
	"internal/cmd/doctor.go":                {sink: 1},
	"internal/cmd/edit.go":                  {sink: 1},
	"internal/cmd/env.go":                   {sink: 4},
	"internal/cmd/error.go":                 {error: 2},
	"internal/cmd/flagspec.go":              {flag: 30},
	"internal/cmd/fragment.go":              {error: 5},
	"internal/cmd/init.go":                  {sink: 1},
	"internal/cmd/install.go":               {flag: 3},
	"internal/cmd/login.go":                 {sink: 1, print: 4, error: 5},
	"internal/cmd/lsplace.go":               {sink: 2, error: 1},
	"internal/cmd/mise_global.go":           {error: 1},
	"internal/cmd/miseinit.go":              {sink: 2, print: 2, error: 10},
	"internal/cmd/ops.go":                   {sink: 1, error: 8},
	"internal/cmd/pin.go":                   {sink: 2, error: 1},
	"internal/cmd/pinindex.go":              {error: 2},
	"internal/cmd/preservation.go":          {sink: 2, print: 3},
	"internal/cmd/profile.go":               {sink: 6},
	"internal/cmd/rebind.go":                {error: 2},
	"internal/cmd/relogin.go":               {sink: 3, print: 5},
	"internal/cmd/run.go":                   {sink: 2, print: 2, error: 6},
	"internal/cmd/status.go":                {sink: 2},
	"internal/cmd/switch.go":                {sink: 1, error: 1},
	"internal/cmd/uninstall.go":             {sink: 1, print: 3, flag: 1, error: 1},
	"internal/config/config.go":             {error: 21},
	"internal/config/writer.go":             {error: 2},
	"internal/envprofile/envprofile.go":     {error: 6},
	"internal/installation/image_darwin.go": {error: 2},
	"internal/installation/receipt.go":      {error: 2},
	"internal/keychain/keychain.go":         {error: 5},
	"internal/lock/lock.go":                 {error: 3},
	"internal/patch/atomic.go":              {error: 6},
	"internal/patch/durable.go":             {error: 2},
	"internal/patch/json_pointer.go":        {error: 15},
	"internal/patch/jsonc.go":               {error: 4},
	"internal/picker/picker.go":             {error: 3},
	"internal/secret/file.go":               {error: 1},
	"internal/secret/keychain.go":           {error: 3},
	"internal/secret/libsecret.go":          {error: 4},
	"internal/secret/secret.go":             {error: 7},
	"internal/state/state.go":               {error: 3},
}
