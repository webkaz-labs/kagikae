package adapter

import (
	"fmt"
	"io"
)

// ResidentHolder is implemented by a tool whose resident processes keep the
// account they started with after kae rewrites the live credential (codex).
// It connects to nothing, starts nothing and writes nothing: cmd owns the probe,
// the restart and the desktop-app handling (docs/ARCHITECTURE.md § Adapter
// Interface; what codex declares is docs/ADAPTERS.md § Resident processes).
type ResidentHolder interface {
	// ResidentDaemon describes the managed daemon of env's tool home. Its only
	// filesystem access is resolving the home's symlinks.
	ResidentDaemon(env Env) DaemonSpec
	// CredentialAccount reads the account key from a credential payload, from
	// either store. ok is false when the payload names no account.
	CredentialAccount(payload []byte) (ResidentAccount, bool)
	// ParseDaemonAccount reads the account key from the `result` member of the
	// daemon's answer to the last request in DaemonProbeRequests. ok is false for
	// anything it cannot read: missing fields, wrong types, malformed JSON.
	ParseDaemonAccount(result []byte) (ResidentAccount, bool)
	// DesktopApps lists the bundle ids of desktop apps that embed the tool;
	// empty except on darwin (the platform kae runs on, not Env.GOOS: the apps
	// are reached through Apple Events, which exist only there).
	DesktopApps() []string
}

// DaemonSpec is how to reach and control one managed daemon.
type DaemonSpec struct {
	// Socket is the declared control socket path. It may be a symlink; the
	// caller resolves it before checking its owner and connecting.
	Socket string
	// Restart and Status are argv, Binary first.
	Restart []string
	Status  []string
	// Env holds KEY=VALUE entries the caller appends to the environment of
	// Restart and Status. It always sets the tool's home variable explicitly,
	// so a value inherited from a bound directory cannot select another daemon.
	Env []string
}

// ResidentAccount is an opaque account key, compared and never printed: the
// value behind it is an account id, which is personal data. Every fmt verb
// prints a placeholder, and encoding/json sees no exported field.
type ResidentAccount struct{ id string }

// NewResidentAccount wraps a non-empty account id; ok is false for "".
func NewResidentAccount(id string) (ResidentAccount, bool) {
	if id == "" {
		return ResidentAccount{}, false
	}
	return ResidentAccount{id: id}, true
}

// Same reports whether two keys name the same account. A zero key matches
// nothing, itself included.
func (a ResidentAccount) Same(b ResidentAccount) bool {
	return a.id != "" && a.id == b.id
}

const residentAccountPlaceholder = "[redacted]"

// Format prints the placeholder for every verb, %#v and %x included. It is a
// redaction marker, not a message, so like cmd's "[redacted]" it is not
// localized.
func (ResidentAccount) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, residentAccountPlaceholder)
}

// String is the placeholder, for callers that bypass fmt.
func (ResidentAccount) String() string { return residentAccountPlaceholder }

// The daemon probe's request sequence (docs/SECURITY.md § Resident processes):
// the `initialize` request, the `initialized` notification and `account/read`
// with `refreshToken: false`. They are constants of this package so the command
// layer cannot change them; DaemonProbeRequests hands out fresh copies.
const (
	daemonInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"kae_probe","title":null,"version":"0"},"capabilities":{"experimentalApi":false,"requestAttestation":false}}}`
	daemonInitialized = `{"jsonrpc":"2.0","method":"initialized"}`
	daemonAccountRead = `{"jsonrpc":"2.0","id":2,"method":"account/read","params":{"refreshToken":false}}`

	// DaemonProbeResponseID is the JSON-RPC id of the account/read request.
	DaemonProbeResponseID = 2
)

// DaemonProbeRequests returns the fixed probe sequence, freshly allocated on
// every call so that no caller can alter what another one sends.
func DaemonProbeRequests() [][]byte {
	return [][]byte{[]byte(daemonInitialize), []byte(daemonInitialized), []byte(daemonAccountRead)}
}
