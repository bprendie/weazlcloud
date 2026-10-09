package library

import (
	"context"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
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
	go func() { _, _ = l.PhotoMetadataStatus() }()
	if !l.photoAutoDisabled {
		pending := []catalog.File{}
		for _, file := range l.catalog.List() {
			if file.PhotoProcessingPending {
				if strings.HasSuffix(strings.ToLower(file.Path), ".json") {
					l.noteMetadataSidecar(file.Path)
				} else {
					pending = append(pending, file)
				}
			}
		}
		if len(pending) != 0 {
			go l.retryPhotoIngest(pending)
		}
	}
	return nil
}

func (l *Library) clearSessionCache() {
	l.catalog.Clear()
	l.albumMetadata = nil
	l.catalogLoaded = false
	l.catalogSession = 0
	l.storageSummaryReady = false
	l.photoMu.Lock()
	l.photoRows, l.photoMediaRows = nil, nil
	l.photoContentIndex = nil
	l.photoByID, l.photoByPath, l.photoMediaByPath = nil, nil, nil
	l.photoMediaByID, l.photoHiddenFolders = nil, nil
	l.photoDateSummary = PhotoDateSummary{}
	l.photoQueryCache, l.photoQueryOrder = nil, nil
	l.photoNavigationCache = nil
	l.photoDateSummaryEpoch, l.photoArchivedCount = 0, 0
	l.photoReady = false
	l.photoMu.Unlock()
	l.clearPhotoJobMemory()
	l.clearMetadataMemory()
}

// ForgetVaultSession clears decrypted metadata after an explicit vault lock.
func (l *Library) ForgetVaultSession() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearSessionCache()
}
