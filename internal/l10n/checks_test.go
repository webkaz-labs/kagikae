package l10n

import "testing"

// The catalog checks hold vacuously while the catalog is empty, so each one is
// shown failing on a bad entry here.

func TestVerbsAgreeComparesArgumentsAndVerbs(t *testing.T) {
	cases := []struct {
		en, ja string
		want   bool
	}{
		{"cannot read %s: %v", "%s を読めません: %v", true},
		{"%s/%s is active", "%[2]s の %[1]s が有効です", true}, // reordered by explicit indices
		{"%s then %d", "%[2]d の後に %[1]s", true},
		{"%s then %d", "%d の後に %s", false},
		{"one %s", "ひとつ", false},
		{"one %s", "%s と %s", false},
		{"%q here", "%s です", false},
		{"%-10s|%d", "%-12s|%d", true}, // width may differ
		{"%*d", "%*d", true},
		{"100%% sure %s", "%s は確実です", true},
		{"wrap: %w", "包む: %w", true},
		{"wrap: %w", "包む: %v", false},
	}
	for _, tc := range cases {
		if got := verbsAgree(tc.en, tc.ja); got != tc.want {
			t.Errorf("verbsAgree(%q, %q) = %v, want %v", tc.en, tc.ja, got, tc.want)
		}
	}
}

func TestAmbiguousRunesFollowsTheWidthTable(t *testing.T) {
	for _, s := range []string{"設定は変更しません。", "アカウント・プロファイル", "（例: 1 つ）", "100％", "kae: warning: x"} {
		if rs := ambiguousRunes(s); len(rs) > 0 {
			t.Errorf("%q reported ambiguous %q", s, string(rs))
		}
	}
	for _, s := range []string{"a → b", "· 区切り", "省略…", "—", "×3", "※注", "①", "“引用”"} {
		if rs := ambiguousRunes(s); len(rs) == 0 {
			t.Errorf("%q has an East Asian Ambiguous character the check missed", s)
		}
	}
}

func TestHasProseSkipsDirectivesAndLinePrefixes(t *testing.T) {
	for _, s := range []string{"%s\n", "  %-*s %s\n", " · ", "kae:", "kae: warning: %v\n", "kae: note: %s", "%[2]q=%[1]d", "-"} {
		if hasProse(s) {
			t.Errorf("hasProse(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"Created %s\n", "kae: warning: could not %s", "kae: harvested %s", "  warning: %s\n", "%s places:\n"} {
		if !hasProse(s) {
			t.Errorf("hasProse(%q) = false, want true", s)
		}
	}
}
