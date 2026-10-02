# Mobile server additions — Sol execution workbook

Date: **October 2, 2026**
Owner: Bob
Implementer: Sol
Status: **MS0–MS8 server implementation/checks complete; workbook frozen for parent release commit and deployment**
Source baseline/current local HEAD: **a077baf336764ead17cbe983e44907a89efa16fd**;
implementation is in the uncommitted shared worktree. The tested local candidate
is `weazlcloud:smoke`, version `mobile-server-local`, image ID
`sha256:cd7429e383ca3825a6ebef0bb737ca8e69df5347f03896bd0d11d3949dc5004d`
(parent-reported). No release commit/push, completed production snapshot or
deployment is claimed here.

## Outcome

Give the native WeazlCloud app a reliable server foundation for contextual Files
and Photos modes, recurring one-way backups, preserved photo albums/folders,
offline copies, and file/folder/photo grab links. A phone should not need a fresh
web login every day to back up files. An interrupted upload or metadata conflict
must not duplicate a backup, erase organization, or stop unrelated items.

The app scaffold plan is
[`BUILD_PLAN.md`](/home/bobp/Code/iOS/weazlcoud_app/BUILD_PLAN.md).
This workbook specifies its server gaps B1–B5 and the ingestion compatibility
issues identified below. It does not build the iOS app.

Read `weazl_ethos.md`, `README.md`, `docs/photo-api.md`, `docs/photo-api.yaml`,
`docs/modal-architecture.md`, this workbook, and the app's settled decision table.
Earlier workbooks describe history; current code and this workbook govern this pass.

## Scope and settled behavior

- Execute the workbook and record implementation and test evidence. The user has
  explicitly authorized execution, commit/push and eventual production deployment;
  this supersedes the original planning-only text. Authorization is not evidence
  that release checks, publication or deployment have occurred. This checkpoint
  update changes only this workbook; the parent owns final checks and rollout.
- Preserve users, vaults, IDs, dates, albums, Hidden state, storage references,
  existing browser/WebDAV flows, active uploads and frozen grabs. No wipe/reimport.
- Keep Go and the existing encrypted catalog/storage backends. No database
  side-container, cloud identity, external backup relay, or server iCloud login.
- Keep every Go file **below 300 lines**, as `make lines` actually enforces.
- Photos backup includes existing/new accessible originals, videos and Live Photo
  components. Preserve source albums and nested folders without a byte copy per album.
- Selected phone folders/files are recurring **one-way** sources. Preserve relative
  paths and empty folders. Phone deletion never deletes a server original.
- Default app backup is Wi-Fi, with cellular opt-in and charging preferred for a
  large initial run. Scheduling and PhotoKit permissions belong to iOS, not Go.
- Support protected client listings/previews and explicit offline file/folder/album
  pins. Keep the server's HTTP responses private/no-store.
- One account/server with home/remote addresses. App UI lock is distinct from
  server-vault lock. A locked server vault still rejects ingestion/private reads.
- Admin approval and owner isolation remain intact. Admin status does not grant
  another user's vault access. No device credential gains administration rights.
- Shared available space remains the quota; retain the silent reserve. No new
  arbitrary file-size cap. Trash remains 30 days; ready on-demand ZIPs 90 minutes;
  grab material follows its existing burn/expiry/revocation lifecycle.
- Preserve concurrent agents' changes and the existing production configuration.
  Complete final candidate validation and the consistent-snapshot runbook before
  exercising the already granted publication/deployment authorization.

## Execution checkpoint — October 2, 2026

The implementation, API docs, isolated smoke script and app-agent handoff exist.
The authoritative release evidence is the
[validation ledger](docs/mobile-server-validation-2026-10-02.md); companion
[decisions](docs/mobile-server-decisions.md),
[migration runbook](docs/mobile-server-migration.md) and
[app handoff](docs/mobile-app-handoff.md) record contracts and remaining work.
Checked boxes below mean implemented or evidenced as specified. Local final-image
passes are recorded below; unchecked boxes identify remaining release deliverables.

Read-only local inspection found HEAD above and a dirty shared worktree (180
tracked/untracked status rows at inspection). Installed tools: Go
`go1.27.0-X:nodwarf5 linux/amd64`, Node `v26.7.0`, Python `3.14.7`, Restic
`0.19.1` built with `go1.26.4-X:nodwarf5`; the image pins Restic **0.18.0**.
Host and image dependency versions are not interchangeable validation evidence.
Fixtures cover `restic` and `shared-experimental`; the mobile smoke target uses
an isolated local image/volume with **2 CPUs / 4 GiB** and defaults to both backends.
Focused fixture commands are in the migration runbook and below.

This agent observed passing full users/accountlifecycle/app suites and a full desk
suite (105.255 s) before subsequent parent freeze fixes. Focused publication
lock-order/revocation race checks passed. Latest named HTTP regressions passed:
`go test ./internal/desk -run TestMobilePartsReauthorizationRejectsChangedCommitAndResumesSameIntent -count=1`
(8.382 s, including encrypted expired-stage fixtures) and
`go test ./internal/desk -run TestMobilePartsCancellationSurvivesSweepRestartAndPreservesStoredOriginal -count=1`
(8.796 s, Photos and backups). These establish immutable replay rejection,
fresh same-ID admission, stage key/progress reset, durable cancellation across
sweep/restart, and stored-original preservation; they do not certify the final image.

Final update supplied by the parent: **`make check` exit 0**, `make build`, and
final-image container, browser and Photos smokes passed on **both** storage
backends; native extended Photos plus **250 MiB** passed on both with **2 CPUs /
4 GiB**, exit 0. These supersede the earlier pending/failed local-image gates.
The ledger records sampled PID-1 RSS peaks of **143.7 MiB Restic / 145.6 MiB shared**;
these are sampled observations, not unsampled maxima or physical-phone/LAN claims.

The combined owner-deletion regression below passed normally (4.755 s) and with
the race detector (7.711 s). It closes the previously missing deletion fixture.
Together with full-suite legacy token/editor checks, final-image WebDAV/ordered
restart/native sequences and the existing migration-twice fixture, this closes
the MS7 compatibility matrix; no single gigantic all-client fixture is required.

Final Photos **both-backend PASS** is confirmed by parent session **21528**.
The same-image native staging-disk checks also passed on both backends: before
the last missing part, **245,506,048 allocated bytes / 15 authenticated WZA1
payloads**; after stored receipt recovery, **16,384 allocated bytes / zero payloads /
four encrypted receipts**. These measurements close the pending bounded-staging
probe; they describe the disposable fixture, not production capacity or phone disk.
See the [validation ledger](docs/mobile-server-validation-2026-10-02.md) for
artifact/metrics evidence, and the [migration runbook](docs/mobile-server-migration.md)
for release snapshot and recovery instructions.

Remaining parent release work only: record the exact release commit/push,
production snapshot and deployment outcomes after those actions. **Workbook
frozen**: no further agent writes; the main agent records the release afterward.
Physical iOS permissions, iCloud/provider access and background scheduling remain
unrun; server/image passes cannot close that separate gate.

## Historical starting points — before this implementation

| Code | Existing behavior / implication |
| --- | --- |
| `internal/users/devices.go`, `store.go` | Hashed 90-day device tokens, fixed Photos-only path allowlist, 24-hour account cookies. Token presence already prevents cookie fallback. Device revoke currently removes its record. |
| `internal/desk/devices.go`, `multi.go` | Cookie/unlocked-vault enrollment; bearer self-revoke; Library/account/Photos handlers already share owner/lifecycle guards. |
| `internal/catalog/albums.go`, `catalog.go` | Custom albums have IDs/revisions/memberships but no parent collection folder or source identity. Catalog is encrypted JSON; adding fields alone is not a safe downgrade strategy. |
| `internal/catalog/journal.go`, `checkpoint.go` | Atomic catalog/journal writes, 8,192 retained changes, restore/branch detection, device checkpoints. Reuse this machinery. |
| `internal/library/photo_sync*.go`, `photo_album*.go` | Bounded snapshots/deltas and membership paging already exist. Preserve legacy album response bounds. |
| `internal/photoingest/`, `internal/catalog/photo_ingest.go` | Encrypted logical receipts, `(device, asset, revision)` identity, still/motion commit, album assignment and processing outbox. Reuse the commit path. |
| `internal/photoingest/spec.go` | At most original/motion components; tight ASCII source keys; format allowlist rejects DNG and unknown originals. Don't pass raw PhotoKit identifiers into path-safe fields. |
| `internal/upload/` | 16 MiB ordered chunks, offsets/checksums, 24-hour unfinished-session lifetime, reservations and recovery. `.json`, `.chunk` and `.part` currently contain unencrypted staging metadata/bytes. |
| `internal/desk/multi_upload.go` | Generic Library finalize can recognize same path/hash/size, but lacks recurring phone-source identity and a durable per-source replacement contract. |
| `internal/cryptox/streamfile_*.go` | Existing authenticated streaming-file helpers are available for encrypted staging; don't invent a cipher. |
| `internal/accountlifecycle/`, `internal/filesvc/` | Owner draining, shutdown, account deletion and shared-object reference cleanup exist. New jobs/stores must participate. |
| `internal/desk/multi_capsules.go`, Photos grab/archive handlers | Existing file/folder/gallery sharing, frozen copies, retry counting and retention. Extend authorization and retry safety, not sharing semantics. |

## Defaults for Sol to implement

These are engineering defaults chosen for this workbook. Record material changes
with evidence in `docs/mobile-server-decisions.md`; don't silently reduce the app
requirements or reopen already answered product questions.

### Device identity and authorization

Separate a durable **device ID** from its replaceable credential generations.
Preserve the device ID during rotation, expiry/re-enrollment, and account-password
reauthentication so existing upload receipts and source mappings remain usable.
An owner account session may explicitly reactivate a known device; an expired or
revoked token cannot reactivate itself. Device labels are not identity.

Use explicit method/route scope checks, default deny. Proposed scopes:

| Scope | Allowed responsibility |
| --- | --- |
| `photos:read` | Visible/explicit-Hidden photo reads, albums, dates, seek, search, photo sync |
| `photos:write` | Photo upload/metadata/organization/trash operations; source collection import |
| `files:read` | Owner Library browsing/search/metadata/previews/original reads and file sync |
| `files:write` | Owner file/folder mutation, ordinary uploads and trash/restore |
| `backup:write` | Register/update own-device backup sources, recurring file ingestion and receipts; also require `files:write` |
| `grabs:read` | Owner grab status/listing |
| `grabs:write` | Mint/revoke owner grabs; mint/export additionally requires read access to the selected source |
| `storage:read` | Owner-visible quota/dedupe figures only |

Self-status/rotation/self-revoke are intrinsic device operations. New app enrollment
requests the needed scope set and returns the granted set. Cookie authentication
and approval rules remain unchanged. Existing devices lacking a scopes field retain
their exact legacy `photos:v1` rights and **gain no Files or generic grab rights**.
Do not reinterpret an empty scopes array as full access.

Avoid making any `/api/` or `/api/v1/` prefix generally bearer-accessible. Account
settings/password/rekey, vault unlock, enrollment, admin, node hostname, takeout,
global maintenance and unrelated devices remain outside these grants. Add narrow
safe read projections where the app needs profile/status; don't expose all account
handlers for convenience. Existing Photos maintenance rights need an explicit
legacy mapping; new tokens do not implicitly receive them.

Keep token hashes only. Proposed rotation protocol: the client first saves a
fresh random 32-byte replacement token, then submits it over authenticated TLS
with an operation ID and expected credential generation. Atomically replace the
stored hash, retaining the prior generation for at most **15 minutes** to settle
requests. A repeat of the same operation returns the committed generation, not
another token. Previous-generation credentials cannot initiate a different
rotation or extend their grace period. A lost response is recoverable because
the client already holds the new secret. Competing rotations return a conflict.
Redact the replacement token from all logs and error bodies.

New generations expire after 90 days; clients can rotate proactively when less
than 30 days remain. This avoids daily login without making inactive credentials
immortal. Delayed background tasks using an expired generation fail explicitly
and resume after client credential reconciliation. Password change, disable,
delete and revoke invalidate **all** generations, including grace credentials.
Count active devices against the existing enrollment limit; retained identity
tombstones must not consume every slot.

Persist a random node `instance_id` under the data volume and expose it through
minimal discovery plus authenticated capabilities. Preserve it on restart/upgrade;
define clone/reset behavior. It helps catch a wrong home/remote server but is
not a replacement for valid TLS and explicit client-approved origins. Never build
grab URLs from the app's chosen connection address.

### Source organization and one-way semantics

Add owner-encrypted **collection folders** distinct from physical Library folders.
Albums may reference a collection folder; folders may reference parent folders.
Persist source namespace/device/source-collection IDs and source revision for
idempotent import. Preserve empty folders/albums, display order and duplicate
titles under different parents. Membership is many-to-many; media bytes remain
at their canonical Photos paths.

Keep old custom albums at collection root with their existing IDs, titles,
memberships and revisions. Existing Takeout folder-derived albums remain valid
projections; don't relocate their files into the new virtual tree. Omitted new
fields in old browser mutations preserve existing values, not reset nesting.

Source scans are paged and restartable. Default reconciliation adds media and
memberships, and tracks source renames/moves by identity. A missing item in an
incomplete scan never means deletion. Source deletion/removal retains server
originals and backed-up organization. Detect intentional server-side rename,
move or membership edits; return a conflict rather than overwrite them silently.

External source IDs are opaque metadata, never filesystem paths. Specify a shared
normalization test vector for native clients: use a deterministic path-safe key
for the existing `device_asset_id` field (for example base64url SHA-256 of the
source namespace and exact opaque ID bytes with unambiguous framing). This is
an identity key, **not a media-content hash**. Store any raw source identifiers
only in encrypted mappings; preserve the legacy safe-ID contract.

### Recurring file backups

Register a source scoped to `(owner, device, source_id)` and an owner-chosen
destination folder ID. Default client placement is `/Backups/{device}/{source}`;
the server accepts owner-selected folders without inventing a new vault.
Use stable entry IDs and revision preconditions, not path strings alone.

An item's idempotency identity is `(owner, device, source_id, source_item_id,
source_revision)`. The immutable specification includes original size/hash,
relative path, modification metadata, kind and replacement precondition.
Identical retry returns the original operation/result; changed content/specification
under the same identity returns 409. A source revision must not silently target
a new unrelated file after rename or restore.

Changes may replace the entry previously mapped to that source only if its
expected server revision still matches. A user edit, destination collision,
deleted/restored entry or moved parent produces an explicit conflict. Source
renames move the owned mapping under the same checks. No unbounded history is
implied. Source deletion, stopping backup, or detaching a source retains assets.
Support zero-byte ordinary files and empty folders; media validators may still
reject empty photo/video originals.

### Background transport and staging

Apple background uploads use file-backed URLSession tasks, and repeated app
wakeups can be rate limited. Our design inference is to let the app prepare a
bounded group of independent transfers and let the server finalize complete
uploads, avoiding a required wakeup per ordered chunk. This still needs device
validation; server tests cannot prove iOS scheduling or throughput.
[Apple background URLSession documentation](https://developer.apple.com/documentation/foundation/downloading-files-in-the-background)
and [background scheduling guidance](https://developer.apple.com/documentation/backgroundtasks/choosing-background-strategies-for-your-app).

Keep the existing ordered PATCH API as a compatible fallback. Add a negotiated
**`parts-v1`** transport for Photos and recurring file uploads: immutable,
independently addressed parts, accepted out of order, default part size 16 MiB,
streaming writes, per-part hashes and final full-component hash verification.
Expose bounded missing-part pages; never return a list proportional to a huge ISO
in every progress response. Retries of an accepted matching part succeed; different
bytes at the same committed index return 409. Wrong lengths/hashes never advance
accepted progress. Empty files finalize with zero data parts.

At creation, `commit_when_complete: true` records authorization and intent for
server-side finalization. Once every component is verified, persist an outbox
job and return promptly. The job rechecks current authorization, destination and
vault state before publishing. Both still/motion components publish as one logical
asset. `accepted`, `verifying`, `stored`, preview-processing and organization-sync
are distinct states. A successful part upload is not a durable-original receipt.

Build this as another input transport into existing commit/receipt/storage logic,
not a second vault or dedupe engine. Reuse `cryptox` authenticated streaming files
for staging. Never concatenate an ISO into memory or copy it to `/tmp`. All
staging belongs on the configured data volume with quota reservations covering
actual staging, encryption overhead and commit workspace. Don't promise dedupe
savings until physical storage commits. Start conservatively on the 2-CPU/4-GiB
profile and honor effective container limits on larger hosts.

The existing raw staging format is an implementation fact, not encrypted storage.
New mobile staging and private manifests must be encrypted. Share the new staging
abstraction with new legacy-PATCH sessions as practical; migrate in-flight old
sessions by bounded streaming conversion while unlocked, or retain a read/resume
adapter until drained. Never delete uncommitted old parts as a migration shortcut.

Keep the 24-hour **inactivity** timeout for unfinished sessions. Verified incoming
parts refresh activity; an active leased upload/commit cannot be swept. Persist
durable receipts with their source mappings, independent of temporary payload
cleanup. Add fault recovery at part-write, manifest-write, storage-commit,
catalog-publication and receipt-write boundaries.

## API additions — implemented contract and documented exceptions

The table retains the original targets for traceability. The implemented shapes
and explicit unimplemented-route inventory are in [mobile-api.md](docs/mobile-api.md)
and [mobile-api.yaml](docs/mobile-api.yaml). Existing browser/Photos routes remain.
Backup parts use `/components/original/parts/{index}`, matching Photos; the shorter
`/parts/{index}` target below is not an implemented alias. Generic `/api/uploads`
remain bearer-denied to prevent coordinator scope bypass; use native parts or
the explicitly granted Library routes. `/receipt` aliases are also unimplemented:
logical upload status exposes stored results. Backup source updates change status,
not destination. Delegated legacy handlers do not yet uniformly emit `retryable`
or `Retry-After`; clients must use the documented status/code contract.

| Route / extension | Purpose |
| --- | --- |
| Minimal discovery + `GET /api/v1/mobile/capabilities` | Stable instance ID, contract version, owner/device IDs after auth, grants, vault state, features, limits, staging expiry and supported transport/media modes; no private catalog enumeration while locked |
| Extend `POST /api/v1/devices` | Optional requested scopes/profile; legacy body keeps legacy privileges |
| `POST /api/v1/devices/{id}/rotate` | Durable same-device credential rotation with expected generation and client-prepared replacement secret |
| `POST /api/v1/devices/{id}/reauthorize` | Owner cookie-only reauthorization of an existing identity; never bearer self-resurrection |
| Existing Library/upload/quota/capsule routes | Carefully allow scoped native credentials through the same owner-checked services |
| `GET/POST /api/v1/photos/collections` | Bounded collection-folder tree plus album parent fields and revision-checked edits |
| `POST /api/v1/photos/source-collections` | Bounded, idempotent source folder/album upserts with per-operation outcomes and source mappings |
| `POST /api/v1/photos/source-memberships` | Retry-safe bounded membership additions/reconciliation after originals are stored |
| Extend `/api/v1/photos/uploads` | Negotiated transport, auto-commit intent, atomic initial Hidden state, source-key mapping and byte-preserving original mode |
| `GET/POST /api/v1/backups/sources`; `PATCH /{id}` under that prefix | Register/list/update/detach own-device recurring sources; detach never deletes originals |
| `POST /api/v1/backups/uploads`; `GET/DELETE /{id}`; `POST /{id}/finalize` | Durable file/folder source operation, status/receipt and manual compatibility finalization |
| `PUT /api/v1/photos/uploads/{id}/components/{component}/parts/{index}` | Independent photo parts; receipt/status exposes paged missing parts |
| `PUT /api/v1/backups/uploads/{id}/parts/{index}` | Same transport for ordinary files |
| `GET /api/v1/files/{id}` and `/content` | Stable-ID metadata and bounded authenticated Range/HEAD reads with revision preconditions |
| `GET /api/v1/files/sync` and `POST /api/v1/files/sync/checkpoint` | Bounded file/folder snapshot, tombstones and revision changes for offline manifests |
| Grab create idempotency extension | Retried native mint resolves to the same owner operation/capsule; no duplicate sealing or retry consumption |

Use one documented machine-readable error vocabulary, with legacy `error` text
retained where existing clients expect it: authentication required, insufficient
scope, vault locked, reauthorization required, stale revision, idempotency conflict,
offset/part conflict, incomplete upload, checksum mismatch, staging expired,
storage full, resync required and retryable service unavailable. Do not change
existing lock HTTP statuses casually; add codes for native disambiguation.
Foreign-owner resource IDs must not disclose existence. Honor `Retry-After` for
rate/resource backoff and publish retryable versus user-action-required outcomes.

## Execution order and deliverables

Dependency order: **MS0 → MS1 → MS2 → MS3 → MS4 → MS5 → MS6 → MS7 → MS8**.
No phase is complete just because its route compiles. Smoke its failure cases
before closing release gates and write a short resume note. Implementation boxes
are reconciled below; final integrated verification is tracked separately.

### MS0 — Freeze contracts and establish the local baseline

Deliver: `docs/mobile-server-decisions.md`, `docs/mobile-api.md`,
`docs/mobile-api.yaml`, and `docs/mobile-server-validation-2026-10-02.md`.

- [x] Record HEAD, worktree, runtime/dependency versions, storage profiles and local fixture commands.
- [x] Map every app feature and B1–B5 to an existing route or explicit new contract; distinguish server work from iOS-only work.
- [x] Define scope/method matrix, new DTOs, capability flags, error codes, idempotency keys, pagination and validation limits.
- [x] Add example request/response fixtures usable by the Swift core; label new examples proposed until their implementation tests pass.
- [x] Reuse small local fixtures on both `restic` and `shared-experimental` configurations. Include two owners, two devices for one owner, locked/disabled accounts and a cookie-only admin.
- [x] Capture current account, Photos upload/sync and Library smoke results; record pre-existing failures separately.

Evidence: contract/DTO/error examples and source-key vector are in the API docs
and app handoff; owner/device/lock fixtures are distributed across users, desk,
backup, Files and lifecycle tests. The ledger separates earlier browser/Photos
passes from the still-pending final-image smoke.

Gate: Sol has reproducible local baselines and exact contracts. No production
data or 500-file upload exercise is needed.

### MS1 — Scoped device credentials, renewal and discovery

Primary code: `internal/users/`, `internal/desk/devices.go`, `multi.go`,
owner guards, node persistence, and small new auth/capability modules.

- [x] Persist stable device identity separately from credential generations/revocation, with an upgrade path for legacy device records.
- [x] Implement explicit route+method grants and legacy Photos-only compatibility; preserve bearer-with-cookie fail-closed behavior.
- [x] Add scoped enrollment, self-status, rotation, owner reauthorization, revoke and minimal mobile profile/status/capabilities.
- [x] Keep `instance_id` stable through restart; document clone/reset and trusted home/remote alias handling.
- [x] Recheck lifecycle and credentials during long upload/job completion, not just at initial creation. Revocation stops future admission/publication for that device.
- [x] Bind upload/part/receipt access to owner, device and operation kind. Generic upload routes must not expose or mutate a Photos/backup coordinator's internal sessions as a scope bypass; preserve explicit cookie-owner management where intended.
- [x] Ensure no bearer grant reaches admin/users/node/password/rekey/unlock/enrollment accidentally; no token/secret in list/status/error output.

Evidence: `internal/users/device_*`, `mobile_identity.go` and desk identity
modules implement these items. Injected-clock users tests and actual router
authentication/scope tests passed; async jobs persist grants and guard final CAS.
Generic uploads are deliberately denied to bearer credentials.

Tests: old tokens stay Photos-only; requested/granted scopes persist after restart;
GET permission never implies POST/DELETE; foreign device IDs are rejected;
rotation lost response, concurrent rotation, previous-token grace expiry and
replay; password change/revoke/disable invalidate all generations; owner cookie
reauthorizes the same identity; original receipt remains findable afterward.

Gate: native credentials can perform their granted Files/Photos/grab operations
without a live browser cookie. Authentication does not unlock the vault.

### MS2 — Versioned catalog support for album folders

Primary code: `internal/catalog/catalog.go`, `albums.go`, `journal.go`,
`internal/library/photo_album*.go`, `internal/desk/photo_album*.go`.

- [x] Introduce versioned encrypted collection folders, album parent fields and source mappings with atomic persistence.
- [x] Keep virtual collection parents separate from Library paths and media Hidden ancestry.
- [x] Validate same-owner parent references, folder type, no cycles/self-parenting, bounded depth and bounded request sizes. Default maximum nesting depth: 64.
- [x] Add revision-checked create/rename/reparent/order operations. Preserve missing fields on old browser writes.
- [x] Migrate old custom albums to root without ID churn, membership loss or changes to originals/Takeout paths.
- [x] Journal folder/parent/mapping changes atomically and teach snapshots/deltas about new node types. Old clients retain their legacy compatible view; new clients negotiate the extended sync schema.
- [x] Use empty-folder delete only unless an explicit later operation defines recursive collection removal; deleting a collection/album must never delete photos implicitly.

Evidence: catalog collections/schema/persistence, Library projection and desk
collection handlers implement these items. Collection tests cover nesting,
legacy fields, depth, rollback and paging; schema-2 sync is separate from the
legacy Photos view. See the freeze storage-suite evidence in the ledger.

Tests: empty folders/albums; duplicate titles; album in two nested folders via
move, not cloning; stale revisions; parent cycle/cross-owner rejection; restart;
old browser rename preserves parent; failed persistence leaves no half-move.

Gate: a saved nested album tree survives reload and appears through bounded APIs;
legacy albums and web mutations still work.

### MS3 — Preserve phone organization, originals and privacy on ingestion

Primary code: `internal/photoingest/`, catalog photo ingest, library collection
projection/sync, and small source-reconciliation handlers.

- [x] Add paged, idempotent source-folder/album import and stable mapping lookup. Topologically apply parents before children; return per-operation applied/conflict/invalid/dependency-blocked outcomes.
- [x] Keep independent valid operations moving when one source operation fails. Retry only failed operations; never infer deletes from a partial scan.
- [x] Reconcile memberships separately from immutable media upload receipts. Album changes must not force reupload or trigger same-identity/different-content errors.
- [x] Reuse media asset IDs across multiple albums; handle albums larger than the 200-ID summary through membership pages.
- [x] Preserve source renames/moves by identity, with server revision checks; retain source-deleted collections by default and never erase media.
- [x] Document/test opaque PhotoKit-style source IDs and shared safe-key derivation vectors without placing raw IDs into filesystem paths.
- [x] Add atomic initial Hidden handling for original/motion pairs. Hidden media must never briefly appear in the normal timeline or a visible cover while a later mutation catches up.
- [x] Support preservation of DNG and other original resources the app can deliver. Separate verified stored bytes from preview support: size/hash verification is mandatory, a working image decoder is not.
- [x] Add an explicit negotiated opaque-original fallback for unrecognized photo/video resources, recording unverified/unsupported format honestly. Never claim a sniffed type when unknown; never run unsupported bytes through an unsafe preview path.
- [x] Keep known-format mismatch/corruption outcomes explicit. An opaque-original receipt cannot silently become decoder-verified merely because its filename has a familiar extension.
- [x] Ensure such originals retain Photos identity, capture provenance and memberships, and appear with a placeholder rather than disappearing due to extension-based Photos filtering.
- [x] Preserve capture offsets/unknown dates and user corrections. A preview failure cannot change a stored-original receipt to failure.

Evidence: source import/membership mappings, original negotiation and guarded
photo commit paths are implemented. Named fixtures include 201-member/Hidden
paging, source-key framing, opaque/DNG originals, shared parts and receipt
recovery. This is server preservation support, not PhotoKit delivery evidence.

Tests: one original in two nested albums; rename/title collision; empty tree nodes;
lost import response; album membership changes after media receipt; 201-member
synthetic membership paging; Hidden still/motion pair; DNG/unsupported preview
beside normal JPEG; unknown offset; corrupt item beside valid upload.

Gate: server-side albums/folders and durable-original receipts independently
describe complete or partial backup. No flattening or duplicate original per album.

### MS4 — Durable recurring file/folder backup coordinator

Primary code: new small `internal/backup/` modules, desk backup handlers,
`internal/upload/`, catalog/filesvc commit interfaces and lifecycle integration.

- [x] Register own-device sources and destination IDs; persist private source/item mappings encrypted under the owner vault.
- [x] Implement idempotent source-revision operations for files, source renames and empty folders. Validate paths, lengths, traversal and parent ownership.
- [x] Stream through existing storage/dedupe and quota admission. Add a compare-and-swap commit interface if needed; do not check a revision and then perform an unguarded overwrite later.
- [x] Persist final source receipt/mapping with catalog publication, or use a recoverable commit journal that proves whether the publish already happened.
- [x] Make retries after commit but before response return the same result; a conflict must not rewrite another source/user's entry.
- [x] Add source pause/detach/status. Removal on the phone or detaching a source never sends a Library delete.
- [x] Account for user trash/restore and server edits before resuming old operations; require explicit conflict resolution instead of resurrecting deleted assets.
- [x] Integrate owner drain, lock, password/rekey, device revoke, account disable/delete, quota release and orphan cleanup.

Evidence: `internal/backup`, catalog backup CAS, encrypted intents and recovery
implement these items; fixture tests cover replacement, rename, zero-byte/empty
nodes, conflicts and receipt loss. Destination registration requires an existing
folder ID; updates currently support pause/detach/status, not retargeting.
Owner cleanup drains jobs and propagates staging-release errors.

Tests: new/changed/renamed source file; zero-byte file; empty nested folder;
same filename in two sources; foreign source/destination; changed destination
revision; missing/deleted parent; disk-full; lost finalize response; source
deletion preserves stored bytes; device credential rotates without a new mapping.

Gate: recurring source retries and revisions work after a server restart with no
silent overwrite or duplicate import. Large inputs use bounded readers.

### MS5 — Encrypted independent parts and autonomous finalize

Primary code: `internal/upload/`, `internal/cryptox/streamfile_*.go`, photo/backup
coordinators, filesvc work queues, quota and maintenance/lifecycle handlers.

- [x] Implement negotiated `parts-v1` without changing legacy ordered-PATCH semantics.
- [x] Validate fixed index-to-offset/length arithmetic with overflow checks; last part may be shorter; session creation fixes component size/hash/part size.
- [x] Accept independent parts in any order; stream authenticated encrypted staging, verify checksums and publish part acceptance only after durable persistence.
- [x] Make matching repeats idempotent, reject mismatched repeats, and return bounded received/missing-part pages and verified-byte totals.
- [x] Add durable auto-finalize intent/outbox with bounded workers, per-owner fairness and foreground headroom. Release request leases after enqueueing; worker leases participate in shutdown/revoke/lock.
- [x] Stream component reconstruction/hash checking directly into existing commit paths. No whole-file RAM allocation or plaintext assembled copy.
- [x] Keep logical pair commits atomic; recheck destination/source revisions and device authorization at publication. Locked/revoked work pauses or is denied truthfully.
- [x] Enforce inactivity expiry without sweeping active operations; free temporary payload only after receipt recovery is safe.
- [x] Upgrade staging readers/writers with format detection and safe conversion/drain for existing sessions. Private manifests must not leak paths, names, source IDs or hashes into plaintext sidecars.
- [x] Expose actual resource/admission limits through capabilities. Preserve full-disk reservations across restart and count encryption/workspace overhead.

Evidence: `internal/mobileparts`, encrypted ordered format-2 staging, guarded
coordinators and app `RunUploads` wiring implement the transport. Actual app
autonomous-finalization/shutdown tests passed. Named HTTP reauthorization and
cancellation tests above passed; queue/restore isolation fixes require final
image verification. Worker count is `min(2,max(1,GOMAXPROCS/2))`, one per owner;
bounded code and configured limits do not establish measured RSS/throughput.

Tests: out-of-order parts, concurrent duplicate index, wrong size/hash, truncated
request, disconnect after acknowledgement, restart mid-conversion, restart between
storage/catalog/receipt, final part causing auto-finalize without any client poll,
pair with missing motion, revoke/lock racing publication, active-session expiry
race, disk-full during encrypted write, and an ISO-sized generated stream with
bounded RSS. Use small actual data and a larger locally generated stream for the
memory measurement; no large production upload or claimed wire-speed guarantee.

Gate: accepted parts recover after restart; auto-finalize works with no browser
or client follow-up; original bytes are verified, encrypted at rest and not
reported stored until committed. Real iOS behavior remains a separate device gate.

### MS6 — Offline contracts and reliable native grabs

Primary code: catalog journal, Library listing/read/range services,
Photos sync/selection, capsule/archive handlers and mobile capabilities.

- [x] Expose stable file/folder IDs and revisions for native reads; implement metadata/content lookup by ID, HEAD/single Range and owner-scoped ETag/If-Range behavior.
- [x] Prevent a range resume from mixing revisions or following a reused old path. Validate content authorization before headers/bytes; avoid raw global object hashes as public cache identities.
- [x] Add bounded Files snapshot/delta/checkpoints by reusing the encrypted journal. Capture a baseline checkpoint before snapshot and replay subsequent changes; handle restored/expired checkpoints with `resync_required`.
- [x] Extend Photos sync to collection parents/source mappings with visibility filtering, compatible cursor versions and explicit Hidden checkpoints. Do not make old clients misinterpret new record kinds silently.
- [x] Document offline pin reconciliation for folder/album membership, tombstones, revisions, access loss and preview identity. Keep Cache-Control private/no-store; offline copies are explicit app-managed downloads.
- [x] Make file/folder/gallery grab creation retry-safe with durable operation identity, job status and result lookup. Same key/different spec conflicts; a lost reply doesn't create another capsule or consume a retry.
- [x] Seal complete selections, not an album's first summary page. Scope generic grab listing/revoke without opening unrelated APIs; keep sealed copies and retry accounting unchanged.
- [x] Preserve missing-admin-hostname errors, returned canonical grab URLs, Hidden confirmation, frozen-content semantics, 90-minute ordinary ZIP expiry and capsule retention.
- [x] Add capability flags only when each implementation is usable; absent/false flags give older clients a documented fallback.

Evidence: native Files Range/sync tests and source-backed idempotent capsule
operations cover the contracts. Complete-album/Hidden fixtures and final grant
publication tests exist; focused Files and publication race evidence is recorded
in the ledger/checkpoint. Offline pin storage/eviction remains app-owned.

Tests: rename during download; old ETag with new revision; lock/revoke mid-read;
file tombstone; expired/branched cursor; empty snapshot; folder move; Hidden cover
and membership filtering; whole-album grab over a page boundary; duplicate mint
after restart; cancelled mint; burn/revoke; foreign owner and insufficient scope.

Gate: native clients can reconcile offline manifests and create/retry shares with
stable identities. Document that already downloaded offline bytes cannot be
remotely recalled while a device is disconnected.

### MS7 — Migration, compatibility and failure testing

Deliver: repeatable small migration fixture and
`docs/mobile-server-migration.md` with exact paths/formats/commands discovered
during implementation, not placeholders presented as executed commands.

- [x] Implement supported schema versions for user/device metadata, encrypted catalog/collections, backup receipts, staging and job records. New readers reject unknown future formats without rewriting them.
- [x] Support legacy-device grant projections and persisted explicit legacy semantics with unchanged device IDs/token hashes/expiry; no privilege expansion or forced fresh backup identity.
- [x] Upgrade catalogs lazily while owner-unlocked, with durable atomic writes and an encrypted recovery copy; a crash resumes safely. Never require an admin to know vault keys.
- [x] Preserve legacy photo source receipts, old sync compatibility or explicit reset, source-revision identities, timestamps, favorites, hidden flags, originals and storage references.
- [x] Complete the old-browser/WebDAV/sequential-upload/old-Photos-token/new-mobile compatibility matrix through the full suite's legacy token/editor coverage and final-image browser/Photos/container/native sequences on both backends.
- [x] Restore a consistent pre-upgrade fixture and rerun migration twice; compare IDs, counts, memberships, content hashes and logical/physical accounting.
- [x] Complete account deletion with active parts/jobs/mappings/grabs in one integrated fixture; all that owner's assets and private metadata disappear, while another owner's deduped references survive.
- [x] Exercise the reproducible fault fixtures and measure RAM/transfer/staging-disk behavior on the recommended 2-CPU/4-GiB profile; same-image native probes passed on both backends. Retain bounded worker/resource checks; no unconstrained-profile performance claim is made.

Evidence and limits: catalog migration fixtures restore encrypted legacy input
and migrate twice, preserving IDs/metadata/references/accounting; this is a
synthetic fixture, not a full production-volume restore exercise. Future-format
readers and recovery copies exist. Legacy devices receive compatible legacy-grant
projections without forced rewriting of IDs/hashes/expiry. Full `make check` and
the final two-backend smoke matrix are parent-reported passes. Physical iOS,
sustained-load and unconstrained-profile measurements are not established.

New combined fixture:
`go test -race ./internal/desk -run TestMobileOwnerDeleteDrainsPartsJobsMappingsGrabsAndPreservesSharedNeighbor -count=1`
**PASS, 7.711 s** (normal invocation without `-race`: **PASS, 4.755 s**).
`internal/desk/mobile_owner_delete_test.go` uses real shared storage, a held active
parts reader under an owner lease, a second queued native job, encrypted backup
source/item mappings, an archive job and frozen grabs for both owners. Deletion
invalidates credentials and waits for the reader to drain; owner data/grabs/jobs
disappear and quota reservations reach zero. Shared garbage collection removes
the owner-only object, while the neighbor's reference, original bytes, grab and
ability to upload survive. Shared logical/unique accounting equals exactly the
surviving original's bytes. No production state is used.

Important downgrade rule: current old binaries may ignore new JSON fields and
discard them on a later save. Merely adding `schema_version` does not teach old
binaries to refuse writes. **Do not roll an old image onto migrated live data.**
Use a forward fix or restore a complete consistent pre-upgrade data snapshot,
including user credentials, catalog/journals, shared storage/reference state,
receipts and pending staging. Document writes that would be lost by restoration.
Never mix an old catalog with newer dedupe reference metadata.

Gate: migration is repeatable and preserves data; downgrade restrictions and
recovery are explicit. There is no wipe/reimport or silent old-writer data loss.

### MS8 — Final local smoke, documentation and app-agent handoff

- [x] Run the full repository checks once after focused phase tests pass, then the relevant existing browser/container/Photos smokes and the new mobile smoke on both storage backends; final passes reported by the parent.
- [x] Add `scripts/smoke-mobile-server.py` and a documented Make target using isolated fixture accounts/volumes; it must never default to production.
- [x] Update README, OpenAPI, Photos API notes, capability examples, migration/runbook and validation evidence. Regenerate embedded desk assets through normal build targets when needed.
- [x] Update the app handoff's API map/B1–B5 status based on evidence; publish DTO/error fixtures and source-ID test vectors for the Swift agent.
- [x] Record the tested local image identity, source baseline/dirty-tree status, observed resource measurements, remaining Mac/device checks and next release task. The parent records the exact release commit and deployment after execution; the local image ID is not a release commit.
- [x] Leave a deployment runbook: confirm active workloads, back up consistently, inspect disk headroom, deploy the candidate when authorized, preserve mounts/settings, unlock as owner when needed, verify jobs/collections/grabs, and follow the supported recovery path.

Evidence: the isolated `smoke-mobile` script/Make target, README/API/Photos notes,
migration runbook, B1–B5 app map, DTO/error examples and source-key vector exist.
Final full checks/build and the rebuilt image smoke matrix now pass as recorded
in the checkpoint/ledger. The supplemental integrated deletion test also passes
with `-race`; it adds test coverage without changing the frozen application code.
The candidate artifact and RAM/disk measurements are recorded above and linked
to the validation ledger. All server checklist items are closed. Exact release
commit/push and deployment records are reserved for the parent after execution;
no production snapshot or rollout result is claimed by this frozen workbook.

Gate: Sol can truthfully report **server additions complete and locally verified**.
Do not report **iOS backup proven** until the physical-device matrix has passed.
Deployment and publication follow the authorization at execution time, not this
workbook's existence.

## Commands and evidence

Use the repository's existing build prerequisites (Go 1.25, Node, pinned Restic,
native preview dependencies). Record missing prerequisites or baseline failures;
don't claim success from skipped checks. Start with focused tests for changed
packages, extending the list for any new packages:

```bash
make desk-assets
go test ./internal/users ./internal/catalog ./internal/upload ./internal/photoingest ./internal/library ./internal/desk ./internal/filesvc ./internal/accountlifecycle
```

After all phases, follow the existing CI path:

```bash
make check
make build VERSION=mobile-server-local
make smoke-browser
make smoke-container
make smoke-photos
```

`smoke-photos` requires the documented Python/Playwright/Chromium setup and the
local smoke image; inspect the Makefile for `PHOTOS_PYTHON` and fixture limits.
Add the new mobile smoke to CI once deterministic. Existing smokes use isolated
data and both backends; preserve that isolation. Do not add sleeps or time-sensitive
tests for token expiry: inject the clock. Do not repeatedly rerun the entire suite
after it passes unless code changes or failures justify it.

Required mobile end-to-end sequence: owner sign-in and unlock → enroll scoped
device → import nested/empty albums → upload still/motion plus unsupported original →
register file source → send reordered/retried parts → auto-finalize without client
polling → restart → rotate credential with lost response → recover same receipts
→ edit source file with revision guard → reconcile Files/Photos offline manifests
→ create file/folder/whole-album grabs → revoke → verify other owner isolation.

For each phase record: command, fixture/backend, pass/fail, key assertions,
measured limits, and pending platform work. A token in a test report is a defect;
use generated fixtures and redact secrets. A 401/409/507 expected by a test must
not be counted as an unexpected service failure.

## Physical iOS gate owned jointly with the app agent

The Go work cannot prove PhotoKit access, iCloud retrieval, security-scoped folder
bookmarks, Share extension handoff, Face ID, or system background scheduling.
Once the app has real adapters, test full/limited access, nested source albums,
cloud-only originals, Live Photos/DNG, folder-provider loss, locked phone/reboot,
system termination/force-quit/reopen, delayed task admission, Wi-Fi/cellular and
home/remote changes, limited phone disk and credential rotation with queued tasks.
Measure wakeups, completion time, bytes retransmitted and peak staging space.
Keep the legacy transport fallback until the new path has device evidence.

## Resume prompt for Sol

> Read this checkpoint, the validation ledger and the app BUILD_PLAN decision
> table. Preserve the implemented shared worktree; close remaining MS7 evidence
> and MS8 final checks using the fixed candidate tree/image. Record actual checks,
> resource measurements and failures. Commit/push and production deployment are
> already user-authorized, but have not been recorded as performed here; follow
> the reviewed consistent-snapshot migration runbook and preserve custom settings.
> Keep physical iOS verification separate and retain ordered-PATCH fallback until
> device evidence supports removal. Do not reimplement completed server features.
