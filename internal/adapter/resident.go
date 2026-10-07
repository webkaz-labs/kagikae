package adapter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	// CredentialConflict reads whether a credential payload, from either store,
	// names one account throughout or carries one account's tokens under
	// another's account record: the shape a refresh by a process of the previous
	// account writes into the live login after a switch (docs/ADAPTERS.md
	// § Resident processes). It reports no id, so nothing in it needs redacting.
	CredentialConflict(payload []byte) ConflictVerdict
	// ParseDaemonAccount reads what the daemon holds from the `result` member of
	// its answer to the last request in DaemonProbeRequests. ok is false for
	// anything it cannot read: missing fields, wrong types, malformed JSON.
	ParseDaemonAccount(result []byte) (DaemonAccount, bool)
	// ParseDaemonStatus reads the stdout of DaemonSpec.Status: whether the daemon
	// reports itself running and, when it does, the socket path it reports. ok is
	// false for anything it cannot read, a running daemon without an absolute
	// socket path included.
	ParseDaemonStatus(output []byte) (running bool, socket string, ok bool)
	// ParseDaemonRestart reads the stdout of DaemonSpec.Restart: the socket path
	// it reports for the daemon it restarted. ok is false for anything that is not
	// a report of a restart with an absolute socket path.
	ParseDaemonRestart(output []byte) (socket string, ok bool)
	// DesktopApps lists the bundle ids of desktop apps that embed the tool;
	// empty except on darwin (the platform kae runs on, not Env.GOOS: the apps
	// are reached through Apple Events, which exist only there).
	DesktopApps() []string
}

// ConflictVerdict is CredentialConflict's answer. The zero value is
// ConflictUnknown, which changes no decision a caller would otherwise take.
type ConflictVerdict int

const (
	// ConflictUnknown: the payload is not a shape the tool can judge, or it holds
	// fewer than two of the values compared.
	ConflictUnknown ConflictVerdict = iota
	// ConflictConsistent: every value compared names the same account.
	ConflictConsistent
	// ConflictDetected: two of the values compared name different accounts.
	ConflictDetected
)

// DaemonAccount is what a daemon answers that it holds: Account when Held, or
// no account at all, which the caller compares as another account than any
// credential's.
type DaemonAccount struct {
	Account ResidentAccount
	Held    bool
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
// value behind it is an account id, which is personal data. It keeps only a
// SHA-256 digest of the id, never the id itself, because fmt cannot reach the
// Format method of a value held in another struct's unexported field and then
// prints the fields raw. Printed directly, every fmt verb shows a placeholder;
// printed inside another value, at most the digest shows; encoding/json sees
// no exported field.
type ResidentAccount struct {
	digest [sha256.Size]byte
	ok     bool
}

// NewResidentAccount keys a non-empty account id; ok is false for "".
func NewResidentAccount(id string) (ResidentAccount, bool) {
	if id == "" {
		return ResidentAccount{}, false
	}
	return ResidentAccount{digest: sha256.Sum256([]byte(id)), ok: true}, true
}

// Same reports whether two keys name the same account. A zero key matches
// nothing, itself included.
func (a ResidentAccount) Same(b ResidentAccount) bool {
	return a.ok && b.ok && a.digest == b.digest
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
	daemonInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"` + daemonProbeClientName + `","title":null,"version":"0"},"capabilities":{"experimentalApi":false,"requestAttestation":false}}}`
	daemonInitialized = `{"jsonrpc":"2.0","method":"initialized"}`
	daemonAccountRead = `{"jsonrpc":"2.0","id":2,"method":"account/read","params":{"refreshToken":false}}`

	// daemonProbeClientName is the probe's clientInfo.name (docs/ADAPTERS.md
	// § Resident processes, client name).
	daemonProbeClientName = "kae_probe"

	// DaemonProbeInitializeID and DaemonProbeResponseID are the JSON-RPC ids of
	// the initialize and account/read requests, written into daemonInitialize and
	// daemonAccountRead above; TestDaemonProbeRequestsAreTheFixedReadOnlySequence
	// fails if they part.
	DaemonProbeInitializeID = 1
	DaemonProbeResponseID   = 2
)

// ProbeOriginated reports whether the `result` of the daemon's answer to the
// probe's initialize says the probe became the daemon's originator: upstream
// builds its `userAgent` once the originator is settled, starting with
// "<originator>/" (docs/ADAPTERS.md § Resident processes, client name). Anything
// it cannot read is false. The user agent is read only for this and never kept.
func ProbeOriginated(result []byte) bool {
	var doc struct {
		UserAgent *string `json:"userAgent"`
	}
	if json.Unmarshal(result, &doc) != nil || doc.UserAgent == nil {
		return false
	}
	return strings.HasPrefix(*doc.UserAgent, daemonProbeClientName+"/")
}

// DaemonProbeRequests returns the fixed probe sequence, freshly allocated on
// every call so that no caller can alter what another one sends.
func DaemonProbeRequests() [][]byte {
	return [][]byte{[]byte(daemonInitialize), []byte(daemonInitialized), []byte(daemonAccountRead)}
}
