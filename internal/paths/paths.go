// Package paths resolves kagikae's XDG directory layout. kagikae is
// XDG-compliant on every platform, including macOS (see docs/DATA-MODEL.md).
package paths

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

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
		DataDir:   XDGDataHome(getenv, home, "kagikae"),
		StateDir:  dir("XDG_STATE_HOME", filepath.Join(".local", "state")),
	}
	if v := getenv("XDG_RUNTIME_DIR"); v != "" && filepath.IsAbs(v) {
		p.RuntimeDir = filepath.Join(v, "kagikae")
	} else {
		p.RuntimeDir = p.StateDir
	}
	return p
}

// XDGDataHome resolves $XDG_DATA_HOME/<app> with the same spec rule as
// Resolve: a relative value is ignored and the default is used instead.
// Adapters use it for tools that keep state under the XDG data home.
func XDGDataHome(getenv func(string) string, home, app string) string {
	if v := getenv("XDG_DATA_HOME"); v != "" && filepath.IsAbs(v) {
		return filepath.Join(v, app)
	}
	return filepath.Join(home, ".local", "share", app)
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

// HomesDir returns the root of all home-mode tool homes.
func (p Paths) HomesDir() string { return filepath.Join(p.DataDir, "homes") }

// HomeModeDir returns the isolated tool home for home mode.
func (p Paths) HomeModeDir(tool, account string) string {
	return filepath.Join(p.HomesDir(), tool, account)
}

// OverlaysDir returns the root of all overlay tool homes.
func (p Paths) OverlaysDir() string { return filepath.Join(p.DataDir, "overlays") }

// OverlayDir returns the partially-isolated tool home for overlay mode.
func (p Paths) OverlayDir(tool, account string) string {
	return filepath.Join(p.OverlaysDir(), tool, account)
}

// StateFile returns the state.json path.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "state.json") }

// BackupsDir returns the backup metadata directory.
func (p Paths) BackupsDir() string { return filepath.Join(p.StateDir, "backups") }

// LocksDir returns the per-tool lock directory.
func (p Paths) LocksDir() string { return filepath.Join(p.RuntimeDir, "locks") }

// PinID returns the stable short identifier for a bound directory: the first
// 16 hex characters of the SHA-256 hash of the directory's absolute path.
// The 8-byte prefix keeps store paths short while providing adequate
// collision resistance for the number of directories any user would bind.
// absDir must be an absolute path; callers are responsible for resolving it
// with filepath.Abs before calling.
func PinID(absDir string) string {
	sum := sha256.Sum256([]byte(absDir))
	return fmt.Sprintf("%x", sum[:8])
}

// IsolationDir returns the root of all per-directory isolation stores
// (the shared and isolated per-directory mechanisms).
func (p Paths) IsolationDir() string { return filepath.Join(p.DataDir, "isolation") }

// Per-directory store segments under isolation/<pinID>/<tool>/. Exported so
// callers that classify a directory by its path (cmd.kaeManagedHomeKind) stay
// in lockstep with SharedDir / IsolatedConfigDir.
const (
	SharedSegment   = "shared"   // per-directory shared (kae pin --shared)
	IsolatedSegment = "isolated" // per-directory isolated (kae pin --isolated)
	GlobalSegment   = "global"   // global isolated homes (kae use --isolated)
)

// toolIsolDir returns the per-tool isolation root isolation/<pinID>/<tool>
// shared by SharedDir and IsolatedConfigDir.
func (p Paths) toolIsolDir(pinID, tool string) string {
	return filepath.Join(p.IsolationDir(), pinID, tool)
}

// SharedDir returns the config directory for the per-directory shared
// mechanism (account-agnostic). The env-var isolation pointer
// (CLAUDE_CONFIG_DIR etc.) is set to this path on directory entry.
func (p Paths) SharedDir(pinID, tool string) string {
	return filepath.Join(p.toolIsolDir(pinID, tool), SharedSegment)
}

// IsolatedConfigDir returns the config directory (CLAUDE_CONFIG_DIR /
// CODEX_HOME) for one account in the per-directory isolated mechanism. It is
// composed at setup time from opt-in symlinks plus the private credential.
func (p Paths) IsolatedConfigDir(pinID, tool, account string) string {
	return filepath.Join(p.toolIsolDir(pinID, tool), IsolatedSegment, account, "config")
}

// GlobalIsolationDir returns the root of all global-isolated tool homes
// (kae use --isolated): isolation/global. It lives under IsolationDir so the
// kae-managed classifier (cmd.kaeManagedHomeKind) covers both the per-directory
// <pin-id> stores and the global homes from one root.
func (p Paths) GlobalIsolationDir() string {
	return filepath.Join(p.IsolationDir(), GlobalSegment)
}

// GlobalIsolatedHomeDir returns the full per-account private home for one tool
// in global-isolated mode: the directory the kae-owned global mise fragment
// points CLAUDE_CONFIG_DIR / CODEX_HOME at.
func (p Paths) GlobalIsolatedHomeDir(tool, account string) string {
	return filepath.Join(p.GlobalIsolationDir(), tool, account)
}

// MiseGlobalFragmentFile returns the kae-owned global mise fragment path
// (~/.config/mise/conf.d/kagikae.toml), a sibling of kagikae's own config dir
// under the XDG config home. mise loads and merges conf.d/*.toml, so it reaches
// every globally activated terminal; kae regenerates it from state.json
// `synced` and deletes it when no tool is globally isolated.
func (p Paths) MiseGlobalFragmentFile() string {
	return filepath.Join(filepath.Dir(p.ConfigDir), "mise", "conf.d", "kagikae.toml")
}
