package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
)

func fixtureSpec() Spec {
	return Spec{SchemaVersion: 1, Repository: "example/side", Project: "github.com/example/side", Tag: "v1.2.3", Version: "1.2.3", Commit: strings.Repeat("a", 40), Workflow: ".github/workflows/publish.yml", Identity: "https://github.com/example/side/.github/workflows/publish.yml@refs/tags/v1.2.3", Issuer: "https://token.actions.githubusercontent.com", Artifacts: []Artifact{{Name: "side_1.2.3_darwin_arm64.tar.gz", OS: "darwin", Arch: "aarch64", Binary: "bin/side", Members: []string{"bin/side", "NOTICE"}}}, Resources: []map[string]string{}}
}

func put(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.WriteFile(p, b, 0o600); e != nil {
		t.Fatal(e)
	}
}

func tarBytes(t *testing.T, names []string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for _, name := range names {
		data := []byte("notice")
		if name == "bin/side" {
			data = []byte("#!/bin/sh\nprintf 'side v1.2.3\\n'\n")
		}
		h := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: kind}
		if kind != tar.TypeReg {
			h.Size = 0
		}
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if kind == tar.TypeReg {
			if _, e := w.Write(data); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := errors.Join(w.Close(), z.Close()); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func fixtures(t *testing.T) (Spec, string, string) {
	t.Helper()
	s := fixtureSpec()
	root := t.TempDir()
	archive := tarBytes(t, s.Artifacts[0].Members, tar.TypeReg)
	put(t, filepath.Join(root, s.Artifacts[0].Name), archive)
	put(t, filepath.Join(root, "checksums.txt"), fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(archive), s.Artifacts[0].Name))
	bundle := filepath.Join(root, "packslip.sigstore.json")
	put(t, bundle, []byte("fixture signature"))
	return s, root, bundle
}

func statement(t *testing.T, s Spec, root string) string {
	t.Helper()
	a := s.Artifacts[0]
	data, e := os.ReadFile(filepath.Join(root, a.Name))
	if e != nil {
		t.Fatal(e)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	v := map[string]any{"_type": "https://in-toto.io/Statement/v1", "predicateType": "https://packslip.dev/release/v1", "subject": []any{map[string]any{"name": a.Name, "digest": map[string]string{"sha256": digest}}}, "predicate": map[string]any{"project": s.Project, "version": s.Version, "source": map[string]string{"repo": "https://github.com/" + s.Repository, "tag": s.Tag, "commit": s.Commit}, "artifacts": []any{map[string]any{"name": a.Name, "os": a.OS, "arch": a.Arch, "size": len(data), "url": "https://github.com/" + s.Repository + "/releases/download/" + s.Tag + "/" + a.Name, "format": "tar.gz", "bin": []string{a.Binary}, "provenance": []string{"https://api.github.com/repos/" + s.Repository + "/attestations/sha256:" + digest}}}, "resources": s.Resources}}
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func TestForeignProductPipelineAndSnapshot(t *testing.T) {
	s, root, bundle := fixtures(t)
	raw := statement(t, s, root)
	var called []string
	v, e := Verify(s, root, bundle, root, func(name string, args, env []string, cwd string) (string, error) {
		called = append(called, name+" "+args[0])
		if name == "packslip" && args[0] == "show" {
			put(t, filepath.Join(root, s.Artifacts[0].Name), []byte("source replaced"))
			return raw, nil
		}
		return "", nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(called, ",") != "gh attestation,packslip verify,packslip show" {
		t.Fatal(called)
	}
	if e = v.RunNative(context.Background(), s.Artifacts[0].Name, []string{"version"}, "side v1.2.3\n"); e != nil {
		t.Fatal(e)
	}
	installer := filepath.Join(t.TempDir(), "install.sh")
	put(t, installer, []byte(`set -eu
version=$2
destination=$4
mkdir -p "$destination"
curl --fail --location --silent --show-error --output "$TMPDIR/archive.tar.gz" "https://github.com/$SIDE_REPO/releases/download/$version/side_1.2.3_darwin_arm64.tar.gz"
tar -xzf "$TMPDIR/archive.tar.gz" -C "$TMPDIR" bin/side
cp "$TMPDIR/bin/side" "$destination/side"
chmod +x "$destination/side"
`))
	if e = v.RunInstaller(context.Background(), Installer{Path: installer, RepositoryEnv: "SIDE_REPO", Binary: "side", VersionArgs: []string{"version"}, Expected: "side v1.2.3\n"}); e != nil {
		t.Fatal(e)
	}
}

func TestSignatureFailureStopsBeforeShowOrExecution(t *testing.T) {
	s, root, bundle := fixtures(t)
	var calls []string
	v, e := Verify(s, root, bundle, root, func(name string, args, env []string, cwd string) (string, error) {
		calls = append(calls, name+" "+args[0])
		if name == "packslip" {
			return "", errors.New("invalid signature")
		}
		return "", nil
	})
	if e == nil || len(calls) != 2 {
		t.Fatal(e, calls)
	}
	if e = v.RunNative(context.Background(), s.Artifacts[0].Name, nil, ""); e == nil {
		t.Fatal("unverified native run")
	}
}

func TestArchiveControls(t *testing.T) {
	for _, defect := range []string{"missing", "extra", "duplicate", "symlink", "checksum", "gzip", "extra archive", "manifest duplicate", "unsafe name", "empty members"} {
		t.Run(defect, func(t *testing.T) {
			s, root, _ := fixtures(t)
			a := &s.Artifacts[0]
			names := append([]string(nil), a.Members...)
			kind := byte(tar.TypeReg)
			switch defect {
			case "missing":
				names = names[:1]
			case "extra":
				names = append(names, "unexpected")
			case "duplicate":
				names = append(names, names[0])
			case "symlink":
				kind = tar.TypeSymlink
			case "unsafe name":
				a.Name = "../escape.tar.gz"
			case "empty members":
				a.Members = nil
			}
			if defect == "unsafe name" || defect == "empty members" {
				if e := VerifyArchives(root, s.Artifacts); e == nil {
					t.Fatal("accepted")
				}
				return
			}
			data := tarBytes(t, names, kind)
			if defect == "gzip" {
				data = data[:len(data)-5]
			}
			put(t, filepath.Join(root, a.Name), data)
			manifest := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), a.Name)
			if defect == "checksum" {
				manifest = strings.Repeat("0", 64) + "  " + a.Name + "\n"
			}
			if defect == "manifest duplicate" {
				manifest += manifest
			}
			put(t, filepath.Join(root, "checksums.txt"), []byte(manifest))
			if defect == "extra archive" {
				put(t, filepath.Join(root, "extra.tar.gz"), data)
			}
			if e := VerifyArchives(root, s.Artifacts); e == nil {
				t.Fatal("accepted " + defect)
			}
		})
	}
}

func TestSpecControls(t *testing.T) {
	s := fixtureSpec()
	raw, _ := json.Marshal(s)
	if _, e := ParseSpec(raw); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Spec){func(s *Spec) { s.Artifacts[0].Name = "../side.tar.gz" }, func(s *Spec) { s.Artifacts[0].Members = []string{"/absolute"} }, func(s *Spec) { s.Artifacts[0].Members = []string{"bin/side", "bin/side"} }, func(s *Spec) { s.Identity = ".*" }, func(s *Spec) { s.Artifacts = nil }} {
		s := fixtureSpec()
		mutate(&s)
		if e := s.Validate(); e == nil {
			t.Fatal("invalid spec accepted")
		}
	}
	raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
	if _, e := ParseSpec(raw); e == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestFixtureFinalVerdict(t *testing.T) {
	for _, auth := range []bool{false, true} {
		f := NewFixture(map[string]Response{"/asset": {Body: []byte("ok")}, "/missing": {Status: 404}})
		r := httptest.NewRequest(http.MethodGet, "http://fixture/missing", nil)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if f.Verdict() != nil || w.Code != 404 {
			t.Fatal("declared missing route")
		}
		r = httptest.NewRequest(http.MethodGet, "http://fixture/unknown", nil)
		if auth {
			r.Header.Set("Authorization", "fixture")
		}
		f.ServeHTTP(httptest.NewRecorder(), r)
		if f.Verdict() == nil {
			t.Fatal("rejection not sticky")
		}
	}
}

func TestInstallerCannotIgnoreFixtureRefusal(t *testing.T) {
	s, root, bundle := fixtures(t)
	raw := statement(t, s, root)
	v, e := Verify(s, root, bundle, root, func(name string, args, env []string, cwd string) (string, error) {
		if name == "packslip" && args[0] == "show" {
			return raw, nil
		}
		return "", nil
	})
	if e != nil {
		t.Fatal(e)
	}
	installer := filepath.Join(t.TempDir(), "install.sh")
	put(t, installer, []byte(`set -eu
curl ignored-bad-argv || true
mkdir -p "$4"
printf '#!/bin/sh\nprintf "side v1.2.3\\n"\n' > "$4/side"
chmod +x "$4/side"
`))
	if e = v.RunInstaller(context.Background(), Installer{Path: installer, RepositoryEnv: "SIDE_REPO", Binary: "side", Expected: "side v1.2.3\n"}); e == nil || !strings.Contains(e.Error(), "transport rejection") {
		t.Fatalf("ignored rejection: %v", e)
	}
}

func TestFixtureVerdictPathIsNotRewritten(t *testing.T) {
	root := filepath.Join(t.TempDir(), "exit 90")
	if e := os.Mkdir(root, 0o700); e != nil {
		t.Fatal(e)
	}
	asset := filepath.Join(root, "asset")
	put(t, asset, []byte("fixture"))
	marker := filepath.Join(root, "rejected")
	shim, e := CurlFixtureWithVerdict(map[string]string{"https://example.com/asset": asset}, marker)
	if e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(root, "curl")
	put(t, script, []byte(shim))
	if e = os.Chmod(script, 0o700); e != nil {
		t.Fatal(e)
	}
	r, e := (commandrun.Command{Name: script, Args: []string{"invalid"}, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + root}, Dir: root, Timeout: time.Second}).Run(context.Background())
	if e != nil || r.ExitCode != 90 {
		t.Fatal(e, r)
	}
	if _, e = os.Stat(marker); e != nil {
		t.Fatal("marker path rewritten", e)
	}
}
