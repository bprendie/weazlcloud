# Capture dates and timeline navigation — October 1, 2026

This release adds a continuous Photos date rail and an owner-private background
capture-date repair job. Local checks use disposable data. Production's existing
37,082 undated media entries have not been repaired by these checks.

## Behavior and preservation

The rail navigates by scoped asset rank. Dragging updates the cached month badge;
release makes one bounded seek request. Year buttons, an exact-day picker,
Unknown date, daily/monthly keyboard navigation and dated endpoints use the same
collection as the grid. Favorites, albums, Hidden and composite searches retain
their scope. Viewer return and browser history retain a stable anchor; a missing
anchor can fall back to its capture day. Old year/month controls explicitly filter.
The rail avoids an open upload tray; an expanded tray that leaves insufficient
vertical room temporarily hides it. Ordinary scrolling and date filters remain.

The repair job records encrypted owner outcomes, commits at most 100 capture
changes with one catalog save and checkpoints after that commit. A checkpoint
failure retains pending work; resume compares against durable catalog state.
Catalog write failures retry three times before pausing. Concurrent source changes
retry independently; corrections, albums, IDs, original references, captions,
favorites, rotations and Hidden flags are preserved. Bad or absent metadata is
reported per asset. Neither completion nor failure deletes anything. Dry runs keep a separate encrypted
checkpoint; monotonic durable sequences select the current job after restart
without filesystem clock ordering. Inspecting completed work preserves its apply
checkpoint byte for byte.

Takeout `photoTakenTime` precedes JPEG EXIF and mobile/client dates; owner edits
always win. Import time, filesystem mtime and Takeout `creationTime` are excluded.
Offsets are retained when known and never inferred from a browser/server zone.
Conventional and supplemental JSON names are matched first; truncated/collision
names need an unambiguous title match within the same directory. Ambiguity is
reported rather than guessed. Embedded capture extraction currently supports
JPEG EXIF only; other formats can use Takeout sidecars. Late sidecars queue repair
of existing media, and queued ingestion cannot be consumed by an older checkpoint.

Sidecars and JPEG prefixes are bounded to 4 MiB; oversized sidecars are rejected.
The Restic path uses streaming `dump`, with cancellation at the bounded writer;
it does not restore the original to a temporary file. Shared reads use bounded
encrypted segments/chunks and stop on writer cancellation. These paths can read
or decrypt a backend block beyond the requested prefix; this is not a promise of
exactly 4 MiB of physical I/O. Tests use a synthetic 1-TiB source without allocating
it and confirm at most 4 MiB reaches the metadata writer. Navigation reads no
originals. Repair shares background admission/readers/memory with preview work;
it does not increase the configured preview CPU allowance. New Photos uploads
and sidecars carry a canonical pending-processing marker in the same commit as
their original reference. After restart, sidecar directories replay into durable
metadata work before the marker is acknowledged. An import's lost in-memory queue
cannot silently lose a late date sidecar.

## Reproducible checks

```sh
make check
make smoke-container
make smoke-browser
make smoke-recovery
make smoke-photos PHOTOS_PYTHON=/path/to/playwright-venv/bin/python
go test ./internal/library -run '^$' -bench '^BenchmarkPhotoNavigation$' -benchtime=100x
```

Normal/race tests cover dry-run preservation, encrypted checkpoint reload,
pause/restart/resume, idempotence, manual correction, renamed/out-of-root sources,
stale queued outcomes, catalog/checkpoint failures, offset calendar boundaries,
owner/scope-bound cursors and deleted-anchor continuation. HTTP isolation checks
include ordinary/Hidden summaries and seeks plus another owner's job status.
The catalog batch test observes two changed assets and one catalog generation
advance; a stale update and failed write do not advance authority.

The constrained Chromium smoke uses the existing five-photo fixture on both
Restic and shared storage, with 2 CPU and 4 GiB allocated to each node. It checks
rail seeking, viewer return, browser Back, Library-mode return after a metadata
change, Home/End, exact days, a delayed obsolete
response, one request per desktop/touch release, mobile overflow, ordinary touch
scrolling, automatic late-sidecar repair, a corrupt neighbor and byte-identical
originals after repeated repair. Existing upload/preparation/gallery/restart
checks remain part of that run. No 500-image upload was added.

## Measurements and limits

Warm metadata-only seek plus summary microbenchmarks, 100 repetitions:

| Synthetic entries | Average pair | Pair p95 | Allocated bytes per pair |
| --- | --- | --- | --- |
| 37,082 | 0.081 ms | 0.336 ms | 114,328 |
| 100,000 | 0.098 ms | 0.132 ms | 114,328 |

These use a prepared private index on the development host; they exclude cold
unlock/index construction, storage reads, browser layout and thumbnail decoding.
They are not proof of full-library browser responsiveness.

The smoke writes tiny-fixture observations to
`/tmp/weazl-timeline-{backend}-performance.json` and the existing modal measurements
to `/tmp/weazl-modal-{backend}-performance.json`. Five reloads and twenty warm
viewport visits are measured separately from twenty rail jumps. The rail record
contains release-to-mounted-cards p95, seek/summary HTTP pair p95, active and idle
animation-frame intervals, and a sampled node-process RSS. RSS is a sample, not a
peak; a snapshot containing only the API does not measure simultaneous decoder
memory. ScriptDuration is reported separately and is not whole-frame latency.

Final local checks all exit successfully: `make check` (normal, race, vet, Go
size and JavaScript checks), both-backend container/browser/Photos smokes, and
filesystem recovery. The focused metadata race suite also passes with real
Restic/shared restart fixtures. OpenAPI YAML parses and local references resolve.

Final constrained tiny-fixture observations (milliseconds unless noted):

| Measurement | Restic | Shared |
| --- | --- | --- |
| Rail release to mounted cards p95, 20 jumps | 72.01 | 73.45 |
| Seek + summary HTTP pair p95, 20 requests | 4.18 | 4.08 |
| Active / idle animation-frame interval p95 | 16.7 / 16.7 | 16.7 / 16.7 |
| Five page reloads, median | 151.55 | 153.37 |
| Twenty warm viewport visits p95 | 86.96 | 118.04 |
| Sampled API RSS, KiB | 150,124 | 153,320 |
| Original requests during grid scrolling | 0 | 0 |

Warm HTTP and card targets pass. Earlier frame runs ranged 16.7–16.8 ms;
this small Chromium fixture alone cannot establish a universal 60-fps guarantee.
Page reloads reuse the running server and are not cold unlock/index measurements.
Real cold production unlock/index cost, sustained mixed upload/render/repair RSS,
real-library frames and actual iOS Safari remain separate measurement gates. Chromium touch emulation does not establish Apple-device
compatibility. No SQLCipher/database cutover or storage migration is included.

Published source: `4ca6cf287770e4fa642d3f071775fefea83eae03`.
[GitHub CI run 36925632808](https://github.com/bprendie/weazlcloud/actions/runs/36925632808)
passed checks (including native sanitizer), container and browser jobs.

Bob subsequently authorized production release and existing-data repair (T6).
The network restriction was resolved. The tested release is live, with a fresh
consistent checkpoint and passed preservation/HTTPS/desktop-mobile browser
checks. A supervised owner-private full dry run is running; apply follows
inspection of its completed report. Final live date counts and dated browser
verification remain open. See [the production timeline rollout record](photos-timeline-rollout-2026-10-01.md).
