# Thumbnail pipeline performance — October 1, 2026

Workbook: [Sol's thumbnail/cache workbook](../photos_thumbnail_cache_workbook_2026-10-01.md).
Local implementation and functional gates passed. Production rollout evidence is
recorded separately; the one-hour full-library preparation target is **unmeasured**.

Baseline revision: `3c410fd` plus the workbook and isolated measurement fixtures.
Local host: Intel Core i7-1365U, 12 effective logical CPUs, approximately 30 GiB
RAM; no tighter host cgroup limit. Go 1.27.0, Restic CLI 0.19.1, Node 26.7.0.
Docker builds use Go 1.25 and Restic 0.18.0; real reader tests passed against both
Restic versions. Native JPEG uses libjpeg-turbo, with runtime SIMD selection.
No host page caches were dropped. File-backed sources and repeated reads may
benefit from a warm OS page cache. Unrelated screenshots/cache artifacts were
preserved and excluded from the release.

## Reproducible local measurements

The four distinct synthetic 1200×800 sources are two JPEGs and two transparent
PNGs in the isolated local-file backend. Cold means a disposable derivative cache
was empty; it does not mean a cold production Restic repository. They lack stored
dimensions. Eight outputs are retained for a four-asset grid/viewer bundle pass.
The native run uses native JPEG plus Go PNG; the Go run uses Go for both.

| Case | Before | After |
| --- | --- | --- |
| Four assets, sequential separate 320 + 1280 requests | 511.223 ms; 7.824 assets/s; 16 source reads | Go: 497.814 ms; 8.035/s; 8 reads. Native JPEG mix: 382.443 ms; 10.459/s; 8 reads |
| Warm service thumbnail p50 / p95, 40 calls | 0.676 / 1.592 ms; zero source reads | Go: 0.003 / 0.005 ms; native mix: 0.003 / 0.006 ms; zero reads |
| One-worker full bundles, four assets | Not measured | Go: 406.204 ms, 9.847/s; native mix: 237.977 ms, 16.808/s; four reads |
| Two-worker full bundles, four assets | Not measured | Go: 272.061 ms, 14.703/s; native mix: 150.977 ms, 26.494/s; four reads |
| Four-worker full bundles, four assets | Not measured | Go: 175.540 ms, 22.787/s; native mix: 101.064 ms, 39.579/s; four reads |
| Eight-worker full bundles | Not measured | Not measured: local six-CPU photo ceiling; four background workers configured |
| Warm complete bundle pass, RAM cleared, four assets | Not measured | Go: 18.871 ms; native mix: 13.320 ms; zero source reads |
| 37,082-job dispatch, one iteration | 19.210 ms; 1,518,976 allocated bytes; 33 allocations | 0.033416 ms; 1,160 bytes; five allocations |
| 37,082-job build, one iteration | 2.309 s | 35.265 ms; 54,652,616 allocated bytes |
| 1,000-job dispatch, one iteration | 0.444 ms | 0.011511 ms; 1,160 bytes; five allocations |

This distinguishes separate foreground size requests from a coalesced bundle:
sequential requests made after the first job ends can read twice; simultaneous
requests and explicit background bundles share one source. Warm bytes are reused.
All four bundle assets and all eight requested variants succeeded. The source
copying component of this local-file baseline was only 0.186 ms: it primarily
measures rendering, encryption and cache overhead. A concurrent local browser
smoke ran during the Go sample, so these are indicative timings, not an isolated
codec comparison or a production ETA.

The baseline 100-decode synthetic PNG benchmark took 1.260551175 s, with
559,853,792 allocated bytes and 6,838,627 allocations. It is one repeated source,
not 100 distinct uploads, retained bundles or Restic reads.

Commands (the performance tests are opt-in and use disposable test directories):

```sh
go test ./internal/library -run '^$' -bench '^BenchmarkThumbnailDecode100$' -benchtime=1x -benchmem
WEAZLCLOUD_MEASURE_PREVIEWS=1 WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS=4 WEAZLCLOUD_PREVIEW_RENDERER=go go test ./internal/library -run '^TestThumbnailPerformanceRecord$' -v -count=1
PATH="$PWD/.build:$PATH" WEAZLCLOUD_MEASURE_PREVIEWS=1 WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS=4 go test ./internal/library -run '^TestThumbnailPerformanceRecord$' -v -count=1
go test ./internal/photos -run '^$' -bench '^BenchmarkMediaQueue' -benchtime=1x -benchmem
```

## Stage sample with real Restic 0.18

`TestThumbnailStagePerformanceRecord` uses one 1200×800 solid JPEG, 15,593 bytes,
a fresh tiny Restic repository and the native JPEG helper. It does not represent
production pack distribution. Reader startup includes authenticated index loading;
render timings aggregate decode/resize/encode rather than pretending to separate
those inside the native helper.

| Stage | Measured time |
| --- | --- |
| Persistent reader cold startup | 675.576 ms |
| Authenticated full original read after startup | 1.776 ms |
| First 320px output after render dispatch | 15.347 ms |
| 1280px output / bundle render completion | 40.690 ms |
| Grid JSON/encryption | 0.091 ms; 3,300 encrypted bytes |
| Viewer JSON/encryption | 0.151 ms; 32,028 encrypted bytes |
| Grid durable write, including encryption | 0.211 ms |
| Viewer durable write, including encryption | 0.201 ms |
| 37,082-job encrypted snapshot | 92.628 ms |
| One lease journal append / completion append | 0.180 / 0.048 ms; 1,012 bytes combined |

Run with the pinned Restic CLI and `.build/weazl-restic-reader` in `PATH`, plus
`WEAZLCLOUD_MEASURE_PREVIEWS=1`, selecting `^TestThumbnailStagePerformanceRecord$`.
The initial measurement fixture failed because it used a pre-assignment catalog
entry; it was corrected to reload authoritative metadata and the separate stage
run passed. No failed measurement is counted as a successful workload.

## Browser and small-host measurements

Both full Photos smokes passed with disposable **2-CPU / 4-GiB** containers,
one photo worker and a 512-MiB working allowance. The persistent reader falls back
to the bounded CLI on this profile so a resident index cannot starve decoding.
Chromium ran locally at 1440×1000 over loopback; mobile checks use 390×844.
The existing fixture has five logical photos, mostly identical tiny PNG content.
It is useful for UI/cache correctness and dedupe reuse, not large-image throughput.

| Measurement | Restic | Shared experimental |
| --- | --- | --- |
| Initial four cards / first decoded thumbnail | 366 / 490 ms | 416 / 474 ms |
| Photos API pair p95 | 9.285 ms | 7.691 ms |
| Date jump | 106.436 ms | 101.951 ms |
| Warm viewport p95 | 143.828 ms | 124.813 ms |
| Scroll script work p95 | 9.445 ms | 10.265 ms |
| 30 warm grid rebuilds: paint p95 | 41.7 ms | 38.8 ms |
| Browser hits / additional thumbnail requests | 150 / 0 | 150 / 0 |
| Retained browser cache | 965 bytes, one deduped entry | 965 bytes, one deduped entry |
| Rail release to cards p95 | 130.516 ms | 118.738 ms |
| Seek + summary HTTP pair p95 | 8.017 ms | 9.686 ms |
| Sample API RSS after browsing | 150,512 KiB | 153,832 KiB |

These are sampled API RSS values, **not combined peak RSS including children**.
Grid navigation made no original download requests. The 30 warm interactions pass
the 500-ms grid target; the 40 warm service calls pass the 100-ms thumbnail target.
A final navigation rerun after fixing a delayed-summary history race also passed
on both backends: warm grid paint p95 was 36.9 / 39.1 ms and rail release-to-cards
p95 was 132.9 / 120.7 ms (Restic/shared). Mouse drags starting on year labels,
one seek on touch release, keyboard navigation,
obsolete-request cancellation and the maintenance hamburger menu passed. A final
spacing check reserves the rail column for toolbar/filter controls as well as tiles.

The global jobs remain manually paused on production; no full rebuild was used
as a smoke test. Detailed local JSON/screenshot artifacts are under `/tmp/weazl-*`
and are intentionally not committed because browser screenshots can be private.

## Codec decision and functional evidence

A disposable Alpine 3.21 / libvips 8.15.3 exploration used two CPU / one GiB limits
and one native thread. Separate CLI 320/1280 renders of a 1200×800 transparent PNG
took 485.187, 574.655, 476.743 and 617.601 ms (four runs). That CLI test used tmpfs
outputs; it is not a production-safe shared-decode worker and is not a clean paired
comparison. Libvips was not adopted: equivalent private streaming bounds and a
measured advantage were not established. Existing accelerated JPEG and bounded
fallbacks remain. ThumbHash code is ISC licensed; licenses are shipped.

Passed gates:

- `make check`: all Go tests, vet, race detector, source-size gate and JS/cache tests.
- Native JPEG tests with normal and ASan/UBSan builds; orientation, transparency,
  supported native formats/video posters, malformed inputs and aggregate limits.
- Real persistent-reader tests against local Restic 0.19.1 and pinned 0.18.0;
  framing, cancellation, helper failure, new packs and graceful lock cleanup.
- `make smoke-container`, `make smoke-browser` and `make smoke-photos` on both
  storage backends, including restart, corruption/retry, Hidden/owner isolation,
  album/metadata edits, manual pause, viewer, upload and gallery downloads.
- Focused concurrent tests for early grid output, one-source bundles, surviving
  waiters, two owners, slow-neighbor refill, capacity reservations and RAM reclaim.
- Actual image rollback drill on both backends: new image → prior `3c410fd` → new
  image, exercising the old queue writer and preserving pause/IDs/originals/previews.
- Encrypted journal tests for legacy migration, incremental replay, torn tail,
  corrupt interior, failed writes, active leases and compatible rollback export.

## Open performance measurements

The following remain explicitly **not measured**: representative production
retained bundles/second by format, separate grid-only backfill throughput,
queue-wait distribution, combined API/reader/renderer peak RSS during a sustained
large pass, and the same foreground p95 baseline versus sustained backfill to
assess the 20% regression target. Decode, resize and encode are aggregated in the
native stage sample. Existing tests enforce admission and output bounds but do
not turn estimates into a hard combined-process RSS guarantee.

The 10.3 retained bundles/second needed for 37,082 assets in one hour remains an
open production performance target. Tiny synthetic local results exceeding that
rate do not close it. Obtain a sustained, authorized sample of remaining missing
bundles before making an ETA or raising production's preserved CPU limits.

CI initially exposed a fixture mismatch in the two-worker slow-neighbor test: the
queue used two workers while the runner's independently initialized render gate
allowed only one. The fixture now supplies matching two-worker admission gates
and bounded memory, independent of host defaults. The production resource policy
is unchanged. The corrected test is checked under one-worker environment defaults.
