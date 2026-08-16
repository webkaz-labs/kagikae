package adapter_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// fingerprintRow matches one row of the literal table in docs/VALIDATION.md
// § "Upstream Literal Fingerprints": | <tool> | `<literal>` | <count> |
var fingerprintRow = regexp.MustCompile("(?m)^\\| (\\w+) \\| `([^`]+)` \\| (\\d+) \\|$")

// artifactRow matches one row of the artifact table in the same section:
// | <tool> | `<path>` <prose> | `<version>` |
// A backticked final cell is what keeps it off the literal table above (whose last
// cell is a bare number) and off the assumption tables (whose last cell is prose).
var artifactRow = regexp.MustCompile("(?m)^\\| (\\w+) \\| `[^`]+`[^|]*\\| `([^`]+)` \\|$")

// fingerprintExclusions names every tool with no fingerprintable artifact, and why.
// A tool that is in neither this map nor the table fails the parse half, so a new
// adapter cannot arrive without either fingerprints or a recorded reason.
var fingerprintExclusions = map[string]string{
	constants.ToolCodex: "stripped Rust, and `compute_store_key` exists in two modules so a " +
		"symbol grep finds the MCP-OAuth one first; its instrument is the public source at " +
		"tag rust-v<VerifiedVersion()>",
}

// fingerprintArtifacts is a tool's artifact path, with versionToken standing in for
// the version the table records. Relative paths resolve against $HOME. Mirrors the
// artifact table in docs/VALIDATION.md § "Upstream Literal Fingerprints".
//
// The version is **pinned from the table, never guessed** — picking the newest match
// instead was tried and read the wrong build for two tools (docs/VALIDATION.md
// § "Upstream Literal Fingerprints" records what it did).
//
// This header used to say an upgrade therefore shows up here as "not installed"
// against the recorded version. **It does not, for any tool that keeps its old
// builds** — measured 2026-08-16, when the machine had been running claude 2.1.233
// for some time while the table said 2.1.220: `~/.local/share/claude/versions/` held
// 2.1.220, 2.1.221, 2.1.228 and 2.1.233 together, so this test opened the recorded
// one, found it, and passed. A green run therefore means "the recorded build still
// says what the table says", never "the installed tools agree with the table", and
// the two stop being the same answer the moment a version sits. That an old bundle
// survives is also what makes a version bump cheap to investigate
// (.claude/skills/upstream-auth-drift/references/measuring.md calls the pair on disk
// the highest-yield moment), so this is a property to know rather than one to fix
// here. `doctor`'s upstream_version check is what watches for the upgrade itself —
// and it compares major and minor only, so it is silent across exactly the range
// this defeat was measured in.
//
// The `.local/share` prefixes are written as measured, not resolved through
// XDG_DATA_HOME: whether these installers honour that variable for their own install
// location is unmeasured, and this file is the wrong place to guess.
var fingerprintArtifacts = map[string]string{
	constants.ToolClaude:   ".local/share/claude/versions/" + versionToken,
	constants.ToolCursor:   ".local/share/cursor-agent/versions/" + versionToken,
	constants.ToolCopilot:  ".copilot/pkg/universal/" + versionToken + "/app.js",
	constants.ToolOpencode: ".local/share/mise/installs/opencode/" + versionToken + "/opencode",
	// agy installs straight to /usr/local/bin with no per-version directory, so its
	// recorded version cannot be checked against the path. A version bump therefore
	// shows up here as moved counts rather than as a missing file.
	constants.ToolAgy: "/usr/local/bin/agy",
}

const versionToken = "<version>"

// fingerprintEnv opts into the half that reads upstream binaries. `mise run audit`
// sets it; `mise run check` does not, so a commit never waits on a 266 MB read and
// never depends on which tools the machine has.
const fingerprintEnv = "KAE_FINGERPRINT"

// TestUpstreamLiteralFingerprints checks that every literal kae depends on upstream
// owning still occurs in that tool's artifact the same number of times. It is the
// only signal that fires on an upstream release which keeps the layout
// byte-identical and changes the code around it — the failure shape that let
// switched claude sessions keep naming the previous account for a full release.
//
// The counts live in docs/VALIDATION.md and are **measured**, never derived from
// kae's constants: `Claude Code-credentials` and cursor's four service names occur
// zero times in their bundles because upstream composes them, so deriving the table
// would make its most load-bearing rows permanently red. Zero is a legitimate
// recorded value for the same reason (agy no longer names `google_accounts.json`,
// which kae still reads).
func TestUpstreamLiteralFingerprints(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "VALIDATION.md"))
	if err != nil {
		t.Fatal(err)
	}
	type fingerprint struct {
		literal string
		count   int
	}
	documented := map[string][]fingerprint{}
	seen := map[string]bool{}
	for _, m := range fingerprintRow.FindAllStringSubmatch(string(data), -1) {
		tool, literal := m[1], m[2]
		count, err := strconv.Atoi(m[3])
		if err != nil {
			t.Fatalf("%s %q: count %q is not a number", tool, literal, m[3])
		}
		if key := tool + "\x00" + literal; seen[key] {
			t.Errorf("%s has two rows for %q; one of them is unmaintained", tool, literal)
		} else {
			seen[key] = true
		}
		documented[tool] = append(documented[tool], fingerprint{literal, count})
	}
	if len(documented) == 0 {
		t.Fatal("parsed no rows from the Upstream Literal Fingerprints table " +
			"(its shape changed; fix fingerprintRow or the table)")
	}
	for tool := range documented {
		if _, err := adapter.ForTool(tool); err != nil {
			t.Errorf("the fingerprint table has rows for %q, which is not a kae tool", tool)
		}
	}
	for _, tool := range constants.Tools {
		if len(documented[tool]) > 0 {
			continue
		}
		if reason, ok := fingerprintExclusions[tool]; ok {
			t.Logf("%s: no fingerprints by design — %s", tool, reason)
			continue
		}
		t.Errorf("%s has no fingerprints in docs/VALIDATION.md and no recorded reason; "+
			"measure its literals or add it to fingerprintExclusions with why", tool)
	}

	if os.Getenv(fingerprintEnv) == "" {
		t.Skipf("table parsed; set %s=1 (or run `mise run audit`) to count against the "+
			"installed tools", fingerprintEnv)
	}

	versions := map[string]string{}
	for _, m := range artifactRow.FindAllStringSubmatch(string(data), -1) {
		// Same duplicate guard as the literal table above: a map write would resolve
		// two rows for one tool to whichever comes last in the file, so a stale row
		// left by a re-measure would silently decide which artifact gets read.
		if _, ok := versions[m[1]]; ok {
			t.Errorf("the artifact table has two rows for %s; one of them is unmaintained", m[1])
		}
		versions[m[1]] = m[2]
	}
	if len(versions) != len(documented) {
		t.Fatalf("the artifact table names %d tools and the literal table %d; every "+
			"fingerprinted tool needs the version its counts were measured on "+
			"(artifact table: %v)", len(versions), len(documented), versions)
	}

	tools := make([]string, 0, len(documented))
	for tool := range documented {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		artifact, err := artifactPath(fingerprintArtifacts[tool], versions[tool])
		if err != nil {
			// Never a skip. A fingerprint run that passes because it found nothing
			// to read reports "the assumptions hold" on no evidence at all.
			t.Errorf("%s: %v", tool, err)
			continue
		}
		t.Logf("%s: reading %s", tool, artifact)
		literals := make([][]byte, len(documented[tool]))
		for i, fp := range documented[tool] {
			literals[i] = []byte(fp.literal)
		}
		counts, err := countAll(artifact, literals)
		if err != nil {
			t.Errorf("%s: reading %s: %v", tool, artifact, err)
			continue
		}
		for i, fp := range documented[tool] {
			if counts[i] != fp.count {
				t.Errorf("%s: %q occurs %d times in %s, docs/VALIDATION.md records %d — "+
					"the upstream code around this assumption moved; work that tool's rows in "+
					"the Upstream Behaviour Assumptions table, then update the count",
					tool, fp.literal, counts[i], artifact, fp.count)
			}
		}
	}
}

// artifactPath fills the recorded version into the tool's path. The error names the
// exact path, because the caller turns it into a failure and that path is where the
// operator looks — an upgraded tool is the expected reason for it to be gone.
func artifactPath(pattern, version string) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("no artifact path is recorded in fingerprintArtifacts")
	}
	if version == "" {
		return "", fmt.Errorf("no version recorded in the artifact table")
	}
	path := strings.ReplaceAll(pattern, versionToken, version)
	if !filepath.IsAbs(path) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path)
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("not installed: %s — if the tool was upgraded, re-measure "+
			"the literals against the installed build and update both tables", path)
	}
	return path, nil
}

// countAll returns one count per literal, reading the artifact once — a file whole,
// a directory once per file (cursor ships thousands of webpack chunks).
//
// Reading whole costs the file's size in memory: 266 MB for claude's bundle, at
// release time, on a developer machine. That buys the thing that matters, which is
// that `bytes.Count` over one contiguous buffer is *exactly* the non-overlapping
// single pass `grep -Foa … | wc -l` reports — the procedure the table was measured
// with. A chunked reader has to carry bytes between reads, and this one got that
// carry wrong: it re-formed a match across the boundary out of bytes an earlier
// match had already consumed, so a literal with a border (`abab` over `ababab`)
// counted 2 where one pass counts 1. No literal in today's table has a border, so it
// was a latent miscount that would have been indistinguishable from the upstream
// drift this check exists to report. If an artifact ever grows big enough that
// holding it is a problem, the replacement is one pass counting every literal at
// once (Aho-Corasick), not a per-literal carry.
func countAll(path string, literals [][]byte) ([]int, error) {
	counts := make([]int, len(literals))
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return counts, addFileCounts(path, literals, counts)
	}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		return addFileCounts(p, literals, counts)
	})
	return counts, err
}

func addFileCounts(path string, literals [][]byte, counts []int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for i, literal := range literals {
		if len(literal) == 0 {
			return fmt.Errorf("empty literal")
		}
		counts[i] += bytes.Count(data, literal)
	}
	return nil
}

// TestFingerprintCountingSumsEveryFile covers what countAll adds on top of
// bytes.Count: the per-literal accumulation and the directory walk. Cursor's artifact
// is a directory of thousands of chunks, so a count that stopped at the first file,
// or that credited one literal's hits to another, would read as upstream drift on a
// tool that never moved.
func TestFingerprintCountingSumsEveryFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.js":            "auth.json auth.json COPILOT_HOME",
		"b.js":            "auth-json auth.json",
		"sub/c.js":        "COPILOT_HOME COPILOT_HOME",
		"sub/deep/d.js":   "nothing here",
		"sub/deep/e.json": "auth.json",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	literals := [][]byte{[]byte("auth.json"), []byte("COPILOT_HOME"), []byte("absent")}
	got, err := countAll(dir, literals)
	if err != nil {
		t.Fatal(err)
	}
	// auth.json: 2 + 1 + 1, and `auth-json` must not count for it. COPILOT_HOME: 1 + 2.
	if want := []int{4, 3, 0}; !slices.Equal(got, want) {
		t.Errorf("counted %v over the tree, want %v", got, want)
	}

	single := filepath.Join(dir, "a.js")
	if got, err := countAll(single, literals); err != nil {
		t.Fatal(err)
	} else if want := []int{2, 1, 0}; !slices.Equal(got, want) {
		t.Errorf("counted %v in one file, want %v", got, want)
	}
	if _, err := countAll(single, [][]byte{nil}); err == nil {
		t.Error("an empty literal must be an error, not a count of every position")
	}
	if _, err := countAll(filepath.Join(dir, "missing"), literals); err == nil {
		t.Error("a missing artifact must be an error, not a zero count")
	}
}
