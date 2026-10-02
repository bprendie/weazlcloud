# Photos dates and timeline navigation — Luna execution workbook

Date: October 1, 2026

Owner: Bob

Implementer: Luna

Status: T0–T4 local gates passed; T5 automated acceptance and documentation complete. Manual Safari/real-library measurements remain open. T6 deployment verified; supervised live dry-run/apply and final reconciliation are in progress.

## What Bob should be able to do

Open Photos, grab a quiet purple slider at the right edge, and jump to June 2018.
See photos taken then, with the surrounding timeline available in both directions.
Scroll normally and see the slider follow along. Open a photo, close it, and stay
in the same place. Do the same with a finger on a phone.

Dates must come from capture metadata. An import date is not a capture date.
Repair the existing imported dates before declaring the live timeline complete.

This workbook plans local implementation and smoke testing first. Bob asked to
make the workbook before proceeding; that does not start implementation or live
backfill. The earlier production deployment is complete and separate. A later
instruction to execute these phases authorizes local work; follow the session's
then-current deployment instruction before changing production.

## Starting facts — do not rediscover or mislabel them

- The October 1 production inventory contains 37,082 media entries and 16
  imported albums. All 37,082 entries have unknown capture dates. These are a
  dated baseline, not a count to hard-code into application logic.
- Takeout sidecars and originals remain in the vault. Their actual naming and
  metadata coverage still need an inventory; do not promise every date is recoverable.
- `internal/library/photo_metadata.go` exposes `BackfillPhotoMetadata`, but no
  operator handler/CLI invokes it. It assumes `/Photos` and checks `file.Path +
  ".json"`; that is not comprehensive matching for all Takeout exports.
- `internal/photos/metadata.go` resolves user > Takeout > embedded > client
  capture dates. Embedded capture extraction currently supports JPEG EXIF only.
- `internal/photos/migration.go` has a sequential migration helper and a JSON
  checkpoint. Do not expose its unencrypted checkpoint as new private job storage.
- `internal/catalog/metadata.go` saves the encrypted catalog for each mutation.
  The observed production catalog is about 102 MB. Calling that once per photo
  is not an acceptable live backfill implementation.
- The UI's year/month dropdowns are date filters, not a continuous scrubber.
- `/api/v1/photos/dates` provides month counts and unknown counts, but its current
  implementation only distinguishes the ordinary and Hidden collections.
- `/api/v1/photos?around=<id>` supports a bounded window around a stable ID.
  There is no direct capture-date seek operation yet.
- Existing layout mounts at most 300 photo cards and retains at most 2,000
  loaded items. Preserve those bounds and the existing derivative-only grid.
- Production preview preparation is a server background job. This workbook must
  not stop it, silently expand its allowance, or require a browser to stay open.

Read `weazl_ethos.md`, `plan_modal.md`, `docs/photo-api.md`,
`docs/photos-release-runbook.md` and `docs/photos-production-rollout-2026-10-01.md`
before editing. Keep Go files at or below 300 lines. Reuse local identity,
encrypted canonical metadata and existing storage/job primitives.

## Agreed interaction and implementation defaults

- Newest dates at the top; oldest at the bottom. Undated assets occupy an
  explicitly labeled Unknown date section, not a fabricated year.
- A narrow rail is visible on desktop and expands during interaction. Use sparse
  year markers, a readable floating month/year badge and the existing purple palette.
- Mobile gets a large touch target without a permanently wide sidebar. Adopt
  this quiet-rail recommendation unless Bob steers it differently.
- Rail position follows collection rank, with more room for months containing
  more photos. It is not an equal-width calendar with years of empty space.
  This is an approximate collection position, not an exact physical page-height promise.
- Dragging previews a date immediately using cached metadata. Fetch the final
  destination on release; do not restore originals or issue a query per pixel.
- Fine navigation supports days through a compact date picker and keyboard.
  Any day detail fetched for the rail is bounded to an engaged month/year.
- A date jump changes position. Explicit date filters remain available in the
  compact filter controls and keep their existing filtering semantics.
- The rail reflects the current timeline, album or filtered results only.
  The album overview and empty results do not show an active rail.
- Recently added remains explicitly sorted by import time. Hide the capture-date
  rail there in this pass rather than mixing two different date meanings.
- Hidden assets never affect ordinary counts, labels, anchors or seek results.
  Hidden mode has a separate, explicitly selected scope. Admins gain no vault access.
- Photo worker CPU allowance is at most 50% of effective available CPU across
  thumbnail and metadata work combined. Respect any stricter existing limits;
  do not grant each pipeline its own independent 50% allowance.

## T0 — establish the date contract and inventory

Purpose: prove which metadata can repair the collection without altering it.

- [x] Add a local diagnostic/report path using the owner's authenticated resource.
  Report known/unknown dates, provenance, candidate sidecars, supported formats,
  ambiguous matches and missing/corrupt metadata. Reports containing paths are
  owner-private and encrypted at rest; application logs contain bounded summaries.
- [x] Define resolution as user correction first, valid Takeout `photoTakenTime`
  second, embedded capture date third, and retained mobile/client capture last.
  Never use Takeout `creationTime`, filesystem mtime or upload time as capture time.
- [x] Keep UTC instants, known capture offsets and provenance separately. Calendar
  headings, date filtering, summaries and seeks use the same existing
  `photo_capture_date.go` rules. Unknown offset stays unknown; do not infer it from
  the server, browser or GPS. Test offsets crossing midnight and year boundaries.
- [x] Distinguish malformed metadata from metadata absent in an otherwise valid
  file. Unsupported embedded formats are unresolved, not lost media.
- [x] Inventory all owner-selected Photos roots, not only a hard-coded `/Photos`.
  Define how excluded roots, trash and logical still/motion components are handled.
  Process present logical assets; keep paired components and stable IDs intact.
- [x] Extend the existing small fixture with metadata-only cases: conflicting
  capture sources, midnight offsets, leap day, same timestamp/different IDs,
  missing offset, corrupt sidecar and unresolved asset. Reuse existing image bytes.

Likely files: `internal/photos/metadata.go`, `metadata_test.go`,
`internal/library/photo_capture_date.go`, `photo_metadata.go`,
`photo_metadata_test.go`, selected-root helpers and `internal/desk/photo_folders.go`.

Gate: dry run reports candidate changes and unknowns accurately without changing
catalog dates, revisions, original bytes, albums, grants or checkpoints of an
apply job. Persisting its own encrypted report/job status is permitted. No live run.

## T1 — integrate a durable, batched metadata repair job

Purpose: run safely in the service without a separate writer or open browser.

- [x] Add owner-authenticated versioned job endpoints for start/dry-run, status,
  pause, resume and explicit retry. Proposed namespace:
  `/api/v1/photos/metadata-jobs`; document the exact contract in OpenAPI before
  adding handlers. Use the existing mutation/CSRF protection and resource ownership.
- [x] Keep one active metadata repair per owner. Repeated starts return that job
  rather than duplicating work. Bind a job to roots, parser version and options.
  Status contains examined/updated/unchanged/unresolved/failed counts and phase.
- [x] Store job state, durable results and bounded failure details encrypted with
  owner-vault material. Checkpoint completed source identities/revisions after
  durable commits. Recover a commit-before-checkpoint crash by idempotent comparison.
- [x] Add a catalog batch mutation which builds one candidate state and performs
  one durable catalog save for up to 100 resolved updates. Flush the final partial
  batch. Do not implement it by calling the current saving method in a loop.
  Measure the default batch size; make it bounded and tunable if needed.
- [x] Resolve files outside the main library lock, with bounded readers and memory.
  Recheck stable ID, source identity/revision and current user edits at commit.
  A concurrent rename, replace, delete or date correction must not receive stale
  results. Retry affected assets without replacing owner-corrected fields.
- [x] Commit only capture/provenance and intentionally resolved derived fields.
  Preserve captions, favorites, rotations, hidden flags, IDs and album memberships.
  Do not blank valid derived metadata because a parser omitted a field.
- [x] Catalog persistence failure must leave in-memory authority and checkpoints
  consistent with the last durable state. Retry transient failures with bounded
  backoff; persistent shared storage failures pause/report the job, not false success.
- [x] Missing/corrupt metadata and individual source conflicts record per-asset
  outcomes and allow other files to finish. Do not fail the entire collection
  because one item is bad. Terminal states distinguish clean completion from
  completion with unresolved dates or errors; no automatic deletion follows either.
- [x] Vault lock/rekey/user revocation cancels private work and releases key-bearing
  buffers. Resume after the owner unlocks; no plaintext credential is stored.
- [x] Publish bounded metadata-change events after each durable batch and refresh
  the private query index without one full rebuild/save per asset.

Likely files: `internal/catalog/metadata.go` plus a small new batch module,
`internal/library/photo_metadata*.go`, existing encrypted job helpers,
`internal/desk/photo_preparation.go`, API routing and `docs/photo-api.yaml`.

Gate: both Restic/shared tests prove pause/resume, process restart, idempotent
second run, concurrent edits, one bad item among good items, and durable storage
failure handling. Count saves: one catalog write per processing batch with changed assets,
excluding job checkpoints, rather than one full catalog write per asset.
Sparse changes may span more batches than ceil(changed assets / batch size).

## T2 — reliable Takeout matching and bounded metadata reads

Purpose: recover available dates without guessing or loading huge files into RAM.

- [x] Match ordinary `name.ext.json`, supplemental metadata naming variants and
  Takeout collision/truncation cases using a directory-local sidecar index and
  validated sidecar title/original identity where available. Fixture each supported
  form; prefer exact unambiguous matches and report ambiguity instead of guessing.
- [x] Bound sidecar size, JSON parsing and candidate counts. Reuse the per-directory
  index; do not rescan all vault files for every photo. Reject path traversal and
  accidental sidecar matches across owners, roots or unrelated album directories.
- [x] Process sidecar candidates first. Read embedded data only when needed for
  missing capture information or explicitly requested derived metadata. Avoid the
  existing helper's unconditional original-prefix restore before sidecar lookup.
- [x] Keep reads bounded (existing 4-MiB prefix is a starting limit). Verify the
  actual Restic/shared source path: a small output prefix does not alone prove
  that underlying restore avoids reading/spooling the entire original.
- [x] Reuse JPEG EXIF parsing. Publish actual embedded-format capability; HEIC,
  AVIF, PNG and video capture extraction are not silently assumed to exist.
  Their valid Takeout sidecars can still provide dates. Add further parsers only
  through measured, bounded existing native-worker paths and tested provenance.
- [x] Do not regenerate thumbnails for date-only changes. Preserve pixel cache
  identity while refreshing chronological metadata and the mobile sync journal.
- [x] Apply the same resolver to future Takeout/mobile ingestion so another import
  does not recreate the missing-date backlog. Mobile capture remains a fallback
  and cannot overwrite a user correction.

Gate: supported sidecars recover dates for all existing fixture media formats;
unmatched/unsupported embedded cases remain explicitly unknown. Tests verify
bounded reads, no whole-video/ISO buffering and zero source deletion. Benchmark
metadata-only batch overhead separately from actual source-read costs.

## T3 — scoped timeline summaries and direct date seeking

Purpose: jump to a destination without fetching all intervening pages.

- [x] Introduce a shared validated query scope used by listing/search, bucket
  summaries and seeks: owner, roots, album, favorites/archive/Hidden and existing
  search/date/type/camera/outside-album filters. Do not duplicate subtly different
  predicates. Counts and seek results must describe the same visible collection.
- [x] Extend `/api/v1/photos/dates` compatibly with scoped counts, generation,
  known/unknown totals and month ranks. Return `months: []`, not `null`, for no
  dates. Bound optional day buckets to one requested month/year.
- [x] Add a direct seek operation, such as
  `GET /api/v1/photos/seek?at=2018-06-15&...scope...`. Return a bounded page around
  the anchor, its stable ID, position/rank, generation and before/after cursors.
  This is navigation within the scope; it must not silently add a day-only filter.
- [x] Define day seeks as the newest visible asset on that capture-calendar day;
  for an empty day choose the closest available day, preferring the newer day
  on a tie. Clamp dates outside the collection to its dated endpoints. Unknown
  has an explicit target. Empty scopes return an empty result without another
  owner's/date filter's assets. Add rank seeking for precise rail interpolation.
- [x] Known source offsets can make local calendar days non-monotonic in UTC
  order. Use indexed calendar buckets/ranks rather than binary-searching raw UTC
  timestamps for a local day. Within a day retain the timeline's deterministic
  timestamp + stable-ID ordering. Test midnight and duplicate timestamps.
- [x] Cache scope projections with bounded entries and invalidation by metadata
  generation/album revision. Warm seeks use indexed lookup and bounded page copy;
  avoid restoring sources or scanning the whole catalog on every pointer movement.
- [x] Bind cursors to scope/generation. On mutation refresh once around a surviving
  stable anchor; if removed, use its nearest date. No loops or jumps to the top.
- [x] Dates/counts/anchors fail closed across users and hidden scopes. Retain
  `Cache-Control: no-store`, response/input bounds and vault-lock behavior.

Likely files: `internal/library/photo_timeline.go`, `photo_query_index.go`,
`photo_page_query.go`, `photo_search.go`, `photo_anchor.go`,
`internal/desk/photo_page.go`, API routes, `docs/photo-api.md` and YAML contract.

Gate: first/last/middle/empty-gap/unknown seeks, albums and composite filters,
offsets and generation changes pass owner-isolation tests. Prepared metadata
summary/seek requests restore zero originals. Older date-filter clients still work.

## T4 — build the desktop and mobile date rail

Purpose: make the navigation feel immediate while retaining a bounded viewport.

- [x] Put rail geometry, rank/date mapping and pointer state in a small dedicated
  module. Integrate `mockup-ui/photo-layout.js`, `views.js`, `app.js`, `engine.js`
  and styles; do not expand the monolithic app with another unrelated subsystem.
- [x] Map pointer fraction to the scoped count/rank distribution. Show sparse year
  ticks, an expanded date badge while dragging, and a clear Unknown date stop.
  All-undated collections show Unknown date without misleading year markers.
- [x] Use pointer capture, a >=44-pixel mobile interaction area and narrowly scoped
  `touch-action`. Preserve ordinary grid scrolling. Clean up capture/listeners on
  cancel, lock, navigation and viewport resize. Rail and browser scrollbar must
  not overlap each other or the floating upload tray.
- [x] Update the badge at most once per animation frame from cached summaries.
  On release issue one destination request; abort stale seeks and reject their
  responses by request ID, owner, scope and generation. Do not prefetch every
  thumbnail passed during a long drag. A small final-window prefetch is permitted.
- [x] Render bounded destination skeletons from metadata, then fetch derivative
  bytes. Keep <=300 mounted cards and <=2,000 cached items. Seek does not clear
  unrelated Library state, cancel uploads or create background original restores.
- [x] Keep the thumb synchronized to the visible stable asset/rank during normal
  scroll, including after cached-page eviction and density/viewport changes.
  Synchronization must not trigger another seek or rebuild all grid DOM per frame.
- [x] Keyboard: focusable control, Arrow keys for adjacent available days,
  Page Up/Down for months, Home/End for dated endpoints; compact date picker for
  exact days and an accessible Unknown date action. Use truthful position/date
  labels, focus indication and reduced-motion behavior. Do not announce every pixel.
- [x] Preserve stable asset + pixel offset and scope in mode/history state. Back,
  viewer close, album return and Library/Photos switching restore the rail and
  viewport. Add one history entry on completed navigation, none per drag frame.
- [x] Move the old year/month controls into explicit date filters if retained;
  remove misleading Jump labels on filtering controls. Keep the toolbar compact.
  Progress for date repair lives with existing task controls, not a slogan/banner.

Gate: desktop and touch-emulated mobile can jump through dates, continue both
directions, reopen/close viewer and return through history without position loss,
horizontal overflow, hidden-date leakage or original grid requests.

## T5 — smoke, measure and document

- [x] Run the relevant catalog/photos/library/desk normal and race tests after
  each backend phase. Add targeted failure-injection and concurrency tests rather
  than tests that merely duplicate the implementation.
- [x] Extend `scripts/photo-layout.test.mjs` for mapping/geometry/history bounds
  and `scripts/smoke_modal_photos.py` for real dated rail, touch, keyboard, filters,
  stale-request cancellation and continued scrolling on both storage backends.
- [x] Use the existing small photo fixture for visual/decoder checks. Use synthetic
  metadata rows for 37k/100k index benchmarks; do not create or upload 500 real
  images. Metadata benchmarks are not large-library browser performance proof.
- [x] Run `make check`, `make smoke-container`, `make smoke-browser` and
  `make smoke-photos PHOTOS_PYTHON=/path/to/playwright-venv/bin/python` before handoff.
  Run recovery checks when job persistence or shared storage changes affect them.
- [ ] Measure five cold and twenty warm runs under the 2-CPU/4-GiB baseline.
  Record cold index/unlock separately. Initial warm targets: summary/seek HTTP p95
  <=100 ms and release-to-first destination cards p95 <=250 ms with ready cache.
  Measure thumbnail decode/network separately; record misses rather than hiding them.
- [ ] Target <=16.7 ms p95 interaction frames on the test browser. Measure actual
  frame/layout/paint observations where available; ScriptDuration alone is not
  a whole-frame result. Record API/worker aggregate RSS and simultaneous upload/
  preview/metadata behavior. Do not reserve production-size RAM on small nodes.
- [x] Update README, API docs and runbook with job controls, unknown-date behavior,
  embedded-format limits, safe repair/recovery and measured results. In
  `plan_modal.md`, only close the date-backfill/scrubber items with real evidence.
- [ ] Record actual Safari/touch verification separately. Chromium emulation is
  useful but does not prove iOS Safari or a future native app works.

Local evidence: all automated checks below pass on both backends. Five browser
reloads and twenty warm visits are measured; a reload is not cold server
unlock/index construction. Twenty rail jumps meet the warm HTTP/card targets.
Active Chromium frame intervals are 16.7 ms p95 on the final small-fixture run
(earlier runs ranged 16.7–16.8 ms). Sampled API RSS is recorded, but sustained
aggregate API/decoder peaks, real-library frames and actual Safari are unmeasured.
The three measurement/device checkboxes above intentionally remain open.

Gate: local acceptance evidence is reproducible, preservation/failure tests pass,
and measurements honestly distinguish synthetic, tiny-fixture and real-library runs.

## T6 — authorized rollout and existing-data repair

Bob authorized production deployment and live repair on October 1. Execution
access was restored and the tested release is live with a fresh consistent
checkpoint and verified preservation. See [the live rollout record](docs/photos-timeline-rollout-2026-10-01.md).
A detached supervisor is running the dry-run → inspect → apply → reconcile
sequence; completion and real dated browser gates remain open.

- [x] Preserve current image/config and a consistent rollback checkpoint. Check
  active imports, uploads and preview jobs; follow the release runbook without
  wiping a vault or blindly restoring an old writer over newer metadata.
- [x] Deploy the tested image, verify readiness/public HTTPS and confirm that
  account/settings/originals/albums still reconcile. Retain the original backend.
- [ ] Run an owner-private dry run against the live selected roots and retain its
  report. Inspect source coverage and ambiguity before the apply job.
- [ ] Start the durable batched repair. Let it run without browser presence;
  unrelated thumbnails continue within the shared CPU/I/O budget. Keep corrupt
  sources and failures for explicit later retry. Do not remove originals, sidecars,
  duplicate entries, old repositories or rollback material as part of this job.
- [ ] After completion, report before/after known dates, updated/unchanged/
  unresolved/error counts and retained corrections. Reconcile stable IDs, source
  hashes/sizes and album memberships; separately allow expected metadata revisions.
- [ ] Smoke real first/middle/oldest dates, Unknown date, filtered albums, mobile,
  viewer return and background restart. Record cold/warm production measurements
  independently from the constrained local measurements.

Gate: available capture dates are repaired, unknown/error cases are explained,
sources and memberships are preserved, and the live slider works with real dates.

## Follow-up candidates — recommendations, not approved implementation scope

These are useful behaviors to reengineer for WeazlCloud. Do not add them to T0–T6
without Bob choosing them. Existing Favorites, Archive, Hidden, imported/custom
albums, frozen grab galleries, exact duplicate grouping and metadata filters are
already present; do not rebuild them as supposedly missing features.

| Priority | Addition | WeazlCloud approach and dependency | Reference |
| --- | --- | --- | --- |
| Next | Visual duplicate review and photo stacks | Extend existing exact-hash groups with optional derivative perceptual matching. Owner chooses a representative or groups bursts/RAW+JPEG; keep originals and album references. Physical storage dedupe and visual similarity are different. No automatic deletion. | [Immich duplicate review](https://docs.immich.app/features/duplicates-utility/) |
| Next | Saved searches / rule-based albums | Save existing metadata filters such as videos, date range, camera or photos outside albums; update matching membership dynamically. Minting a grab freezes the result at creation, preserving capsule semantics. Smart albums are a roadmap idea, not assumed to be an existing Immich feature. | [Search filters](https://docs.immich.app/features/searching/), [roadmap](https://immich.app/roadmap) |
| Next | Crop and mirror with revert | Extend current derivative rotation with an encrypted edit recipe. Originals never change; edited export and original download are explicit choices. | [Non-destructive editing](https://docs.immich.app/features/editing/) |
| Next | Compatible video playback / Live Photo playback | Logical still/motion preservation already exists. Add actual paired playback and bounded background browser-compatible video proxies for unsupported codecs; retain originals and small-host fallback. | [Immich media milestones](https://immich.app/roadmap) |
| Later | Local OCR and natural-language photo search | Optional local worker, encrypted per-owner text/embeddings, resource limits and offline model provisioning. Search for a receipt's text or beach photos; index prepared derivatives and respect Hidden scope. | [Immich searching](https://docs.immich.app/features/searching/) |
| Later | People and places | Optional local face clustering and offline location lookup. Owner names/merges groups; maps need self-hosted tiles to avoid exposing browsing locations. Exclude Hidden material from ordinary people/place results. | [Faces](https://docs.immich.app/features/facial-recognition/), [local geocoding](https://docs.immich.app/features/reverse-geocoding/) |
| Later | Tags and portable metadata export | Add owner-managed tags, import XMP/IPTC and offer explicit metadata sidecar export. Retain editable catalog authority and do not modify encrypted original files implicitly. | [Immich tags](https://docs.immich.app/features/tags/) |
| Later | Family/event contributions | A separately scoped upload invitation with expiry and owner-controlled acceptance. Ordinary burnable grab links remain frozen downloads; they do not silently become writable vault links. | [Immich sharing](https://docs.immich.app/features/sharing/) |
| Native-app track | Backup receipts and safe device-space cleanup | Show durable server verification, retry/resume and per-device backup state. A future phone app may offer explicit device cleanup only after verified original retention; server receipt alone is not a disaster backup. | [Mobile backup](https://docs.immich.app/features/mobile-backup/) |

Recommendation: finish the date rail first, then select visual duplicate/stack
review and saved searches. Local OCR, people and map views can come later behind
optional resource budgets. An on-this-day view could be an optional date query,
without notifications or a feed imposed on the user.

## Handoff ledger

| Phase | Status | Evidence / remaining work |
| --- | --- | --- |
| T0 | Local gate passed | Typed API/date contract; fixture dry run records initial known/unknown counts and outcomes without catalog changes. JPEG EXIF capability is explicit. |
| T1 | Local gate passed | Encrypted job/report, 100-asset candidate commits, bounded retries, pause/restart/resume, manual-edit guards and injected durable failures pass normal/race checks. Pending restart tested with real Restic/shared sources. |
| T2 | Local gate passed | Conventional/supplemental and validated-title matches; ambiguity/size bounds; 1-TiB synthetic prefix cancellation; automatic late-sidecar repair and byte preservation. No additional embedded formats claimed. |
| T3 | Local gate passed | Scoped month/day ranks, nearest-day and rank seeks, bounded before/after pages, filtered and cross-owner cursors. Warm metadata-only p95: 0.336 ms at 37,082 rows; 0.132 ms at 100,000. |
| T4 | Local gate passed | Desktop and Chromium-touch pointer release, keyboard/date picker/Unknown, viewer/history/mode restoration, canceled seeks and tray clearance pass on both backends. Safari remains separate. |
| T5 | Automated acceptance passed; manual measurements open | Full normal/race/check, container restart/migration, browser, Photos and recovery pass. Rail cards p95: 72.01/73.45 ms; HTTP pair: 4.18/4.08 ms (Restic/shared). GitHub CI run 36925632808 passed checks, container and browser. Actual Safari, cold index and real-library sustained resource gates remain open. |
| T6 | Live deployment passed; repair in progress | Fresh 275,048-file checkpoint; original mounts/backend/limits retained. 96,971 Library entries, 37,082 media and 16 albums reconcile; sample originals unchanged. HTTPS and desktop/mobile Chromium pass. First live dry-run checkpoint: 100 examined, 98 recoverable dates, two failures retained. Supervised dry-run/apply and final source/date reconciliation remain running/open. |

After each phase, record changed behavior, tests, measurements and limitations.
Leave failed gates open. A photo missing metadata is not a reason to stop the
collection; a failed durable catalog write is not a reason to claim success.


## October 1 implementation evidence

See [the verification record](docs/photos-timeline-verification-2026-10-01.md)
for exact behavior, recovery controls and measurement limits. New code lives in
small catalog batch, metadata job/resolver and scoped navigation modules; the
rail has its own JavaScript module. The existing encrypted catalog remains the
authority. No new database side-container, storage cutover, original deletion or
production preview allowance change is part of this release.

The 100-result processing batch is a fixed bound, not a production auto-tuner.
It makes one catalog save for the changed subset and no catalog save if that
subset is empty; sparse changed results can span more batches than
ceil(changed / 100). One batch containing two changes advances the catalog once.
Metadata-only synthetic scale measurements do not close the real-library browser
or cold unlock gate. Node RSS snapshots do not close a sustained mixed-load peak
gate. Actual iOS Safari verification remains pending.

### Extraction performance follow-up

The first production dry-run batch exposed repeated Restic index startup as a
major bottleneck. The [acceleration record](docs/photos-metadata-performance-2026-10-01.md)
targets one-hour extraction of the existing 37,082 assets. The implementation
uses one authenticated reader session and bounded parallel resolutions, retaining
the original encrypted checkpoint and single catalog coordinator. Sample source
throughput is measured separately from complete repair and dated-UI acceptance.
