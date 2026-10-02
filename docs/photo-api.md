# Photos API v1

Updated September 30, 2026. The versioned routes are implemented locally; the
browser/container release gates are tracked in
[plan_modal.md](../plan_modal.md). This is a web/API foundation, not an iOS app.

Authenticate with the local account. An approved account may enroll a device
from an authenticated, unlocked account session with `POST /api/v1/devices`
and `{"name":"My phone"}`. Save the returned token in the platform keychain;
the server stores only its hash. Native requests send `Authorization: Bearer
<token>`. Mutations also send `X-Weazl-Desk: 1`. If an Origin header is supplied,
it must match the request origin. No third-party identity or helper is involved.

Credentials expire after 90 days and are limited to Photos/device routes. A
device cannot enroll other devices or change the node hostname. Password change,
account disable/delete and explicit device revocation invalidate credentials.
`GET /api/v1/devices` lists only the owner's devices; `POST
/api/v1/devices/revoke` accepts an `id`. A bearer credential may revoke itself;
the owner's account session may revoke any of its own devices.

Device credentials contain no vault key. A locked vault rejects metadata,
original reads and ingestion; it never reports a pending original as stored.
Unlock uses the existing local account/vault flow. The capabilities endpoint remains available while locked, with an empty root list.
Background processing is server-side while unlocked and does not depend on an open browser. Revocation
denies subsequent requests; bytes already delivered cannot be recalled.

## Queries and edits

`GET /api/v1/photos/capabilities` reports the implemented contract, current vault
state, root IDs, page/chunk limits and optional features. Do not infer support
from future routes in the workbook.

`GET /api/v1/photos` accepts `limit` (maximum 200), `cursor`, `album`, `mode`
(`all`, `recent`, `favorites`, `archived`, `hidden`), `date` (year/month/day or
`unknown`) and `around` (stable asset ID). The response has `items`,
`next_cursor`, `previous_cursor` and `generation`. Cursors are owner-encrypted
and filter-bound. A deleted cursor anchor resumes at a surviving neighbor.

Assets use the same opaque entry ID in page, detail, upload result and sync.
A logical still/motion pair is one timeline asset with `components`; sync also
returns component records with `parent_asset_id`. Component IDs use the same
owner/hidden checks as originals. Paths are descriptive and may change; use IDs as client identity. The internal
projection's `asset:` prefix is not a wire ID. Revisions change on committed
metadata or content changes. Capture timestamps use the known original offset
for display/day/month filtering. An unknown offset remains unknown; import time
is separate and is used only for Recently added.

`GET /api/v1/photos/search` supports `q` (case-insensitive substring, `*`, `?`),
`type` (`image`/`video`), `camera` (substring), `from`/`to` (inclusive capture
calendar dates), `favorite=1`, `archived=1`, `hidden=1`, `unknown=1`,
`outside_albums=1`, `album`, `limit` and `cursor`. Outside albums excludes named
folder albums and custom membership; it is incompatible with an album filter.
Camera extraction currently reads JPEG EXIF make/model; absent metadata is not
invented. Search pages use a bounded index cache and do not read originals.

`GET /api/v1/photos/dates` returns month counts and unknown dates. Use `hidden=1`
only for explicit Hidden browsing. Detail and thumbnail routes are
`/api/v1/photos/assets/{id}` and `/assets/{id}/thumbnail?size=320`. Thumbnail
size is 96–1280. `GET|HEAD /assets/{id}/original` supports single byte ranges,
ETag and If-Range without loading the original into RAM; responses are private
and no-store. A vault lock cancels in-flight owner reads. Hidden assets require `hidden=1` in detail/thumbnail requests;
knowing their IDs does not bypass the normal-view filter. Existing owner Library
access remains available because hiding is Photos presentation within one vault.

Photo items may include `preview_identity` (an owner-keyed derivative identity,
not an original hash) and base64 `thumbhash`. These fields follow the same owner
and Hidden filtering as the asset. Hashes come from bounded encrypted manifest
segments and can be absent for older or unprepared media. A date/caption change
preserves compatible derivative identity; content/rotation changes replace it.
Clients must scope any in-memory reuse to the authenticated session and visibility,
and clear it on lock/account/access changes. Responses remain private/no-store.

`GET /api/photos/preparation` reports `ready`
for retained grid previews and `bundle_ready` for retained grid+viewer pairs.
`POST` accepts `start`, `resume`, `pause`, or `retry` and requires the desk mutation
header. Explicit preparation fills missing 320px/1280px outputs; unsupported files
fail individually. Status becomes partial if required outputs cannot be retained.
Progress reports completed bundles while running and reconciles retained variants
at completion. Browser closure does not cancel this server job. Existing completed
and manually paused jobs stay closed/paused on upgrade; explicit resume opts into
filling missing bundle variants. Photos maintenance controls live in the ☰ menu.

`POST /api/v1/photos/assets/{id}` edits `favorite`, `archived`, `caption`,
`captured_at`, `offset_known` and `rotation` (0/90/180/270). Blank capture time
means unknown. A correction changes ordering and buckets, not the original
bytes. Rotation transforms only the bounded derivative. Archive removes a photo
from the normal timeline without removing its album memberships.

`GET /api/v1/photos/albums` lists imported folder and owner-created albums.
`hidden=1` explicitly requests hidden memberships/covers. List and mutation
responses include at most 200 visible member IDs per album; `count` is complete.
`POST` accepts `action: save|members|delete`, an album `id` and optimistic
`revision`. Save accepts title, description, cover ID, position and optional
asset IDs. Members accepts `add_ids`/`remove_ids`. Deleting an album never deletes
originals. Stale revisions return 409. Album ID/path uses `album:<id>` for
timeline filtering of a custom album. `GET /albums/memberships?id=<id>` pages
at most 200 member IDs with revision/filter-bound cursors; stale cursors return
409. Use this route to reconcile a large custom album rather than downloading
all memberships with every sync page.

`POST /api/v1/photos/folders` accepts a Photos folder `path` and `hidden` flag.
Hidden is inherited through ancestors; unhide preserves explicit child flags.
`GET /api/v1/photos/trash?hidden=1` provides an explicit hidden trash context.
`POST /trash/restore` accepts an asset/folder `id`. Restore a deleted parent
before its child to preserve inherited visibility. Existing retention is 30 days.

`GET /api/v1/photos/duplicates` returns bounded exact-match groups without
exposing global content hashes. Group IDs are vault-specific fingerprints.
`POST` with an asset `id` marks a preferred presentation within the authorized
visible/hidden group. No file is automatically merged, moved or deleted and
album memberships stay intact.

## Resumable mobile ingestion

A logical upload has an `original` and optionally a `motion` component. The
existing byte upload engine handles staging, chunks, 24-hour unfinished session
retention, checksums and quota; an encrypted coordinator receipt binds the
components, device asset, source revision, capture provenance, destination root
and album memberships. Completed receipts stay with the vault for idempotency.

Create with `POST /api/v1/photos/uploads`:

```json
{"device_asset_id":"phone-asset-123","source_revision":"r1",
 "root_id":"root:photos","captured_at":"2013-04-05T06:07:08-04:00",
 "offset_known":true,"album_ids":["<owner custom album id>"],
 "components":[
   {"id":"original","filename":"IMG_0123.heic","media_type":"image/heic",
    "size":123456,"sha256":"<64 hex digits>"},
   {"id":"motion","filename":"IMG_0123.mov","media_type":"video/quicktime",
    "size":654321,"sha256":"<64 hex digits>"}]}
```

Legacy flat `filename`, `size`, `sha256` creates one original. Omitted source
revision defaults to `1`. A bearer credential supplies the device ID; cookie
clients supply `device_id`. Identity is `(device, device_asset_id,
source_revision)`, separate from filenames and content hashes. Matching creation
returns the same session/result, including after a lost final response. Changed
content under the same identity returns 409; a new source revision stores a new
asset, preserving the previous original. Devices using the same filename do not
collide. This does not promise perceptual matching or automatic version merging.

Choose a root from capabilities, starting with `root:photos`. Other advertised
roots are stable folders within the owner's `/Photos`; placement is resolved
again at finalize and inherits hidden state. The final path is
`{root}/Mobile/{device}/{deviceAsset}/{sourceRevision}/{filename}`. Arbitrary
filesystem paths or foreign roots/albums are rejected. Root listings are capped
at 200. This version does not enroll roots outside `/Photos` without placing
files into the selected Photos collection.

`GET /uploads/{id}` reports each component's accepted offset and chunk limit.
Append with `PATCH /uploads/{id}/components/{original|motion}`, a bounded body
(maximum 16 MiB), `Upload-Offset` and `Upload-Chunk-SHA256`. Wrong offsets return
409 with the accepted offset; checksum mismatches return 422 without advancing.
Resume from server state after a lost response. Exhausted physical capacity
returns 507. Cancel an unfinished upload with `DELETE /uploads/{id}`; a
partially committed finalization must be retried, not discarded.

`POST /uploads/{id}/finalize` rejects an incomplete pair before committing any
component. Each source is size/hash verified and its declared media container
sniffed. Container sniffing is not a guarantee that every frame is decodable.
The components, stable identity, capture metadata, album memberships and pending
processing marker publish in one encrypted catalog transaction. That outbox
marker is acknowledged only after an encrypted media job checkpoint succeeds;
unlock/reload retries pending markers without an open browser.

The response separates `status: stored` from `processing_state:
pending|processing|ready|failed`. Derivative failure cannot invalidate the backed
up original. An optional finalize `captured_at`/`offset_known` supplies client
provenance if creation omitted it. Creation metadata or the first persisted
finalize metadata wins on retries; user corrections and verified embedded/
Takeout capture metadata keep their higher precedence. Pair deletion/restoration
is logical; copying a whole pair gives independent IDs and source identity.

Backup is one-way ingestion. Deleting a phone item must not delete the server
backup. A future Free up space action needs verified durable originals and a
separate explicit device deletion. iOS schedules background work opportunistically;
no API contract promises continuous instant backup.

## Durable synchronization

`GET /api/v1/photos/sync` begins a bounded snapshot. Follow `next_cursor` until
`checkpoint` is returned. Save page results by stable ID/revision. The base
checkpoint precedes the snapshot; replaying its subsequent changes reconciles
concurrent creations, edits and deletes. Initial sync pages media/folders, then bounded custom album headers; it does
not attach unbounded memberships. Fetch `/albums/memberships` separately. A
delta album has `membership_changed: true` and no complete ID list. Apply
component records to the same local identity graph without showing motion
components as separate timeline photos.

Follow the checkpoint as `cursor` to receive ordered `changes`. Deletions are
tombstones. Folder/visibility changes set `resync_required` so clients reconcile
inherited state. Explicit Hidden sync is a separate context and checkpoint.
Persist a fully applied checkpoint with `POST /api/v1/photos/sync/checkpoint` and
`{"checkpoint":"...","hidden":false}`; only a device can acknowledge its own.
Use `resume=1` to resume its saved checkpoint after reconnect.

The encrypted journal retains 8,192 catalog changes. Expired, restored-future or
divergent checkpoints return 409 with `resync_required: true`. Start a fresh
snapshot and reconcile local IDs, including deletions; do not append a snapshot
over an unreconciled cache. Never interpret a lost cursor as permission to
resurrect an original.

## Frozen gallery grabs

`POST /api/v1/photos/selections` accepts explicit `ids` or a timeline/filter
selection (`mode`, `date`, `search`, `filter`). It returns only an encrypted,
owner/visibility/revision-bound token, count and 90-minute expiry. Up to 100,000
primary IDs stay on the server. `POST /selection-actions` accepts `selection_id`,
`hidden` and `action: archive|add_album|remove_album|delete`; album actions also
need `album_id` and optimistic `revision`. Hidden archive export additionally
requires `confirm_hidden`. Expired/stale selections return 409; select again.
`GET /api/v1/photos/archives?id=<job>` polls an owner Photos ZIP; add
`download=1` to stream its ready output, with single-range support. DELETE
cancels unfinished preparation. This versioned route rejects generic Library
archives, even for a device credential belonging to the same owner.
Album save can use `selection_id` for initial membership of a new custom album.

`POST /api/v1/photos/grabs` accepts selected `ids` or `selection_id` (not both), title, gate (`open` or
`passphrase`), passphrase, expiry (`24h`, `3d`, `7d`) and transfer count `grabs`.
At most 10,000 selected originals may be sealed into one gallery. Hidden sharing
requires both `hidden: true` and `confirm_hidden: true`; default selection cannot
include hidden assets. The node administrator owns the public hostname.

The encrypted capsule holds frozen originals, re-encoded previews and a ZIP.
A corrupt or unsupported preview leaves a placeholder rather than failing the
whole gallery; authorization/cancellation and original storage failures still
abort unpublished creation.
Creation reserves physical disk for both originals and ZIP plus bounded preview
overhead. Source deletion, album edits and vault lock do not change the frozen
copies. The normal revoke/expiry/account deletion lifecycle removes capsule
material and denies new requests. A creation failure publishes no partial gallery.

Guests open `/g/{id}` without an account. `POST /g/{id}/gallery` with a passphrase
returns a manifest and a sealed 15-minute capsule-only session. The session
authorizes `POST /preview/{member}`, `/original/{member}` and `/zip`. ZIP accepts
optional selected member IDs; omission means the whole gallery. Member IDs are
new opaque capabilities rather than private catalog IDs or paths.

Metadata/preview browsing never spends a retry. Each admitted explicit download
POST spends one, including an interrupted transfer. GET/HEAD/Range probes cannot
start a gallery transfer. Selected ZIPs count once for the whole selection. Final
admission immediately closes new admission; retained bytes are removed when
all already-admitted transfers settle. Explicit revocation/account deletion
cancels those transfers. Derivatives omit EXIF/GPS; original
downloads may retain it. Ordinary owner on-demand ZIP jobs retain the existing
90-minute expiry; frozen grab ZIPs remain until burn, expiry or revoke.

The guest UI renders 60 tiles per page, fetches up to three previews at once,
limits its in-memory URL cache to 120, and renews capsule-only sessions while
active. It never fetches an original as a thumbnail or keeps a passphrase in
localStorage. An expired session needs reopening; opening/preparing never spends
a retry.

Selected downloads use `POST /g/{id}/zip/jobs` with `ids`, then POST
`/zip/jobs/{job}` to poll `queued|preparing|ready|failed`. A ready job streams
through POST `/zip/jobs/{job}/download`, spending one retry. Jobs and ZIP output
are encrypted; at most two workers prepare them, with a node quota reservation.
A queued/preparing job resumes from the frozen manifest when polled after a
restart. Failed preparation can be retried by requesting the same selection.
Status also rejects a missing or truncated prepared output instead of advertising
it as ready; requesting preparation again rebuilds from the frozen originals
without consuming a retry. Stream authentication detects damaged encrypted chunks.
There are at most 32 saved selections per capsule and 64 active preparations
per node. ZIPs remain with the capsule until burn/expiry/revoke. Full-album ZIPs
are already durable when minting completes; older `/zip` selected-download
clients remain supported but stream a fresh subset instead of polling a job.

Owner archives also have encrypted job metadata and chunked encrypted ZIP output,
with bounded-memory range reads and lazy queued-job recovery after restart.
Ready owner ZIPs expire 90 minutes after preparation. Public gallery transfers
still require a new explicit POST on reconnect; preparation recovery does not
claim HTTP download resume for guests.

The machine-readable [OpenAPI contract](photo-api.yaml) covers Photos/device
routes and guest gallery operations. Passing constrained browser/container checks
and the remaining device/media measurements are in the
[verification record](photos-local-verification-2026-09-30.md).

## Capture-date repair and continuous navigation

`GET /api/v1/photos/metadata-jobs` returns the owner's current encrypted durable
repair status. `POST` accepts `action` (`start`, `dry-run`, `pause`, `resume`,
`retry`), optional `root` (default `Photos`), `dry_run` and `sidecars_only`.
One owner job runs at a time. Start repeats return active work.
Starting with different options while work is active returns 409; it never silently
changes a dry run into an apply job. Status includes initial known/unknown counts,
parser version and `supported_embedded: ["jpeg-exif"]`.
Retry requeues failed/unresolved assets; clean results are retained. `GET ?report=1&cursor=N`
returns up to 100 private per-asset outcomes and a next offset. An empty page ends
pagination. Counts distinguish changed, unchanged, unresolved and failed assets.
Dry runs persist only their own encrypted report/checkpoint, preserving a completed
apply checkpoint. Durable `sequence` selects the current job across restart. Jobs resume after owner unlock
and continue without a browser. Lock/rekey stops private work; source errors do
not stop other assets. Shared durable commit failures pause/report the job.

Takeout `photoTakenTime` takes priority over JPEG EXIF and mobile/client capture;
user corrections always win. Conventional and supplemental sidecars are supported;
truncated names require an unambiguous directory-local title match. `creationTime`,
mtime and import time are never capture fallbacks. JPEG is the current embedded
capture parser; other media can receive dates from Takeout sidecars. Unknown
metadata remains unknown. Batched catalog saves preserve source bytes and edits.

`GET /api/v1/photos/dates` now accepts the same scope as the rail: `mode`
(`all`, `favorites`, `archived`, `hidden`), `album`, explicit date filter `date`,
`search=1`, `q`, `camera`, `type`, `from`, `to`, `outside_albums=1`, `unknown=1`.
The existing `hidden=1` alias remains accepted. It returns an array of month
counts/ranks, known/unknown totals and generation. `month=YYYY-MM` adds bounded
day anchors. Ordinary responses exclude Hidden assets; admin identity does not
provide vault access.

`GET /api/v1/photos/seek` accepts this scope plus one destination: `at=YYYY-MM-DD`,
`at=unknown`, `rank=N`, `around=stableID`, or `cursor=...`; `limit` defaults to 100
and is capped at 200. It returns a bounded `items` page, `anchor_id`, `position`,
`start`, `total`, `generation` and before/after cursors. Continue using the seek
endpoint with the returned cursor and the identical scope. A seek navigates the
continuous scoped collection; it does not add a destination-day filter.
Empty calendar days choose the nearest available day, newer on a tie. Outside
ranges clamp to dated endpoints; Unknown date is separate. Capture offsets define
calendar buckets and are never inferred from the browser/server timezone.
Prepared summary/seek requests read metadata only, not originals.
