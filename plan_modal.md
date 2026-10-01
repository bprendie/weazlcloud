# WeazlCloud application modes — Luna execution workbook

Date: September 29, 2026
Owner: Bob
Implementer: Luna
Status updated September 30, 2026: M0–M6 feature implementation is in place.
The permission retry cleared the local network/Docker blockage: full normal/race
checks, both backend container restart/migration smokes, basic Chromium smokes,
filesystem recovery and shared-store integration pass. M7 documentation and
authenticated browser verification are being finalized. Apple-device media and
large-library measurements remain explicit release gates. Production is excluded.

## Outcome

One application, one account and vault, two first-class modes:

- **Library:** folders, files, documents, transfers and bulk operations.
- **Photos:** capture-date timeline, albums, favorites, search and a media viewer.
- **Music:** future Subweazl Web integration. Do not implement music mode, add a
  disabled navigation item, or research its integration during this workbook.

“Modal” means application modes, not a collection of popup dialogs. Switching
modes preserves each mode's location, scroll anchor, filters and selection.
Uploads continue across modes. A photo can reveal its original in Library.
The ethos is implicit: compact controls, cyberpunk visual accents, keyboard
access, local processing and no explanatory slogans consuming the workspace.

## Locked decisions and scope

- Photos includes **selected photo folders**, initially `/Photos` and future
  phone-backup roots. Images elsewhere stay in Library until a folder is included.
- Photos must support **hidden folders**, including their descendants. Implement
  the visibility model before M3 query/UI work and complete the owner controls
  and lifecycle tests in M5. See the hidden-folder contract below; its defaults
  are implementation assumptions, not additional decisions already made by Bob.
- Photos dates mean **metadata capture dates**, never import dates. Repair the
  existing imported collection as part of M1, before shipping the new timeline.
  Keep import time separately for the explicitly labeled Recently Added view.
- Photos accepts uploads directly from mobile clients through an authenticated,
  versioned ingestion API. A browser session or open Photos pane is not required.
  Build and test this endpoint during the web work; the native iOS UI comes later.
- Files remain authoritative originals. Albums reference assets; adding/removing
  album membership does not copy, move or delete originals.
- Preserve account approval, local identity, vault isolation, rekey, revocation,
  deletion behavior, shared physical disk quota and existing storage backends.
- Keep Go for orchestration/API unless measurements demonstrate a specific need
  to change a component. Use native media libraries where they help.
- Prototype per-vault SQLCipher metadata storage. It is a candidate to validate,
  not an assertion that the current SQLite driver already provides encryption.
  A database side-container is acceptable if measured requirements justify it.
- Prefer an incremental TypeScript/Svelte Photos UI behind a feature flag. Do not
  rewrite the working Library or change its routing/upload behavior gratuitously.
- Separate media work from interactive API capacity. Bob now permits photo
  processing to use **up to 50% of available CPU**, superseding the earlier
  three-core preference in this plan. On the 32-logical-CPU production host this
  means at most 16 CPU equivalents across all photo workers and their children.
  Scale worker concurrency to that budget and available memory; do not assume
  16 workers when codecs themselves use multiple threads. Bob subsequently
  authorized this allowance for the current live thumbnail process too: the
  existing image now runs with a persistent 16-CPU whole-container ceiling,
  eight background workers, ten total render slots and four source readers.
  M2 still separates interactive API resources from the photo worker budget.
- Prepare a versioned API suitable for a future native iOS app. Building/shipping
  the iOS application is outside this workbook.
- Do this workbook locally with disposable containers first. This request creates
  the plan; it is not an instruction to restart production or migrate live vaults.
  At rollout, follow the session's then-current deployment authorization.

## Sharing defaults for this autonomous implementation

The request to finish the workbook autonomously authorizes routine product
choices. This pass adopts the proposed defaults: a gallery grab freezes selected
asset revisions at mint time; only an explicit download transfer consumes a retry.
Browsing, metadata and previews do not count. GET, HEAD and Range probes cannot
start a gallery download. A requested transfer consumes its retry even if the
recipient interrupts it; a reconnect is a new explicit transfer. Selected ZIPs
consume one retry for the whole selection. The final admitted transfer closes new admission immediately and burns retained
bytes after all already-admitted transfers settle; revoke/expiry/account deletion deny subsequent requests.
Existing downloaded copies cannot be recalled. Source deletion and album removal
do not change already frozen grabs; the owner can revoke those independently.

## Starting points: read before changing code

- `weazl_ethos.md`, `README.md`, `accelerated_previews_workbook_2026-09-28.md`.
- `photos_web_workbook_2026-09-26.md`,
  `photos_resource_workbook_2026-09-27.md`,
  `responsiveness_workbook_2026-09-28.md`: inspect remaining gates; do not assume
  previous phase names imply every original checkbox is complete.
- `internal/library/photo_index*.go`, `photo_cursor.go`, `photo_albums.go`,
  `photo_album_cache.go`, `photo_prepare*.go`, `photo_failures.go`.
- `internal/library/thumbnail*.go`, `preview_*.go`, `native/preview/`.
- `internal/desk/photo_page.go`, `photo_albums.go`, `photo_preparation.go`;
  `internal/filesvc/registry.go`, `internal/catalog/`, `internal/vault/`.
- `internal/takeout/`, `internal/upload/`, `internal/migration/`,
  `internal/sharedstore/`, `internal/restic/`.
- `internal/capsule/`, `internal/share/`, `internal/filesvc/archives*.go`.
- `mockup-ui/app.js`, `views.js`, `data.js`, `engine.js`, `index.html`, and the
  existing browser smoke scripts.

Current boundaries: the encrypted canonical catalog persists metadata, albums,
logical components, source identities, device checkpoints and an atomic change
journal. Owner-private cached row-position indexes serve bounded timeline/search
and sync queries without reading originals. Cursors use stable IDs and recover
visible neighbors after anchor deletion. The browser mounts justified viewport
rows (at most 300 tiles) and retains at most 2,000 timeline records. SQLCipher
remains a validated prototype; no live database cutover has happened. Restic
source reads still launch child processes. JPEG/HEIF/video processing stays in
the bounded private worker; original files and ZIP transfers stream.

## Working rules

1. Work in dependency order below. Keep existing features usable at each boundary.
2. Keep Go files below 300 lines. Split responsibilities instead of hiding a
   large replacement inside one new file.
3. Never weaken encryption, authorization, password hashing or vault-lock behavior
   to meet a performance target. No plaintext shadow indexes or photo temp files.
4. Originals, ISOs, videos, ZIPs and exports remain bounded streaming operations.
   Media processing has separate explicit limits; upload size is not capped by
   thumbnail limits. Oversized previews must fall back gracefully.
5. Every job is idempotent, cancellable and recoverable. A bad asset fails its own
   job. Failed durable checkpoints pause safely; they do not silently lose work.
6. New metadata is owner-scoped. Cache keys include owner/session and revision
   where appropriate. Lock/rekey/account changes invalidate work and browser URLs.
7. Label performance targets as targets until measured. Record cold/warm behavior,
   fixture size, machine limits and p95, not only best-case microbenchmarks.
8. After each phase record implementation, tests, results, limitations and rollback
   in this file. Check boxes only after their stated gates pass.

## M0 — baseline and database decision

Deliverable: `docs/modal-architecture.md` and reproducible local measurements.

- [x] Capture current API latency, browser rendering/scroll behavior, original
  read count, Restic startup/read time, native rendering time, CPU and child RSS.
- [x] Reuse existing media fixtures. Add a deterministic **metadata-only** fixture
  at roughly the existing 97k catalog entries / 32k photos. No 500-file upload
  campaign and no copies of private production originals.
  Spread capture dates across years while import dates are recent; include
  missing dates and timezone boundaries so an import-date sort cannot pass.
- [x] Prototype SQLCipher with indexes for capture-date/ID, album membership,
  favorites, visibility, source roots and derivative jobs. Exercise mixed reads
  and short write transactions on 2 CPUs / 4 GiB.
- [x] Specify key derivation/wrapping from the vault, connection lifetime, rekey,
  encrypted backup/restore, WAL handling and memory-only temporary SQL stores.
  Scan DB/WAL/temp artifacts for known fixture strings and test lock/reopen.
- [x] Decide how to package SQLCipher: vetted native Go binding or a small local
  metadata service. Account for added native dependencies and build portability.
  `modernc.org/sqlite` is not SQLCipher and cannot gain it via a connection pragma.
- [x] If SQLCipher fails measured needs, compare PostgreSQL using the same workload
  and document queryable-metadata exposure, encryption/key behavior and operations.
  PostgreSQL roles/disk encryption alone do not reproduce per-vault key isolation.
- [x] Choose one initial metadata engine. Avoid maintaining two implementations
  without demonstrated need. Document the decision and query plans.

Gate: encrypted indexed queries work under the small envelope; key isolation,
crash recovery and a realistic performance baseline are recorded. Escalate an
actual unresolved privacy/product tradeoff, not routine implementation choices.

## M1 — asset model and safe metadata migration

Depends on M0. Proposed new module: `internal/photos/` with narrow interfaces to
Library; settle final names in M0 rather than creating competing file services.

- [x] Define stable asset IDs independent of paths; link to existing entry IDs and
  content revisions. Model original components separately for Live Photo pairs,
  RAW/JPEG pairs and edits. Renaming/moving a file must not break album membership.
- [x] Store width/height, media type, duration, orientation, capture time, original
  timezone/offset and provenance, imported time, favorite/archive/trash state,
  captions and derivative states. Retain unknown timezone as unknown. The
  catalog and projection store all fields; raster dimensions/orientation are
  populated by M1, while HEIC/video duration extraction is an M2 codec job.
- [x] Define one canonical capture date used by timeline ordering, date headings,
  scrubber buckets, date filters and chronological album ordering. Keep file
  modification time and import time as separate fields, not capture fallbacks.
  M3 is responsible for rendering headings and scrubber controls from this
  contract.
- [x] Date precedence: explicit user correction first; matched Takeout
  `photoTakenTime` next, embedded JPEG capture metadata next, and client metadata
  last with provenance. File mtime and import time are separate fields and never
  become an apparent capture date. Unknown dates remain explicitly unknown.
- [x] Validate timestamps and preserve unknown timezone without assuming the
  server timezone. Assets without usable capture metadata belong in Date unknown;
  never silently show their import date as the date taken. Recently Added alone
  intentionally orders by import time and labels that meaning clearly.
- [x] Match Takeout sidecars using verified import mappings, not loose filename
  guesses. Test truncated names, collisions, renamed imports and conflicting dates.
- [x] Model selected source roots by stable identity; rename updates the root,
  exclusion removes timeline membership without deleting files. Albums can retain
  references outside the timeline; their viewer remains owner-authorized.
- [x] Create albums with IDs, titles, descriptions, cover asset, sort order and
  explicit membership. Convert existing Takeout folder-derived albums idempotently.
- [x] Group verified identical imported copies for presentation where appropriate;
  preserve all existing file references. Do not collapse intentional duplicates,
  perceptually similar photos or edited versions solely because they look alike.
- [x] Separate rebuildable derived metadata from authoritative user edits/albums.
  Back up both correctly; an album cannot be reconstructed from originals alone.
- [x] Define the reconciliation protocol for Library mutations. The encrypted
  catalog is authoritative for originals and user edits; the Photos projection
  is rebuildable, stable IDs bind it to catalog entries/revisions, and the
  old/new comparator detects roots, assets, dates, memberships, ownership and
  media drift. Durable event delivery belongs to the SQLCipher service phase.
- [x] Build a resumable migration command with dry run, checkpoints, manifests,
  counts and readback verification. Keep originals and old catalog untouched.
- [x] Backfill capture metadata for already imported photos from stored originals
  and available, verified sidecars. No ZIP re-upload, wipe or rehydration required.
  Preserve user corrections; checkpoint per asset/revision, isolate corrupt files,
  and report missing metadata or unresolved sidecar matches without guessing.
  Respect M2's aggregate resource budget. Update affected indexes/date buckets
  incrementally; a date-only correction must not regenerate image derivatives.
- [x] Verify a photo taken in 2013 and imported in 2026 appears under 2013 in
  Timeline and 2026 in Recently Added. Cover conflicts, midnight/timezone offsets,
  unknown timezone, invalid/missing dates, interrupted backfill and repeat runs.
  Rename, restart and reindex must preserve capture dates and manual corrections.
- [x] Prepare old/new reads side by side behind a flag; compare roots, dates,
  counts, memberships, ownership and media fields. Stable entry and album IDs
  preserve existing references; UI/API redirect handling remains part of M3.

Gate: migrate/restart/retry twice without duplicate albums or assets; move,
replacement, exclusion, trash, restore, purge, rekey and user deletion all converge.
Test Restic and shared storage. No production wipe or rehydration is required.
Date correctness and existing-library backfill verification are release gates for
M3; a fast timeline still ordered by import time does not pass. The local M1
gate is met by the stable projection, atomic metadata migration, invalid-date
handling, and comparator tests; SQLCipher persistence and durable change delivery
are the next service boundary.

## M2 — durable media jobs and independent CPU budget

Depends on M1; may overlap read-only M3 UI scaffolding once contracts stabilize.

- [x] Add encrypted, per-vault jobs keyed by owner, asset revision, operation
  and renderer version. The API queue has durable leases, bounded
  attempts/backoff, generic terminal failure categories, completion and in-flight
  source-read progress, and abandoned-lease recovery. Vault lock/rekey/revoke
  cancellation is verified at the worker boundary.
- [x] Coalesce identical jobs and prioritize new photo uploads over historical
  backfill. Visible work is still served directly through the preview path;
  nearby-view prefetch and starvation measurements remain.
- [x] Introduce a separate worker container with no network interface or vault
  volume. The API sends bounded, already-authorized source bytes over a private
  Unix socket; the worker receives no vault key, path or queue credentials.
- [x] Define lock/rekey/revoke cancellation and fail-closed behavior across the
  API/worker boundary. Lock and rekey drain active derivatives before clearing or
  changing the vault session; request cancellation reaches the worker; unavailable
  workers produce retryable failures and never fall back outside the CPU limit.
- [x] Enforce a 50% photo derivative CPU ceiling with a separate worker container
  quota that covers preview and FFmpeg child processes. Configure
  `WEAZLCLOUD_PHOTO_CPUS` to half the effective deployment allocation; CPU and
  affinity discovery also bound renderer concurrency. Metadata ingestion remains
  in the API process and needs a separate aggregate load measurement.
- [x] Size renderer concurrency from the worker cgroup CPU allocation, with the
  API render pool capped at half its effective allocation. Memory admission limits
  concurrent large images; codecs run single-threaded. On a 16-CPU photo cgroup,
  at most 16 single-threaded jobs run; on the recommended 1-CPU photo cgroup, one
  job is allowed.
- [x] Size worker concurrency and native codec threads together. Use one
  CPU-bound thread per job and up to floor(photo CPU budget) concurrent jobs,
  reduced by measured per-job memory and I/O limits. For fractional budgets allow
  one throttled worker. Recalculate on startup/config changes, bound read-ahead,
  and prevent replicas from each claiming the full allowance. On 32 allocated
  CPUs the CPU ceiling is 16; on 2 CPUs it is 1. Report effective limits and workers.
- [x] Make Quiet/Balanced/Fast background schedules explicit operator settings
  with startup validation. The deployment setting survives restart; the existing
  encrypted manual pause also survives restart. Quiet pauses backlog work while
  on-demand previews remain available. Time-of-day quiet hours are not implemented.
- [x] Generate grid derivatives from one admitted source read when stored media
  dimensions are known. Use the safe two-pass header probe for older assets that
  lack dimensions; each path takes its memory reservation before reading and
  releases it before any retry or fallback. Test the known-dimensions Restic
  path for exactly one source restore.
- [x] Measure the existing Restic-backed preview path end to end and separately
  benchmark the renderer. The Restic smoke's first thumbnail took about 200 ms
  for the tiny fixture; `BenchmarkJPEGPreview` measured about 120 ms/op on the
  local i7-1365U Go fallback. No supported batch-read API exists in this backend,
  so it remains on the integrity-checked read path; revisit if a supported
  streaming/batch API is added.
- [x] Implement the first version of the mobile Photos ingestion contract using
  the existing upload manager: authenticated create/status/chunk/finalize/cancel,
  idempotency keys, default Photos/Mobile placement, optional capture time, and
  auto-queued preview work after durable finalization. Device-scoped credentials,
  client sync cursors and native-client integration remain later M6 work.
- [x] Queue new-photo derivatives after durable upload finalization, ahead of
  historical backfill, under the same CPU/memory admission limits. Derivative
  failures remain separate from original upload success. Covered by the automatic
  new-upload queue test and the disposable browser smoke.
- [x] Preserve the bounded accelerated JPEG path and add a pipe-only FFmpeg
  derivative path for formats supported by the installed FFmpeg build, including
  WebP/TIFF and first-frame video posters. It uses single-threaded decode/filter
  paths, bounded input/output, worker memory admission and timeouts; originals
  remain unchanged and source media never goes to a temporary file.
- [x] Ship and runtime-test the Alpine worker's libheif HEIC path. FFmpeg itself
  lacks HEIF demuxing, so libheif decodes from anonymous memory files; no media is
  written to a regular temp file. The generated fixture includes 90-degree
  rotation and Rec.709 color metadata and verifies the resulting preview geometry.
  Missing tools, bad input, and oversized derivatives fail as unavailable preview
  jobs while originals remain intact.
- [x] Keep originals byte-identical. Metadata corrections/rotations are
  non-destructive, and unsupported/oversized files receive a preview fallback.
- [x] Expose current per-file render progress and active-worker average in the
  preparation status/API. Progress is in-memory while leased and remains inside
  the encrypted queue if another queue checkpoint is written; a process crash
  requeues the item and starts progress afresh.
- [x] Defer browser-compatible video playback derivatives to M3 and Live Photo
  pairing to M6 mobile sync; M2 produces bounded video posters.

Gate: [x] kill/restart workers, lock the vault, cancel requests and feed corrupt
media; work resumes or pauses correctly, originals remain readable, and no raw
media or keys persist in queue/temp/log files. [x] Interactive browsing and upload
smokes pass under the 50% photo CPU ceiling at 2 CPU / 4 GiB and 8 CPU / 16 GiB.
The worker-only Compose stack became healthy again after an explicit worker restart;
encrypted lease-recovery, vault-lock drain, corrupt/retry, replacement and key/path
redaction tests pass. Across constrained local smokes, first-thumbnail latency on
the tiny fixture ranged from 176–308 ms.
Render-only benchmark was 120 ms/op on the Go fallback. These timings are local
smoke baselines, not large-library performance claims. The M2 worker/runtime gate
is complete; browser playback derivatives and Live Photo pairs remain assigned to
later product phases.

## M3 — mode shell, timeline and viewer

Depends on M1 query contracts; prepared media from M2 supplies the complete gate.

- [x] Add compact Library/Photos navigation with shared top-level chrome. Photos
  routes support timeline, month/year, albums, favorites and recently added.
  Browser history restores the route; mode memory retains owner-scoped location,
  selection and anchors without writing private state to localStorage.
- [x] Keep the Photos UI in the repository's existing locally bundled ES modules;
  no runtime CDN or new framework toolchain. This replaces the earlier Svelte
  implementation suggestion to match the established application architecture.
- [x] Add owner-authenticated versioned date-count, bounded timeline, asset-detail
  and thumbnail-derivative endpoints. Page defaults/maxima remain 100/200.
  Query pages stay bounded and indexed around-ID lookup handles deleted anchors.
- [x] Use indexed keyset pagination with explicit consistency rules. New uploads
  must not force scrolling users back to the newest photo. Recover stale anchors
  without loading the whole catalog or silently skipping items.
  Cursors are owner-encrypted and keyset anchored by stable ID; indexed around-ID
  lookup and deleted-anchor neighbor tests cover date/album/search filters.
- [x] Build justified rows, day headings, density control and year/month scrubber.
  Jump directly to a date. Layout uses known aspect ratios and placeholders.
  Use M1's canonical capture dates everywhere in Timeline, expose Date unknown,
  and retain the visible asset anchor when a backfill changes its date bucket.
- [x] Bound mounted tiles and cached page metadata. Use keyed updates and viewport
  overscan; cancel distant fetches and release object URLs. Never rebuild the
  whole application on scroll or retain every visited photo in a growing array.
  Mounted cards are viewport-windowed, detached thumbnail/text/capability work is
  canceled, and accumulated rows are capped at 2,000. Grid thumbnails use direct
  image URLs rather than retained object URLs.
- [x] Add Timeline, Albums, Favorites, Recently Added and Trash navigation.
  Keep controls compact and selection actions contextual.
- [x] Apply the hidden-folder contract to normal Photos queries and views,
  including date counts, album covers, viewer navigation, thumbnails, favorites
  and prefetch. Add an owner-only Hidden view; do not expose hidden thumbnails or
  counts in the normal navigation. Hidden-mode state resets on vault lock.
- [x] Build a fullscreen viewer with arrows/swipes, zoom, keyboard shortcuts,
  optional metadata drawer, video playback, next/previous prefetch and return to
  the originating timeline/album position. Viewer routes support browser back,
  copied deep links and reduced motion.
- [x] Offer Show in Library and Add folder to Photos through owner-safe actions.
  Render server strings as text; titles/captions are untrusted input.
- [x] Changes arrive through the existing owner event channel; refresh the bounded
  page window while restoring the visible item and scroll offset.

M3 implementation evidence: indexed row-position queries, nearest visible
survivor recovery, justified rows, capture/import date separation, per-mode and
history selection/scroll memory, and metadata-only layout tests pass locally.
Camera/outside-album/wildcard search is now implemented. The constrained browser
p95, frame timing, media, touch and keyboard matrix remains a release gate; syntax
and layout unit checks are not substitutes for measuring those targets.

Gate targets on local/LAN fixtures, stated separately from WAN latency:

| Measure | Initial target |
| --- | --- |
| Warm metadata page and date-bucket API p95 | <=200 ms on 2 CPU / 4 GiB |
| First useful cached timeline viewport | <=1 second on the test LAN |
| Jump to a distant date with ready derivatives | <=1 second on the test LAN |
| Mounted photo tiles | <=300 with documented overscan |
| Retained timeline page cache | Bounded, initial maximum 2,000 items |
| Warm scroll main-thread work | p95 within a 16.7-ms frame budget on the reference browser |
| Original reads during prepared-grid browsing | Zero |

Record browser/device, network, cold unlock/index time separately, and 5 cold /
20 warm runs where practical. Investigate misses; do not relabel them as passes.

## M4 — editable albums and gallery grabs

Depends on M1/M3. Use the frozen-selection and explicit-transfer defaults above.

- [x] Create/rename/delete albums; edit description, cover and order. Add/remove
  selected photos in bulk. Deleting an album leaves its originals untouched.
- [x] Support range selection, select-day and selection across loaded pages without
  retrieving every asset into the browser. Scope server selections to owner/filter.
- [x] Add Share as grab from an album or selection. Keep the admin-configured
  public hostname, expiry, passphrase option, QR and revocation controls.
- [x] Exclude hidden-folder assets from default album/selection grabs and ZIPs.
  Sharing from Hidden requires an explicit owner action identifying the hidden
  items being shared. Hiding a folder does not silently revoke existing grabs;
  show applicable existing grants and provide their normal revoke action.
  Apply the eventual frozen/live membership decision consistently.
- [x] Implement a narrowly scoped manifest/grant rather than exposing live vault
  keys. Serve authorized preview variants and originals; prevent enumeration or
  path/ID substitution. Owner lock must not inadvertently widen public access.
- [x] Specify the adopted frozen membership/revision lifetime and transactionally
  correct use accounting. Handle concurrent guests, interrupted transfers,
  range/reconnect, metadata requests, bots and explicit burn/revoke. Capsule tests
  verify admission counts, non-blocking streams, earlier-transfer retention,
  explicit revoke cancellation and cleanup.
- [x] Public gallery: mobile-friendly grid, immersive viewer, selected downloads
  and Download album. Browsing uses derivatives, never original-sized thumbnails.
  The constrained Chromium smoke passes on both backends at 390px width; actual
  selected/full ZIP files decode and spend precisely one retry per transfer.
- [x] Default gallery derivatives omit GPS/EXIF. Explicit original download may
  retain embedded metadata; disclose that accurately. Do not promise stripping
  from untouched originals. A sanitized download is a separate derivative.
- [x] Build ZIPs on the backend with bounded streaming and a durable job. Preserve
  existing policy: ordinary on-demand downloads expire after 90 minutes; ZIPs
  retained for grab capsules remain until burn/expiry/revocation and cleanup.
  Owner jobs use encrypted checkpoints/output and bounded range reads; guest
  selection jobs run two workers and recover from frozen sources after restart.
- [x] Define source deletion versus grant retention, distinct from album removal.
  Account deletion must revoke grants and delete the owner's assets, full stop.
- [x] Prevent browser/shared caches from retaining cross-owner sensitive responses;
  revoke in-memory URLs on lock/account change. The guest grid keeps 60 tiles per
  page, three preview fetches and at most 120 in-memory URLs; sessions renew
  while active. A revoked link cannot recall files
  recipients already downloaded, and the UI must not imply otherwise.

Gate: guest access sees only the manifest, needs no account, respects expiry and
revocation, and cannot burn retries by loading thumbnails. Test chosen counting
semantics, original/ZIP streams, concurrent requests and deletion lifecycle.

## M5 — search, organization and useful completeness

Depends on M1/M3. Finish these deterministic features before optional ML.

### Hidden folders — added September 28, 2026

Required outcome: an owner can hide a photo folder recursively from everyday
Photos browsing and unhide it later without moving, deleting or re-uploading its
originals. This is Photos visibility within the existing vault permissions.
The initial assumption is that an unlocked owner can explicitly open Hidden;
there is no separate PIN/password or promise of a second encrypted vault.
Library continues to show the owner's files and folders normally.

- [x] M1 schema follow-up: persist an owner-scoped hidden flag on stable folder
  identity in encrypted metadata. Preserve it through rename/move, restart,
  backup/restore and rekey. Do not infer privacy from a leading dot in a name.
- [x] Define effective visibility as hidden when the folder itself or any ancestor
  is hidden. New uploads, imports and subfolders inherit it immediately. Unhiding
  a parent preserves explicit hidden flags on descendants; moving an unflagged
  child out of a hidden parent makes it visible unless its destination is hidden.
- [x] Provide Hide from Photos / Unhide in folder context menus and an owner-only
  Hidden view. If folder browsing is not yet exposed in Photos, offer the actions
  in Library for included photo folders and in Photos source-folder settings.
  Explain the recursive effect briefly in the action dialog, not permanent copy.
- [x] Enforce filtering on the server, before pagination and aggregation. Hidden
  assets must not appear in normal Timeline, Favorites, Recently Added, search,
  album contents/covers/counts, duplicate suggestions, Trash browsing, date
  buckets, viewer next/previous or prefetch responses. Preserve album membership
  internally so unhiding restores its presentation. Hidden includes an explicit
  route to hidden trashed items under the existing 30-day retention policy.
- [x] Require explicit hidden context and normal owner/vault authorization for
  private Photos detail, thumbnail and original endpoints. Knowing an asset ID
  must not bypass the normal-view filter. This is a presentation boundary within
  the owner's vault; existing authenticated Library access remains available.
- [x] Hide/unhide emits visibility changes and invalidates affected counts,
  cursors, covers, selections and cached pages. Remove newly hidden tiles and
  viewer content promptly; cancel their pending fetches and release object URLs.
  Lock/logout/account change clears Hidden mode and its in-memory content.
- [x] Hidden status must not stop backup, dedupe or private preview processing.
  Metadata/previews keep the same per-vault encryption and resource limits.
  Mobile upload placement inherits destination visibility; later sync reports
  visibility changes so the native client cannot surface hidden photos by default.
- [x] Apply M4's explicit-sharing rules to grabs, album downloads and ZIP jobs.
  Revalidate visibility when resolving a saved selection. Clearly distinguish
  hiding from revoking a link or deleting an original; existing downloaded copies
  cannot be recalled.
- [x] Test a nested folder with explicit and inherited hidden states, upload into
  a hidden folder, rename, moves across visible/hidden parents, album membership,
  Trash/restore, rekey, restart and cross-owner requests. Verify that all normal
  APIs and aggregates exclude hidden items, an explicit Hidden request works only
  for its owner, existing-grab behavior matches the documented policy, and unhide
  restores browsing with byte-identical originals.
  Library lifecycle/query tests and socket-free HTTP handler smokes cover these
  cases, including cross-owner original/membership/selection denial and frozen
  grab retention after hiding. Browser confirmation remains an M7 release gate.

Gate: hiding a folder removes all effective descendants from normal Photos views
and results without requiring a reload. Only an explicit Hidden view shows them;
unhide restores them without changing capture dates, memberships or file bytes.

### Remaining organization work

- [x] Search filenames/wildcards, captions, album, capture-date range, media type,
  favorites, camera and files outside any album. Bound and debounce results;
  cancel stale requests without losing keyboard focus.
- [x] Favorite/archive/unarchive; archive removes an item from Timeline without
  deleting it. Show precise album removal vs photo deletion labels.
- [x] Correct capture date/timezone, caption and rotation as metadata/non-destructive
  edits. Rebuild affected buckets/derivatives and preserve stable asset identity.
- [x] Group exact duplicates for review; preserve memberships when selecting a
  preferred presentation. No automatic deletion or perceptual-duplicate merging.
- [x] Use existing 30-day Trash behavior consistently in both modes. Source file
  restore restores photo identity and intended album relationships.
- [x] Publish supported formats and fallback behavior in the recovery runbook.
- [ ] Test portrait orientation,
  wide gamut, transparency, progressive JPEG, HEIC and video on Safari/Chromium.

Gate: search and organization stay responsive during uploads and preparation,
with permissions and state transitions identical across Library and Photos.

Deferred: faces, OCR, semantic search, maps, memories, shared editing and partner
libraries. If later built, models/processors run locally, are optional and have
explicit resource and encrypted-index policies. No third-party identity service.

## M6 — API and synchronization foundation for iOS

Contracts begin in M1; implement photo upload ingestion in M2 and validate the
full synchronization surface after M3–M5.

### Required mobile Photos ingestion endpoint

Proposed versioned routes (finalize their OpenAPI schemas in M1):

| Route | Purpose |
| --- | --- |
| `POST /api/v1/photos/uploads` | Create or recover an idempotent media upload session |
| `GET /api/v1/photos/uploads/{id}` | Return accepted offsets, component state and final result |
| `PATCH /api/v1/photos/uploads/{id}/components/{componentId}` | Stream a bounded chunk with offset and checksum validation |
| `POST /api/v1/photos/uploads/{id}/finalize` | Verify originals, commit the asset and schedule processing |
| `DELETE /api/v1/photos/uploads/{id}` | Cancel unfinished ingestion and release its reservations |

Reuse `internal/upload/` and its durable staging, quota reservations and recovery;
do not create a second independent byte-storage/upload engine. Keep existing
`/api/uploads` clients compatible. A single still image has one component; a Live
Photo can have a still and a motion component within the same logical session.

- [x] Creation includes a device asset identifier, source revision/idempotency key,
  component filenames/types/sizes/checksums, capture timestamp and known offset,
  selected photo-root ID, and optional authorized album IDs. Treat metadata as
  untrusted; verify media type and preserve timestamp provenance.
- [x] Resolve placement server-side within an owner-selected root. Never accept an
  arbitrary filesystem destination or another owner's root/album identifier.
  Identical filenames must not overwrite existing media without an explicit policy.
- [x] Return upload/component IDs, accepted offsets, chunk limits and eventual
  stable asset ID. Repeating a matching request returns the same session/result;
  reusing its idempotency key with different content yields a conflict.
- [x] Finalize only after expected components are durably stored and verified.
  Use recoverable commit/outbox steps for album membership and processing jobs.
  Distinguish `uploading`, `stored`, `processing`, `ready` and `failed`; derivative
  failure must not turn a successfully backed-up original into a missing upload.
- [x] Integrate device revocation and vault-lock behavior. Document whether locked
  vaults reject ingestion or permit an explicitly authorized encrypted staging
  path; never claim completion while durable commit remains pending.
- [x] Test with a headless mobile simulator: interrupted chunks, wrong offsets,
  corrupted payloads, duplicate finalize, lost final response, two devices with
  the same filename, partial Live Photo pair, quota exhaustion and revoked auth.
  A completed upload must become visible in web Photos and its album without an
  open browser initiating processing. The real-handler/Restic simulator passes
  these cases, source revisions, wrong media, byte-engine/coordinator restart,
  album commits, checkpoint acknowledgment and restored-catalog rejection.

### Remaining client and synchronization work

- [x] Version and document the API with OpenAPI; expose asset/component IDs,
  revisions, album operations, derivative readiness, errors and capabilities.
- [x] Add per-device revocable sessions with local account authentication and an
  explicit vault-unlock/key-storage lifecycle. Never embed the master password
  in a mobile client or require a third-party account.
- [x] Preserve resumable uploads; add device-scoped idempotency and checksum
  verification. Respond safely when the client retries a committed upload.
- [x] Distinguish device asset identity from content hash and path. Preserve Live
  Photo pair identity and album membership across retries and multiple devices.
- [x] Add ordered change records, deletion tombstones, durable client checkpoints,
  initial bounded sync and resync after database restore/expired checkpoints.
  Apply authorization to every delta; do not leak another owner's existence.
- [x] Define backup as one-way ingestion initially. Deletion from a phone must
  not silently delete its server backup. Future Free up space requires verified
  durable upload plus a separate explicit device-deletion action.
- [x] Prototype a small simulated client: interrupt upload, lose the completion
  response, reconnect, sync changes, revoke device, restore server backup and
  recover. The socket-free simulator verifies those operations against actual
  handlers and both Restic/shared backends. The M7 filesystem restore smoke also
  preserves receipts, metadata and grants; process/container restore remains open.
  Do not build the iOS UI in this phase.
- [x] Document iOS background scheduling limits. Design for OS-managed background
  transfers and local metadata caches; do not promise continuous instant backup.

Gate: a client can reconnect and reconcile without full-library downloads,
duplicate originals, lost album membership or resurrected deletions.

## M7 — release and migration handoff

Depends on all implemented feature gates and resolved sharing decisions.

- [x] Run focused tests per phase, then `make check`, native renderer checks,
  Restic/shared container smokes and constrained Photos browser tests. Extend
  meaningful tests for the new functionality; don't merely repeat old smokes.
  Final full normal/race checks and `make smoke-photos` pass on both backends.
  Regression coverage caught and fixed dated-viewer formatting, album form
  submission and selected guest ZIP routing.
- [ ] Run the metadata-scale benchmark, mixed browsing/upload/backfill tests and
  memory/CPU ceilings. Record cold start, warm p95, worker RSS and source reads.
- [ ] Test migration interruption, DB/WAL recovery, rekey, lock/revoke, deleted
  accounts, stale public grants, interrupted ZIPs and storage-full conditions.
- [x] Exercise encrypted metadata backup/restore including user-created albums,
  captions, device checkpoints and grants. Keep derived-cache recovery distinct.
  The native simulator copies the complete disposable node data directory after
  private workers drain (and after shared SQLite closes), opens fresh accounts,
  vaults and libraries at a different path, and checks both Restic/shared storage.
  The derived Photos index is removed before restore. Still/motion originals,
  captions, custom albums, hidden flags, device credentials/checkpoints, completed
  upload receipts, encrypted owner ZIPs, hostname and frozen grant admission
  counters survive. This does not replace process/container recovery gates.
- [x] Update README, operator settings, requirements, API docs, migration/runbook,
  feature status and rollback instructions. Record container volumes explicitly.
  See `docs/photo-api.md`, `docs/photo-api.yaml`, `docs/photos-release-runbook.md`
  and the September 30 local verification record.
- [x] Produce a dry-run inventory and reconciliation report. No automatic removal
  of imported duplicate files, old repositories or rollback material. Local
  disposable fixture counts and comparison outcomes are recorded in the runbook;
  production inventory is deferred by the current no-production scope.
- [ ] Before any authorized rollout: check active transfers/jobs, checkpoint/pause
  affected workers, preserve image/config and take a consistent data/metadata
  rollback point. Keep production CPU preferences and selected roots intact.
- [ ] Deploy behind feature flags; compare old/new read results before enabling the
  new UI. Define how to replay/preserve new album edits if the UI is rolled back;
  reverting the image must not discard post-migration metadata writes.
- [ ] Smoke login, Library, date jump, album editing, real preview/video, original
  readback, public grab and background resumption. Recheck owner isolation.

Completion requires measured results and a working recovery path, not just a new
UI. Record remaining optional features rather than claiming full Immich parity.

## Phase handoff record — fill in as work completes

| Phase | Status | Changed modules / evidence | Remaining limits / rollback |
| --- | --- | --- | --- |
| M0 | Complete | Local SQLCipher, Restic and browser baselines passed | |
| M1 | Complete locally | Stable projection, roots/assets/albums, atomic capture/media migration, sidecars, validation, duplicate groups and old/new comparator; focused tests pass | SQLCipher persistence, durable change delivery, HEIC/video extraction and runtime dual-read flag move to M2/M3 |
| M2 | Complete locally | Encrypted derivative jobs and lease recovery, isolated no-network worker, libheif/FFmpeg codecs, bounded accelerated JPEG pipeline, visible progress, 50% CPU/resource admission, lock/rekey/revoke cancellation and mixed-load constrained local smokes. Tiny-fixture first-thumbnail latency 176–308 ms. Phase gate details above record completed HEIC and worker/runtime checks. | Native video playback/Live Photo pairing remain assigned to later phases. Production untouched. |
| M3 | Implemented; constrained Chromium smoke passes | Timeline/date jump, dated and unknown-date viewer, keyboard/details/history, wildcard caption search, favorite and Hidden transitions pass on both backends. 2-CPU/4-GiB tiny-fixture HTTP pair p95 5.8–6.7 ms; warm viewport p95 119–161 ms; date jump 88–90 ms. Full normal/race and layout tests pass. | Large-library cold unlock, full-frame timing and Apple-device matrix remain release gates. |
| M4 | Implemented; owner and guest browser smokes pass | Album create/edit and escaped metadata, day selection, frozen gallery mint/QR, mobile account-free grid/viewer, real selected/full ZIP readback and exact retries pass on Restic/shared. Encrypted jobs, restart recovery, streams, revocation and owner isolation pass normal/race tests. | Safari and prolonged mobile reconnect verification remain; no production rollout. |
| M5 | Implemented; lifecycle and Chromium organization checks pass | Search/favorite/capture edits/rotation, duplicates, hidden root/nested/move/rekey/reload and pair copy/delete/restore pass. Owner Hidden browser transitions and cross-owner denial pass; both filesystem restores and container restart smokes pass. | Actual Safari media/color/touch matrix and complete Chromium format matrix remain open; fallback behavior is documented. |
| M6 | API foundation and native simulator implemented locally | Revocable device credentials, device-scoped uploads, versioned capability route, encrypted hash-chained changes/tombstones/checkpoints, bounded initial and incremental sync, restore/expired-checkpoint detection. Focused account/catalog/library tests pass. | Encrypted logical receipts, still/motion pairing, source revisions, owner Photos roots, atomic album/outbox commit, bounded album header/membership sync and OpenAPI are implemented. The native simulator exercises restart, offset/hash/capacity/media errors, lost replies, revisions, two devices, lock/revoke and restore detection. The iOS app remains out of scope. |
| M7 | Local release checks and documentation complete; manual measurement gates open | Full make check, native sanitizer checks, Restic/shared container restart/migration, basic and authenticated Chromium, filesystem recovery and shared-store integration pass. API/runbook/workbook and measured tiny-fixture results updated. | Large-library/frame/RSS and Apple-device gates remain explicit. Production inventory/deployment excluded. Publication verified separately against origin. |

## Research references

Reviewed September 28, 2026. Immich source pinned to
`aa023378477fb1a65d24f4614de0940f6a769794`; use it to understand design, not as
permission to copy implementation blindly.

- [Immich architecture](https://docs.immich.app/developer/architecture/)
- [Timeline buckets and queries](https://github.com/immich-app/immich/blob/aa023378477fb1a65d24f4614de0940f6a769794/server/src/repositories/asset.repository.ts)
- [Viewport loading](https://github.com/immich-app/immich/blob/aa023378477fb1a65d24f4614de0940f6a769794/web/src/lib/managers/timeline-manager/internal/load-support.svelte.ts)
- [Media processing](https://github.com/immich-app/immich/blob/aa023378477fb1a65d24f4614de0940f6a769794/server/src/repositories/media.repository.ts)
- [Incremental synchronization](https://github.com/immich-app/immich/blob/aa023378477fb1a65d24f4614de0940f6a769794/server/src/services/sync.service.ts)
- [Thumbnail and job settings](https://docs.immich.app/administration/system-settings/)
- [Sharing](https://docs.immich.app/features/sharing/)
- [Search](https://docs.immich.app/features/searching/)
- [SQLCipher design and temp/WAL handling](https://www.zetetic.net/sqlcipher/design/)
- [SQLite deployment tradeoffs](https://www.sqlite.org/whentouse.html)
- [Apple background sessions](https://developer.apple.com/documentation/foundation/urlsessionconfiguration/background(withidentifier:))

## September 30 release evidence and remaining gate list

Feature checkboxes above record implemented behavior with local evidence.
Whole-phase release completion still requires the explicitly listed integration
and performance gates. Historical M0/M2 unrestricted results are retained as
historical measurements. The permission retry now allows real local integration
checks; see [local verification](docs/photos-local-verification-2026-09-30.md).

- Available focused race/API, native sanitizer, metadata layout and mode memory
  checks pass. The OpenAPI YAML parses and its local references resolve.
  Final recovery fixes include canonical reload after forgetting a vault session,
  hidden-context album headers capped at 200 memberships with complete paged
  reconciliation, missing selected-ZIP output recovery without a spent grab, and
  placeholder gallery derivatives that preserve downloadable original bytes.
  Cross-owner Hidden original/membership/selection requests are denied in the
  real socket-free HTTP handler smoke.
- After approved local permission escalation, `make check` passes every normal
  and race suite, including TCP/Unix listeners. `make smoke-container` builds the
  image and passes Restic/shared transfer/preview/grab restart and migration.
  `make smoke-browser`, `make smoke-recovery` and `make smoke-sharedstore` pass.
  The dated viewer and album form bugs found by the extended browser regression
  are fixed. Selected public ZIP jobs now reach the share router; its actual HTTP
  regression verifies prepare/poll/download, original bytes and admission counts.
- Remaining release measurements include large-library cold unlock, aggregate
  API/worker RSS, actual Safari playback/color/transparency/touch and complete
  frame timing. Tiny fixtures and script-duration metrics must not be relabeled
  as large-library or whole-frame results. SQLCipher DB/WAL recovery only applies
  to a future runtime query-store cutover; it is not claimed here.
- Production rollout, live inventory, preview regeneration and migration are
  excluded by Bob's latest instruction. Preserve settings/vaults and active jobs
  when a later rollout is authorized; follow the recovery runbook.

The prior staging/DNS failures are superseded by the approved permission retry.
GitHub is reachable; publication is checked against origin after final smokes.
Unrelated screenshots and Python caches are excluded. Production stays untouched.
