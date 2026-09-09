package portable

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

// Run the public commands against a foreign tree, including a deliberately
// unusable Go workspace. Source lookup must not follow the inspected project.
func TestForeignProject(t *testing.T) {
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "foreign project")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GOENV", "off")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir, name string, args ...string) commandrun.Result {
		t.Helper()
		result, err := (commandrun.Command{Name: name, Args: args, Dir: dir, Timeout: time.Minute}).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	assert := func(result commandrun.Result, success bool, text string) {
		t.Helper()
		if (result.ExitCode == 0) != success || !strings.Contains(result.Stdout+result.Stderr, text) {
			t.Fatalf("exit %d: %s%s; wanted success=%v, %q", result.ExitCode, result.Stdout, result.Stderr, success, text)
		}
	}
	check := func() commandrun.Result {
		return run(root, "bash", filepath.Join(source, "scripts/check-docs.sh"), "--portable", root)
	}
	write("go.mod", "not a module\n")
	write("go.work", "not a workspace\n")
	write("README.md", "[guide](docs/GUIDE.md)\n[directory](docs/)\n\ndocs/GUIDE.md § Usage\n")
	write("docs/GUIDE.md", "# Guide\n\n## Usage\n")
	assert(check(), true, "2 links, 1 section citations checked")
	write("README.md", "[bad](missing.md)\n")
	assert(check(), false, "link target does not exist")
	write("README.md", "docs/GUIDE.md § Missing\n")
	assert(check(), false, "declares no section")
	write("README.md", "# Plain document\n")
	assert(check(), true, "no checkable references")
	assert(run(root, "bash", filepath.Join(source, "scripts/check-docs.sh"), "--portable", filepath.Join(root, "missing")), false, "")

	binary := filepath.Join(t.TempDir(), "docscan")
	assert(run(source, "go", "build", "-o", binary, "./scripts/docscan"), true, "")
	assert(run(root, binary, "-portable", "-root", root), true, "no anchors; comparison unavailable")
	paragraph := "Widget preserves the selected configuration across every operation and keeps the original value available for later inspection while another command reads the same stable configuration without changing any part of it.\n"
	write("README.md", paragraph)
	write("docs/GUIDE.md", paragraph)
	write("terms.md", "## Vocabulary\n\n| Term | Meaning |\n|---|---|\n| Widget | Example |\n")
	assert(run(root, binary, "-portable", "-root", root, "-glossary", "terms.md", "-glossary-sections", "Vocabulary"), true, "1 candidate pairs compared")
	assert(run(root, binary, "-portable", "-root", root, "-glossary", "absent.md"), false, "absent.md")

	assert(run(root, "git", "init", "-b", "trunk"), true, "")
	assert(run(root, "git", "add", "."), true, "")
	assert(run(root, "git", "-c", "user.name=Fixture", "-c", "user.email=you@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "fixture"), true, "")
	write("README.md", "the two commands in this document\n")
	assert(run(root, "bash", filepath.Join(source, "scripts/sweep-quantities.sh"), "--portable", "trunk"), true, "two commands")
	assert(run(root, "bash", filepath.Join(source, "scripts/sweep-quantities.sh"), "--portable", "missing"), false, "no commit named")
}
