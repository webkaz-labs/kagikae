package claude

import (
	"fmt"
	"testing"
	"time"
)

func TestClaudeFreshnessNestedAndFlat(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ms := exp.UnixMilli()
	nested := fmt.Appendf(nil, `{"claudeAiOauth":{"expiresAt":%d,"refreshToken":"r"}}`, ms)
	flat := fmt.Appendf(nil, `{"expiresAt":%d,"refreshToken":"r"}`, ms)
	for name, payload := range map[string][]byte{"nested": nested, "flat": flat} {
		info := Claude{}.Freshness(payload)
		if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
			t.Fatalf("%s: %+v (want exp %v, refresh true)", name, info, exp)
		}
	}
}

func TestClaudeFreshnessNoRefresh(t *testing.T) {
	info := Claude{}.Freshness([]byte(`{"claudeAiOauth":{"expiresAt":1000000000000}}`))
	if !info.Known || info.HasRefresh || info.ExpiresAt.IsZero() {
		t.Fatalf("Freshness = %+v (want Known, no refresh, dated)", info)
	}
}

func TestClaudeFreshnessUnparseable(t *testing.T) {
	if info := (Claude{}).Freshness([]byte("not json")); info.Known {
		t.Fatalf("Freshness on garbage = %+v (want Known=false)", info)
	}
}
