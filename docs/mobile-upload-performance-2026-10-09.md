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
Defaults are `max(1,min(M,P/2))` globally. Per owner, hosts below 16 visible
CPUs use `max(1,min(M,P/4))`; hosts with at least 16 visible CPUs use `min(8,M)`.
Integer division rounds down. CPU affinity/GOMAXPROCS and cgroup CPU/RAM limits
govern sizing. A two-CPU/four-GiB host uses one finalizer; a host with at least
16 visible CPUs and 16 GiB RAM uses eight globally and eight per owner.

Optional settings, also forwarded by the reference Compose configuration:

- `WEAZLCLOUD_MOBILE_FINALIZE_WORKERS`: 1–8 globally.
- `WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER`: 1–8, capped by the global limit.

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

A second run against the final candidate, with other checks running locally,
measured 12.949 → 5.691 seconds for Restic (2.28×) and 12.184 → 1.650 seconds
for shared storage (7.38×). The variation is why these fixture results should
not be extrapolated to a seeded production repository or phone network.

Both backends passed the old-to-new upgrade fixture: queued work resumed after
unlock without client finalization, partial uploads retained exact part receipts,
and original/motion hashes, album membership, Hidden visibility and stored IDs
survived another restart. The full mobile smokes also covered a 250-MiB component. The final release image
passed those smokes on both storage backends with two CPUs and four GiB. Sampled
server RSS peaked at about 146–147 MiB during that large-file fixture; this is a
sample, not a memory limit or a bound for larger repositories.

`make check` passed Go unit/race tests, vet, native-helper tests, source-size checks
and JavaScript checks. The release image passed isolated renderer probes and
Desk/WebDAV/Grab/restart/migration container smokes.

### Production activation

Activated `weazlcloud:release-97cb1d939d7f` on October 9, 2026 at 18:25 UTC.
The old API and renderer exited zero without OOM; the fresh XFS reflink checkpoint
contains 68,583 files and matched canonical hashes, file sizes and ownership.
Checkpoint: `/exports/dockervolume/weazlcloud-backups/mobile-ingest-20261009/data`.

The cutover and verification completed in 22.9 seconds. Both services are healthy;
LAN Desk/Grab/WebDAV readiness, the renderer probe, public HTTPS readiness, exact
served UI assets and the unauthenticated browser login check passed. Mounts,
ownership, environment and resource limits were compared before/after and
preserved. No seed reset or staging purge was performed.

That earlier release reported eight global finalizers and four per owner. Preview policy
remains eight render workers, four background workers and four source readers.
Owner sign-in/unlock is required after restart before private queued work can
resume. The public check did not submit owner credentials; it does not establish
that the phone has reconnected or that its seed has finished.

The implementation is committed and pushed. GitHub CI was still running at the
last release check; local checks and the release-image smokes above passed.


### Upload stall trace and proxy timeout

After the rollout, production accepted owner unlock and successfully wrote
multi-file Restic batches. Later, repeated part PUTs returned 503 at approximately
60,000 ms. The installed Traefik binary reported a default HTTPS request-body
read timeout of 60 seconds, with no override in its static configuration.
Restarting that unchanged configuration retained the limit.

A harmless synthetic login request used a random nonexistent account and delayed
its final request byte for 65 seconds. Direct access to port 7272 returned the
expected 401 after reading the complete body; the same public HTTPS request
returned 499 through the proxy. No production credentials or photo bytes were
used in the probe.

The operator configuration on `traefik.teralab.local` now explicitly sets:

```yaml
entryPoints:
  https:
    address: ":443"
    transport:
      respondingTimeouts:
        readTimeout: 300s
```

This applies to the shared HTTPS entrypoint, not just the WeazlCloud router.
Only that setting changed; the prior static configuration is preserved at
`/home/bobp/traefik-releases/weazl-upload-timeout-20261009/before.yml`.
The candidate parsed with the installed image in an isolated container (live
providers and certificate files were intentionally absent). After the real proxy
restart, WeazlCloud/Grab readiness and WeazlTunes/Subweazl/WeazlMusic returned 200.
The repeated 65-second probe then returned the expected 401 through both direct
and public HTTPS paths, confirming the former cutoff is removed. The vault
server was not restarted and staging was not cleared.

Separate recovery concern: an explicit device reauthorization was observed in
server logs. It invalidates previously admitted upload grants; polling a transfer
can still return 200 while the scheduler refuses its old grant. This is a code-
verified possibility, not a decrypted inspection of each production manifest.
The app must replay the same create request with current credentials and unchanged
source revision, component hashes, destination, Hidden and album metadata. The
existing create handler refreshes the grant for matching immutable intent and
preserves accepted parts. A new upload identity or seed reset is unnecessary.
GET status polling alone does not perform this recovery. Parallelize different
assets first; part receipt operations on one upload currently share a session gate.

### Queue scan blocked by an incoming part

A second defect was reproduced independently of Traefik: `Pending` first reconciles
live sessions, and both reconciliation and sweeping used a blocking session gate.
`Append` holds that same gate while reading a request body. A slow/incomplete
network upload could therefore stop discovery of every ready upload, despite
available finalizer workers. Longer proxy timeouts alone cannot fix this.

Background index recovery, pending discovery and sweeping now try the gate and
skip busy sessions until a later bounded scan. Foreground writes retain their
existing serialization and durability barriers. If a busy legacy session has no
index marker, recovery persists a live marker before completing the index upgrade,
so skipped work cannot disappear. Regression tests hold a real Append reader open
and require discovery of its completed peer plus a successful sweep; the original
implementation fails that test. Separate coverage checks legacy marker recovery.

The worker also emits a rate-limited aggregate count of queued uploads rejected
by device-grant checks. This distinguishes stale authorization from unavailable
workers without exposing credentials, manifests or filenames.

Queue-scan fix activated as `weazlcloud:release-ba6c920d48c7` at 19:14 UTC on
October 9. The API/renderer drained cleanly, a fresh 69,267-file reflink checkpoint
passed canonical hash/inventory comparisons, and activation/verification completed
in 21.9 seconds. Checkpoint:
`/exports/dockervolume/weazlcloud-backups/upload-queue-20261009/data`.

Validation passed: mobileparts and desk race suites, vet and source-size checks;
release-image renderer, Desk/WebDAV/Grab/restart/migration checks; extended mobile
smokes with 250-MiB fixtures on both storage backends at two CPUs/four GiB; public
TLS readiness, served assets and browser login. Resource limits and data mounts
were preserved. Owner unlock is required after this restart. Confirmation that
all old grants have been renewed still requires authenticated app reconciliation;
the rollout does not claim every queued production upload has completed.


## Catalog batching follow-up

A direct-LAN seed exposed a separate bottleneck: each private component and final
photo publication rewrote the full encrypted catalog. On this owner that catalog
was approximately 27 MB, including about 24,000 retained Drive entries. Several
finalizers contended on those durable writes while most CPU capacity stayed idle.

Photo component insertions and same-grant final publications now coalesce for
20 ms, up to eight requests per atomic catalog write. A private shadow transaction
validates each member with the existing mutation logic; conflicts are isolated.
No success is returned before the encrypted catalog and directory are synced.
Source mappings, album memberships, Live Photo pairs and journal changes publish
together. Device authorization keys include owner, device, authorization epoch,
expiry and scopes, and the current grant remains locked across publication.
Cancelled/stale-vault requests are excluded; groups with different grants never
share authorization. Storage intents and existing receipts remain compatible.

This keeps the encrypted catalog format and Restic layout unchanged. Restic
storage batching remains bounded and sequential per owner; catalog coalescing
allows more useful work per snapshot and pipelines storage with metadata work.
Eight finalizers are now allowed per owner on large hosts. There is no unlimited
worker setting and no need to resend previously accepted parts.

A local metadata-only benchmark with 24,000 retained rows and eight new component
updates measured 973 ms with individual saves versus 167 ms with one batch
(about 5.8x for this catalog stage, one sample, not an end-to-end throughput claim).


### Catalog-batch production rollout — 20:22 UTC

Release `f63cc3d4c58ae15eafc65730eb6a9180aceb709b` is live as
`weazlcloud:release-f63cc3d4c58a` for the API and isolated renderer. Production
reports `mobile finalize workers=8 per_owner=8`; container resource allocations,
environment, security controls, ports and data mounts are unchanged.

Validation passed: full `make check` (unit/race/vet/JS/native helpers/line limits),
focused failed-write and authorization-group tests, exact-release container
smokes on both storage backends, renderer probes, and 2-CPU/4-GiB extended mobile
smokes with 250 MiB uploads. Old-to-new upgrade fixtures passed on both backends:
three queued complete uploads resumed and an incomplete upload retained accepted
parts; hashes, receipt IDs, album membership, Hidden flags, Live Photo components
and stored replay survived. The 12-image empty-library comparison was roughly
unchanged (Restic stored-rate ratio 1.040; shared 0.994, single cold runs). This
is distinct from the 5.8x catalog-only benchmark above.

The old API and renderer exited zero. A complete XFS reflink checkpoint at
`/exports/dockervolume/weazlcloud-backups/catalog-batch-20261009/data` matched
canonical hashes and the sizes/modes/owners of all 63,206 files. There were 247
queued upload markers at the pre-cutover sample; staged parts and receipts were
preserved. Cutover completed in about 24 seconds. Public TLS readiness, exact
UI assets and login rendering passed. Private evidence is under
`/home/bobp/weazlcloud-releases/catalog-batch-20261009`.

Live throughput remains to be measured after the owner unlocks the vault again;
background finalization cannot run while it is locked. No reseed, credential
rotation or reset of the app's accepted parts is needed for this upgrade.
