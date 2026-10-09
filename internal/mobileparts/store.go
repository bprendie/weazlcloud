package mobileparts

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
)

type Manager struct {
	admissionMu    sync.Mutex
	mu             sync.Mutex
	scanMu         sync.Mutex
	scans          map[string]*directoryScan
	scanTick       uint64
	gates          map[string]*sessionGate
	checked        map[string]time.Time
	reservations   map[string]reservation
	active         map[string]*activeJob
	draining       map[string]int
	quota          *quota.Manager
	now            func() time.Time
	receivers      map[*receiver]bool
	receiveBlocked map[string]int
	receiveLimits  ReceiveLimits
}

func New(q *quota.Manager) *Manager {
	return &Manager{scans: map[string]*directoryScan{}, gates: map[string]*sessionGate{}, checked: map[string]time.Time{}, reservations: map[string]reservation{}, active: map[string]*activeJob{}, draining: map[string]int{}, quota: q, now: time.Now, receivers: map[*receiver]bool{}, receiveBlocked: map[string]int{}, receiveLimits: ReceiveLimits{Global: 32, PerOwner: 32, Pending: 512, PendingBytes: 32 << 30}}
}
func Root(res *filesvc.Resource) string {
	return filepath.Join(filepath.Dir(res.Lib.PhotoIngestDir()), ".weazl-mobile-parts")
}
func keyFor(res *filesvc.Resource, id string) string { return filepath.Join(Root(res), id) }

type sessionGate struct {
	sync.Mutex
	refs  int
	owner string
}

func (m *Manager) lock(res *filesvc.Resource, id string) func() {
	m.mu.Lock()
	key := keyFor(res, id)
	g := m.gates[key]
	if g == nil {
		g = &sessionGate{}
		m.gates[key] = g
	}
	g.refs++
	m.mu.Unlock()
	g.Lock()
	return func() { m.unlockGate(key, g) }
}
func (m *Manager) unlockGate(key string, g *sessionGate) {
	g.Unlock()
	m.mu.Lock()
	g.refs--
	if g.refs == 0 {
		delete(m.gates, key)
		if !m.hasReceiversLocked(key) {
			delete(m.checked, key)
		}
	}
	m.mu.Unlock()
}
func writeSealed(res *filesvc.Resource, name string, v any) error {
	p, e := json.Marshal(v)
	if e != nil {
		return e
	}
	defer clear(p)
	b, e := res.Vault.Wrap(p)
	if e != nil {
		return e
	}
	if e = cryptox.AtomicWrite(name, b, 0600); e != nil {
		return e
	}
	return syncDir(filepath.Dir(name))
}
func readSealed(res *filesvc.Resource, name string, v any) error {
	f, e := os.Open(name)
	if errors.Is(e, os.ErrNotExist) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 256<<10))
	if e != nil {
		return e
	}
	if len(b) == 256<<10 {
		return ErrCorrupt
	}
	p, e := res.Vault.Unwrap(b)
	if e != nil {
		return e
	}
	defer clear(p)
	if json.Unmarshal(p, v) != nil {
		return ErrCorrupt
	}
	return nil
}
func (m *Manager) save(res *filesvc.Resource, s *Session) error {
	s.UpdatedAt = m.now().UTC()
	if e := m.persist(res, s); e != nil {
		return e
	}
	return m.reserve(res, *s)
}
func (m *Manager) persist(res *filesvc.Resource, s *Session) error {
	if e := addSessionMarkers(res, *s); e != nil {
		return e
	}
	e := writeSealed(res, filepath.Join(keyFor(res, s.ID), "session.enc"), s)
	if e == nil {
		e = syncSessionMarkers(res, *s)
	}
	m.mu.Lock()
	if e == nil {
		m.checked[keyFor(res, s.ID)] = s.UpdatedAt
	} else {
		delete(m.checked, keyFor(res, s.ID))
	}
	m.mu.Unlock()
	return e
}
func (m *Manager) load(res *filesvc.Resource, id string) (Session, error) {
	if !validID(id) {
		return Session{}, ErrNotFound
	}
	var s Session
	if e := readSealed(res, filepath.Join(keyFor(res, id), "session.enc"), &s); e != nil {
		return s, e
	}
	if e := validateSession(s, id); e != nil {
		return s, e
	}
	if s.Status != "stored" && s.Status != "cancelled" && m.now().Sub(s.UpdatedAt) >= Lifetime {
		if !m.isActive(res, id) {
			m.release(res, id)
		}
		return s, ErrExpired
	}
	m.mu.Lock()
	checked := m.checked[keyFor(res, id)].Equal(s.UpdatedAt)
	m.mu.Unlock()
	if !checked && s.Status != "stored" && s.Status != "cancelled" {
		if e := m.recoverSession(res, &s); e != nil {
			return s, e
		}
	}
	if e := m.reserve(res, s); e != nil {
		return s, e
	}
	if s.Status == "stored" || s.Status == "cancelled" {
		if e := m.removePayload(res, id); e != nil {
			return s, e
		}
	}
	return s, nil
}

type partReceipt struct {
	Component string `json:"component"`
	Index     int64  `json:"index"`
	Size      int64  `json:"size"`
	Hash      string `json:"hash"`
}

func partName(c string, i int64) string { return c + "-" + strconv.FormatInt(i, 10) }
func (m *Manager) reconcile(res *filesvc.Resource, s *Session) error {
	entries, e := os.ReadDir(keyFor(res, s.ID))
	if e != nil {
		return e
	}
	for i := range s.Spec.Components {
		s.Spec.Components[i].ReceivedParts = 0
		s.Spec.Components[i].ReceivedBytes = 0
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".receipt.enc") {
			continue
		}
		var p partReceipt
		if e = readSealed(res, filepath.Join(keyFor(res, s.ID), entry.Name()), &p); e != nil {
			return e
		}
		found := false
		for i := range s.Spec.Components {
			c := &s.Spec.Components[i]
			if c.ID != p.Component {
				continue
			}
			n, e := partLength(*c, p.Index)
			if e != nil || n != p.Size || !validHash(p.Hash) || entry.Name() != partName(p.Component, p.Index)+".receipt.enc" {
				return ErrCorrupt
			}
			if _, e = os.Stat(filepath.Join(keyFor(res, s.ID), partName(p.Component, p.Index)+".wza")); e != nil {
				return ErrCorrupt
			}
			c.ReceivedParts++
			c.ReceivedBytes += n
			found = true
		}
		if !found {
			return ErrCorrupt
		}
	}
	if ready(*s) && s.Spec.CommitWhenComplete && s.Status == "uploading" {
		s.Status = "queued"
	}
	return nil
}
