package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// usageEndpoint is the ChatGPT-backend read the Codex client uses for plan
// windows. It is not a documented public API; the local session record is
// preferred, and this is only the fill-in when that record is absent.
const usageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

// sessionHead caps the rollout's first line, its session_meta, which carries
// the session's instructions and so can be long. A longer line names no creator.
const sessionHead = 1 << 20

// sessionTail is how much of a rollout file is read from the end. Rate-limit
// events are written as the session goes, so the latest one is at the tail;
// reading the whole file would drag a large session into every listing.
const sessionTail = 2 << 20

func (Codex) UsageHome(env adapter.Env) string { return codexHome(env) }

func (Codex) LocalUsage(home string) (usagelimit.Reading, bool) {
	files := newestJSONL(filepath.Join(home, "sessions"), 8)
	for _, path := range files {
		data, err := readTail(path, sessionTail)
		if err != nil {
			continue
		}
		windows, ok := newestRateLimits(data)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		return usagelimit.Reading{Path: path, ModTime: info.ModTime(), Windows: windows}, true
	}
	return usagelimit.Reading{}, false
}

// UsageCreator reads payload.creator_account_id from the rollout's first line
// when that line is session_meta: the account of the login that created the
// session, not of each reading in it. Rollouts of 0.154 and earlier have none.
func (Codex) UsageCreator(path string) (adapter.ResidentAccount, bool) {
	line, ok := firstLine(path, sessionHead)
	if !ok {
		return adapter.ResidentAccount{}, false
	}
	var doc struct {
		Type    string `json:"type"`
		Payload struct {
			CreatorAccountID string `json:"creator_account_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &doc) != nil || doc.Type != "session_meta" {
		return adapter.ResidentAccount{}, false
	}
	return adapter.NewResidentAccount(doc.Payload.CreatorAccountID)
}

func (Codex) ProbeUsage(payload []byte, now time.Time) (*http.Request, bool) {
	token, accountID, expires, ok := codexAccess(payload)
	if !ok || !expires.After(now) || !headerSafe(token) || (accountID != "" && !headerSafe(accountID)) {
		return nil, false
	}
	req, err := http.NewRequest(http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}
	return req, true
}

func (Codex) ParseUsageBody(body []byte) (usagelimit.Reading, bool) {
	windows, ok := windowsFromCodexJSON(body)
	if !ok {
		return usagelimit.Reading{}, false
	}
	return usagelimit.Reading{Windows: windows}, true
}

func codexAccess(payload []byte) (token, accountID string, expires time.Time, ok bool) {
	var doc struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.Tokens.AccessToken == "" {
		return "", "", time.Time{}, false
	}
	expires, ok = freshness.JWTExpiry(doc.Tokens.AccessToken)
	if !ok {
		return "", "", time.Time{}, false
	}
	return doc.Tokens.AccessToken, doc.Tokens.AccountID, expires, true
}

func headerSafe(s string) bool {
	return !strings.ContainsAny(s, " \r\n")
}

type rateWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	WindowMinutes      int     `json:"window_minutes"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetsAt           float64 `json:"resets_at"`
}

func (w *rateWindow) window() (usagelimit.Window, bool) {
	if w == nil {
		return usagelimit.Window{}, false
	}
	minutes := w.WindowMinutes
	if minutes == 0 && w.LimitWindowSeconds > 0 {
		minutes = w.LimitWindowSeconds / 60
	}
	if w.ResetsAt <= 0 && minutes == 0 && w.UsedPercent == 0 {
		return usagelimit.Window{}, false
	}
	id := usagelimit.IDForMinutes(minutes)
	if id == "" {
		return usagelimit.Window{}, false
	}
	return usagelimit.Window{
		ID: id, UsedPercent: w.UsedPercent, Minutes: minutes,
		ResetsAt: freshness.EpochToTime(w.ResetsAt),
	}, true
}

func windowsFromPair(primary, secondary *rateWindow) ([]usagelimit.Window, bool) {
	var windows []usagelimit.Window
	if w, ok := primary.window(); ok {
		windows = append(windows, w)
	}
	if w, ok := secondary.window(); ok {
		windows = append(windows, w)
	}
	return windows, len(windows) > 0
}

// windowsFromCodexJSON accepts both shapes Codex leaves behind: a session
// event's rate_limits (primary / secondary, minutes) and the usage response's
// rate_limit (primary_window / secondary_window, seconds).
func windowsFromCodexJSON(data []byte) ([]usagelimit.Window, bool) {
	var event struct {
		Payload struct {
			RateLimits *struct {
				Primary   *rateWindow `json:"primary"`
				Secondary *rateWindow `json:"secondary"`
			} `json:"rate_limits"`
		} `json:"payload"`
		RateLimit *struct {
			Primary   *rateWindow `json:"primary_window"`
			Secondary *rateWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, false
	}
	if event.Payload.RateLimits != nil {
		if windows, ok := windowsFromPair(event.Payload.RateLimits.Primary, event.Payload.RateLimits.Secondary); ok {
			return windows, true
		}
	}
	if event.RateLimit != nil {
		return windowsFromPair(event.RateLimit.Primary, event.RateLimit.Secondary)
	}
	return nil, false
}

func newestRateLimits(tail []byte) ([]usagelimit.Window, bool) {
	lines := strings.Split(string(tail), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, "rate_limit") {
			continue
		}
		if windows, ok := windowsFromCodexJSON([]byte(line)); ok {
			return windows, true
		}
	}
	return nil, false
}

func newestJSONL(dir string, n int) []string {
	type hit struct {
		mod  time.Time
		path string
	}
	var hits []hit
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		hits = append(hits, hit{mod: info.ModTime(), path: path})
		return nil
	})
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.path
	}
	return out
}

// firstLine reads path's first line, without its newline. ok is false when the
// file cannot be read or the line is longer than limit.
func firstLine(path string, limit int64) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	line, err := bufio.NewReader(io.LimitReader(f, limit+1)).ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, false
	}
	line = bytes.TrimRight(line, "\r\n")
	if int64(len(line)) > limit {
		return nil, false
	}
	return line, true
}

func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > n {
		if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

var _ adapter.UsageCreator = Codex{}
