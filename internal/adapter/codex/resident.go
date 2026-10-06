package codex

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

// desktopAppBundleID is the ChatGPT desktop app, which embeds a codex server.
const desktopAppBundleID = "com.openai.codex"

// ResidentDaemon declares codex's managed daemon for env's codex home
// (docs/ADAPTERS.md § Resident processes). The home is canonicalized like the
// keyring store key, and CODEX_HOME is set to it explicitly rather than left to
// whatever the caller's environment carries. On macOS the socket is a symlink
// to a per-uid temporary directory; the caller follows it rather than kae
// re-deriving the target's name.
func (c Codex) ResidentDaemon(env adapter.Env) adapter.DaemonSpec {
	home := canonicalHome(env)
	return adapter.DaemonSpec{
		Socket:  filepath.Join(home, "app-server-control", "app-server-control.sock"),
		Restart: []string{c.Binary(), "app-server", "daemon", "restart"},
		Status:  []string{c.Binary(), "app-server", "daemon", "version"},
		Env:     []string{"CODEX_HOME=" + home},
	}
}

// CredentialAccount reads tokens.account_id, which auth.json and the keyring
// payload carry alike. An API-key-only login has none.
func (Codex) CredentialAccount(payload []byte) (adapter.ResidentAccount, bool) {
	var doc struct {
		Tokens *struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.Tokens == nil {
		return adapter.ResidentAccount{}, false
	}
	return adapter.NewResidentAccount(doc.Tokens.AccountID)
}

// ParseDaemonAccount reads workspaceRouting.chatgptAccountId from an
// account/read result. `account` null, with workspaceRouting null or absent, is
// a daemon that holds no account (held false, ok true): a daemon on 0.160.1
// answered so after the live credential changed under it (docs/ADAPTERS.md
// § Resident processes). workspaceRouting is also null for an API-key login,
// whose account is not null; that, like every other shape, is unreadable.
func (Codex) ParseDaemonAccount(result []byte) (account adapter.ResidentAccount, held, ok bool) {
	var doc struct {
		Account          json.RawMessage `json:"account"`
		WorkspaceRouting json.RawMessage `json:"workspaceRouting"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		return adapter.ResidentAccount{}, false, false
	}
	// encoding/json keeps a null member as the literal, and leaves an absent one empty.
	if string(doc.Account) == "null" {
		if len(doc.WorkspaceRouting) == 0 || string(doc.WorkspaceRouting) == "null" {
			return adapter.ResidentAccount{}, false, true
		}
		return adapter.ResidentAccount{}, false, false
	}
	var routing *struct {
		ChatGPTAccountID string `json:"chatgptAccountId"`
	}
	if len(doc.WorkspaceRouting) == 0 || json.Unmarshal(doc.WorkspaceRouting, &routing) != nil || routing == nil {
		return adapter.ResidentAccount{}, false, false
	}
	account, ok = adapter.NewResidentAccount(routing.ChatGPTAccountID)
	return account, ok, ok
}

// daemonRunning is the `status` `codex app-server daemon version` reports for a
// running daemon (docs/ADAPTERS.md § Resident processes).
const daemonRunning = "running"

// ParseDaemonStatus reads the JSON object `codex app-server daemon version`
// prints first, its `status` and `socketPath` only; whatever follows the object
// (a log line a daemon it started writes to the same stdout) is not read. Both
// must be strings; the socket path matters, and must be absolute, only while the
// daemon is running.
func (Codex) ParseDaemonStatus(output []byte) (running bool, socket string, ok bool) {
	var doc struct {
		Status     *string `json:"status"`
		SocketPath *string `json:"socketPath"`
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&doc); err != nil || doc.Status == nil {
		return false, "", false
	}
	if *doc.Status != daemonRunning {
		return false, "", true
	}
	if doc.SocketPath == nil || !filepath.IsAbs(*doc.SocketPath) {
		return false, "", false
	}
	return true, *doc.SocketPath, true
}

// DesktopApps is the ChatGPT app on macOS and nothing elsewhere.
func (Codex) DesktopApps() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return []string{desktopAppBundleID}
}

var _ adapter.ResidentHolder = Codex{}
