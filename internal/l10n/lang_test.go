package l10n

import "testing"

func TestDetectFollowsCLISelectionOrder(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Language
	}{
		{"nothing set", nil, English},
		{"LANG ja_JP.UTF-8", map[string]string{"LANG": "ja_JP.UTF-8"}, Japanese},
		{"bare ja", map[string]string{"LANG": "ja"}, Japanese},
		{"ja-JP", map[string]string{"LANG": "ja-JP"}, Japanese},
		{"ja.UTF-8", map[string]string{"LANG": "ja.UTF-8"}, Japanese},
		{"ja@modifier", map[string]string{"LANG": "ja@x"}, Japanese},
		{"case-sensitive JA_JP", map[string]string{"LANG": "JA_JP.UTF-8"}, English},
		{"not a ja prefix", map[string]string{"LANG": "jam"}, English},
		{"en_US", map[string]string{"LANG": "en_US.UTF-8"}, English},
		{"LC_MESSAGES outranks LANG", map[string]string{"LC_MESSAGES": "ja_JP.UTF-8", "LANG": "en_US.UTF-8"}, Japanese},
		{"LC_ALL outranks LC_MESSAGES", map[string]string{"LC_ALL": "C", "LC_MESSAGES": "ja_JP.UTF-8"}, English},
		{"deciding variable wins even when English", map[string]string{"LC_ALL": "C", "LANG": "ja_JP.UTF-8"}, English},
		{"maintainer's locale", map[string]string{"LC_ALL": "ja_JP.UTF-8", "LANG": "en_US.UTF-8"}, Japanese},
		{"KAE_LANG=en outranks the locale", map[string]string{"KAE_LANG": "en", "LC_ALL": "ja_JP.UTF-8"}, English},
		{"KAE_LANG=ja outranks the locale", map[string]string{"KAE_LANG": "ja", "LC_ALL": "C"}, Japanese},
		{"empty value is unset", map[string]string{"KAE_LANG": "", "LC_ALL": "ja_JP.UTF-8"}, Japanese},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(func(name string) string { return tc.env[name] })
			if got != tc.want {
				t.Fatalf("Detect(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestSelectInJSONModeIsEnglishWhateverTheEnvironment(t *testing.T) {
	t.Cleanup(func() { Set(English) })
	japaneseEnv := func(name string) string {
		if name == EnvVar {
			return "ja"
		}
		return ""
	}
	if got := Select(true, japaneseEnv); got != English || Current() != English {
		t.Fatalf("JSON mode selected %v (current %v), want English", got, Current())
	}
	if got := Select(false, japaneseEnv); got != Japanese || Current() != Japanese {
		t.Fatalf("human mode selected %v (current %v), want Japanese", got, Current())
	}
}
