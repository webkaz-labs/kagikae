package adapter_test

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
// The version is **pinned from the table, never guessed**. Choosing the newest
// match instead was tried and is worse than useless: on the machine this was
// written, "newest by modification time" read copilot 1.0.36 while 1.0.61 was the
// installed CLI, and mise's `opencode/1` version alias beat `opencode/1.17.4` — both
// of which report a pile of moved counts for a tool that never changed, which is the
// noise this whole check is supposed to be quieter than. Sorting by version instead
// would need a comparator per tool (claude semver, cursor a timestamp, agy no
// version in the path at all).
//
// So an upgrade shows up as "not installed" against the recorded version, whose
// remedy is exactly what an upgrade calls for: re-measure and update both tables.
// `doctor`'s upstream_version check is the one that watches for the upgrade itself.
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
		for _, fp := range documented[tool] {
			got, err := countInPath(artifact, []byte(fp.literal))
			if err != nil {
				t.Errorf("%s %q: %v", tool, fp.literal, err)
				continue
			}
			if got != fp.count {
				t.Errorf("%s: %q occurs %d times in %s, docs/VALIDATION.md records %d — "+
					"the upstream code around this assumption moved; work that tool's rows in "+
					"the Upstream Behaviour Assumptions table, then update the count",
					tool, fp.literal, got, artifact, fp.count)
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

// countInPath counts occurrences of literal in a file, or summed over every file
// under a directory (cursor ships thousands of webpack chunks).
func countInPath(path string, literal []byte) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return countInFile(path, literal)
	}
	total := 0
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		n, err := countInFile(p, literal)
		total += n
		return err
	})
	return total, err
}

func countInFile(path string, literal []byte) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return countReader(f, literal, 1<<20)
}

// countReader streams so a 266 MB bundle costs one buffer rather than its own size in
// memory. The window keeps len(literal)-1 bytes of the previous read, so an
// occurrence split across two reads is found — and counted once, because no whole
// occurrence fits in a carry that short. chunk is a parameter only so a test can
// shrink it below a literal's length; production always passes 1 MiB.
func countReader(r io.Reader, literal []byte, chunk int) (int, error) {
	if len(literal) == 0 {
		return 0, fmt.Errorf("empty literal")
	}
	carry := len(literal) - 1
	buf := make([]byte, chunk+carry)
	total, held := 0, 0
	for {
		n, err := r.Read(buf[held:])
		if n > 0 {
			window := buf[:held+n]
			total += bytes.Count(window, literal)
			held = min(carry, len(window))
			copy(buf, window[len(window)-held:])
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// TestFingerprintCountingSpansReadBoundaries is the fingerprint check's own teeth: an
// undercount from a literal straddling two reads would read as upstream drift on a
// tool that never moved, which is indistinguishable from the signal the check exists
// to produce. Every case runs with a chunk smaller than the literal, so the carry is
// exercised on every read rather than once in a 266 MB file.
func TestFingerprintCountingSpansReadBoundaries(t *testing.T) {
	const lit = "auth.json"
	for _, tc := range []struct {
		name string
		data string
		want int
	}{
		{"absent", strings.Repeat("x", 100), 0},
		{"one", "xxauth.jsonxx", 1},
		{"adjacent", "auth.jsonauth.json", 2},
		{"at both ends", "auth.json" + strings.Repeat("y", 50) + "auth.json", 2},
		{"truncated tail", "xxauth.jso", 0},
		{"literal-ish neighbour", "auth-json auth.json", 1},
		{"dense", strings.Repeat("auth.json|", 40), 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{1, 2, 4, len(lit) - 1, len(lit), len(lit) + 1, 1024} {
				got, err := countReader(strings.NewReader(tc.data), []byte(lit), chunk)
				if err != nil {
					t.Fatalf("chunk %d: %v", chunk, err)
				}
				if got != tc.want {
					t.Errorf("chunk %d: counted %d, want %d", chunk, got, tc.want)
				}
			}
		})
	}
	if _, err := countReader(strings.NewReader("x"), nil, 8); err == nil {
		t.Error("an empty literal must be an error, not a count of every position")
	}
}
