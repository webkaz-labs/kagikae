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
	Version        int                `toml:"version"`
	DefaultProfile string             `toml:"default_profile"`
	Security       Security           `toml:"security"`
	Tools          map[string]Tool    `toml:"tools"`
	Profiles       map[string]Profile `toml:"profiles"`
}

// Security holds the secret backend and retention policy.
type Security struct {
	SecretBackend string `toml:"secret_backend"`
	BackupKeep    int    `toml:"backup_keep"`
}

// Tool holds per-tool settings. Pointers distinguish "unset" from "false".
type Tool struct {
	Enabled *bool `toml:"enabled"`
	// SharedDenylistExtra adds file names to the per-directory shared bind's
	// denylist (kae pin -s), on top of the hard-coded credential list.
	SharedDenylistExtra []string `toml:"shared_denylist_extra"`
	// IsolatedSharedItems opts file names into a per-directory isolated bind
	// (kae pin -i), which shares nothing from the real home by default.
	IsolatedSharedItems []string `toml:"isolated_shared_items"`
	// Driver, when set to constants.DriverValueFile, is the persisted, explicit
	// opt-in counterpart to the KAE_CLAUDE_DRIVER env var (claude only). It
	// forces the file-patch driver even on darwin. The env var takes
	// precedence; see App env plumbing and docs/ADAPTERS.md.
	Driver string `toml:"driver"`
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
		if len(key) > 0 {
			if repl, removed := renamedToolKeys[key[len(key)-1]]; removed {
				if repl != "" {
					return nil, warnings, fmt.Errorf(
						"config key %q was renamed to %q in v0.8.0 (pre-1.0 hard break; rename it)", key.String(), repl,
					)
				}
				return nil, warnings, fmt.Errorf(
					"config key %q was removed in v0.8.0 (overlay/home modes are gone; bind directories with kae pin -s|-i)", key.String(),
				)
			}
		}
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
		for _, item := range settings.SharedDenylistExtra {
			if !ValidFileName(item) {
				return fmt.Errorf("tools.%s.shared_denylist_extra item %q is not a bare file name", tool, item)
			}
			if refusedSharedDenylistExtra[item] {
				return fmt.Errorf("tools.%s.shared_denylist_extra: %q is already in the hard-coded credential denylist", tool, item)
			}
		}
		for _, item := range settings.IsolatedSharedItems {
			if !ValidFileName(item) {
				return fmt.Errorf("tools.%s.isolated_shared_items item %q is not a bare file name", tool, item)
			}
			if refusedIsolatedShare[item] {
				return fmt.Errorf("tools.%s.isolated_shared_items must not share the auth credential %q", tool, item)
			}
		}
		if settings.Driver != "" {
			if tool != constants.ToolClaude {
				return fmt.Errorf("tools.%s.driver is only valid for claude", tool)
			}
			if settings.Driver != constants.DriverValueFile {
				return fmt.Errorf("tools.claude.driver %q is invalid (only %q is supported)", settings.Driver, constants.DriverValueFile)
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

// renamedToolKeys maps per-tool config keys removed in v0.8.0 to their
// replacement (empty = removed outright). Old configs error at load naming the
// new key — a pre-1.0 hard break with no silent acceptance (docs/RELEASE.md).
var renamedToolKeys = map[string]string{
	"bond_denylist_extra":  "shared_denylist_extra",
	"pin_shared_items":     "isolated_shared_items",
	"overlay_extra_shared": "", // overlay mode removed
	"overlay_mode_enabled": "", // overlay mode removed
	"home_mode_enabled":    "", // home mode removed
}

// refusedSharedDenylistExtra lists the auth artifacts that are always on the
// hard-coded shared-bind denylist (see bondDenylistItems in
// internal/cmd/miseinit.go); adding them to SharedDenylistExtra is rejected to
// avoid confusion. The names mirror what the tool adapters switch (claude:
// .credentials.json; codex: auth.json).
var refusedSharedDenylistExtra = map[string]bool{
	".credentials.json": true,
	"auth.json":         true,
}

// refusedIsolatedShare lists the auth credentials that must never be listed in
// isolated_shared_items — they must remain private to the directory and account.
var refusedIsolatedShare = map[string]bool{
	".credentials.json": true,
	"auth.json":         true,
}

// SharedDenylistExtra returns the user-configured extra items to exclude from
// the per-directory shared bind's symlink sharing (validated at load time).
func (c *Config) SharedDenylistExtra(tool string) []string {
	if t, ok := c.Tools[tool]; ok {
		return t.SharedDenylistExtra
	}
	return nil
}

// IsolatedSharedItems returns the user-configured items to symlink from the
// real home into a per-directory isolated bind (opt-in; validated at load time).
func (c *Config) IsolatedSharedItems(tool string) []string {
	if t, ok := c.Tools[tool]; ok {
		return t.IsolatedSharedItems
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

[tools.copilot]
enabled = true

# Profiles bundle per-tool accounts:
#
# [profiles.main]
# label = "Main"
# [profiles.main.accounts]
# claude = "main"
# codex = "main"
#
# [profiles.side]
# label = "Side"
# [profiles.side.accounts]
# claude = "side"
# codex = "side"
`)
	return b.String()
}
