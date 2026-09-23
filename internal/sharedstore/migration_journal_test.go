package sharedstore

import (
	"context"
	"strings"
	"testing"
)

func TestMigrationJournalGroupsSafeFailureCategories(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RecordMigration(ctx, "owner", "entry-a", 1, "blocked", 0, "workspace"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordMigration(ctx, "owner", "entry-b", 1, "blocked", 64, "copy-or-verify"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.MigrationStatus(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("failure categories were not separated: rows=%+v err=%v", rows, err)
	}
	categories := map[string]int64{}
	for _, row := range rows {
		categories[row.ErrorCategory] = row.Items
	}
	if categories["workspace"] != 1 || categories["copy-or-verify"] != 1 {
		t.Fatalf("safe categories missing from status: %+v", rows)
	}
	if err = s.RecordMigration(ctx, "owner", "entry-c", 1, "blocked", 0, "/private/user/file"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("journal accepted a private path as an error category: %v", err)
	}
}
