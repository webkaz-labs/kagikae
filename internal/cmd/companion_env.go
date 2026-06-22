package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// companionTokenSubcmd is the hidden credential-helper subcommand mise's
// exec() template invokes to resolve a secret token at environment-evaluation
// time. It is the one documented path on which kae prints a secret to stdout
// (see docs/SECURITY.md); it never appears in human/JSON reporting output.
const companionTokenSubcmd = "__companion-token"

// companionEnvEntry is one resolved env binding for a profile's companions.
// A non-nil Lookup (argv of kae's credential helper) marks a secret token
// resolved at mise eval time; otherwise Value is a literal path set directly.
type companionEnvEntry struct {
	EnvVar string
	Value  string   // literal env value (git-config file path, config-dir path)
	Lookup []string // non-nil: secret; argv resolved at eval time to the value
}

// companionPlan resolves a profile's companion bindings into env entries plus
// the redaction list, and a prepare closure that writes any generated files
// (the git-config kind's config). It mirrors isolationPlan: entries describe,
// prepare performs IO. It returns nil entries when the profile binds no
// companion, leaving the fragment unchanged. Companions are profile-scoped, so
// only the per-directory pin fragment carries them (the global fragment has no
// profile); see docs/ADAPTERS-COMPANION.md.
func (app *App) companionPlan(profileName string) (entries []companionEnvEntry, redactions []string, prepare func() error, err error) {
	noop := func() error { return nil }
	profile, ok := app.Config.Profiles[profileName]
	if !ok || len(profile.Companions) == 0 {
		return nil, nil, noop, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("locate kae binary for companion token lookup: %w", err)
	}
	if strings.ContainsAny(self, "'") {
		return nil, nil, nil, fmt.Errorf("kae binary path %q contains a quote; companion token lookup cannot be templated safely", self)
	}
	var writes []func() error
	// Iterate the registry in canonical order for stable fragment output.
	for _, spec := range companion.All() {
		data, bound := profile.Companions[spec.ID]
		if !bound {
			continue
		}
		switch spec.Kind {
		case companion.KindGitConfig:
			path := app.Paths.CompanionConfigFile(profileName, spec.ID)
			content, rerr := renderCompanionFile(spec, data, filepath.Join(app.Env.Home, ".gitconfig"))
			if rerr != nil {
				return nil, nil, nil, rerr
			}
			entries = append(entries, companionEnvEntry{EnvVar: spec.FileEnvVar, Value: path})
			p, c := path, content
			writes = append(writes, func() error {
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					return fmt.Errorf("create companion dir: %w", err)
				}
				return patch.WriteFileAtomic(p, []byte(c), 0o644)
			})
		case companion.KindConfigDir:
			for _, k := range spec.Knobs {
				// An empty path means the knob is unset; skip it (git-config omits
				// empty knobs in its template, token knobs use "" as the secret
				// marker — each kind handles an empty value as fits its delivery).
				if v := data[k.Name]; v != "" {
					entries = append(entries, companionEnvEntry{EnvVar: k.EnvVar, Value: v})
				}
			}
		case companion.KindToken:
			for _, k := range spec.Knobs {
				if _, set := data[k.Name]; set {
					entries = append(entries, companionEnvEntry{
						EnvVar: k.EnvVar,
						Lookup: []string{self, companionTokenSubcmd, profileName, spec.ID, k.Name},
					})
					redactions = append(redactions, k.EnvVar)
				}
			}
		}
	}
	prepare = func() error {
		for _, w := range writes {
			if err := w(); err != nil {
				return err
			}
		}
		return nil
	}
	return entries, redactions, prepare, nil
}

// renderCompanionFile renders a git-config-kind companion's generated file from
// its template, feeding the knob values plus the user's home git config path
// (which the template [include]s so existing global settings survive).
func renderCompanionFile(spec companion.Spec, data map[string]string, homeGitconfig string) (string, error) {
	tmpl, err := template.New(spec.ID).Parse(spec.FileTmpl)
	if err != nil {
		return "", fmt.Errorf("parse %s config template: %w", spec.ID, err)
	}
	vals := map[string]string{"HomeGitconfig": homeGitconfig}
	for _, k := range spec.Knobs {
		vals[k.Name] = data[k.Name]
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vals); err != nil {
		return "", fmt.Errorf("render %s config: %w", spec.ID, err)
	}
	return buf.String(), nil
}

// companionFragmentLines renders the companion env entries as mise [env] lines.
// Token entries become an exec() template resolved at eval time; literal
// entries are quoted values.
func companionFragmentLines(entries []companionEnvEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(e.Lookup) > 0 {
			lines = append(lines, fmt.Sprintf("%s = %q", e.EnvVar, companionExecTemplate(e.Lookup)))
		} else {
			lines = append(lines, fmt.Sprintf("%s = %q", e.EnvVar, e.Value))
		}
	}
	return lines
}

// companionExportLines renders the companion env entries as POSIX `export`
// lines for the mise-not-activated fallback. Token entries run the helper via
// command substitution; literal entries are single-quoted.
func companionExportLines(entries []companionEnvEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(e.Lookup) > 0 {
			lines = append(lines, fmt.Sprintf("export %s=\"$(%s)\"", e.EnvVar, shellArgv(e.Lookup)))
		} else {
			lines = append(lines, fmt.Sprintf("export %s=%s", e.EnvVar, shellSingleQuote(e.Value)))
		}
	}
	return lines
}

// companionExecTemplate builds the mise tera exec() template that resolves a
// secret at environment-evaluation time. The argv is shell-quoted into the
// single-quoted command string mise runs through a shell.
func companionExecTemplate(argv []string) string {
	return "{{ exec(command='" + shellArgv(argv) + "') }}"
}

// shellArgv double-quotes each argument (the surrounding contexts — the tera
// single-quoted string and the fallback's $() — are single-quote or
// command-substitution, so embedded double quotes are the only concern) and
// joins them, so a kae binary path with spaces survives the shell that runs it.
func shellArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellDoubleQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellDoubleQuote wraps s in double quotes, escaping the characters the shell
// still interprets inside them.
func shellDoubleQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}
