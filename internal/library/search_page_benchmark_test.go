package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func BenchmarkSearchPage97kEntries(b *testing.B) {
	root := b.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("benchmark-pass"), []byte("benchmark-pass")); err != nil {
		b.Fatal(err)
	}
	rows := make([]catalog.File, 97000)
	for i := range rows {
		rows[i] = catalog.File{Path: fmt.Sprintf("Photos/%03d/family-%06d.jpg", i%400, i), Size: int64(i), Mtime: time.Unix(int64(i), 0), Hash: fmt.Sprintf("%x", i), Present: true}
	}
	plain, err := json.Marshal(map[string]any{"files": rows})
	if err != nil {
		b.Fatal(err)
	}
	sealed, err := v.Wrap(plain)
	if err != nil {
		b.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.enc")
	if err := os.WriteFile(catalogPath, sealed, 0o600); err != nil {
		b.Fatal(err)
	}
	l := New(filepath.Join(root, "repo"), catalogPath, v)
	l.backend = &isolatedLegacy{root: filepath.Join(root, "repo")}
	if err := l.Ensure(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.SearchPage(context.Background(), SearchOptions{Query: "family-0001", Type: "image", Sort: "name", Limit: 100}); err != nil {
			b.Fatal(err)
		}
	}
}
