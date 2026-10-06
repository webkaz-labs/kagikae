package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

func TestLocalUsageUsesTheNewestSessionTail(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "27")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(dir, "rollout-old.jsonl")
	newer := filepath.Join(dir, "rollout-new.jsonl")
	write := func(path, line string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write(older, `{"payload":{"rate_limits":{"primary":{"used_percent":90,"window_minutes":300,"resets_at":1800000000},"secondary":{"used_percent":80,"window_minutes":10080,"resets_at":1800003600}}}}`, time.Unix(100, 0))
	write(newer, `{"payload":{"type":"event","rate_limits":{"primary":{"used_percent":1,"window_minutes":10080,"resets_at":1800000000},"secondary":null}}}`, time.Unix(200, 0))
	reading, ok := (Codex{}).LocalUsage(home)
	if !ok || reading.Path != newer {
		t.Fatalf("reading = %+v ok=%v", reading, ok)
	}
	if got, want := usagelimit.Format(reading.Windows), "7d 1%"; got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}

func TestParseUsageBodyReadsWindowSeconds(t *testing.T) {
	body := []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"resets_at":1800000000},"secondary_window":{"used_percent":5,"limit_window_seconds":604800,"resets_at":1800003600}}}`)
	reading, ok := (Codex{}).ParseUsageBody(body)
	if !ok {
		t.Fatal("expected windows")
	}
	if got, want := usagelimit.Format(reading.Windows), "5h 25% · 7d 5%"; got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}

func TestUsageHomeIsCodexHome(t *testing.T) {
	env := adapter.Env{Home: "/tmp/home", Getenv: func(string) string { return "" }}
	if got, want := (Codex{}).UsageHome(env), "/tmp/home/.codex"; got != want {
		t.Fatalf("UsageHome = %s, want %s", got, want)
	}
}

func TestUsageCreatorReadsTheFirstSessionMeta(t *testing.T) {
	const creator = "acct-creator-0000"
	dir := t.TempDir()
	event := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":1,"window_minutes":300,"resets_at":1800000000}}}}`
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"creator", `{"type":"session_meta","payload":{"id":"t","creator_account_id":"` + creator + `"}}` + "\n" + event, true},
		{"first session_meta wins", `{"type":"session_meta","payload":{"id":"t"}}` + "\n" + `{"type":"session_meta","payload":{"creator_account_id":"` + creator + `"}}`, false},
		{"no creator (0.154 and earlier)", `{"type":"session_meta","payload":{"id":"t"}}` + "\n" + event, false},
		{"no session_meta", event + "\n" + `{"type":"response_item","payload":{"text":"session_meta","creator_account_id":"` + creator + `"}}`, false},
		{"empty creator", `{"type":"session_meta","payload":{"creator_account_id":""}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".jsonl")
			if err := os.WriteFile(path, []byte(tc.body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := (Codex{}).UsageCreator(path)
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v", ok, tc.want)
			}
			if !ok {
				return
			}
			// The key is the one CredentialAccount reads for the same id.
			same, _ := (Codex{}).CredentialAccount([]byte(`{"tokens":{"account_id":"` + creator + `"}}`))
			other, _ := (Codex{}).CredentialAccount([]byte(`{"tokens":{"account_id":"acct-other-1111"}}`))
			if !got.Same(same) || got.Same(other) {
				t.Fatalf("creator key does not compare with the credential's")
			}
		})
	}
	if _, ok := (Codex{}).UsageCreator(filepath.Join(dir, "missing.jsonl")); ok {
		t.Fatal("a missing file names no creator")
	}
}
