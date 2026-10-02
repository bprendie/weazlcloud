# Photos browsing, HEIC recovery and Live Photos — October 2, 2026

Photos now owns a scroll pane. Both ends of the retained page window load
continually, and metadata-derived spacers represent unloaded dates. The date
rail, mouse wheel, keyboard and touch use this same pane. Page boundaries retain
their own cursors; duplicate replies merge by asset ID and eviction removes
whole pages. The window is limited to 2,000 metadata items and 300 mounted tiles.
Closing the viewer restores the pane position without refetching the collection.
Entering Photos resets the document scroll offset and sizes the pane above the
actual status footer, including narrow screens. Photos has no competing document
scrollbar; Library retains its own remembered document scroll position.
Background catalog changes reload around the visible asset instead of the first
page. Refresh preserves its pixel offset; if hiding/deleting removes that anchor
or its entire date group, navigation falls back within the current visibility
scope. Directional paging captures the visible anchor when the response arrives,
so continuing to wheel during a slow request does not restore an obsolete offset.
Virtual spacer/row styles are applied through CSSOM synchronously during rendering,
before restoring scroll: strict CSP rejects HTML inline style attributes, which
previously left a temporary short layout and caused browser scroll clamping.
Late status/event refreshes and mode-restoration frames cannot supersede a newer
navigation request.

## Preview worker and recovery

The private worker requires `TMPDIR=/tmp`, `HOME=/tmp` and
`XDG_CACHE_HOME=/tmp/cache`. Its isolated `/data` tmpfs does **not** contain the
API's `/data/tmp`. Keep the API's scratch directory on its existing data volume.
Startup checks scratch, codec availability and an embedded generated HEIC decode;
`weazl-photo-worker -probe` checks PNG and HEIC 320/1280 single and bundled RPCs.

RPC v3 carries bounded error codes, falling back to old v2/v1 routes. Worker
configuration, unavailability and deadlines are temporary service failures
(HTTP 503 with `Retry-After`); invalid/unsupported media stays a separate result.
Decoder stderr is private. Browser failures have a short negative-cache expiry.

On owner unlock, enabled preparation performs one encrypted, owner-scoped legacy
HEIC recovery. It clears only eligible failed 320px negative records and requeues
failed HEIC jobs. Date-only revisions are accounted for. Successful derivatives,
originals and explicit pauses stay intact. The marker is
`.weazl-heic-scratch-recovery-v1.enc`; do not erase it to force global rebuilding.
A genuine corrupt or unsupported original can still fail after this retry.
The Photos menu contains **Preview failures** and **Retry failed previews**.

## Live Photos

A paired photo uses one still-first tile and a **Live** badge. The viewer plays
motion explicitly, initially muted, then returns to the still. Both original
resources remain downloadable; Download Live Photo prepares the existing backend
ZIP. Genuine short videos remain videos.

**Inspect Live Photos** is a dry run. **Repair Live Photos** discovers matching
Apple still/QuickTime `ContentIdentifier` (or `MediaGroupUUID`) metadata within
one owner and source directory. Only a unique image/video pair with compatible
hidden/archive/favorite state is applied. Basenames and duration are never pairing
evidence. Missing identifiers, duplicate candidates and conflicting visibility
remain unresolved; they do not abort neighboring files. An owner may explicitly
link a selected still/video or unlink a pair.

The encrypted `.weazl-live-photos.enc` checkpoint resumes queued/running work after
unlock. Pause remains paused; checkpoint errors require an explicit resume.
After an apply job has been enabled, later ordinary imports join its durable queue
before their processing receipt is acknowledged. Scan batches are bounded to 16,
with at most four admitted source readers. Source resources are capped at 64 MiB;
large originals remain stored even when automatic discovery cannot inspect them.

Motion previews use a bounded, single-thread FFmpeg H.264/AAC MP4 transcode: up to
10 seconds, 1280 pixels and 8 MiB. The worker receives anonymous memory files,
never vault keys or source paths, and has no network. Derived motion is cached
encrypted and revalidates the owner, pair and visibility on access. This is a Live
Photo playback path, not a general video transcoding service or native PhotoKit
animation. The existing original streams are preserved.

Frozen gallery grabs retain guest-local pair relationships and a compatible
motion derivative. Guest browsing/playback spends no retry; explicit downloads
still spend one. A selected primary ZIP includes both original components.
Owner IDs, paths and vault APIs are not placed in the guest manifest. Existing
revocation, hidden-confirmation and frozen-source rules still apply.

## Owner API additions

All routes require the current unlocked owner; mutations use existing desk CSRF
protection. Responses are private and not cached by shared proxies.

| Route | Contract |
| --- | --- |
| `GET /api/v1/photos/failures?start=0` | Up to 100 current failed assets, next offset, category and format counts. |
| `GET /api/v1/photos/live-jobs?report=1` | Job counts/status; report includes the first 100 private candidates. |
| `POST /api/v1/photos/live-jobs` | `action`: `dry-run`, `start`, `pause`, `resume` or `retry`. |
| `POST /api/v1/photos/live-pair` | `still_id`, `motion_id`, both current revisions, `hidden`, optional `unlink`. Stale revisions fail; originals are unchanged. |
| `GET/HEAD /api/v1/photos/assets/{primary}/motion?hidden=1` | Authorized compatible MP4 with byte-range support; hidden is opt-in. |

Automatic pairing uses ExifTool's documented Apple maker tag and QuickTime key;
see [Apple tag definitions](https://github.com/exiftool/exiftool/blob/master/lib/Image/ExifTool/Apple.pm).
The generated fixture embeds Apple maker-note tag `0x11` in a JPEG and the matching
QuickTime key in a two-second synthetic clip. It contains no personal media.
Physical iPhone/HEIC identifier varieties and Safari playback remain device
validation; metadata stripped by an export cannot be reconstructed reliably.

## Validation and deployment record

Local Chromium/Docker Photos smokes passed on both Restic and shared storage,
with each application limited to 2 CPUs / 4 GiB. They exercised real upload,
Hidden/Archive selection, filters, history, touch rail, restart/preparation,
identifier discovery, Live playback/Range, frozen guest motion and exact original
readback. The 4,000-row synthetic timeline crossed both page boundaries, kept
wheel movement during a delayed page response, and preserved an SSE-refresh
anchor within 2 CSS pixels. No large fixture was uploaded to production.

| Fixture measurement | Restic | Shared |
| --- | ---: | ---: |
| First four photo cards | 365 ms | 384 ms |
| First thumbnail | 449 ms | 472 ms |
| Warm viewport p95 (20 runs) | 169 ms | 163 ms |
| Scroll script work p95 | 6.58 ms | 11.79 ms |
| Repeated thumbnail requests (30 cached renders) | 0 | 0 |

These small local fixtures distinguish cold renders from warm browser cache;
these timings are not a guarantee for every device or large collection. The
isolated worker smoke rendered the 734-byte generated HEIC at both variant sizes,
in single and bundled RPCs, and rejected absent scratch truthfully. Focused Go
race tests cover pairing/revisions, motion cache ownership, frozen guest playback,
error framing and recovery. The production record is appended after rollout.
Use the [recovery runbook](photos-release-runbook.md) and preserve live overrides,
vaults, catalogs, original storage, CPU limits and imported albums. Older writers
can drop new pair metadata: prefer a forward fix or a complete consistent restore.


### Production rollout

Release `photos-remediation-754465ea546f`, source
`754465ea546fe68b0a472e116507a493acb795dc`, is live on October 2, 2026.
Both services use `weazlcloud:release-754465ea546f` and passed health checks; the
isolated worker's real HEIC probe passed. `make check`, focused race tests and
both complete browser/storage smokes passed before activation.

The application drained and exited cleanly before stopping the worker. A full,
consistent XFS reflink backup was taken at
`/exports/dockervolume/weazlcloud-backups/photos-remediation-754465ea546f/data`.
Node/account settings, catalog, albums, vault envelope and node key checksums
matched the stopped backup and the restarted node before unlock; repository
inventory also matched: 140,039 immutable repository files had identical relative
paths and sizes in the live `users/*/library` trees and the backup (ephemeral
Restic locks excluded). Live bind mounts, ports, resource ceilings, accounts,
passwords and hostname stayed intact. Release configuration and check records
are under `/home/bobp/weazlcloud-releases/photos-remediation-754465ea546f/`.
The production custom Compose configuration changed only both image references
and the worker's three scratch environment overrides.

Live Chromium checks used the existing 37,111-item timeline through an encrypted
SSH tunnel. A middle-date jump reached position 18,553; wheel-up reached 18,397
and reversal reached 18,634 across neighboring pages. The retained window was
400 items with 35 mounted tiles, no document scroll, no original-file requests,
no page errors and **0 CSS px** drift after three complete UI refreshes.
No production fixture upload or metadata edit was used for that browsing check.

Both previously rejected HEIC samples returned real JPEGs at 320 and 1280 px.
Preparation resumed with 8 render workers / 4 readers. At the recorded snapshot,
ready increased from 32,276 to 32,981, failed decreased from 4,785 to 2,652,
and 1,428 assets still awaited a result. Current failed categories were 1,962
invalid/unsupported and 690 previous terminal failures; only seven current HEIC
failures remained. These are in-progress counts, not a promise every remaining
file is decodable. Successful original storage was not reimported or removed.

Initial post-unlock cache inventory temporarily exceeded the status request's
60-second client timeout. Once rendering was admitted, preparation and failure
reports returned in 129 and 110 ms; live browsing passed during rendering.
Cold inventory latency is separate from the corrected scroll-layout bugs and
remains a responsiveness follow-up.

The owner-authorized identifier-based Live Photo repair was queued for 35,901
eligible unpaired image/video resources. It continues server-side with durable
checkpoints and only applies unique compatible same-directory identifiers.
The browser may close; unmatched media and original resources remain intact.
Physical-device/Safari coverage described above remains a separate follow-up.
