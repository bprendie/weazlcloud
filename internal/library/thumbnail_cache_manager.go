package library

import (
	"container/list"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const thumbnailNodeDefaultBytes = 16 << 30

var (
	thumbnailOwnerID      = regexp.MustCompile(`^[a-f0-9]{32}$`)
	thumbnailCacheMu      sync.Mutex
	thumbnailCaches       = map[string]*thumbnailNodeCache{}
	thumbnailNodeMaxBytes = previewLimit("WEAZLCLOUD_PREVIEW_NODE_BYTES", 0)
	thumbnailCacheEpoch   atomic.Uint64
)

type trackedThumbnail struct {
	path, owner string
	size        int64
	when        time.Time
	variant     int
	obsolete    bool
}
type thumbnailRecency struct {
	element *list.Element
	class   int
}
type thumbnailNodeCache struct {
	mu           sync.Mutex
	files        map[string]trackedThumbnail
	ownerBytes   map[string]int64
	ownerFiles   map[string]int
	total        int64
	recent       map[string]thumbnailRecency
	owners       map[string]*[3]list.List
	pinned       map[string]bool
	pending      int64
	pendingOwner map[string]int64
	pendingFiles map[string]int
	target       int64
	checked      time.Time
}

func thumbnailOwnerAndRoot(dir string) (string, string) {
	owner := filepath.Dir(dir)
	if thumbnailOwnerID.MatchString(filepath.Base(owner)) && filepath.Base(filepath.Dir(owner)) == "users" {
		return owner, filepath.Dir(filepath.Dir(owner))
	}
	return owner, owner
}
func thumbnailNode(root string) *thumbnailNodeCache {
	thumbnailCacheMu.Lock()
	defer thumbnailCacheMu.Unlock()
	cache := thumbnailCaches[root]
	if cache == nil {
		cache = scanThumbnailNode(root)
		thumbnailCaches[root] = cache
	}
	return cache
}
func (c *thumbnailNodeCache) init() {
	if c.recent != nil {
		return
	}
	c.recent = map[string]thumbnailRecency{}
	c.owners = map[string]*[3]list.List{}
	c.pinned = map[string]bool{}
	c.pendingOwner = map[string]int64{}
	c.pendingFiles = map[string]int{}
	var ordered []cacheFile
	for path, f := range c.files {
		ordered = append(ordered, cacheFile{name: path, when: f.when})
	}
	sortCacheFiles(ordered)
	for _, f := range ordered {
		c.touch(f.name)
	}
}
func (c *thumbnailNodeCache) touch(path string) {
	f, ok := c.files[path]
	if !ok {
		return
	}
	lists := c.owners[f.owner]
	if lists == nil {
		lists = &[3]list.List{}
		c.owners[f.owner] = lists
	}
	class := 1
	if f.variant == 320 {
		class = 2
	}
	if f.obsolete {
		class = 0
	} // Larger/legacy outputs yield before known grid previews.
	if old, ok := c.recent[path]; ok {
		if old.class == class {
			lists[class].MoveToBack(old.element)
			return
		}
		lists[old.class].Remove(old.element)
	}
	c.recent[path] = thumbnailRecency{lists[class].PushBack(path), class}
}
func (c *thumbnailNodeCache) remove(path string) error {
	f := c.files[path]
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(c.files, path)
	if item, ok := c.recent[path]; ok {
		c.owners[f.owner][item.class].Remove(item.element)
		delete(c.recent, path)
	}
	c.total -= f.size
	c.ownerBytes[f.owner] -= f.size
	c.ownerFiles[f.owner]--
	thumbnailCacheEpoch.Add(1)
	return nil
}
func (c *thumbnailNodeCache) victim(owner string) string {
	lists := c.owners[owner]
	if lists == nil {
		return ""
	}
	for i := range lists {
		for e := lists[i].Front(); e != nil; e = e.Next() {
			path := e.Value.(string)
			if !c.pinned[path] {
				return path
			}
		}
	}
	return ""
}
func (c *thumbnailNodeCache) evict(writingOwner string) {
	c.init()
	for (thumbnailMaxBytes > 0 && c.ownerBytes[writingOwner] > thumbnailMaxBytes) || c.ownerFiles[writingOwner] > thumbnailMaxFiles || (thumbnailNodeMaxBytes > 0 && c.total > thumbnailNodeMaxBytes) {
		owner := writingOwner
		if (thumbnailMaxBytes == 0 || c.ownerBytes[owner] <= thumbnailMaxBytes) && c.ownerFiles[owner] <= thumbnailMaxFiles {
			owner = c.largestOwner()
		}
		path := c.victim(owner)
		if path == "" || c.remove(path) != nil {
			return
		}
	}
}
func (c *thumbnailNodeCache) largestOwner() string {
	owner := ""
	var largest int64
	for candidate, size := range c.ownerBytes {
		if size > largest && c.victim(candidate) != "" {
			owner, largest = candidate, size
		}
	}
	return owner
}
func scanThumbnailNode(root string) *thumbnailNodeCache {
	c := &thumbnailNodeCache{files: map[string]trackedThumbnail{}, ownerBytes: map[string]int64{}, ownerFiles: map[string]int{}}
	add := func(owner, dir string) {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".enc" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			c.files[path] = trackedThumbnail{path: path, owner: owner, size: info.Size(), when: info.ModTime()}
			c.total += info.Size()
			c.ownerBytes[owner] += info.Size()
			c.ownerFiles[owner]++
		}
	}
	add(root, filepath.Join(root, ".weazl-previews"))
	owners, _ := os.ReadDir(filepath.Join(root, "users"))
	for _, entry := range owners {
		if entry.IsDir() && thumbnailOwnerID.MatchString(entry.Name()) {
			owner := filepath.Join(root, "users", entry.Name())
			add(owner, filepath.Join(owner, ".weazl-previews"))
		}
	}
	c.init()
	return c
}
func forgetThumbnailNode(dir string) {
	_, root := thumbnailOwnerAndRoot(dir)
	thumbnailCacheMu.Lock()
	delete(thumbnailCaches, root)
	thumbnailCacheMu.Unlock()
}
func touchThumbnail(dir, key string, variant int) {
	_, root := thumbnailOwnerAndRoot(dir)
	c := thumbnailNode(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(dir, key+".enc")
	if f, ok := c.files[path]; ok {
		if variant > 0 {
			f.variant = variant
		}
		f.obsolete = false
		c.files[path] = f
		c.touch(path)
	}
}
