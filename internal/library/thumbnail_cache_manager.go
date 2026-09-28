package library

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const thumbnailNodeDefaultBytes = 16 << 30

var (
	thumbnailOwnerID      = regexp.MustCompile(`^[a-f0-9]{32}$`)
	thumbnailCacheMu      sync.Mutex
	thumbnailCaches       = map[string]*thumbnailNodeCache{}
	thumbnailNodeMaxBytes = previewLimit("WEAZLCLOUD_PREVIEW_NODE_BYTES", thumbnailNodeDefaultBytes)
	thumbnailCacheEpoch   atomic.Uint64
)

type trackedThumbnail struct {
	path, owner string
	size        int64
	when        time.Time
}

type thumbnailNodeCache struct {
	files      map[string]trackedThumbnail
	ownerBytes map[string]int64
	ownerFiles map[string]int
	total      int64
}

func (l *Library) accountThumbnail(dir, name string, size int64) {
	owner, root := thumbnailOwnerAndRoot(dir)
	thumbnailCacheMu.Lock()
	defer thumbnailCacheMu.Unlock()
	cache := thumbnailCaches[root]
	if cache == nil {
		cache = scanThumbnailNode(root)
		thumbnailCaches[root] = cache
	}
	full := filepath.Join(dir, name)
	info, err := os.Stat(full)
	if err != nil {
		return
	}
	next := trackedThumbnail{path: full, owner: owner, size: size, when: info.ModTime()}
	if old, ok := cache.files[full]; ok {
		cache.total -= old.size
		cache.ownerBytes[old.owner] -= old.size
		cache.ownerFiles[old.owner]--
	}
	cache.files[full] = next
	cache.total += size
	cache.ownerBytes[owner] += size
	cache.ownerFiles[owner]++
	cache.evict(owner)
}

func thumbnailOwnerAndRoot(dir string) (string, string) {
	owner := filepath.Dir(dir)
	if thumbnailOwnerID.MatchString(filepath.Base(owner)) && filepath.Base(filepath.Dir(owner)) == "users" {
		return owner, filepath.Dir(filepath.Dir(owner))
	}
	return owner, owner
}

func scanThumbnailNode(root string) *thumbnailNodeCache {
	cache := &thumbnailNodeCache{files: map[string]trackedThumbnail{}, ownerBytes: map[string]int64{}, ownerFiles: map[string]int{}}
	add := func(owner, dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".enc" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			cache.files[path] = trackedThumbnail{path: path, owner: owner, size: info.Size(), when: info.ModTime()}
			cache.total += info.Size()
			cache.ownerBytes[owner] += info.Size()
			cache.ownerFiles[owner]++
		}
	}
	if thumbnailOwnerID.MatchString(filepath.Base(root)) {
		add(root, filepath.Join(root, ".weazl-previews"))
		return cache
	}
	add(root, filepath.Join(root, ".weazl-previews"))
	users := filepath.Join(root, "users")
	owners, err := os.ReadDir(users)
	if err == nil {
		for _, entry := range owners {
			if entry.IsDir() && thumbnailOwnerID.MatchString(entry.Name()) {
				owner := filepath.Join(users, entry.Name())
				add(owner, filepath.Join(owner, ".weazl-previews"))
			}
		}
	}
	return cache
}

func (c *thumbnailNodeCache) evict(writingOwner string) {
	for c.ownerBytes[writingOwner] > thumbnailMaxBytes || c.ownerFiles[writingOwner] > thumbnailMaxFiles || c.total > thumbnailNodeMaxBytes {
		var candidates []trackedThumbnail
		for _, file := range c.files {
			if c.ownerBytes[writingOwner] > thumbnailMaxBytes || c.ownerFiles[writingOwner] > thumbnailMaxFiles {
				if file.owner != writingOwner {
					continue
				}
			}
			candidates = append(candidates, file)
		}
		if len(candidates) == 0 {
			return
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].when.Before(candidates[j].when) })
		old := candidates[0]
		if err := os.Remove(old.path); err != nil && !os.IsNotExist(err) {
			return
		}
		delete(c.files, old.path)
		thumbnailCacheEpoch.Add(1)
		c.total -= old.size
		c.ownerBytes[old.owner] -= old.size
		c.ownerFiles[old.owner]--
	}
}

func forgetThumbnailNode(dir string) {
	_, root := thumbnailOwnerAndRoot(dir)
	thumbnailCacheMu.Lock()
	delete(thumbnailCaches, root)
	thumbnailCacheMu.Unlock()
}
