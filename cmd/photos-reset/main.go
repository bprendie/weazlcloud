// photos-reset performs owner-scoped offline maintenance. Inspect is read-only;
// apply requires a stopped server, a rollback copy and the inspected digest.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
	"os"
	"path/filepath"
	"sort"
)

type inventory struct {
	Owner              string `json:"owner"`
	CatalogSHA         string `json:"catalog_sha256"`
	PhotoEntries       int    `json:"photo_entries"`
	PhotoFiles         int    `json:"photo_files"`
	PhotoBytes         int64  `json:"photo_logical_bytes"`
	PhotoDeviceFiles   int    `json:"photo_device_files"`
	DriveEntries       int    `json:"retained_entries"`
	DriveBytes         int64  `json:"retained_logical_bytes"`
	DriveSHA           string `json:"retained_metadata_sha256"`
	Albums             int    `json:"albums"`
	ExclusiveSnapshots int    `json:"exclusive_photo_snapshots"`
	SharedSnapshots    int    `json:"snapshots_retained_for_drive"`
	SharedReferences   int    `json:"photo_shared_object_references"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data := flag.String("data", "/data", "node data directory")
	name := flag.String("user", "", "owner username")
	apply := flag.Bool("apply", false, "apply after stopping the server and taking an owner backup")
	expected := flag.String("expected", "", "catalog SHA256 from stopped-server inspection")
	backup := flag.String("backup", "", "existing full owner rollback directory")
	report := flag.String("report", "", "write non-secret inventory and snapshot retirement plan here")
	retire := flag.Bool("retire", false, "retire the applied plan's unreferenced Restic snapshots while offline")
	prune := flag.Bool("prune", false, "also reclaim unused Restic packs (requires retire)")
	verify := flag.Bool("verify", false, "check repository and stream-hash retained file samples")
	readback := flag.Bool("verify-readback", false, "verify retained snapshots and sample bytes without an exclusive repository check")
	flag.Parse()
	if *name == "" {
		return errors.New("user is required")
	}
	store, err := users.NewReadOnly(filepath.Join(*data, "users.json"), filepath.Join(*data, "users"))
	if err != nil {
		return err
	}
	var owner users.User
	for _, u := range store.Users() {
		if u.Username == *name {
			owner = u
		}
	}
	if owner.ID == "" {
		return errors.New("owner not found")
	}
	root, err := store.DataPath(owner)
	if err != nil {
		return err
	}
	v := vault.New(store.VaultPath(owner), store.NodeKeyPath(owner))
	if err := v.UnlockNode(); err != nil {
		return err
	}
	defer v.Lock()
	cp := store.CatalogPath(owner)
	raw, err := os.ReadFile(cp)
	if err != nil {
		return err
	}
	c := catalog.New(cp, v)
	if err := c.LoadReadOnly(); err != nil {
		return err
	}
	before, retired := inspect(c, raw, owner.ID)
	if *verify || *readback || *retire {
		if err := requireCatalogOwner(cp); err != nil {
			return err
		}
	}
	if *verify || *readback {
		return verifyRepository(v, root, c, *report, !*readback)
	}
	if *retire {
		return retireSnapshots(v, root, c, *report, *prune)
	}
	if !*apply {
		return json.NewEncoder(os.Stdout).Encode(before)
	}
	if *expected == "" || before.CatalogSHA != *expected {
		return errors.New("catalog differs from inspected digest")
	}
	if *backup == "" || *report == "" {
		return errors.New("apply requires backup and report directories")
	}
	prior, err := os.ReadFile(filepath.Join(*backup, "catalog.enc"))
	if err != nil || digest(prior) != before.CatalogSHA {
		return errors.New("rollback catalog does not match live catalog")
	}
	for _, file := range []string{"vault.json", "node.key"} {
		original, e := os.ReadFile(filepath.Join(root, file))
		if e != nil {
			return e
		}
		saved, e := os.ReadFile(filepath.Join(*backup, file))
		if e != nil || digest(original) != digest(saved) {
			return errors.New("rollback key files do not match")
		}
	}
	if before.SharedReferences != 0 {
		return errors.New("shared-object references require shared-store retirement; reset refused")
	}
	if err := os.MkdirAll(*report, 0700); err != nil {
		return err
	}
	plan, err := json.Marshal(struct {
		Before inventory `json:"before"`
		Retire []string  `json:"retire_snapshots"`
	}{before, retired})
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(filepath.Join(*report, "plan.json"), plan, 0600); err != nil {
		return err
	}
	if err := resetUploadState(*data, root, *backup, owner.ID, v, false); err != nil {
		return err
	}
	// The report and full rollback copy are durable before canonical mutation.
	if err := rewritePreservingAccess(cp, func() error {
		_, err := c.ResetPhotos()
		return err
	}); err != nil {
		return err
	}
	if err := c.LoadReadOnly(); err != nil {
		return err
	}
	afterRaw, err := os.ReadFile(cp)
	if err != nil {
		return err
	}
	after, _ := inspect(c, afterRaw, owner.ID)
	if after.PhotoEntries != 0 || after.Albums != 0 || after.DriveSHA != before.DriveSHA {
		return errors.New("reset verification failed; keep server stopped and restore rollback catalog")
	}
	if err := resetUploadState(*data, root, *backup, owner.ID, v, true); err != nil {
		return err
	}
	result, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(filepath.Join(*report, "after.json"), result, 0600); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(after)
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func inspect(c *catalog.Catalog, raw []byte, owner string) (inventory, []string) {
	out := inventory{Owner: owner, CatalogSHA: digest(raw), Albums: len(c.Albums())}
	photos, retained := map[string]bool{}, map[string]bool{}
	var kept []catalog.File
	for _, f := range c.All() {
		snap := f.Snap
		if f.Reference != nil && f.Reference.Backend == catalog.ResticBackend {
			snap = f.Reference.Snapshot
		}
		if catalog.PhotoResetPath(f.Path) {
			out.PhotoEntries++
			if !f.Folder {
				out.PhotoFiles++
				out.PhotoBytes += f.Size
			}
			if f.DeviceID != "" {
				out.PhotoDeviceFiles++
			}
			if snap != "" {
				photos[snap] = true
			}
			if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
				out.SharedReferences++
			}
		} else {
			kept = append(kept, f)
			out.DriveEntries++
			if !f.Folder {
				out.DriveBytes += f.Size
			}
			if snap != "" {
				retained[snap] = true
			}
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].EntryID < kept[j].EntryID })
	b, _ := json.Marshal(kept)
	out.DriveSHA = digest(b)
	var retired []string
	for snap := range photos {
		if retained[snap] {
			out.SharedSnapshots++
		} else {
			retired = append(retired, snap)
		}
	}
	sort.Strings(retired)
	out.ExclusiveSnapshots = len(retired)
	return out, retired
}
