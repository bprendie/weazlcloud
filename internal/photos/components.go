package photos

import "github.com/bprendie/weazlcloud/internal/catalog"

func projectionComponents(file catalog.File, byID map[string]catalog.File) []Component {
	if len(file.PhotoComponents) == 0 {
		return []Component{{ID: "component:" + file.EntryID, EntryID: file.EntryID, Revision: file.Revision, Path: file.Path, Kind: "original", Hash: file.Hash}}
	}
	parts := make([]Component, 0, len(file.PhotoComponents))
	for _, part := range file.PhotoComponents {
		component, found := byID[part.AssetID]
		if !found || component.Present != file.Present || part.ID != "original" && component.PhotoParentID != file.EntryID {
			continue
		}
		parts = append(parts, Component{ID: "component:" + component.EntryID, EntryID: component.EntryID, Revision: component.Revision, Path: component.Path, Kind: part.ID, Hash: component.Hash})
	}
	return parts
}
