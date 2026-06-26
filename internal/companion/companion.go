// Package companion declares auth-lockstep targets: git, gh, and cloud CLIs
// whose identity kae binds per profile by driving environment and config,
// without capturing their credentials the way internal/adapter does for Tools.
//
// Each companion is one declarative Spec registered from its own package's
// init(); adding a tool is one struct literal. The override Kind selects how
// the identity is delivered (see constants.Override*): a git-config file, a
// secret env var resolved at mise eval time via an exec() lookup, or an env
// var pointing at a user-provided config path. Secret values live only in the
// secret backend under SecretRef; config.toml holds knob names and non-secret
// values. The normative switched/preserved contract is docs/ADAPTERS-COMPANION.md.
package companion

import (
	"fmt"
	"regexp"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// OverrideKind classifies how a companion's identity reaches the tool.
type OverrideKind string

const (
	// KindGitConfig renders FileTmpl into a kae-owned file and points FileEnvVar
	// at it; the knobs are non-secret data fields (email/name/signingkey).
	KindGitConfig OverrideKind = constants.OverrideGitConfig
	// KindToken delivers each knob as a secret env var (Knob.EnvVar) whose value
	// is resolved at mise eval time from the secret backend; never on disk.
	KindToken OverrideKind = constants.OverrideToken
	// KindConfigDir sets each knob's env var (Knob.EnvVar) to a non-secret
	// filesystem path the user supplies (e.g. KUBECONFIG).
	KindConfigDir OverrideKind = constants.OverrideConfigDir
)

// knobNameRE bounds a knob name to a safe identifier: it becomes a config.toml
// key, a secret-ref segment, an env var name, or a git-config field.
var knobNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidKnobName reports whether name is a safe companion knob name.
func ValidKnobName(name string) bool { return knobNameRE.MatchString(name) }

// Knob is one configurable input of a companion.
//
// For KindToken each knob is a secret delivered through its own env var via an
// exec() lookup against the secret backend. For KindConfigDir the knob's value
// is a non-secret path set directly on EnvVar. For KindGitConfig the knobs are
// non-secret data fields rendered into the file named by Spec.FileEnvVar and
// their EnvVar is empty.
type Knob struct {
	Name   string // config.toml key; for KindToken also the secret-ref subkey
	EnvVar string // env var this knob feeds (KindToken, KindConfigDir)
}

// Spec declares one companion tool.
type Spec struct {
	ID         string       // a constants.Companion* id
	Binary     string       // upstream CLI name, for doctor's PATH check
	Kind       OverrideKind // delivery mechanism
	Knobs      []Knob       // configurable inputs
	FileTmpl   string       // KindGitConfig only: text/template for the generated file
	FileEnvVar string       // KindGitConfig only: env var pointing at the generated file
}

// Secret reports whether this companion's knob values live in the secret
// backend (token companions) rather than in config.toml.
func (s Spec) Secret() bool { return s.Kind == KindToken }

// Knob returns the named knob, or false when the companion has no such knob.
func (s Spec) Knob(name string) (Knob, bool) {
	for _, k := range s.Knobs {
		if k.Name == name {
			return k, true
		}
	}
	return Knob{}, false
}

// validate checks a Spec is internally consistent. Registration panics on a
// malformed Spec because it is a programming error in a companion package.
func (s Spec) validate() error {
	if !constants.IsCompanion(s.ID) {
		return fmt.Errorf("unknown companion id %q (add it to constants.Companions)", s.ID)
	}
	if s.Binary == "" {
		return fmt.Errorf("companion %q: empty Binary", s.ID)
	}
	if len(s.Knobs) == 0 {
		return fmt.Errorf("companion %q: no knobs", s.ID)
	}
	for _, k := range s.Knobs {
		if !ValidKnobName(k.Name) {
			return fmt.Errorf("companion %q: invalid knob name %q", s.ID, k.Name)
		}
	}
	switch s.Kind {
	case KindGitConfig:
		if s.FileTmpl == "" || s.FileEnvVar == "" {
			return fmt.Errorf("companion %q: git-config kind needs FileTmpl and FileEnvVar", s.ID)
		}
		for _, k := range s.Knobs {
			if k.EnvVar != "" {
				return fmt.Errorf("companion %q: git-config knob %q must not set EnvVar", s.ID, k.Name)
			}
		}
	case KindToken, KindConfigDir:
		if s.FileTmpl != "" || s.FileEnvVar != "" {
			return fmt.Errorf("companion %q: %s kind must not set FileTmpl/FileEnvVar", s.ID, s.Kind)
		}
		for _, k := range s.Knobs {
			if k.EnvVar == "" {
				return fmt.Errorf("companion %q: %s knob %q needs an EnvVar", s.ID, s.Kind, k.Name)
			}
		}
	default:
		return fmt.Errorf("companion %q: unknown kind %q", s.ID, s.Kind)
	}
	return nil
}

// SecretRef builds the secret-backend key for one companion knob value.
// Mirrors envprofile.SecretRef but is profile-scoped, not tool/account-scoped.
func SecretRef(profile, id, knob string) string {
	return "companion/" + profile + "/" + id + "/" + knob
}

var registry []Spec

// Register installs a companion Spec; called from each companion package's
// init(). It panics on a malformed Spec (a programming error).
func Register(s Spec) {
	if err := s.validate(); err != nil {
		panic("companion: " + err.Error())
	}
	registry = append(registry, s)
}

// All returns the registered companion specs in registration order. Callers
// must not mutate the result.
func All() []Spec {
	out := make([]Spec, len(registry))
	copy(out, registry)
	return out
}

// EnvVars returns every environment variable name any registered companion can
// set: FileEnvVar for git-config specs and Knob.EnvVar for token and config-dir
// knobs. A re-bind strips these from the fragment before re-applying the new
// profile's bindings, so a stale companion env line never lingers across a
// `kae pin <tool> <account>`.
func EnvVars() []string {
	var out []string
	for _, s := range registry {
		if s.FileEnvVar != "" {
			out = append(out, s.FileEnvVar)
		}
		for _, k := range s.Knobs {
			if k.EnvVar != "" {
				out = append(out, k.EnvVar)
			}
		}
	}
	return out
}

// For returns the spec for a companion id, or false.
func For(id string) (Spec, bool) {
	for _, s := range registry {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}
