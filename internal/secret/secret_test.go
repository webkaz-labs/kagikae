package secret

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

type fakeRunner struct {
	stdout string
	stderr string
	code   int
	name   string
	args   []string
	stdin  string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int) {
	f.name = name
	f.args = append([]string(nil), args...)
	return f.stdout, f.stderr, f.code
}

func (f *fakeRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	f.stdin = stdin
	return f.Run(ctx, name, args...)
}

func lookPathFound(string) (string, error)   { return "/usr/bin/x", nil }
func lookPathMissing(string) (string, error) { return "", errors.New("not found") }

func TestResolve(t *testing.T) {
	cases := []struct {
		configured, goos string
		lookPath         func(string) (string, error)
		wantName         string
		wantErr          bool
	}{
		{"auto", "darwin", lookPathMissing, BackendKeychain, false},
		{"auto", "linux", lookPathFound, BackendLibsecret, false},
		{"auto", "linux", lookPathMissing, "", true},
		{"keychain", "darwin", lookPathMissing, BackendKeychain, false},
		{"keychain", "linux", lookPathMissing, "", true},
		{"libsecret", "linux", lookPathFound, BackendLibsecret, false},
		{"libsecret", "linux", lookPathMissing, "", true},
		{"file", "linux", lookPathMissing, BackendFile, false},
		{"bogus", "linux", lookPathFound, "", true},
	}
	for _, tc := range cases {
		b, err := Resolve(tc.configured, tc.goos, t.TempDir(), tc.lookPath)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s/%s: expected error", tc.configured, tc.goos)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.configured, tc.goos, err)
		}
		if b.Name() != tc.wantName {
			t.Fatalf("%s/%s: got backend %s", tc.configured, tc.goos, b.Name())
		}
	}
}

func TestResolveUnavailableIsTyped(t *testing.T) {
	_, err := Resolve("auto", "linux", t.TempDir(), lookPathMissing)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestFileBackendRoundTrip(t *testing.T) {
	b := fileBackend{dir: t.TempDir()}
	ctx := context.Background()
	if _, found, err := b.Get(ctx, "claude/work/oauth"); err != nil || found {
		t.Fatalf("expected absent: found=%v err=%v", found, err)
	}
	payload := []byte(`{"token":"s3cret"}`)
	if err := b.Set(ctx, "claude/work/oauth", payload); err != nil {
		t.Fatal(err)
	}
	got, found, err := b.Get(ctx, "claude/work/oauth")
	if err != nil || !found || string(got) != string(payload) {
		t.Fatalf("round trip failed: %s %v %v", got, found, err)
	}
	path := filepath.Join(b.dir, "claude", "work", "oauth.secret")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode: %v", info.Mode())
	}
	raw, _ := os.ReadFile(path)
	if string(raw) == string(payload) {
		t.Fatal("payload stored without encoding")
	}
	if err := b.Delete(ctx, "claude/work/oauth"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "claude/work/oauth"); err != nil {
		t.Fatal("double delete should be safe:", err)
	}
}

func TestValidateKey(t *testing.T) {
	for _, bad := range []string{"", "/abs", "a//b", "a/../b", "x/.."} {
		if err := validateKey(bad); err == nil {
			t.Fatalf("expected invalid: %q", bad)
		}
	}
	if err := validateKey("backup/20260611T000000Z/claude/oauth"); err != nil {
		t.Fatal(err)
	}
}

func TestKeychainBackend(t *testing.T) {
	ctx := context.Background()
	b := keychainBackend{}
	payload := []byte(`{"v":1}`)
	encoded := base64.StdEncoding.EncodeToString(payload)

	fake := &fakeRunner{stdout: encoded + "\n"}
	runner.With(fake, func() {
		got, found, err := b.Get(ctx, "claude/work/oauth")
		if err != nil || !found || string(got) != string(payload) {
			t.Fatalf("get: %s %v %v", got, found, err)
		}
	})
	if fake.name != "security" || fake.args[0] != "find-generic-password" {
		t.Fatalf("unexpected command: %s %v", fake.name, fake.args)
	}

	fake = &fakeRunner{stderr: "security: ... could not be found ...", code: 44}
	runner.With(fake, func() {
		_, found, err := b.Get(ctx, "claude/work/oauth")
		if err != nil || found {
			t.Fatalf("expected not found: %v %v", found, err)
		}
	})

	fake = &fakeRunner{}
	runner.With(fake, func() {
		if err := b.Set(ctx, "claude/work/oauth", payload); err != nil {
			t.Fatal(err)
		}
	})
	if fake.args[len(fake.args)-1] != encoded {
		t.Fatalf("payload not base64 in argv: %v", fake.args)
	}

	fake = &fakeRunner{stderr: "could not be found", code: 44}
	runner.With(fake, func() {
		if err := b.Delete(ctx, "claude/work/oauth"); err != nil {
			t.Fatal("delete of missing entry should be safe:", err)
		}
	})
}

func TestLibsecretBackend(t *testing.T) {
	ctx := context.Background()
	b := libsecretBackend{}
	payload := []byte(`{"v":1}`)
	encoded := base64.StdEncoding.EncodeToString(payload)

	fake := &fakeRunner{stdout: encoded}
	runner.With(fake, func() {
		got, found, err := b.Get(ctx, "codex/work/auth")
		if err != nil || !found || string(got) != string(payload) {
			t.Fatalf("get: %s %v %v", got, found, err)
		}
	})

	// exit 1 with empty stderr means not found
	fake = &fakeRunner{code: 1}
	runner.With(fake, func() {
		_, found, err := b.Get(ctx, "codex/work/auth")
		if err != nil || found {
			t.Fatalf("expected not found: %v %v", found, err)
		}
	})

	// exit 1 with stderr means error
	fake = &fakeRunner{code: 1, stderr: "cannot connect to secret service"}
	runner.With(fake, func() {
		if _, _, err := b.Get(ctx, "codex/work/auth"); err == nil {
			t.Fatal("expected error")
		}
	})

	fake = &fakeRunner{}
	runner.With(fake, func() {
		if err := b.Set(ctx, "codex/work/auth", payload); err != nil {
			t.Fatal(err)
		}
	})
	if fake.stdin != encoded {
		t.Fatalf("payload must go via stdin, got argv=%v stdin=%q", fake.args, fake.stdin)
	}
	for _, arg := range fake.args {
		if arg == encoded {
			t.Fatal("payload leaked into argv")
		}
	}
}
