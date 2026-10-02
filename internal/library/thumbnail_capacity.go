package library

import (
	"golang.org/x/sys/unix"
	"path/filepath"
	"time"
)

func thumbnailFree(dir string) (int64, bool) {
	var stat unix.Statfs_t
	if unix.Statfs(dir, &stat) != nil {
		return 0, false
	}
	free := int64(stat.Bavail)*int64(stat.Bsize) - int64(stat.Blocks)*int64(stat.Bsize)*3/100
	return max(0, free), true
}
func automaticThumbnailTarget(current, free int64) int64 {
	return min(int64(64<<30), max(0, current+free)/10)
}

// A reservation pins the destination and accounts for all concurrent publications.
// Encryption and file writes occur after this short bookkeeping transaction.
func reserveThumbnail(dir, key string, size int64, variant int) (func(bool), error) {
	owner, root := thumbnailOwnerAndRoot(dir)
	free, ok := thumbnailFree(root)
	if !ok {
		return nil, ErrPreviewCacheSkipped
	}
	c := thumbnailNode(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checked) > time.Minute || c.target == 0 {
		target := automaticThumbnailTarget(c.total, free)
		if c.target == 0 || target < c.target*9/10 || target > c.target*11/10 {
			c.target = target
		}
		c.checked = time.Now()
	}
	limit := c.target
	if thumbnailNodeMaxBytes > 0 {
		limit = thumbnailNodeMaxBytes
	}
	if size > limit || thumbnailMaxBytes > 0 && size > thumbnailMaxBytes || size+c.pending > free {
		return nil, ErrPreviewCacheSkipped
	}
	path := filepath.Join(dir, key+".enc")
	old, exists := c.files[path]
	delta := size - old.size
	count := 0
	if !exists {
		count = 1
	}
	c.pinned[path] = true
	for {
		ownerOver := thumbnailMaxBytes > 0 && c.ownerBytes[owner]+c.pendingOwner[owner]+delta > thumbnailMaxBytes || c.ownerFiles[owner]+c.pendingFiles[owner]+count > thumbnailMaxFiles
		nodeOver := c.total+c.pending+delta > limit
		if !ownerOver && !nodeOver {
			break
		}
		evictOwner := owner
		if !ownerOver {
			evictOwner = c.largestOwner()
		}
		victim := c.victim(evictOwner)
		if victim == "" {
			delete(c.pinned, path)
			return nil, ErrPreviewCacheSkipped
		}
		if err := c.remove(victim); err != nil {
			delete(c.pinned, path)
			return nil, err
		}
	}
	c.pending += delta
	c.pendingOwner[owner] += delta
	c.pendingFiles[owner] += count
	return func(success bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.pinned, path)
		c.pending -= delta
		c.pendingOwner[owner] -= delta
		c.pendingFiles[owner] -= count
		if success {
			c.files[path] = trackedThumbnail{path: path, owner: owner, size: size, when: time.Now(), variant: variant}
			c.total += delta
			c.ownerBytes[owner] += delta
			c.ownerFiles[owner] += count
			c.touch(path)
		}
	}, nil
}
