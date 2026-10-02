package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	data := flag.String("data", "/data", "")
	helper := flag.String("helper", "/tmp/weazl-restic-reader", "")
	workers := flag.Int("workers", 8, "")
	count := flag.Int("count", 1000, "")
	flag.Parse()
	var creds struct{ Username, Password string }
	must(json.NewDecoder(os.Stdin).Decode(&creds))
	raw, err := os.ReadFile(filepath.Join(*data, "users.json"))
	must(err)
	var users struct {
		Users []struct{ ID, Username string }
	}
	must(json.Unmarshal(raw, &users))
	clear(raw)
	id := ""
	for _, u := range users.Users {
		if u.Username == creds.Username {
			id = u.ID
		}
	}
	if id == "" {
		panic("owner not found")
	}
	root := filepath.Join(*data, "users", id)
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	must(v.Unlock([]byte(creds.Password)))
	creds.Password = ""
	defer v.Lock()
	c := catalog.New(filepath.Join(root, "catalog.enc"), v)
	must(c.LoadReadOnly())
	files := []catalog.File{}
	var total int
	for _, f := range c.List() {
		if !f.Folder && strings.HasPrefix(f.Path, "Photos/") && strings.HasSuffix(strings.ToLower(f.Path), ".json") && f.Size > 0 && f.Size <= 4<<20 {
			files = append(files, f)
			total++
		}
	}
	// Sample across the collection without disclosing paths.
	sort.Slice(files, func(i, j int) bool { return files[i].Hash < files[j].Hash })
	if len(files) > *count {
		stride := float64(len(files)) / float64(*count)
		sample := make([]catalog.File, *count)
		for i := range sample {
			sample[i] = files[int(float64(i)*stride)]
		}
		files = sample
	}
	pass, drive, err := v.Secrets()
	must(err)
	clear(drive)
	start := time.Now()
	r, err := restic.StartReader(context.Background(), *helper, restic.Repo{Location: filepath.Join(root, "library"), Password: pass}, *workers, 2<<30)
	clear(pass)
	must(err)
	opened := time.Since(start).Seconds()
	fmt.Printf("{\"index_ready_seconds\":%.3f,\"sample\":%d,\"total_sidecars\":%d,\"workers\":%d}\n", opened, len(files), total, *workers)
	var ok, failed, parsed, bytes atomic.Int64
	var wg sync.WaitGroup
	jobs := make(chan catalog.File)
	start = time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				snap, obj := f.Snap, f.Object
				if f.Reference != nil {
					snap, obj = f.Reference.Snapshot, f.Reference.Object
				}
				if obj == "" {
					obj = f.Hash
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				out, e := r.Read(ctx, snap, obj, int(f.Size))
				cancel()
				hash := sha256.Sum256(out.Body)
				if e != nil || uint64(f.Size) != out.Size || hex.EncodeToString(hash[:]) != f.Hash {
					failed.Add(1)
				} else {
					ok.Add(1)
					bytes.Add(int64(len(out.Body)))
					if _, e := photos.ParseTakeoutSidecar(out.Body); e == nil {
						parsed.Add(1)
					}
				}
				clear(out.Body)
			}
		}()
	}
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	r.Close()
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage)
	fmt.Printf("{\"read_seconds\":%.3f,\"verified\":%d,\"failed\":%d,\"capture_dates\":%d,\"bytes\":%d,\"files_per_second\":%.2f,\"child_max_rss_kib\":%d}\n", elapsed, ok.Load(), failed.Load(), parsed.Load(), bytes.Load(), float64(len(files))/elapsed, usage.Maxrss)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
