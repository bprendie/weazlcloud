# Photos production rollout — October 1, 2026

Bob authorized production deployment after the local release checks and publication.
The earlier modal rollout ran `weazlcloud:release-2c4cede`, built from source revision
`2c4cede8a1b34707a7e7c34cc6a847fab60df6f6`. Its GitHub CI run
[36797709993](https://github.com/bprendie/weazlcloud/actions/runs/36797709993)
passed. Both API and isolated photo-worker containers are healthy.

## Preservation and recovery

The existing `/exports/dockervolume/weazlcloud:/data` bind, Restic backend,
accounts, vaults, hostname, ports, secure cookies, read-only import mount and
16-CPU API limit were retained. No source cleanup or storage migration ran.
Preview preparation was paused and the API stopped cleanly before copying the
complete data directory with `cp -a --reflink=always` on its reflink-enabled XFS
filesystem. Backup and cutover to readiness took 38.1 seconds.

- Consistent data checkpoint:
  `/exports/dockervolume/weazlcloud-rollbacks/2026-10-01-modal-2c4cede/data`.
  Both trees contained 268,794 files at the checkpoint; critical files compared
  byte-for-byte. This same-disk checkpoint is not an independent disaster backup.
- Private config/image/inventory records:
  `/home/bobp/weazlcloud-rollouts/2026-10-01-modal-2c4cede`.
- Retained previous image: `weazlcloud:rollback-modal-20261001`.

The photo worker has no network or vault bind, an 8-CPU / 16-GiB ceiling,
and only the private IPC volume. Its `/data` is an empty 1-MiB tmpfs overriding
Dockerfile `VOLUME /data`; the effective kernel mount was checked. The reference
Compose configuration now includes that override too.

Never point the older writer at the live catalog after new metadata edits.
Follow the release runbook's full restore/replay or compatible forward-fix rules.
Preview preparation was resumed after successful browser verification and its
queue was checked; originals and existing failed-media records were preserved.

## Verification and remaining gaps

All 37,082 photo entries and 16 imported albums reconcile by complete stable-ID,
path, size, date, flags, caption and membership-summary fingerprints. Three
sample originals are byte-identical through the new original API. Account/node
settings, vault envelopes and node keys match the checkpoint byte-for-byte.

All three local readiness ports return HTTP 200. Public Desk and Grab readiness
respond over certificate-verified HTTPS. Authenticated production Chromium
checks pass for desktop Photos, decoded preview/viewer/info/navigation, imported
albums, Library controls and mobile layout, without page errors or horizontal
overflow. Production tests did not create albums, spend grabs or delete files;
those mutation scenarios passed on both disposable local backends. Actual
Safari and complete large-library frame/RSS measurement gates remain open.

**Capture-date backfill remains required.** The existing catalog has 37,082
undated media entries and zero known capture dates. Takeout sidecars and originals
are retained. The code contains a checkpointed metadata migration library, but
no operator API/CLI currently invokes it. Add a safe durable, batched integration
before running the live backfill; do not repeatedly rewrite the whole encrypted
catalog per asset or introduce a competing offline writer. Missing dates stay
unknown rather than being replaced with import dates.

New derivative identities require preview regeneration; the older preparation
had 31,683 ready and 685 failed of 32,368 entries. New cache readiness counters
reset during reconciliation and should not be interpreted as lost originals.
The resumed job is a server background task, independent of browser lifetime.

## Later timeline release

The same-day timeline release `4ca6cf2` now supersedes `2c4cede` on production.
It adds the durable encrypted date-repair API and scoped rail. Fresh checkpoint,
full preservation reconciliation, live browser checks and the running supervised
dry-run/apply are recorded in [the timeline rollout record](photos-timeline-rollout-2026-10-01.md).
The unknown-date counts above describe the earlier baseline, not a final repair
result.
