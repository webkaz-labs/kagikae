package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/wsrpc"
)

// Limits of the resident-daemon probe (docs/SECURITY.md § Resident processes).
const (
	defaultResidentProbeTimeout = 2 * time.Second
	residentProbeMaxMsg         = 1 << 20
)

// credentialReader returns the credential payload a probe compares the daemon
// with; ok is false when there is none to read.
type credentialReader func(context.Context) ([]byte, bool)

// residentProbeLimit bounds one probe: App.residentProbeTimeout, or the default.
func (app *App) residentProbeLimit() time.Duration {
	if app.residentProbeTimeout > 0 {
		return app.residentProbeTimeout
	}
	return defaultResidentProbeTimeout
}

// unixDialer is the seam the daemon probe connects through: App.dialUnix, or
// the standard dialer.
func (app *App) unixDialer() wsrpc.Dialer {
	if app.dialUnix != nil {
		return app.dialUnix
	}
	return (&net.Dialer{}).DialContext
}

func (app *App) euid() int {
	if app.euidForTest != nil {
		return app.euidForTest()
	}
	return os.Geteuid()
}

// probeResidentDaemon asks the managed daemon spec describes which account it
// holds and compares it with the account of the credential that credential
// returns, read only once the socket is known to exist. The caller derives spec
// once (h.ResidentDaemon) and uses the same one for a restart, so the two
// cannot resolve the home differently. It returns one of the
// constants.ResidentObserved* tokens:
//
//   - absent: the declared socket, or the target it links to, does not exist;
//   - unknown: the socket exists but kae could not read the daemon's answer, or
//     the credential names no account — the resolved socket is not a socket or
//     not owned by the current user (no connection is made then), the
//     connection is refused (a socket left behind by a daemon that exited), the
//     exchange failed or timed out, the answer is unreadable, or the credential
//     is an API-key login;
//   - matches / differs: the credential's account was read and the daemon
//     answered; a daemon that answers it holds no account differs.
//
// It sends adapter.DaemonProbeRequests and nothing else, and no account, email
// or plan leaves it: the result is the token only.
func (app *App) probeResidentDaemon(ctx context.Context, h adapter.ResidentHolder, spec adapter.DaemonSpec,
	credential credentialReader,
) string {
	socket, absent, err := resolveDeclaredSocket(spec.Socket)
	if absent {
		return constants.ResidentObservedAbsent
	}
	if err != nil || !ownedSocket(socket, app.euid()) {
		return constants.ResidentObservedUnknown
	}
	payload, ok := credential(ctx)
	if !ok {
		return constants.ResidentObservedUnknown
	}
	want, ok := h.CredentialAccount(payload)
	if !ok {
		return constants.ResidentObservedUnknown
	}

	ctx, cancel := context.WithTimeout(ctx, app.residentProbeLimit())
	defer cancel()
	// The connection goes to the resolved path the owner check examined, which
	// also keeps a long declared path from reaching the sun_path limit.
	msg, err := wsrpc.Call(ctx, app.unixDialer(), socket, adapter.DaemonProbeRequests(),
		adapter.DaemonProbeResponseID, residentProbeMaxMsg)
	if err != nil {
		return constants.ResidentObservedUnknown
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	// A member present as null is absent (encoding/json keeps the literal).
	if json.Unmarshal(msg, &reply) != nil || jsonMember(reply.Error) || !jsonMember(reply.Result) {
		return constants.ResidentObservedUnknown
	}
	got, held, ok := h.ParseDaemonAccount(reply.Result)
	if !ok {
		return constants.ResidentObservedUnknown
	}
	if held && got.Same(want) {
		return constants.ResidentObservedMatches
	}
	return constants.ResidentObservedDiffers
}

// resolveDeclaredSocket resolves the symlinks of a daemon's declared socket
// path, as the probe and doctor's `daemon version` half both compare it.
// absent is true, with a nil error, when the path or the target it links to
// does not exist; err is any other failure to resolve it.
func resolveDeclaredSocket(declared string) (resolved string, absent bool, err error) {
	resolved, err = filepath.EvalSymlinks(declared)
	if errors.Is(err, fs.ErrNotExist) {
		return "", true, nil
	}
	return resolved, false, err
}

// jsonMember reports whether a raw JSON-RPC member is present and not null.
func jsonMember(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

// ownedSocket reports whether path is a Unix socket owned by uid.
func ownedSocket(path string, uid int) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Type() != fs.ModeSocket {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(st.Uid) == int64(uid)
}

// liveCredential returns a reader of ad's live credential in env, from
// whichever store the adapter resolves (auth.json or the keyring item), for
// probeResidentDaemon. It reads what is live when called, not what a switch
// will leave: the probe before a switch is given the target's payload instead.
// ok is false when the store cannot be resolved or read, or holds nothing.
func liveCredential(ad adapter.Adapter, env adapter.Env) credentialReader {
	return func(ctx context.Context) ([]byte, bool) {
		specs, err := ad.Artifacts(ctx, env)
		if err != nil {
			return nil, false
		}
		var credential []artifact.Spec
		for _, sp := range specs {
			if !sp.IdentityOnly {
				credential = append(credential, sp)
			}
		}
		if len(credential) != 1 {
			return nil, false
		}
		live, err := artifact.ReadLive(ctx, credential[0])
		if err != nil || !live.Present {
			return nil, false
		}
		return live.Data, true
	}
}

// residentRestartCommand is the command a person types to restart the daemon
// spec describes, for every message that names that manual step (the switch's
// warnings, and doctor's resident_drift). It is the bare restart argv when the
// shell kae was started from resolves the same daemon; otherwise it prefixes
// the spec's environment (CODEX_HOME=<real home>), because a shell that exports
// a bound directory's or a global isolation's CODEX_HOME would otherwise
// restart that home's daemon (docs/SECURITY.md § Resident processes). Values
// are single-quoted like kae's other printed shell lines.
func (app *App) residentRestartCommand(h adapter.ResidentHolder, spec adapter.DaemonSpec) string {
	command := strings.Join(spec.Restart, " ")
	shell := app.Env
	if app.shellEnv != nil {
		shell = *app.shellEnv
	}
	if slices.Equal(h.ResidentDaemon(shell).Env, spec.Env) {
		return command
	}
	parts := make([]string, 0, len(spec.Env)+1)
	for _, kv := range spec.Env {
		key, value, _ := strings.Cut(kv, "=")
		parts = append(parts, key+"="+shellSingleQuote(value))
	}
	return strings.Join(append(parts, command), " ")
}
