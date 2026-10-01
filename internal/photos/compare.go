package photos

import (
	"github.com/bprendie/weazlcloud/internal/catalog"
	"slices"
)

type CompareReport struct {
	LegacyAssets         int
	ProjectedAssets      int
	RootMismatches       int
	MissingAssets        int
	UnexpectedAssets     int
	PathMismatches       int
	RevisionMismatches   int
	OwnerMismatches      int
	CaptureMismatches    int
	MediaMismatches      int
	ComponentMismatches  int
	SourceMismatches     int
	MembershipMismatches int
	AlbumMismatches      int
}

func CompareLegacy(ownerID string, files []catalog.File, projection Projection) CompareReport {
	return CompareProjection(ownerID, files, projection.Roots, projection)
}

func CompareProjection(ownerID string, files []catalog.File, legacyRoots []SourceRoot, projection Projection) CompareReport {
	report := CompareReport{}
	legacy := BuildProjection(ownerID, files, legacyRoots)
	report.LegacyAssets, report.ProjectedAssets = len(legacy.Assets), len(projection.Assets)
	report.RootMismatches = compareRoots(legacy.Roots, projection.Roots)
	byID := make(map[string]Asset, len(projection.Assets))
	legacyByID := make(map[string]Asset, len(legacy.Assets))
	for _, asset := range legacy.Assets {
		legacyByID[asset.ID] = asset
	}
	for _, asset := range projection.Assets {
		byID[asset.ID] = asset
		if asset.OwnerID != ownerID {
			report.OwnerMismatches++
		}
	}
	for _, asset := range legacy.Assets {
		current, ok := byID[asset.ID]
		if !ok {
			report.MissingAssets++
			continue
		}
		if current.Path != asset.Path {
			report.PathMismatches++
		}
		if current.Revision != asset.Revision {
			report.RevisionMismatches++
		}
		if !sameCapture(current.Capture, asset.Capture) {
			report.CaptureMismatches++
		}
		if current.MediaType != asset.MediaType || current.Width != asset.Width || current.Height != asset.Height || current.DurationMillis != asset.DurationMillis || current.Orientation != asset.Orientation || current.UserRotation != asset.UserRotation || current.PreferredPhoto != asset.PreferredPhoto || current.Camera != asset.Camera {
			report.MediaMismatches++
		}
		if !slices.Equal(current.Components, asset.Components) {
			report.ComponentMismatches++
		}
		if current.DeviceID != asset.DeviceID || current.DeviceAssetID != asset.DeviceAssetID || current.SourceRevision != asset.SourceRevision {
			report.SourceMismatches++
		}
		if !sameIDs(current.AlbumIDs, asset.AlbumIDs) {
			report.MembershipMismatches++
		}
	}
	for _, asset := range projection.Assets {
		if _, ok := legacyByID[asset.ID]; !ok {
			report.UnexpectedAssets++
		}
	}
	for _, album := range projection.Albums {
		if len(album.AssetIDs) == 0 {
			report.AlbumMismatches++
		}
	}
	legacyAlbums := make(map[string]Album, len(legacy.Albums))
	for _, album := range legacy.Albums {
		legacyAlbums[album.ID] = album
	}
	for _, album := range projection.Albums {
		old, ok := legacyAlbums[album.ID]
		if !ok || old.OwnerID != album.OwnerID || !sameIDs(old.AssetIDs, album.AssetIDs) {
			report.AlbumMismatches++
		}
	}
	return report
}

func compareRoots(left, right []SourceRoot) int {
	if len(left) != len(right) {
		return 1
	}
	byID := make(map[string]SourceRoot, len(left))
	for _, root := range left {
		byID[root.ID] = root
	}
	for _, root := range right {
		old, ok := byID[root.ID]
		if !ok || old.EntryID != root.EntryID || old.Path != root.Path || old.Included != root.Included {
			return 1
		}
	}
	return 0
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameCapture(left, right *Capture) bool {
	if left == nil || right == nil {
		return left == right
	}
	if !left.Time.Equal(right.Time) || left.Source != right.Source || left.UserCorrected != right.UserCorrected {
		return false
	}
	if (left.OffsetMinutes == nil) != (right.OffsetMinutes == nil) {
		return false
	}
	return left.OffsetMinutes == nil || *left.OffsetMinutes == *right.OffsetMinutes
}
