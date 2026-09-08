package installation

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
)

func testReceipt(t *testing.T) (root string, r Receipt) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "kae")
	dev, ino, err := parentIdentity(destination)
	if err != nil {
		t.Fatal(err)
	}
	r = Receipt{
		SchemaVersion: 1, Source: constants.InstallSourceLocal, Destination: destination,
		Version: "v0.21.0", SHA256: strings.Repeat("a", 64), OS: runtime.GOOS,
		Arch: runtime.GOARCH, Status: constants.InstallActive,
		ParentDevice: dev, ParentInode: ino,
	}
	return filepath.Join(dir, "installations"), r
}

func TestReceiptValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "schema", "path", "source", "digest", "platform", "status", "mode", "duplicate", "symlink", "hardlink", "directory_mode"} {
		t.Run(scenario, func(t *testing.T) {
			root, r := testReceipt(t)
			destination := r.Destination
			switch scenario {
			case "schema":
				r.SchemaVersion = 2
			case "path":
				r.Destination += ".other"
			case "source":
				r.Source = "mise"
			case "digest":
				r.SHA256 = "invalid"
			case "platform":
				r.OS = "unknown"
			case "status":
				r.Status = "unknown"
			}
			if err := save(root, r); err != nil {
				t.Fatal(err)
			}
			path := ReceiptPath(root, destination)
			var err error
			switch scenario {
			case "path":
				err = os.Rename(ReceiptPath(root, r.Destination), path)
			case "mode":
				err = os.Chmod(path, 0o644)
			case "directory_mode":
				err = os.Chmod(root, 0o755)
			case "duplicate":
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				err = os.WriteFile(path, []byte(strings.Replace(string(data), "{", "{\"schema_version\": 1,", 1)), 0o600)
			case "symlink":
				if err := os.Rename(path, path+".target"); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(path+".target", path)
			case "hardlink":
				err = os.Link(path, path+".alias")
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := Load(root, destination)
			if scenario == "valid" {
				if err != nil || got != r {
					t.Fatalf("round trip: %v %v", got, err)
				}
			} else if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unsafe receipt accepted: %v", err)
			}
		})
	}
}

func TestInstallationLockProtocol(t *testing.T) {
	_, r := testReceipt(t)
	lockPath := filepath.Join(filepath.Dir(r.Destination), ".kae.kae-install-lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if release, err := Acquire(r.Destination); !errors.Is(err, lock.ErrBusy) || release != nil {
		t.Fatalf("shell-style lock did not exclude Go: %v", err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(r.Destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); !os.IsExist(err) {
		t.Fatalf("Go lock did not exclude shell-style writer: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestReinstallRetainsRemovedReceiptHistory(t *testing.T) {
	root, r := testReceipt(t)
	r.Status = constants.InstallRemoved
	if err := save(root, r); err != nil {
		t.Fatal(err)
	}
	if err := retainHistory(root, r); err != nil {
		t.Fatal(err)
	}
	r.Status = constants.InstallPending
	if err := save(root, r); err != nil {
		t.Fatal(err)
	}
	dir := historyDir(root, r.Destination)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("removed receipt history lost: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || !strings.Contains(string(data), `"status":"removed"`) {
		t.Fatalf("removed state missing from history: %v", err)
	}
}

func TestReplacementReceiptFailure(t *testing.T) {
	for _, failAt := range []string{"pending", "active"} {
		t.Run(failAt, func(t *testing.T) {
			root, r := testReceipt(t)
			if err := os.WriteFile(r.Destination, []byte("previous binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			r.Status = constants.InstallPending
			failure := errors.New("receipt storage failed")
			_, err := recordReplacement(root, r, []byte("replacement binary"), func(root string, next Receipt) error {
				if (failAt == "pending" && next.Status == constants.InstallPending) ||
					(failAt == "active" && next.Status == constants.InstallActive) {
					return failure
				}
				return save(root, next)
			})
			if !errors.Is(err, failure) {
				t.Fatalf("storage failure hidden: %v", err)
			}
			data, err := os.ReadFile(r.Destination)
			if err != nil {
				t.Fatal(err)
			}
			if failAt == "pending" {
				if string(data) != "previous binary" {
					t.Fatal("binary replaced before pending receipt was stored")
				}
			} else {
				if string(data) != "replacement binary" {
					t.Fatal("replacement lost after finalization failure")
				}
				pending, err := Load(root, r.Destination)
				if err != nil || pending.Status != constants.InstallPending {
					t.Fatalf("repair evidence lost: %v %v", pending, err)
				}
				if _, err := InspectRemoval(root, r.Destination); !errors.Is(err, ErrUnsafe) {
					t.Fatalf("incomplete installation allowed removal: %v", err)
				}
			}
		})
	}
}
