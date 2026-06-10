// Package paths resolves kagikae's XDG directory layout. kagikae is
// XDG-compliant on every platform, including macOS (see docs/DATA-MODEL.md).
package paths

import "path/filepath"

// Paths holds the resolved kagikae base directories.
type Paths struct {
	ConfigDir  string // $XDG_CONFIG_HOME/kagikae
	DataDir    string // $XDG_DATA_HOME/kagikae
	StateDir   string // $XDG_STATE_HOME/kagikae
	RuntimeDir string // $XDG_RUNTIME_DIR/kagikae, or StateDir fallback
}

// Resolve builds Paths from environment lookups and the home directory.
// XDG values must be absolute paths per the XDG spec; relative values are
// ignored and the default is used instead.
func Resolve(getenv func(string) string, home string) Paths {
	dir := func(envKey, def string) string {
		if v := getenv(envKey); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, "kagikae")
		}
		return filepath.Join(home, def, "kagikae")
	}
	p := Paths{
		ConfigDir: dir("XDG_CONFIG_HOME", ".config"),
		DataDir:   dir("XDG_DATA_HOME", filepath.Join(".local", "share")),
		StateDir:  dir("XDG_STATE_HOME", filepath.Join(".local", "state")),
	}
	if v := getenv("XDG_RUNTIME_DIR"); v != "" && filepath.IsAbs(v) {
		p.RuntimeDir = filepath.Join(v, "kagikae")
	} else {
		p.RuntimeDir = p.StateDir
	}
	return p
}

// ConfigFile returns the config.toml path.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// AccountDir returns the metadata directory for one captured account.
func (p Paths) AccountDir(tool, account string) string {
	return filepath.Join(p.DataDir, "accounts", tool, account)
}

// AccountsDir returns the root of all account snapshots.
func (p Paths) AccountsDir() string { return filepath.Join(p.DataDir, "accounts") }

// SecretsDir returns the opt-in file-backend secret root.
func (p Paths) SecretsDir() string { return filepath.Join(p.DataDir, "secrets") }

// EnvProfileDir returns the metadata directory for one env profile.
func (p Paths) EnvProfileDir(tool, account string) string {
	return filepath.Join(p.DataDir, "env", tool, account)
}

// EnvProfilesDir returns the root of all env profiles.
func (p Paths) EnvProfilesDir() string { return filepath.Join(p.DataDir, "env") }

// HomeModeDir returns the isolated tool home for home mode.
func (p Paths) HomeModeDir(tool, account string) string {
	return filepath.Join(p.DataDir, "homes", tool, account)
}

// OverlayDir returns the partially-isolated tool home for overlay mode.
func (p Paths) OverlayDir(tool, account string) string {
	return filepath.Join(p.DataDir, "overlays", tool, account)
}

// StateFile returns the state.json path.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "state.json") }

// BackupsDir returns the backup metadata directory.
func (p Paths) BackupsDir() string { return filepath.Join(p.StateDir, "backups") }

// LocksDir returns the per-tool lock directory.
func (p Paths) LocksDir() string { return filepath.Join(p.RuntimeDir, "locks") }
