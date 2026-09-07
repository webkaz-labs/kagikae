// Package preservation owns bounded, original-store credential copies. It never
// attributes their bytes to an account or resolves a live restoration target.
package preservation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

var (
	ErrQuota      = errors.New("preservation payload budget exceeded")
	ErrNotFound   = errors.New("preservation record not found")
	ErrIncomplete = errors.New("preservation inventory needs repair")
	ErrInvalidID  = errors.New("invalid preservation id")
	ErrProtected  = errors.New("preservation retention would delete the selected restore record")
	idPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidID validates the opaque identifier accepted by record operations.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Locator records addressing evidence, not instructions to redirect a restore.
type Locator struct {
	Name                 string `json:"name"`
	Kind                 string `json:"kind"`
	Target               string `json:"target"`
	Pointer              string `json:"pointer,omitempty"`
	KeychainAccount      string `json:"keychain_account,omitempty"`
	KeychainMatchAccount bool   `json:"keychain_match_account,omitempty"`
	JSONC                bool   `json:"jsonc,omitempty"`
}

// Origin describes the binding observed at capture time. BoundAccount is its
// label, never a claim about the identity of the credential's owner.
type Origin struct {
	Directory    string  `json:"directory"`
	Tool         string  `json:"tool"`
	BoundAccount string  `json:"bound_account"`
	Mode         string  `json:"mode"`
	ConfigDir    string  `json:"config_dir"`
	CredDir      string  `json:"cred_dir,omitempty"`
	Locator      Locator `json:"locator"`
}

type Record struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Origin        Origin    `json:"origin"`
	Size          int64     `json:"size"`
	Digest        string    `json:"digest"`
	State         string    `json:"state"`
}

type SaveResult struct {
	Record    Record
	Duplicate bool
	Removed   []string
}

// Store serializes every operation with one advisory lock. A command holding a
// binding lock must acquire it before entering Store, never in the reverse order.
type Store struct {
	Dir        string
	LockDir    string
	Backend    secret.Backend
	LimitBytes int64
}

func (s Store) acquire() (*lock.Lock, error) {
	if !filepath.IsAbs(s.Dir) || !filepath.IsAbs(s.LockDir) {
		return nil, fmt.Errorf("preservation requires absolute metadata and lock directories")
	}
	return lock.Acquire(s.LockDir, "preservation")
}

func validOrigin(o Origin) bool {
	return filepath.IsAbs(o.Directory) && filepath.IsAbs(o.ConfigDir) && (o.CredDir == "" || filepath.IsAbs(o.CredDir)) && o.Tool != "" && o.BoundAccount != "" && o.Mode != "" && o.Locator.Name != "" && o.Locator.Kind != "" && o.Locator.Target != ""
}

// sameHistory groups sibling directories addressing the same credential location
// under the same configured label. It does not infer credential ownership.
func sameHistory(a, b Origin) bool {
	return a.Tool == b.Tool && a.BoundAccount == b.BoundAccount && a.Locator == b.Locator
}

func validRecord(r Record) bool {
	return r.SchemaVersion == 1 && ValidID(r.ID) && !r.CreatedAt.IsZero() && validOrigin(r.Origin) && r.Size >= 0 && digestPattern.MatchString(r.Digest) && (r.State == constants.PreservationStatePending || r.State == constants.PreservationStateReady || r.State == constants.PreservationStateDeleting)
}

func (s Store) inventory() ([]Record, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		// Atomic-write leftovers contain metadata only; the published pending record
		// already reserves payload capacity before any backend write can start.
		if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: unexpected metadata entry", ErrIncomplete)
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var r Record
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("%w: invalid metadata", ErrIncomplete)
		}
		if err := dec.Decode(new(any)); err != io.EOF || !validRecord(r) || entry.Name() != r.ID+".json" {
			return nil, fmt.Errorf("%w: invalid metadata", ErrIncomplete)
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}

// List includes pending/deleting records: these still reserve quota and can be
// removed explicitly after interrupted writes. It never reads payload bytes.
// ListRecords reads metadata without acquiring locks, creating directories or
// accessing secrets. Completion uses this advisory snapshot; mutations must use
// Store methods, which re-read the inventory under their lock.
func ListRecords(dir string) ([]Record, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("preservation requires an absolute metadata directory")
	}
	return (Store{Dir: dir}).inventory()
}

func (s Store) List(ctx context.Context) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer l.Release()
	return s.inventory()
}

func secretRef(id string) string { return secret.NSPreservation + "/" + id + "/payload" }
func digest(data []byte) string  { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func (s Store) payload(ctx context.Context, r Record) ([]byte, error) {
	if s.Backend == nil {
		return nil, errors.New("preservation secret backend is required")
	}
	if r.State != constants.PreservationStateReady {
		return nil, ErrIncomplete
	}
	data, found, err := s.Backend.Get(ctx, secretRef(r.ID))
	if err != nil {
		return nil, err
	}
	if !found || int64(len(data)) != r.Size || digest(data) != r.Digest {
		return nil, fmt.Errorf("%w: payload missing or inconsistent for %s", ErrIncomplete, r.ID)
	}
	return data, nil
}

func (s Store) Load(ctx context.Context, id string) (Record, []byte, error) {
	if !ValidID(id) {
		return Record{}, nil, ErrInvalidID
	}
	l, err := s.acquire()
	if err != nil {
		return Record{}, nil, err
	}
	defer l.Release()
	return s.load(ctx, id)
}

func (s Store) load(ctx context.Context, id string) (Record, []byte, error) {
	records, err := s.inventory()
	if err != nil {
		return Record{}, nil, err
	}
	for _, r := range records {
		if r.ID == id {
			data, err := s.payload(ctx, r)
			return r, data, err
		}
	}
	return Record{}, nil, ErrNotFound
}

func (s Store) publish(r Record) error {
	if err := patch.MkdirAllDurable(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := patch.WriteFileAtomic(filepath.Join(s.Dir, r.ID+".json"), data, 0o600); err != nil {
		return err
	}
	return patch.SyncDir(s.Dir)
}

// Save deduplicates exact origin and payload bytes. Capacity admission includes
// the new bytes before pruning; it never deletes a copy to make room. A pending
// reservation survives any ambiguous backend failure and requires explicit removal.
func (s Store) Save(ctx context.Context, origin Origin, data []byte, protectID string) (SaveResult, error) {
	var result SaveResult
	if !validOrigin(origin) {
		return result, errors.New("invalid preservation origin")
	}
	if protectID != "" && !ValidID(protectID) {
		return result, ErrInvalidID
	}
	if s.LimitBytes <= 0 || s.Backend == nil {
		return result, errors.New("preservation requires a positive budget and secret backend")
	}
	l, err := s.acquire()
	if err != nil {
		return result, err
	}
	defer l.Release()
	return s.save(ctx, origin, data, protectID, false)
}

func (s Store) save(ctx context.Context, origin Origin, data []byte, protectID string, checkOnly bool) (SaveResult, error) {
	var result SaveResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if s.LimitBytes <= 0 || s.Backend == nil {
		return result, errors.New("preservation requires a positive budget and secret backend")
	}
	records, err := s.inventory()
	if err != nil {
		return result, err
	}
	var duplicate *Record
	var total int64
	protectedFound := protectID == ""
	for _, r := range records {
		if r.ID == protectID {
			if !sameHistory(r.Origin, origin) {
				return result, ErrProtected
			}
			protectedFound = true
		}
		payload, err := s.payload(ctx, r)
		if err != nil {
			return result, err
		}
		if r.Size > math.MaxInt64-total {
			return result, ErrIncomplete
		}
		total += r.Size
		if r.Origin == origin && bytes.Equal(data, payload) {
			copy := r
			duplicate = &copy
		}
	}
	if !protectedFound {
		return result, ErrNotFound
	}
	if duplicate != nil {
		count := 0
		var victims []Record
		for _, r := range records {
			if sameHistory(r.Origin, origin) {
				count++
				if count > 3 {
					if r.ID == protectID {
						return result, ErrProtected
					}
					victims = append(victims, r)
				}
			}
		}
		result = SaveResult{Record: *duplicate, Duplicate: true}
		// An interrupted prune can leave an older duplicate outside the retained
		// three. Require explicit repair rather than return an ID about to be deleted.
		oldDuplicate := false
		for _, r := range victims {
			if r.ID == duplicate.ID {
				oldDuplicate = true
			}
		}
		if !oldDuplicate {
			if checkOnly {
				return result, nil
			}
			for _, r := range victims {
				if err := s.remove(ctx, r); err != nil {
					return result, err
				}
				result.Removed = append(result.Removed, r.ID)
			}
			return result, nil
		}
		return result, ErrIncomplete
	}
	if total > s.LimitBytes || int64(len(data)) > s.LimitBytes-total {
		return result, ErrQuota
	}
	var victims []Record
	count := 1 // the new record is newer than all current records
	for _, r := range records {
		if sameHistory(r.Origin, origin) {
			count++
			if count > 3 {
				if r.ID == protectID {
					return result, ErrProtected
				}
				victims = append(victims, r)
			}
		}
	}
	if checkOnly {
		return result, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, err
	}
	now := time.Now().UTC()
	if len(records) > 0 && !now.After(records[0].CreatedAt) {
		now = records[0].CreatedAt.Add(time.Nanosecond)
	}
	r := Record{SchemaVersion: 1, ID: hex.EncodeToString(random[:]), CreatedAt: now, Origin: origin, Size: int64(len(data)), Digest: digest(data), State: constants.PreservationStatePending}
	// Guard the astronomically unlikely collision rather than overwrite evidence.
	if _, err := os.Lstat(filepath.Join(s.Dir, r.ID+".json")); err == nil {
		return result, errors.New("preservation id collision")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if err := s.publish(r); err != nil {
		return result, err
	}
	result.Record = r
	if err := s.Backend.Set(ctx, secretRef(r.ID), data); err != nil {
		return result, err
	}
	stored, found, err := s.Backend.Get(ctx, secretRef(r.ID))
	if err != nil {
		return result, err
	}
	if !found || !bytes.Equal(stored, data) {
		return result, fmt.Errorf("%w: saved payload verification failed", ErrIncomplete)
	}
	r.State = constants.PreservationStateReady
	if err := s.publish(r); err != nil {
		return result, err
	}
	result.Record = r
	for _, victim := range victims {
		if err := s.remove(ctx, victim); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, victim.ID)
	}
	return result, nil
}

// remove records the deletion before touching the backend. Failed deletion keeps
// the reservation charged; removing the metadata is the last durable step.
func (s Store) remove(ctx context.Context, r Record) error {
	r.State = constants.PreservationStateDeleting
	if err := s.publish(r); err != nil {
		return err
	}
	if err := s.Backend.Delete(ctx, secretRef(r.ID)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.Dir, r.ID+".json")); err != nil {
		return err
	}
	return patch.SyncDir(s.Dir)
}

// Remove is the storage operation after the command has obtained explicit user
// confirmation. Pending records are removable even if no payload was published.
func (s Store) Remove(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if s.Backend == nil {
		return errors.New("preservation secret backend is required")
	}
	l, err := s.acquire()
	if err != nil {
		return err
	}
	defer l.Release()
	records, err := s.inventory()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.ID == id {
			return s.remove(ctx, r)
		}
	}
	return ErrNotFound
}

// RestoreSession is valid only during WithRestore's callback. SaveCurrent uses
// the selected record's origin and protects its ID from history pruning.
type RestoreSession struct {
	Record  Record
	Payload []byte
	store   Store
	ctx     context.Context
	origin  Origin
	id      string
	active  bool
}

func (r *RestoreSession) SaveCurrent(data []byte) (SaveResult, error) {
	if !r.active {
		return SaveResult{}, errors.New("preservation restore session is closed")
	}
	return r.store.save(r.ctx, r.origin, data, r.id, false)
}

// CheckCurrent runs the same quota, integrity and protected-retention admission
// as SaveCurrent without publishing or removing records. The session lock keeps
// its preview coherent with the selected record.
func (r *RestoreSession) CheckCurrent(data []byte) error {
	if !r.active {
		return errors.New("preservation restore session is closed")
	}
	_, err := r.store.save(r.ctx, r.origin, data, r.id, true)
	return err
}

// WithRestore keeps the inventory lock across selection, displaced-copy
// preservation and the caller's live write. The caller validates the binding
// under its already-held pin lock and never starts an interactive login here.
func (s Store) WithRestore(ctx context.Context, id string, apply func(*RestoreSession) error) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l, err := s.acquire()
	if err != nil {
		return err
	}
	defer l.Release()
	record, payload, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	session := &RestoreSession{Record: record, Payload: payload, store: s, ctx: ctx, origin: record.Origin, id: id, active: true}
	defer func() { session.active = false }()
	return apply(session)
}
