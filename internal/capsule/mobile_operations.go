package capsule

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var ErrMobileConflict = errors.New("idempotency key already has a different specification")

// MobilePrepare replaces path-based readers with held immutable references.
type MobilePrepare func(Record, StreamSource) (Record, StreamSource, func(), error)
type MobileResult struct {
	Status int
	Body   []byte
}
type mobileOperation struct {
	Version int          `json:"version"`
	Owner   string       `json:"owner"`
	Spec    string       `json:"spec"`
	ID      string       `json:"id"`
	Base    string       `json:"base"`
	Success int          `json:"success"`
	State   string       `json:"state"`
	Result  MobileResult `json:"result"`
}

// MobileMint durably reserves a capsule identity before invoking the existing
// mint flow. An interrupted unpublished operation is terminal: retrying cannot
// silently select newer source bytes. Published metadata recovers a lost reply.
// The caller must hold its authenticated owner lifecycle lease.
func (s *Store) MobileMint(v *vault.Vault, owner, key string, spec []byte, base string, success int, prepare MobilePrepare, mint func(*Store) MobileResult) (MobileResult, error) {
	return s.mobileOperation(v, owner, key, spec, base, success, prepare, mint)
}

// MobileLookup uses the same Idempotency-Key and never consumes a guest grab.
func (s *Store) MobileLookup(v *vault.Vault, owner, key string) (MobileResult, error) {
	return s.mobileOperation(v, owner, key, nil, "", 0, nil, nil)
}

func (s *Store) mobileOperation(v *vault.Vault, owner, key string, spec []byte, base string, success int, prepare MobilePrepare, mint func(*Store) MobileResult) (MobileResult, error) {
	identity, err := json.Marshal([]string{owner, key})
	if err != nil {
		return MobileResult{}, err
	}
	defer clear(identity)
	digest, err := v.Fingerprint("mobile-grab-key-v1", identity)
	if err != nil {
		return MobileResult{}, err
	}
	dir := filepath.Join(filepath.Dir(v.Path()), ".mobile-grabs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return MobileResult{}, err
	}
	name := filepath.Join(dir, hex.EncodeToString(digest))
	lock, err := os.OpenFile(name+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return MobileResult{}, err
	}
	defer lock.Close()
	mode := syscall.LOCK_EX
	if mint == nil {
		mode |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(lock.Fd()), mode); err != nil {
		if mint == nil && errors.Is(err, syscall.EWOULDBLOCK) {
			op, readErr := readMobileOperation(v, name+".enc")
			if readErr != nil {
				return MobileResult{}, readErr
			}
			if op.Version != 1 || op.Owner != owner || !validGalleryToken(op.ID) {
				return MobileResult{}, ErrStorage
			}
			body, _ := json.Marshal(map[string]string{"operation_id": op.ID, "state": "minting"})
			return MobileResult{Status: http.StatusAccepted, Body: body}, nil
		}
		return MobileResult{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var specID string
	if spec != nil {
		hash, err := v.Fingerprint("mobile-grab-spec-v1", spec)
		if err != nil {
			return MobileResult{}, err
		}
		specID = hex.EncodeToString(hash)
	}
	op, err := readMobileOperation(v, name+".enc")
	if err == nil {
		if op.Version != 1 || op.Owner != owner || !validGalleryToken(op.ID) {
			return MobileResult{}, ErrStorage
		}
		if spec != nil && op.Spec != specID {
			return MobileResult{}, ErrMobileConflict
		}
		if op.State == "done" {
			return op.Result, nil
		}
		rec, readErr := s.mobileRecord(op.ID)
		if readErr == nil {
			if rec.Owner != owner {
				return MobileResult{}, ErrStorage
			}
			op.Result = mobileReady(rec, op.Base, op.Success)
			op.State = "done"
			if err := writeMobileOperation(v, name+".enc", op); err != nil {
				return MobileResult{}, err
			}
			return op.Result, nil
		}
		if !os.IsNotExist(readErr) {
			return MobileResult{}, ErrStorage
		}
		body, _ := json.Marshal(map[string]string{"operation_id": op.ID, "state": "interrupted", "error": "mint interrupted before publication; create a new operation to select sources again"})
		s.mu.Lock()
		cleanupErr := s.prepareMobileDirectory(op.ID)
		s.mu.Unlock()
		if cleanupErr != nil {
			return MobileResult{}, cleanupErr
		}
		op.Result = MobileResult{Status: http.StatusConflict, Body: body}
		op.State = "done"
		if err := writeMobileOperation(v, name+".enc", op); err != nil {
			return MobileResult{}, err
		}
		return op.Result, nil
	}
	if !os.IsNotExist(err) {
		return MobileResult{}, err
	}
	if mint == nil {
		return MobileResult{Status: 404, Body: []byte(`{"error":"operation not found"}`)}, nil
	}
	raw, err := cryptox.Random(16)
	if err != nil {
		return MobileResult{}, err
	}
	op = mobileOperation{Version: 1, Owner: owner, Spec: specID, ID: encodeToken(raw), Base: base, Success: success, State: "minting"}
	if err := writeMobileOperation(v, name+".enc", op); err != nil {
		return MobileResult{}, err
	}
	scoped := &Store{root: s.root, mobileOrigin: s, mobileID: op.ID, mobilePrepare: prepare}
	op.Result = mint(scoped)
	if op.Result.Status >= 200 && op.Result.Status < 300 {
		rec, err := s.mobileRecord(op.ID)
		if err != nil || rec.Owner != owner {
			return MobileResult{}, ErrStorage
		}
		if err := syncMobileDir(filepath.Join(s.root, op.ID)); err != nil {
			return MobileResult{}, err
		}
		if err := syncMobileDir(s.root); err != nil {
			return MobileResult{}, err
		}
		if err := syncMobileDir(filepath.Dir(s.root)); err != nil {
			return MobileResult{}, err
		}
		op.Result = mobileReady(rec, base, success)
	}
	op.State = "done"
	if err := writeMobileOperation(v, name+".enc", op); err != nil {
		return MobileResult{}, err
	}
	return op.Result, nil
}

func mobileReady(rec Record, base string, status int) MobileResult {
	view := rec.View()
	view["url"] = strings.TrimRight(base, "/") + "/g/" + rec.ID
	view["operation_id"] = rec.ID
	view["state"] = "ready"
	body, _ := json.Marshal(view)
	return MobileResult{Status: status, Body: body}
}

func (s *Store) mobileRecord(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readMeta(filepath.Join(s.root, id))
}

// Called under the normal mint mutex; never remove a published capsule.
func (s *Store) prepareMobileDirectory(id string) error {
	if !validGalleryToken(id) {
		return ErrStorage
	}
	dir := filepath.Join(s.root, id)
	if _, err := readMeta(dir); !os.IsNotExist(err) {
		return ErrStorage
	}
	return os.RemoveAll(dir)
}
