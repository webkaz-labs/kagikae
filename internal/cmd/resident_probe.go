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

// daemonProbe is what probeResidentDaemon read: observed, one of the
// constants.ResidentObserved* tokens, and originated, whether the daemon's
// answer to initialize says the probe became its first client
// (adapter.ProbeOriginated).
type daemonProbe struct {
	observed   string
	originated bool
}

// probeResidentDaemon is askResidentDaemon's observed token alone, for doctor.
func (app *App) probeResidentDaemon(ctx context.Context, h adapter.ResidentHolder, spec adapter.DaemonSpec,
	credential credentialReader,
) string {
	return app.askResidentDaemon(ctx, h, spec, credential).observed
}

// askResidentDaemon asks the managed daemon spec describes which account it
// holds and compares it with the account of the credential that credential
// returns, read only once the socket is known to exist. Only a run that may
// restart the daemon, and doctor --yes, call it: connecting has side effects on
// the daemon (docs/CLI.md § kae use Semantics, step 1). The caller derives spec
// once (h.ResidentDaemon) and uses the same one for a restart, so the two
// cannot resolve the home differently. Its observed token is one of:
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
// originated is read from the answer to initialize whenever one arrived, also
// when account/read then failed.
//
// It sends adapter.DaemonProbeRequests and nothing else, and no account, email,
// plan or user agent leaves it: the result is the token and the flag only.
func (app *App) askResidentDaemon(ctx context.Context, h adapter.ResidentHolder, spec adapter.DaemonSpec,
	credential credentialReader,
) daemonProbe {
	unknown := daemonProbe{observed: constants.ResidentObservedUnknown}
	socket, observed := socketOf(spec, app.euid())
	if observed != "" {
		return daemonProbe{observed: observed}
	}
	payload, ok := credential(ctx)
	if !ok {
		return unknown
	}
	want, ok := h.CredentialAccount(payload)
	if !ok {
		return unknown
	}

	ctx, cancel := context.WithTimeout(ctx, app.residentProbeLimit())
	defer cancel()
	// The connection goes to the resolved path the owner check examined, which
	// also keeps a long declared path from reaching the sun_path limit.
	msgs, err := wsrpc.CallEach(ctx, app.unixDialer(), socket, adapter.DaemonProbeRequests(),
		[]int{adapter.DaemonProbeInitializeID, adapter.DaemonProbeResponseID}, residentProbeMaxMsg)
	var got daemonProbe
	if len(msgs) == 2 {
		if result, ok := rpcResult(msgs[0]); ok {
			got.originated = adapter.ProbeOriginated(result)
		}
	}
	got.observed = constants.ResidentObservedUnknown
	if err != nil || len(msgs) != 2 {
		return got
	}
	result, ok := rpcResult(msgs[1])
	if !ok {
		return got
	}
	held, ok := h.ParseDaemonAccount(result)
	if !ok {
		return got
	}
	if held.Held && held.Account.Same(want) {
		got.observed = constants.ResidentObservedMatches
	} else {
		got.observed = constants.ResidentObservedDiffers
	}
	return got
}

// residentSocket is what a run that may not restart the daemon reads without
// connecting to it (docs/CLI.md § kae use Semantics, step 1): absent, present
// for a socket owned by the current user, or unknown for a declared path that
// resolves to anything else or cannot be resolved.
func (app *App) residentSocket(spec adapter.DaemonSpec) string {
	if _, observed := socketOf(spec, app.euid()); observed != "" {
		return observed
	}
	return constants.ResidentObservedPresent
}

// socketOf resolves spec's declared socket and checks that it is a socket uid
// owns. It returns the resolved path and "" when it is, and otherwise the token
// the daemon reads without a connection: absent, or unknown.
func socketOf(spec adapter.DaemonSpec, uid int) (socket, observed string) {
	socket, absent, err := resolveDeclaredSocket(spec.Socket)
	if absent {
		return "", constants.ResidentObservedAbsent
	}
	if err != nil || !ownedSocket(socket, uid) {
		return "", constants.ResidentObservedUnknown
	}
	return socket, ""
}

// rpcResult returns the `result` member of a JSON-RPC response that carries no
// error; ok is false for anything else.
func rpcResult(msg []byte) (json.RawMessage, bool) {
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	// A member present as null is absent (encoding/json keeps the literal).
	if msg == nil || json.Unmarshal(msg, &reply) != nil || jsonMember(reply.Error) || !jsonMember(reply.Result) {
		return nil, false
	}
	return reply.Result, true
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
