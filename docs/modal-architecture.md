# Photos mode M0 architecture decision — September 28, 2026

M0 establishes the performance baseline and chooses the metadata boundary for
the first-class Photos mode. Production was not contacted or changed.

## Decision

Keep Go as the API, authorization layer and vault coordinator. Add an owner-
scoped SQLCipher metadata service behind a local Unix socket in a later phase.
The service will hold one encrypted database per vault and expose only typed
Photos queries and mutations. It will not expose SQL or a TCP listener.

The API will pass a short-lived vault-derived database key over the protected
socket after the vault is unlocked. The metadata process will close the database
and wipe its key when the vault is locked, rekeyed, revoked, or its lease ends.
The API remains the authority for identity, permissions, quota and original-file
reads. SQLCipher is a query/index layer, not a second source of truth for
originals.

This boundary keeps the current CGO-free Go service portable while allowing
SQLCipher and native media tooling to be isolated in a purpose-built process.
It also gives us a clean path to a side-container on larger hosts without
requiring a database server or a third-party identity service. A Unix socket and
private volume are mandatory; SQLCipher alone is not a substitute for process
authorization.

## Metadata model

The first schema prototype contains `assets`, `albums`, `album_assets`, and
`derivative_jobs`. The important indexes are:

- owner + capture time + stable asset ID for timeline pages;
- owner + selected source root + capture time for the Photos root policy;
- owner + favorite + capture time for Favorites;
- album + position + asset ID for album pages;
- owner + job status + kind for worker pickup.

Asset IDs are independent of paths. The local M1 projection links each asset to
the existing catalog entry and revision, stores capture-date provenance and
timezone, and preserves membership through rename, replacement, Trash, restore,
rekey and account deletion. Durable query-store persistence is a later service
integration.

SQLCipher key handling, WAL and temp-file rules are part of the service contract:

- use a vault-derived key only while the owner session is unlocked;
- use encrypted WAL/journal pages and checkpoint before close;
- disable file-backed plaintext temporary storage;
- keep temporary query buffers in memory or an explicitly encrypted directory;
- verify `cipher_integrity_check` after reopen and restore;
- use a fresh database backup before rekey, verify it with the new key, and retain
  an auditable checkpoint before replacing the active database.

The service must never log paths, captions, hashes, keys or query values. It must
not accept a path supplied by a browser or mobile client as a filesystem path.

## M0 measurements

The reproducible SQLCipher fixture is `scripts/m0-sqlcipher-prototype.sh`. It
runs Alpine SQLCipher in a disposable Docker container limited to 2 CPUs and
4 GiB, creates 97,000 deterministic metadata-only entries including exactly
32,000 photo assets, five albums, 4,999 memberships and 10,000 derivative jobs,
and spreads capture dates across 2010–2026 while imports remain in 2026. It
includes 283 photos with unknown capture dates and timezone-offset cases, so
sorting by import time cannot pass. It never mounts a WeazlCloud vault.

Run it with:

```sh
bash scripts/m0-sqlcipher-prototype.sh
```

The existing in-memory Go baselines remain useful for comparison:

```sh
go test ./internal/library -run '^$' \
  -bench 'BenchmarkPhotoPage100K' -benchtime=100x -count=3
```

The September 28 run completed the security and recovery checks:

- cold CLI timeline median 370 ms / p95 402 ms; date buckets 430 ms / 471 ms;
  album page 352 ms / 385 ms; mixed write 413 ms / 454 ms. These include one
  SQLCipher process and key setup per query, so they are not warm service
  latency targets.
- one SQLCipher timeline process used about 8.4 MiB peak RSS and 47–49 CPU
  ticks in the disposable container. Browser smoke passed locally, and the
  existing Go photo-page baseline is 15.2 µs for a warm 100-item page over a
  100k in-memory index.
- local `/live` health requests measured 0.42 ms median and 0.91 ms p95 over
  20 requests on the reference workstation. This is a node-health baseline,
  not authenticated catalog or browser-paint latency.
- the disposable Restic/shared-store comparison passed: shared storage wrote
  in 709 ms and read in 110 ms, with 7.22 MiB logical dedupe savings; Restic
  wrote in 6.68 s, restored in 1.50 s, used 149 MiB child RSS and 6.89 s child
  CPU. The fixture is synthetic and these are engineering baselines.
- encrypted export/reopen, forced-kill rollback, rekey, cross-vault key
  rejection, WAL checkpoint and plaintext-sentinel scans all passed.

These numbers separate index/query work from Restic reads, browser layout,
thumbnail rendering and encrypted vault loading. The production Xeon has a
different CPU budget, so no production ETA or throughput claim belongs here.

## M1 implementation checkpoint

The local M1 foundation is implemented behind the existing catalog and Library
boundaries. Catalog entries now keep imported time, capture time, capture offset,
capture provenance and a manual-correction bit as separate encrypted fields,
alongside media dimensions, orientation, duration, favorite/archive state and
captions. Photo pages expose these fields and sort canonical capture dates before
an explicit Date unknown tail. Existing Library modified dates remain separate.

Takeout imports parse matched `photoTakenTime` sidecars after archive files land.
JPEG originals can also supply EXIF DateTimeOriginal and a valid offset.
`internal/photos` applies user correction, Takeout, embedded and client
precedence and provides a metadata-only, checkpointed backfill. It reads at most
a bounded prefix of an original, preserves originals, leaves unresolved dates
unknown, and isolates per-asset parse/apply errors.

The projection model now gives source roots, assets, original components, stable
album IDs, explicit album memberships, duplicate groups, trash state and
rebuildable derivative state. Raster dimensions and JPEG orientation are read
from bounded source prefixes. Capture and media updates commit together as one
catalog revision, and invalid capture dates or offsets are rejected. The
projection comparator checks roots, assets, dates, media, memberships and
ownership before a future query-store cutover.

Focused tests cover Takeout dates, EXIF offsets and orientation, precedence,
manual corrections, invalid dates, checkpoint resume, dry-run behavior, raster
media fields, encrypted catalog persistence, stable rename identities and the
photo API payload. HEIC/video metadata extraction, durable database-backed album
membership, durable change delivery and runtime old/new flags are intentionally
the next service/codec boundary before the M3 timeline gate.

## M2 implementation checkpoint

The local M2 foundation adds an encrypted per-vault queue for photo derivatives
jobs. A job identity includes owner, stable asset ID, catalog revision,
operation and renderer version. The queue persists leases, attempts, retry
times, status and generic error categories; expired leases are reclaimed after
restart. New mobile-photo uploads receive higher priority than historical
backfill, and a finalized upload schedules its preview without browser polling.
Queue state is wrapped with the active vault key and contains no source paths,
content hashes or plaintext media.

The first version of `POST /api/v1/photos/uploads` uses the existing resumable
upload manager for authenticated create, status, chunk, finalize and cancel
operations. An idempotency key is based on the device and source asset IDs.
Uploads land under `Photos/Mobile/<device>/<asset>/`; an optional client capture
timestamp is stored as metadata while the original bytes remain unchanged.
Device-scoped credentials and sync cursors are still part of the later native
client contract.

The private worker is a separate container with no network interface or vault
volume. The API authorizes each operation by sending bounded source bytes through
a mode-0600 Unix socket; the worker receives no path or key and returns a bounded
derivative. Raster and FFmpeg subprocesses run under the worker's cgroup limits,
with per-process output, memory-admission, thread and time bounds. The deployment
operator sets the worker CPU quota to half the available allocation. The API
queue remains encrypted and owner-scoped. Operators can choose
`WEAZLCLOUD_PHOTO_SCHEDULE=quiet|balanced|fast`; quiet pauses background work,
balanced is the default, and fast can use the full photo worker allowance while
reserving a render slot for foreground requests. The selected mode is reported
by preparation status. Locking through the resource registry now cancels and
settles background jobs before removing the vault key, so the queue can release
leases safely; request cancellation reaches the worker over the local socket.
The worker generates first-frame video posters and WebP/TIFF previews through
bounded pipes where the installed FFmpeg build has the required codecs. The
original Alpine FFmpeg build did not decode HEIF: a generated HEIC fixture failed
in the container even though the host FFmpeg test passed. HEIF/AVIF runtime
support therefore remains an explicit M2 gate. Mid-render percentage progress,
broad device fixture coverage, video playback transcoding, Live Photo pairing,
raster Restic read reuse, Restic batch work and mixed-load measurements also
remain. No production system was contacted.

Local validation passed `go test ./...`, the Compose worker render probe,
container readiness checks and `git diff --check`. The HEIF/video pipe tests run
when the local FFmpeg/HEIF tools are present.

## M3 UI checkpoint

The Photos mode groups visible items by canonical capture day and opens a
full-screen viewer. The viewer supports image derivatives, browser-playable
video, previous/next controls, arrow keys, horizontal swipe, zoom, adjacent
thumbnail prefetch, capture/path/size details, original download, and URL/history
return to its source route. The owner-authenticated APIs return bounded pages,
date counts, detail and thumbnails; cursors bind to filters and stable asset IDs.
The unfiltered timeline has direct ID-to-index anchor lookup. The client windows
mounted cards, cancels detached preview work, caps retained rows at 2,000 and
refreshes from the current visible anchor after catalog events. Folder visibility
is encrypted in the owner catalog and enforced across normal Photos pages, date
counts, album covers, detail and thumbnails, with a separate Hidden view.

M3 now has bounded cached row-position indexes for filtered/date/album/search
queries, deleted-cursor neighbor recovery, and justified aspect-ratio rows with
metadata-only layout tests. Native snapshot sync also reuses a stable-ID index.
Full socket/browser frame and constrained HTTP measurements remain open because
the resumed shell rejects local TCP/Unix listener creation. Socket-free real
handler tests cover Takeout/photo ingestion, preview readback, gallery grants and
owner isolation; these do not replace the browser/container release gate.

## September 30 implementation checkpoint

Owner-created albums, captions, capture corrections, rotation and preferred
exact-duplicate presentation live in the encrypted canonical catalog. Album
changes and file mutations append to an encrypted hash-chained journal in the
same atomic write. Device checkpoints are encrypted there too. Restore/future
or divergent checkpoints require a bounded resync. Device tokens contain no vault
key; vault lock rejects reads and ingestion. See `docs/photo-api.md`.

Gallery capsules freeze selected original revisions and re-encoded derivatives,
then build an encrypted ZIP. A random capsule key grants only those copies,
independently of the owner's vault session. Public manifest and preview browsing
uses no retries; explicit original/selected ZIP/full ZIP transfers do. Guest
session tokens are sealed, capsule-scoped and expire after 15 minutes. Expiry,
revoke and account deletion deny new requests and remove retained capsule bytes.
Source deletion or album removal does not silently revoke an already frozen grab.

The runtime still uses the encrypted JSON catalog plus in-memory query indexes.
The SQLCipher service described above remains a prototype and later integration;
it has not silently replaced canonical persistence. Do not claim DB/WAL runtime recovery, an implemented iOS app or full Immich
parity from this checkpoint. Logical still/motion ingestion is now implemented
through the existing byte upload engine, with encrypted receipts and atomic
source identity/album/outbox metadata; see the current API contract.

## Packaging choice and alternatives

M0 selects the local SQLCipher service prototype over linking SQLCipher directly
into the Go binary. Direct CGO would complicate the existing static, portable
build and would put database-native failure handling in the request process.
PostgreSQL is deferred: it becomes reasonable if measured concurrent writers,
multi-node access, or metadata volume justify a server, but its roles and disk
encryption would not by themselves reproduce per-vault key isolation.

The current encrypted JSON catalog remains authoritative during M1 migration.
The SQLCipher database is introduced as a rebuildable derived/query store until
dual-read comparisons, crash recovery and album/user-edit migration pass.

## Rollback

M0 has no runtime migration and no production effect. Delete the disposable
fixture directory after a run. During M1, leave the encrypted JSON catalog intact,
write a versioned SQLCipher database beside it, and switch reads only behind a
feature flag after comparison. A failed migration disables the feature flag and
removes only the rebuildable database after preserving its audit record. Never
delete originals, catalogs, vaults, or album edits as part of a metadata rollback.

The M2 workbook gate supersedes the earlier codec warning: the worker now uses
libheif for HEIC in anonymous memory files and its generated rotation/color
fixture passed. Mid-render progress and worker recovery/resource smokes passed.

## September 30 recovery and native API completion

Logical still/motion uploads use source revisions, owner-resolved stable Photos
root IDs and one encrypted catalog commit for pairing and album membership.
A durable processing marker is cleared only after media job checkpointing.
The socket-free native simulator exercises byte-engine/coordinator restart,
offset/checksum errors, incomplete pairs, lost final responses, source revisions,
two devices using the same filename, wrong media, physical capacity exhaustion,
checkpoint restore detection, vault lock and device revocation.

Large selection tokens keep up to 100,000 primary IDs encrypted on the server
rather than sending a whole day/album into the browser. Owner ZIPs use sealed job
metadata and indexed AES-GCM chunks (1 MiB) for bounded memory and range reads.
Queued jobs recover lazily on the first owner request; ready jobs retain for 90
minutes. Public selected ZIP jobs recover from frozen originals independently of
the owner's vault and remain until capsule burn/expiry/revoke. Guest streams
release the node-wide capsule mutex after admission; the final admission closes
the grant while already-admitted streams retain leases on their frozen bytes.
Explicit revoke cancels leases. No changes in this pass require a SQLCipher
runtime database or expose private media to a third-party service.
