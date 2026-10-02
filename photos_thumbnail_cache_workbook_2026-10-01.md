# Faster Photos thumbnails and caching — Sol execution workbook

Date: October 1, 2026
Owner: Bob
Implementer: Sol
Status: Implementation shipped and live verification complete as `8700c60`.
Open sustained-load performance experiments are listed explicitly below.
Performance targets not established by the small fixture remain explicit in the measurement report.

## Outcome

Make Photos populate quickly, keep scrolling smooth, and avoid restoring the
same original repeatedly to produce different previews. Background preparation
must survive bad files, restarts and browser closure. Large hosts should finish
more work per second; small hosts must stay responsive within their limits.

For the historical collection of 37,082 logical assets, a one-hour preparation
pass needs about 10.3 assets/second. This is a benchmark target, not a promised
ETA. Measure full source reads and image rendering; the recent metadata
extraction result does not establish thumbnail throughput.

Read `weazl_ethos.md`, `README.md`, `docs/photos-release-runbook.md`,
`docs/photos-metadata-performance-2026-10-01.md` and this workbook first. The ethos
is expressed through behavior, not new slogans or explanatory banners. Keep Go
files within the repository's 300-line gate. Earlier workbooks describe historical
implementations: use current source and this workbook for this pass.

## Scope and execution rules

- Work in phase order. Smoke-test each phase before advancing. Record commands,
  evidence and limitations; check boxes only after the corresponding work passes.
- Resolve routine implementation choices using these defaults. Existing session
  authorization still applies; do not invent a new approval step for each phase.
- This request creates a plan. A later execution instruction starts local work.
  Production rollout is C7; use the session's deployment authorization at that
  time rather than inferring it from the request to write a workbook.
- Preserve unrelated working-tree changes, original media, authoritative dates,
  albums, IDs, rotations, Hidden flags, accounts, vault settings and grab capsules.
- Keep local identity, owner encryption and the isolated render sidecar. No SaaS,
  public thumbnail URLs, vault keys in the renderer, or plaintext disk staging.
- Retain Go for orchestration. Benchmark native rendering improvements inside the
  existing worker; a language rewrite or new database container is not required.
- Preserve owner opt-in and manual pause. Do not reopen completed preparation on
  upgrade merely to fill new viewer variants. Explicit preparation and future
  opted-in ingestion can generate the new bundle; browsing requests what it needs.
- Do not erase existing caches to demonstrate speed. Use disposable local cache
  directories. No 500-file upload exercise or production-wide rebuild for testing.
- A malformed asset fails that asset. Persistent shared storage/checkpoint errors
  pause the job visibly. Neither outcome deletes originals or reports false success.

## Starting facts — verified in the current code

| Surface | What exists and what needs changing |
| --- | --- |
| `internal/library/thumbnail_source.go` | Restic thumbnail reads still use individual CLI restores. Missing dimensions cause a header read followed by a full read. Source reads already occur outside the main library lock. |
| `internal/library/photo_metadata_reader.go`, `internal/restic/reader.go`, `native/restic-reader/` | Metadata shares a persistent authenticated Restic index. Its JSON/base64 protocol is limited to 4 MiB; it is not a full-image streaming protocol. |
| `internal/library/photo_prepare_parallel.go` | Durable jobs run in waves; all results settle before another wave is leased. Preparation targets 320px only. |
| `internal/library/photo_job_store.go`, `internal/photos/jobs.go` | Full encrypted queue saves occur on leasing/settlement. Upsert, progress and lookup scan slices; leasing scans/sorts pending jobs. |
| `internal/library/thumbnail_cache*.go` | Owner-encrypted, content-keyed files. A global write lock covers encoding/encryption/I/O/accounting. Eviction uses creation/write age, not access recency. |
| `internal/library/thumbnail.go` | Defaults: 4 GiB and 100,000 cache files per owner; 16 GiB per node. Explicit environment overrides exist. These are cache limits, not user storage quotas. |
| `internal/library/thumbnail_turbo.go`, `native/preview/` | JPEG already uses libjpeg-turbo with runtime SIMD detection and scaled decoding. Other supported formats use existing bounded fallbacks. |
| `mockup-ui/app.js`, `internal/desk/photo_page.go` | Lazy loading, bounded DOM and four frontend thumbnail requests already exist. Photos uses direct image URLs with private/no-store responses, without a dedicated retained Photos blob cache. |

Cache keys already follow owner/content identity, size and rendering/rotation
inputs. Date-only edits, renames and captions must not regenerate image bytes.
Authorization must still validate the current asset on every server response.

Production was last documented with a 16-CPU API allocation, an eight-CPU render
sidecar, and 128 GiB host RAM. Host CPU count is 32. These are dated observations,
not configuration to overwrite or hard-code; inspect effective limits at rollout.
No claim is made here that the live disk cache is full.

## Defaults to implement

1. Fixed normal Photos outputs: 320px grid, 1280px viewer, and a tiny ThumbHash
   placeholder stored in the encrypted derivative manifest. Keep existing JPEG/
   PNG response compatibility and transparency. Do not create a third disk image
   per asset just for the placeholder. Keep other supported requested sizes working.
2. One reusable Restic reader session per active unlocked owner/repository,
   shared where practical by metadata and thumbnails. Bound open sessions by
   node memory and owner fairness; never start one index-bearing process per tile.
3. Start large-profile measurements with four source readers and up to eight
   render slots, reserving foreground capacity. Respect stricter live settings.
   CPU work, including readers, native children and metadata, must fit the shared
   photo allowance; slots alone are not proof of a 50% CPU ceiling.
4. One-worker/source fallback on a 2-CPU/4-GiB deployment, with the existing
   512-MiB working allowance; unknown resources retain the conservative current
   fallback. The smaller container wins over the host's advertised resources.
5. Keep current 64-MiB input, 32-million-pixel and 1280px output-dimension limits.
   Retain per-variant output limits and add a bounded aggregate bundle limit.
   A giant video or ISO must not become a whole-file memory allocation.
6. RAM caches hold compressed derivative bytes, not full decoded originals.
   All processes, retained buffers and queues participate in resource accounting.
   Preserve the silent disk reserve and prioritize original-file writes over caches.

## C0 — capture a reproducible baseline

Deliverable: `docs/photos-thumbnail-performance-2026-10-01.md` with actual
before/after measurements and a table whose unfinished cells say “not measured.”

- [x] Record starting commit, working-tree status, CPU/cgroup/RAM limits, storage
  backend, image tool versions and configured resource/cache limits.
- [x] Reuse the existing Photos fixture for correctness. Add only necessary small
  cases: JPEG, transparency, rotated image, HEIC/AVIF if supported, video poster,
  corrupt neighbor and oversized rejection. Use distinct authorized local images
  for throughput; identical copies mainly measure dedupe/coalescing.
- [x] Separate cold derivative/warm source, warm encrypted derivative, and warm
  browser cases. Report whether the OS page cache was warm; do not drop host caches.
- [ ] Measure source/index open, read, decode/resize, encode, encrypted write and
  queue-persistence time; original bytes/read count; completed assets/sec; variant
  count; cache hit/eviction counts; request p50/p95; queue wait; peak RSS including
  child processes. Label sample size and distinguish attempts from retained success.
- [x] Add a no-media benchmark of 37,082 synthetic job records to expose queue
  scaling. This does not upload 37,082 files or pretend to measure image decoding.
- [x] Extend existing scripts rather than run `scripts/measure-previews.sh`
  unchanged: it currently selects both 100 and 500 decode loops. A direct small
  baseline command follows; it measures only decoding.

```bash
go test ./internal/library -run '^$' -bench '^BenchmarkThumbnailDecode100$' -benchtime=1x -benchmem
```

Gate: Sol can repeat the same local workload before and after each optimization.
Existing thumbnails, original bytes and paused preparation remain intact.

## C1 — reuse the authenticated reader for thumbnail sources

Primary files: `internal/restic/reader.go`, `native/restic-reader/`,
`internal/library/photo_metadata_reader.go`, `thumbnail_source.go`,
`preview_access.go`, `preview_memory.go`, plus small session/protocol modules.

- [x] Introduce a versioned, bounded binary streaming extension. Keep the 4-MiB
  metadata prefix API working. Full-image requests enforce their declared size
  and the existing input limit before allocation. Do not enlarge JSON/base64
  messages to 64 MiB and call that streaming.
- [x] Use bounded chunks and explicit request IDs, completion and cancellation.
  One canceled request must not corrupt framing, stall others or leak a pending
  buffer. Detect truncated, oversized, mismatched and duplicate responses.
- [x] Share the loaded index across concurrent reads. Charge the resident index,
  chunk buffers and destinations against admission before starting the session.
  Avoid holding partial reservations while waiting for an impossible upgrade.
- [x] Authorize the current owner/asset, capture an immutable reference and hold
  its storage lease before reading. Verify complete length/hash and Restic blob
  authentication; revalidate identity/session before publishing a result.
- [x] Bound session lifetime: close on lock, rekey, revocation, owner deletion,
  shutdown, maintenance drain and idle expiry. Release Restic locks normally.
  Never retain plaintext credentials in files, argv, environment or logs.
- [x] Handle newly uploaded packs through one coordinated refresh/reopen or the
  bounded existing CLI fallback. Prevent every miss from opening another index.
  Shared-object storage keeps its native reader and equivalent integrity checks.
- [x] Keep a documented operator fallback to the old reader path. Metadata
  repair must work with the new helper, the old protocol path and helper disabled.

Gate: real Restic tests prove byte parity for multi-chunk files, parallel reads,
cancellation, stale references, new uploads, helper failure and lock cleanup.
An original needed by grid and viewer is fetched once in the C3 integrated test.
Missing dimensions may still need a bounded probe until C3 admission is integrated;
measure this separately instead of claiming it is eliminated here.

## C2 — keep workers busy and make job tracking scale

Primary files: `photo_prepare_parallel.go`, `photo_job_store.go`,
`photo_progress.go`, `internal/photos/jobs.go`, `preview_jobs.go` and lifecycle hooks.
Unqualified Go filenames in this workbook are under `internal/library/`.

- [x] Replace wave dispatch with a bounded continuously refilled pool. A worker
  finishing a fast JPEG can take another job while a neighbor is still decoding.
  Bound queued source bytes and completed results, not only goroutine counts.
- [x] Index jobs by ID; maintain a priority/due-time structure for eligible work.
  Remove collection-wide scans/sorts from each progress update and dispatch.
  Store only the active working set in channels; the durable queue holds backlog.
- [x] Replace full-queue saves per small batch with an owner-encrypted append
  journal and periodic encrypted snapshots. Use authenticated versioned records,
  fresh nonces, sequence continuity, atomic snapshot publication and bounded
  compaction. Do not introduce plaintext job paths/asset IDs in a SQLite sidecar.
- [x] Persist leases before dispatch and completion after durable cache writes.
  Group commits may batch up to 100 outcomes or 250 ms, whichever comes first;
  these are starting bounds to measure. A crash can replay unfinished work, but
  cannot turn an uncommitted result into a durable success. Flush on clean pause.
- [x] Recover a torn final journal record without accepting corrupted interior
  records. Disk-full/checkpoint errors stop dispatch and surface a resumable error.
  Renew long leases; one coordinator must not requeue another live worker's lease.
- [x] Migrate `.weazl-photo-jobs.enc` once, after validation, preserving attempts,
  failures, enabled/manual-pause state and content-ready caches. Define and test
  a compatibility export for image rollback; never discard the only readable state.
- [x] Keep foreground priority, bounded admission and fair service across owners.
  Coalesce compatible work without canceling surviving waiters. Respect imports,
  maintenance and quiet scheduling; metadata and thumbnails share resource limits.

Gate: a deliberately slow job cannot idle the remaining workers. The large
metadata-only queue benchmark demonstrates bounded dispatch/progress cost and
incremental writes. Kill/restart, pause/resume, expired leases, corrupt-neighbor,
two-owner fairness and write-failure tests pass on Restic and shared storage.

## C3 — fetch once and generate a preview bundle

Primary files: `thumbnail.go`, `thumbnail_source.go`, `thumbnail_turbo.go`,
`thumbnail_render.go`, `internal/previewrpc/client.go`,
`cmd/weazl-photo-worker/`, `native/preview/` and preparation status/API modules.

- [x] Add an asset-level work key using owner/session, content, orientation,
  user rotation and renderer version. A requested size selects an output from
  that work; simultaneous grid/viewer requests must not restore twice.
- [x] Decode the source once at suitable resolution; produce 320px, 1280px and
  ThumbHash from that shared representation. Account for decoded pixels, output
  copies and encoder scratch before fan-out. Limit native internal threads so
  eight jobs do not secretly become dozens of runnable decoder threads.
- [x] For missing dimensions, probe within the same bounded source transfer with
  a conservative reservation, or release/retry admission without keeping buffers
  that block other workers. Recheck actual dimensions before decode. Record any
  small-host fallback requiring a second read; do not remove size or memory checks
  to satisfy the one-read target.
- [x] Publish the requested/grid output as soon as it is validated and durable;
  do not hold a visible tile behind optional viewer output. Track grid-ready and
  bundle-ready separately. A failed viewer encode must not discard a valid grid.
- [x] Explicit preparation creates missing bundle outputs. On-demand requests
  must work with preparation disabled; optional extra work follows owner opt-in
  and schedule. Missing variants after restart retry individually without a cache wipe.
- [x] Prefer the existing accelerated JPEG path. Benchmark libvips in the isolated
  worker for multi-output and other formats; adopt it only with measured benefit
  and equivalent limits, orientation, transparency and color correctness. Do not
  require a Node runtime or replace working codecs just to resemble Immich.
- [x] Extend the private worker protocol with bounded output framing, capability
  detection and legacy single-output fallback. Keep the Go API CGO-free and the
  worker without network access, vault mounts or keys. No plaintext temp files.
- [x] Keep existing `media-v4` outputs usable where transforms are compatible.
  Use a new rendering identity only where pixels/format actually change. Never
  bump every cache key merely because transport or queue implementation changed.
- [x] Verify JPEG/PNG/GIF, supported native formats and video-poster paths. Extract
  one poster frame for its image variants where supported; this is not a new
  video-transcoding or playback feature. Unsupported/oversized media stays explicit.

Gate: instrumentation proves one source transfer and shared decode for a cold
supported bundle, correct orientation/alpha, early grid display, bounded peak
memory, and no extra original reads on a second complete preparation pass.

## C4 — retain useful derivatives and remove cache write bottlenecks

Primary files: `thumbnail_cache.go`, `thumbnail_cache_manager.go`,
`thumbnail_maintenance.go`, `thumbnail_limits.go` and new small manifest modules.

- [x] Keep an encrypted manifest of derivative variants, sizes, rendering identity
  and readiness. Cache bytes remain owner-encrypted. ThumbHash is private data;
  scope it like the image and exclude Hidden placeholders from ordinary results.
  Update bounded records/segments rather than rewriting the entire owner's
  derivative manifest per thumbnail. Reuse C2's durable-write design where suitable.
- [x] Encode/encrypt outside the global bookkeeping lock. Publish unique temp
  files atomically; coordinate same-key writers and global capacity reservations.
  A reader must see a complete old or new file, never a partial write.
- [x] Introduce access-recency accounting and an indexed eviction structure.
  Batch access updates; do not fsync on every hit, sort all files for each eviction,
  or stat/redecrypt every derivative for each progress poll.
- [x] Prefer dropping obsolete variants, then least-used larger previews, then
  grid thumbnails if pressure still requires it. Grid retention is a preference,
  never permission to breach an operator cap or the disk reserve. Pin active writes.
- [x] Use an adaptive default when no explicit byte caps are set. Starting node
  target: `min(64 GiB, (current cache bytes + free bytes above system reserve)/10)`.
  Clamp available bytes at zero; re-evaluate periodically with hysteresis and
  preflight each write. This target uses only part of available/reclaimable space.
- [x] In automatic mode, share that node allowance fairly without the legacy
  implicit 4-GiB per-owner cap. Honor all explicit owner/node/file limits exactly;
  document automatic versus configured behavior. No new user storage quota.
  Bound manifest entries and filesystem counts as well as bytes.
- [x] Import existing encrypted cache files lazily or in a bounded background
  reconciliation. Preserve valid grid images. Disk corruption/eviction makes a
  variant missing; authenticated read failure cannot count as ready.
- [x] Keep upgrade, cleanup, deletion and rollback readers compatible with legacy
  envelopes. Under low space, stop speculative generation first and report partial
  readiness without a regenerate/evict loop. Never reclaim capsule or original data.

Gate: concurrent owners/writers stay within capacity reservations; warm frequently
used entries outlive unused ones; partial writes/corruption recover; no global
cache wipe occurs; a warm pass and restart reuse existing valid encrypted previews.

## C5 — make repeat scrolling cheap

Primary files: `mockup-ui/app.js`, new small Photos cache module,
`mockup-ui/engine.js`, `internal/desk/photo_page.go`, owner lifecycle/event code.

- [x] Add a byte-bounded server RAM LRU for compressed derivatives. Start with
  `min(2 GiB, effective RAM/64, preview memory budget/4)` as a ceiling, disable it
  when resources are unknown, and charge retained entries to the shared allowance.
  Reclaim cache memory before making foreground rendering wait for memory.
- [x] Validate current owner, vault session, source identity and Hidden scope
  before serving any hit. Clear/invalidate on replacement, rotation, trash/purge,
  rekey, lock and revocation. Date-only edits should keep compatible bytes reusable.
- [x] Add a bounded session-only browser blob cache: default 32 MiB with at most
  256 entries. Explicitly revoke evicted URLs after mounted users release them;
  bound mounted images as today. No IndexedDB, service-worker or localStorage
  persistence of images, placeholders, keys or private source identities.
- [x] Key browser reuse by authorized owner/session and derivative identity, with
  Hidden scope separated. Purge on logout, account change, lock and received
  access/mutation events; propagate local logout across tabs. Do not imply that
  previously delivered browser pixels can be remotely recalled while offline.
- [x] Preserve `Cache-Control: private, no-store` on network responses. Retain
  request cancellation and viewport priority. Prefetch only a bounded adjacent
  window after visible requests, not an album or all 37,000 entries.
- [x] Render the tiny placeholder while a real derivative arrives, preserving
  aspect ratio and tile position. Reuse the same cache in grid and viewer.
  Keep the timeline rail, keyboard/swipe behavior and return scroll position intact.

Gate: browser smoke records a repeated nearby scroll with memory hits and no
duplicate thumbnail requests while entries fit; no original downloads for grid
view; logout/lock/account/Hidden transitions do not reuse another scope's images.
Under memory pressure, bounded eviction works without unbounded object URLs.

- [x] Move date inspection/repair, duplicate review and preview preparation into
  an accessible Photos hamburger menu. Keep maintenance status in that menu;
  ordinary timeline browsing should not spend a toolbar row on these operations.
- [x] Inset the date rail from the browser scrollbar, retain a minimum 44px hit
  target, and allow mouse drags starting on year labels. Keep immediate date-label
  feedback with one seek on release; test mouse, touch, keyboard and stale requests.

## C6 — integration, failure drills and performance acceptance

- [x] Run focused meaningful tests after each phase; then run `make check` once
  on the integrated result. Include new modules in JS and source-size checks.
  C1 covers `./internal/restic` and `./internal/library`; C2 adds
  `./internal/photos`; C3 adds `./internal/previewrpc` and
  `./cmd/weazl-photo-worker`; C4 covers cache/lifecycle tests in
  `./internal/library`; C5 covers new JS cache tests and browser transitions.
  Use `go test -race` for changed concurrent code. Add the browser assertions to
  existing smoke entry points rather than leave an uninvoked standalone script.
- [x] Run `bash scripts/test-native-preview.sh` when native rendering changes;
  test the persistent reader against the Restic version pinned in Docker as well
  as the local CLI. Do not rely on a newer local Restic fixture syntax.
- [x] Run `make smoke-container`, which builds `weazlcloud:smoke` and tests both
  storage backends; then `make smoke-photos PHOTOS_PYTHON=<playwright-python>`.
  Resolve the interpreter locally. These use disposable 2-CPU/4-GiB containers.
- [x] Extend the existing preparation and modal/timeline smoke modules for bundle
  readiness, warm scroll, worker restart and privacy transitions. Run
  `make smoke-browser` for the integrated browser regression gate on both backends.
- [ ] Repeat C0 measurements with 1, 2, 4 and up to 8 workers as permitted by
  actual available resources. Include concurrent foreground browsing. Do not
  simulate 32 cores in a config and report that as measured large-host performance.
- [x] Verify helper/worker death, corrupt neighbor, stale source, cancellation,
  forced shutdown, disk-full, journal corruption, cache corruption and one owner
  locking while another browses. Check original hashes and album/metadata identity.
- [x] Require zero source reads for warm derivative hits; one source transfer per
  supported cold bundle; no full-queue rewrite per ordinary completion; no batch
  barrier; no cold rebuild after a clean restart or date-only edit.
- [ ] Target local warm-thumbnail p95 below 100 ms and first visible warm grid
  paint below 500 ms, over at least 30 measured interactions. Report machine,
  image count, network and sample count. Under backfill, investigate >20% regression
  against the same foreground baseline; do not hide misses by changing the sample.
- [x] Record whether 10.3 retained bundles/second is demonstrated on representative
  media. Report grid-only and full-bundle throughput separately, plus failures and
  slow formats. If unmeasured or missed, keep the one-hour target open and identify
  the measured bottleneck. Do not mark a local result as a production ETA.

Gate: functional/durability/privacy tests pass; the performance record distinguishes
passed, missed and unmeasured targets. Only new changes or unresolved failures
justify repeating the full test suite.

## C7 — document, package and roll out when deployment is in scope

- [x] Update README, requirements/config documentation, `docs/photo-api.md`,
  `docs/photo-api.yaml`, recovery runbook and C0's performance record with actual
  behavior, limits, migration/export steps, feature fallbacks and measured results.
  Keep future capabilities labeled as future; do not claim unsupported codecs.
- [x] Review the complete diff, exclude screenshots/cache artifacts/credentials,
  commit the implementation and docs when execution scope includes committing,
  and push when authorized. Check relevant CI jobs before declaring release-ready.
- [x] For an authorized rollout, inspect current jobs and effective Compose mounts,
  CPU/RAM overrides, image versions and cache limits. The recent date-repair result
  is historical evidence; verify current status rather than assuming it finished.
- [x] Preserve the prior image/config and a consistent recovery point using the
  existing runbook. Checkpoint affected jobs before replacing containers. Preserve
  production bind mounts/settings; never replace them with template named volumes.
- [x] Deploy API/worker versions that negotiate the new protocol. Smoke login,
  Library, Photos grid/viewer/rail, Hidden and albums. Verify existing previews
  survive; originals, dates and album membership reconcile. Do not start a global
  rebuild or override a manual pause just because the code was deployed.
- [x] Measure a bounded authorized sample before any full preparation run. Tune
  within existing CPU/fan and memory limits using observed throughput/RSS. Any
  production ETA uses remaining missing bundles and sustained measured rate.
- [x] Verify image rollback with the queue compatibility export and legacy cache
  path. Do not restore an entire old data volume over uploads/edits made since the
  checkpoint. Follow the recovery runbook if a data restore is actually needed.

Gate: documentation describes the shipped behavior, release checks pass, and any
authorized live change has a recorded image/config, smoke result and recovery path.
If production is outside execution scope, finish with a verified local release
candidate and leave only the live rollout boxes open.

## Handoff checklist

| Phase | Result to hand back | Status |
| --- | --- | --- |
| C0 | Reproducible baseline and existing fixture inventory | Baseline and after samples recorded; detailed measurement limits below |
| C1 | Bounded persistent thumbnail reads and lifecycle tests | Implemented; focused/race and both-backend smokes passed |
| C2 | Continuously supplied workers, scalable durable job tracking | Implemented; focused/race and both-backend smokes passed |
| C3 | One-read shared-render bundle with compatible existing previews | Implemented; focused/race and both-backend smokes passed |
| C4 | Encrypted adaptive disk cache, efficient writes and eviction | Implemented; focused/race and both-backend smokes passed |
| C5 | Private bounded RAM/browser caches and smooth repeat scroll | Implemented; focused/race and both-backend smokes passed |
| C6 | Both-backend smokes, failure drills and measured performance | Functional and delayed-summary navigation gates passed; open performance targets recorded |
| C7 | Accurate docs, release candidate and scoped rollout evidence | Shipped 8700c60; all CI, live desktop/mobile and data reconciliation passed |

When handing back a phase, name the commit/diff, changed behavior, tests actually
run, measurements and remaining limits. Do not count a placeholder, an unrun
benchmark or a passed synthetic queue test as completed image throughput work.

## Research behind this workbook

Immich's still-image pipeline shares a decoded representation across thumbnail,
preview and ThumbHash generation; its media repository uses Sharp/libvips.
This supports the C3 design, but does not prove a particular speedup for Restic.
See [image job implementation](https://github.com/immich-app/immich/blob/main/server/src/services/media.service.ts)
and [media renderer](https://github.com/immich-app/immich/blob/main/server/src/repositories/media.repository.ts).

Immich separates background job concurrency from API responsiveness and warns
against oversubscribing thumbnail workers. Apply that principle within WeazlCloud's
existing vault and resource model. See
[system settings](https://docs.immich.app/administration/system-settings/)
and [jobs and workers](https://docs.immich.app/administration/jobs-workers/).
Links describe the inspected upstream design, not a pinned dependency or code to copy.

## Execution evidence — October 1, 2026

See [the performance record](docs/photos-thumbnail-performance-2026-10-01.md) for
commands, sample sizes and actual results. Reader reuse, durable indexed jobs,
bundle output, adaptive encrypted cache, private RAM/browser reuse and the
Photos menu/rail changes are implemented. The default small-host path was verified
in 2-CPU/4-GiB containers; a resident index yields to the bounded CLI when memory
cannot accommodate both indexing and decoding. The new ThumbHash dependency and
browser port carry their ISC license. Libvips remains unadopted after exploratory
measurement; no unbounded plaintext renderer was added.

C0/C6's unchecked measurement lines contain remaining portions: separate native
decode/resize/encode costs, queue-wait and combined peak RSS, and the sustained
foreground-vs-backfill comparison. One/two/four-worker bundle tests and 30 warm
browser interactions were measured. Eight local workers would exceed this host's
photo CPU allowance. The warm latency targets passed; representative 10.3-bundle/s
production throughput is not established. These are open performance experiments,
not omitted implementation or a claim that the one-hour target passed.

Production was inspected before rollout: no active imports/uploads, metadata
repair complete with reported issues, preview preparation manually paused. The
private pre-upgrade inventory contains 96,971 Library entries, 37,082 media assets,
16 albums and three hashed sample originals. Preserve the existing pause and
API/worker CPU, memory, storage and hostname settings during C7.

The rollout gate caught an unclean shutdown of the previous image and immediately
restarted it. Follow-up `8700c60` closes admission/SSE streams, drains owner readers
and journals before locking vaults, and retains ready ZIPs. Targeted lifecycle,
SSE-aware rollback, both-backend container smokes, full local checks and all final
[CI jobs](https://github.com/bprendie/weazlcloud/actions/runs/36957557012) passed.
The clean retry verified 275,971 backup files and brought both services healthy
in 43.7 seconds. Full Library/photo/date/album reconciliation and three original
hashes matched. See [the rollout record](docs/photos-thumbnail-rollout-2026-10-01.md).

Live Chromium desktop/mobile smoke passed with zero page errors: menu, inset rail,
grid, decoded viewer, date navigation, Hidden, 16 albums and Library. The warm
production thumbnail sample measured 1.02 ms p95; the manual preparation pause
remains intact. All implementation/release phases are shipped. The unchecked
C0/C6 lines remain honest sustained-load measurement follow-ups, including the
20% foreground/backfill comparison and representative one-hour throughput target.
