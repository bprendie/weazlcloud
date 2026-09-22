# WeazlCloud improvement plan — September 21, 2026

Owner: Bob. Implementation handoff: Luna.
Status: Phases 2–3 complete; Phase 4 in progress; Phases 5–6 planned.

## What we are doing

Make the existing cloud safer, faster, and easier to use. Keep the current features and cyberpunk appearance. Fix the file-handling foundation before adding more features.

This plan follows `mu_workplan.md`; it does not replace its product requirements.

September 21 storage clarification: every approved user can use as much remaining usable disk as is available. There are no per-user quotas or equal shares. The hidden 3% system reserve remains. This supersedes earlier allocation requirements; `mu_workplan.md` has been updated to match.

Product direction: a calm, sovereign file browser for your own disk. Prioritize direct file actions, predictable navigation, local processing, and recovery. No advertising, upsells, engagement feeds, forced external accounts, or unsolicited AI features. Keep cyberpunk colors useful for recognizing file types; avoid adding visual noise.

## Rules for Luna

- Work through the phases in order. Finish one numbered task before starting the next dependent task.
- Make small commits. Put the task ID in each commit message, for example `P1.2: Share library instances across desk and WebDAV`.
- Start each task by reading the listed files and checking the current code. File locations may change during this work.
- Write a regression test for a bug before fixing it when practical. Tests must check behavior, not just repeat the implementation.
- After each task, record what changed, the commands run, their results, and any unfinished work in the work log below. Check a box only when its pass condition is met.
- Use temporary users, files, and data directories for testing. Never use real users' files as test fixtures or consume real grab links.
- Keep identity local. No external login, rendering, QR, or other hosted service.
- Keep each user's files private. Do not add an admin “open user vault” feature.
- Keep the 3% storage reserve invisible in the normal UI. Show the usable 97% as 100%. Do not introduce a per-file upload size cap.
- Keep WebDAV internal for now. Do not change Traefik, DNS, or public exposure as part of this plan.
- Until P6.1 changes the build: edit `mockup-ui/`, run `make desk-assets`, and commit both UI copies.
- Do not commit screenshots, credentials, uploaded files, runtime data, or generated executables.
- Preserve the server's local Compose configuration and `/exports/dockervolume/weazlcloud` bind mount. Implementation commits and production deployment are separate steps.
- If a task needs a product decision listed below, prepare the options and finish independent work first. Do not silently choose a new privacy or data-retention policy.

## Starting facts

The September 21 review found that `go test ./...` passed, `go vet ./...` reported six places where a handler containing a mutex was copied, and race tests, browser flows, and a full restore were not verified. Phase 1 has now removed those handler copies and passed full Go tests, vet, and targeted race tests. Browser flows and full restore remain open work.

The app has three HTTP listeners: desk on 7272, grab on 7273, WebDAV on 7274. Each user has a vault, encrypted catalog, and restic repository. A grab contains its own encrypted copy. The frontend is plain JavaScript embedded in the Go binary.

## Phase 1 — Stop writes from conflicting or filling the disk

Goal: every way of writing files follows the same rules.

Read: `internal/app/run.go`, `internal/desk/desk.go`, `internal/desk/multi.go`, `internal/drive/drive.go`, `internal/library/library.go`, `internal/catalog/catalog.go`, `internal/quota/quota.go`.

- [x] **P1.1 — Capture the failures.** Added isolated integration tests for concurrent desk/WebDAV writes by one user, simultaneous writes by two users, and a quota test seam that simulates a nearly full filesystem.
- [x] **P1.2 — Share one file service per user.** Added a shared per-user service registry used by desk and WebDAV, removed handler copies containing mutexes, and gave each user an independent WebDAV lock system. Concurrent writes retain both catalog entries; `go vet ./...` passes.
- [x] **P1.3 — Put quota checks in the shared write path.** Browser uploads, WebDAV writes, unknown-length request bodies, and multiuser grab creation reserve incoming working bytes. Reservations release on success and failure; same-sized overwrites reserve the full incoming spool.
- [x] **P1.4 — Make file operations unambiguous.** Catalog writes now publish only after encrypted save succeeds. File/folder collisions, duplicate folders, destination collisions, missing paths, and descendant moves return errors without mutating the visible catalog.
- [x] **P1.5 — Remove artificial user storage limits.** Removed equal-share enforcement. Approved users share the remaining usable disk; user usage remains informational, the 3% reserve stays hidden, and filesystem headroom remains the hard guard.

Phase exit: complete. `go test ./...`, `go vet ./...`, and targeted `go test -race` pass. The localhost smoke service also passed bootstrap, unlock, concurrent desk/WebDAV writes, and list verification on temporary storage.

## Phase 2 — Make public links and local accounts trustworthy

Goal: outside input cannot turn into code, and account access has a clear lifetime.

Read: `internal/share/page.go`, `internal/share/share.go`, `internal/capsule/grab.go`, `internal/users/store.go`, `internal/desk/csrf.go`, `internal/desk/places.go`, `internal/vault/open.go`, `internal/vault/write.go`.

- [x] **P2.1 — Fix grab input handling.** Accept generated 32-character hexadecimal tokens, render folder names with DOM text nodes, keep CSP protection, and sanitize download filenames. Tests cover malformed tokens and the former markup path.
- [x] **P2.2 — Expire sessions on the server.** Sessions now carry server-side expiry, expired entries are rejected and removed, secure cookies can be enabled for HTTPS deployments with `WEAZLCLOUD_SECURE_COOKIES=true`, and password changes invalidate old sessions.
- [x] **P2.3 — Bound authentication attempts.** Added bounded expiring limits for login, vault unlock, access requests, WebDAV authentication, and grab attempts. The limiter uses the direct peer address and does not trust forwarded headers. Password verification now runs outside the global user-store lock.
- [x] **P2.4 — Fix node settings persistence.** Hostnames are validated as DNS hosts, node settings are synchronized, memory changes happen after durable write, per-user drive Places are loaded on reads, and multiuser minting enforces the administrator grab base.
- [x] **P2.5 — Use stored node-key unlock.** Bob chose convenience for this personal node. The authenticated WebDAV listener may use the user's stored `node.key` after restart or after the in-memory vault is locked, so a mounted drive can reconnect. Desk login/session state remains separate from vault unlock; logout and session expiry do not expose the library or create a Desk session. Rekey rewrites the stored node key and invalidates the old one. A host administrator who controls the node can still decrypt stored vaults; this is recorded as a trust boundary, not hidden behind UI admin isolation.
- [x] **P2.6 — Make grab counts durable.** Grab metadata is written before a use is accepted, terminal key/payload cleanup errors are surfaced, duplicate clicks are suppressed, remaining retries remain available in the page, and filenames survive repeated downloads. Wrong passphrases do not consume a grab.

Phase exit: complete. Go tests, vet, and targeted race tests pass. Browser automation still needs to verify the hostile-filename page flow. WebDAV's existing `UnlockNode` integration test covers stored unlock; a later end-to-end test should cover lock/reconnect/rekey together. Keep all identity and processing on the node.

## Phase 3 — Make transfers dependable and faster

Goal: keep browsing during uploads and handle large files without loading them all into RAM.

Read: `mockup-ui/app.js`, `mockup-ui/engine.js`, `mockup-ui/views.js`, `internal/library/library.go`, `internal/restic/ops.go`, `internal/drive/drive.go`, `internal/desk/capsules.go`, `internal/capsule/store.go`.

- [x] **P3.1 — Use one upload queue.** Uploads now have stable IDs, destinations, states, byte counts, and attempts. New selections append to one queue, with a maximum of three active workers shared across all batches.
- [x] **P3.2 — Complete the tray controls.** The background tray remains below The Weazl Promise, shows three fixed rails, separates transferring/saving/completed/failed states, supports failure details, retry-failed, cancel, collapse, dismiss, and narrow-screen layout, and refreshes the library after successful files.
- [x] **P3.3 — Stream file reads.** Normal downloads and WebDAV reads use direct restic streaming, metadata supplies the size, and authenticated single-range responses support media/resumable clients. Preview requests retain the bounded-by-preview path until Phase 4 render work is split out.
- [x] **P3.4 — Stream grab creation and delivery.** New file and folder grabs use a versioned authenticated chunk stream. Folder ZIPs are written into the encrypted stream, large files never become one in-memory payload, old one-shot capsules remain readable, and tests cover wrong phrases and interrupted delivery.
- [x] **P3.5 — Measure before batching.** Durable per-user staging is protected by a manifest and recovers after interruption. The measurement harness compares many small files with one equal-sized large file, reports elapsed time, restic commit count, repository growth, Go allocation data, and process high-water memory, and the existing upload queue now groups nearby unique stages into batch restic commits. Pass conditions are covered by recovery and batch restore tests. Report measurements rather than promising a speed multiplier.

Phase exit: complete for the transfer foundation. The browser must remain open until local file bytes have reached the server; a background tray alone cannot upload after the tab closes. Browser-level queue testing and host-volume measurements remain operational follow-ups.

## Phase 4 — Make previews quick and accurate

Goal: make Library feel like a Drive-style file browser. Show useful content previews across common and unusual file types, play audio/video directly inside grid cards, and make folders with 100+ photos usable immediately while thumbnails arrive. This is our desired behavior, not a promise that every format can be rendered.

Read: `internal/desk/preview.go`, `internal/desk/library.go`, `mockup-ui/views.js`, `mockup-ui/app.js`, `internal/desk/preview_test.go`.

**Blocking gate — P4.0 account/vault handoff:** the authenticated login state must transition cleanly into vault-only unlock. A hidden or disabled username field must never be required while unlocking the already-authenticated user's vault. This gate is mandatory before Phase 4 can exit. Verify the sequence with account login, an incorrect vault passphrase, a retry using the correct passphrase, a refresh between steps, and a locked-vault session; the user must never be told that the username is missing after account authentication succeeds.

- [x] **P4.1 — Add a private thumbnail endpoint.** Generate small raster thumbnails instead of returning full-size photos to grid cards. Key cached previews by owner, file version, renderer version, and requested size. Enforce authorization and vault state on cache hits too. Define encryption and eviction for cached data. Pass: replacing a file changes its preview; another user and a locked session cannot read a cached thumbnail. Implemented for JPEG, PNG, and GIF with encrypted per-user cache files, a 256 MiB/4096-file eviction bound, versioned keys, and an authenticated `/api/library/thumbnail` endpoint. The localhost smoke path uploaded a PNG after bootstrap/unlock and received a `200 image/png` thumbnail.
- [ ] **P4.2 — Bound preview work.** Limit simultaneous render jobs and total cache size; prioritize visible cards and cancel obsolete requests. Limit parser memory, archive expansion, and execution time without restricting upload size. Pass: a large folder or malformed preview file does not monopolize uploads or exhaust the node.
- [x] **P4.3 — Improve document previews.** Resolve XLSX shared strings into their cells, preserve row/column structure, and put slides in numeric order. Clearly label text-only previews. If exact page layouts require a local converter, prepare its image-size and maintenance tradeoffs for Bob. Pass: reference documents show meaningful content in the correct order. Implemented bounded XLSX shared-string, inline-string, boolean, row, and cell rendering plus numeric PPTX slide ordering; DOCX/OpenDocument text previews retain their explicit text-only label.
- [x] **P4.4 — Improve model previews.** Honor 3MF object indexes, components, and transforms. Use a useful 3D viewing angle; handle invalid numeric coordinates and large STL meshes explicitly. Pass: multi-object 3MF fixtures and ASCII/binary STL fixtures produce recognizable previews without silently showing an arbitrary fragment. Implemented object/build/component traversal with affine transforms, recursion/cycle bounds, 20,000-triangle caps, 64 MiB model-input bounds, and finite-coordinate rejection for 3MF and STL.

- [ ] **P4.5 — Recognize content beyond the MIME label.** Replace the grid's extension allowlist with a shared preview-capability response from the server. Use bounded content inspection, declared MIME type, and filename together; handle missing, incorrect, and `application/octet-stream` labels. Cover images, PDF, Markdown/plain text/code, SVG, STL/3MF, Microsoft Office/OpenDocument, and audio/video. Keep a fixture table recording what gets a thumbnail, readable preview, player, or fallback. Render untrusted HTML/SVG and documents through safe renderers or isolation; never execute their scripts in the app. Pass: recognizable files with generic or incorrect MIME labels still get previews; genuinely unsupported files show a useful type/metadata card and download action instead of a broken image or endless spinner.
- [ ] **P4.6 — Play media inside the grid.** Give video cards a poster and play button; give audio cards embedded artwork when available, otherwise a clear audio card. Clicking Play starts an inline player with play/pause, seek, volume, and elapsed/duration controls; offer an expanded viewer as well. Keep player controls separate from selection, drag, and context-menu actions. Only one card plays at a time, do not autoplay, and stop playback when leaving the folder or locking the vault. Do not download every media file just to draw the grid. Depend on P3.3 for authenticated streaming and byte-range support. Pass: audio/video play and seek without leaving Library, ordinary refreshes do not restart playback, and keyboard controls work. For unsupported codecs, show a clear fallback; evaluate bounded local transcoding as a separate task, preserving originals and accounting for derived-file disk use.
- [ ] **P4.7 — Show the folder before generating all previews.** Render filenames, metadata, and fixed-size placeholder cards as soon as the listing arrives. Request thumbnails only for visible cards plus a small look-ahead; prioritize newly visible cards when scrolling. Use one bounded client request queue and the bounded server workers from P4.2. Share duplicate in-flight render jobs for the same authorized cache key, and stop obsolete work when nobody needs it. Patch individual cards as results arrive instead of rebuilding the whole grid. Pass: opening a 100+ image folder never waits for all thumbnails; scrolling, selection, menus, and uploads stay responsive; pending requests do not grow with the entire folder. Measure whether windowing the grid is needed, preserving keyboard navigation and scroll position if added.
- [ ] **P4.8 — Prepare once, reuse on later visits.** After a successful upload, enqueue low-priority thumbnail generation while the user's vault is available. Give visible requests priority over background jobs and pause warming under storage or upload pressure. Never delay upload success for a preview. Use the private cache from P4.1 across visits/restarts, with bounded eviction and invalidation on replacement/deletion. Retry transient failures with backoff; show stable fallbacks for unsupported/corrupt files instead of repeatedly rendering them. Pass: revisiting an unchanged cached folder does not decode originals again; locking a vault prevents serving cached previews or continuing work that requires its keys.
- [ ] **P4.9 — Prove the large-folder improvement.** Build isolated folders of 100 and 500 mixed-size photos, plus a mixed-format folder with media, documents, and malformed files. Record cold-cache and warm-cache results on a named browser/node/network: listing latency, first visible thumbnail, all visible thumbnails ready, bytes transferred, maximum queued/active jobs, server memory, and UI responsiveness. Proposed local-LAN targets: usable grid within one second of listing arrival, visible thumbnails within two seconds warm and five seconds cold. Treat these as targets to validate, not measured claims; record misses and causes. Pass: a repeat visit uses cached thumbnails without original downloads, and opening a folder does not schedule every offscreen preview or stall a concurrent upload.

Luna's order within this phase: establish the P4.9 baseline first; implement P4.1–P4.2, then P4.7–P4.8 for the immediate photo-folder speed improvement. Follow with P4.5, P4.3–P4.4, and P4.6. Repeat P4.9 after each relevant change. Deliver cache, scheduling, format support, and playback as separate reviewable changes.

Phase exit: list/grid views share preview capabilities; generic MIME labels do not unnecessarily prevent previews; supported audio/video play inline. Compare cold/warm large-folder measurements against the baseline, retaining privacy checks and responsive browsing during uploads.

## Phase 5 — Finish normal file management

Goal: file actions feel predictable and the displayed storage numbers mean something.

Read: `mockup-ui/app.js`, `mockup-ui/views.js`, `mockup-ui/data.js`, `internal/catalog/catalog.go`, `internal/library/library.go`, `internal/quota/quota.go`.

- [ ] **P5.1 — Add shared change notifications.** Emit per-user changes after successful storage commits. Deliver updates to the browser, including WebDAV changes, with reconnect/resync behavior. Pass: two open views converge without a reload and without losing the current folder, selection, or scroll position.
- [ ] **P5.2 — Add multi-select and batch actions.** Implement Ctrl/Shift selection and bulk move, download, and delete with clear partial-failure reporting. Keep keyboard and context-menu actions consistent. Pass: a failed item does not hide successful items or clear an unrelated selection.
- [ ] **P5.2a — Prepare bulk downloads as server-side ZIP jobs.** After P3.3 and P5.2, make Download on multiple selected files or any folder create one ZIP on the backend. A single selected file still downloads directly. Include nested folders and empty directories, preserve relative paths and modified times, and use ZIP64 for large archives. Resolve overlapping selections without duplicate entries; use safe relative archive paths and deterministic names for collisions. Capture an authorized manifest of file versions when the job starts so later moves/replacements cannot silently change its contents. Pass: a mixed selection of files and nested folders extracts to the expected structure with matching file hashes, including Unicode names and a large-file fixture.
- [ ] **P5.2b — Keep ZIP preparation in the background.** Show queued/preparing/ready/failed/cancelled states in the transfer tray, with processed files and bytes where known. Keep Library usable and offer Download ZIP when ready; attempt automatic download only where the browser permits it. Stream source files into the archive with bounded memory, bounded workers, and cancellation. Stage jobs on the configured data volume, never the container's small `/tmp`; reserve and enforce temporary disk use through P1.3 without adding per-user quotas or fixed file-size caps. Pass: a large archive does not grow RAM with its size or block browsing/uploads, cancellation releases reservations, and insufficient space produces an actionable error. Never silently omit unreadable files or label an incomplete archive successful.
- [ ] **P5.2c — Protect and clean up generated ZIPs.** Jobs, status, and download endpoints belong to the authenticated owner; check vault state on access. Protect temporary plaintext and settle lock/logout behavior using P2.5 before implementation. Serve completed archives with resumable byte-range downloads; keep them for a documented short expiry so an interrupted download need not rebuild the archive. Clean expired/cancelled/failed jobs and reconcile interrupted jobs on restart. Keep bulk downloads separate from public grab links and their use counts. Pass: another user cannot inspect or download a job, a locked vault cannot serve its archive, resumed downloads match the original ZIP, and expiry/restart leave no unaccounted temporary files. Validate the expiry policy with Bob before shipping.
- [ ] **P5.3 — Decide deletion and retention with Bob.** Current deletion hides catalog entries; it does not reclaim old restic snapshots. Specify trash duration, restore, permanent deletion, and expired-grab cleanup. Then add a recoverable cleanup job that never prunes a snapshot still referenced by a live catalog entry. Pass: restores work during retention and permanent cleanup demonstrably reclaims unreferenced storage.
- [ ] **P5.4 — Make storage/dedupe reporting honest.** Distinguish logical bytes, actual occupied disk space, pending reservations, and reclaimable bytes. The current dedupe number measures duplicate whole-file hashes within one user's catalog; it is not global or actual restic physical savings. Show a quiet “Storage” meter with shared space available and optional own usage; no personal allowance or equal-share calculation. Do not reveal other users' individual usage to ordinary users. Pass: duplicate uploads, overwrite, deletion, cleanup, and adding a user produce documented results; adding a user does not reduce anyone's limit. The 3% reserve stays invisible.
- [ ] **P5.5 — Make navigation remember where you were.** Support browser Back/Forward and reloadable folder locations. Preserve folder scroll, sort, view mode, and selection when returning from a preview or search. Keep clickable breadcrumbs and an Up action; let users pin favorite folders. Pass: open a deep folder, preview a file, search, and return without losing the original location. Validate folder access on every navigation.
- [ ] **P5.6 — Make everyday actions direct.** Add discoverable keyboard shortcuts for preview, rename, selection, copy/cut/paste within Library, and delete. Do not intercept shortcuts while typing in fields or player controls. Provide a keyboard-accessible alternative for every drag operation. On name collisions, offer explicit keep-both, replace, or skip actions; never silently overwrite. Pass: the same operations work from toolbar, context menu, and keyboard, with consistent results and clear batch error reporting.
- [ ] **P5.7 — Make mistakes recoverable.** Build on the retention decision in P5.3: add Trash with restore and offer Undo for supported moves, renames, and soft deletes. Undo must check for later changes and path conflicts before acting. Surface retained file versions only if the chosen storage policy actually retains them. Pass: Undo restores the expected file/location without overwriting newer work; permanent deletion is explicitly identified and confirmed.
- [ ] **P5.8 — Make search useful without taking over.** Keep realtime wildcard search; add visible filters for type, modified date, and size, plus an explicit current-folder/all-my-files scope. Show each result's location and an “Open containing folder” action. Keep ordering stable and explain empty results. Pass: narrowing and clearing filters preserves the query, results never cross vault boundaries, and back navigation restores the previous folder. No recommendation feed or speculative “suggested files.”
- [ ] **P5.9 — Keep controls quiet and accessible.** Show a small selection toolbar only when needed, an optional details panel for full filename/path/type/size/dates, and unobtrusive operation status. Label sharing consistently as “Create grab link” and explain its expiry/use count without suggesting permanent collaboration. Preserve filename extensions when truncating; show full names on focus as well as hover. Pass: keyboard focus is visible, file types remain identifiable without color, narrow screens retain essential actions, and errors remain actionable after transient notices disappear.

Phase exit: browser tests exercise the same actions in list and grid views. Keep cross-user deduplication deferred until a separate privacy-reviewed design exists.

## Phase 6 — Make releases and recovery routine

Goal: a change can be tested, deployed, rolled back, and restored without improvising.

- [ ] **P6.1 — Remove duplicate frontend ownership.** Choose one UI source directory and generate the embedded copy during builds, or embed directly from one source. Split upload, preview, account, and grab code into small modules. Pass: one edit reaches both development and Docker builds with no manual copying.
- [ ] **P6.2 — Add CI.** Run Go tests, `go vet`, race tests, JavaScript checks, and the essential browser flows. Read `Makefile` and `scripts/check-go-lines.sh`; resolve the existing line-count policy rather than silently skipping it. Pass: a clean checkout reproduces the checks and builds the Docker image.
- [ ] **P6.3 — Test actual recovery.** Back up isolated test data, restore onto a fresh node, unlock users, and compare file hashes. Simulate failure during catalog save, vault rekey, and upload commit. Check stale temporary-file cleanup. Pass: the documented procedure restores usable files, not just a parseable recovery kit.
- [ ] **P6.4 — Improve operational visibility.** Record job durations and failures without passwords, passphrases, or grab tokens. Separate process health from storage readiness. Document migrations, backup compatibility, and rollback limits. Pass: a read-only/full data volume is diagnosable and fails clearly.
- [ ] **P6.5 — Rehearse and deploy.** Build a versioned image, check backups and compatibility, preserve host Compose overrides, and test on disposable data first. Deploy completed phases using the established release process. Pass: readiness, login, upload/download, preview, and a disposable grab work after deployment; the release and rollback instructions are recorded.

## Suggested first assignment

Luna: start with P1.1 and P1.2 only. Deliver the shared-service change, regression tests, and test results as a small reviewable commit series. Then move to P1.3. Do not attempt all six phases in one large rewrite.

## Work log

For each completed task, append:

```text
Date:
Task ID:
Commit:
Changed:
Checks run and results:
Known limitations / decisions needed:
Next task:
```

Date: September 21, 2026
Task ID: P1.1–P1.5
Commit: 92f5740
Changed: Shared per-user file-service registry; per-user WebDAV locks; shared-storage quota reservations including unknown-length uploads and grab copies; safe catalog collision and rename handling; Phase 1 regression tests and quota seam.
Checks run and results: `go test ./...` passed; `go vet ./...` passed; targeted `go test -race ./internal/app ./internal/catalog ./internal/quota ./internal/drive ./internal/desk ./internal/filesvc` passed; localhost three-listener smoke test passed.
Known limitations / decisions needed: Browser automation, full restore, restic pruning, and the Phase 2–6 work remain open. Production deployment was not performed.
Next task: P2.1, grab input handling.

Date: September 21, 2026
Task ID: P2.1–P2.4, P2.6
Commit: 036fc00, d7cf3c9
Changed: Strict grab token validation and DOM rendering; server session expiry and secure-cookie mode; password-change session invalidation; bounded local authentication limits; hostname validation and synchronized persistence; per-user Places reads; durable grab-use accounting and retry UX.
Checks run and results: `go test ./...` passed; `go vet ./...` passed; targeted `go test -race ./internal/share ./internal/users ./internal/desk ./internal/drive ./internal/ratelimit` passed.
Known limitations / decisions needed: Stored node-key unlock means host control remains a decryption trust boundary. Browser automation and production deployment remain open.
Next task: begin P3.1.

Date: September 21, 2026
Task ID: P3.1–P3.4
Commit: a174f5c, 76ddf71
Changed: Replaced batch-local uploads with one three-rail background queue; added stable upload state, retry/cancel/collapse controls, saving/failure reporting, and incremental refresh; streamed normal library/WebDAV reads with byte ranges; added chunk-authenticated streaming capsule creation and delivery for large files and folders while retaining legacy capsule reads.
Checks run and results: Focused library, desk, drive, capsule, and share tests passed; streaming capsule tests cover multi-megabyte data, wrong passphrases, interrupted writes, and legacy grabs; JavaScript syntax checks passed.
Known limitations / decisions needed: Browser-level queue testing remains open.
Next task: P3.5 measurement and staging design.

Date: September 21, 2026
Task ID: P3.5
Commit: f2f4c09, fe3ad90, ed4848d
Changed: Replaced transient upload spools with protected per-user staging data and durable manifests; added restart recovery and stale-stage cleanup; added concurrent transfer measurements; added a short-window batch coordinator that links staged files into one restic snapshot and records each file's snapshot object path for later restore.
Checks run and results: `go test ./...` passed; `go vet ./...` passed; focused `go test -race ./internal/library ./internal/desk ./internal/drive ./internal/catalog` passed; `scripts/measure-transfers.sh` passed with 24 files grouped into two batch commits.
Known limitations / decisions needed: Browser-level queue testing, production-volume measurement, and the existing line-count policy remain operational follow-ups.
Next task: Phase 4 preview baseline.

Date: September 21, 2026
Task ID: P4.1 foundation
Commit: 8f50855
Changed: Added encrypted private raster thumbnail caching with file-version/renderer/size keys and bounded eviction; added an authenticated multiuser and single-user thumbnail route; made native image, audio, video, and PDF previews stream from restic instead of buffering them in Go; added grid thumbnail rails with intersection-based look-ahead, bounded document-preview work, and inline range-backed audio/video cards. Synced both frontend source copies.
Checks run and results: `go test ./...` passed; `go vet ./...` passed; `node --check mockup-ui/app.js` and `node --check mockup-ui/views.js` passed; `git diff --check` passed; localhost bootstrap/unlock/upload/thumbnail smoke passed.
Known limitations / decisions needed: Format capability detection, XLSX/3MF renderer improvements, single-player lifecycle, and the 100/500-file browser cold/warm benchmark remain open. The existing repository line-count check still reports the pre-existing oversized `internal/desk/multi.go`, `internal/drive/drive.go`, and `internal/users/store.go` files.
Next task: P4.2 bounded server preview jobs and the large-folder baseline.

Date: September 21, 2026
Task ID: P4.2 foundation
Commit: pending
Changed: Added a process-wide four-worker thumbnail semaphore and per-user in-flight job sharing keyed by the encrypted file-version thumbnail key. Duplicate visible requests now wait on one restore/decode/cache write; cancelled waiters stop waiting without interrupting an already useful shared cache job. Added `scripts/measure-previews.sh` plus 100-item and 500-item renderer benchmarks with allocation reporting.
Checks run and results: `go test ./internal/library ./internal/desk` passed; targeted `go test -race ./internal/library ./internal/desk` passed; `go vet ./...` passed; JavaScript syntax checks passed; preview benchmark completed at approximately 1.12 seconds/557 MiB allocations for 100 synthetic thumbnails and 5.54 seconds/2.79 GiB allocations for 500 synthetic thumbnails on this workstation.
Known limitations / decisions needed: The benchmark is a renderer baseline, not a browser paint or network measurement. Parser execution limits, true obsolete-job cancellation, shared capability detection, and the 100/500-file browser/node benchmark remain open.
Next task: P4.5 shared preview capabilities and generic MIME detection.

Date: September 21, 2026
Task ID: P4.3–P4.4
Commit: pending
Changed: Split document and model preview parsing into bounded helpers. XLSX previews now resolve shared strings and cell types into row/cell labels; PPTX previews sort slide numbers naturally; 3MF previews honor build objects, nested components, and 3MF affine transforms; STL and 3MF geometry rejects non-finite coordinates and remains capped.
Checks run and results: `go test ./...` passed; `go vet ./...` passed; `go test -race ./internal/desk` passed; new XLSX, PPTX, 3MF component-transform, and existing STL/DOCX tests passed. New preview files remain below the repository's 300-line file policy.
Known limitations / decisions needed: These are text-only document previews rather than page-faithful office rendering. The browser still chooses preview behavior from a frontend extension/kind allowlist; generic MIME capability detection, SVG safety, and browser/node large-folder measurements remain open.
Next task: P4.5 shared preview capabilities and generic MIME detection.

Measurement run, September 21, 2026:

- 24 concurrent unique 256 KiB files: 6,291,456 logical bytes, 2 batched restic commits, 4.513 seconds, 6,302,007 repository bytes.
- One generated 6 MiB disk-image-shaped file: 1 restic commit, 0.777 seconds, 2,068 additional repository bytes because restic deduplicated the generated pattern against existing chunks.
- Go total-allocation delta: 2,139,096 bytes; process high-water memory: 87,512 KiB; staging bytes after completion: 0. Host-volume runs should be repeated on the production data mount.

The result justifies the batch format, but it is not a claimed speed multiplier for production hardware. The measurement harness is `scripts/measure-transfers.sh` and can use `WEAZLCLOUD_MEASURE_ROOT` to place its temporary repository on the configured data volume.
