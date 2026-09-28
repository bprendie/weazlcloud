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
