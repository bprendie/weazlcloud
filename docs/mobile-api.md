# Native mobile API — implementation contract

Photo upload preflight: [SHA-256 and size lookup](photo-content-lookup.md).

Updated October 9, 2026. This documents the implemented mobile contract and the
concurrent finalization update. See [validation](mobile-server-validation-2026-10-02.md) and
[decisions/migration](mobile-server-decisions.md). The machine-readable companion
is [mobile-api.yaml](mobile-api.yaml); its paths include implemented handlers, with
grant-only/pending patterns isolated in `x-unimplemented-route-inventory`; existing Photos DTOs remain documented in
[photo-api.md](photo-api.md) and [photo-api.yaml](photo-api.yaml).

## Authentication, admission and errors

Use `Authorization: Bearer <device-secret>` over an explicitly approved TLS origin.
The secret is a 64-character hexadecimal encoding of 32 random bytes. Authorization
header presence prevents cookie fallback, including an invalid or expired token.
Device tokens contain no vault key and do not unlock private data. After a
server restart, an owner must sign in and unlock the vault again before private
reads, ingestion and queued finalizers resume. App UI/Face ID lock is separate
from this server-vault state. Scopes are checked by method and exact route shape; a route grant does not bypass
owner, device, resource-kind, vault or lifecycle checks. Account/admin privileges
never grant another owner's vault. Supply `X-Weazl-Desk: 1` on mutations; when an
Origin header is present, its host must match the request host. JSON bodies use
`Content-Type: application/json`.

Bearer scopes are additive only when explicitly enrolled. `backup:write` requires
`files:write`; `files:write` does not imply `files:read`. Omitted device `scopes`
retains legacy `photos:v1`; an explicit `[]` grants no resource scopes. Intrinsic
self-status/rotation/revoke and narrow mobile discovery projections do not require
resource scopes. Legacy devices gain no Files, backup, parts or generic grab rights.
Do not send bearer credentials to enrollment, reauthorization, login/unlock,
password/rekey, administration, node settings, takeout or generic `/api/uploads`.

New native handlers emit `Cache-Control: private, no-store`. Some older Photos
handlers still emit `no-store` alone. These responses are not permission for an
HTTP cache to store private bytes; offline downloads are explicit app-managed data.
Native errors commonly have `{ "error": "...", "code": "..." }`; codes and
`retryable` are not yet uniform across older delegated handlers. Parse the status
first. 401 means authentication or vault lock, 403 insufficient scope/admission,
404 unavailable/foreign resource, 409 idempotency/CAS conflict or expired sync,
416 invalid range, 410 expired parts staging, 422 part checksum mismatch, 423 parts vault-lock
errors, and 507 storage admission failure. Some early vault checks use 401 instead. A Files 409 with
`code: resync_required` and `resync_required: true` requires a fresh snapshot.
After content headers are sent, a mid-read failure truncates the stream rather
than appending JSON; never mark such a download complete.

## Contract-version negotiation

For `/api/v1/*` and `/.well-known/weazlcloud`, send either
`X-Weazl-Mobile-Contract: 1` or `?contract_version=1`. Both may be present with
value `1`; omission preserves legacy requests. Empty/future/duplicate values or
a header/query mismatch return 400 `{error,code:"unsupported_contract_version",
supported_contract_versions:[1]}` before native dispatch. Never silently retry a
future contract as legacy mutation semantics. This wire version is independent
of user/catalog schema 2. Ordinary non-v1 browser routes retain their own behavior.

## Instance identity and capability discovery

`GET /.well-known/weazlcloud` is public and returns only `instance_id`,
`contract_version: 1`, and `capabilities_path: /api/v1/mobile/capabilities`.
The ID is a random 32-hex identifier in `instance_id` beside the account store.
It survives restart and whole-volume restoration. An independent cloned node
must deliberately generate a new identity while stopped (see migration notes).
Matching IDs help validate home/remote aliases but do not replace TLS validation
or explicit client approval of connection origins. Use the server-returned grab
URL; do not construct it from the connection address.

`GET /api/v1/mobile/{capabilities|profile|status}` currently returns the same
capability projection: instance/contract/owner IDs, profile username/full name,
`vault_unlocked`, and bearer `device_id`, `generation`, `grants`, `expires_at`.
Cookie requests instead include `authentication: account_session`.
At final handler inspection, `features` advertises `scoped_devices`,
`credential_rotation`, `photo_collections`, `source_collections`, `source_memberships`,
`file_reads`, `files_sync`, `recurring_backups`, and `idempotent_grabs` as true;
`parts_v1` and `auto_finalize` reflect whether the parts manager is configured.
`transports` advertises `ordered-patch-v1` and `parts-v1`. These are actual emitted
flags; verification results are recorded in the release ledger.
Clients must inspect the server response rather than assume flags from this document.
Limits are active devices 32, credential TTL 7,776,000 seconds, rotation grace 900
seconds, staging expiry 86,400 seconds, ordered chunk/part size 16,777,216 bytes,
missing parts page 200. Finalization uses a persistent, fair worker pool.
With P equal to visible CPU capacity (including GOMAXPROCS and cgroup limits)
and M equal to `max(1,min(8,visible_RAM/2_GiB))`, defaults are
`mobile_finalize_workers = max(1,min(M,P/2))` and
`mobile_finalize_workers_per_owner = max(1,min(4,M,P/4))`.
Integer division rounds down. Two-CPU hosts use one worker; large hosts can use
eight globally and four per owner. Explicit operator overrides are described in
[mobile upload performance](mobile-upload-performance-2026-10-09.md).

## Durable devices

`GET /api/v1/devices` returns `{devices:[...]}`; bearer sees only itself, cookie
sees the owner's devices. `GET /api/v1/devices/{id}` returns `{device,grants}` and
bearer ID must be its own. Public device records omit token hashes and rotation
secrets; retain stable `id`, `owner_id`, name, scopes, generation,
`authorization_version`, revoked state, creation and expiry dates.

`POST /api/v1/devices` is owner-cookie-only and requires an unlocked vault.
Body `{name, scopes?}` returns 201 `{device,token,scopes}`; legacy enrollment also
returns `scope: photos:v1`. Store the returned token securely; it is not retrievable.

`POST /api/v1/devices/{id}/rotate` is bearer-self-only. Body:

```json
{"operation_id":"rotation-2026-10-02","expected_generation":1,"replacement_token":"<client-prepared 64 hex characters>"}
```

Persist the replacement secret before sending. Success returns `{device,generation}`.
Repeating the same operation/expected generation/replacement returns the committed
generation; a competing or changed rotation is 409. The previous credential has
at most 15 minutes of grace and cannot start another rotation. A new generation
expires in 90 days. Expiry, password change, disable, delete or revoke do not let
a credential resurrect itself.

`POST /api/v1/devices/{id}/reauthorize` requires owner cookie and unlocked vault:
`{replacement_token,scopes?}` preserves identity and returns `{device,grants}`.
Omitted scopes preserve prior grants. `POST /api/v1/devices/{id}/revoke` or legacy
`POST /api/v1/devices/revoke` with `{id}` revokes the stable identity; bearer can
revoke itself only. Retained inactive identities do not occupy all active slots.

## Independent parts and server jobs

The `mobile_parts*.go` handlers implement the transport below. Frozen-image
server verification is recorded in the [release ledger](mobile-server-validation-2026-10-02.md).

Create a logical upload at `POST /api/v1/photos/uploads` or
`POST /api/v1/backups/uploads` with `transport: parts-v1`. Set
`commit_when_complete: true` when requesting autonomous finalization. The Photos
coordinator uses `upload.id`; the backup coordinator uses `id == receipt_id`.
The parts session uses that exact logical ID, so clients must not replace it with
an underlying legacy upload ID. File backup's coordinator Spec lacks
`commit_when_complete`; the HTTP adapter consumes that top-level field separately.

The transport routes and the separately pending receipt alias are:

| Route | Required bearer scopes |
| --- | --- |
| `PUT /api/v1/photos/uploads/{id}/components/{original|motion}/parts/{index}` | `photos:write` |
| `PUT /api/v1/backups/uploads/{id}/components/original/parts/{index}` | `backup:write` + `files:write` |
| `GET /api/v1/{photos|backups}/uploads/{id}` (status); `/receipt` pending | Photos: `photos:write`; backup: both scopes above |
| `GET /api/v1/{photos|backups}/uploads/{id}/parts?component=&cursor=&limit=` | Same respective upload scopes |
| `POST /api/v1/{photos|backups}/uploads/{id}/retry` | Same respective upload scopes |
| `POST /api/v1/{photos|backups}/uploads/{id}/finalize` | Same respective upload scopes |

Use zero-based index `i`, offset `i * 16777216`, and length
`min(16777216, component_size - offset)`. Every non-final part is exactly 16 MiB;
the last is exact remaining length. Send a file-backed binary body, exact
`Content-Length`, and `X-Weazl-SHA256` containing its 64-hex SHA-256. Chunked or
unknown-length transfer is not this contract. Parts may arrive out of order.
Wrong index, length, truncated body or checksum cannot advance verified counters.
A matching committed index/hash/length is idempotent; changing an accepted part
is a conflict. Full-component verification still runs before storage publication.
Zero-byte ordinary files have no data parts; Photos components must be nonempty.

Engine progress DTO: `{id,transport,status,components,part_size,error_code?,result?,
updated_at,expires_at}`. Components have `{id,size,sha256,received_parts,received_bytes}`.
Missing pages have `{missing:[indices],next,has_more}`; continue with `cursor=next`
even when `missing` is empty and `has_more` is true. Engine limits are 200 returned
missing indices and at most 200 scanned indices per page. HTTP defaults are component `original`, cursor `0`, limit `200` (accepted range
1–200). Create returns 201 `{receipt,transfer}`; a stored create replay returns
200 `{receipt}`; empty-folder backup create commits synchronously and returns
201 `{receipt}` without a transfer. GET status, successful PUT, retry and finalize
return 200 `{transfer}`; stored finalize is idempotent. Native parts DELETE returns 200
`{status:"cancelled",transfer}` after durable coordinator and engine cancellation.
Cancellation is terminal: repeating create for the same cancelled intent returns
404; use a new `source_revision` for a new logical intent. If cancellation discovers
an original already published after a lost receipt, it recovers `transfer.status`
`stored` and the coordinator receipt in `transfer.result`, returning 409
`{code:"already_stored",transfer}`. It never deletes that stored original. POST retry/finalize queues complete non-stored work and
returns promptly; polling GET status does not itself perform finalization.
The `/receipt` suffix is not yet handled/granted: use GET the logical upload ID,
whose stored `transfer.result` is the coordinator receipt. OpenAPI keeps that
requested alias in its unimplemented inventory, outside generated paths.

Engine states are `uploading`, `queued`, `verifying`, `stored`, `failed`, `cancelled`.
An accepted part or queued job is not a stored-original receipt. `stored` means
component verification and coordinator/catalog commit succeeded, with the logical
result in `result`. Preview processing and organization imports are separate.
Queued/verifying transfer responses include `Retry-After: 2` as a polling hint.
Clients may upload the next asset after accepted parts; they must still wait for
`stored` before marking that original backed up. A transient batch writer failure
retries from retained encrypted parts after 2, 4 and 8 seconds, at most three
automatic retries. Retries isolate files into individual writes; persistent
failures remain inspectable and explicitly retryable. Session IDs and results
retain the existing format, including uploads staged before this upgrade.
Retry queues a complete non-stored/non-cancelled session; incomplete retry is a
conflict. Finalize must use the immutable create specification rather than change
capture/Hidden/destination fields halfway through parts. Server work must recheck
current authorization and owner/vault/CAS state before publication.

Unfinished staging has a 24-hour inactivity lifetime. Newly verified parts and
persisted state transitions refresh activity; a matching duplicate does not
currently save the engine manifest. Recovery reconciliation persists counters without extending the accepted-part
activity clock. Status polling does not extend the inactivity deadline. Active engine
processing is protected from its sweeper. Payload is encrypted in `.wza` files,
private manifests/part receipts are vault-wrapped, and final reconstruction streams
without a plaintext assembled copy. Durable logical receipts outlive payload
cleanup. `Handler.RunUploads` runs the parts worker alongside the legacy upload runner;
queued/verifying work is discovered through durable `.queue` markers for unlocked
owners. Empty `.queue`/`.live` marker files contain opaque job IDs, not private
payloads; the index uses schema-1 recovery (`.indexed-v1`). Each Pending call reads
at most 100 queue markers plus 100 incremental recovery entries. It does not scan
all historical terminal receipts every second.
A fair owner round processes at most one job per owner and
`min(2,max(1,GOMAXPROCS/2))` globally, under
owner leases and vault cancellation. A 250 ms authorization watcher cancels long
work; the final atomic device-grant guard is the publication barrier. The worker schedules inactive-staging sweeps every 15 minutes;
each Sweep reads at most 100 live markers plus 100 incremental recovery entries. These mechanisms are inspected implementation,
with measured local resource behavior and verification scope recorded in the release ledger.

PUT append checks the admitted device grant before and after each reader Read;
vault-session cancellation interrupts streaming as well. Final publication still
uses the atomic guard. Native parts session format dispatch reports locked vaults
as 423 `vault_locked`; legacy ordered Photos GET still uses 401. Clients must not
classify a valid device credential as revoked solely because the vault is locked.

An owner may reauthorize the same stable device identity after credential expiry
or password invalidation. Repeating parts create with identical immutable photo/file
metadata and fresh authority can CAS-rebind the private authorization payload.
A still-valid admission grant is retained. An expired admission can be explicitly
readmitted by create using freshly rotated credentials even in the same authorization
epoch. Active jobs and unexpired verifying jobs conflict with 409 until the old
finalizer has cancelled/drained; stored and cancelled sessions cannot be rebound.
Stored receipt lookup/replay remains unchanged. All immutable fields, including
`commit_when_complete`, must match: changing one returns 409 without refreshing
the admitted grant.

An expired inactive stage may restart under freshly authorized identical metadata
with the same logical ID after quota admission. Restart generates a fresh encrypted
staging key, resets counters and removes expired payloads: after 24 hours of inactivity,
accepted bytes require retransmission. This does not change content identity, expected
destination revision, or any already stored original. Cancelled intents remain terminal.

Background commit failures preserve actionable `transfer.error_code` values:
`stale_revision`, `source_inactive`, `insufficient_storage`,
`device_authorization_required`, and `vault_locked`; otherwise `commit_failed`.
These persisted failure codes are distinct from the HTTP status of an accepted
queue request. Clients must resolve authorization/source/CAS/storage conditions
before retrying; an accepted queue response is not evidence of publication.

Legacy ordered PATCH remains a fallback: `Upload-Offset` plus
`Upload-Chunk-SHA256`, at most 16 MiB, in offset order. It uses different headers
from parts PUT. New owner-backed ordered sessions have encrypted format-2 WZU2
manifests and encrypted per-PATCH segments. Old format-0 raw sessions keep their
read/resume/finalize drain adapter; they are not eagerly converted or discarded.
Format is private staging metadata, not a new field in the HTTP SessionView.

## Photo identity, originals and organization

Photo create fields: `device_id`, `device_asset_id`, `source_revision` (default `1`),
`root_id` (default `root:photos`), `components`, `album_ids` (at most 100),
`captured_at`, `offset_known`, `hidden`, `original_mode`, `source_namespace`,
`source_asset_id`, `source_mapping_revision`, `transport`, `commit_when_complete`.
Components are one `original`, optionally one `motion`, each with `filename`,
`media_type?`, positive `size`, `sha256`. Original precedes motion after normalization.
Legacy single-component `{filename,size,sha256}` is accepted instead of components.
Bearer device ID is imposed by authentication and foreign IDs are rejected.
Logical receipt identity is `(device_id,device_asset_id,source_revision)` under the
owner vault. Identical normalized retries recover the receipt; changed immutable
specification conflicts. Changing album membership after storage uses the source
membership API, not a changed retry of the upload specification.

Raw source IDs are opaque metadata. When `source_namespace` and `source_asset_id`
are supplied, `device_asset_id` is derived as base64url-without-padding SHA-256 of:
`uint64be(len(namespace UTF-8)) || namespace UTF-8 || uint64be(len(raw ID UTF-8)) || raw ID UTF-8`.
A provided derived key must match; this is not a media-content hash. Legacy safe
keys are ASCII letters/digits/`-_.`, at most 100 bytes, excluding `.` and `..`.
No Unicode normalization of opaque source IDs is applied.

Source-key fixture (exact UTF-8 bytes): namespace `photokit`, raw ID `A/B+é:asset/L0/001`,
framed bytes (hex) `000000000000000870686f746f6b69740000000000000013412f422bc3a93a61737365742f4c302f303031`, derived key `KU4HSDhAwyKEJSEAwMUtG8t2dD2FFomiNQ6Bq0BRlKs`.
Clients must reproduce this vector before sending a precomputed device_asset_id.

Known media extensions include JPEG, PNG, GIF, WebP, HEIC/HEIF, AVIF, TIFF, DNG,
MP4/M4V, MOV, WebM and MKV; format checks use a bounded prefix, not a guarantee of
full decoder validity. DNG accepts TIFF framing and preserves original bytes.
`original_mode: opaque-original-v1` makes the original `application/octet-stream`,
preserves verified size/hash, and stores it under a `.opaque` suffix. It does not
claim a sniffed/decoder-verified format or a working preview. Initial `hidden`
is committed atomically with the still/motion pair. Preview failure does not undo
an already stored receipt. Unknown capture offset stays unknown; preserve
`offset_known: false` rather than inventing the phone's current timezone.

For iOS Hidden backups, set `hidden: true` on the initial upload specification
from the asset's PhotoKit hidden state. An album or folder named `Hidden` alone
does not set visibility. The same unlocked owner vault and scoped credential
authorize the explicit Hidden view; there is no separate Hidden password or
unlock endpoint. Use `mode=hidden` for the timeline and `hidden=1` for asset,
collection and membership reads that support that parameter. Normal views must
continue to exclude Hidden content. Preserve album membership and the hidden
state of both Live Photo components. See the [native handoff](mobile-app-handoff.md#ios-hidden-uploads--october-9-2026)
for client behavior and upload performance guidance.

`GET/HEAD /api/v1/photos/assets/{id}/original?hidden=1&download=1` reads the captured
immutable original; `hidden=1` selects the explicit Hidden context and `download=1`
sets attachment disposition. Normal and Hidden identities/components/covers are
filtered separately. Existing Photos ETag is `"<id>-<revision>"`; it is not the
Files owner-hashed ETag. Existing Photos conditional handling is narrower (exact
If-None-Match/If-Range), and does not promise the Files precondition-list behavior.

Virtual collections are separate from physical Library paths. Folder fields:
`id,parent_id?,title,position,revision`; album headers add `parent_id` to existing
IDs/metadata. Root parent is empty. Nesting is bounded (64 nodes including self),
parents must exist in the same owner catalog, and cycles/self-parenting fail.
Titles may repeat under distinct parents; originals are not copied per membership.

`POST /api/v1/photos/collections` uses `{action,folder?,album?,parent_id?}`;
actions are `save-folder`, `delete-folder`, `save-album`. New nodes omit ID;
updates/deletes provide current revision. Deletion requires an empty collection
and never deletes media. For `save-album`, omitted parent preserves the existing
parent; explicit top-level `parent_id: ""` moves it to root. The allowlisted
PATCH/DELETE collection-ID routes are not implemented by this dispatcher.

`GET /api/v1/photos/collections?cursor=&limit=&hidden=1` returns schema-2 bounded nodes:
`{schema,position,nodes,next?,has_more,checkpoint?}`. Snapshot `next` is an encrypted
schema-2 `photo-collections` cursor bound to the explicit Hidden context; the final checkpoint starts deltas. Snapshot position must still match
exactly; a changed catalog forces restart. Delta pages instead have
`{schema,position,changes,next_cursor?,checkpoint?,has_more}`. Changes include
`album`, `collection`, `source-mapping`; album member IDs/covers and asset mappings
are omitted. Collection organization metadata is owner-scoped; membership/media
visibility belongs to explicit Photos context. Legacy `/photos/sync` still returns
asset/folder/album records and skips these new record kinds. Do not feed a
collection cursor to legacy photo sync or infer a `schema=2` query option there.

`POST /api/v1/photos/source-collections` and `/source-memberships` accept
`{device_id?,operations:[...]}` (1–200 operations, body at most 2 MiB). Each operation
has `operation_id,namespace,source_id,source_revision,kind,parent_source_id?,title,
position,expected_revision,deleted?,add_ids?`; memberships target `kind: album`
and add at most 200 existing logical asset IDs. Results are ordered `outcomes`
with `applied`, `conflict`, `invalid`, `retryable`, or `dependency-blocked`, and
`server_id/revision` on success. Children can precede parents in the same batch.
Successful operations replay by operation ID and exact specification; failures do
not consume their ID. Server edits require fresh expected revisions. `deleted`
retains server organization and originals; incomplete scans never infer removal.

Album summary `asset_ids` is bounded and is not a full album export. Use
`GET /api/v1/photos/albums/memberships?id=&cursor=&limit=&hidden=1` to enumerate
all context-visible IDs (at most 200/page). Cursors bind the album, generation and
Hidden context; membership changes can require restart. Photo sync likewise
separates headers from membership changes, and normal/Hidden device checkpoints.

## Recurring file backups and compare-and-swap

`POST /api/v1/backups/sources` registers `{device_id?,source_id,destination_id,name}`
against an existing owner folder **stable entry ID**. Success is 201 with
`{device_id,source_id,destination_id,name,status,revision}`. Identical registration
preserves mappings; changed name/destination under the same identity conflicts.
The server does not auto-create a `/Backups/...` destination from a source label.
`GET .../sources?after=` returns at most 200 `{sources,next}` for the current device.
`PATCH .../sources/{source_id}` accepts `{status,expected_revision}` where status is
`active`, `paused`, or `detached`. Detached is terminal in this implementation;
pause/detach never deletes Library originals.

Backup create body: `{device_id?,source_id,source_item_id,source_revision,
relative_path,kind,size,sha256,mtime,expected_entry_id?,expected_revision?,transport?}`.
`filename` is a fallback when relative_path is absent. Kind defaults to `file`;
transport defaults to literal `sequential` (Photos instead uses empty transport
for legacy ordered PATCH). Relative paths disallow absolute paths, backslashes,
empty/dot/dot-dot components, NUL, leading/trailing whitespace and components over
255 bytes; total path is at most 4096 bytes. Opaque source/item/revision IDs are
valid UTF-8, nonempty, at most 1024 bytes, without NUL. Files permit zero bytes and
require SHA-256 including the empty-file digest. Folders require size zero and an
empty hash; empty folders and intermediate parents are preserved.

Receipt identity is owner-encrypted `(device,source_id,source_item_id,source_revision)`.
Same normalized specification returns the original operation/result; changed
specification is 409. Response coordinator View is
`{id,receipt_id,status,spec,upload_id?,file?}`. `id == receipt_id`; `upload_id` is only
fallback staging identity. Replacement/rename requires **both** expected entry ID
and revision from the mapped server entry. Publication CAS guards destination,
parents, old entry identity/path/revision and absence/collisions atomically.
Server moves/edits, trash/restore, moved destination or unowned children produce a
conflict; an old operation cannot resurrect a deleted original or overwrite a
new occupant. Source deletion or missing phone items never imply server delete.

Fallback routes: GET/DELETE/PATCH `.../uploads/{id}` and POST `.../{id}/finalize`;
PATCH uses ordered headers above. Parts routes use the independent transport described
above. Durable backup intent/source records live in owner-encrypted `.weazl-backups`;
recovery reconciles catalog publication with mapping/receipt writes. Private source
and operation records now carry version 1 and reject unsupported future versions;
the public backup Source/View JSON does not gain a version field.

## Stable Files reads and offline reconciliation

`GET /api/v1/files/{id}` returns `{id,revision,path,folder,size,mtime}` without
backend/global hash references. `GET/HEAD .../{id}/content` resolves by stable ID,
captures one revision/reference/retention hold under the mutation lock, then streams.
Folders do not have content; deleted/foreign IDs are unavailable even if their
old path is reused. `.weazl-*` coordinator entries are excluded.

ETag is a strong owner+stable-ID+revision hash, shared between metadata/content.
Preconditions follow If-Match (strong list or `*`), else If-Unmodified-Since;
then If-None-Match (weak list or `*`), else If-Modified-Since. Failed match is 412;
unchanged GET/HEAD is 304. GET supports one byte range including suffix/open-ended
ranges; invalid/multiple ranges are 416 with `Content-Range: bytes */<size>`.
If-Range must match the strong ETag or exact second-precision Last-Modified date;
otherwise send the full 200 representation. HEAD ignores Range and reports full
length without bytes. Clients must replace, not append, when resume returns 200.
Reauthorization is checked before headers and each output chunk; lock/revoke
can interrupt a read. Disconnected offline copies cannot be remotely recalled.

`GET /api/v1/files/sync?cursor=&prefix=&limit=&resume=1` requires a device bearer,
with limit 1–200 (default 100). Prefix matches itself/subtree; it is an exact filter
bound into the cursor. Snapshot items are stable-ID paged. Its baseline is captured
**before** enumeration; finish snapshot then replay checkpoint deltas to reconcile
concurrent inserts/moves/edits/deletes. Pages have
`{items,changes?,next_cursor?,checkpoint?,has_more,resync_required}`; changes carry
`sequence,kind,id,deleted?,item?`. Out-of-prefix moves become tombstones; ignore
unknown tombstoned IDs. A page may be empty while scan progress advances.

Cursors are owner-vault encrypted and bind device, prefix, process boot identity,
vault session, journal hash/epoch/sequence and a 24-hour token expiry. Journal
retention is 8192 changes. Altered/expired/branched/restored cursors, a different
device/filter/session, or server restart force 409 resync. Durable device checkpoint
resume can issue a new current-session cursor when its stored position remains valid.
`POST /api/v1/files/sync/checkpoint` takes `{checkpoint,prefix?,device_id?}`; only a
delta/checkpoint token can be acknowledged, and requested device must match bearer.
`resume=1` without a saved valid checkpoint is a resync conflict.

Offline pins should reconcile membership, tombstones and current revision before
refreshing downloads; remove stale manifest associations on lost access without
claiming remote deletion of device bytes. Pin a whole folder/album by exhausting
all pages, not by copying a bounded summary. Key preview/download associations by
instance/owner/stable ID/revision/context.

## Retry-safe grabs and Idempotency-Key grants

`POST /api/capsules` requires `files:read` + `grabs:write`; Photos gallery mint
`POST /api/v1/photos/grabs` requires `photos:read` + `grabs:write` for explicit tokens.
Legacy Photos gallery grants remain compatibility-specific. `GET /api/capsules`
requires `grabs:read`; DELETE requires `grabs:write`.
`GET /api/v1/grabs/operations` requires `grabs:read`, even for a key previously
minted with write scope. All are still owner/vault scoped.

Supply `Idempotency-Key` on mint and operation lookup: 1–200 bytes, no surrounding
whitespace, CR/LF or NUL. JSON object key order/whitespace is normalized; array
order and options remain significant, and route is part of the specification.
Same owner/key/spec replays the original HTTP result; changed route/spec is 409.
Lookup can return 202 `{operation_id,state:minting}` while active, or the persisted
result. It does not consume a guest grab. Interrupted unpublished mint is terminal
rather than silently selecting newer bytes. Unkeyed browser calls retain existing
behavior. Mint responses use canonical admin-configured HTTPS grab base; missing
base remains an error. These are synchronous mint/recovery operations, not a new
advertised asynchronous sharing-job platform.

Use `POST /api/v1/photos/selections` with `{filter,mode?,date?,search?}`
(or explicit `ids`) to obtain `{id,count,hidden,expires_at}` and a complete immutable
selection. Pass that ID as photo grab `selection_id` for whole-album sharing, with `hidden` and `confirm_hidden` for Hidden material. Files/folders and
galleries capture immutable references; retained frozen copies follow existing
burn/expiry/revoke semantics. Ordinary generated ZIPs retain their 90-minute ready
lifetime; that is not the upload inactivity or grab-retention lifetime.

## Exact bearer route allowlist at documentation inspection

The table below is generated from `internal/users/device_scopes.go`. “Legacy”
means a nil-scopes `photos:v1` credential is permitted independently of the listed
explicit scopes. `*` matches one nonempty path segment, not arbitrary descendants.
An allowlist entry alone is not a guarantee of a wired handler. The requested `/receipt` alias has no grant or handler at this inspection.
The old backup PUT `.../{id}/parts/{index}` and collection PATCH/DELETE-ID patterns
are grants only, without matching operations in the new dispatchers.

| Method | Route pattern | Required explicit scopes | Legacy |
| --- | --- | --- | --- |
| `GET` | `/api/v1/photos` | `photos:read` | yes |
| `GET` | `/api/v1/photos/capabilities` | `photos:read` | yes |
| `GET` | `/api/v1/photos/albums` | `photos:read` | yes |
| `GET` | `/api/v1/photos/albums/memberships` | `photos:read` | yes |
| `POST` | `/api/v1/photos/albums` | `photos:write` | yes |
| `GET` | `/api/v1/photos/search` | `photos:read` | yes |
| `GET` | `/api/v1/photos/dates` | `photos:read` | yes |
| `GET` | `/api/v1/photos/seek` | `photos:read` | yes |
| `GET` | `/api/v1/photos/sync` | `photos:read` | yes |
| `POST` | `/api/v1/photos/sync/checkpoint` | `photos:read` | yes |
| `GET` | `/api/v1/photos/assets/*` | `photos:read` | yes |
| `POST` | `/api/v1/photos/assets/*` | `photos:write` | yes |
| `GET` | `/api/v1/photos/assets/*/thumbnail` | `photos:read` | yes |
| `GET` | `/api/v1/photos/assets/*/original` | `photos:read` | yes |
| `HEAD` | `/api/v1/photos/assets/*/original` | `photos:read` | yes |
| `POST` | `/api/v1/photos/folders` | `photos:write` | yes |
| `GET` | `/api/v1/photos/trash` | `photos:read` | yes |
| `POST` | `/api/v1/photos/trash/restore` | `photos:write` | yes |
| `POST` | `/api/v1/photos/selections` | `photos:read` | yes |
| `POST` | `/api/v1/photos/selection-actions` | `photos:read` + `photos:write` | yes |
| `GET` | `/api/v1/photos/archives` | `photos:read` | yes |
| `DELETE` | `/api/v1/photos/archives` | `photos:write` | yes |
| `POST` | `/api/v1/photos/grabs` | `photos:read` + `grabs:write` | yes |
| `GET` | `/api/v1/photos/duplicates` | `photos:v1` | yes |
| `POST` | `/api/v1/photos/duplicates` | `photos:v1` | yes |
| `GET` | `/api/v1/photos/metadata-jobs` | `photos:v1` | yes |
| `POST` | `/api/v1/photos/metadata-jobs` | `photos:v1` | yes |
| `GET` | `/api/v1/photos/collections` | `photos:read` | no |
| `POST` | `/api/v1/photos/collections` | `photos:write` | no |
| `PATCH` | `/api/v1/photos/collections/*` | `photos:write` | no |
| `DELETE` | `/api/v1/photos/collections/*` | `photos:write` | no |
| `POST` | `/api/v1/photos/source-collections` | `photos:write` | no |
| `POST` | `/api/v1/photos/source-memberships` | `photos:write` | no |
| `POST` | `/api/v1/photos/uploads` | `photos:write` | yes |
| `GET` | `/api/v1/photos/uploads/*` | `photos:write` | yes |
| `GET` | `/api/v1/photos/uploads/*/parts` | `photos:write` | no |
| `POST` | `/api/v1/photos/uploads/*/retry` | `photos:write` | no |
| `DELETE` | `/api/v1/photos/uploads/*` | `photos:write` | yes |
| `PATCH` | `/api/v1/photos/uploads/*` | `photos:write` | yes |
| `POST` | `/api/v1/photos/uploads/*/finalize` | `photos:write` | yes |
| `PATCH` | `/api/v1/photos/uploads/*/components/*` | `photos:write` | yes |
| `PUT` | `/api/v1/photos/uploads/*/components/*/parts/*` | `photos:write` | no |
| `GET` | `/api/v1/files/*` | `files:read` | no |
| `GET` | `/api/v1/files/*/content` | `files:read` | no |
| `HEAD` | `/api/v1/files/*/content` | `files:read` | no |
| `POST` | `/api/v1/files/sync/checkpoint` | `files:read` | no |
| `GET` | `/api/v1/backups/sources` | `backup:write` + `files:write` | no |
| `POST` | `/api/v1/backups/sources` | `backup:write` + `files:write` | no |
| `PATCH` | `/api/v1/backups/sources/*` | `backup:write` + `files:write` | no |
| `POST` | `/api/v1/backups/uploads` | `backup:write` + `files:write` | no |
| `GET` | `/api/v1/backups/uploads/*` | `backup:write` + `files:write` | no |
| `GET` | `/api/v1/backups/uploads/*/parts` | `backup:write` + `files:write` | no |
| `POST` | `/api/v1/backups/uploads/*/retry` | `backup:write` + `files:write` | no |
| `PUT` | `/api/v1/backups/uploads/*/components/*/parts/*` | `backup:write` + `files:write` | no |
| `DELETE` | `/api/v1/backups/uploads/*` | `backup:write` + `files:write` | no |
| `POST` | `/api/v1/backups/uploads/*/finalize` | `backup:write` + `files:write` | no |
| `PATCH` | `/api/v1/backups/uploads/*` | `backup:write` + `files:write` | no |
| `PUT` | `/api/v1/backups/uploads/*/parts/*` | `backup:write` + `files:write` | no |
| `GET` | `/api/library` | `files:read` | no |
| `GET` | `/api/library/page` | `files:read` | no |
| `GET` | `/api/library/search/page` | `files:read` | no |
| `GET` | `/api/library/thumbnail` | `files:read` | no |
| `GET` | `/api/library/music` | `files:read` | no |
| `GET` | `/api/library/capability` | `files:read` | no |
| `GET` | `/api/library/events` | `files:read` | no |
| `PUT` | `/api/library` | `files:write` | no |
| `DELETE` | `/api/library` | `files:write` | no |
| `POST` | `/api/library/folder` | `files:write` | no |
| `POST` | `/api/library/rename` | `files:write` | no |
| `POST` | `/api/library/copy` | `files:read` + `files:write` | no |
| `GET` | `/api/trash` | `files:read` | no |
| `DELETE` | `/api/trash` | `files:write` | no |
| `POST` | `/api/trash/restore` | `files:write` | no |
| `GET` | `/api/library/archive` | `files:read` | no |
| `POST` | `/api/library/archive` | `files:read` | no |
| `DELETE` | `/api/library/archive` | `files:read` | no |
| `GET` | `/api/capsules` | `grabs:read` | no |
| `GET` | `/api/v1/grabs/operations` | `grabs:read` | no |
| `POST` | `/api/capsules` | `grabs:write` + `files:read` | no |
| `DELETE` | `/api/capsules` | `grabs:write` | no |
| `GET` | `/api/quota` | `storage:read` | no |

Intrinsic grants: GET mobile capabilities/profile/status, GET devices/self-device;
POST self rotate/revoke and the legacy self-revoke endpoint. Enrollment and
reauthorization remain cookie-only. The allowlist's duplicate maintenance routes
require `photos:v1`, which is not an enrollable explicit scope.
