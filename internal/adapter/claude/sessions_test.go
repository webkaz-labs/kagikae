package claude

import (
	"math"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

func TestProjectDirNameReplacesEverythingButASCIIAlphanumerics(t *testing.T) {
	for _, tc := range []struct{ name, path, want string }{
		{"plain", "/Users/you/code/main-app", "-Users-you-code-main-app"},
		{"dot underscore space", "/Users/you/code/side.project_x y", "-Users-you-code-side-project-x-y"},
		// One dash per UTF-16 code unit: CJK and accented letters are not alphanumeric
		// and a character outside the BMP is two units.
		{"CJK", "/tmp/日本語", "-tmp----"},
		{"accent, composed", "/tmp/caféx", "-tmp-caf-x"},
		{"astral", "/tmp/\U0001F4A1x", "-tmp---x"},
		// A decomposed name is the composed name: the path is NFC first, so the
		// combining mark does not add a dash of its own.
		{"accent, decomposed", "/tmp/caféx", "-tmp-caf-x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProjectDirName(tc.path); got != tc.want {
				t.Fatalf("ProjectDirName(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
	if ProjectDirName("/tmp/café") != ProjectDirName(norm.NFC.String("/tmp/café")) {
		t.Fatal("NFD and NFC spellings of one path must name one directory")
	}
}

// A converted name of exactly 200 characters is kept whole; the 201st character
// is what makes it a truncation plus the hash.
func TestProjectDirNameTruncatesAtTwoHundredWithTheHash(t *testing.T) {
	exactly200 := "/" + strings.Repeat("a", 199)
	if got := ProjectDirName(exactly200); got != "-"+strings.Repeat("a", 199) || len(got) != 200 {
		t.Fatalf("200 characters: got %d characters %q", len(got), got)
	}
	over := "/" + strings.Repeat("a", 200)
	got := ProjectDirName(over)
	want := "-" + strings.Repeat("a", 199) + "-" + referenceSuffix(over)
	if got != want {
		t.Fatalf("201 characters:\n got %q\nwant %q", got, want)
	}
	if len(got) > 200+1+6 {
		t.Fatalf("the suffix is at most 6 characters: %q", got)
	}
}

// The goldens were measured against claude 2.1.284 on 2026-09-30: with a scratch
// CLAUDE_CONFIG_DIR holding a `projects/<name>` directory of an empty .jsonl,
// `claude project purge --dry-run <path>` found the project state under the
// name ProjectDirName gave for each of these paths.
func TestProjectDirNameMatchesNamesClaudeResolved(t *testing.T) {
	for _, tc := range []struct{ name, path, suffix string }{
		{"long ASCII", "/private/tmp/" + strings.Repeat("a", 250), "3zbt03"},
		{"long with CJK, astral and punctuation", "/private/tmp/" + strings.Repeat("b", 300) + "/日本語/\U0001F4A1/café.x_y z", "xoaafq"},
		{"long accented", "/private/tmp/" + strings.Repeat("é", 190), "q53u4z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ProjectDirName(tc.path)
			if !strings.HasSuffix(got, "-"+tc.suffix) || len(got) != 200+1+len(tc.suffix) {
				t.Fatalf("ProjectDirName = %q, want a 200-character body then -%s", got, tc.suffix)
			}
			if referenceSuffix(tc.path) != tc.suffix {
				t.Fatalf("the reference implementation disagrees with the measurement: %s", referenceSuffix(tc.path))
			}
		})
	}
}

func TestHashSuffix(t *testing.T) {
	for _, tc := range []struct {
		hash int32
		want string
	}{
		{math.MinInt32, "zik0zk"}, // the absolute value needs 64 bits
		{0, "0"},
		{35, "z"},
		{-36, "10"},
		{math.MaxInt32, "zik0zj"},
	} {
		if got := hashSuffix(tc.hash); got != tc.want {
			t.Errorf("hashSuffix(%d) = %q, want %q", tc.hash, got, tc.want)
		}
	}
}

// referenceSuffix is upstream's algorithm as JavaScript writes it,
// `h = (h << 5) - h + charCodeAt(i) | 0` over the path's UTF-16 units, written
// independently of javaHash: 64-bit arithmetic wrapped to 32 bits by masking.
func referenceSuffix(path string) string {
	var h int64
	for _, u := range utf16.Encode([]rune(norm.NFC.String(path))) {
		h = ((h << 5) - h + int64(u)) & 0xffffffff
	}
	if h >= 1<<31 {
		h -= 1 << 32
	}
	if h < 0 {
		h = -h
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if h == 0 {
		return "0"
	}
	var out []byte
	for ; h > 0; h /= 36 {
		out = append([]byte{digits[h%36]}, out...)
	}
	return string(out)
}

func TestJavaHashAgreesWithTheReferenceOverManyPaths(t *testing.T) {
	for i := 0; i < 500; i++ {
		path := "/x/" + strings.Repeat("é日", i%40) + strings.Repeat("q", i)
		want := referenceSuffix(path)
		if got := hashSuffix(javaHash(utf16.Encode([]rune(norm.NFC.String(path))))); got != want {
			t.Fatalf("path %d: %q, want %q", i, got, want)
		}
	}
}

func TestValidProjectDirName(t *testing.T) {
	for value, want := range map[string]bool{
		"work":                         true,
		"a_b-C9":                       true,
		strings.Repeat("x", 64):        true,
		"a b":                          false,
		strings.Repeat("x", 65):        false,
		"":                             false,
		"con":                          false,
		"COM1":                         false,
		"lpt9":                         false,
		"Nul":                          false,
		"com10":                        true, // only a single digit names a device
		"console":                      true,
		"a.b":                          false,
		"日本":                           false,
		"line\n":                       false,
		strings.Repeat("y", 64) + "\n": false,
	} {
		if got := ValidProjectDirName(value); got != want {
			t.Errorf("ValidProjectDirName(%q) = %v, want %v", value, got, want)
		}
	}
}
