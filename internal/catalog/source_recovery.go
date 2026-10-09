package catalog

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

const SourceRecoveryMaximum = 200

var ErrSourceRecovery = errors.New("invalid source collection lookup")

// ServerRevision is the revision recorded by the source mapping. A later server
// edit can differ; a deleted target retains its mapping and has no current revision.
type SourceRecoveryMatch struct {
	SourceMapping
	CurrentServerRevision *uint64 `json:"current_server_revision"`
	TargetExists          bool    `json:"target_exists"`
}

type SourceRecoveryPage struct {
	Matches    []SourceRecoveryMatch `json:"matches"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor,omitempty"`
	After      string                `json:"-"`
}

func ValidateSourceRecovery(ids []string, namespace string, limit int) error {
	if len(ids) < 1 || len(ids) > SourceRecoveryMaximum || limit < 1 || limit > SourceRecoveryMaximum || len(namespace) > 200 || !utf8.ValidString(namespace) || strings.ContainsRune(namespace, 0) {
		return ErrSourceRecovery
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 4096 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) || seen[id] {
			return ErrSourceRecovery
		}
		seen[id] = true
	}
	return nil
}

// LookupSourceCollections reads only this owner's catalog, across all devices
// and (unless filtered) namespaces. It never adopts, edits, or recreates a target.
// Keyset pages are live reads, not a snapshot; revisions are current per page.
func (c *Catalog) LookupSourceCollections(ctx context.Context, ids []string, namespace, after string, limit int) (SourceRecoveryPage, error) {
	if err := ValidateSourceRecovery(ids, namespace, limit); err != nil {
		return SourceRecoveryPage{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	// Keep only the next bounded page, even when many old devices share IDs.
	keys := &sourceRecoveryKeys{}
	for key, m := range c.collections.Sources {
		if err := ctx.Err(); err != nil {
			return SourceRecoveryPage{}, err
		}
		if key <= after || !wanted[m.SourceID] || (namespace != "" && m.Namespace != namespace) || (m.Kind != "album" && m.Kind != "folder") {
			continue
		}
		if keys.Len() < limit+1 {
			heap.Push(keys, key)
		} else if key < (*keys)[0] {
			(*keys)[0] = key
			heap.Fix(keys, 0)
		}
	}
	sort.Strings(*keys)
	page := SourceRecoveryPage{Matches: []SourceRecoveryMatch{}, HasMore: keys.Len() > limit}
	if page.HasMore {
		*keys = (*keys)[:limit]
		page.After = (*keys)[limit-1]
	}
	revisions := make(map[string]*uint64, len(*keys))
	for _, key := range *keys {
		m := c.collections.Sources[key]
		revisions[m.Kind+"/"+m.ServerID] = nil
	}
	for _, a := range c.albums {
		if _, ok := revisions["album/"+a.ID]; ok {
			revisions["album/"+a.ID] = &a.Revision
		}
	}
	for _, f := range c.collections.Folders {
		if _, ok := revisions["folder/"+f.ID]; ok {
			revisions["folder/"+f.ID] = &f.Revision
		}
	}
	for _, key := range *keys {
		m := c.collections.Sources[key]
		match := SourceRecoveryMatch{SourceMapping: m}
		if revision := revisions[m.Kind+"/"+m.ServerID]; revision != nil {
			match.TargetExists, match.CurrentServerRevision = true, revision
		}
		page.Matches = append(page.Matches, match)
	}
	return page, ctx.Err()
}

// A max heap retains the smallest limit+1 keys with bounded extra memory.
type sourceRecoveryKeys []string

func (h sourceRecoveryKeys) Len() int           { return len(h) }
func (h sourceRecoveryKeys) Less(i, j int) bool { return h[i] > h[j] }
func (h sourceRecoveryKeys) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *sourceRecoveryKeys) Push(x any)        { *h = append(*h, x.(string)) }
func (h *sourceRecoveryKeys) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
