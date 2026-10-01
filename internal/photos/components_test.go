package photos

import (
	"github.com/bprendie/weazlcloud/internal/catalog"
	"testing"
)

func TestProjectionPreservesLogicalPairAndSourceIdentity(t *testing.T) {
	files := []catalog.File{
		{EntryID: "still", Revision: 4, Path: "Photos/Phone/a.heic", Present: true, DeviceID: "phone", DeviceAssetID: "local", SourceRevision: "r1", PhotoComponents: []catalog.PhotoComponent{{ID: "original", AssetID: "still"}, {ID: "motion", AssetID: "motion"}}},
		{EntryID: "motion", Revision: 2, Path: "Photos/Phone/a.mov", Present: true, PhotoParentID: "still"},
	}
	p := BuildProjection("owner", files, nil)
	if len(p.Assets) != 1 || len(p.Assets[0].Components) != 2 || p.Assets[0].SourceRevision != "r1" {
		t.Fatalf("pair lost: %+v", p)
	}
	if len(p.Albums) != 1 || len(p.Albums[0].AssetIDs) != 1 {
		t.Fatalf("motion counted as photo: %+v", p.Albums)
	}
	p.Assets[0].Components = append([]Component(nil), p.Assets[0].Components...)
	p.Assets[0].Components[1].Revision++
	p.Assets[0].SourceRevision = "r2"
	report := CompareLegacy("owner", files, p)
	if report.ComponentMismatches != 1 || report.SourceMismatches != 1 || report.MissingAssets != 0 {
		t.Fatalf("comparison failed: %+v", report)
	}
}
