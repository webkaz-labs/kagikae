package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

func TestLocalUsageReadsTheStableFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "usage-exact.json")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("usage-exact.json", `{"schemaVersion":2,"fiveHour":{"usedPercent":16,"resetsAt":1800000000},"sevenDay":{"usedPercent":95,"resetsAt":1800003600}}`)
	// A temp sibling must not be read in place of the stable name.
	write("usage-exact.json.abc", `{"fiveHour":{"usedPercent":99,"resetsAt":1800000000}}`)
	reading, ok := (Claude{}).LocalUsage(home)
	if !ok || reading.Path != path {
		t.Fatalf("reading = %+v ok=%v", reading, ok)
	}
	if got, want := usagelimit.Format(reading.Windows), "5h 16% · 7d 95%"; got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
}

func TestProbeUsageRefusesAnExpiredToken(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	payload := []byte(`{"accessToken":"sk-ant-oat01-CANARY","expiresAt":1000}`)
	if _, ok := (Claude{}).ProbeUsage(payload, now); ok {
		t.Fatal("expired access token must not be sent")
	}
	payload = []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-CANARY","expiresAt":1800000000000}}`)
	req, ok := (Claude{}).ProbeUsage(payload, now)
	if !ok {
		t.Fatal("unexpired token must probe")
	}
	if req.URL.Host != "api.anthropic.com" || req.Header.Get("anthropic-beta") == "" {
		t.Fatalf("request = %v", req.URL)
	}
	if req.Header.Get("User-Agent") != "claude-code/"+(Claude{}).VerifiedVersion() {
		t.Fatalf("user agent = %q", req.Header.Get("User-Agent"))
	}
}

func TestParseUsageBody(t *testing.T) {
	body := []byte(`{"five_hour":{"utilization":10,"resets_at":"2026-07-01T00:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-07-08T00:00:00Z"},"seven_day_opus":null}`)
	reading, ok := (Claude{}).ParseUsageBody(body, time.Time{})
	if !ok {
		t.Fatal("expected windows")
	}
	if got, want := usagelimit.Format(reading.Windows), "5h 10% · 7d 20%"; got != want {
		t.Fatalf("Format = %q, want %q", got, want)
	}
	if reading.Windows[0].ID != constants.UsageWindowFiveHour {
		t.Fatalf("id = %s", reading.Windows[0].ID)
	}
}

func TestUsageHomeIsTheConfigDir(t *testing.T) {
	env := adapter.Env{Home: "/tmp/home", Getenv: func(string) string { return "" }}
	if got, want := (Claude{}).UsageHome(env), "/tmp/home/.claude"; got != want {
		t.Fatalf("UsageHome = %s, want %s", got, want)
	}
}
