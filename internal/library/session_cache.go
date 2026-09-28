package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func (l *Library) ensure(ctx context.Context) error {
	unlocked, session := l.vault.State()
	if !unlocked {
		l.clearSessionCache()
		return vault.ErrLocked
	}
	if err := l.backend.Ensure(ctx); err != nil {
		return err
	}
	return l.loadCatalogSession(session)
}

func (l *Library) loadCatalogSession(session uint64) error {
	if l.catalogLoaded && l.catalogSession != session {
		l.clearSessionCache()
	}
	if l.catalogLoaded && !l.catalog.DiskUnchanged() {
		l.clearSessionCache()
	}
	if l.catalogLoaded {
		return nil
	}
	if err := l.catalog.Load(); err != nil {
		return err
	}
	l.catalogSession, l.catalogLoaded = session, true
	return nil
}

func (l *Library) clearSessionCache() {
	l.catalog.Clear()
	l.catalogLoaded = false
	l.catalogSession = 0
	l.storageSummaryReady = false
	l.photoMu.Lock()
	l.photoRows, l.photoMediaRows = nil, nil
	l.photoByID, l.photoByPath, l.photoMediaByPath = nil, nil, nil
	l.photoReady = false
	l.photoMu.Unlock()
}

// ForgetVaultSession clears decrypted metadata after an explicit vault lock.
func (l *Library) ForgetVaultSession() {
	l.catalog.Clear()
	l.photoMu.Lock()
	l.photoRows, l.photoMediaRows = nil, nil
	l.photoByID, l.photoByPath, l.photoMediaByPath = nil, nil, nil
	l.photoReady = false
	l.photoMu.Unlock()
}
