package secret

import (
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestAccountKey(t *testing.T) {
	cases := []struct {
		key         string
		tool, acct  string
		wantAccount bool
	}{
		{key: "claude/main/claude_ai_oauth", tool: "claude", acct: "main", wantAccount: true},
		{key: "codex/side/auth", tool: "codex", acct: "side", wantAccount: true},
		// The three prefixed namespaces: no snapshot dir behind any of them.
		{key: "backup/20260101T000000Z/claude/claude_ai_oauth"},
		{key: "companion/main/git/email"},
		{key: "env/claude/main/API_KEY"},
		// Malformed.
		{key: ""},
		{key: "claude/main"},
		{key: "claude//oauth"},
		{key: "claude/main/oauth/extra"},
	}
	for _, tc := range cases {
		tool, acct, ok := AccountKey(tc.key)
		if ok != tc.wantAccount {
			t.Errorf("AccountKey(%q) ok = %v, want %v", tc.key, ok, tc.wantAccount)
			continue
		}
		if ok && (tool != tc.tool || acct != tc.acct) {
			t.Errorf("AccountKey(%q) = %q/%q, want %q/%q", tc.key, tool, acct, tc.tool, tc.acct)
		}
	}
}

// The account namespace is un-prefixed, so a tool id that equals a reserved
// prefix would make its own keys unreadable as accounts (and a prefixed
// namespace's keys readable as that tool's).
func TestToolIDsDoNotCollideWithKeyNamespaces(t *testing.T) {
	reserved := map[string]bool{NSBackup: true, NSCompanion: true, NSEnv: true}
	for _, tool := range constants.Tools {
		if reserved[tool] {
			t.Errorf("tool id %q collides with a reserved secret-key namespace", tool)
		}
	}
}
