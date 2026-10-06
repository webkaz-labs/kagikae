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
// account/read result. A daemon that holds no account is the one shape
// docs/ADAPTERS.md § Resident processes names (noAccount); every other shape
// without a string chatgptAccountId, an API-key login's included, is unreadable.
func (Codex) ParseDaemonAccount(result []byte) (adapter.DaemonAccount, bool) {
	var doc struct {
		Account            json.RawMessage `json:"account"`
		RequiresOpenaiAuth json.RawMessage `json:"requiresOpenaiAuth"`
		WorkspaceRouting   json.RawMessage `json:"workspaceRouting"`
	}
	if err := json.Unmarshal(result, &doc); err != nil {
		return adapter.DaemonAccount{}, false
	}
	if string(doc.Account) == "null" {
		return adapter.DaemonAccount{}, noAccount(doc.WorkspaceRouting, doc.RequiresOpenaiAuth)
	}
	var routing *struct {
		ChatGPTAccountID string `json:"chatgptAccountId"`
	}
	if nullOrAbsent(doc.WorkspaceRouting) || json.Unmarshal(doc.WorkspaceRouting, &routing) != nil {
		return adapter.DaemonAccount{}, false
	}
	account, ok := adapter.NewResidentAccount(routing.ChatGPTAccountID)
	return adapter.DaemonAccount{Account: account, Held: ok}, ok
}

// noAccount reports whether an answer whose account is null says the daemon
// holds no account: workspaceRouting null or absent and requiresOpenaiAuth the
// literal true. With requiresOpenaiAuth false upstream answers the same nulls
// for a model provider that needs no OpenAI login, whatever the credential, so a
// restart would not change it; that, and anything else, is unreadable.
func noAccount(routing, requiresOpenaiAuth json.RawMessage) bool {
	return nullOrAbsent(routing) && string(requiresOpenaiAuth) == "true"
}

// nullOrAbsent reports whether a raw member is absent or null: encoding/json
// leaves an absent member empty and keeps a null one as the literal.
func nullOrAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
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

// daemonRestarted is the `status` `codex app-server daemon restart` reports when
// it has restarted the daemon (docs/ADAPTERS.md § Resident processes).
const daemonRestarted = "restarted"

// ParseDaemonRestart reads the JSON object `codex app-server daemon restart`
// prints first when it succeeds, its `status` and `socketPath` only, like
// ParseDaemonStatus: the status must be the string `restarted` and the socket
// path an absolute string.
func (Codex) ParseDaemonRestart(output []byte) (socket string, ok bool) {
	var doc struct {
		Status     *string `json:"status"`
		SocketPath *string `json:"socketPath"`
	}
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&doc); err != nil {
		return "", false
	}
	if doc.Status == nil || *doc.Status != daemonRestarted || doc.SocketPath == nil || !filepath.IsAbs(*doc.SocketPath) {
		return "", false
	}
	return *doc.SocketPath, true
}

// DesktopApps is the ChatGPT app on macOS and nothing elsewhere.
func (Codex) DesktopApps() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return []string{desktopAppBundleID}
}

var _ adapter.ResidentHolder = Codex{}
