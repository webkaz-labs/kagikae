package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const fixtureCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fixtureServer struct {
	releases map[string]map[string]any
	files    map[string][]byte
	requests atomic.Int64
}

func (f *fixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if _, ok := r.Header["Authorization"]; ok {
		http.Error(w, "fixture received an authorization header", http.StatusBadRequest)
		return
	}
	prefix := "/api.github.com/repos/webkaz-labs/kagikae/releases"
	var payload []byte
	switch {
	case r.URL.Path == prefix:
		releases := []map[string]any{}
		// Publication finishes before the first consumer request.
		tags := make([]string, 0, len(f.releases))
		for tag := range f.releases {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			releases = append(releases, f.releases[tag])
		}
		payload, _ = json.Marshal(releases)
	case strings.HasPrefix(r.URL.Path, prefix+"/tags/"):
		release, ok := f.releases[strings.TrimPrefix(r.URL.Path, prefix+"/tags/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		payload, _ = json.Marshal(release)
	default:
		var ok bool
		payload, ok = f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
	_, _ = w.Write(payload)
}

func (s *scenario) archive(stage, path string) {
	if s.err != nil {
		return
	}
	var data bytes.Buffer
	z := gzip.NewWriter(&data)
	tw := tar.NewWriter(z)
	for _, name := range []string{"kae", "completions/kae.bash", "completions/kae.zsh", "completions/kae.fish"} {
		contents := s.read(filepath.Join(stage, name))
		if s.err != nil {
			return
		}
		mode := int64(0o644)
		if name == "kae" {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			s.err = err
			return
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			s.err = err
			return
		}
	}
	if s.err = tw.Close(); s.err != nil {
		return
	}
	if s.err = z.Close(); s.err != nil {
		return
	}
	s.write(path, data.String(), 0o600)
}

func (s *scenario) copySource(source string) {
	for _, name := range []string{"go.mod", "go.sum", "main.go"} {
		s.copy(filepath.Join(s.repo, name), filepath.Join(source, name), 0o600)
	}
	if s.err != nil {
		return
	}
	s.err = filepath.WalkDir(filepath.Join(s.repo, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source is not a regular file: %s", path)
		}
		rel, err := filepath.Rel(s.repo, path)
		if err != nil {
			return err
		}
		s.copy(path, filepath.Join(source, rel), 0o600)
		return s.err
	})
}

func (s *scenario) publish(root, source, original, packslip, key, otherKey, version, defect string, build bool, backend *fixtureServer) {
	if s.err != nil {
		return
	}
	tag := "v" + version
	stage := filepath.Join(root, version)
	binary := filepath.Join(root, "kae")
	if build {
		needle := `toolVersion = "v0.21.0"`
		s.require(strings.Count(original, needle) == 1, "fixture version constant is missing or ambiguous")
		s.write(filepath.Join(source, "internal/cmd/cmd.go"), strings.Replace(original, needle, `toolVersion = "`+tag+`"`, 1), 0o600)
		s.run(source, "go", "build", "-o", binary, ".")
	}
	s.copy(binary, filepath.Join(stage, "kae"), 0o755)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script := s.run(stage, binary, "completion", shell)
		marker := "fixture-static-" + version
		// These differences are confined to fixture resources, not shipped scripts.
		switch shell {
		case "bash":
			needle := `compgen -W "$(kae __complete commands)"`
			s.require(strings.Contains(script, needle), "bash fixture anchor missing")
			script = strings.Replace(script, needle, `compgen -W "`+marker+` $(kae __complete commands)"`, 1)
		case "zsh":
			needle := `compadd -- ${(f)"$(kae __complete commands)"}`
			s.require(strings.Contains(script, needle), "zsh fixture anchor missing")
			script = strings.Replace(script, needle, `compadd -- `+marker+` ${(f)"$(kae __complete commands)"}`, 1)
		default:
			script += "\ncomplete -c kae -f -a " + marker + "\n"
		}
		s.write(filepath.Join(stage, "completions/kae."+shell), script, 0o600)
	}
	system := runtime.GOOS
	if defect == "platform" {
		system = "windows"
	}
	name := fmt.Sprintf("kae_%s_%s_%s.tar.gz", version, system, runtime.GOARCH)
	archive := filepath.Join(stage, name)
	s.archive(stage, archive)
	selectedProject := project
	if defect == "project" {
		selectedProject += "-other"
	}
	selectedKey := key
	if defect == "key" {
		selectedKey = otherKey
	}
	base := "https://github.com/webkaz-labs/kagikae/releases/download/" + tag
	args := []string{packslip, "create", "--project", selectedProject, "--version", version, "--tag", tag, "--source-repo", "https://github.com/webkaz-labs/kagikae", "--commit", fixtureCommit, "--url-base", base, "--bin", "kae", "--key", selectedKey, "--no-log", "--out", stage}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		args = append(args, "--resource", "completion/"+shell+"=archive:completions/kae."+shell)
	}
	s.run(root, append(args, archive)...)
	if defect == "digest" {
		contents := s.read(archive)
		s.write(archive, contents+"tampered", 0o600)
	}
	assets := []map[string]any{}
	for _, asset := range []string{archive, filepath.Join(stage, "packslip.sigstore.json")} {
		data := []byte(s.read(asset))
		url := base + "/" + filepath.Base(asset)
		assets = append(assets, map[string]any{"id": len(backend.files) + 1, "name": filepath.Base(asset), "size": len(data), "browser_download_url": url, "url": url, "content_type": "application/octet-stream"})
		if defect != "missing" || asset != archive {
			backend.files[strings.TrimPrefix(url, "https:/")] = data
		}
	}
	backend.releases[tag] = map[string]any{"id": len(backend.releases) + 1, "tag_name": tag, "name": tag, "draft": false, "prerelease": false, "created_at": "2026-09-01T00:00:00Z", "published_at": "2026-09-01T00:00:00Z", "assets": assets}
}

func (s *scenario) fixture() (result any, err error) {
	packslip := os.Getenv("PACKSLIP_BIN")
	if packslip == "" {
		packslip = "packslip"
	}
	s.require(strings.TrimSpace(s.run(s.home, packslip, "--version")) == "packslip 1.1.1", "fixture signing CLI must be Packslip 1.1.1")
	if s.err != nil {
		return nil, s.err
	}
	s.env["GOPROXY"] = "off"
	root, err := os.MkdirTemp(s.home, "packslip-fixtures-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := os.RemoveAll(root); e != nil && err == nil {
			err = e
		}
	}()
	source := filepath.Join(root, "source")
	s.copySource(source)
	original := s.read(filepath.Join(source, "internal/cmd/cmd.go"))
	key := filepath.Join(root, "fixture.key")
	otherKey := filepath.Join(root, "other.key")
	s.run(root, packslip, "keygen", "--out", key)
	publicLines := strings.Split(s.read(filepath.Join(root, "fixture.pub")), "\n")
	if s.err != nil {
		return nil, s.err
	}
	if len(publicLines) < 2 {
		return nil, fmt.Errorf("fixture public key missing")
	}
	publicKey := publicLines[1]
	s.run(root, packslip, "keygen", "--out", otherKey)
	backend := &fixtureServer{releases: map[string]map[string]any{}, files: map[string][]byte{}}
	s.publish(root, source, original, packslip, key, otherKey, "0.21.0", "", true, backend)
	s.publish(root, source, original, packslip, key, otherKey, "0.21.1", "", true, backend)
	defects := []string{"key", "project", "digest", "platform", "missing"}
	for index, defect := range defects {
		s.publish(root, source, original, packslip, key, otherKey, fmt.Sprintf("0.21.%d", index+2), defect, false, backend)
	}
	if s.err != nil {
		return nil, s.err
	}
	server := httptest.NewUnstartedServer(backend)
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Start()
	defer server.Close()
	global := filepath.Join(s.env["XDG_CONFIG_HOME"], "mise")
	settings := "[settings]\nminimum_release_age = \"0\"\nlockfile = true\n[settings.url_replacements]\n'regex:^https?://([^/]+)/(.*)$' = " + quote(server.URL+"/$1/$2") + "\n"
	s.write(filepath.Join(global, "config.toml"), settings, 0o600)
	s.lifecycle(root, global, publicKey, backend, defects)
	return map[string]any{"status": "success", "mise": "2026.9.3", "trust": "ephemeral key/unlogged fixture only", "checks": s.checks, "requests": backend.requests.Load()}, s.err
}
