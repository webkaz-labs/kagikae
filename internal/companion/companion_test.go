package companion_test

import (
	"testing"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"

	// Blank imports register the bundled companion specs via their init().
	_ "github.com/webkaz-labs/kagikae/internal/companion/cloudflare"
	_ "github.com/webkaz-labs/kagikae/internal/companion/gh"
	_ "github.com/webkaz-labs/kagikae/internal/companion/git"
	_ "github.com/webkaz-labs/kagikae/internal/companion/kubectl"
)

func TestRegisteredCompanionsMatchConstants(t *testing.T) {
	specs := companion.All()
	if len(specs) != len(constants.Companions) {
		t.Fatalf("registered %d companions, constants lists %d", len(specs), len(constants.Companions))
	}
	for _, id := range constants.Companions {
		if _, ok := companion.For(id); !ok {
			t.Errorf("companion %q is in constants.Companions but not registered", id)
		}
	}
}

func TestSpecKindsAndKnobs(t *testing.T) {
	cases := []struct {
		id     string
		kind   companion.OverrideKind
		secret bool
		knob   string
		envVar string // expected EnvVar of knob; "" for git-config data knobs
	}{
		{constants.CompanionGit, companion.KindGitConfig, false, "email", ""},
		{constants.CompanionGH, companion.KindToken, true, "GH_TOKEN", "GH_TOKEN"},
		{constants.CompanionCloudflare, companion.KindToken, true, "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_TOKEN"},
		{constants.CompanionKubectl, companion.KindConfigDir, false, "KUBECONFIG", "KUBECONFIG"},
	}
	for _, tc := range cases {
		s, ok := companion.For(tc.id)
		if !ok {
			t.Errorf("%s not registered", tc.id)
			continue
		}
		if s.Kind != tc.kind {
			t.Errorf("%s kind = %q, want %q", tc.id, s.Kind, tc.kind)
		}
		if s.Secret() != tc.secret {
			t.Errorf("%s Secret() = %v, want %v", tc.id, s.Secret(), tc.secret)
		}
		k, ok := s.Knob(tc.knob)
		if !ok {
			t.Errorf("%s missing knob %q", tc.id, tc.knob)
			continue
		}
		if k.EnvVar != tc.envVar {
			t.Errorf("%s knob %q EnvVar = %q, want %q", tc.id, tc.knob, k.EnvVar, tc.envVar)
		}
	}
}

func TestGitFileDelivery(t *testing.T) {
	s, ok := companion.For(constants.CompanionGit)
	if !ok {
		t.Fatal("git not registered")
	}
	if s.FileEnvVar != "GIT_CONFIG_GLOBAL" {
		t.Errorf("git FileEnvVar = %q, want GIT_CONFIG_GLOBAL", s.FileEnvVar)
	}
	if s.FileTmpl == "" {
		t.Error("git FileTmpl is empty")
	}
}

func TestSecretRef(t *testing.T) {
	got := companion.SecretRef("main", "gh", "GH_TOKEN")
	if want := "companion/main/gh/GH_TOKEN"; got != want {
		t.Errorf("SecretRef = %q, want %q", got, want)
	}
}

func TestValidKnobName(t *testing.T) {
	for _, ok := range []string{"email", "GH_TOKEN", "_x", "KUBECONFIG"} {
		if !companion.ValidKnobName(ok) {
			t.Errorf("ValidKnobName(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "1abc", "has space", "a/b", "a-b"} {
		if companion.ValidKnobName(bad) {
			t.Errorf("ValidKnobName(%q) = true, want false", bad)
		}
	}
}

func TestRegisterPanicsOnMalformedSpec(t *testing.T) {
	cases := map[string]companion.Spec{
		"unknown id":               {ID: "nope", Binary: "nope", Kind: companion.KindToken, Knobs: []companion.Knob{{Name: "X", EnvVar: "X"}}},
		"empty binary":             {ID: constants.CompanionGH, Kind: companion.KindToken, Knobs: []companion.Knob{{Name: "X", EnvVar: "X"}}},
		"unknown kind":             {ID: constants.CompanionGH, Binary: "gh", Kind: companion.OverrideKind("bogus"), Knobs: []companion.Knob{{Name: "X", EnvVar: "X"}}},
		"token without envvar":     {ID: constants.CompanionGH, Binary: "gh", Kind: companion.KindToken, Knobs: []companion.Knob{{Name: "GH_TOKEN"}}},
		"git-config with envvar":   {ID: constants.CompanionGit, Binary: "git", Kind: companion.KindGitConfig, FileTmpl: "x", FileEnvVar: "GIT_CONFIG_GLOBAL", Knobs: []companion.Knob{{Name: "email", EnvVar: "OOPS"}}},
		"git-config no FileTmpl":   {ID: constants.CompanionGit, Binary: "git", Kind: companion.KindGitConfig, FileEnvVar: "GIT_CONFIG_GLOBAL", Knobs: []companion.Knob{{Name: "email"}}},
		"git-config no FileEnvVar": {ID: constants.CompanionGit, Binary: "git", Kind: companion.KindGitConfig, FileTmpl: "x", Knobs: []companion.Knob{{Name: "email"}}},
		"no knobs":                 {ID: constants.CompanionKubectl, Binary: "kubectl", Kind: companion.KindConfigDir},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%s) did not panic", name)
				}
			}()
			companion.Register(spec)
		})
	}
}
