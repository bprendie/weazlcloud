# Local smoke follow-up — September 27, 2026 (Eastern)

Production was not accessed. Tests used fresh disposable local Docker volumes
and synthetic fixtures. No commit, push or deployment was performed.

## Findings and fixes

- Docker published-port requests connected but timed out before reaching the
  handler. Inside-container `/live` returned HTTP 200 immediately. The prior
  hang was in initial readiness probing, not a proven restart failure.
  Both smoke scripts now support `WEAZLCLOUD_SMOKE_HOST_NETWORK=1`, using
  distinct loopback-only listeners. No host networking/firewall settings changed.
  Container smoke curl calls have finite connection and request timeouts;
  Photos readiness probes have a two-second timeout.
- Removing the permanent rail left `renderDeck` writing to absent `dedupe-n`
  and `dedupe-caption` elements. Removed those stale writes; the footer retains
  dedupe and storage status. The authenticated browser flow now runs without
  JavaScript exceptions.
- Weighted decode admission previously acquired channel tokens one at a time.
  Competing jobs could each hold part of the budget forever; cancellation
  could leak already-acquired tokens. Reservations are now atomic, wait with
  cancellation, and release idempotently. Focused race tests cover contention,
  oversized reservations, cancellation and full capacity recovery.

## Results

Fresh application image: `weazlcloud:local-smoke-20260927` (local tag).

- `make check` passed after the fixes: all Go tests, vet, race tests, Go line
  limits, JavaScript syntax checks and regenerated embedded assets.
- Container smoke passed on Restic and shared-experimental: Desk/WebDAV reads,
  upload resume/finalization across restart, SVG preview and sealed grab reads.
- Restic-to-shared migration smoke passed, including verification and reopening
  the disposable migrated data.
- Authenticated Chromium Photos/album/music smoke passed on both backends:
  paginated Photos without a full-library fetch, image covers, album metadata,
  deep links, preparation, restart, music artwork/playback and access isolation.
- Expanded Library checks passed: compact toolbar, keyboard filter toggle,
  clearable filter chips, real resumable upload, progress completion, tray
  minimization, persistence across navigation, dismissal and no horizontal
  overflow at 1440px and 390px.
- Four-card warm fixture: Restic first cards 57 ms / first thumbnail 93 ms;
  shared first cards 74 ms / first thumbnail 130 ms. These are single smoke
  observations on a tiny fixture, not scaling benchmarks or production ETAs.

Screenshots are local temporary artifacts in `/tmp/weazl-library-<backend>-<width>.png`.
The temporary Playwright environment is `/tmp/weazl-smoke-venv`.

## Reproduction

```sh
docker build -f deploy/Dockerfile -t weazlcloud:local-smoke-20260927 .
WEAZLCLOUD_SMOKE_HOST_NETWORK=1 \
WEAZLCLOUD_IMAGE=weazlcloud:local-smoke-20260927 \
WEAZLCLOUD_CONTAINER_PORT=29372 bash scripts/container-smoke.sh
WEAZLCLOUD_SMOKE_HOST_NETWORK=1 \
WEAZLCLOUD_IMAGE=weazlcloud:local-smoke-20260927 \
WEAZLCLOUD_ALBUM_PORT=29581 \
/tmp/weazl-smoke-venv/bin/python scripts/smoke-photo-albums.py
make check
```

Repeat with `WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental` for shared
storage. Default bridge-network mode remains available for other hosts.

## Limits and deployment gate

The follow-up implementation addresses the four requested correctness areas:

- Nested v1/v2 cgroup discovery, host RAM/affinity/GOMAXPROCS bounds, startup
  validation, and conservative admission before source allocation. Header and
  render reservations are separate all-or-nothing acquisitions; Restic child
  overhead, decoded pixels, scratch, output and cache encoding are included.
- Backend holds acquired under the metadata lock; owner leases and vault-session
  invalidation; fresh entry/revision/hash checks before cache publication and
  response. Coalesced raster jobs survive cancellation of an individual waiter.
- Stable identity snapshots, restart rescanning of caches/failures, explicit
  persisted manual pause, full-import pauses, and cancellation/drain on shutdown.
- Explicit cache-skipped errors, serialized temporary writes/maintenance,
  readiness reconciliation after eviction, encrypted bounded failure records,
  visible checkpoint failures, and an explicit failed-preview retry action.

Focused race tests cover nested limits, malformed/tiny hosts, stale cached and
in-flight reads, replacement/deletion, lock/rekey, revocation, drain, coalesced
cancellation, bounded reads, unsafe restart positions, manual pause, cache
retention failures, failed-item retry and eviction. The browser fixture adds
pause across restart and mutation, one corrupt photo, retry, and replacement
recovery. It uses the existing four-photo fixture plus one failure fixture.

Resource admission uses estimates; Restic's Go heap target is not a hard RSS
cap. Queue priority/fairness, adaptive pressure feedback and representative
throughput/RSS benchmarks remain open in A3/A5. The complete resource workbook
is **not** declared finished. No production performance or ETA claim is made.

No rollback was needed: production still runs its existing image. Retain that
image and keep preparation off when a later rollout is explicitly initiated.

## Follow-up browser/container results

Rebuilt local image: `weazlcloud:local-smoke-20260927`.

- Restic browser smoke passed in a real **2-CPU / 4-GiB Docker limit**.
  The script asserted the startup policy: two render workers, one background
  worker, one source reader and a 512-MiB preview budget.
- Shared-experimental browser smoke passed in the local default envelope.
- Both exercised persisted pause across container restart/index mutation,
  corrupt-image partial readiness, the retry button, replacement recovery,
  Photos/albums, the Library toolbar/upload tray and music playback/isolation.
- Both container smokes passed, including resumable uploads and restart;
  the Restic-to-shared disposable migration smoke passed too.
- New warm four-card observations: constrained Restic 81 ms first cards /
  108 ms first thumbnail; shared 67 ms / 99 ms. These tiny-fixture observations
  are **not** throughput comparisons or a large-library scaling claim.
- Every disposable smoke container and volume was removed by its script.

Use `WEAZLCLOUD_SMOKE_CPUS=2 WEAZLCLOUD_SMOKE_MEMORY=4g` with the browser command
above to repeat the constrained run. Full check output is retained locally at
`/tmp/weazl-check-verified.log`; browser and container logs use
`/tmp/weazl-{browser,container}-{restic,shared}-final.log`.

Final `make check` passed on the finished source: all Go tests, vet, race tests,
Go file-length limits, regenerated assets and JavaScript syntax checks.
`git diff --check` passed. Nothing was committed, pushed or deployed.
