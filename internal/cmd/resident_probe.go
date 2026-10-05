package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/wsrpc"
)

// Limits of the resident-daemon probe (docs/SECURITY.md § Resident processes).
const (
	residentProbeTimeout = 2 * time.Second
	residentProbeMaxMsg  = 1 << 20
)

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

// probeResidentDaemon asks the managed daemon of env's tool home which account
// it holds and compares it with the account of the credential that credential
// returns, read only once the socket is known to exist. It returns one of the
// constants.ResidentObserved* tokens:
//
//   - absent: the declared socket, or the target it links to, does not exist;
//   - unknown: the socket exists but kae could not read an account from it or
//     from the credential — the resolved socket is not a socket or not owned
//     by the current user (no connection is made then), the connection is
//     refused (a socket left behind by a daemon that exited), the exchange
//     failed or timed out, or an answer or the credential names no account;
//   - matches / differs: both accounts were read.
//
// It sends adapter.DaemonProbeRequests and nothing else, and no account, email
// or plan leaves it: the result is the token only.
func (app *App) probeResidentDaemon(ctx context.Context, h adapter.ResidentHolder, env adapter.Env,
	credential func(context.Context) ([]byte, bool),
) string {
	socket, err := filepath.EvalSymlinks(h.ResidentDaemon(env).Socket)
	if errors.Is(err, fs.ErrNotExist) {
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

	ctx, cancel := context.WithTimeout(ctx, residentProbeTimeout)
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
	got, ok := h.ParseDaemonAccount(reply.Result)
	if !ok {
		return constants.ResidentObservedUnknown
	}
	if got.Same(want) {
		return constants.ResidentObservedMatches
	}
	return constants.ResidentObservedDiffers
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
// probeResidentDaemon. ok is false when the store cannot be resolved or read,
// or holds nothing.
func liveCredential(ad adapter.Adapter, env adapter.Env) func(context.Context) ([]byte, bool) {
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
