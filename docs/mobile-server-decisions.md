# Mobile server implementation decisions and release notes

October 2, 2026. Local working-tree implementation against
`a077baf336764ead17cbe983e44907a89efa16fd`. No release commit/image, deployment,
production operation or publication is established by this document.
[Contract](mobile-api.md), [OpenAPI](mobile-api.yaml), and
[validation evidence](mobile-server-validation-2026-10-02.md) are companion files.
The dedicated [migration runbook](mobile-server-migration.md) is owned separately;
use its final reviewed instructions for migration execution. This file records
engineering decisions and release constraints, not an executed migration.

## Identity and authorization

- Durable device identity is distinct from replaceable 90-day credential
  generations. Rotation uses a client-prepared secret and operation/generation
  precondition, with 15-minute previous-generation grace. Secrets are hashed;
  public device projections strip secret/rotation fields.
- Account state writes `state_version: 2`; future versions above 2 are rejected.
  Nil/omitted scopes retain legacy `photos:v1`. Explicit empty scopes remain no
  resource grants. Compatibility must not manufacture new Files/backup/grab rights.
  Public legacy generation/authorization-version projections default to 1;
  this is not evidence of rewriting every legacy record on startup.
- Revocation retains a device identity tombstone. Authorization-version grants
  protect persisted jobs from expiry/revoke/password change/disable/delete while
  allowing ordinary credential rotation to preserve source/receipt identities.
  Parts admission stores `users.DeviceGrant` inside the owner-vault encrypted
  intent; PUT checks grant validity before/after each body Read and uses vault
  session cancellation; the publication guard rechecks that admitted authorization epoch.
- `instance_id` and its lock file are adjacent to the account-store path, normally
  `users.json`. Whole-volume backup/restore preserves it. For an independent clone,
  stop the node, remove only the clone's `instance_id`, and restart to generate a
  new ID. Preserve account/catalog/storage data. Clients must explicitly approve
  the new identity and TLS origins; matching IDs are not transport authentication.
- API contract version 1 is independent of on-disk schema 2. Native requests may
  use `X-Weazl-Mobile-Contract: 1` or `?contract_version=1`; both may be present if
  equal. Omission retains legacy behavior. Unsupported, duplicate, empty or
  mismatched values return 400 `unsupported_contract_version`. Browser `/api/...`
  routes outside `/api/v1/` are not subject to this negotiation check.

Device credentials hold no vault key. Restart leaves the vault locked until the
owner unlocks it; queued workers cannot bypass that step. Native parts format
checks return 423 while legacy ordered Photos GET retains 401 for a locked vault.
After fresh same-ID owner reauthorization, identical immutable create metadata
can CAS-rebind private authorization. Still-valid admission grants are retained;
expired admission can be explicitly readmitted with freshly rotated credentials
in the same epoch. Active jobs and unexpired verifying finalizers conflict until
cancelled/drained. Changing any immutable field, including commit-when-complete,
returns 409 without refreshing the grant. Stored receipt replay is unchanged.
Expired inactive staging can restart with the same logical ID after fresh authority
and quota admission, using a fresh encryption key and zero counters. Accepted bytes
must be retransmitted; content identity and stored originals remain unchanged.
Cancelled intents are durable terminal records and cannot be resurrected.

## Organization and originals

Collections are owner-encrypted virtual folders; they do not relocate canonical
Photos files. Albums have a parent collection, with omitted old mutation fields
preserving the old parent. Folder deletion is empty-only. Source-import batches
apply operations individually, retry dependencies within the page, and return
ordered outcomes; there is no all-or-nothing transaction across an entire source
scan. Source disappearance retains organization/originals. Membership addition
is independent of immutable original receipt identity and does not copy bytes.

Opaque source ID framing is two unsigned 64-bit big-endian UTF-8 byte lengths
followed by the corresponding exact UTF-8 bytes; hash with SHA-256 and encode
unpadded base64url. No Unicode normalization is performed. This derived key is
an identifier, not a content checksum. The API doc includes a reproducible vector.

DNG originals are recognized by TIFF framing, verified for size/hash and marked
preview-unsupported rather than claiming a decoder. Explicit
`opaque-original-v1` uses application/octet-stream and a `.opaque` storage suffix.
Preview support is separate from stored-original success. Initial Hidden state
and original/motion pair metadata publish together; source mappings remain
private. Collection schema-2 cursor negotiation is separate from legacy Photos
asset/album sync, and explicit Hidden context must remain bound to membership
and media projections. Legacy clients must not receive unknown new sync kinds.

## Recurring Files and offline manifests

Backup registration requires an existing stable destination folder ID; the server
does not silently choose/create `/Backups/{device}/{source}`. Registration retries
must match name/destination. Updates currently change source status, not destination.
Detached is terminal; paused can become active. This is a material clarification
of the workbook's proposed revision-checked destination editing: no such endpoint
is implemented in the inspected coordinator.

File/folder operation identity is owner/device/source/item/source-revision. The
immutable normalized specification includes content, path, metadata and expected
server identity/revision. CAS publication checks destination/ancestor/mapped entry
identity, revision, path and collisions under catalog/library mutation locks.
It never turns a disappeared phone item into server deletion. Zero-byte files,
empty folders and owned subtree renames use the same mapping/receipt mechanism.
Encrypted intents plus recovery reconcile storage, publication and receipt writes;
this is not a claim of a multi-file database transaction.

Native Files metadata omits raw backend hashes/references. Content ETags derive
from owner/stable ID/revision; content reference and retention hold are captured
before streaming, preventing path reuse or revision mixing. Offline Files sync
uses a baseline before snapshot and reconciles later deltas. Cursors bind owner
vault encryption, device/filter/session/process and journal chain; 24-hour token
expiry or 8192-change retention loss requires resync. Durable device checkpoints
may resume after session changes if their journal position remains valid.

Collections currently use exact-position snapshot invalidation instead of the
Files snapshot-plus-delta reconciliation approach. A mutation during collection
paging can force restart; clients must not assume all cursor types are equivalent.

## Parts transport and background processing

The new transport is fixed 16 MiB independent parts, exact Content-Length and
`X-Weazl-SHA256`, with complete-component verification before commit. It preserves
existing coordinator receipt IDs rather than exposing legacy staging IDs as new
logical operations. Admission records immutable auto-commit intent. Missing-part
pages bound both response count and scanned indices to 200. Ordinary empty files
have zero data parts; empty folders commit without a parts session.

Payloads use authenticated streaming encryption on the data volume. Session,
private intent and part receipts are owner-vault wrapped. Reconstruction streams
into existing storage/coordinator paths without whole-file RAM or a plaintext
assembled temporary file. Quota reservations account for commit workspace and
staging overhead; no throughput, memory-profile or dedupe saving is promised
without measurement.

The initialized parts manager runs through `Handler.RunUploads`. Global finalizer
concurrency is `min(2,max(1,GOMAXPROCS/2))` (one on the small two-CPU profile,
at most two on larger profiles), with one per owner per fair round. Durable empty
`.queue` and `.live` marker files contain opaque job IDs; `.indexed-v1` records
schema-1 index recovery. Pending examines at most 100 queue markers plus 100
incremental recovery entries per call, rather than a full historical scan every
second. The worker sweeps every 15 minutes; Sweep examines at most 100 live markers
plus 100 recovery entries. Private recovery is limited to unlocked owners. Jobs hold owner leases, watch vault and
credential validity, and use an atomic device-grant publication barrier. This
supports server-driven completion without a client poll. Frozen-image checks and
local RAM/staging-disk measurements are recorded in the
[release ledger](mobile-server-validation-2026-10-02.md); its fixture limits apply.

Unfinished sessions expire after 24 hours of inactivity measured by accepted-part
activity and saved state transitions. Status reads/recovery reconciliation must
not extend that clock. Matching duplicate parts are receipts of prior activity,
not a new accepted-byte advance. Stored logical receipts survive payload cleanup.
The dedicated migration runbook must account for retained receipts and in-flight
job manifests.

Cancellation first durably cancels the coordinator, then the engine, retaining
terminal receipts. Same-intent create returns 404 after cancellation; a new source
revision is required. `CancelCoordinated` can recover publication after a lost
receipt: HTTP returns 409 `already_stored`, with stored status/result metadata,
without deleting the original. Background failures preserve `stale_revision`,
`source_inactive`, `insufficient_storage`, `device_authorization_required`, and
`vault_locked`, with `commit_failed` as the fallback.

The native Photos stream path uses a flat catalog entry ID as its backend object
key. The pinned Restic 0.18 backend rejects nested stdin filenames; logical nested
names remain catalog metadata and references retain the actual backend object key.
This fix does not introduce raw staging or a plaintext assembled copy.

New owner-backed ordered uploads now use encrypted **format 2**. Their existing
`.json` manifest filename starts with `WZU2\n`, followed by owner-vault wrapped
private state. Each verified PATCH payload is an authenticated encrypted segment,
promoted from `.chunk` to `<id>.part/<index>.enc` through a durable pending marker.
Full verification/finalize opens one segment at a time; no plaintext assembled
spool is required by this adapter. The owner-vault resolver is required for new
production encrypted sessions. Future/unsupported manifest formats fail closed.

Old raw JSON manifests and flat `.part/.chunk` payloads remain **format 0** and
use the read/resume/finalize drain adapter until completion, cancellation or
expiry. No eager conversion or deletion of accepted legacy bytes is performed;
format-0 staging remains plaintext until drained. Resolver-less adapters retain
legacy behavior and must not be described as encrypted production staging.
Locked format-2 owners defer private reservation recovery/expiry until unlock;
existing disk allocation still counts for available-space admission. This adapter
covers `upload.Manager` staging, not every separate generic Library commit spool.
See [ordered staging compatibility](../internal/upload/STAGING.md) for exact paths.

Backup source and operation records now write explicit private `version: 1`.
Version 0/missing is accepted as the earlier compatible record and projected to
version 1 on read; negative or future versions above 1 fail closed with
`ErrUnsupportedVersion`. Wrong record type/operation identity is also rejected.
The public Source/View DTOs do **not** expose that private format-version field.

## Grabs and release compatibility

Idempotency-Key wraps existing file/folder/gallery mint operations and stores
owner-encrypted operation recovery records. Same key/spec recovers the original
result; changed canonical specification conflicts. An unpublished interrupted
mint is terminal to avoid silently choosing newer bytes. Lookup requires
`grabs:read`; mint needs `grabs:write` plus the relevant source read scope.
Unkeyed browser behavior is preserved. Whole-album export must use a complete
selection/membership enumeration rather than an album header's bounded IDs.
Canonical admin HTTPS base, frozen bytes, burn/revoke semantics, 30-day Trash and
90-minute ready on-demand ZIP expiry retain distinct lifetimes.

## Migration/release constraints

Inspect the final migration runbook after projection fixes. Current storage paths
observed in code (relative to the configured owner directory) are:

| Artifact | Format/role |
| --- | --- |
| `catalog.enc` | Vault-wrapped tree with `schema: 2`, files/albums/collections/source mappings, journal/checkpoints |
| `.weazl-photo-ingest/{id}.enc` | Version-1 logical photo receipts |
| `.weazl-backups/{key}.enc` | Version-1 encrypted source/operation intent and mappings; compatible version-0 reader |
| `.weazl-mobile-parts/{id}/session.enc` | Version-1 encrypted session and private authorization intent |
| `.weazl-mobile-parts/{id}/{component}-{index}.wza` | Encrypted payload part |
| `.weazl-mobile-parts/{id}/{component}-{index}.receipt.enc` | Encrypted part acceptance receipt |
| `.mobile-grabs/{fingerprint}.enc` | Version-1 encrypted mint/recovery operation |
| Existing configured upload root | New WZU2 format-2 encrypted manifests/segments plus retained format-0 raw sessions |

Catalog loading lazily upgrades missing identities/references and older schema
while owner-unlocked; saves publish schema-2 metadata/journal together atomically.
Existing custom album IDs/memberships remain at root unless explicitly parented;
Takeout projections remain at their physical paths. Future catalog schema above
2 is rejected. An encrypted recovery copy/repeatable complete-volume migration
fixture must be verified in the runbook; atomic write alone is not that evidence.

Before any authorized deployment: record a fixed candidate commit/image digest,
stop/drain writes using the deployment's existing lifecycle, take a consistent
snapshot of the entire mounted data/reference state, inspect headroom and active
sessions, then run local compatibility checks. Owner unlock is needed for private
migration/job reconciliation; administration does not substitute for vault keys.
Compare stable IDs/revisions/counts/memberships and original hashes after restart,
and verify active old uploads resume. Do not delete data to make a fixture pass.

**Do not deploy an old binary against migrated writable data.** Old JSON writers
may ignore/drop new fields even if a new schema field exists. Prefer a forward
fix. Downgrade requires restoring a complete consistent pre-upgrade snapshot,
including users/credentials, vault/node keys, catalogs/journals, Restic/shared
reference state, receipts, staging and frozen grabs. State all newer writes lost
by restoration. Never mix a restored catalog with newer shared reference data.
No rollback/restore exercise is claimed here.
