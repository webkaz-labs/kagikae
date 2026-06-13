// Package config parses and validates kagikae's TOML configuration.
// Unknown keys warn (pre-1.0 schema); a newer schema version is an error.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// SupportedVersion is the highest config schema this build understands.
const SupportedVersion = 1

// DefaultBackupKeep is the default backup retention count.
const DefaultBackupKeep = 30

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// ValidName reports whether s is a safe account/profile name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// ValidFileName reports whether s is a safe bare file name: the same
// character set as ValidName (which already excludes path separators) minus
// the directory self-references the regexp would let through.
func ValidFileName(s string) bool { return ValidName(s) && s != "." && s != ".." }

// Config is the parsed user policy.
type Config struct {
	Version        int                   `toml:"version"`
	DefaultProfile string                `toml:"default_profile"`
	Security       Security              `toml:"security"`
	Tools          map[string]Tool       `toml:"tools"`
	Profiles       map[string]Profile    `toml:"profiles"`
}

// Security holds the secret backend and retention policy.
type Security struct {
	SecretBackend string `toml:"secret_backend"`
	BackupKeep    int    `toml:"backup_keep"`
}

// Tool holds per-tool settings. Pointers distinguish "unset" from "false".
type Tool struct {
	Enabled            *bool    `toml:"enabled"`
	HomeModeEnabled    *bool    `toml:"home_mode_enabled"`
	OverlayModeEnabled *bool    `toml:"overlay_mode_enabled"`
	OverlayExtraShared []string `toml:"overlay_extra_shared"`
}

// Profile bundles per-tool accounts under one name.
type Profile struct {
	Label    string            `toml:"label"`
	Accounts map[string]string `toml:"accounts"`
}

// Default returns the built-in policy used when no config file exists.
func Default() *Config {
	return &Config{
		Version:  SupportedVersion,
		Security: Security{SecretBackend: secret.BackendAuto, BackupKeep: DefaultBackupKeep},
		Tools:    map[string]Tool{},
		Profiles: map[string]Profile{},
	}
}

// Load reads the config file. A missing file yields defaults without error.
// The returned warnings cover unknown keys and soft issues.
func Load(path string) (*Config, []string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	meta, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}
	var warnings []string
	for _, key := range meta.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("unknown config key %q ignored", key.String()))
	}
	warnings = append(warnings, stripRemovedTools(cfg)...)
	if err := cfg.validate(); err != nil {
		return nil, warnings, err
	}
	return cfg, warnings, nil
}

func (c *Config) validate() error {
	if c.Version > SupportedVersion {
		return fmt.Errorf("config version %d is newer than supported %d", c.Version, SupportedVersion)
	}
	if c.Security.BackupKeep < 1 {
		return fmt.Errorf("security.backup_keep must be >= 1")
	}
	for tool, settings := range c.Tools {
		if !constants.IsTool(tool) {
			return fmt.Errorf("unknown tool %q in [tools]", tool)
		}
		for _, item := range settings.OverlayExtraShared {
			if !ValidFileName(item) {
				return fmt.Errorf("tools.%s.overlay_extra_shared item %q is not a bare file name", tool, item)
			}
			if refusedOverlayShare[item] {
				return fmt.Errorf("tools.%s.overlay_extra_shared must not share the auth/identity artifact %q", tool, item)
			}
		}
	}
	for name, profile := range c.Profiles {
		if !ValidName(name) {
			return fmt.Errorf("invalid profile name %q", name)
		}
		for tool, account := range profile.Accounts {
			if !constants.IsTool(tool) {
				return fmt.Errorf("profile %q maps unknown tool %q", name, tool)
			}
			if !ValidName(account) {
				return fmt.Errorf("profile %q maps tool %q to invalid account name %q", name, tool, account)
			}
		}
	}
	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; !ok {
			return fmt.Errorf("default_profile %q is not defined under [profiles]", c.DefaultProfile)
		}
	}
	return nil
}

// refusedOverlayShare lists the auth/identity artifacts that must never be
// shared into an overlay — sharing them would defeat the isolation. The
// names mirror what the tool adapters switch (claude: .credentials.json and
// the ~/.claude.json identity file; codex: auth.json); docs/ADAPTERS.md
// "Isolation" is the normative source — keep all three in sync.
var refusedOverlayShare = map[string]bool{
	".credentials.json": true,
	".claude.json":      true,
	"auth.json":         true,
}

// OverlayExtraShared returns the user-configured extra real-home items to
// share into a tool's overlays (validated at load time).
func (c *Config) OverlayExtraShared(tool string) []string {
	if t, ok := c.Tools[tool]; ok {
		return t.OverlayExtraShared
	}
	return nil
}

// ToolEnabled reports whether a tool participates in switch all / status.
func (c *Config) ToolEnabled(tool string) bool {
	t, ok := c.Tools[tool]
	if !ok || t.Enabled == nil {
		return true
	}
	return *t.Enabled
}

// HomeModeEnabled reports whether home mode is allowed for a tool
// (default true).
func (c *Config) HomeModeEnabled(tool string) bool {
	t, ok := c.Tools[tool]
	if !ok || t.HomeModeEnabled == nil {
		return true
	}
	return *t.HomeModeEnabled
}

// OverlayModeEnabled reports whether overlay mode is enabled for a tool
// (default true since v0.5.0; per-tool opt-out via
// tools.<tool>.overlay_mode_enabled = false).
func (c *Config) OverlayModeEnabled(tool string) bool {
	t, ok := c.Tools[tool]
	if !ok || t.OverlayModeEnabled == nil {
		return true
	}
	return *t.OverlayModeEnabled
}

// stripRemovedTools drops config references to tools kae no longer
// supports with a warning instead of a hard validation error, so configs
// written before a removal keep working (docs/RELEASE.md Breaking Changes).
func stripRemovedTools(c *Config) []string {
	var warnings []string
	for tool, successor := range constants.RemovedTools {
		if _, ok := c.Tools[tool]; ok {
			delete(c.Tools, tool)
			warnings = append(warnings,
				fmt.Sprintf("[tools.%s] ignored: %s was removed (successor: %s)", tool, tool, successor))
		}
		for name, profile := range c.Profiles {
			if _, ok := profile.Accounts[tool]; ok {
				delete(profile.Accounts, tool)
				warnings = append(warnings,
					fmt.Sprintf("profiles.%s.accounts.%s ignored: %s was removed (successor: %s)", name, tool, tool, successor))
			}
		}
	}
	return warnings
}

// ProfileNames returns profile names sorted ascending.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MatchProfile returns the profile name whose mapping equals active (for the
// tools the profile maps), or "" when none matches exactly.
func (c *Config) MatchProfile(active map[string]string) string {
	for _, name := range c.ProfileNames() {
		profile := c.Profiles[name]
		if len(profile.Accounts) == 0 {
			continue
		}
		all := true
		for tool, account := range profile.Accounts {
			if active[tool] != account {
				all = false
				break
			}
		}
		if all {
			return name
		}
	}
	return ""
}

// InitialContent is the config.toml written by kae init.
func InitialContent(defaultProfile string) string {
	var b strings.Builder
	b.WriteString("version = 1\n")
	if defaultProfile != "" {
		fmt.Fprintf(&b, "default_profile = %q\n", defaultProfile)
	}
	b.WriteString(`
[security]
# auto | keychain | libsecret | file (file stores plaintext; explicit opt-in)
secret_backend = "auto"
backup_keep = 30

[tools.claude]
enabled = true

[tools.codex]
enabled = true

[tools.agy]
enabled = true

[tools.opencode]
enabled = true

[tools.cursor]
enabled = true

# Profiles bundle per-tool accounts:
#
# [profiles.work]
# label = "Work"
# [profiles.work.accounts]
# claude = "work"
# codex = "work"
#
# [profiles.personal]
# label = "Personal"
# [profiles.personal.accounts]
# claude = "personal"
# codex = "personal"
`)
	return b.String()
}
