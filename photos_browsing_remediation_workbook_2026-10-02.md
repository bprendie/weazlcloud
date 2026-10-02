# Photos browsing and preview remediation workbook

Date: **October 2, 2026**
Status: **Deployed and smoke-tested; device/fault-matrix follow-ups documented**
Baseline: `3c8b9e4e8099ec664a68597e7b478ce9da32efd1`
Previous production application: `15c45144b660eb5d673851d1ca0ae51396407116`
Released code: `754465ea546fe68b0a472e116507a493acb795dc`

## Outcome

After jumping to a date, ordinary mouse-wheel, trackpad, touch and keyboard
scrolling must continue through photos in **both directions**. The date rail and
grid describe the same position in one continuous collection. A loaded page is
never an artificial beginning or end of the timeline.

HEIC previews must work in the deployed isolated worker. Recover affected preview
jobs without reimporting originals or rebuilding successful previews. Distinguish
worker/environment failures from unsupported or damaged originals.

An iPhone Live Photo appears as **one still-photo tile with a Live badge**. Opening
it shows the still image; an explicit Play motion control plays its paired clip
on demand. Preserve both original resources and repair confidently identifiable
existing imports without uploading or rehydrating them again. Genuine short videos
remain videos; duration alone never determines whether something is a Live Photo.

The owner authorized implementation and a production rollout. See
[the implementation and validation record](docs/photos-browsing-remediation-2026-10-02.md)
for delivered behavior, repair scope and physical-device follow-up. The findings
below describe the previous production release, before this remediation.

## Implementation record

Delivered modules and API contracts are recorded in the linked release note.
Implementation for P1, P2, P2L, P3 and P4 is present; P5 closes with the recorded
release checks and production verification. Detailed unchecked items below are
coverage follow-ups, not a claim that every hardware/device/fault combination
was exercised. In particular, rotated/mirrored real iPhone HEIC variants, Safari
and the exhaustive delayed-delete/lock/device matrix require separate validation.

The October 2 follow-up reproduced snap-back independently of preview generation:
strict CSP rejected HTML spacer styles before a deferred CSSOM layout, clamping
scroll to a short temporary document. Layout now applies synchronously before
scroll restoration. Background updates seek around the current visible asset;
late refresh/mode frames cannot cancel newer navigation. Paging anchors are
captured when a response arrives, and an absent Hidden/deleted date group has a
bounded in-scope fallback. The 4,000-row metadata-only regression includes a
held page response with continued wheel input and a real SSE metadata mutation.

## Confirmed findings

### Scrolling: automatic pagination is one-sided

Relevant code:

- `mockup-ui/app.js`: `photoMoreObserver`, `hydratePhotoPager`,
  `loadPhotoPage`, `restorePhotoJump`, `schedulePhotoGridWindow`.
- `mockup-ui/views.js`: `photoGridLayout`, `photoGridMarkup`, `refreshPhotoGrid`,
  and the `photos-earlier` button.
- `mockup-ui/photo-timeline.js`, `photo-layout.js`, `mode-memory.js`.
- `internal/library/photo_navigation.go`, `photo_navigation_scope.go`.

The observer watches only the bottom load-more control. The main grid has a
manual previous-page button; fullscreen-viewer navigation has a separate previous
page path. Wheel scrolling in the grid does not invoke that path.

The grid's virtual spacers cover only `state.photoItems`, not the whole filtered
collection. A seek replaces this array with a small window. Layout and restoration
use `window.scrollY`, `window.innerHeight` and `window.scrollTo`, so the page reaches
its physical top even when the API reports a valid `previous_cursor`.

There are additional cursor correctness hazards to fix with the scroll behavior:

- Appending a page overwrites `photoPreviousCursor` with that page's cursor,
  although the first retained page can still be much earlier in the collection.
- Incoming pages are unshifted/pushed without an asset-ID merge.
- The 2,000-item trim is not accompanied by cursor reconstruction for both
  retained boundaries. Reversing direction can therefore overlap or skip data.

The API already returns scoped previous/next cursors, `start`, `position`, `total`,
`anchor_id` and generation. Its date summary includes month counts and ranks.
Reuse this foundation; a new database or a full-library listing is unnecessary.

### Previews: a worker scratch-directory mismatch breaks HEIC

At **18:51:46 UTC**, authenticated preparation status reported:

| Field | Observed value |
| --- | ---: |
| Status | running |
| Total | 37,061 |
| Ready / bundle ready | 4,597 / 4,597 |
| Failed | 668 |
| Position | 5,265 |
| Reported CPU budget / workers / readers | 8 / 8 / 4 |

These are a point-in-time snapshot, not final counts. Both containers were healthy.
Five assets sampled from HTTP-400 thumbnail requests were HEIC originals, roughly
1.3–2.1 MB. All five returned `photo worker rejected media` at 320 px; the first
two also failed at 1280 px. No claim is made that all 668 failures share one cause.

Confirmed configuration chain:

1. `deploy/Dockerfile` sets `TMPDIR=/data/tmp` for the shared app/worker image.
2. The worker overrides `/data` with an empty 1 MiB tmpfs and has a writable
   64 MiB `/tmp` tmpfs. Its Compose environment does not override `TMPDIR`.
3. Production inspection confirmed `TMPDIR=/data/tmp` and that `/data/tmp` does
   not exist. `heif-convert` and the libde265 HEIC decoder are installed.
4. `internal/library/heif_pipe.go` calls `os.MkdirTemp("", "weazl-heif-")`.
   This fails before libheif sees the image. Source/output bytes use anonymous
   memory files; the temporary directory is only for suffix-preserving symlinks.

A local synthetic HEIC was generated and rendered using the current image,
non-root user, read-only root, no network, 2 CPUs / 4 GiB and matching tmpfs mounts:

```text
TMPDIR=/data/tmp source_bytes=734 output_bytes=0 mime=
error=HEIF conversion: stat /data/tmp: no such file or directory

TMPDIR=/tmp source_bytes=734 output_bytes=671 mime=image/jpeg error=<nil>
```

Only the environment override changed between these runs. This proves an
environment defect with valid media; it does not prove every production original
is decodable. The existing health check only connects to the worker socket, and
the existing render probe uses PNG. Neither detects this failure.

### Error handling conceals the cause and can preserve the failure

`cmd/weazl-photo-worker/main.go` collapses render errors to HTTP 422; bundle
responses end with a failure flag. `internal/previewrpc/client.go` and `bundle.go`
reduce these to `ErrRejected`. The public handler reports HTTP 400, and
`photo_job_ingest.go` treats rejection as invalid/unsupported media and not
retryable. Encrypted negative records in `.weazl-preview-failures` can then stop
later preparation attempts. A corrected environment alone is not a complete
recovery plan for those already-failed jobs.

### Live Photos: the pair model exists, but import and presentation are incomplete

Confirmed from local code, independently of the production preview sample:

- `internal/catalog/photo_ingest.go` atomically publishes a primary `original`
  plus an optional `motion` component. The child has `PhotoParentID`; the primary
  exposes `PhotoComponents`. Mobile ingestion already accepts these resources.
- `internal/library/photo_index_rows.go` excludes paired children from the
  timeline. `internal/photos/model.go` likewise projects one logical asset.
- `photo_pair_lifecycle_test.go` and `photo_selection_visibility_test.go` already
  cover paired export, delete/restore, copying and Hidden component protection.
  Reuse these rules rather than inventing a parallel Live Photo representation.
- `internal/takeout/entry.go` imports files independently and remembers album
  sidecars, but does not establish still/motion relationships. The current
  capture parser reads dates, not a Live Photo content identifier.
- `mockup-ui/engine.js:toFixture` discards `components` and `parent_asset_id`.
  `mockup-ui/app.js:renderPhotoViewer` chooses image versus autoplaying video by
  filename extension alone. There is no Live Photo playback control.

These gaps explain how an unpaired motion resource can be presented as a normal
short video. We have not identified the user's specific affected production files;
do not assert every two-second clip is a Live Photo or that every export retains
the metadata needed for automatic pairing. The HEIC worker defect can also prevent
the associated still from producing a preview; fixing it remains the first step.

## Immich reference and chosen direction

Reviewed Immich main at `c5e06dcfd1b35f8625863108a60d370e5b61c58e`:

- [Timeline.svelte](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/web/src/lib/components/timeline/Timeline.svelte): a dedicated scrollable asset pane, virtual total height, and date groups positioned within that space; the scrubber targets that same scroll position.
- [Intersection support](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/web/src/lib/managers/timeline-manager/internal/intersection-support.svelte.ts): viewport proximity includes space above and below the visible region.
- [Layout support](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/web/src/lib/managers/timeline-manager/internal/layout-support.svelte.ts): unloaded months retain estimated geometry; loaded groups get calculated geometry.
- [Load support](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/web/src/lib/managers/timeline-manager/internal/load-support.svelte.ts): group loading is abortable and scoped.
- [Image settings](https://docs.immich.app/administration/system-settings/#image-settings-thumbnails-and-previews): distinct small timeline thumbnails and larger viewer previews. Preserve WeazlCloud's existing bundle/cache separation.
- [Browser height-limit report](https://github.com/immich-app/immich/issues/30061): very large virtual heights can hit browser limits. Include bounded-coordinate testing rather than assuming unlimited CSS height.
- [Live Photo matching in MetadataService](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/server/src/services/metadata.service.ts): reads `ContentIdentifier`/`MediaGroupUUID`, matches the opposite media type within owner/library scope, and associates motion with the still. Use the metadata association principle with WeazlCloud's existing component model; do not copy its visibility mutations, because our Hidden mode means private photos.
- [Link/unlink motion action](https://github.com/immich-app/immich/blob/c5e06dcfd1b35f8625863108a60d370e5b61c58e/web/src/lib/components/timeline/actions/LinkLivePhotoAction.svelte): an explicit owner correction path for pairing.
- [Apple paired-video resource](https://developer.apple.com/documentation/photos/phassetresourcetype/pairedvideo): the original video resource belonging to a Live Photo. Future iOS uploads should send PhotoKit's explicit resource relationship through the existing component API.

The following is our implementation design using existing Go and browser modules.
Adopt the interaction and virtualization principles; do not transplant Immich's
framework, database or whole-month response sizes.

Settled decisions:

- One Photos scroll container shared by grid navigation and date-rail position.
- Date jumps change position, not the active date/search/album filter.
- Native scrolling works above and below a jump without clicking load buttons.
- Bounded server pages, bounded retained metadata, bounded DOM and existing blob
  cache limits. Virtual space includes unloaded content in both directions.
- Keep settings and repair controls in the Photos menu. Keep the ethos implicit.
- Preserve Hidden/archive boundaries, selections, dates, stable IDs, mode memory,
  history, viewer position, cache privacy and original storage.
- Fix worker scratch configuration first. Do not increase CPU/memory limits to
  compensate for the HEIC defect. Keep all Go files strictly below 300 lines.
- Live Photos use their still as the primary asset and poster. Motion is a
  component, not a second timeline entry or a reason to auto-play the grid.
- Automatic repair requires trustworthy pairing metadata or an explicit source
  relationship. Filename/capture-time similarity supplies review candidates only.

## P0 — Save regression evidence and fixtures

- [x] Trace grid loading, cursor boundaries, worker configuration and error path.
- [x] Inspect production health and a bounded owner-authorized failure sample.
- [x] Reproduce the HEIC environment failure locally and prove the `/tmp` control.
- [x] Review current Immich source and pin references above.
- [ ] Add durable small fixtures/tests during implementation: valid HEIC, rotated
  HEIC, JPEG/PNG controls, truncated HEIC, unavailable scratch directory and missing
  decoder. Use generated or appropriately licensed fixtures, never private photos.
- [ ] Add an actual mouse-wheel regression: seek into a multi-page collection,
  wheel upward beyond the initially fetched page, then reverse direction.
- [ ] Add tiny Live Photo fixtures with matching and mismatching content IDs,
  an ordinary two-second video, a missing still, duplicate album copies, and a
  valid HEIC still plus playable motion. Include the existing mobile pair fixture.

Gate: tests distinguish missing environment support from invalid media and fail
on the current one-sided grid behavior. Test data needs no large production upload.

## P1 — Repair the isolated HEIC worker and make its health truthful

Primary files: `deploy/compose.yaml`, `deploy/Dockerfile`,
`cmd/weazl-photo-worker/`, `internal/library/heif_pipe.go`, container smoke scripts.

- [x] Explicitly set the **worker** `TMPDIR=/tmp`; set worker HOME/cache locations
  deliberately if needed. Keep the API's volume-backed `/data/tmp` unchanged.
- [x] Keep worker `/data` empty, read-only root, non-root UID, network isolation
  and bounded tmpfs. Do not give the worker vault keys or the live data mount.
- [x] Validate the configured scratch directory at startup: it exists, is writable
  and permits the required temporary symlink operations. Use only synthetic data.
- [x] Validate HEIF helper/decoder capability when HEIC support is advertised.
  A listening socket alone must not imply that the promised renderer is usable.
- [x] Extend the explicit render probe/release smoke to exercise HEIC and the
  bundle path under the **actual Compose worker environment**. Do not rely only
  on an in-process Go test or an app-only container with a writable `/data/tmp`.
- [x] Preserve the anonymous-memory-file conversion: never assemble private
  source images or decoded outputs as plaintext regular files on the data volume.

Gate: valid HEIC generates both 320 and 1280 variants through the isolated socket
worker; orientation is correct. An invalid scratch configuration yields an explicit
environment failure. JPEG/PNG and worker shutdown behavior continue to pass.

## P2 — Classify failures and recover affected previews safely

Primary files: worker handlers, `internal/previewrpc/`, `photo_failures.go`,
`photo_job_ingest.go`, `photo_job_store.go`, `photo_prepare*.go`,
`internal/desk/photo_page.go`, `mockup-ui/photo-cache.js` and maintenance UI.

- [ ] Carry bounded typed error information through single and bundle responses:
  worker environment/unavailable, timeout/resource pressure, unsupported format,
  corrupt media, source read/integrity and cache admission/write failure.
- [x] Maintain mixed-version RPC safety. Version any changed bundle framing and
  test old/new peers; malformed replies remain bounded and fail closed.
- [x] Treat environment/unavailable failures as service failures, not HTTP 400
  client mistakes or permanent corruption. Expose a stable error code and retry
  guidance; keep decoder stderr bounded and private rather than forwarding it raw.
- [ ] Retry temporary failures with capped backoff. Avoid a tight retry loop when
  the worker environment is broken; other supported formats should remain usable.
- [x] Recover legacy HEIC failures by reconciling owner-index format/revision with
  failed jobs and negative records. Legacy `ErrRejected` has lost detail, so do
  not label those originals corrupt. Clear/requeue only eligible failed variants.
- [x] Preserve successful cached derivatives and original hashes. Do not globally
  bump every preview key or delete the entire cache to recover this incident.
- [x] Respect explicit pause, owner lock/revoke/delete, current resource ceilings
  and live job leases. Recovery resumes/checkpoints after restart and is idempotent.
- [x] Add an owner-only, bounded failure summary/report under the Photos menu:
  counts by format and category, retryable/terminal status, and paged affected
  items. Do not expose another owner's filenames or previews to an administrator.
- [ ] Distinguish queued, retrying, unsupported and failed tiles without toast
  storms; maintain neutral placeholders and avoid repeated failing fetches on
  each remount. Scope negative client caching to asset revision, variant and
  recovery state, with a short expiry for temporary failures.

Gate: after the environment fix, targeted retry recovers valid HEICs, retains good
JPEG cache entries, and leaves a deliberately corrupt neighbor honestly reported.
Repeated recovery and restart do not double-count progress or reprocess successes.
Report exact tested recovery scope; do not promise that all production failures
are solved merely because the five samples were HEIC.

## P2L — Repair Live Photo pairing and show still-first playback

Execute after P1/P2 and before integrating the continuous timeline. Primary files:
`internal/photos/`, `internal/catalog/photo_ingest.go`, new small catalog pairing
mutation modules, `internal/library/photo_components.go`, metadata/job modules,
`internal/takeout/`, `internal/desk/photo_original.go`, `mockup-ui/engine.js`,
`app.js`, Photos grid/viewer modules and owner maintenance UI.

### Pair discovery and repair of existing files

- [x] Preserve the mobile API's explicit `original`/`motion` relationship. A
  completed pair is one asset even when the motion uploads before the still.
- [x] Extract bounded Live Photo identity metadata from supported HEIC/JPEG and
  QuickTime resources. Validate the actual fixture tags and parser capabilities;
  `ffprobe` alone must not be assumed to expose still-image maker metadata.
  Run any added media helper inside the isolated worker with memory/time limits,
  anonymous source files and typed errors. Do not introduce plaintext staging.
- [x] Index candidate identities within owner, source collection and compatible
  visibility scope. Require a unique compatible still/motion association and
  valid resource types. Never match owners together, guess from duration, or
  pair merely because basenames coincide. Reject conflicting edited versions.
- [ ] Recognize verified Takeout/source-side relationships where present. Do not
  invent a Google sidecar field or assume exported identifiers always survive.
  Keep ambiguous or identifier-free candidates unresolved and report them.
- [x] Add an owner-only, bounded, resumable discovery/repair job under the Photos
  menu, with dry-run counts for pairable, already paired, ambiguous and orphan
  resources. Discovery failures affect individual files, never the whole job.
  Respect pause, vault lock, cancellation and existing worker budgets.
- [x] Apply pair mutations atomically with checks of both IDs, revisions, hashes,
  owner scope and visibility. Preserve paths, encrypted references, original
  bytes and the still's stable ID/date/user corrections. Store the relationship
  and checkpoints encrypted. Replay/restart must not create extra children.
- [x] Reconcile album membership to the primary while preserving memberships
  represented by either component. Do not collapse separate still copies across
  albums or union private and public copies merely because identifiers match.
  Preserve favorite/archive intent; conflicting states stay unresolved for owner
  review. Linking never makes a previously private component public.
- [x] Queue reconciliation after ordinary imports so resources arriving in
  separate ZIPs or out of order can eventually pair. The archive cleanup decision
  remains based on durable original ingestion, not successful preview generation.
- [x] Add explicit owner Link motion/Unlink motion actions for ambiguous exports:
  accept one still and one compatible video, validate current revisions and
  scope, and explain the result before applying. Unlink restores independent
  timeline membership without deleting resources or losing album associations.
  Keep a reversible membership record where needed for that restoration.

### Timeline, playback and sharing

- [x] Preserve the API's visible component information in `toFixture`; derive
  Live Photo status from an accessible `motion` relationship, not `.mov` or
  duration. Timeline/date summaries/search/album counts and selection contain one
  primary per pair; Library still exposes the original stored files.
- [x] Render the primary still thumbnail with a small accessible Live badge.
  Open the still normally. Add keyboard/touch-accessible Play motion/Stop controls
  in the viewer; play once, initially muted, then return to the still. Stop and
  release playback when navigating, closing, locking or changing account.
  No hover-triggered fetch of every clip or automatic grid-wide playback.
- [x] Reuse the owner-scoped range reader for motion where its authorization is
  sufficient; otherwise add a parent-and-component route which checks the current
  pair on every request. Support Range/HEAD without buffering the whole original.
  A normal-mode still must not provide a shortcut to a Hidden motion component.
- [ ] Detect browser playback support and provide a bounded, encrypted compatible
  motion derivative when the original HEVC/QuickTime resource cannot play. Cache
  by component hash/revision and rendition version; generate on demand or within
  the existing background budget. Preserve the full-quality source untouched.
- [ ] If the still is missing, keep the orphan as an ordinary video and report
  unresolved pairing. If a confirmed pair's still preview fails, retain the Live
  relationship and an honest retryable placeholder; do not silently promote its
  motion into a separate video or erase the pair.
- [x] Update component/primary cache identities, counts and navigation generation
  on link/unlink. Preserve the current scroll anchor and selection by primary ID.
- [x] Verify photo/album grab galleries show the still and can play only the
  authorized paired motion. Both original resources must survive paired exports;
  offer Download Live Photo as a two-resource ZIP alongside Download still.
  Guest playback uses the existing capsule access/retry policy rather than a
  freely accessible owner URL or one burn for each video Range request.
- [ ] Document the iOS resource contract and which original/adjusted resources
  the present two-component schema can preserve. Report additional edited
  resources explicitly; do not silently discard them or fake Photos round-trip
  fidelity that the current API does not implement.

Gate: a verified imported pair becomes one still-first asset, plays motion on
demand, and exports both unchanged resources. Genuine short videos remain visible.
Repair is repeatable and resumable, and uncertain candidates remain untouched.
Existing pair lifecycle tests still pass, including split Hidden components.

## P3 — Build one continuous Photos scroll surface

Primary files: small new browser scroll/window modules, `app.js`, `views.js`,
`photo-layout.js`, `photo-timeline.js`, `mode-memory.js`, Photos CSS and state.

- [x] Introduce a Photos-specific scrollport with a defined viewport height below
  app controls. Keep Library mode behavior intact. All Photos layout, observers,
  date rail, restoration and viewer-return calculations use this element.
- [x] Put grid and date rail in the same scroll context. Wheel/trackpad movement
  over either navigates the photo pane. Preserve ordinary scrolling in menus and
  dialogs; avoid document-wide wheel interception and scroll traps.
- [ ] Build compact date-group geometry from the existing scoped month counts,
  ranks and totals. Keep estimated space for unloaded groups before and after
  the loaded window; replace estimates with measured layout as groups arrive.
- [ ] Keep anchor `(asset ID, offset within viewport)` stable when estimates,
  row height, density, viewport size or toolbar height change. Do not replace the
  whole pane DOM during pagination or use a hard-coded 145 px window offset.
- [x] Scroll to the accepted date/asset within that logical collection, then
  hydrate its neighborhood. The rail tracks the first visible capture date.
  Preserve unknown-date grouping and the existing special ordering for Recent.
- [x] Preserve bounded DOM (target at most 300 media cards), blob leases and
  current cache limits. Do not fetch originals to populate a timeline.
- [x] Bound the physical scroll extent and map/rebase logical positions if a
  collection would exceed browser layout limits. Test huge count-only geometry
  without creating or uploading thousands of actual photos.
- [x] Adapt Back/Forward, mode restoration, fullscreen return, deep links,
  selection/keyboard focus and touch behavior to the new scroll owner.

Gate: a middle-date jump leaves reachable space in both directions. The browser
page scroll bar no longer defines the limits of photo navigation. Rail and grid
stay synchronized through resizing, touch and repeated date jumps.

## P4 — Make bidirectional loading and eviction correct

Primary files: a small page-window controller wired into P3,
`loadPhotoPage`, existing seek API and focused navigation tests as needed.

- [x] Prefetch when the viewport nears **either** loaded boundary. Observe both
  edges relative to the Photos scrollport; re-evaluate after layout/load settles
  so a stationary intersecting sentinel does not strand the user at an edge.
- [x] Keep page records with scope/generation, first/last asset, start/rank,
  previous/next cursor. The aggregate previous cursor belongs to the first
  retained page; the next cursor belongs to the last retained page.
- [x] Merge by stable asset ID and preserve canonical order. Do not overwrite a
  window's previous cursor whenever a page is appended.
- [x] Evict whole page records outside the retained budget, not arbitrary array
  slices with stale cursors. Keep enough geometry/cursor information to fetch
  evicted neighbors again in either direction. Preserve selected IDs independently.
- [x] Retain the 2,000-item metadata ceiling initially; avoid large empty gaps
  or index shifts in the fullscreen viewer when pages are evicted.
- [x] Serialize/admit directional loads safely, cancel obsolete seek/filter work,
  and reject late replies from another owner, scope or navigation request. Never
  combine incompatible generations. Re-anchor or explicitly resync on mutations.
- [ ] Loading/failed-neighbor placeholders retain geometry and support bounded
  retry. One failed page must not create an infinite loop or erase usable pages.
- [ ] Leave load buttons only as accessibility/error fallback, not the primary
  way to cross a page boundary. Add useful live status without stealing focus.

Gate: seek → several pages newer → several pages older → beyond an eviction →
reverse again gives the exact expected unique ID sequence, with no gaps and no
visible anchor jump. Small/API tests cover equal capture times and missing dates.

## P5 — End-to-end acceptance, documentation and later rollout

- [x] Use a small real media fixture plus mocked/synthetic metadata pages for
  boundary/eviction tests. No 500-file production upload. Browser wheel tests must
  cross actual page boundaries; the previous five-photo smoke cannot prove this.
- [ ] Test mouse wheel, trackpad-like deltas, keyboard Page Up/Down/Home/End,
  touch, rail dragging, Back/Forward and viewer return. Include narrow screens,
  unequal image aspect ratios, density changes and sparse/dense date groups.
- [ ] Test normal/Favorites/Hidden/Archived, albums and filters. A seek never
  changes visibility scope or unexpectedly turns navigation into date filtering.
- [ ] Delay/reorder/fail page responses; mutate/hide/delete the anchor; lock or
  change account mid-fetch. Confirm no stale data or Hidden thumbnail leakage.
- [ ] Test both Restic and shared storage, real isolated-worker HEIC single/bundle
  requests, timeout/unavailable/corrupt neighbors, retry and restart recovery.
- [x] Ensure required HEIC container tests cannot silently skip because a host
  encoder is missing. Generate/include the tiny fixture in the container harness.
- [ ] Test Live Photo mobile ingestion plus existing-import discovery/repair,
  both arrival orders, cross-ZIP resources, identifier conflicts, duplicate album
  copies, missing components and restart during commit. Link/unlink must preserve
  both resource hashes and membership, dates and primary ID.
- [ ] Browser-test one still tile and explicit motion playback, codec fallback,
  Range seeks, viewer navigation/close, ordinary two-second videos and photo/album
  grabs. Test owner isolation and concurrent hide/delete/revoke during playback;
  no unauthorized component appears in API details, cache, export or guest view.
- [x] Run focused tests, `make check`, build, and relevant browser/container/
  Photos smokes. Update old tests that intentionally scroll `window` to drive
  the real Photos scrollport; do not weaken the behavioral/performance assertions.
- [x] Measure idle/active-background browsing on 2 CPUs / 4 GiB. Initial targets:
  cached reverse scrolling needs no original reads, mounted media ≤300, retained
  metadata ≤2,000, prepend/eviction anchor drift ≤2 CSS px after settling, and
  warm scroll script p95 within the existing 16.7 ms budget. Record cold misses
  separately; latency targets do not imply guaranteed LAN throughput.
- [x] Update README, Photos API/error notes, deployment example, validation report
  and this workbook. Record the exact image, fixture sizes and supported recovery.
- [x] When execution/rollout is authorized: preserve the custom live Compose
  settings, explicitly carry the worker TMPDIR override into that configuration,
  inspect active jobs, follow the existing backup/upgrade runbook, then verify
  real HEIC samples and owner-directed recovery. A repo example change alone will
  not update production's customized Compose JSON.
- [x] After recovery, record before/after failure categories, recovered variants,
  remaining unsupported/corrupt cases and untouched originals/successful caches.
  Full preparation may continue in the background after the release smoke passes.

## Completion checklist

- [x] Date jump followed by wheel-up crosses into newer photos automatically.
- [x] Wheel-down and direction reversal work across fetched and evicted pages.
- [x] Rail, pane, history and fullscreen return agree on the visible position.
- [x] Valid HEICs render in the deployed worker configuration.
- [x] Environment failures are actionable and recoverable, not mislabeled corruption.
- [x] Previously failed eligible previews can recover without losing good caches.
- [x] Verified Live Photos show one still tile with motion available on demand.
- [x] Existing import pairing repairs preserve originals and ordinary short videos.
- [x] Pair playback, exports and grab links respect Hidden and owner boundaries.
- [x] Tests exercise both directions and actual HEIC container rendering.
- [x] Originals, privacy boundaries, settings and existing media remain intact.

Recommended execution order: **P0 fixtures → P1 → P2 → P2L → P3 → P4 → P5**.
P1/P2 are independently releasable once their specific container/recovery gates
pass and rollout is authorized; the Photos UI work does not need to delay that fix.
