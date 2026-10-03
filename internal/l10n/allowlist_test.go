package l10n

// machineOutput is the permanent allowlist: functions whose literal prints are
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

// unmigrated is the second allowlist: per file, the human sinks, flag
// registrations and errors below internal/cmd whose text is not in the catalog
// yet. The test requires each count to equal the source, so the list only
// shrinks: migrating a call fails the test until its count is lowered, and a new
// unmigrated call fails it until its message is in the catalog. Done when empty
// (docs/ROADMAP.md, localization stage 5).
var unmigrated = map[string]pendingCounts{
	"internal/account/account.go":           {error: 3},
	"internal/adapter/adapter.go":           {error: 2},
	"internal/adapter/agy/agy.go":           {error: 3},
	"internal/adapter/claude/claude.go":     {error: 7},
	"internal/adapter/codex/codex.go":       {error: 10},
	"internal/adapter/copilot/copilot.go":   {error: 5},
	"internal/adapter/cursor/cursor.go":     {error: 4},
	"internal/adapter/opencode/opencode.go": {error: 3},
	"internal/artifact/artifact.go":         {error: 16},
	"internal/backup/backup.go":             {error: 3},
	"internal/cmd/account.go":               {sink: 17, print: 7},
	"internal/cmd/app.go":                   {sink: 5, flag: 12},
	"internal/cmd/backup.go":                {sink: 8, print: 5},
	"internal/cmd/candidates.go":            {sink: 3},
	"internal/cmd/capture.go":               {sink: 1, print: 1},
	"internal/cmd/cmd.go":                   {sink: 3},
	"internal/cmd/companion.go":             {sink: 14, print: 8},
	"internal/cmd/completion.go":            {sink: 3},
	"internal/cmd/completion_install.go":    {sink: 6, print: 16},
	"internal/cmd/dircred.go":               {print: 7},
	"internal/cmd/dircred_harvest.go":       {print: 14},
	"internal/cmd/dircred_identity.go":      {sink: 4, print: 1},
	"internal/cmd/dircred_store.go":         {print: 3},
	"internal/cmd/doctor.go":                {sink: 1, print: 5},
	"internal/cmd/edit.go":                  {sink: 3, print: 1},
	"internal/cmd/env.go":                   {sink: 10, print: 4},
	"internal/cmd/flagspec.go":              {flag: 28},
	"internal/cmd/fragment.go":              {print: 2},
	"internal/cmd/freshness.go":             {sink: 1, print: 9},
	"internal/cmd/identity.go":              {sink: 4, print: 1},
	"internal/cmd/init.go":                  {sink: 2, print: 5},
	"internal/cmd/install.go":               {sink: 4, print: 1, flag: 3},
	"internal/cmd/list_diagnostics.go":      {print: 2},
	"internal/cmd/login.go":                 {sink: 9, print: 4},
	"internal/cmd/ls.go":                    {print: 7},
	"internal/cmd/lsplace.go":               {sink: 31, print: 6},
	"internal/cmd/mise_global.go":           {sink: 11},
	"internal/cmd/miseinit.go":              {sink: 9, print: 3},
	"internal/cmd/modes.go":                 {print: 1},
	"internal/cmd/open.go":                  {sink: 5, print: 1},
	"internal/cmd/ops.go":                   {sink: 10, print: 4},
	"internal/cmd/pin.go":                   {sink: 6, print: 16},
	"internal/cmd/place.go":                 {print: 3},
	"internal/cmd/preservation.go":          {sink: 17, print: 4},
	"internal/cmd/profile.go":               {sink: 17, print: 8},
	"internal/cmd/rebind.go":                {sink: 5, print: 1},
	"internal/cmd/relogin.go":               {sink: 12, print: 15},
	"internal/cmd/restorecred.go":           {print: 1},
	"internal/cmd/run.go":                   {sink: 12, print: 5},
	"internal/cmd/status.go":                {sink: 2, print: 5},
	"internal/cmd/storelink.go":             {print: 5},
	"internal/cmd/switch.go":                {sink: 5, print: 13},
	"internal/cmd/text.go":                  {print: 1},
	"internal/cmd/uninstall.go":             {sink: 3, print: 3, flag: 1},
	"internal/cmd/useauto.go":               {sink: 4, print: 2},
	"internal/cmd/usebare.go":               {sink: 1, print: 1},
	"internal/companion/companion.go":       {error: 10},
	"internal/config/config.go":             {error: 21},
	"internal/config/writer.go":             {error: 2},
	"internal/envprofile/envprofile.go":     {error: 6},
	"internal/installation/image.go":        {error: 1},
	"internal/installation/image_darwin.go": {error: 2},
	"internal/installation/receipt.go":      {error: 5},
	"internal/integration/file.go":          {error: 2},
	"internal/keychain/keychain.go":         {error: 5},
	"internal/lock/lock.go":                 {error: 4},
	"internal/patch/atomic.go":              {error: 6},
	"internal/patch/durable.go":             {error: 2},
	"internal/patch/json_pointer.go":        {error: 15},
	"internal/patch/jsonc.go":               {error: 4},
	"internal/picker/picker.go":             {error: 3},
	"internal/preservation/store.go":        {error: 20},
	"internal/secret/file.go":               {error: 1},
	"internal/secret/keychain.go":           {error: 3},
	"internal/secret/libsecret.go":          {error: 4},
	"internal/secret/secret.go":             {error: 8},
	"internal/state/state.go":               {error: 3},
}
