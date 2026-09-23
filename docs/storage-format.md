# Data format marker and legacy storage compatibility

Every supported release checks `<configured-data-dir>/.weazl-storage.json` before opening listeners or mutating a library. `weazlcloud -check` validates the marker without creating or changing it. Normal startup creates it atomically on a new or existing unmarked data directory.

The current marker is:

```json
{
  "format_version": 1,
  "minimum_reader_version": 1,
  "minimum_writer_version": 1,
  "mode": "restic-legacy"
}
```

`restic-legacy` means per-user Restic writes. `mixed-shared-experimental` permits whole-file shared-object writes while retaining Restic references. The latter can only be selected with `WEAZLCLOUD_STORAGE_BACKEND=shared-experimental`; the default is `restic`. Startup rejects unknown versions, unsupported minimum reader/writer versions, malformed markers, and attempts to run a mixed data directory in legacy mode. Marker files use mode 0600 in the configured data directory.

Per-user catalogs are still vault-encrypted. On first load, an unversioned legacy entry receives a random stable 128-bit entry ID, revision 1, and a version-1 `restic` reference. Existing `snap` and `object` fields remain intact, including empty legacy object fields and batch object paths; the effective legacy object fallback is recorded separately in the versioned reference. The upgrade is atomically saved and is idempotent. Replacement preserves the entry ID and advances its revision; move, Trash, and restore advance the affected entry revision; copy creates a new ID. Separate live and trashed rows at the same path retain separate IDs.

An unknown or internally inconsistent reference makes that user's catalog load fail closed. The catalog is not rewritten or published in memory on that error. Mixed catalogs can contain both versioned `restic` and `shared-object` references. Shared-object references are bound to an owner, entry, revision, and durable write operation; a reference from another owner or revision cannot be read through the ordinary library API.

The marker is a compatibility check for supported releases. Historic binaries predating it do not inspect it and cannot be made to reject a newer format. Never start such an image against a data directory after a future release has changed its marker or storage format. Do not treat an arbitrary old image as a rollback target; D6 must establish and test the bridge release and reverse-migration procedure before shared writes or source retirement.
# Experimental mixed storage

The application defaults to the existing Restic backend. `WEAZLCLOUD_STORAGE_BACKEND=shared-experimental` opts a disposable test instance into new whole-file shared-object writes while retaining reads of existing Restic catalog references. The data-directory marker prevents starting that directory in legacy mode after shared writes have begun.

This setting is for disposable integration tests only. The shared store has not completed D3 chunking, comparative benchmarks, migration, backup/restore, or the full D4/D6 acceptance gates. Do not enable it on the production volume.
