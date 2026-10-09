# Photos release and recovery runbook

Updated October 2, 2026. See the [Photos selection release](release-2026-10-02.md)
and [Compose guide](docker-compose.md) for the current UI and deployment settings.
See [the modal rollout record](photos-production-rollout-2026-10-01.md) and
[the timeline rollout record](photos-timeline-rollout-2026-10-01.md) for the preserved
settings/data checkpoint, reconciliation and remaining capture-date backfill.
See also the [thumbnail/cache rollout](photos-thumbnail-rollout-2026-10-01.md)
for the graceful-shutdown fix, consistent checkpoint and live reconciliation.
The runtime keeps the encrypted catalog authoritative. SQLCipher is a prototype,
not a new live database or a dependency needed to enable these features.

For an intentional empty Photos collection before an iCloud reseed, see the
[October 9 reset procedure](photos-reset-2026-10-09.md). It preserves Drive and
device authorization, expires old sync state, and explains rollback-copy disk
retention and verification under the service UID.

## Data and volume inventory

Keep the existing data volume mounted at `/data`. Do not substitute a fresh
Compose named volume for an existing bind mount. Vault settings, passwords,
approved accounts, hostname, source files and imported memberships stay there.
The photo worker mounts only `/run/weazlcloud-photo` through the private IPC
volume; it has no network, no vault volume and no private path or key.

| Material | Backup/recovery policy |
| --- | --- |
| Node settings, account store, vault envelopes and node keys | Preserve as part of a consistent node backup; keep credentials private. |
| Owner `catalog.enc` | Authoritative identities, capture edits, hidden flags, custom albums, pair/source identity, processing outbox, journal and checkpoints. Back it up with the owner's vault and originals. |
| Restic repositories or shared manifests/objects/key envelopes | Preserve the configured backend and all its required keys. A Photos UI update never migrates or deletes these. |
| `.weazl-photo-ingest/*.enc` | Durable logical upload receipts. Keep with the vault; completed receipts prevent duplicate uploads after lost replies. |
| Node `uploads/` | Existing byte-engine manifests/chunks and unfinished staging. Preserve active sessions or explicitly cancel them. Unfinished sessions retain for 24 hours. |
| `.weazl-photo-jobs.enc`, `.weazl-photo-jobs.journal.enc` and preparation state | Durable per-asset jobs, attempts and leases. Pending catalog markers on Photos uploads and sidecars retry checkpoint creation after unlock/reload. |
| `.weazl-photo-metadata.enc` / `.weazl-photo-metadata-dry-run.enc` | Encrypted capture-date repair options, source checkpoints and private per-asset outcomes. Preserve it with the catalog. Queued/running work resumes after owner unlock; paused work stays paused. Dry-run output preserves the apply checkpoint; a durable sequence selects the latest job. |
| `.weazl-photos-index.enc`, preview/failure/thumbnail caches | Encrypted derived data. Rebuild from the canonical catalog and originals; clearing a cache must not erase albums or edits. |
| `.weazl-live-photos.enc` / `.weazl-heic-scratch-recovery-v1.enc` | Encrypted owner Live Photo discovery checkpoint and one-time targeted HEIC recovery marker. Preserve pause and candidate state. |
| `.weazl-photo-selections/*.enc` | Owner/revision/visibility-bound 90-minute selections. Expired selections are recreated; do not treat them as permanent albums. |
| Owner archive directory `.enc` + `.wza` | Encrypted ZIP jobs and indexed encrypted ZIP output. Queue recovery is lazy on the first owner request. Ready output retains for 90 minutes. |
| Capsule directories and `gallery-session.key` | Frozen grab originals/derivatives/ZIPs, encrypted selected-ZIP checkpoints, capsule-only key material and admission counters. Keep until burn/expiry/revoke; account deletion removes the owner's material. |

Check directory names through the configured owner library/user paths rather
than guessing an alternate volume. Take consistent backups with active writes
settled; copying one catalog while ignoring new upload receipts, object manifests
or capsule counters is not a complete restore point. Restore detection forces
native clients to reconcile a fresh snapshot instead of replaying future cursors.

## Required checks before a future rollout

Run in an environment that permits Docker and local TCP/Unix listeners:

```sh
make check
make smoke-container
make smoke-browser
bash scripts/test-native-preview.sh
# Install Python Playwright and Chromium first. Uses disposable 2-CPU/4-GiB nodes.
make smoke-photos PHOTOS_PYTHON=/path/to/playwright-venv/bin/python
```

The authenticated Photos smoke covers preparation, Library controls, dated
viewer navigation, album editing, guest ZIP transfers and explicit Hidden
surfaces on both backends, including hover/touch selection, deselection, bulk
Archive/Hidden restore and selection toolbar clearance beside the date rail. CI runs it after building the container. Exercise
Safari and Chromium with representative JPEG/PNG, transparency, progressive
JPEG, HEIC/AVIF, portrait/mirrored EXIF and browser-playable videos. Unsupported
codecs show a placeholder or download fallback; ordinary videos have no general playback transcoder or wide-gamut fidelity promise.
Paired Live Photos now have a bounded compatible motion preview; see the
[October 2 remediation](photos-browsing-remediation-2026-10-02.md). Native PhotoKit
animation and physical-device codec coverage remain device validation.
A logical still/motion backup preserves both original components.

Measure five cold and twenty warm runs on the workbook's 2-CPU/4-GiB reference
setup: first viewport, date jump, metadata p95, frame p95, worker RSS and original
source reads. Keep cold unlock/index costs separate from warm queries. Repeat
browsing during upload, import and backfill. Metadata-only microbenchmarks are
recorded separately and cannot close those browser/resource gates.

## Dry-run reconciliation

Before enabling any future SQLCipher query-store cutover, inventory the owner's
canonical catalog with stable IDs, selected roots, logical assets/components,
folder/custom albums, capture provenance and membership counts. Run the existing
`internal/photos` dry-run migration and `CompareProjection` against the candidate
projection. A valid comparison has zero missing/unexpected assets and zero
root/path/revision/owner/capture/media/component/source/membership mismatches.
Report missing metadata separately; leave those capture dates unknown.

Local fixture evidence for this pass: the two-asset migration fixture reports
`Examined=2, Updated=2` with zero writes in dry run; real application reports two
updates, then a repeated run skips both. Stable-ID rename and still/motion fixtures
preserve identities and memberships. Deliberately changing component revision or
source revision raises the corresponding comparison mismatch. The 32,000 and
100,000 row metadata benchmarks read no original files. These are disposable
fixture results, not a production inventory or proof of DB/WAL recovery.

The socket-free native simulator additionally tests filesystem backup/restore
on both storage backends: three logical assets/six original components, one
custom album with three members, two device credentials, a hidden folder,
completed mobile receipts, one ready encrypted owner ZIP and one frozen gallery
with a preserved admission counter. It drains private work and closes the shared
SQLite store before copying all node data to a new directory. Removing the derived
Photos index does not affect restored canonical metadata or original readback.
This is a clean filesystem recovery fixture, not a crash during WAL writes or an
actual process/container reboot. Those release checks remain separate.

No automatic cleanup of duplicate originals, old repositories or rollback data
is part of this release. Date-only metadata changes reuse pixel cache identities.

## Restart, failure and rollback

For date repair, use **Inspect dates** before **Repair dates**, or the owner
`/api/v1/photos/metadata-jobs` endpoint documented in [photo-api.md](photo-api.md).
Keep the private report; unresolved/ambiguous/unsupported cases are not permission
to delete their sources. A running job continues without the browser. A locked
vault cancels work and resumes queued work after unlock; an explicit pause persists.
`paused_error` requires fixing storage and explicitly resuming. Commit-before-job-
checkpoint recovery compares the current catalog instead of applying twice.
Do not run the old sequential `BackfillPhotoMetadata` helper with a plaintext
checkpoint against live data; the service operator path is encrypted and batched.
Date-only changes reuse existing pixel caches. See the
[timeline verification record](photos-timeline-verification-2026-10-01.md).

1. Preserve the current image/config and consistent data backup before rollout.
   Check active imports/transfers/jobs and settle or checkpoint affected workers.
2. Retain backend, worker CPU allowance, hostname and mounts. Worker limits remain
   at most half the effective CPU allocation with memory/thread admission; a small
   deployment uses the same APIs and bounded streaming paths.
3. On restart, unlock the vault through local identity. Durable preview leases
   recover; pending catalog processing markers retry. Requesting an owner archive
   reloads queued/ready jobs. Polling a queued guest ZIP resumes from the frozen
   capsule without the owner vault. Browser closure does not cancel accepted jobs.
4. A failed photo/ZIP job records a bounded error instead of blocking unrelated
   work. Retry the preparation explicitly; do not report an incomplete original
   as stored. Corrupt media may back up successfully while preview processing fails.
   Guest ZIP status detects missing/truncated output and supports explicit rebuild
   from the frozen originals without spending a grab. A failed gallery derivative
   displays a placeholder while the stored original remains downloadable.
5. Source or album edits do not change frozen grants. Browsing/preparation spends
   no retry; download admission spends one even if interrupted. Final admission
   rejects new guests while earlier admitted transfers settle. Explicit revoke
   cancels transfers; expire/burn cleanup retries removal on storage errors.
6. If the UI is rolled back, retain the new authoritative metadata. An older binary
   that rewrites an envelope without understanding albums, journals, pair identity
   or outbox fields can lose those additions. Use an isolated copy or a compatible
   forward-fix image; do not place an unaware writer against the live new catalog.
   A full data rollback needs a consistent restore point and intentional replay
   of post-backup edits/uploads/admissions, not just an image downgrade.

Account deletion remains final removal of that owner's files, upload data,
albums, device credentials and frozen grants. Hiding a photo or folder is only
Photos presentation within the owner's vault and is neither deletion nor grant
revocation. Preserve individual asset hidden flags as well as inherited folder
flags; an older image that understands only folder hiding can expose those
assets in its Photos views.

## Thumbnail cache / queue upgrade (October 1, 2026)

The version-1 encrypted job snapshot remains readable by the previous image.
New operations append authenticated, sequenced encrypted records to
`.weazl-photo-jobs.journal.enc`; compaction atomically publishes a snapshot before
clearing that journal. A torn final frame is truncated; complete corrupt records
pause processing. Keep both files in the same consistent recovery point.

Before an image rollback, **unlock and cleanly drain the new image** so it exports
all journal state into the compatible snapshot. Verify the stop/drain succeeded.
Never remove the only journal or downgrade after a forced stop without first
replaying/exporting it with the current image. A locked shutdown cannot decrypt
and export pending journal changes. Existing original/catalog compatibility rules
above still apply. The `WEAZLCLOUD_PREVIEW_BUNDLE=off` and
`WEAZLCLOUD_PREVIEW_READER=off` switches provide forward fallbacks without an image
rollback or private-data restore.

Cache filenames and legacy JSON envelopes stay compatible. New envelopes add
optional size/ThumbHash data; `<content-key>.meta.enc` files are small encrypted
manifest segments and count toward cache bounds. Cache corruption is a miss,
not evidence of a stored original failure. Do not wipe these directories to test
an upgrade. Explicit limits are preserved; unset byte caps use adaptive space
sharing and a 200,000-record owner bound. A 2-CPU/4-GiB deployment retains one
worker and a 512-MiB preview allowance, using CLI reads when a resident index
would not leave room for a full decode. Keep separate API/worker cgroup budgets;
concurrency knobs alone are not a hard combined CPU or RSS limit.

An isolated image rollback drill is available after building the previous and new
images. It always creates a disposable volume and loopback port; it never accepts
a production volume. Run it for both storage backends:

```sh
WEAZLCLOUD_ROLLBACK_IMAGE=weazlcloud:previous python3 scripts/smoke-photo-rollback.py
WEAZLCLOUD_ROLLBACK_IMAGE=weazlcloud:previous WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental python3 scripts/smoke-photo-rollback.py
```

The October 1 release passed this drill against prior revision `3c410fd`: prepare
both variants, pause, clean unlocked drain/export, boot the previous image and
exercise its queue writer, then return to the new image. Both backends retained
IDs, original hashes, derivative hashes and the manual pause across image changes.

Normal host shutdown closes admission and authenticated event streams, waits for
HTTP/maintenance work, checkpoints archive workers without deleting ready ZIPs,
then drains each loaded owner library before locking its vault. This exports the
photo journal while keys are still available and closes persistent reader locks.
The bounded shutdown allowance is 60 seconds; configure container stop grace
above that (the supplied Compose uses 90 seconds). A failed drain is an error, not a verified checkpoint. The regression
test keeps an SSE connection open and verifies journal export plus retained ZIP
bytes. The image drill additionally checks an empty exported journal after stop.
