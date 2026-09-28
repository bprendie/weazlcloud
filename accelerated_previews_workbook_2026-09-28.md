# Accelerated previews — September 28, 2026

Goal: ship measured JPEG preview acceleration to production without changing
vaults, originals, quota, permissions or the portable Go executable.

1. **Implement:** isolated libjpeg-turbo helper, runtime SIMD dispatch, scaled
   JPEG decode, bounded pipes, cancellation/timeout, strict dimensions/input/output
   limits, no plaintext disk intermediates. Keep Go for PNG/GIF and portable hosts.
   Add `WEAZLCLOUD_PREVIEW_RENDERER=auto|go|turbo`; invalid/required-missing rejects
   startup. Maintain existing global worker/read/memory admission limits.
2. **Verify:** compare actual JPEG output dimensions/content, test malformed,
   oversized, truncated and progressive inputs, cancellation and helper failure,
   Go fallback and parallel rendering. Run native sanitizers and Go race tests.
3. **Measure:** repeat renderer-only benchmarks at photo resolutions with Go,
   native runtime SIMD and native SIMD disabled; distinguish scaled decode gains
   from SIMD gains. Use synthetic fixtures, no private photo copies. Keep platform
   compatibility and single-thread helper work bounded by the existing scheduler.
4. **Package:** build helper in Docker with libjpeg-turbo; document settings,
   limits, cache version and rollback. Run full checks and constrained container
   and browser smokes; commit/push and inspect CI.
5. **Roll out:** keep preparation paused until validation. Preserve production
   Compose/mounts and vault data; retain prior image and rollback snapshot. Deploy,
   verify login/catalog/original readback and JPEG previews. Resume preparation,
   observe advancing counters and parallel children, then leave it running.

AVX availability is not proof that every operation executes AVX. libjpeg-turbo
selects SIMD per operation and CPU; there is no forced AVX/AVX-512 build baseline.
Sources: https://libjpeg-turbo.org/About/SIMDCoverage and
https://github.com/libjpeg-turbo/libjpeg-turbo/blob/main/simd/README.md.

## Implementation and local evidence

The native worker is `native/preview/main.c`; dispatch lives in
`internal/library/thumbnail_turbo.go`. Docker installs libjpeg-turbo and the helper;
non-JPEG and CMYK stay on Go. Cache identity advances to `raster-v3`. No storage
format or original changes. Native failure does not crash or stop the batch.

Boundary/race tests and native ASan/UBSan checks passed: baseline, progressive,
grayscale, malformed/truncated data, pixel/output-size limits, concurrent workers,
required-helper validation, portable fallback and cancellation of a running child.
Restic/shared Docker smokes passed, including restart and disposable migration.

Initial renderer-only benchmark (i7-1365U, synthetic 6000x4000 JPEG, 320px output,
three runs of five renders) measured Go 398–505 ms, turbo 59–61 ms, and turbo with
SIMD disabled 62–75 ms. These runs overlapped other local checks and are preliminary.
Scaled decode and the codec dominate the gain; this is not a production throughput
claim. Go allocation counts omit native subprocess memory. Production uses the
Alpine-packaged codec, while the local host has libjpeg-turbo 3.2.0.

## Release validation and host measurement

Full `make check` passed with the native helper available (Go tests, race tests,
vet, line limits and JavaScript syntax). The Docker Chromium smoke passed under
2 CPUs / 4 GiB, including pause/restart, corrupt/retry/replacement, owner isolation,
Photos, albums, Library, uploads and music. Native boundary and sanitizer tests
also passed. The CI script now generates embedded UI assets before compiling its
native tests; a clean checkout exposed that missing setup step.

On the production Xeon Gold 6132, an isolated read-only test container limited to
2 CPUs / 4 GiB, with no vault mount, rendered a synthetic 6000x4000 JPEG at 320px:

| Renderer | Three runs, ten renders per run |
| --- | --- |
| Go | 200.9–211.8 ms/render |
| libjpeg-turbo, automatic SIMD | 22.7–23.3 ms/render |
| libjpeg-turbo, SIMD disabled | 26.4–27.3 ms/render |

Median speedup is about 8.9x for rendering, with automatic SIMD reducing native
elapsed time by about 16%. Most of the total gain is native/scaled decoding, not
SIMD alone. The benchmark executable used local Go 1.27; production remains Go
1.25. Both native measurements used the deployed Alpine libjpeg-turbo 3.0.4-r0.
The fixture is synthetic, and Go allocation figures omit child-process memory.
Restic retrieval is excluded: these numbers are not a whole-library ETA or proof
that all operations use AVX2 (and no AVX-512 optimization is claimed).

## Production deployment

Release `56cf829` is live. All 96,971 catalog entries (95,476 files) retain the same
metadata fingerprint. Login/unlock, folder/search/photo pages, a real JPEG preview,
and three original SHA-256 readbacks passed. Public Library and Grab readiness
return HTTP 200. Production reports native rendering available, eight background
workers, four source readers and an 8-GiB admission budget.

Production settings and both mounts were preserved. Rollback image:
`weazlcloud:rollback-turbo-20260928`. Consistent stopped snapshot:
`/exports/dockervolume/weazlcloud.pre-turbo-56cf829-20260928`.
Private configuration/source/deployment evidence is retained in
`/home/bobp/weazlcloud-import-watch/release-turbo-20260928`.
The existing pre-release snapshot remains as well; neither is an off-host backup.
Preparation was explicitly resumed after successful validation. New `raster-v3`
identities cause the previous small set of cached derivatives to rebuild; originals
and prior encrypted cache files were not deleted.

Observed after resume: `running`, 16 ready / 0 failed out of 32,368 candidates,
with four concurrent Restic children. Source retrieval still consumes substantial
wall time. All five delivery phases are implemented and production rollout checks
passed; final CI status is recorded below when the hosted workflow completes.
