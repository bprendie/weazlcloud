# Phone upload pipeline — October 9, 2026

The phone can continue transferring originals while the server commits earlier
uploads and prepares Photos metadata and previews. `parts-v1` and
`commit_when_complete: true` remain the transport contract. This release changes
server scheduling and storage, preserving existing device identities, upload
IDs, encrypted parts, source revisions, receipts and photo organization.

## Receive, store, then prepare

1. Accepted parts have passed their size/checksum checks and are encrypted and
   synced to the configured volume. The app can send another asset immediately.
2. A fair background pool verifies complete originals and commits them to storage.
   `stored` remains the durable backup receipt; `queued` is not equivalent.
3. Capture metadata, thumbnails and Live Photo processing remain asynchronous.
   Preview completion is not a prerequisite for uploading another photo.

No whole movie or disk image is buffered in RAM. Finalizers stream from the
existing encrypted parts. No new plaintext staging area or data migration is
introduced. The vault must remain unlocked for private work. As before, a server
restart requires owner sign-in/unlock; the existing seed resumes from its saved
parts and receipts. Incomplete/unfinished staging still has its existing
24-hour inactivity lifetime, so clients should bound their pending receipt queue.

## Scheduling and locking

Finalizers run in a persistent pool, with one session per owner admitted per
fair round until capacity is full. Completing a job immediately fills an available
slot from the bounded pending scan. New jobs are discovered once per second.
Duplicate owner/upload pairs cannot run concurrently. Device revocation, owner
deletion and vault lock still cancel work and guard final publication.

Let P be visible CPU capacity and M be `max(1,min(8,visible_RAM/2_GiB))`.
Defaults are `max(1,min(M,P/2))` globally and `max(1,min(4,M,P/4))` per owner,
using integer division. CPU affinity/GOMAXPROCS and cgroup CPU/RAM limits govern
sizing. A two-CPU/four-GiB host uses one finalizer; a sixteen-CPU/sixteen-GiB or
larger allocation uses eight globally and at most four per owner.

Optional settings, also forwarded by the reference Compose configuration:

- `WEAZLCLOUD_MOBILE_FINALIZE_WORKERS`: 1–8 globally.
- `WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER`: 1–4, capped by the global limit.

Defaults account for resources; explicit overrides can raise those defaults up
to the hard maxima. Malformed nonempty values fail startup. These are concurrency
limits, not hard aggregate RAM reservations. Capabilities report the actual pool.

Photo component storage uses per-path gates. The library-wide mutation lock is
held for identity and catalog publication rather than the entire byte transfer.
This lets upload creation, browsing and unrelated components proceed during
slow storage. Shared-backend prepare/recovery intents remain owner-bound.

## Restic batching and recovery

The installed `weazl-restic-writer` helper groups up to eight components and
64 MiB of declared source bytes, using a 35 ms collection window. It loads one
Restic index and writes one standard snapshot with separately addressable files.
Originals pass through inherited pipes; both caller and helper verify lengths
and hashes. The helper uses two readers, a 512-MiB soft Go heap target and a
two-minute helper deadline. Source readers must cooperate with cancellation;
production reads bounded encrypted chunks from local files and joins readers
before releasing vault keys. A stalled kernel/filesystem read can delay that
drain. Repository index memory still depends on repository size. Larger files, single requests and hosts without the helper retain the
ordinary streaming Restic path. Shared storage retains its native dedupe path.

Verified references are journaled in encrypted component intents before catalog
publication. They survive a lost receipt or restart, and trash pruning protects
references still needed by pending component intents. A failed batch never earns
a stored receipt. Its encrypted parts remain available, and transient batch
failures retry after 2, 4 and 8 seconds. Retry writes run individually so a bad
member cannot repeatedly abort healthy siblings. Persistent failures remain
visible for explicit retry; cancellation and corruption checks still apply.

## App handoff

- Keep separate transfer and receipt queues. Advance transfer lanes after all
  parts are accepted; mark a backup complete only after `stored`.
- Start with four different assets in flight. Bound uploaded-but-not-stored work
  by both count and bytes, rather than filling the entire server staging volume.
- Queued/verifying responses send `Retry-After: 2`. Poll at two seconds or slower
  with backoff/jitter, stop terminal polls, and reconcile after connectivity
  returns. Do not fetch capabilities or refresh the whole library per progress tick.
- Keep originals, album/source identifiers, Hidden flags and paired Live Photo
  components intact. No app API migration or seed reset is required.

One production sample before this change recorded 10,214 status requests versus
150 part PUTs. These endpoint counts identify excess polling; they do not measure
the phone's export/iCloud download time or prove every upload delay is server-side.
The inaccessible iOS checkout was not modified in this pass.

## Validation and rollout

Use `make check` for Go/race/native-helper checks, `make smoke-mobile` for the
mobile contract, and `scripts/smoke-mobile-throughput.py --image NEW --old-image OLD`
for disposable local throughput and upgrade fixtures. Native writer tests also
read output with stock Restic and run its data-integrity check.

A disposable local comparison used 12 unique generated PNGs (20.26 MiB), four
upload lanes, and the same eight-CPU/eight-GiB container limits for both versions.
These are single runs with client polling lag, not production throughput promises.

| Storage backend | Old all-stored time | New all-stored time | Observed improvement |
| --- | ---: | ---: | ---: |
| Restic | 12.160 s | 6.113 s | 1.99× |
| Shared experimental | 11.630 s | 5.606 s | 2.07× |

All parts landed in 0.12–0.17 seconds on the local host; this does not measure
Wi-Fi or iCloud export. Preview completion fell from 23.181 to 16.132 seconds
with Restic and from 12.634 to 5.608 seconds with shared storage.

Both backends passed the old-to-new upgrade fixture: queued work resumed after
unlock without client finalization, partial uploads retained exact part receipts,
and original/motion hashes, album membership, Hidden visibility and stored IDs
survived another restart. The full mobile smokes also covered a 250-MiB component.

Production activation is recorded after final release validation.
