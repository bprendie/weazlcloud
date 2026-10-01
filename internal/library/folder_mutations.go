package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) Mkdir(ctx context.Context, name string) error {
	name, err := cleanPath(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.catalog.Mkdir(name); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "mkdir", Paths: []string{name}})
	return nil
}

func (l *Library) Rename(ctx context.Context, oldName, newName string) error {
	oldName, err := cleanPath(oldName)
	if err != nil {
		return err
	}
	newName, err = cleanPath(newName)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	if err := l.catalog.Rename(oldName, newName); err != nil {
		return err
	}
	l.publishChange(Change{Kind: "rename", Paths: []string{oldName, newName}})
	return nil
}

func (l *Library) Copy(ctx context.Context, oldName, newName string) error {
	oldName, err := cleanPath(oldName)
	if err != nil {
		return err
	}
	newName, err = cleanPath(newName)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	err = l.catalog.CopyWith(oldName, newName, func(source, destination *catalog.File) error {
		if source.Reference == nil || source.Reference.Backend != catalog.SharedBackend {
			return nil
		}
		if l.sharedStore == nil || l.ownerID == "" {
			return catalog.ErrUnknownReference
		}
		ref, e := l.sharedStore.Grant(ctx, l.ownerID, l.vault, toSharedReference(*source.Reference), destination.EntryID, destination.Revision)
		if e != nil {
			return e
		}
		destination.Reference = &catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(ref.Version), Object: ref.ObjectID, Operation: ref.Operation, OwnerEntryID: ref.EntryID, OwnerRevision: ref.Revision}
		destination.Object = ref.ObjectID
		return nil
	})
	if err != nil {
		return err
	}
	l.publishChange(Change{Kind: "copy", Paths: []string{oldName, newName}})
	return nil
}
