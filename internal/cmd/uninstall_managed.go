package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/integration"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// managedUninstallGuidance reads only bounded config candidates and mise help.
// It does not invoke config discovery that could update mise's tracked registry.
func (app *App) managedUninstallGuidance(executable string, dirs []string) []string {
	root := app.Env.Getenv("MISE_DATA_DIR")
	if root == "" {
		root = paths.XDGDataHome(app.Env.Getenv, app.Env.Home, "mise")
	}
	managed := false
	for _, subdir := range []string{"installs", "shims"} {
		base := filepath.Join(root, subdir)
		rel, err := filepath.Rel(base, executable)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			managed = true
		}
	}
	if !managed {
		return []string{fmt.Sprintf("Unrecorded executable: %s. A manual copy or plain go install has no removal receipt; reinstall this path through the supported direct installer before automatic removal.", executable)}
	}
	ctx := context.Background()
	help, _, code := runner.Run(ctx, "mise", "unuse", "--help")
	if code != 0 || !strings.Contains(help, "--path") || !strings.Contains(help, "--no-prune") {
		return []string{"This executable is mise-managed. Inspect the installed mise help and owning configuration before removing its request; kae will not unlink a managed binary or shim."}
	}
	candidates := []string{globalMiseConfigPath(app.Env), filepath.Join(filepath.Dir(app.Paths.MiseGlobalFragmentFile()), "kagikae-install.toml")}
	for _, dir := range dirs {
		candidates = append(candidates, filepath.Join(dir, "mise.toml"), filepath.Join(dir, ".mise.toml"))
	}
	sort.Strings(candidates)
	seen := map[string]bool{}
	var guidance []string
	for _, path := range candidates {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") || seen[path] {
			continue
		}
		seen[path] = true
		file, err := integration.Read(path)
		if err != nil || !file.Exists {
			continue
		}
		var cfg struct {
			Tools map[string]any `toml:"tools"`
		}
		if _, err := toml.Decode(string(file.Content), &cfg); err != nil {
			continue
		}
		for _, tool := range []string{"github:webkaz-labs/kagikae", "packslip:github.com/webkaz-labs/kagikae"} {
			if _, present := cfg.Tools[tool]; !present {
				continue
			}
			guidance = append(guidance, fmt.Sprintf("After integration cleanup, remove this configured request with: mise unuse --path %s %s. This also prunes versions unused by tracked configs; use --no-prune to retain installations. Other projects may still need them.", shellSingleQuote(path), shellSingleQuote(tool)))
		}
	}
	if len(guidance) == 0 {
		guidance = append(guidance, "This executable is mise-managed but no matching loaded request was identified. Inspect aliases, project requests and mise config ls --tracked-configs before removing the installation.")
	}
	return guidance
}
