package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/usagelimit"
)

// usageReadingTTL is how long a remembered reading, local or remote, may answer
// a listing before kae asks again, how long kae waits after a failed request
// before asking again, and how old a reading must be before its cell shows its
// age. A file this process reads in the same listing is not on this timer: it
// is the source. Ten minutes keeps a burst of `kae` and `kae ls` to one
// request, without leaving an account stuck on a number from the start of a
// work session.
const usageReadingTTL = 10 * time.Minute

// usageOld reports whether a reading taken at observed is ten minutes old.
func usageOld(observed, now time.Time) bool { return now.Sub(observed) >= usageReadingTTL }

// usageBodyLimit caps a usage response. The windows are a small JSON object;
// anything larger is not a reading we asked for.
const usageBodyLimit = 1 << 20

// usageView is one account's windows as a listing will show them.
type usageView struct {
	Source     string
	ObservedAt time.Time
	Windows    []usagelimit.Window
}

func (v usageView) empty() bool { return len(v.Windows) == 0 }

func (v usageView) report() *usageJSON {
	if v.empty() {
		return nil
	}
	observed := ""
	if !v.ObservedAt.IsZero() {
		observed = v.ObservedAt.UTC().Format(time.RFC3339)
	}
	return &usageJSON{
		Source: v.Source, ObservedAt: observed,
		Windows: usagelimit.Normalize(v.Windows),
	}
}

// usageJSON is the additive `usage` object on account and status rows.
type usageJSON struct {
	Source     string              `json:"source"`
	ObservedAt string              `json:"observed_at,omitempty"`
	Windows    []usagelimit.Window `json:"windows"`
}

type usageHome struct {
	path      string
	account   string
	pathOwned bool
}

type usageCache struct {
	SchemaVersion int          `json:"schema_version"`
	Entries       []usageEntry `json:"entries"`
}

type usageEntry struct {
	Tool        string              `json:"tool"`
	Account     string              `json:"account"`
	Origin      string              `json:"origin"`
	SourcePath  string              `json:"source_path,omitempty"`
	ModTimeUnix int64               `json:"mod_time_unix,omitempty"`
	ObservedAt  time.Time           `json:"observed_at"`
	Windows     []usagelimit.Window `json:"windows"`
	// ProbedAt is when a usage request for this account last failed. It holds
	// the next request back for usageReadingTTL. A new reading replaces the
	// entry and clears it.
	ProbedAt *time.Time `json:"probed_at,omitempty"`
}

// accountReadings is what the inventory listings call: each account's
// credential state and subscription windows, under one secret read cache so the
// usage veto reuses the payloads the freshness column read.
func (app *App) accountReadings(ctx context.Context, captured []account.Account, st *state.State) (map[toolAccount]credentialState, map[toolAccount]usageView) {
	ctx = secret.WithReadCache(ctx)
	return app.capturedCredentialStates(ctx, captured), app.accountUsages(ctx, captured, st)
}

// accountUsages resolves a compact subscription-window reading for every
// captured account that has one.
//
// Local first. usage-exact.json in Claude's config directory is written by a
// user's statusline script, not by Claude Code; Codex appends rate_limits to
// the session rollout. A file that does not name an
// account — the shared home, which the next switch keeps until the tool
// rewrites it — stays attributed to the account that owned it at that mtime.
// A path that is the account's own home (global isolated, or an isolated pin)
// is attributed to that account directly. A shared home's reading whose record
// names another captured account as its creator is vetoed: not shown, not
// cached, and the account's remembered reading of that file is dropped
// (credentialKeys.vetoes).
//
// Cache second. An account with no current local file keeps the last reading,
// until every window in it has reset. A file the tool rewrote so that every
// window has reset drops that memory; a network reading taken after the
// rewrite is kept. A remembered reading, whatever its origin, is asked again
// once it is usageReadingTTL old or any window in it has reset; a file this
// listing just read is current and is not. A failed request holds the next one
// back for usageReadingTTL (usageEntry.ProbedAt).
//
// Network last, and only for that gap. Failure leaves the still-valid
// remembered windows as they were. The token never reaches the report. usageClient nil skips the network,
// which is how tests stay offline.
func (app *App) accountUsages(ctx context.Context, captured []account.Account, st *state.State) map[toolAccount]usageView {
	now := app.Now()
	cache, writable := loadUsageCache(app.Paths.UsageCacheFile())
	homes := app.usageHomes(captured, st)
	local := map[toolAccount]usageView{}
	// Newest file per account, then one cache write. Homes are visited in no
	// particular order, and an older file must not replace a newer one — or a
	// remote reading taken after the newest file was written.
	newest := map[toolAccount]usagelimit.Reading{}
	keys := &credentialKeys{app: app, captured: captured}
	for tool, candidates := range homes {
		ad, reader, ok := localUsageReader(tool)
		if !ok {
			continue
		}
		for _, home := range candidates {
			reading, ok := reader.LocalUsage(home.path)
			if !ok {
				continue
			}
			accountName := home.account
			if !home.pathOwned {
				if entry, found := cache.bySource(reading.Path, reading.ModTime); found {
					accountName = entry.Account
				}
			}
			if accountName == "" {
				continue
			}
			key := toolAccount{tool, accountName}
			if !home.pathOwned && keys.vetoes(ctx, ad, key, reading) {
				cache.forget(key, reading)
				continue
			}
			if prev, exists := newest[key]; exists && !reading.ModTime.After(prev.ModTime) {
				continue
			}
			newest[key] = reading
		}
	}
	for key, reading := range newest {
		fresh := usagelimit.Fresh(reading.Windows, now)
		if len(fresh) == 0 {
			// The tool rewrote the file and every window in it has reset.
			// Forget the previous percents. A network reading taken after
			// that rewrite is newer, so it stays until it too resets.
			if prev, found := cache.byAccount(key.tool, key.account); found &&
				prev.Origin == constants.UsageOriginRemote &&
				!reading.ModTime.After(prev.ObservedAt) &&
				len(usagelimit.Fresh(prev.Windows, now)) > 0 {
				continue
			}
		} else {
			local[key] = usageView{
				Source: constants.UsageSourceLocal, ObservedAt: reading.ModTime.UTC(),
				Windows: usagelimit.Normalize(fresh),
			}
		}
		cache.upsert(localUsageEntry(key, reading))
	}
	views := map[toolAccount]usageView{}
	for key, view := range local {
		views[key] = view
	}
	var probe []account.Account
	for _, acc := range captured {
		key := toolAccount{acc.Tool, acc.Name}
		if _, ok := views[key]; ok {
			continue
		}
		entry, found := cache.byAccount(acc.Tool, acc.Name)
		fresh := []usagelimit.Window(nil)
		if found {
			fresh = usagelimit.Fresh(entry.Windows, now)
		}
		cached := usageView{
			Source: constants.UsageSourceCache, ObservedAt: entry.ObservedAt,
			Windows: usagelimit.Normalize(fresh),
		}
		backedOff := found && entry.ProbedAt != nil && !usageOld(*entry.ProbedAt, now)
		current := found && len(fresh) == len(entry.Windows) && !usageOld(entry.ObservedAt, now)
		switch {
		case len(fresh) > 0 && (current || backedOff):
			views[key] = cached
		case app.usageClient != nil && !backedOff:
			probe = append(probe, acc)
		case len(fresh) > 0:
			views[key] = cached
		}
	}
	if len(probe) > 0 {
		probed, failed := app.probeUsages(ctx, probe, now)
		for key := range failed {
			if _, ok := probed[key]; ok {
				continue
			}
			// Nothing came back: hold the next request back, on the entry the
			// listing shows or, with none, on an entry that shows nothing.
			entry, found := cache.byAccount(key.tool, key.account)
			if !found {
				entry = usageEntry{
					Tool: key.tool, Account: key.account, Origin: constants.UsageOriginRemote,
					Windows: []usagelimit.Window{},
				}
			}
			probedAt := now.UTC()
			entry.ProbedAt = &probedAt
			cache.upsert(entry)
		}
		for key, view := range probed {
			views[key] = view
			cache.upsert(usageEntry{
				Tool: key.tool, Account: key.account, Origin: constants.UsageOriginRemote,
				ObservedAt: view.ObservedAt, Windows: view.Windows,
			})
		}
		for _, acc := range probe {
			key := toolAccount{acc.Tool, acc.Name}
			if _, ok := views[key]; ok {
				continue
			}
			entry, found := cache.byAccount(acc.Tool, acc.Name)
			if !found {
				continue
			}
			fresh := usagelimit.Fresh(entry.Windows, now)
			if len(fresh) == 0 {
				continue
			}
			views[key] = usageView{
				Source: constants.UsageSourceCache, ObservedAt: entry.ObservedAt,
				Windows: usagelimit.Normalize(fresh),
			}
		}
	}
	if writable {
		saveUsageCache(app.Paths.UsageCacheFile(), cache)
	}
	return views
}

// localUsageEntry is the cache's memory of a local file's reading for key.
func localUsageEntry(key toolAccount, reading usagelimit.Reading) usageEntry {
	return usageEntry{
		Tool: key.tool, Account: key.account, Origin: constants.UsageOriginLocal,
		SourcePath: reading.Path, ModTimeUnix: reading.ModTime.UnixNano(),
		ObservedAt: reading.ModTime.UTC(), Windows: usagelimit.Normalize(reading.Windows),
	}
}

// recordSharedUsage runs before `kae use` switches tools: each shared usage
// home's current file is remembered under the account that is active now, so a
// listing that first comes after the switch does not attribute it to the new
// account. It is the cache write a listing would have made, under the same
// codex veto, and never overwrites a reading observed after the file. Best
// effort and lock-free like every usage cache write: any failure is ignored.
func (app *App) recordSharedUsage(ctx context.Context, st *state.State, targets map[string]string) {
	if st == nil {
		return
	}
	all, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		return
	}
	var captured []account.Account
	for _, acc := range all {
		if _, switching := targets[acc.Tool]; switching {
			captured = append(captured, acc)
		}
	}
	cache, writable := loadUsageCache(app.Paths.UsageCacheFile())
	if !writable {
		return
	}
	keys := &credentialKeys{app: app, captured: captured}
	changed := false
	for tool, candidates := range app.usageHomes(captured, st) {
		outgoing := st.Active[tool]
		if outgoing == "" || outgoing == targets[tool] {
			continue
		}
		ad, reader, ok := localUsageReader(tool)
		if !ok {
			continue
		}
		for _, home := range candidates {
			if home.pathOwned || home.account != outgoing {
				continue
			}
			reading, ok := reader.LocalUsage(home.path)
			if !ok {
				continue
			}
			if _, seen := cache.bySource(reading.Path, reading.ModTime); seen {
				continue
			}
			key := toolAccount{tool, outgoing}
			if keys.vetoes(ctx, ad, key, reading) {
				continue
			}
			if prev, found := cache.byAccount(tool, outgoing); found && prev.ObservedAt.After(reading.ModTime) {
				continue
			}
			cache.upsert(localUsageEntry(key, reading))
			changed = true
		}
	}
	if changed {
		saveUsageCache(app.Paths.UsageCacheFile(), cache)
	}
}

// localUsageReader is tool's adapter and its local usage reader, when it has one.
func localUsageReader(tool string) (adapter.Adapter, adapter.LocalUsage, bool) {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return nil, nil, false
	}
	reader, ok := ad.(adapter.LocalUsage)
	return ad, reader, ok
}

// usageHomes lists the directories that can hold a tool's own window record,
// and which account a newly written file there belongs to.
//
// The live home belongs to the active account, unless this process's
// environment points it at the current directory's pin store — then it
// belongs to the bound account, and an isolated pin owns it outright. Other
// homes are that account's global isolated home and every bound directory's
// config store. A shared store does not own the file: the account bound there
// is only the guess for a file kae has not seen before.
func (app *App) usageHomes(captured []account.Account, st *state.State) map[string][]usageHome {
	byTool := map[string]map[string]usageHome{}
	add := func(tool string, home usageHome) {
		if home.path == "" {
			return
		}
		home.path = filepath.Clean(home.path)
		homes := byTool[tool]
		if homes == nil {
			homes = map[string]usageHome{}
			byTool[tool] = homes
		}
		if prev, ok := homes[home.path]; ok && (prev.pathOwned || !home.pathOwned) {
			return
		}
		homes[home.path] = home
	}
	active := map[string]string{}
	if st != nil {
		active = st.Active
	}
	var pinMode, pinID string
	var pinAccounts map[string]string
	if cwd, err := cwdAbs(); err == nil {
		if info, ok, ferr := readDirFragment(); ferr == nil && ok {
			pinMode, pinID, pinAccounts = info.Mode, paths.PinID(cwd), info.Accounts
		}
	}
	tools := map[string][]account.Account{}
	for _, acc := range captured {
		tools[acc.Tool] = append(tools[acc.Tool], acc)
	}
	for tool := range tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		homeOf, ok := ad.(adapter.UsageHome)
		if !ok {
			continue
		}
		live := homeOf.UsageHome(app.Env)
		owner := active[tool]
		pathOwned := false
		if acc, bound := pinAccounts[tool]; bound && pinID != "" {
			if dir, ok := app.modeStoreDir(pinMode, pinID, tool, acc); ok && filepath.Clean(dir) == filepath.Clean(live) {
				owner = acc
				pathOwned = modeStoreIsPerAccount(pinMode)
			}
		}
		add(tool, usageHome{path: live, account: owner, pathOwned: pathOwned})
		for _, acc := range tools[tool] {
			add(tool, usageHome{
				path:      app.Paths.GlobalIsolatedHomeDir(tool, acc.Name),
				account:   acc.Name,
				pathOwned: true,
			})
		}
	}
	index := app.boundDirectoryIndex()
	if index.err == nil {
		for _, pin := range index.directories {
			info, exists, ferr := pin.readFragment()
			if ferr != nil || !exists {
				continue
			}
			for tool, acc := range info.Accounts {
				dir, bound := app.boundStoreDir(pin.PinID, tool, info)
				if !bound {
					continue
				}
				add(tool, usageHome{path: dir, account: acc, pathOwned: modeStoreIsPerAccount(info.Mode)})
			}
		}
	}
	out := map[string][]usageHome{}
	for tool, homes := range byTool {
		for _, home := range homes {
			out[tool] = append(out[tool], home)
		}
	}
	return out
}

// credentialKeys holds, per tool, the account key each captured credential
// names, read once per listing and only when a veto needs it. Through
// secret.Cached under the listing's read cache (accountReadings), a payload
// the freshness column already read is not read again.
type credentialKeys struct {
	app      *App
	captured []account.Account
	byTool   map[string]map[string]adapter.ResidentAccount
}

// vetoes reports whether a shared home's reading, about to be attributed to
// owner, was written under another captured account: the record's creator is
// not owner's account but is another captured account's of the same tool. Any
// key it cannot read leaves the reading attributed (docs/CLI.md § Subscription
// windows in listings, Codex veto).
func (k *credentialKeys) vetoes(ctx context.Context, ad adapter.Adapter, owner toolAccount, reading usagelimit.Reading) bool {
	creatorOf, ok := ad.(adapter.UsageCreator)
	if !ok {
		return false
	}
	holder, ok := ad.(adapter.ResidentHolder)
	if !ok {
		return false
	}
	creator, ok := creatorOf.UsageCreator(reading.Path)
	if !ok {
		return false
	}
	keys := k.forTool(ctx, holder, owner.tool)
	ownerKey, ok := keys[owner.account]
	if !ok || ownerKey.Same(creator) {
		return false
	}
	for name, key := range keys {
		if name != owner.account && key.Same(creator) {
			return true
		}
	}
	return false
}

// forTool reads the keys of tool's captured accounts on its first call. An
// account whose credential names no key, or cannot be read, is absent.
func (k *credentialKeys) forTool(ctx context.Context, holder adapter.ResidentHolder, tool string) map[string]adapter.ResidentAccount {
	if keys, ok := k.byTool[tool]; ok {
		return keys
	}
	keys := map[string]adapter.ResidentAccount{}
	if k.byTool == nil {
		k.byTool = map[string]map[string]adapter.ResidentAccount{}
	}
	k.byTool[tool] = keys
	be, err := k.app.secretBackend()
	if err != nil {
		return keys
	}
	be = secret.Cached(be)
	for _, acc := range k.captured {
		if acc.Tool != tool {
			continue
		}
		for _, name := range acc.ArtifactNames() {
			art := acc.Artifacts[name]
			if !art.Present {
				continue
			}
			data, found, err := be.Get(ctx, art.SecretRef)
			if err != nil || !found {
				continue
			}
			if key, ok := holder.CredentialAccount(data); ok {
				keys[acc.Name] = key
				break
			}
		}
	}
	return keys
}

// probeUsages asks for each account's windows and returns the readings, and
// the accounts whose request was sent and gave no reading. An account whose
// token is not eligible is in neither: it cost no request.
func (app *App) probeUsages(ctx context.Context, captured []account.Account, now time.Time) (map[toolAccount]usageView, map[toolAccount]bool) {
	be, err := app.secretBackend()
	if err != nil {
		return nil, nil
	}
	be = secret.Cached(be)
	type result struct {
		key       toolAccount
		view      usageView
		ok        bool
		attempted bool
	}
	results := make([]result, len(captured))
	var wg sync.WaitGroup
	for i, acc := range captured {
		wg.Add(1)
		go func(i int, acc account.Account) {
			defer wg.Done()
			view, ok, attempted := app.probeAccountUsage(ctx, be, acc, now)
			results[i] = result{key: toolAccount{acc.Tool, acc.Name}, view: view, ok: ok, attempted: attempted}
		}(i, acc)
	}
	wg.Wait()
	views := map[toolAccount]usageView{}
	failed := map[toolAccount]bool{}
	for _, r := range results {
		if r.ok {
			views[r.key] = r.view
		} else if r.attempted {
			failed[r.key] = true
		}
	}
	return views, failed
}

func (app *App) probeAccountUsage(ctx context.Context, be secret.Backend, acc account.Account, now time.Time) (view usageView, ok, attempted bool) {
	ad, err := adapter.ForTool(acc.Tool)
	if err != nil {
		return usageView{}, false, false
	}
	prober, isProber := ad.(adapter.UsageProber)
	if !isProber {
		return usageView{}, false, false
	}
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		if !art.Present {
			continue
		}
		data, found, err := be.Get(ctx, art.SecretRef)
		if err != nil || !found {
			continue
		}
		req, eligible := prober.ProbeUsage(data, now)
		if !eligible {
			continue
		}
		attempted = true
		reading, got := app.doUsageProbe(ctx, prober, req, now)
		if !got {
			continue
		}
		fresh := usagelimit.Fresh(reading.Windows, now)
		if len(fresh) == 0 {
			continue
		}
		return usageView{
			Source: constants.UsageSourceCache, ObservedAt: now.UTC(),
			Windows: usagelimit.Normalize(fresh),
		}, true, true
	}
	return usageView{}, false, attempted
}

func (app *App) doUsageProbe(ctx context.Context, prober adapter.UsageProber, req *http.Request, now time.Time) (usagelimit.Reading, bool) {
	if app.usageClient == nil || !usageRequestAllowed(req) {
		return usagelimit.Reading{}, false
	}
	resp, err := app.usageClient.Do(req.WithContext(ctx))
	if err != nil {
		return usagelimit.Reading{}, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, usageBodyLimit))
	if err != nil || resp.StatusCode != http.StatusOK {
		return usagelimit.Reading{}, false
	}
	return prober.ParseUsageBody(body, now)
}

func usageRequestAllowed(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodGet || req.URL.Scheme != "https" {
		return false
	}
	switch req.URL.Hostname() {
	case "api.anthropic.com", "chatgpt.com":
		return true
	default:
		return false
	}
}

func newUsageClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func loadUsageCache(path string) (usageCache, bool) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return usageCache{SchemaVersion: constants.SchemaVersion, Entries: []usageEntry{}}, true
	}
	if err != nil {
		return usageCache{}, false
	}
	var cache usageCache
	if json.Unmarshal(data, &cache) != nil || cache.SchemaVersion != constants.SchemaVersion {
		return usageCache{}, false
	}
	if cache.Entries == nil {
		cache.Entries = []usageEntry{}
	}
	return cache, true
}

func saveUsageCache(path string, cache usageCache) {
	cache.SchemaVersion = constants.SchemaVersion
	cache.normalize()
	if len(cache.Entries) == 0 {
		// Nothing to remember: create no file, but empty one a veto emptied.
		if _, err := os.Stat(path); err != nil {
			return
		}
	}
	next, err := patch.EncodeJSON(cache)
	if err != nil {
		return
	}
	if prev, err := os.ReadFile(path); err == nil && string(prev) == string(next) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = patch.WriteFileAtomic(path, next, 0o600)
}

func (c *usageCache) byAccount(tool, account string) (usageEntry, bool) {
	for _, entry := range c.Entries {
		if entry.Tool == tool && entry.Account == account {
			return entry, true
		}
	}
	return usageEntry{}, false
}

func (c *usageCache) bySource(path string, mod time.Time) (usageEntry, bool) {
	if path == "" {
		return usageEntry{}, false
	}
	modNano := mod.UnixNano()
	for _, entry := range c.Entries {
		if entry.SourcePath == path && entry.ModTimeUnix == modNano {
			return entry, true
		}
	}
	return usageEntry{}, false
}

// forget drops key's entry when it remembers reading's file at any mtime: the
// file's creator does not change, so no reading of it belongs to the account.
func (c *usageCache) forget(key toolAccount, reading usagelimit.Reading) {
	for i, entry := range c.Entries {
		if entry.Tool == key.tool && entry.Account == key.account && entry.SourcePath == reading.Path {
			c.Entries = append(c.Entries[:i], c.Entries[i+1:]...)
			return
		}
	}
}

func (c *usageCache) upsert(entry usageEntry) {
	for i := range c.Entries {
		if c.Entries[i].Tool == entry.Tool && c.Entries[i].Account == entry.Account {
			c.Entries[i] = entry
			return
		}
	}
	c.Entries = append(c.Entries, entry)
}

func (c *usageCache) normalize() {
	sort.Slice(c.Entries, func(i, j int) bool {
		if c.Entries[i].Tool != c.Entries[j].Tool {
			return c.Entries[i].Tool < c.Entries[j].Tool
		}
		return c.Entries[i].Account < c.Entries[j].Account
	})
}
