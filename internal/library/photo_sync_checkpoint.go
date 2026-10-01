package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func syncDeviceKey(device string, hidden bool) string {
	if hidden {
		return device + ":hidden"
	}
	return device
}

func (l *Library) AcknowledgePhotoSync(ctx context.Context, device, raw string, hidden bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return err
	}
	cursor, err := l.decodeSyncCursor(raw)
	if err != nil || cursor.Mode != "delta" || cursor.Hidden != hidden {
		return catalog.ErrSyncExpired
	}
	return l.catalog.AcknowledgeDevice(syncDeviceKey(device, hidden), cursor.Position)
}

func (l *Library) PhotoDeviceCheckpoint(ctx context.Context, device string, hidden bool) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return "", vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return "", err
	}
	position, err := l.catalog.DeviceCheckpoint(syncDeviceKey(device, hidden))
	if err != nil {
		return "", err
	}
	return l.encodeSyncCursor(photoSyncCursor{Position: position, Mode: "delta", Hidden: hidden})
}
