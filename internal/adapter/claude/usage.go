package claude

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// usageExactFile is the subscription-window record Claude Code writes in its
// config directory. The sibling names with a random suffix are its temp files;
// only the stable name is a record.
const usageExactFile = "usage-exact.json"

// usageEndpoint is the read Claude Code's own usage view makes. The beta
// header and the client User-Agent are what that view sends; a different
// agent is throttled before it returns the windows.
const (
	usageEndpoint = "https://api.anthropic.com/api/oauth/usage"
	usageBeta     = "oauth-2025-04-20"
)

func (Claude) UsageHome(env adapter.Env) string { return configDir(env) }

func (Claude) LocalUsage(home string) (usagelimit.Reading, bool) {
	path := filepath.Join(home, usageExactFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return usagelimit.Reading{}, false
	}
	windows, ok := parseUsageExact(data)
	if !ok {
		return usagelimit.Reading{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return usagelimit.Reading{}, false
	}
	return usagelimit.Reading{Path: path, ModTime: info.ModTime(), Windows: windows}, true
}

func parseUsageExact(data []byte) ([]usagelimit.Window, bool) {
	var doc struct {
		FiveHour *usageExactWindow `json:"fiveHour"`
		SevenDay *usageExactWindow `json:"sevenDay"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	var windows []usagelimit.Window
	if w, ok := doc.FiveHour.window(constants.UsageWindowFiveHour, 300); ok {
		windows = append(windows, w)
	}
	if w, ok := doc.SevenDay.window(constants.UsageWindowSevenDay, 10080); ok {
		windows = append(windows, w)
	}
	return windows, len(windows) > 0
}

type usageExactWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    float64 `json:"resetsAt"`
}

func (w *usageExactWindow) window(id string, minutes int) (usagelimit.Window, bool) {
	if w == nil {
		return usagelimit.Window{}, false
	}
	return usagelimit.Window{
		ID: id, UsedPercent: w.UsedPercent, Minutes: minutes,
		ResetsAt: freshness.EpochToTime(w.ResetsAt),
	}, true
}

func (Claude) ProbeUsage(payload []byte, now time.Time) (*http.Request, bool) {
	token, expires, ok := claudeAccess(payload)
	if !ok || !expires.After(now) || !headerSafe(token) {
		return nil, false
	}
	req, err := http.NewRequest(http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", usageBeta)
	req.Header.Set("User-Agent", "claude-code/"+Claude{}.VerifiedVersion())
	return req, true
}

func (Claude) ParseUsageBody(body []byte) (usagelimit.Reading, bool) {
	var doc struct {
		FiveHour *apiUsageWindow `json:"five_hour"`
		SevenDay *apiUsageWindow `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return usagelimit.Reading{}, false
	}
	var windows []usagelimit.Window
	if w, ok := doc.FiveHour.window(constants.UsageWindowFiveHour, 300); ok {
		windows = append(windows, w)
	}
	if w, ok := doc.SevenDay.window(constants.UsageWindowSevenDay, 10080); ok {
		windows = append(windows, w)
	}
	if len(windows) == 0 {
		return usagelimit.Reading{}, false
	}
	return usagelimit.Reading{Windows: windows}, true
}

type apiUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func (w *apiUsageWindow) window(id string, minutes int) (usagelimit.Window, bool) {
	if w == nil || w.Utilization == nil {
		return usagelimit.Window{}, false
	}
	out := usagelimit.Window{ID: id, UsedPercent: *w.Utilization, Minutes: minutes}
	if w.ResetsAt != "" {
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			out.ResetsAt = t.UTC()
		}
	}
	return out, true
}

// claudeAccess reads the access token and its expiry from either nesting the
// credential is stored in: the keychain wrapper, or the inner object the file
// driver snapshots.
func claudeAccess(payload []byte) (token string, expires time.Time, ok bool) {
	root, ok := freshness.DecodeObject(payload)
	if !ok {
		return "", time.Time{}, false
	}
	obj := root
	if inner, exists := root["claudeAiOauth"]; exists {
		if nested, nestedOK := freshness.DecodeObject(inner); nestedOK {
			obj = nested
		}
	}
	var access string
	if err := json.Unmarshal(obj["accessToken"], &access); err != nil || access == "" {
		return "", time.Time{}, false
	}
	expires = freshness.EpochToTime(freshness.NumberFrom(obj["expiresAt"]))
	if expires.IsZero() {
		return "", time.Time{}, false
	}
	return access, expires, true
}

func headerSafe(s string) bool {
	return s != "" && !strings.ContainsAny(s, " \r\n")
}
