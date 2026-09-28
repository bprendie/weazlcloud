# Adaptive Photos generation and compact UI — Luna workbook, September 27, 2026

Owner: Bob. Implementer: Luna. Status: **partially implemented locally; production untouched.**

Follow-on plan: [September 28 responsiveness workbook](responsiveness_workbook_2026-09-28.md).
Its R5–R6 coordinate the remaining scheduling, cache and hardware-acceleration
work here; do not build competing resource managers.

Smoke follow-up: the earlier completion summary overstated this workbook's
coverage. Passing small-fixture smoke tests does not close the resource,
mutation/lifecycle, durability or performance gates below. See
`docs/photos-smoke-followup-2026-09-27.md` for the local smoke results and fixes.

## Outcome and boundary

Make the local Photos preview backend use a larger machine effectively while
remaining usable on smaller hosts. Optimize completed, reusable previews per
second while foreground browsing stays responsive. High CPU utilization alone
is not success.

Also deliver the minor UI refresh in U0: give files more screen space and remove
repetitive slogans/instructions. Bob's direction is explicit: **the Weazl ethos
is implicit in the product, not explicit text occupying the workspace.**

**LOCAL REPOSITORY AND DISPOSABLE LOCAL CONTAINERS ONLY.** Production is importing
Takeout data. Do not SSH to it, read its files, restart its container, deploy,
change its watcher, or run production benchmarks/backfill. Do not git-push.
Bob will initiate production rollout after rehydration and verification finish.

Build on the existing, uncommitted Photos work. Preserve unrelated edits and
the September 27 importer conflict fix. Do not reset, stash everything, clean
untracked files, or replace the branch with the production checkout. Do not
alter original media, the authoritative catalog format, storage backend,
dedupe policy, or user quotas as part of this workbook.

Read `photos_web_workbook_2026-09-26.md` and
`docs/photos-web-performance-2026-09-26.md` first. They contain incomplete tasks;
this workbook does not declare them finished. Work in phase order below.
Mark a gate complete only after its checks actually pass. Record blocked or
unmeasured items explicitly rather than silently dropping requirements.

## What exists today

| Location | Current behavior / reason to inspect it |
| --- | --- |
| `internal/library/thumbnail.go` | Two global render slots; one global background slot. Reads the original while holding `Library.mu`. |
| `internal/library/photo_prepare.go` | One sequential preparation loop; persisted position, counters and enabled flag. |
| `internal/library/thumbnail_limits.go` | Positive integer environment parsing for cache limits; no adaptive CPU/RAM policy. |
| `internal/library/thumbnail_cache*.go` | Encrypted cache, owner/node budgets and shared bookkeeping lock. |
| `internal/library/photo_index*.go` | Owner index, generation changes and current-entry authorization. |
| `internal/filesvc/gate.go`, `registry.go` | Owner lifecycle leases and cancellation; deletion must remain safe. |
| `internal/library/thumbnail_test.go`, `photo_prepare_test.go` | Existing correctness and preparation tests. |
| `scripts/smoke-photo-albums.py` | Existing disposable Restic/shared-backend browser coverage. |

Current raster limits are 64 MiB compressed input and 32 million pixels. Retain
them for this pass. Grid previews are 320 pixels; viewing previews are 1280.
Backfill prepares grid previews only, is owner opt-in, and stays off on upgrade.
Cache limits remain separate from RAM budgets: currently 4 GiB/100,000 entries
per owner and 16 GiB per node, with existing override settings.

The currently inspected README has no explicit recommended-host specification.
Locate an existing requirements document before claiming compatibility with a
published recommendation. If none exists, record that fact and use the test
envelopes below; do not invent historical requirements.

## Initial resource policy — implement this, then measure

Use one node-wide scheduler shared by owners, foreground previews and backfill.
Detect **effective container resources**, not just host hardware.

- Effective CPUs: account for process affinity/cpuset, container CPU quota and
  an explicitly constrained Go execution budget. Handle fractional quotas and
  never return zero workers. Avoid changing global `GOMAXPROCS` as a shortcut.
- Effective RAM: the smallest applicable host/cgroup memory limit. Resolve the
  process's cgroup and enclosing limits correctly; support Linux v1 and v2.
  Unlimited or unavailable limits must not be interpreted as zero or infinity.
- Unknown resources: one worker, one reader, a conservative 256 MiB preview
  working-memory budget, with an observable fallback reason.
- Default background maximum: `max(1, min(8, floor(effective CPUs / 2)))`.
- Foreground allowance: one slot below eight effective CPUs, otherwise two.
  Total render slots are capped by effective CPUs (rounded down, minimum one).
  On a one-slot host, foreground work takes the next slot; never reserve the
  only slot permanently and prevent background work from progressing.
- Source-read maximum: `max(1, min(4, floor(effective CPUs / 2)))`.
- Default preview working-memory budget: `min(8 GiB, effective RAM / 8)`.
  This is a budget for preview work, not the entire service or disk cache.

| Local policy/test envelope | Background max | Total render max | Concurrent reads | Preview memory budget |
| --- | ---: | ---: | ---: | ---: |
| 2 CPUs / 4 GiB | 1 | 2 | 1 | 512 MiB |
| 4 CPUs / 8 GiB | 2 | 3 | 2 | 1 GiB |
| 32 CPUs / 128 GiB | 8 | 10 | 4 | 8 GiB |

These are initial ceilings, not promises of optimal throughput or new minimum
requirements. Start with eight background workers on the large profile. Test
sixteen only after measurement; do not make sixteen the untested default.

Provide validated operator settings for background workers, total workers,
source readers and working-memory bytes. Use a consistent
`WEAZLCLOUD_PREVIEW_*` naming scheme and document the exact names. Overrides
must obey hard memory/admission limits and leave foreground capacity when more
than one slot exists. Reject invalid explicit settings clearly at startup.
Keep resource policy out of normal user-facing Photos controls.

## A0 — establish a local baseline — PARTIAL

- [x] Record starting commit, current diff/status and local CPU/RAM limits.
  Save the baseline outside runtime data; preserve the user's working tree.
- [ ] Reuse the existing small image fixture. For throughput, use a small set
  of distinct locally generated images if necessary. Identical image copies
  mostly measure cache hits/coalescing, not rendering performance.
- [ ] Record source sizes, decoded dimensions and fixture count. Include an
  ordinary photo, large allowed raster, transparency, corrupt input and an
  oversized input. No private production files or 500-file upload test.
- [ ] Measure cold source/cold derivative, warm source/cold derivative, and
  warm derivative separately. State which cache layers you actually controlled.
- [ ] Record previews/sec, source-read/decode/resize/cache-write durations,
  foreground request p50/p95 and sample count, peak container memory including
  child processes, active workers, original reads and cache misses.

Gate: reproducible commands and real before-change measurements are in
`docs/photos-resource-performance-2026-09-27.md`. Do this before implementation.

Status: the working tree already contained the earlier Photos implementation,
so controlled before-change throughput and before screenshots were unavailable;
that limitation is recorded in the performance document.

## A1 — resource discovery and a testable policy — PARTIAL

- [ ] Add small resource-discovery and policy modules; avoid package globals
  initialized from the environment that tests cannot reliably control.
- [x] Separate discovery from calculation. Tests inject CPU/memory/cgroup data
  without modifying the host's cgroup files.
- [x] Test v1/v2, nested limits, cpusets, fractional CPU quotas, unlimited
  values, missing files, malformed values, tiny hosts and explicit overrides.
- [x] Test the three policy rows above, plus one CPU and unknown resources.
- [x] Log effective limits and chosen ceilings once at startup, without
  filenames, paths into user vaults, credentials or content hashes.

Gate: a container limited to two CPUs cannot select the 32-core policy merely
because the host has 32 cores. Resource detection failure uses the fallback.

## A2 — remove serialized source reads safely — IMPLEMENTED; SCALING MEASUREMENTS OPEN

This is a correctness gate, not simply deleting a mutex.

- [x] Trace `thumbnailSource`, `capture`, `readReference` and backend read
  lifetimes. Document which state the current library lock protects.
- [x] Under the short metadata lock, authorize the owner/current entry and
  capture an immutable reference with the necessary read/lifecycle lease.
  Release the metadata lock before slow restore/read/decode work.
- [x] Keep backend references valid across concurrent rename, replacement,
  Trash, purge and user deletion. Reuse existing lifetime mechanisms where
  possible; never rely on a reference that can be reclaimed during the read.
- [x] Cancellation, vault lock, rekey and account revocation must stop or
  invalidate work. Recheck owner/entry/content validity before publishing or
  returning a derivative. Release leases on every error and cancellation path.
- [ ] Bound source readers separately from render workers. Restic child
  processes and their memory must be included in measurements and admission.
  If a backend cannot safely support parallel reads, retain its safe bound
  and report the bottleneck; do not bypass its locks speculatively.

Gate: overlapping source reads are demonstrated where supported, ordinary
metadata requests are not held behind an entire restore, and mutation/lock/
deletion tests prove no stale or cross-owner preview exposure. Race checks pass.

## A3 — bounded worker scheduling and real memory admission — IMPLEMENTED WITH LIMITS

- [ ] Replace the fixed channels with a scheduler using A1's policy. Bound
  queued requests and completed-but-not-persisted results; do not enqueue the
  entire photo collection or spawn a goroutine for every entry.
- [ ] Preserve coalescing by owner/content/renderer/size. Promote queued
  background work when a visible request joins it. Cancellation of one waiter
  must not cancel work still needed by another authorized waiter.
- [ ] Foreground work gets the next eligible slot. Share background capacity
  fairly among enabled owners; one owner's collection cannot monopolize it.
- [x] Reserve source-buffer memory before reading. Inspect image dimensions
  before full decode, then acquire a conservative weighted reservation for
  decoded pixels, decoder/resize scratch, output and encryption/JSON copies.
  Fixed compressed-byte limits alone are not a memory budget.
- [x] Bound jobs while upgrading source reservations to decode reservations;
  avoid all workers holding buffers while waiting forever for more memory.
  A job that cannot fit must get an explicit fallback/failure, not wait forever.
- [ ] Count subprocess overhead and service headroom. Do not treat a Go heap
  setting as a whole-container memory cap. Release reservations on success,
  decode failure, cache failure, cancellation, lock and shutdown.
- [ ] Start below the ceiling and grow under sustained demand. Back off when
  memory pressure, storage latency or foreground latency increases. Use
  hysteresis/cooldowns so concurrency does not oscillate on each request.
- [ ] For pressure decisions distinguish reclaimable filesystem cache from
  application working memory. A host with ample `MemAvailable` must not be
  permanently paused merely because Linux uses spare RAM as file cache.

Gate: on the small container, mixed large/corrupt images cannot create unbounded
allocation or queue growth. On a capable local host, independent images render
concurrently. Foreground work is admitted ahead of queued background work.

Status: bounded admission, a 256-job raster queue ceiling, independent coalesced
waiters, source/pipeline reservations and a Restic heap target are implemented.
Adaptive ramp-up, pressure hysteresis, strict priority/owner fairness and measured
child-process RSS remain follow-up work. A heap target is not a hard RSS bound.

## A4 — make parallel backfill resumable and durable — IMPLEMENTED WITH LIMITS

- [x] Replace the sequential preparation loop with bounded dispatch through
  A3. Keep one owner job/coordinator, allowing multiple in-flight images.
- [x] Do not advance a single shared cursor for each arbitrary completion.
  Persist a contiguous completion checkpoint or bounded per-item outcomes so
  out-of-order completion followed by a crash cannot skip unfinished work.
- [ ] Bind work to entry/content identity and index generation. Reconcile
  changes without crediting a replaced/deleted file or restarting the whole
  scan repeatedly during a small upload stream.
- [x] Report ready only after a derivative was actually stored and is reusable.
  The render path now propagates cache-write errors and explicitly identifies
  skips; it distinguishes retained previews from failed cache writes.
  A full cache or failed write must not produce false completion.
- [x] Account for eviction: avoid endless generation/eviction loops when the
  collection cannot fit the configured cache. Explain partial readiness rather
  than claiming every preview remains cached.
- [x] Persist bounded permanent-failure outcomes by content/renderer identity;
  use bounded transient retries/backoff. One corrupt image cannot stop the job.
  Supply an explicit retry action without discarding good cached previews.
- [ ] Pause dispatch for import/bulk storage work, low disk headroom, memory
  pressure, vault lock and manual pause. An active import must be detected for
  its entire lifetime, including gaps between staging waves. Resume appropriately.
- [x] Manual pause must remain paused through generation changes and restart.
  On shutdown cancel/drain bounded in-flight work and save only valid progress.
- [ ] Audit parallel cache writes, temporary disk reservations and eviction.
  Concurrent workers must share the disk reserve and never evict active temps.
  Avoid holding one global cache lock across lengthy directory scans or I/O.

Gate: kill/restart a disposable container with work in flight; it resumes every
unfinished item without double-counting. Bad images and cache failures do not
stall later good items. Lock/pressure/import pauses and manual pause all work.

Status: preparation rescans a stable identity snapshot after restart, reuses
valid encrypted caches, and persists bounded encrypted per-content failure
records. Manual pause survives index changes/restart, imports pause dispatch
for their complete lifetime, and owner/vault cancellation drains in-flight work.
Cache skips and eviction produce partial readiness; explicit retry preserves
working cached previews. A checkpoint write failure pauses with a visible error.
Pressure-driven backoff, efficient reconciliation during continuous mutations,
and avoiding a global disk-write lock remain tuning work.

## A5 — prove scaling and foreground responsiveness — PARTIAL

- [ ] Test worker settings 1, 2, 4 and 8; test 16 only where actual local
  hardware allows it. Compare the same distinct images and cache conditions.
- [ ] Exercise 2-CPU/4-GiB and 4-CPU/8-GiB disposable container envelopes where
  local resources permit. Test the 32-core policy with synthetic resource
  inputs; that is not a real 32-core throughput benchmark.
- [ ] Require zero container OOM events and bounded queues. Verify observed
  peak memory, including Restic, rather than asserting theoretical estimates.
- [ ] During backfill repeatedly open Photos, switch albums and open an image.
  Target warm page API p95 below 200 ms and a prepared first viewport within
  one second. Compare foreground latency with background disabled; investigate
  regressions rather than masking them with aggregate throughput.
- [ ] Run existing album/browser smoke on Restic and shared storage, and focused
  tests for priority, cancellation, coalescing, weighted memory, owner fairness,
  mutation safety and out-of-order checkpoint recovery. Finish with `make check`.
- [ ] Record throughput, latency, memory, source-read concurrency, pause reasons
  and failures for every measured profile. If extra workers do not help,
  identify the limiting stage instead of increasing the ceiling again.

Gate: the small profile remains stable, and increased concurrency has measured
benefit where hardware supports it. Unavailable high-end benchmarks stay marked
unmeasured. No claim about production speed comes from policy-unit tests.

Status: see the smoke follow-up document for the new authenticated Playwright
and container tests. The earlier container hang occurred during initial
startup probes through Docker's published port, before restart. Performance
and resource-pressure gates remain open regardless of smoke results.

## U0 — minor UI refresh: files first, ethos implicit

This is a small layout/copy pass, not a visual rebrand or a new navigation
system. Read `weazl_ethos.md`, especially “Signal Over Noise,” “Never Tax the
Gig” and protecting flow state. Express those principles through useful space,
fast interactions and restrained styling. Do not replace old slogans with new
manifesto text. Bob's existing browser/file-manager requirements remain in
force; the ethos is not an instruction to replace this app with a TUI.

U0 can be developed independently of A1–A4, then included in A5's browser
verification and A6's handoff. Bob confirmed all four decisions below on
September 27. Implement these choices without reopening the design questions.

| Decision | Accepted choice | Status |
| --- | --- | --- |
| U1: Library heading | One compact breadcrumb/action bar; files immediately below the necessary controls | Confirmed |
| U2: copy-cleanup scope | All everyday app screens; personality stays in colors, icons and branding | Confirmed |
| U3: controls | Search and core actions visible; secondary filters in a popover with visible, clearable active-filter chips | Confirmed |
| U4: right-hand panel | Reclaim the permanent panel's space; file details on demand and background uploads in a floating tray | Confirmed |

Confirmed implementation direction:

- Apply copy cleanup across everyday working screens, not just Library and
  Photos. A wholesale rewrite of home/landing copy is outside this selection.
  Remove “The Weazl Promise” card from the working layout with the permanent
  right-hand panel; do not relocate its slogans into another workspace banner.
- Keep upload/new-folder, search and list/grid controls visible. Put secondary
  filtering options in an accessible popover; retain clear active-filter chips.
- Make file details available on demand without reserving a permanent right
  column. Put upload progress in a floating, minimizable tray that remains
  available across navigation and does not interrupt library use.

- Remove “A file is present or it is not.” and the persistent right-click/upload
  instruction paragraph from the populated Library screen. Do not replace
  them with another large heading or leave their empty vertical margins.
- Reduce duplicate location labels. Keep genuine, clickable folder breadcrumbs
  and parent navigation. A root view must not end with a dangling `›` or an
  empty segment. Long and deep paths must remain usable at narrow widths.
- Keep functional status, actionable errors, authentication prompts and
  destructive-action confirmations. Removing fluff does not mean hiding
  failures, stripping accessible labels or making actions undiscoverable.
- Keep the established cyberpunk palette and file-type colors. Personality
  belongs in the design; working screens do not need to recite the ethos.

Implementation checklist:

- [ ] Capture local before screenshots at desktop and narrow/mobile widths.
  Record the top position of the first file row/tile and visible content area.
- [x] Record Bob's confirmed U1–U4 choices above.
- [x] Implement the confirmed choices; decision completion is not UI completion.
- [ ] Start with `library()`, `libraryBreadcrumb()` and shared `head()` usage
  in `mockup-ui/views.js`. Scope shared-helper changes so forms and dialogs
  do not accidentally lose necessary titles or labels.
- [x] Inspect `mockup-ui/index.html` topbar, `.queue-panel`, `.queue-note`,
  `.signal-card`, sidebar copy and bottom deck for the selected cleanup scope.
  Keep one useful representation of storage/dedupe information.
- [x] Remove the permanent right panel and adapt the code referencing `#queue`,
  `#side-*`, `#dedupe-card` and `#upload-tray`; do not just delete elements and
  leave event/render code pointing at missing nodes.
- [x] Preserve background uploads, per-file rails, progress, pause/retry where
  supported, and completion/error visibility. A relocated tray must not cover
  essential controls; minimization must not cancel uploads.
- [x] Keep upload/new-folder, list/grid, search, sorting and filters reachable.
  With secondary filters in a popover, active filters remain visible and clearable. Preserve
  global search behavior; compact layout must not change search scope silently.
- [ ] Preserve right-click on files and whitespace, keyboard navigation/focus,
  selection, drag/drop into folders, external folder uploads, Photos albums,
  previews, and grab-link actions. Keep touch-accessible menu alternatives.
- [ ] Put optional interaction hints in existing help/tooltips or relevant
  empty states rather than repeating them above every populated file view.
  No onboarding carousel or new attention-demanding prompt.
- [x] Adjust CSS spacing with content, avoiding fixed heights that clip long
  filenames, translated/browser-zoomed text or narrow layouts. Use existing
  styles/components; no new frontend framework or remote assets.
- [x] Regenerate embedded assets with `make desk-assets`. Extend the existing
  browser smoke only where behavior/layout changed; include keyboard and
  narrow-width checks. Compare before/after at the same viewport and confirm
  more usable file area, with no lost controls or horizontal overflow.

Gate: the populated Library opens directly onto navigation, controls and files;
the quoted filler is gone; chosen scope/panel behavior matches Bob's answers;
uploads and file-manager interactions still work. Record screenshots and local
smoke results in the handoff without committing private data or runtime assets.
The local-only/no-production/no-push boundary applies to U0 too.

## A6 — documentation and handoff — COMPLETE WITH RECORDED LIMITS

- [x] Update README with automatic behavior, exact override names, container
  limit handling, failure/retry behavior and the difference between RAM and
  disk-cache budgets. Record actual small-host support tested.
- [x] Finish `docs/photos-resource-performance-2026-09-27.md` with before/after
  commands, sample counts, cache conditions, limits and missed targets.
- [x] Update this workbook and the prior Photos workbook accurately. Leave
  unrelated outstanding items such as EXIF orientation and browser metadata
  retention visible; completing this scheduler does not complete those tasks.
- [x] Review the diff for credentials, runtime data, generated assets and
  accidental inclusion of user screenshots. Keep Go files below 300 lines;
  edit frontend sources and regenerate assets with `make desk-assets` if needed.
- [x] Provide changed files, checks/results, remaining limitations and rollback
  instructions. Local commits may be scoped by phase; no push or deployment.

Stop after the local handoff. Later, only when Bob initiates rollout: verify
rehydration is complete, retain the old image/settings, deploy with preparation
off, smoke owner access, then enable preparation at eight background workers
on the large host. Measure foreground latency and previews/sec before testing
sixteen. Pause preparation and restore the prior image if rollout checks fail;
originals and authoritative metadata must remain compatible.

## Follow-up: four requested correctness fixes — September 27

Implemented locally: nested resource discovery/startup validation and full-pipeline
memory admission; lifecycle-safe preview reads/publication; restart/manual-pause
recovery; and truthful cache/failure/retry outcomes. See
`docs/photos-smoke-followup-2026-09-27.md` for tests and remaining limits.

A cold image uses a bounded 1 MiB header probe, releases that reservation, then
reserves the entire pipeline before a separate bounded source read. This avoids
workers retaining large input buffers while waiting to upgrade reservations.
Oversized headers/images fall back without allocating beyond admission. Restic
gets a 128 MiB Go heap target, not a hard RSS promise. Raster jobs have a shared
256-job admission bound and independent waiter cancellation.

Do not mark A0/A3/A5 complete: baseline/scaling measurements, strict queue priority
and owner fairness, adaptive ramp-up and pressure hysteresis remain open. Production
must remain untouched until rehydration is finished and Bob authorizes rollout.
