package freshness

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// makeJWT builds a minimal unsigned-looking JWT whose payload carries exp.
func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, exp))
	return header + "." + payload + ".sig"
}

func TestClaudeNestedAndFlat(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ms := exp.UnixMilli()
	nested := fmt.Appendf(nil, `{"claudeAiOauth":{"expiresAt":%d,"refreshToken":"r"}}`, ms)
	flat := fmt.Appendf(nil, `{"expiresAt":%d,"refreshToken":"r"}`, ms)
	for name, payload := range map[string][]byte{"nested": nested, "flat": flat} {
		info := Inspect(constants.ToolClaude, payload)
		if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
			t.Fatalf("%s: %+v (want exp %v)", name, info, exp)
		}
	}
}

func TestClaudeNoRefresh(t *testing.T) {
	info := Inspect(constants.ToolClaude, []byte(`{"claudeAiOauth":{"expiresAt":1000000000000}}`))
	if !info.Known || info.HasRefresh || info.ExpiresAt.IsZero() {
		t.Fatalf("unexpected: %+v", info)
	}
}

func TestCodexJWTExpiryAndRefresh(t *testing.T) {
	exp := time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC)
	payload := fmt.Appendf(nil, `{"tokens":{"access_token":%q,"refresh_token":"r"}}`, makeJWT(exp.Unix()))
	info := Inspect(constants.ToolCodex, payload)
	if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("unexpected: %+v (want %v)", info, exp)
	}
}

func TestCodexAPIKeyOnly(t *testing.T) {
	info := Inspect(constants.ToolCodex, []byte(`{"OPENAI_API_KEY":"sk-x"}`))
	if !info.Known || info.HasRefresh || !info.ExpiresAt.IsZero() {
		t.Fatalf("unexpected: %+v", info)
	}
}

func TestOpencodeExpiresMs(t *testing.T) {
	exp := time.Date(2029, 3, 3, 12, 0, 0, 0, time.UTC)
	payload := fmt.Appendf(nil, `{"type":"oauth","refresh":"r","access":"a","expires":%d}`, exp.UnixMilli())
	info := Inspect(constants.ToolOpencode, payload)
	if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("unexpected: %+v (want %v)", info, exp)
	}
}

func TestCursorOpaqueJWT(t *testing.T) {
	exp := time.Date(2028, 9, 9, 9, 9, 9, 0, time.UTC)
	info := Inspect(constants.ToolCursor, []byte(makeJWT(exp.Unix())))
	if !info.Known || info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("unexpected: %+v (want %v)", info, exp)
	}
}

func TestCursorNonJWT(t *testing.T) {
	if info := Inspect(constants.ToolCursor, []byte("not-a-jwt")); info.Known {
		t.Fatalf("expected unknown for non-JWT, got %+v", info)
	}
}

func TestNotDatableTools(t *testing.T) {
	cases := map[string][]byte{
		constants.ToolCopilot: []byte(`{"host":"https://github.com","login":"work"}`),
		constants.ToolAgy:     []byte("opaque-binary"),
	}
	for tool, payload := range cases {
		if info := Inspect(tool, payload); info.Known {
			t.Fatalf("%s: expected not datable, got %+v", tool, info)
		}
	}
}

func TestUnparseable(t *testing.T) {
	if info := Inspect(constants.ToolClaude, []byte("not json")); info.Known {
		t.Fatalf("expected unknown, got %+v", info)
	}
}
