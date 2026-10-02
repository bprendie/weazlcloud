package library

import (
	"container/list"
	"context"
	"sync"
)

type previewRAMKey struct {
	owner *Library
	key   string
}
type previewRAMEntry struct {
	key     previewRAMKey
	env     thumbnailEnvelope
	session context.Context
	epoch   uint64
	release func()
	bytes   int64
}
type previewRAMSession struct {
	session context.Context
	stop    func() bool
}
type previewRAMCache struct {
	mu           sync.Mutex
	entries      map[previewRAMKey]*list.Element
	owners       map[*Library]previewRAMSession
	recent       list.List
	bytes, limit int64
}

var previewRAM = newPreviewRAMCache()

func newPreviewRAMCache() *previewRAMCache {
	r := discoverPreviewResources()
	var limit int64
	if r.CPUs > 0 && r.MemoryBytes > 0 {
		limit = min(int64(2<<30), r.MemoryBytes/64, previewPolicy.MemoryBytes/4)
	}
	return &previewRAMCache{entries: map[previewRAMKey]*list.Element{}, owners: map[*Library]previewRAMSession{}, limit: limit}
}
func (c *previewRAMCache) remove(e *list.Element) {
	v := e.Value.(previewRAMEntry)
	delete(c.entries, v.key)
	c.recent.Remove(e)
	c.bytes -= v.bytes
	v.release()
}
func (c *previewRAMCache) get(l *Library, key string) (thumbnailEnvelope, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[previewRAMKey{l, key}]
	if e == nil {
		return thumbnailEnvelope{}, false
	}
	v := e.Value.(previewRAMEntry)
	if v.session.Err() != nil || v.session != l.vault.Session() || v.epoch != thumbnailCacheEpoch.Load() {
		c.remove(e)
		return thumbnailEnvelope{}, false
	}
	c.recent.MoveToBack(e)
	return v.env, true
}
func (c *previewRAMCache) put(l *Library, key string, env thumbnailEnvelope) {
	size := int64(len(env.Body) + len(env.ThumbHash) + 256)
	if size > c.limit || size <= 256 || l.previewLifetime.Err() != nil {
		return
	}
	session := l.vault.Session()
	if session.Err() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	k := previewRAMKey{l, key}
	if e := c.entries[k]; e != nil {
		c.remove(e)
	}
	for c.bytes+size > c.limit || len(c.entries) >= 100_000 {
		c.remove(c.recent.Front())
	}
	release, ok := previewMemory.tryAcquire(size)
	if !ok {
		return
	}
	if session.Err() != nil {
		release()
		return
	}
	if old, ok := c.owners[l]; !ok || old.session != session {
		if ok {
			old.stop()
		}
		stopSession := context.AfterFunc(session, func() { c.clearOwnerSession(l, session) })
		stopLifetime := context.AfterFunc(l.previewLifetime, func() { c.clearOwnerSession(l, session) })
		c.owners[l] = previewRAMSession{session, func() bool { a := stopSession(); b := stopLifetime(); return a || b }}
	}
	// Cached bodies are immutable; eviction drops references without overwriting
	// slices already being delivered to an authorized response.
	copyEnv := env
	copyEnv.Body = append([]byte(nil), env.Body...)
	copyEnv.ThumbHash = append([]byte(nil), env.ThumbHash...)
	c.entries[k] = c.recent.PushBack(previewRAMEntry{k, copyEnv, session, thumbnailCacheEpoch.Load(), release, size})
	c.bytes += size
}
func (c *previewRAMCache) clearOwnerSession(l *Library, session context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if key.owner == l && (session == nil || e.Value.(previewRAMEntry).session == session) {
			c.remove(e)
		}
	}
	if old, ok := c.owners[l]; ok && (session == nil || old.session == session) {
		old.stop()
		delete(c.owners, l)
	}
}

// Reclaim compressed cache bytes before a decoder waits for shared admission.
func (c *previewRAMCache) reclaim(b *previewMemoryBudget, need int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.recent.Len() > 0 {
		b.mu.Lock()
		fits := need <= b.limit-b.used
		b.mu.Unlock()
		if fits {
			return
		}
		c.remove(c.recent.Front())
	}
}
