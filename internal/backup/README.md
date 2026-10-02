# Recurring backup coordinator integration

`backup.New(uploads *upload.Manager)` is stateless across requests. No Handler
field is required. Wire `tryMobileBackups(w,r)` before the generic Files router;
wire the parent's independent-parts dispatcher first so it owns parts create,
status, cancellation, transfer and autonomous finalize. Existing ordered PATCH
fallback uses the shared upload.Manager.

Parts contract:

```go
CreateParts(ctx context.Context, res *filesvc.Resource, user users.User,
    device string, spec Spec) (View, error)
FinalizeParts(ctx context.Context, res *filesvc.Resource, user users.User,
    id, device string, body io.Reader) (View, error)
SetCommitGuard(func(publish func() error) error)
```

View exposes ID, ReceiptID, Status, normalized Spec, optional UploadID and File.
Spec carries DeviceID, SourceID, ItemID, SourceRevision, RelativePath, Filename,
Kind, Size, SHA256, Mtime, ExpectedEntryID, ExpectedRevision and Transport.
`parts-v1` creates no upload.Manager session. Folder intents finalize with a nil
reader. File readers are streamed through storage and defensively rehashed.
Parent owns encrypted parts staging, missing pages, quota reservation (including
backend workspace), jobs, device grants and Registry.Enter lifecycle leases.

Always install the final request/job guard before use. Acquire
`users.WithDeviceGrant(grant, publish, users.BackupWrite, users.FilesWrite)`
INSIDE the callback, not around the entire coordinator call: library mutation
lock precedes users lock. Source registration/status changes and new intents
also pass through this guard. Parent should cancel long readers on vault lock
and owner drain. Coordinator checks vault/context before publication; no
background work or decrypted private state remains in Manager.

State lives at `<owner data directory>/.weazl-backups`, inside the existing owner
removal boundary. Filenames are vault-keyed hashes; manifests/mappings/receipts
are vault-wrapped and directory-synced. Keys include owner vault, stable device,
opaque source, opaque item and exact source revision. Identical normalized retry
returns the same receipt; a different specification returns conflict. Rotate the
device credential without replacing its stable device identity.

Catalog CAS guards mapped ID/revision/path and existing parent/destination
identity; collisions, user edits, trash/restore and parent moves fail closed.
Missing relative folders get stable IDs. Concurrent intents may adopt only exact
unchanged parents owned by this same source. Folder renames require every current
descendant to belong to that source and update descendant mappings atomically.
Stopping/detaching never sends a Library delete. Detached sources stay detached;
there is no implicit remapping or destructive conflict resolution.

Before publication, the encrypted operation journal records the exact planned
files and backend reference/Operation. After publication, the atomic catalog
change journal proves the result even following a user edit. Source mapping and
receipt writes can then be recovered independently. A pending source publication
is settled before admitting its next mutation. If both current metadata and the
8192-record retained journal lack publication proof, guards fail closed rather
than replaying an old overwrite. This bounded evidence case requires explicit
conflict resolution; finalized immutable receipts do not expire with staging.

Storage uses existing restic/shared backends, including the storage agent's
**encrypted** shared PrepareWithID implementation. Shared publication follows
catalog CAS, then MarkPublished/Commit through Recover; only replaced owner
references are released. Startup can abort unpublished prepares: retry preserves
receipt identity and uses a fresh private storage attempt after checking the
original CAS. Restic zero-byte files use an empty-file batch in the owner data
directory, removed on exit. No source plaintext spool is created here.

Tests cover restic/shared storage, restart/lost response, receipt-write and
shared commit faults, aborted prepare recovery, CAS collisions and trash/restore,
empty files/folders, relative nesting, folder rename/foreign descendant conflict,
independent parent adoption, owner/device isolation, device commit guards,
sequential fallback, checksums and the HTTP source/parts receipt adapter.

## Owned files

- `internal/backup/`: `types.go`, `spec.go`, `sources.go`, `plan.go`,
  `parents.go`, `create.go`, `finalize.go`, `operations.go`, `backup_test.go`,
  `coordination_test.go`, `recovery_test.go`, `version_test.go`, `README.md`.
- `internal/catalog/`: `backup_cas.go`, `backup_cas_test.go`.
- `internal/library/`: `backup_transaction.go`, `backup_storage.go`.
- `internal/desk/`: `mobile_backup.go`, `mobile_backup_uploads.go`,
  `mobile_backup_test.go`.
- Follow-on staging work is listed in `internal/upload/STAGING.md`.
- Explicitly requested lifecycle fixture update:
  `internal/accountlifecycle/manager_test.go` (forge owner vaults; unlock the
  surviving owner's vault before inspecting its encrypted session after restart).

Private source and operation records carry schema version 1. Version 0 records
from the initial coordinator remain readable and upgrade on their next supported
write. Negative/future versions fail closed and preserve the encrypted file
byte-for-byte; registration retries, source mutations and receipt operations
cannot downgrade them. `version_test.go` covers preservation and lazy upgrade.
