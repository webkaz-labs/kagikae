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
	Enabled                   *bool `toml:"enabled"`
	WarnAntigravityTransition *bool `toml:"warn_antigravity_transition"`
	HomeModeEnabled           *bool `toml:"home_mode_enabled"`
	OverlayModeEnabled        *bool `toml:"overlay_mode_enabled"`
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
	for tool := range c.Tools {
		if !constants.IsTool(tool) {
			return fmt.Errorf("unknown tool %q in [tools]", tool)
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

// OverlayModeEnabled reports whether the experimental overlay mode is
// explicitly enabled for a tool (default false).
func (c *Config) OverlayModeEnabled(tool string) bool {
	t, ok := c.Tools[tool]
	if !ok || t.OverlayModeEnabled == nil {
		return false
	}
	return *t.OverlayModeEnabled
}

// WarnAntigravity reports whether the Gemini transition notice is active.
func (c *Config) WarnAntigravity() bool {
	t, ok := c.Tools[constants.ToolGemini]
	if !ok || t.WarnAntigravityTransition == nil {
		return true
	}
	return *t.WarnAntigravityTransition
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

[tools.gemini]
enabled = true
warn_antigravity_transition = true

[tools.agy]
enabled = true

# Profiles bundle per-tool accounts:
#
# [profiles.work]
# label = "Work"
# [profiles.work.accounts]
# claude = "work"
# codex = "work"
# gemini = "work"
#
# [profiles.personal]
# label = "Personal"
# [profiles.personal.accounts]
# claude = "personal"
# codex = "personal"
# gemini = "personal"
`)
	return b.String()
}
