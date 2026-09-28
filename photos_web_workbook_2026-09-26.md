# Fast web Photos workbook — September 26, 2026

Owner: Bob. Implementation: Codex. Status: **implemented locally, extended by the September 27 resource workbook; remaining limits are explicit below.**

Follow-up: [Adaptive Photos generation — September 27](photos_resource_workbook_2026-09-27.md)
gives Luna the local-only implementation plan for resource detection, parallel
preview generation, bounded memory and resumable scheduling. It preserves the
unfinished tasks and production boundary in this workbook.

## Mission and deployment boundary

Make Photos open and scroll quickly, including albums, with a large existing
library. Prepare previews before browsing and avoid work proportional to the
whole collection on every request. This is the first web performance pass,
not the mobile app or a storage-backend migration.

**LOCAL CODEBASE AND DISPOSABLE LOCAL CONTAINERS ONLY.** Bob explicitly deferred
production deployment until the current Takeout rehydration has finished.
Do not SSH to production, pull changes there, rebuild/restart its container,
change its cron/importer, run a backfill there, or touch its vaults/staging.
Finishing this workbook does not authorize deployment. Leave a rollout checklist
for Bob to initiate after import completion and verification.

Execution order: P0 baseline → P1 photo index/API → P2 preview cache →
P3 background preparation → P4 web grid → P5 local verification/handoff.
Work in that order; mark each gate only when its evidence exists. If time runs
out, record the exact remaining task and leave the last working state usable.

## Decisions already made for this pass

- Preserve `/Photos` and existing Takeout album paths, titles and membership.
  Preserve originals, catalog identities, storage references and vault settings.
- Keep existing duplicate-photo semantics. Combining multiple album copies into
  one timeline asset is a later feature; do not silently hide or merge entries.
- All accounts remain local. No outside identity, analytics, CDN or image service.
- Indexes and previews are private to the owner and encrypted at rest. A hash,
  cursor, entry ID or cache filename is never authorization to read an image.
- Use a derived photo index, rebuilt from authoritative catalog state. No new
  database service, canonical catalog format migration or Restic redesign tonight.
- Default page size 100, maximum 200. Sort by current modification time descending,
  then stable entry ID. True capture-date/EXIF/Takeout-sidecar reconciliation is
  deferred; do not label modification time as date taken.
- Keep existing supported formats working. Optimize JPEG/PNG first; leave existing
  fallback behavior for other formats. HEIC/RAW decoding, video transcoding,
  Live Photos, face recognition and semantic search are separate workbooks.
- Start with 320-pixel grid and 1280-pixel viewing derivatives. JPEG quality about
  80 for opaque photographs; PNG for transparency. Preserve original download.
- Initial preview budget: configurable 4 GiB per owner, plus a configurable
  16 GiB node-wide ceiling. These are evictable-cache limits, not user file quotas.
  All allocations still obey the shared disk reserve. Document both settings.
- Start with two global rendering workers, at most one background job per owner.
  A visible request takes the next free slot ahead of background work. Do not
  hard-code workers from the production machine's 32-core allocation.
- Target a useful first viewport within 1 second on a LAN-equivalent local run
  with server derivatives already prepared, and warm page API p95 below 200 ms.
  These are measurement targets, not grounds to claim unmeasured production speed.

## Read these files first

| Area | Existing files | Why it matters |
| --- | --- | --- |
| Photos UI | `mockup-ui/views.js` (`photos`), `mockup-ui/app.js` (`loadLibrary`, `loadPhotoAlbums`, `syncLibraryFromChange`) | Photos currently filters the full library and redraws on changes. |
| API client/routes | `mockup-ui/engine.js`, `internal/desk/multi.go`, `internal/desk/photo_albums.go` | Reuse owner authentication and vault checks. |
| Albums | `internal/library/photo_albums.go`, `photo_album_cache.go` | Album listing currently scans catalog state; preserve metadata behavior. |
| Catalog access | `internal/library/library.go`, `internal/catalog/` | `ensure` reloads the catalog. A thumbnail cache hit still begins with metadata lookup. |
| Previews | `internal/library/thumbnail.go`, `thumbnail_maintenance.go`, `internal/desk/thumbnail.go`, `preview.go` | Current raster cache: 256 MiB/4,096 files, PNG output, path-sensitive keys, on-demand rendering. |
| Browser queues | `mockup-ui/app.js` thumbnail observer, queues and preview warming | Reuse bounded loading; remove duplicate warming paths. |
| Existing checks | `scripts/smoke-photo-albums.py`, `internal/library/thumbnail_test.go`, `thumbnail_benchmark_test.go`, `Makefile` | Extend relevant coverage instead of building a separate test framework. |

Paths without a directory in a row are relative to the preceding directory.
Keep Go files below 300 lines. Edit frontend source in `mockup-ui/`; regenerate
embedded assets with `make desk-assets`. Do not commit generated assets,
credentials, screenshots, caches, binaries or runtime data. Preserve existing
local data volumes and unrelated working-tree changes.

## P0 — establish a small, repeatable local baseline — PARTIAL

- [x] Record the starting commit: `ace4365`.
- [ ] Record the pre-implementation working-tree status (it was not captured;
  unrelated user edits were already present).
- [x] Use the existing album fixture in explicitly disposable local containers.
  Reuse existing images; do not download production data or create a 500-file
  upload test. Avoid ports and volumes used by another local service.
- [ ] Record comparable cold and warm Photos opening, album opening and opening one image.
  Measure API latency, request count, transferred bytes, visible image readiness,
  rendered tile count, catalog loads and original-content reads/restores.
- [x] Label the browser smoke results as warm server index with a small fixture;
  repeat-client and cold-cache timings remain unmeasured.
- [x] Put available measurements and commands in
  `docs/photos-web-performance-2026-09-26.md`. Before/after and detailed
  resource measurements remain outstanding.

Gate: the implementation smoke confirms direct Photos navigation avoids the
full-library request. A reproducible before-change baseline and the requested
latency/resource measurements are still missing. See
`docs/photos-web-performance-2026-09-26.md`.

## P1 — serve a screenful from an owner-private photo index — IMPLEMENTED

Suggested new files: small `internal/library/photo_index*.go` and
`internal/desk/photo_page*.go` modules. Names are suggestions, not existing files.

- [x] Define a derived row: entry ID, content fingerprint, current path, mtime,
  media type, size, optional width/height, album membership and preview status.
  Unknown dimensions must be allowed; first-page listing must not restore images.
- [x] Build one owner snapshot from catalog state after unlock. Persist an
  encrypted, versioned snapshot atomically under the owner's data directory.
  A missing/corrupt snapshot is rebuildable and must not damage originals.
- [x] Make snapshot freshness explicit: identify all authoritative mutation paths,
  including uploads, Takeout, move/rename, replacement, Trash, restore and user
  deletion. Track a generation and update affected rows only after commit.
  Missed events/restarts must trigger reconciliation. Do not rely solely on a
  browser event listener, a time-based cache or catalog file mtime for correctness.
- [x] Do not rebuild/sort the entire index per page, reread it from disk per
  thumbnail, or write the whole derived snapshot for every imported file.
  Batch persistence with bounded lag; reconcile with catalog state on restart.
- [x] Add `GET /api/photos?limit=100&cursor=...&album=...`. Return `items`,
  `next_cursor` and `generation`. Reuse owner/vault guards. Never take an owner
  identity from the query string as authority.
- [x] Expose index readiness/progress as part of the API response.
- [x] Cursor binds sort position, generation and album filter. Specify a stable
  stale-cursor response so the client can refresh without duplicates or gaps.
  Malformed/cross-owner cursors must not leak data or cause expensive work.
- [x] Serve album counts/covers from the same snapshot. Existing album API may
  remain as an adapter; repeated album loads must not scan the catalog.
- [x] Ensure preview authorization uses fresh owner/entry state without forcing
  a catalog reload per tile. Index lag must never expose deleted/replaced content.
- [x] Keep Library behavior intact; Photos no longer depends on `/api/library`.

Gate: local API and browser smokes cover pagination, restart, direct Photos
navigation, and locked/other-owner isolation. Startup index reconciliation has
no separate API readiness/progress signal yet.

## P2 — reusable, bounded preview storage — IMPLEMENTED WITH LIMITS

- [x] Key derivatives by owner scope, verified content hash, renderer version,
  size and rendering options. Exclude filename/path and rename-only revision.
  If no verified hash exists, use a safe fallback tied to content identity.
  Every request still checks access to a current, live owner entry.
- [x] Make moving/renaming reuse the derivative. Replacement selects new content;
  changing renderer/options selects a new version. Existing old caches may be
  evicted naturally; do not require an original-storage migration.
- [x] Generate compact JPEG for opaque photos and PNG where transparency matters.
  Use reasonable resampling. Correct EXIF orientation remains outstanding. Do not
  publish an incomplete/corrupt preview as a successful cached result.
- [x] Inspect dimensions before full decode; enforce a decoded-pixel/memory budget
  as well as compressed byte limits. Large unsupported inputs get a stable
  fallback, not a retry loop. Bounded source staging belongs on the data volume.
- [x] Keep encrypted atomic writes and coalesce requests for identical derivatives.
  Bound queued jobs and concurrent decoded-image memory independently of worker count.
- [x] Implement configurable per-owner and node-wide budgets using actual disk
  accounting. Reserve temporary space and honor the existing silent disk reserve.
  Preview failure must not fail a successfully stored original.
- [x] Replace full-directory scan/sort on every write with bounded bookkeeping
  and periodic cleanup. Cleanup must not remove an active writer's temp file.
  Reconcile accounting on startup, eviction and owner deletion.
- [ ] Distinguish permanent unsupported/corrupt preview failures from transient
  failures. Cache permanent failure status by content/renderer version; bounded
  retries/backoff for transient errors, with a manual retry/rebuild path.
- [x] Use private versioned HTTP caching. Clear in-memory/object-URL state on
  logout, lock and account changes; never render a prior owner's cached image.
  No public endpoint serving a derivative by hash alone.

Gate: implementation enforces owner checks and bounded cache/render resources;
local Restic/shared browser smokes pass. Durable per-content failure caching and
an explicit manual failed-item retry path remain outstanding.

## P3 — prepare thumbnails before the browser asks — IMPLEMENTED WITH LIMITS

- [x] Implement a server-owned bounded queue fed by successful commits and a
  resumable scan of missing derivatives. Do not enqueue the entire collection
  in memory. Resume by reconciling stored readiness and content identity.
- [x] Separate visible work from backfill priority, coalescing duplicate jobs.
  A clicked image's viewing preview outranks background grid preparation.
- [x] Default backfill prepares grid size only. Prepare viewing size on demand
  and at most the next/previous image speculatively; avoid doubling backfill cost.
- [x] Include dimensions in index updates after rendering. Render outside catalog
  locks; recheck identity before publishing results after replacement/deletion.
- [x] Pause on vault lock, shutdown, disk pressure or active bulk import. Cancel
  safely and resume after unlock/idle. Never implicitly unlock somebody's vault.
  Visible reads may proceed within their resource limits.
- [x] Expose owner-scoped counts: ready, queued, failed, paused and pause reason.
  Offer a local UI retry for failed previews; do not expose internal storage paths.
- [x] Make backfill an explicit owner action for the first release, with a persisted
  enabled flag. Default off on upgrade so deployment cannot unexpectedly trigger
  a large job. Generate derivatives for new ordinary uploads once enabled.

Gate: local smoke exercises preparation and restart; no production backfill was
run. Permanent failure status is not durable and initial index readiness is not
reported separately.

## P4 — connect a lightweight Photos grid — IMPLEMENTED WITH LIMITS

- [x] Add a dedicated Photos state/store and paginated API client. Direct links,
  login bootstrap, polling, navigation and change events must not first load the
  full library or quota scan before painting Photos. Inspect all call sites.
- [x] Fetch the first 100 rows; prefetch one next page near the viewport boundary.
  Abort obsolete requests when switching album, view, user or lock state.
- [x] Render a fixed-cell, responsive grid with only viewport rows plus small
  overscan. Keep spacer heights and scroll position stable. DOM nodes are
  bounded; retained client page metadata is not yet strictly capped.
- [x] Reserve cell space immediately. Fill thumbnails individually without
  rebuilding the grid. Permanent failures show a fallback; transient failures
  have bounded retry. Album switching must not flash the previous album's photos.
- [x] Use existing viewport loading with bounded requests. Remove competing
  browser warmers for Photos now that the server queue owns background work.
- [ ] Apply affected-row updates for live changes. Generation reset can refetch
  the visible region while retaining a stable anchor; avoid full-library reloads
  and scroll-to-top on every upload or preview completion.
- [x] Open the prepared viewing derivative first, with original download available.
  Prefetch at most adjacent previews and cancel when the viewer closes. Preserve
  existing video controls and unsupported-format fallback behavior.
- [x] Preserve album deep links, back navigation, selection/context actions and
  keyboard access. If an action currently looks up a file in the global full
  library array, adapt it to the selected Photos row instead of reintroducing
  a full-library fetch. Keep focus stable during virtualization.
- [x] Show quiet preparation status when needed, with a start/resume action.
  Do not put cache budgets, worker counts or internal error dumps in the normal UI.

Gate: smoke confirms direct Photos avoids full-library listing and rendered DOM
stays bounded for the fixture; albums, preview, deep links and actions pass. Client
metadata retention and precise live-update scroll anchoring remain outstanding.

## P5 — local proof and handoff — PARTIAL

- [ ] Add focused tests for pagination with tied timestamps, stale cursors,
  mutation freshness, cache identity, failure retry and cross-owner/lock isolation.
- [x] Exercise actual Restic reads in a disposable local container; if shared
  backend is supported by the touched interfaces, cover its existing smoke too.
- [x] Extend the existing album/browser smoke for page scrolling, preview readiness,
  deep links, live updates and account switching. Reuse existing image fixtures.
- [x] For index scale only, an optional in-memory metadata fixture around 100,000
  rows is acceptable; it must not upload/store 100,000 media files. Report it
  separately from real image/render/browser measurements. No 500-file upload test.
- [x] Run focused checks during each phase and `make check` at completion; run
  relevant disposable container/browser smoke after connecting routes and UI.
- [ ] Record before/after timings on the same machine and fixture, with server and
  browser cache conditions stated. Report p50/p95 over a stated sample count,
  first-screen readiness, bytes/requests, peak memory, DOM size, catalog loads
  and source restores. Do not claim production results from local measurements.
- [x] Explain missed targets and remaining bottlenecks. Cold backfill is separate
  from steady-state browsing; all-green functional tests alone are not a speed result.
- [x] Update README with settings, cache disk usage, supported formats and how to
  enable/pause/retry backfill. Update this workbook with task status and evidence.
- [x] No push, deployment or external-system changes were made as part of this
  local-only workbook.

Stop here. Handoff: changed files/commits, local commands/results, performance
comparison, incomplete items, rollback notes and the pending production gate.

## Later production rollout — checklist only, not tonight's authorization

1. Confirm all Takeout archives reached a terminal, verified outcome. Review held
   problem ZIPs separately; never delete them merely because imports finished.
2. Bob initiates deployment. Preserve Compose mounts/settings and retain the old
   image; confirm there is no active import before restarting the service.
3. Deploy compatible code with backfill disabled. Verify root/Photos layout,
   owner access, album metadata and original-file readbacks.
4. Enable backfill deliberately, monitor disk/CPU and foreground latency, then
   measure the real collection's first-screen and scrolling performance.
5. If necessary, stop backfill and restore the old image. Derived index/cache
   formats must be versioned and disposable; this pass must not require changing
   originals or authoritative catalog format to roll back.
