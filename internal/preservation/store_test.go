package preservation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

func fixture(t *testing.T, limit int64) (Store, Origin) {
	t.Helper()
	root := t.TempDir()
	return Store{Dir: filepath.Join(root, "records"), LockDir: filepath.Join(root, "locks"), Backend: secrettest.NewMem(), LimitBytes: limit}, Origin{Directory: filepath.Join(root, "project"), Tool: constants.ToolClaude, BoundAccount: "main", Mode: "shared", ConfigDir: filepath.Join(root, "config"), CredDir: filepath.Join(root, "credentials"), Locator: Locator{Name: "credential", Kind: constants.KindFile, Target: filepath.Join(root, "credentials", "auth")}}
}

func save(t *testing.T, s Store, o Origin, p string) Record {
	t.Helper()
	r, err := s.Save(context.Background(), o, []byte(p), "")
	if err != nil {
		t.Fatal(err)
	}
	return r.Record
}

func records(t *testing.T, s Store) []Record {
	t.Helper()
	rs, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestBudgetAdmissionPrecedesPruning(t *testing.T) {
	s, o := fixture(t, 6)
	a := save(t, s, o, "aa")
	save(t, s, o, "bb")
	save(t, s, o, "cc")
	if _, err := s.Save(context.Background(), o, []byte("dd"), ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("want quota refusal, got %v", err)
	}
	if len(records(t, s)) != 3 {
		t.Fatal("quota refusal changed inventory")
	}
	if _, _, err := s.Load(context.Background(), a.ID); err != nil {
		t.Fatalf("oldest deleted to make room: %v", err)
	}
	dup, err := s.Save(context.Background(), o, []byte("cc"), "")
	if err != nil || !dup.Duplicate {
		t.Fatalf("exact duplicate at capacity: %+v %v", dup, err)
	}
	s.LimitBytes = 1
	if reused, err := s.Save(context.Background(), o, []byte("cc"), ""); err != nil || !reused.Duplicate {
		t.Fatalf("lowered budget blocked exact reuse: %v", err)
	}
	if _, err := s.Save(context.Background(), o, []byte("dd"), ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("lowered budget admitted new bytes: %v", err)
	}
	s.LimitBytes = 8
	r, err := s.Save(context.Background(), o, []byte("dd"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || r.Removed[0] != a.ID || len(records(t, s)) != 3 {
		t.Fatalf("retention: %+v", r)
	}
	if _, _, err := s.Load(context.Background(), a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSharedStoreHistoryAndExactOriginDedup(t *testing.T) {
	s, o := fixture(t, 100)
	a := save(t, s, o, "a")
	sibling := o
	sibling.Directory += "-side"
	sibling.ConfigDir += "-side"
	sibling.Mode = "isolated"
	duplicate, err := s.Save(context.Background(), sibling, []byte("a"), "")
	if err != nil || duplicate.Duplicate {
		t.Fatalf("distinct restoration mapping reused: %+v %v", duplicate, err)
	}
	save(t, s, sibling, "b")
	save(t, s, o, "c")
	if len(records(t, s)) != 3 {
		t.Fatal("siblings did not share latest-three history")
	}
	if _, _, err := s.Load(context.Background(), a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("old shared-store record survived")
	}
	other := o
	other.BoundAccount = "side"
	save(t, s, other, "d")
	if len(records(t, s)) != 4 {
		t.Fatal("different configured mapping was pruned")
	}
}

func TestProtectedRestoreRecordRefusesBeforeSave(t *testing.T) {
	s, o := fixture(t, 100)
	old := save(t, s, o, "a")
	save(t, s, o, "b")
	newest := save(t, s, o, "c")
	if _, err := s.Save(context.Background(), o, []byte("d"), old.ID); !errors.Is(err, ErrProtected) {
		t.Fatalf("protected victim: %v", err)
	}
	if len(records(t, s)) != 3 {
		t.Fatal("protected refusal wrote a record")
	}
	if _, err := s.Save(context.Background(), o, []byte("d"), newest.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(context.Background(), newest.ID); err != nil {
		t.Fatal("protected survivor lost")
	}
}

type failingBackend struct {
	secret.Backend
	setAfterWrite, deleteFailure bool
}

func (b *failingBackend) Set(ctx context.Context, k string, v []byte) error {
	if err := b.Backend.Set(ctx, k, v); err != nil {
		return err
	}
	if b.setAfterWrite {
		return errors.New("ambiguous set failure")
	}
	return nil
}

func (b *failingBackend) Delete(ctx context.Context, k string) error {
	if b.deleteFailure {
		return errors.New("delete failure")
	}
	return b.Backend.Delete(ctx, k)
}

func TestInterruptedWritesRemainVisibleAndCharged(t *testing.T) {
	s, o := fixture(t, 100)
	first := save(t, s, o, "a")
	save(t, s, o, "b")
	save(t, s, o, "c")
	be := &failingBackend{Backend: s.Backend, setAfterWrite: true}
	s.Backend = be
	result, err := s.Save(context.Background(), o, []byte("new"), "")
	if err == nil || result.Record.State != constants.PreservationStatePending {
		t.Fatalf("pending result %+v %v", result, err)
	}
	rs := records(t, s)
	if len(rs) != 4 || rs[0].Size != 3 {
		t.Fatalf("reservation not retained: %+v", rs)
	}
	if _, _, err := s.Load(context.Background(), first.ID); err != nil {
		t.Fatal("save failure pruned old record")
	}
	be.setAfterWrite = false
	if _, err := s.Save(context.Background(), o, []byte("next"), ""); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("ambiguous reservation bypassed: %v", err)
	}
	if err := s.Remove(context.Background(), result.Record.ID); err != nil {
		t.Fatal(err)
	}
	if len(records(t, s)) != 3 {
		t.Fatal("explicit pending removal failed")
	}
	be.deleteFailure = true
	result, err = s.Save(context.Background(), o, []byte("new"), "")
	if err == nil || result.Record.State != constants.PreservationStateReady {
		t.Fatalf("new copy not committed before prune: %+v %v", result, err)
	}
	if _, payload, err := s.Load(context.Background(), result.Record.ID); err != nil || string(payload) != "new" {
		t.Fatal("new copy not durable")
	}
	if len(records(t, s)) != 4 {
		t.Fatal("failed deletion released charge")
	}
	be.deleteFailure = false
	if err := s.Remove(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if len(records(t, s)) != 3 {
		t.Fatal("deletion retry failed")
	}
}

func TestCorruptPayloadAndInvalidIDFailClosed(t *testing.T) {
	s, o := fixture(t, 100)
	r := save(t, s, o, "safe")
	if err := s.Backend.Set(context.Background(), secretRef(r.ID), []byte("changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), o, []byte("new"), ""); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("corrupt accounting accepted: %v", err)
	}
	for _, id := range []string{"../outside", "", r.ID + ".json"} {
		if err := s.Remove(context.Background(), id); !errors.Is(err, ErrInvalidID) {
			t.Fatal(err)
		}
	}
	if err := s.Remove(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyInventoryAndSerialization(t *testing.T) {
	s, o := fixture(t, 100)
	if rs, err := ListRecords(s.Dir); err != nil || len(rs) != 0 {
		t.Fatalf("missing inventory: %v %v", rs, err)
	}
	if _, err := os.Stat(s.LockDir); !os.IsNotExist(err) {
		t.Fatal("completion created locks")
	}
	l, err := lock.Acquire(s.LockDir, "preservation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), o, []byte("a"), ""); !errors.Is(err, lock.ErrBusy) {
		t.Fatalf("concurrent admission: %v", err)
	}
	l.Release()
	r := save(t, s, o, "a")
	if err := os.Symlink(filepath.Join(s.Dir, r.ID+".json"), filepath.Join(s.Dir, "outside.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), o, []byte("b"), ""); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("symlink metadata accepted: %v", err)
	}
}

func TestRestoreSessionOwnsSelectedAndDisplacedCopies(t *testing.T) {
	s, o := fixture(t, 100)
	r := save(t, s, o, "original")
	var expired *RestoreSession
	applyFailure := errors.New("live write failed")
	err := s.WithRestore(context.Background(), r.ID, func(session *RestoreSession) error {
		expired = session
		if string(session.Payload) != "original" {
			t.Fatal("selected payload differs")
		}
		if err := s.Remove(context.Background(), r.ID); !errors.Is(err, lock.ErrBusy) {
			t.Fatalf("selected record not locked: %v", err)
		}
		if _, err := session.SaveCurrent([]byte("displaced")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Save(context.Background(), o, []byte("concurrent"), ""); !errors.Is(err, lock.ErrBusy) {
			t.Fatalf("restore lost lock after save: %v", err)
		}
		return applyFailure
	})
	if !errors.Is(err, applyFailure) {
		t.Fatal(err)
	}
	if len(records(t, s)) != 2 {
		t.Fatal("failed live write lost recovery copies")
	}
	if _, err := expired.SaveCurrent([]byte("outside callback")); err == nil {
		t.Fatal("closed session accepted write")
	}
}

func TestRestorePreviewUsesAdmissionWithoutMutation(t *testing.T) {
	s, o := fixture(t, 3)
	oldest := save(t, s, o, "a")
	save(t, s, o, "b")
	newest := save(t, s, o, "c")
	preview := func(id string, payload string) error {
		return s.WithRestore(context.Background(), id, func(session *RestoreSession) error { return session.CheckCurrent([]byte(payload)) })
	}
	if err := preview(newest.ID, "d"); !errors.Is(err, ErrQuota) {
		t.Fatalf("preview missed quota: %v", err)
	}
	s.LimitBytes = 10
	if err := preview(oldest.ID, "d"); !errors.Is(err, ErrProtected) {
		t.Fatalf("preview missed protected victim: %v", err)
	}
	if err := preview(newest.ID, "d"); err != nil {
		t.Fatal(err)
	}
	rs := records(t, s)
	if len(rs) != 3 || rs[2].ID != oldest.ID {
		t.Fatal("preview modified history")
	}
}
