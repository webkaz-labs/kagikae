package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testTag = "v1.2.3"

func fixture(t *testing.T, dir, defect string) []string {
	t.Helper()
	names, _ := archivesFor(testTag)
	var manifest strings.Builder
	for _, name := range names {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		z := gzip.NewWriter(f)
		w := tar.NewWriter(z)
		members := []string{"kae", "LICENSE", "README.md"}
		switch defect {
		case "extra":
			members = append(members, "extra")
		case "duplicate":
			members[2] = "kae"
		case "traversal":
			members[2] = "../README.md"
		case "missing-member":
			members = members[:2]
		}
		for _, member := range members {
			data := []byte("synthetic, never execute")
			h := &tar.Header{Name: member, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}
			if member == "kae" {
				switch defect {
				case "symlink":
					h.Typeflag = tar.TypeSymlink
					h.Size = 0
					h.Linkname = "LICENSE"
				case "hardlink":
					h.Typeflag = tar.TypeLink
					h.Size = 0
					h.Linkname = "LICENSE"
				case "directory":
					h.Typeflag = tar.TypeDir
					h.Size = 0
				}
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Typeflag == tar.TypeReg {
				if _, err := w.Write(data); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(bytes), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(manifest.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return names
}

func TestArchiveControls(t *testing.T) {
	for _, defect := range []string{"", "extra", "duplicate", "traversal", "missing-member", "symlink", "hardlink", "directory", "corrupt", "manifest-duplicate", "missing"} {
		t.Run(defect, func(t *testing.T) {
			dir := t.TempDir()
			names := fixture(t, dir, defect)
			switch defect {
			case "corrupt":
				if err := os.WriteFile(filepath.Join(dir, names[0]), []byte("bad"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
					t.Fatal(err)
				}
			case "manifest-duplicate":
				p := filepath.Join(dir, "checksums.txt")
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, append(b, b...), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := verifyArchives(dir, names)
			if (err != nil) != (defect != "") {
				t.Fatalf("defect=%s err=%v", defect, err)
			}
		})
	}
}

func TestVerificationStageOrder(t *testing.T) {
	t.Setenv("SMOKE_WHOLE_FILE", "1")
	for _, failure := range []string{"", "attestation", "version", "installer"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			fixture(t, dir, "")
			attestations := 0
			names, _ := archivesFor(testTag)
			downloaded := false
			executed := false
			installer := false
			run := func(name string, args, env []string, cwd string) (string, error) {
				if name == "gh" {
					if cwd != dir || env != nil {
						t.Fatal("gh execution context changed")
					}
					if args[0] == "attestation" {
						if !downloaded || attestations >= len(names) {
							t.Fatal("attestation without download or duplicate verification")
						}
						want := []string{"attestation", "verify", filepath.Join(dir, names[attestations]), "--repo", repository}
						if !reflect.DeepEqual(args, want) {
							t.Fatalf("attestation argv = %q; want %q", args, want)
						}
						attestations++
						if failure == "attestation" {
							return "", errors.New("proof failed")
						}
					} else {
						want := []string{"release", "download", testTag, "--repo", repository, "--dir", dir, "--pattern", "checksums.txt", "--pattern", "kae_*.tar.gz"}
						if downloaded || !reflect.DeepEqual(args, want) {
							t.Fatalf("download argv = %q; want %q", args, want)
						}
						downloaded = true
					}
					return "", nil
				}
				if name == "/bin/sh" {
					wantArgs := []string{"-eu", "-c", `. scripts/smoke-env.sh; "$1" version`, "sh", filepath.Join(dir, "kae")}
					wantEnv := []string{"PATH=/usr/bin:/bin", "TMPDIR=" + dir}
					if !reflect.DeepEqual(args, wantArgs) || !reflect.DeepEqual(env, wantEnv) || cwd != dir {
						t.Fatalf("native version must use canonical isolation: args=%q env=%q cwd=%q", args, env, cwd)
					}
					executed = true
					if attestations != 4 {
						t.Fatal("binary executed before all attestations")
					}
					if failure == "version" {
						return "wrong", nil
					}
					return "kae " + testTag, nil
				}
				if name == "bash" {
					wholeModes := 0
					for _, entry := range env {
						if strings.HasPrefix(entry, "SMOKE_WHOLE_FILE=") {
							wholeModes++
							if entry != "SMOKE_WHOLE_FILE=0" {
								t.Fatal("installer inherited whole-file mode")
							}
						}
					}
					if wholeModes != 1 {
						t.Fatal("installer must force per-line verdicts")
					}
					installer = true
					if failure == "installer" {
						return "", errors.New("installer failed")
					}
					return "", nil
				}
				t.Fatalf("unexpected command %s", name)
				return "", nil
			}
			got, err := verify(testTag, dir, dir, "darwin", "arm64", run)
			if (err != nil) != (failure != "") {
				t.Fatalf("got %+v err %v", got, err)
			}
			if failure == "" && got.Status != "success" {
				t.Fatal(got)
			}
			if failure == "attestation" {
				if executed || installer {
					t.Fatal("executed after failed attestation")
				}
				if _, err := os.Stat(filepath.Join(dir, "kae")); !os.IsNotExist(err) {
					t.Fatal("binary materialized before attestation")
				}
			}
			if failure == "version" && installer {
				t.Fatal("installer executed after version mismatch")
			}
		})
	}
}

func TestPreconditions(t *testing.T) {
	for _, tag := range []string{"latest", "v1.2", "v1.2.3/other", "v1.2.3;echo"} {
		if _, err := archivesFor(tag); err == nil {
			t.Fatalf("accepted %q", tag)
		}
	}
	called := false
	_, err := verify(testTag, t.TempDir(), t.TempDir(), "windows", "amd64", func(string, []string, []string, string) (string, error) { called = true; return "", nil })
	if err == nil || called {
		t.Fatal("unsupported platform did not stop before download")
	}
}

func TestCurlFixtureRejectsUnknownURL(t *testing.T) {
	dir := t.TempDir()
	native := "archive.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, native), []byte("verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installerSmoke(dir, dir, testTag, native, func(string, []string, []string, string) (string, error) { return "", nil }); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "download")
	args := []string{"--fail", "--location", "--silent", "--show-error", "--output", output, "https://github.com/" + repository + "/releases/download/" + testTag + "/" + native}
	if _, err := command(filepath.Join(dir, "curl"), args, nil, dir); err != nil {
		t.Fatal(err)
	}
	args[6] = "https://example.com/unknown"
	if _, err := command(filepath.Join(dir, "curl"), args, nil, dir); err == nil {
		t.Fatal("unknown URL accepted")
	}
	b, err := os.ReadFile(output)
	if err != nil || string(b) != "verified" {
		t.Fatalf("download changed: %q %v", b, err)
	}
}
