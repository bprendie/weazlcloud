# Capture-date extraction acceleration — October 1, 2026

Target: extract dates for the 37,082 existing logical photo/video assets in
about one hour (10.3 assets/second), with per-file failure isolation, encrypted
checkpoints, bounded memory, and the live catalog as the only writer.

## Diagnosis and approach

The original job resolved assets serially and launched `restic dump` for each
JSON sidecar or JPEG prefix. Restic opens/unlocks the repository and loads its
index for every invocation. The first 100 production assets took about 17
minutes; increasing concurrency alone would still duplicate that index work.

Immich's useful model is independent background queues, configurable concurrency,
and serving already-indexed metadata to the UI. References:
[workers](https://docs.immich.app/administration/jobs-workers/),
[architecture](https://docs.immich.app/developer/architecture/), and
[Restic's dump implementation](https://github.com/restic/restic/blob/v0.18.0/cmd/restic/cmd_dump.go).
Adding a database container does not remove repeated repository opens from this
existing-data extraction path.

WeazlCloud now opens one authenticated Restic reader per active owner metadata
job, holds one refreshed shared repository lock, and shares its loaded index
among bounded readers. Restic remains pinned to v0.18.0 and performs its normal
blob authentication. The owner catalog authorizes each source before reading;
complete sidecars are also checked against their recorded SHA-256. Original
media reads stop at a 4-MiB prefix. No plaintext staging is required.

The job resolves up to eight assets concurrently, bounded further by the
existing background/source-reader policy. It publishes one catalog batch and
encrypted checkpoint at a time. Pause, cancellation, lock, and shutdown drain
the readers; incomplete batches remain pending. Ambiguous/corrupt files are
reported separately. A fresh CLI read handles new packs uploaded after index
load or an unavailable helper. Shared-object storage retains its native reader.

The helper reserves up to 2 GiB of the shared preview memory allowance plus
bounded protocol buffers. Small machines scale the allowance down; allocations
below 256 MiB retain the CLI fallback. `GOMEMLIMIT` is a heap target, not a hard
RSS limit. Set `WEAZLCLOUD_METADATA_READER=off` to disable the helper while keeping
parallel resolution. Existing checkpoints are readable without migration.

## Production read-only sample

An owner-authorized standalone reader sampled 1,000 existing sidecars across the
35,507 small JSON files in `/Photos`, selected at even intervals by content hash.
It opened the actual production repository while the old service kept running.
Only normal temporary Restic shared locks were written; no catalog dates changed.

| Measurement | Result |
|---|---:|
| Reader workers | 8 |
| Repository open and index load | 4.280 seconds |
| Read, SHA-256 verification, and parse | 0.318 seconds |
| Verified / read failures | 1,000 / 0 |
| Valid capture dates in sample | 999 |
| Sidecar bytes | 736,152 |
| Sample throughput after open | 3,146 files/second |
| Maximum reader RSS | 421,156 KiB (about 411 MiB) |

This is an extraction benchmark, not a promise that complete repair takes 12
seconds. The service still validates candidate matches, reads JPEG metadata when
needed, checkpoints reports, and encrypts/catalogs batches. Dry-run and apply
each open a session; the expensive index is loaded once per pass. Persisting
resolved dry-run captures could avoid the second small-data pass, but it is no
longer necessary to remove per-file index overhead and is not part of this patch.

## Validation and rollout

Tests cover real Restic parallel reads and prefix parity, missing-file recovery,
graceful shared-lock cleanup, new sources after an index opens, stale catalog
references, locked vaults, and parallel pause/resume without false progress.
Validation passed: `make check` (Go tests, race detector, vet, source-size and
JavaScript gates), the Docker build, container restart smokes on both storage backends, and the
authenticated Photos/timeline browser smokes on both backends. The Photos smoke includes late
sidecar repair, corrupt-neighbor isolation, repeated apply, source-byte equality,
and pause/restart recovery. Production rollout and service throughput are
recorded below after measurement.

Production cutover keeps the effective Compose settings and data mounts unchanged,
including the 16-CPU API allocation, eight photo slots, four background/source
readers, and isolated eight-CPU render worker. The first stop encountered the
existing five-second HTTP shutdown deadline. The cutover gate restored the old
container and took no checkpoint from that attempt; the subsequent quiet stop
is required to exit cleanly before the new consistent reflink checkpoint.

The initial GitHub run found a parity-test fixture incompatibility: Restic
v0.18.0 does not accept the newer local CLI's nested `--stdin-filename` fixture.
The test now creates a real nested batch, matching imported production storage,
and passes against the exact v0.18.0 binary from the Docker image. This correction
changes only the test fixture, not the deployed reader.

Production now runs `weazlcloud:release-878fec4`. The successful cutover took
44.3 seconds, with both containers healthy. The new consistent checkpoint is
`/exports/dockervolume/weazlcloud-rollbacks/2026-10-01-metadata-878fec4/data`
(275,906 files). Private configuration and records are under
`/home/bobp/weazlcloud-rollouts/2026-10-01-metadata-878fec4`.
Full Library/photo/album fingerprints and three sample originals matched again
before resuming the existing dry run at 00:35:23 UTC on October 2 (October 1
local time), with 200 already examined and four workers. Thumbnail preparation
is temporarily paused to prioritize extraction. The updated detached supervisor
will inspect the completed dry run, apply, reconcile, and restore preparation
unless the owner has changed its pause state in the meantime. Its reports remain
in the original timeline rollout directory.
