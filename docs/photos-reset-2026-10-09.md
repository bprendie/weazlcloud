# Photos reset and Library loading repair — October 9, 2026

## Production reset

The owner requested an empty Photos collection before seeding it from iCloud.
The offline reset removed `/Photos` and pending mobile-photo components,
including hidden/archived files, photo trash, imported sidecars, photo albums,
collection/source mappings, device sync checkpoints, photo upload receipts and
derived preview/job state. Device credentials and account/vault settings remain.
Drive files and Drive trash remain unchanged. Existing frozen grab payloads were
not revoked or removed; they have independent retention.

| Verification | Result |
| --- | --- |
| Removed catalog entries | 73,110, including 73,056 non-folder entries |
| Removed logical bytes | 125,010,833,713 (includes JSON/other sidecars) |
| Previously mobile-associated entries removed | 56 |
| Retained Drive entries | 23,917, including 22,476 files |
| Retained Drive logical bytes | 646,027,487,495 |
| Retained metadata comparison | Identical SHA-256 before and after reset |
| Photo-only Restic snapshots retired | 17,880 |
| Remaining snapshots | 9,130; every retained file's snapshot exists |
| Restic repository check | Passed; this was not a full `--read-data` rehash |
| Sample original readback | Five files, 2,440,135 bytes; SHA-256 matched |

Restic reported pruning 94.817 GiB from the active repository. That is not the
same as filesystem free space: XFS reflink rollback copies continued to reference
the old data blocks. The volume initially still used 754,965,225,472 bytes while
the active data tree occupied approximately 589,967,208,448 bytes. `du` counts
reflinked blocks in each copy; summing those copies overstates physical usage.

The first reset helper ran as root. Atomic catalog replacement and Restic prune
created a root-owned private catalog and 28 repository files. Ownership was
restored to the service UID/GID, preserving restrictive modes. A subsequent
catalog inventory and all five sample reads succeeded as UID/GID 7272, and
production Library/Photos requests returned HTTP 200. The helper now preserves
catalog ownership/mode and refuses retirement/verification under a different
UID/GID. A Docker regression deliberately rewrites a UID-7272 catalog as root
and checks that access is restored.

Before the original reset, both containers were stopped. The app reported a
drain deadline rather than a successful drain; after verifying that both writers
were stopped, the complete owner repository/catalog/key state was reflinked.
Following reset verification, both containers shut down with exit code zero and
a fresh complete node checkpoint was created at:

`/exports/dockervolume/weazlcloud-backups/post-photos-reset-2026-10-09/data`

Catalog and key files were compared against the stopped source, and inspection
of the new checkpoint under the service UID confirmed zero Photos entries and
the unchanged Drive inventory. Production then restarted healthy on the same
release image. Historical rollback-copy retirement requires a separate operator
decision; it is not automatic when the live photo collection is reset.

The owner subsequently approved deleting these three older copies under
`/exports/dockervolume/weazlcloud-backups`:

- `mobile-15c45144b660`
- `photos-remediation-754465ea546f`
- `photos-reset-2026-10-09`

They were removed without stopping production. The verified post-reset copy
remains. Filesystem use fell from 754,910,347,264 to 742,174,367,744 bytes,
reclaiming 12,735,979,520 bytes (12.7 GB); free space became 1,455,774,097,408
bytes. The app and worker remained healthy and `/ready` returned storage ready.

An expanded volume inventory then identified ten additional historical rollback
copies outside that backup directory: `weazlcloud.pre-p6-20260922`,
`weazlcloud.pre-release-7bb01f4-20260928`,
`weazlcloud.pre-turbo-56cf829-20260928`, and seven October 1–2 copies under
`weazlcloud-rollbacks`. After separate owner approval, all ten were removed;
container references were checked again before deletion. The seven dated copies
were `2026-10-01-modal-2c4cede`, `2026-10-01-timeline-4ca6cf2`,
`2026-10-01-metadata-878fec4`, `2026-10-01-thumbnail-cache`,
`2026-10-02-photo-selection`, `2026-10-02-photo-selection-rail`, and
`2026-10-02-photo-selection-toggle`.

The second pass reduced filesystem use to 631,819,657,216 bytes and increased
available space to 1,566,128,807,936 bytes. It reclaimed another 110,354,710,528
bytes, or **123,090,690,048 bytes (123.1 GB) across both cleanup passes**. Live
data and the verified post-reset checkpoint were preserved; production stayed
online, both containers remained healthy, and `/ready` returned storage ready.
After deletion, all retained snapshot references and the five original-file
hash samples passed again under service UID/GID 7272. Read-only inspection of
the preserved checkpoint still found zero Photos entries and the unchanged
23,917-entry Drive inventory. Final filesystem totals matched the second-pass
measurement above. Private before/after disk reports and deletion logs accompany
the original reset reports in the maintenance directory.

Future space estimates must inventory the entire volume, not only the current
backup directory. Reflink copies share blocks, so their individual `du` sizes
do not predict how much each deletion will reclaim.

Reports, bounded snapshot retirement logs, pre/post Compose/container records
and the maintenance binary are retained privately under:

`/home/bobp/weazlcloud-maintenance/photos-reset-2026-10-09`

The client must perform a full Photos reconciliation/reseed. Old server cursors
expire, old source identities/receipts are gone, and the server must not report
previously uploaded photos as still stored. Clearing a native client's local
"already backed up" ledger, if it does not reconcile server resets, remains a
client operation. Existing device authorization does not need to be reissued.

## Offline maintenance helper

Build `go build -o photos-reset ./cmd/photos-reset`. It is an operator tool for
the owner's `/Photos` root on the Restic backend with stored unlock available.
It does not implement shared-store retirement or arbitrary selected-root resets;
it refuses shared-object Photos references. Running the binary without mutation
flags only inspects the owner. Do not run apply/retire against an active writer.

1. Stop/drain the app and worker; preserve the exact deployment settings.
2. Make a consistent complete owner rollback copy, including its Restic
   repository, vault/node key and catalog. A catalog-only backup is insufficient.
3. Run `photos-reset -data /data -user USER` and review the counts and catalog
   digest. The reset explicitly preserves the retained Drive metadata digest.
4. Run `photos-reset -data /data -user USER -apply -expected DIGEST
   -backup OWNER_COPY -report REPORT_DIR`. Catalog/key backup matches are checked
   before mutation. Relevant mobile transport records are copied before removal;
   large unfinished transport files cause refusal rather than buffering/deleting
   an unprotected upload. Keep reports outside the live owner cache directories.
5. Run `photos-reset -data /data -user USER -retire -prune -report REPORT_DIR`
   **as the catalog's service UID/GID**. Make that identity able to write the
   report directory. Retirement protects snapshots referenced by retained files,
   forgets only the reviewed owner plan, and limits each prune repack to 1 GiB.
6. While still offline, run `photos-reset -data /data -user USER -verify
   -report REPORT_DIR` with that same service identity. Verify catalog access as
   the runtime identity as well as repository integrity and original samples.
   `-verify-readback` skips the exclusive repository check for an online sample
   check; its report explicitly says that the repository check was not requested.
7. Restart, check owner Library/Photos responses, and retain or explicitly retire
   the rollback copy. Deletion is necessary to release blocks held by reflinks.

No credentials or unwrapped vault keys should be placed in commands or reports.
If apply fails after changing canonical state, keep writers stopped, inspect the
report and restore the complete consistent copy before retrying as appropriate.
Restoring just a pre-reset catalog after pruning will reference removed snapshots.

## Library loading diagnosis and local fix

A production goroutine trace found a preparation-status request decrypting and
validating the entire thumbnail cache under `photoPrepMu` for approximately
147 seconds. A Photos resume path could then wait for that lock while holding
the Library lock, stalling folder navigation. This was intermittent contention,
not a missing Library route or a permanently deadlocked process.

Cache reconciliation now runs once in the background with a cancellable owner
lifetime, outside the preparation lock. Publication checks generation/cache epoch
so a superseded scan cannot overwrite new preparation results. Status exposes
`cache_checking`; queue progress uses an already-loaded queue without blocking
behind its initialization. Vault lock/shutdown drains reconciliation too.

The regression deliberately blocks cache bookkeeping and the job queue while
requiring preparation status and folder listing to return, then checks shutdown.
Full `make check` passed, including Go tests, race tests, vet and JavaScript
checks. The separate authenticated Docker/browser smoke initially hit an existing
test race: its late Live Photo arrival check accepted the previous completed
job before the async outbox queued the new file. The smoke now waits for the new
queue total before accepting completion. The corrected smoke passed on both
Restic and shared-experimental 2-CPU/4-GiB fixtures, including late Live Photo
pairing, original-byte checks, Library uploads, bidirectional timeline scrolling
and guest playback/downloads.
The Library loading fix is now deployed; see the
[October 9 production rollout](library-loading-rollout-2026-10-09.md) for the
release identity, preservation checks and remaining authenticated-session check.

## Second reset: interrupted iPhone seed, 19:55 UTC

The owner requested a fresh Photos seed after duplicate entries appeared. The
second reset removed 1,017 photo/pending-component file entries (4,810,819,371
logical bytes), 376 albums, photo source/sync state, ingest receipts, photo parts
and derived caches. All 23,917 retained Library entries and 646,027,487,495
logical bytes have an identical retained-metadata digest before and after.
Accounts, device credentials, regular file backups and frozen grabs remain.

The maintenance helper now validates each encrypted multipart session's owner,
ID and `spec.kind` against its rollback header before deletion. It removes only
`photo` sessions and their `.live`/`.queue` markers; `file` sessions and payloads
survive. The old whole-directory removal was unsafe now that Photos and Files
share staging. A regression covers mixed photo/file sessions, missing rollback,
wrong owner, read-only inspection and retained file payload/markers. Targeted
Go race tests for the helper/catalog, helper vet, file-length and diff checks
passed. No runtime application rebuild was needed for this offline tool change.

Both services exited zero before a verified complete owner reflink copy at:

`/exports/dockervolume/weazlcloud-backups/photos-reseed-20261009/owner`

The reset/prune/verification ran as UID/GID 7272. It retired 969 photo-only
snapshots; the repository check passed with 9,130 retained snapshots, all 22,476
retained file references present and five streamed SHA-256 samples matching
(2,440,135 bytes). Post-restart inspection as the service identity confirmed
zero photo entries, zero albums and zero multipart sessions. Both containers
are healthy on `release-091de04ab396`; public TLS readiness, exact UI assets and
browser login smoke checks passed. Deployment settings were unchanged.

Private reports and the exact maintenance binary/script are at:
`/home/bobp/weazlcloud-maintenance/photos-reseed-20261009`.
Rollback copies remain, so reclaimed repository bytes must not be reported as
filesystem space freed. They have not been purged by this reset.

Before reseeding, the app must discard/reconcile its previous Photos checkpoint
and "already uploaded" ledger. Login/unlock is required after the service
restart. Reuse existing device credentials; the app can use content lookup on
this deployment. The inaccessible iOS client's local state was not modified.
The separate duplicate-review `formatBytes` UI error remains unfixed; the owner
switched to the reset before that patch was made.
