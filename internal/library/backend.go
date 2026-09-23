package library

import (
	"context"
	"io"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type BatchReference struct {
	Snapshot string
}

// Backend owns immutable bytes. Callers must authorize through the private
// catalog before resolving a reference or acquiring a hold.
type Backend interface {
	Ensure(context.Context) error
	Put(context.Context, string, io.Reader) (catalog.Reference, error)
	PutBatch(context.Context, string) (BatchReference, error)
	Capture(catalog.File) (catalog.Reference, error)
	Read(context.Context, catalog.Reference, io.Writer) error
	ReadRange(context.Context, catalog.Reference, int64, int64, io.Writer) error
	Snapshots(context.Context) ([]string, error)
	Forget(context.Context, []string) ([]string, error)
	Hold(catalog.Reference) (func(), error)
	Drain(context.Context) error
}

func resticReference(snapshot, object, fallback string) catalog.Reference {
	if object == "" {
		object = fallback
	}
	return catalog.Reference{Backend: catalog.ResticBackend, Version: 1, Snapshot: snapshot, Object: object}
}

func fileReference(file catalog.File) (catalog.Reference, error) {
	if file.Reference != nil {
		return *file.Reference, nil
	}
	if file.Snap != "" {
		return resticReference(file.Snap, file.Object, file.Hash), nil
	}
	return catalog.Reference{}, catalog.ErrUnknownReference
}
