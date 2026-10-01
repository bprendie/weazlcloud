# Photos timeline production rollout — October 1, 2026

Bob authorized deployment and existing-data date repair after local acceptance.
Production runs `weazlcloud:release-4ca6cf2`, source
`4ca6cf287770e4fa642d3f071775fefea83eae03`. Both containers are healthy.
[CI run 36925632808](https://github.com/bprendie/weazlcloud/actions/runs/36925632808)
passed checks, container and browser jobs, including both-backend Photos smokes.

## Checkpoint and preservation

The effective Compose configuration changed only the two image tags. The
Restic backend, `/exports/dockervolume/weazlcloud:/data` bind, read-only import
mount, published ports, hostname, secure cookies, 16-CPU API ceiling and isolated
8-CPU / 16-GiB worker remain unchanged. The worker has no network or vault bind.
Preview preparation was paused for cutover and then resumed at the existing
8-worker / 8-CPU photo allowance with four source readers. Restart resets its
positional counters while the durable job store reconciles retained outcomes;
ready counts subsequently increased past the pre-cutover baseline.

- Fresh consistent checkpoint:
  `/exports/dockervolume/weazlcloud-rollbacks/2026-10-01-timeline-4ca6cf2/data`.
  Source and checkpoint both contained 275,048 files. Critical files compared
  byte-for-byte after the copy. This same-disk reflink checkpoint is not an
  independent disaster backup.
- Private config, inventory and operator records:
  `/home/bobp/weazlcloud-rollouts/2026-10-01-timeline-4ca6cf2`.
- Previous image retained as `weazlcloud:rollback-timeline-20261001`.

No imports or uploads were active at the baseline. The first stop attempt hit
an HTTP shutdown deadline; the gate restored the old service and made no backup
or new-image cutover. The second attempt stopped cleanly and used
`cp -a --reflink=always` while the API was down. Its checkpoint/cutover took
43.9 seconds to new readiness. Do not put an older writer against newer metadata;
follow [the recovery runbook](photos-release-runbook.md).

After deployment, complete metadata fingerprints matched for 96,971 Library
entries, all 37,082 photo/video identities, paths, sizes and preserved owner
fields, and 16 album definitions/memberships. There were no archived or Hidden
entries in this dated baseline. Three sample originals were byte-identical.
Account/node settings, storage format, vault envelopes and node keys matched
the new checkpoint byte-for-byte. These are full metadata reconciliations plus
sample byte checks, not a re-download of every original.

Public Desk and Grab readiness pass over certificate-verified HTTPS.
Authenticated desktop/mobile Chromium checks pass for grid, decoded viewer,
albums, Library-mode return, bounded cards, rail visibility and mobile overflow,
with no page errors. The warmed mobile check mounted 18 cards. It did not create
albums, change capture dates, spend grabs or delete files.

The first browser attempt exceeded 30 seconds during cold album metadata reads.
Observed API logs included a 47.9-second album request and a 40.3-second seek
waiting behind cold work. The warmed retry passes. This remains a responsiveness
gap; tiny-fixture warm timings are not a claim that production cold unlock meets
the same target. Actual Safari and sustained full-library frame/RSS gates remain
open.

## Existing-data repair is running separately

The live baseline has zero known capture dates and 37,082 unknown dates. The
new rail is available on Timeline and scoped album/filter views. An existing
browser tab needs a reload to execute the deployed JavaScript. Before capture
repair, the rail has an Unknown date control and no year markers. Recently added
intentionally has no capture-date rail.

An owner-authenticated full dry run started at **23:32:03 UTC** on October 1.
Its root is `Photos`, parser is `capture-v2`, and embedded parsing is limited to
JPEG EXIF. Conventional/supplemental sidecars supply dates for other formats.
The first 100 entries all have exactly one conventional or supplemental sidecar
candidate. That naming check is not proof that all contents are valid.

The dry run reads sources and persists its own encrypted checkpoint/report; it
does not change catalog dates or revisions. It checkpoints processing batches
of up to 100 results, so examined counts may remain unchanged during source reads.
Source reads have noticeable Restic startup/index overhead on this repository.
The first durable batch checkpoint at **23:49:08 UTC** reports 100 examined,
98 recoverable updates and two failed reads. These are dry-run candidates, not
applied catalog dates. Both failures remain recorded and did not stop processing.
No completion time is promised from a partial first batch.

A detached one-time supervisor runs on the host, independent of SSH/browser
presence. It waits for the complete dry run and inspects all outcome pages:
entry/count consistency, unique IDs, valid Photos paths and source hashes,
recoverable updates, provenance and per-asset error categories. It retains a
private aggregate report, then starts the authorized batched apply. Individual
corrupt or ambiguous entries remain skipped rather than blocking valid assets.
Explicit pauses and persistent storage errors are never automatically resumed.
The supervisor polls locally once per minute and logs progress every 20 minutes
plus phase transitions. Credentials/session material remain in process memory;
none are written into helper files, arguments or logs.

The service's owner job remains the durable authority. A service restart resumes
queued work after owner unlock. The temporary supervisor itself does not survive
a host reboot; if it disappears, inspect the existing job before continuing the
handoff. Reuse the authenticated API, never an offline competing catalog writer.
Private per-file details remain in the owner's encrypted job/report; operator
JSON records contain counts and fingerprints rather than raw path lists.

After apply, the supervisor compares all original source identities against the
dry run and re-runs complete Library/photo/album preservation fingerprints and
sample original-byte checks. A mismatch is reported for review, never cleaned up
or silently declared preserved. The before/after date and job reports remain in
the private rollout directory. Real dated UI navigation and final live date
counts still require completion; this deployment record does not close them.

On the host, inspect progress without credentials:

```sh
cat ~/weazlcloud-rollouts/2026-10-01-timeline-4ca6cf2/controller.state.json
tail -n 5 ~/weazlcloud-rollouts/2026-10-01-timeline-4ca6cf2/controller.log
```

The controller's final event is `repair_completed_and_reconciled` on a clean
comparison; `reconciliation_needs_review` or `supervisor_stopped_for_review`
requires investigation. No originals, sidecars, duplicate entries, repositories,
ZIPs or rollback material are deleted by deployment or date repair.
