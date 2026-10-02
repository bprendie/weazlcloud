package catalog

import (
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"os"
)

func (c *Catalog) saveLocked() error {
	return c.saveFilesLocked(c.files)
}

func (c *Catalog) saveFilesLocked(files []File) error {
	journal, err := c.nextJournalLocked(files)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(tree{Schema: 2, Collections: c.collections, Files: files, Albums: c.albums, Journal: journal, Checkpoints: c.checkpoints})
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	raw, err := c.vault.Wrap(plain)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(c.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := syncCatalogDirectory(c.path); err != nil {
		return err
	}
	c.diskInfo, _ = os.Stat(c.path)
	c.journal = journal
	c.savedAlbums = cloneAlbums(c.albums)
	c.savedCollections = cloneCollectionState(c.collections)
	c.summaryReady = false
	c.children = indexChildren(files)
	c.byPath = indexPaths(files)
	c.version++
	return nil
}
