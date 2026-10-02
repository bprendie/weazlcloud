package catalog

import (
	"errors"
	"testing"
)

func TestBackupCASNeverOverwritesOrResurrects(t *testing.T) {
	c := testCatalog(t)
	if err := c.Mkdir("Backups"); err != nil {
		t.Fatal(err)
	}
	root, _ := c.Get("Backups")
	id, _ := NewBackupIdentity()
	f := File{EntryID: id, Revision: 1, Path: "Backups/empty", Present: true}
	plan := BackupMutation{Guards: []File{root}, Absent: []string{f.Path}, Files: []File{f}}
	if err := c.PublishBackup(plan); err != nil {
		t.Fatal(err)
	}
	if !c.BackupPublished(plan.Files) {
		t.Fatal("publication lacks proof")
	}
	if err := c.Put(File{Path: f.Path, Present: true, Hash: "user edit"}); err != nil {
		t.Fatal(err)
	}
	replacement := f
	replacement.Revision = 2
	if err := c.PublishBackup(BackupMutation{Guards: []File{root, f}, Files: []File{replacement}}); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("user edit overwritten: %v", err)
	}
	if !c.BackupPublished(plan.Files) {
		t.Fatal("lost response after user edit cannot recover")
	}
	current, _ := c.Get(f.Path)
	if err := c.Delete(f.Path); err != nil {
		t.Fatal(err)
	}
	replacement = current
	replacement.Revision++
	if err := c.PublishBackup(BackupMutation{Guards: []File{root, current}, Files: []File{replacement}}); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("trash resurrected: %v", err)
	}
	if err := c.Rename("Backups", "Moved"); err != nil {
		t.Fatal(err)
	}
	otherID, _ := NewBackupIdentity()
	other := File{EntryID: otherID, Revision: 1, Path: "Backups/new", Present: true}
	if err := c.PublishBackup(BackupMutation{Guards: []File{root}, Absent: []string{other.Path}, Files: []File{other}}); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("moved parent accepted: %v", err)
	}
}

func TestBackupCollisionAndAtomicFolderMove(t *testing.T) {
	c := testCatalog(t)
	for _, p := range []string{"Backups", "Backups/tree", "Backups/tree/empty"} {
		if err := c.Mkdir(p); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := c.Get("Backups")
	folder, _ := c.Get("Backups/tree")
	child, _ := c.Get("Backups/tree/empty")
	moved := folder
	moved.Path = "Backups/renamed"
	moved.Revision++
	movedChild := child
	movedChild.Path = "Backups/renamed/empty"
	movedChild.Revision++
	plan := BackupMutation{Guards: []File{root, folder, child}, Absent: []string{moved.Path, movedChild.Path}, Files: []File{moved, movedChild}}
	if err := c.PublishBackup(plan); err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	if !c.BackupPublished(plan.Files) {
		t.Fatal("restart lost publication proof")
	}
	if _, ok := c.Get(child.Path); ok {
		t.Fatal("old descendant remains")
	}
	collision := movedChild
	collision.EntryID, _ = NewBackupIdentity()
	collision.Revision = 1
	if err := c.PublishBackup(BackupMutation{Absent: []string{collision.Path}, Files: []File{collision}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("collision accepted: %v", err)
	}
}
