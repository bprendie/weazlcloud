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

Version 1 means per-user Restic repositories remain the only supported storage backend. Shared storage and migration are not enabled. A supported binary rejects unknown versions, unsupported minimum reader/writer versions, malformed markers, and unknown storage modes before it binds listeners. Marker files use mode 0600 in the configured data directory.

Per-user catalogs are still vault-encrypted. On first load, an unversioned legacy entry receives a random stable 128-bit entry ID, revision 1, and a version-1 `restic` reference. Existing `snap` and `object` fields remain intact, including empty legacy object fields and batch object paths; the effective legacy object fallback is recorded separately in the versioned reference. The upgrade is atomically saved and is idempotent. Replacement preserves the entry ID and advances its revision; move, Trash, and restore advance the affected entry revision; copy creates a new ID. Separate live and trashed rows at the same path retain separate IDs.

An unknown or internally inconsistent reference makes that user's catalog load fail closed. The catalog is not rewritten or published in memory on that error. No supported current operation creates or resolves a shared-object reference.

The marker is a compatibility check for supported releases. Historic binaries predating it do not inspect it and cannot be made to reject a newer format. Never start such an image against a data directory after a future release has changed its marker or storage format. Do not treat an arbitrary old image as a rollback target; D4 and D6 must establish and test the bridge release and reverse-migration procedure before shared writes or source retirement.
