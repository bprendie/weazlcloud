package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a small, bounded fixed-window limiter for authentication-like
// operations. Entries expire and the map is capped so attacker-controlled keys
// cannot grow without bound.
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	maxKeys int
	items   map[string]entry
}

type entry struct {
	started time.Time
	count   int
}

func New(window time.Duration, limit, maxKeys int) *Limiter {
	return &Limiter{window: window, limit: limit, maxKeys: maxKeys, items: make(map[string]entry)}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked(now)
	item, ok := l.items[key]
	if !ok || now.Sub(item.started) >= l.window {
		if len(l.items) >= l.maxKeys {
			return false
		}
		l.items[key] = entry{started: now, count: 1}
		return true
	}
	if item.count >= l.limit {
		return false
	}
	item.count++
	l.items[key] = item
	return true
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.items, key)
	l.mu.Unlock()
}

func (l *Limiter) expireLocked(now time.Time) {
	for key, item := range l.items {
		if now.Sub(item.started) >= l.window {
			delete(l.items, key)
		}
	}
}
