package catalog

import "reflect"

func (c *Catalog) journalCollections(j *Journal) error {
	old := map[string]CollectionFolder{}
	for _, f := range c.savedCollections.Folders {
		old[f.ID] = f
	}
	for _, f := range c.collections.Folders {
		previous, exists := old[f.ID]
		delete(old, f.ID)
		if exists && previous == f {
			continue
		}
		f := f
		if err := j.append(ChangeRecord{Kind: "collection", ID: f.ID, Collection: &f, Photos: true}); err != nil {
			return err
		}
	}
	for id := range old {
		if err := j.append(ChangeRecord{Kind: "collection", ID: id, Deleted: true, Photos: true}); err != nil {
			return err
		}
	}
	for k, m := range c.collections.Sources {
		if reflect.DeepEqual(c.savedCollections.Sources[k], m) {
			continue
		}
		m := m
		if err := j.append(ChangeRecord{Kind: "source-mapping", ID: k, SourceMapping: &m, Photos: true}); err != nil {
			return err
		}
	}
	return nil
}
