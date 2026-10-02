package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

const (
	legacyGlobalIsolatedOwnershipLine = "# Written by `kae use -i`, removed by `kae use -s`; regenerated from kae state."
	globalIsolatedOwnershipLine       = "# Isolated settings follow kae state; shared teardown retains completion."
)

// globalMiseFile preserves the resolved target and mode of an existing symlink.
// Callers hold the state lock across reading and replacing its owned content.
type globalMiseFile struct {
	path, target, content string
	mode                  os.FileMode
	exists                bool
}

func readGlobalMiseFile(path string) (globalMiseFile, error) {
	f := globalMiseFile{path: path, mode: 0o644}
	target, info, exists, err := resolveMiseConfigTarget(path)
	if err != nil {
		return f, err
	}
	f.target, f.exists = target, exists
	if exists {
		f.mode = info.Mode().Perm()
		data, err := os.ReadFile(target)
		if err != nil {
			return f, err
		}
		f.content = string(data)
	}
	return f, nil
}

func (f globalMiseFile) write(content string) error {
	if content == f.content && f.exists {
		return nil
	}
	if content == "" && f.path == f.target {
		if err := os.Remove(f.target); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.target), 0o755); err != nil {
		return err
	}
	return patch.WriteFileAtomic(f.target, []byte(content), f.mode)
}

// knownCompletion recognizes only complete generated blocks, not marker ownership
// alone. TOML table continuations outside the marker make the block foreign.
func knownCompletion(content string) (shell, block string) {
	if strings.Count(content, miseBlockStart) != 1 || strings.Count(content, miseBlockEnd) != 1 {
		return "", ""
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, current := range []bool{true, false} {
			block := miseHookBlock(shell)
			if !current {
				block = legacyMiseHookBlock(shell)
			}
			at := strings.Index(content, block)
			if at >= 0 && miseTableEndsBeforeContent(content[at+len(block):]) && miseEnterHookMatches(content, shell, current) {
				// The bytes must be the actual table, not a copy inside a
				// multiline string whose real hook happens to match.
				cleaned := strings.Replace(content, block, "", 1)
				var remaining map[string]any
				if _, err := toml.Decode(cleaned, &remaining); err != nil {
					continue
				}
				if hooks, ok := remaining["hooks"].(map[string]any); ok {
					if _, exists := hooks["enter"]; exists {
						continue
					}
				}
				return shell, block
			}
		}
	}
	return "", ""
}

// splitGlobalMise separates the two owned features. Unknown content is refused:
// dropping an unrecognized hook while repairing isolation could run arbitrary
// commands differently in the user's next shell.
func splitGlobalMise(content string) (isolated, hook, shell string, err error) {
	isolated = content
	if strings.Contains(content, miseBlockStart) {
		shell, hook = knownCompletion(content)
		if hook == "" {
			return "", "", "", errf(constants.ExitUnsafeRefused, "unrecognized completion in global mise fragment; resolve it manually")
		}
		isolated = strings.Replace(content, hook, "", 1)
	}
	if strings.TrimSpace(isolated) == "" {
		return "", hook, shell, nil
	}
	if !strings.HasPrefix(isolated, "# kagikae-managed mise fragment") {
		return "", "", "", errf(constants.ExitUnsafeRefused, "unrecognized global mise fragment; refusing to replace it")
	}
	var config struct {
		Env map[string]string `toml:"env"`
	}
	meta, e := toml.Decode(isolated, &config)
	if e != nil || len(meta.Undecoded()) != 0 {
		return "", "", "", errf(constants.ExitUnsafeRefused, "invalid global mise fragment; resolve it manually")
	}
	for key := range config.Env {
		known := false
		for _, tool := range constants.Tools {
			if key != "" && (key == isolationEnvVar(tool) || key == credentialEnvVar(tool)) {
				known = true
				break
			}
		}
		if !known {
			return "", "", "", errf(constants.ExitUnsafeRefused, "unrecognized environment setting in global mise fragment")
		}
	}
	return isolated, hook, shell, nil
}

type miseCompletionMigration struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Shell       string `json:"shell"`
}

// updateGlobalMiseCompletion shares isolation's state lock. The small journal
// retains registration intent if the process dies after removing the old block.
// It contains paths and a shell name, never config content or credential bytes.
func updateGlobalMiseCompletion(env adapter.Env, requested string, refresh bool) (path, shell string, registered, changed bool, err error) {
	app := &App{Env: env, Paths: paths.Resolve(env.Getenv, env.Home)}
	path = app.Paths.MiseGlobalFragmentFile()
	l, err := app.acquireNamedLock(lockNameState, "another kae process is updating global mise integration; retry shortly")
	if err != nil {
		return path, "", false, false, err
	}
	defer l.Release()
	dst, err := readGlobalMiseFile(path)
	if err != nil {
		return path, "", false, false, err
	}
	isolated, _, existing, err := splitGlobalMise(dst.content)
	if err != nil {
		return path, "", false, false, err
	}
	sourcePath := globalMiseConfigPath(env)
	if filepath.Clean(sourcePath) == filepath.Clean(path) {
		return path, "", false, false, errf(constants.ExitUnsafeRefused, "global mise config and fragment must have distinct paths")
	}
	src, err := readGlobalMiseFile(sourcePath)
	if err != nil {
		return path, "", false, false, err
	}
	if src.target == dst.target {
		return path, "", false, false, errf(constants.ExitUnsafeRefused, "global mise config and fragment resolve to the same file")
	}
	old, block := knownCompletion(src.content)
	journalPath := filepath.Join(app.Paths.StateDir, "mise-completion-migration.json")
	intent := miseCompletionMigration{Source: sourcePath, Destination: path, Shell: old}
	pending := false
	if data, e := os.ReadFile(journalPath); e == nil {
		pending = true
		intent = miseCompletionMigration{}
		if json.Unmarshal(data, &intent) != nil || intent.Source != sourcePath || intent.Destination != path || (intent.Shell != "bash" && intent.Shell != "zsh" && intent.Shell != "fish") {
			return path, "", false, false, errf(constants.ExitUnsafeRefused, "global mise migration record needs manual resolution")
		}
	} else if !os.IsNotExist(e) {
		return path, "", false, false, e
	}
	if strings.Contains(src.content, miseBlockStart) && old == "" {
		if refresh && !pending {
			return path, existing, existing != "", false, nil
		}
		return path, "", false, false, errf(constants.ExitUnsafeRefused, "customized completion block in global mise config; migrate it manually")
	}
	shell = requested
	if pending {
		shell = intent.Shell
	} else if refresh {
		shell = old
		if shell == "" {
			shell = existing
		}
	}
	if shell == "" {
		return path, "", false, false, nil
	}
	if old != "" && existing != "" && old != existing && refresh {
		return path, "", false, false, errf(constants.ExitUnsafeRefused, "conflicting completion registrations; select a shell with completion --install")
	}
	if pending && existing != "" && existing != intent.Shell {
		return path, "", false, false, errf(constants.ExitUnsafeRefused, "completion changed during migration; resolve the migration record manually")
	}
	registered = true
	if old != "" || pending {
		if !pending {
			intent.Shell = shell
			data, e := json.Marshal(intent)
			if e != nil {
				return path, shell, true, false, e
			}
			if e = os.MkdirAll(app.Paths.StateDir, 0o700); e != nil {
				return path, shell, true, false, e
			}
			if e = patch.WriteFileAtomic(journalPath, data, 0o600); e != nil {
				return path, shell, true, false, e
			}
		}
		cleaned := src.content
		if block != "" {
			cleaned = strings.Replace(src.content, block, "", 1)
		}
		var parsed map[string]any
		if _, e := toml.Decode(cleaned, &parsed); e != nil {
			return path, shell, true, false, errf(constants.ExitUnsafeRefused, "global mise config is invalid after removing completion; resolve it manually")
		}
		if e := src.write(cleaned); e != nil {
			return path, shell, true, false, e
		}
		if e := dst.write(isolated + miseHookBlock(shell)); e != nil {
			// Restore only the source just read under this lock. External editors do
			// not share the lock: a changed source must not be overwritten on rollback.
			now, re := os.ReadFile(src.target)
			if (re == nil && string(now) == cleaned) || (os.IsNotExist(re) && cleaned == "") {
				if restoreErr := patch.WriteFileAtomic(src.target, []byte(src.content), src.mode); restoreErr != nil {
					return path, shell, true, false, fmt.Errorf("write completion failed: %w; source restore failed: %v", e, restoreErr)
				}
			}
			return path, shell, true, false, e
		}
		if e := os.Remove(journalPath); e != nil {
			return path, shell, true, false, e
		}
		return path, shell, true, true, nil
	}
	updated := isolated + miseHookBlock(shell)
	if e := dst.write(updated); e != nil {
		return path, shell, true, false, e
	}
	return path, shell, true, updated != dst.content, nil
}
