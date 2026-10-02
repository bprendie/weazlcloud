package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
)

func TestFutureBackupRecordVersionsPreserved(t *testing.T) {
	for _, kind := range []string{"source", "operation"} {
		t.Run(kind, func(t *testing.T) {
			m, res, u, src, dir := fixture(t)
			ctx := context.Background()
			spec := fileSpec(src, "one", "r1", "one", []byte("one"))
			v, err := m.CreateParts(ctx, res, u, "phone", spec)
			if err != nil {
				t.Fatal(err)
			}
			key := ""
			err = with(ctx, res, u, func(tx *library.BackupTransaction) error {
				if kind == "source" {
					var err error
					key, err = sourceKey(tx, "phone", src.ID)
					if err != nil {
						return err
					}
					source, err := loadSource(tx, key)
					if err != nil {
						return err
					}
					source.Version = 3
					return tx.Write(key, source)
				}
				key = v.ID
				op, err := loadOperation(tx, key, "phone")
				if err != nil {
					return err
				}
				op.Version = 3
				return tx.Write(key, op)
			})
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, ".weazl-backups", key+".enc")
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "source" {
				if _, err := m.UpdateSource(ctx, res, u, "phone", src.ID, "detached", src.Revision); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future source mutated: %v", err)
				}
				if _, err := m.Register(ctx, res, u, "phone", src); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future source re-registered: %v", err)
				}
				if _, err := m.FinalizeParts(ctx, res, u, v.ID, "phone", bytes.NewReader([]byte("one"))); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future source published: %v", err)
				}
			} else {
				if _, err := m.Status(ctx, res, u, v.ID, "phone"); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future receipt read: %v", err)
				}
				if _, err := m.CreateParts(ctx, res, u, "phone", spec); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future receipt replaced: %v", err)
				}
				if err := m.Cancel(ctx, res, u, v.ID, "phone"); !errors.Is(err, ErrUnsupportedVersion) {
					t.Fatalf("future receipt cancelled: %v", err)
				}
			}
			after, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("future record rewritten")
			}
			if _, err := res.Lib.Metadata(ctx, "Backups/one"); err == nil {
				t.Fatal("future record published bytes")
			}
		})
	}
}

func TestLegacyBackupVersionUpgradesOnNextWrite(t *testing.T) {
	m, res, u, src, _ := fixture(t)
	ctx := context.Background()
	spec := fileSpec(src, "one", "r1", "one", []byte("one"))
	v, err := m.CreateParts(ctx, res, u, "phone", spec)
	if err != nil {
		t.Fatal(err)
	}
	err = with(ctx, res, u, func(tx *library.BackupTransaction) error {
		key, err := sourceKey(tx, "phone", src.ID)
		if err != nil {
			return err
		}
		source, err := loadSource(tx, key)
		if err != nil {
			return err
		}
		source.Version = 0
		if err := tx.Write(key, source); err != nil {
			return err
		}
		op, err := loadOperation(tx, v.ID, "phone")
		if err != nil {
			return err
		}
		op.Version = 0
		return tx.Write(op.ID, op)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSource(ctx, res, u, "phone", src.ID, "paused", src.Revision); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(ctx, res, u, v.ID, "phone"); err != nil {
		t.Fatal(err)
	}
	err = with(ctx, res, u, func(tx *library.BackupTransaction) error {
		key, err := sourceKey(tx, "phone", src.ID)
		if err != nil {
			return err
		}
		var source sourceRecord
		if err := tx.Read(key, &source); err != nil {
			return err
		}
		var op operation
		if err := tx.Read(v.ID, &op); err != nil {
			return err
		}
		if source.Version != 1 || op.Version != 1 || op.Status != "cancelled" || source.Source.Status != "paused" || op.Spec.ItemID != spec.ItemID {
			t.Fatalf("legacy version upgrade lost state: %+v %+v", source, op)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
