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
// account/read result. workspaceRouting is null for a login that is not a
// ChatGPT one, which reads as no account.
func (Codex) ParseDaemonAccount(result []byte) (adapter.ResidentAccount, bool) {
	var doc struct {
		WorkspaceRouting *struct {
			ChatGPTAccountID string `json:"chatgptAccountId"`
		} `json:"workspaceRouting"`
	}
	if err := json.Unmarshal(result, &doc); err != nil || doc.WorkspaceRouting == nil {
		return adapter.ResidentAccount{}, false
	}
	return adapter.NewResidentAccount(doc.WorkspaceRouting.ChatGPTAccountID)
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
