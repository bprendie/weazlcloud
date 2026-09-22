# WeazlCloud implementation workbook — September 22, 2026

Owner: Bob. Implementation: Luna.
Status: ready for independent implementation tasks; specific policy decisions remain open below.
Continues `phase_plan_2026-09-21.md`. No task in this workbook is complete merely because it appears here.

## Confirmed product decisions

- Uploads must resume after browser and server restarts, including large ISOs. A background tray alone is insufficient.
- Improve mounted-drive folder browsing first.
- WeazlCloud is not a recovery service. Keep Trash for 30 days and automatically empty it; do not add overwrite version history or a backup service.
- Disabling an account revokes its grab links. Deleting a user deletes all owned assets, with no retirement retention period.
- Maintenance runs when the node is idle.
- RAW/HEIC photos are the first enhanced-preview priority.
- Google Takeout is the first import source. Keep both files when imported content differs at an existing path.
- Normal simultaneous saves may replace each other: last successful committed write wins. Failed writes must not replace committed bytes. Import conflicts use the separate keep-both rule.
- Keep local identity and local processing, shared available-disk storage, the silent system reserve, and no fixed maximum upload size.
- Preserve per-user isolation. Admin controls must not expose other users' files. Stored node-key convenience remains; host control permits decryption.

## Rules for Luna

1. Read the listed code before changing it. Verify behavior: older checked boxes and work logs are evidence to investigate, not proof of every acceptance condition.
2. Work one task at a time in the order below. Keep each commit reviewable; prefix it with its task ID, such as `G1.2: persist resumable upload offsets`.
3. Record changed files, checks, actual results, commit, remaining limitations, and next task in this workbook. Check a box only after its acceptance conditions pass.
4. Use temporary users, disposable files, and separate data directories. Do not use real files, consume real grab links, delete existing users, or run disk-filling tests on production.
5. Production currently runs commit `7a6a377`. Preserve its host Compose edits and `/exports/dockervolume/weazlcloud` mount. This workbook is implementation work, not an instruction for another deployment.
6. Edit UI source in `mockup-ui/`. Run `make desk-assets` before direct Go commands; Make test/build targets generate the embedded copy. Never commit generated `internal/desk/ui/`, executables, runtime data, credentials, or screenshots.
7. Keep Go files below 300 lines. Extract cohesive modules as needed. New UI behavior should use focused modules rather than expanding the existing monolith indefinitely.
8. Run focused behavioral tests for changed storage/concurrency paths. Before each completed workstream, run `make check`; use the relevant browser/container smoke checks. Do not run the 500-file benchmark unless Bob requests it.
9. Open decisions block only dependent behavior. Continue other tasks; do not silently choose retention, administrator succession, remote exposure, or transcoding policy.
10. Do not log passwords, vault phrases, grab URLs/tokens, private filenames, or request bodies. Report failures with operation type and safe diagnostics.

## Decisions still needed

| ID | Decision needed | What may proceed meanwhile |
|---|---|---|
| D1 | How long to keep incomplete uploads: 24 hours, seven days, or another duration? | Durable sessions, cancellation, resume and tests with a configurable clock; do not ship an arbitrary expiry. |
| D2 | Is file/folder reselection after reopening the browser acceptable? Is a local helper acceptable if persistent access is essential? | Server resume protocol and a prototype that explicitly requests reselection. Do not promise unattended resume. |
| D3 | Must mounted access remain helper-free? Should internet access be added? | Optimize existing Thunar/WebDAV browsing; keep existing network exposure. |
| D4 | Multiple local administrators, or a single administrator with transfer? | Disable/delete ordinary users; protect the last active admin and defer role changes. |
| D5 | Where to show maintenance failures: admin dashboard, local email, or both? | Safe local logs and structured internal status; defer notification transport. |
| D6 | May low-space cleanup evict completed ZIPs before their 90-minute expiry? | Existing bounded preview eviction and cleanup of expired data only. |
| D7 | Bundle photo converters in the main image, or an optional local rendering container? | Compare local renderers and prototype behind a boundary; present measured tradeoffs before packaging. |
| D8 | Transcode unsupported video, or download-only? | RAW/HEIC work; preserve current video behavior. |
| D9 | Skip identical import files, or keep both? What should whole-library exports include? | Keep both differing files; export current files as a prototype, not a settled full-export policy. No version history. |
| D10 | Target concurrent users, folder sizes, largest files, and permission for production-host load tests? | Small disposable workstation tests and existing fixtures. Record their limits. |

## Execution order and dependencies

Start G8.1 to establish reliable checks. Implement G1 server work, then browser work once D1/D2 are settled. Measure G2 before optimizing it. Build G5's idle coordinator before connecting G3 cleanup. Implement G4 with coordination for active jobs. Complete the G6 converter comparison and G7 importer design while other decisions are pending. G8 failure checks run throughout, then close the workbook with an integrated smoke pass.

First assignment: G8.1 and G1.1. Deliver the verification findings and a resumable-upload protocol/design note with tests for its invariants; then proceed to G1.2. Do not implement all eight streams in one rewrite.

## G1 — Uploads that resume across restarts

Read: `internal/library/staging.go`, `internal/library/batch.go`, `internal/quota/quota.go`, `internal/filesvc/registry.go`, `internal/desk/multi_library.go`, `mockup-ui/engine.js`, upload queue functions in `mockup-ui/app.js`, and transfer tray in `mockup-ui/views.js`.

- [x] **G1.1 — Define the upload contract.** Document create/status/append/finalize/cancel operations, owner checks, destination, expected length, confirmed offset, content verification, session expiry, and retry semantics. Reuse existing durable staging where possible. Decide how state is encrypted/protected at rest; do not introduce a plaintext spool without documenting its vault-lock behavior. Preserve existing PUT clients. Pass: examples cover initial upload, duplicate chunk, wrong offset, interrupted chunk, repeated finalize, and restart.
- [x] **G1.2 — Implement durable server sessions.** Keep spool and manifests on the configured data volume. Bound per-request buffers and concurrent writers; serialize writes per session. Acknowledge only durable bytes and reconcile partial tails on restart. Verify content before publishing to the catalog. Finalization must be idempotent, including failure after restic success but before catalog save. Pass: restart midway and finish with an identical hash; repeated chunks/finalize do not duplicate bytes or catalog entries; another user cannot inspect or resume the session.
- [ ] **G1.3 — Integrate space and lifecycle checks.** Reconstruct reservations after restart without counting already occupied bytes twice. Reserve remaining workspace, enforce actual disk headroom, release on cancel/expiry/commit, and reject writes for disabled/deleted owners. Do not clear usable sessions merely because the process restarted. Pass: unknown-length and nearly-full-volume tests fail cleanly, leave committed files intact, and release reservations correctly. Production expiry waits for D1.
- [ ] **G1.4 — Resume from the browser.** Persist nonsecret queue metadata, reconcile it with owner-scoped server sessions after login, and preserve the three upload rails. Show server-confirmed progress separately from saving. Reconnect with backoff; cancel old requests before retrying. Match reselected files using content verification, not filename alone. Clear private client state on logout/account change. Pass: close/reopen the browser and restart the server midway; resumption sends only missing data and leaves Library usable. Final UX depends on D2.

Required evidence: hashes, bytes retransmitted, restart boundary, quota reservations before/after, and observed peak memory with a large generated payload. Chunk limits must not become file-size limits.

G1 implementation notes from September 22:

- `POST /api/uploads` creates an owner-scoped session with `{path,size,hash}`; `GET /api/uploads/{id}` reports the server-confirmed offset; `PATCH /api/uploads/{id}` accepts one bounded chunk with `Upload-Offset`; `POST /api/uploads/{id}/finalize` verifies the full SHA-256 and publishes through the shared Library; `DELETE` cancels and removes the session.
- Chunks are limited to 16 MiB per request and the browser uses 8 MiB rails. Each chunk is first synced to an owner-only `.chunk` file, then appended and synced to the owner-only `.part` file before the manifest offset advances. A restart repairs a durable chunk or a synced tail. The manifest remains as a small `complete` record so repeated finalize is idempotent; payload bytes are removed after commit.
- Session operations require the owner’s unlocked local vault. The spool is protected by the configured data volume and `0600`/`0700` permissions; it is not exposed through any other user or protocol while the vault is locked. This is the documented local-only vault-lock boundary while encrypted upload spools remain a later hardening choice.
- The browser persists nonsecret session metadata and resumes after a server restart. After a browser reopen it asks the user to reselect the original file and matches the pending session by destination and size; full content-identity matching and incomplete-session expiry remain dependent on D2 and D1.
- Focused tests cover wrong offsets, interrupted chunks, restart recovery, repeated finalize, and stored-payload hashes. `make check` and the updated disposable-container smoke passed. A large generated payload/peak-memory measurement and production load test remain deferred under D10.

## G2 — Faster Thunar folder browsing

Read: `internal/drive/drive.go`, `internal/drive/filesystem.go`, `internal/filesvc/registry.go`, `internal/users/store.go`, `internal/library/library.go`, `internal/restic/run.go`.

- [ ] **G2.1 — Measure the current path.** Use existing/small disposable nested folders. Record cold/warm PROPFIND latency, time until Thunar displays entries, auth verification count, restic subprocess count, and listing/catalog work per request. Test while a browser upload runs. Pass: reproducible command/client steps and measurements identify the dominant cost.
- [ ] **G2.2 — Fix the measured browsing bottleneck.** Prefer eliminating repeated full catalog scans, repeated initialization, or redundant per-entry metadata work. Preserve shared registry ownership and mutation visibility. Any authentication cache must be bounded and immediately invalidated by password change, disable, deletion, and relevant credential changes. Pass: before/after results on the same fixtures show the benefit; Desk/WebDAV changes appear without stale cross-user results.
- [ ] **G2.3 — Verify daily use.** Browse down/up, refresh, open a small file, save a changed file, rename, and reconnect after restart. Pass: Thunar and Desk agree on paths/content; no new helper or network exposure is required for this baseline. Larger protocol changes wait for D3 and measurements.

## G3 — Thirty-day Trash with automatic emptying

Read: `internal/catalog/mutations.go`, `internal/library/maintenance.go`, `internal/desk/trash.go`, `internal/restic/ops.go`. Depends on G5.1.

- [ ] **G3.1 — Audit safe reclamation.** Check expiry boundaries, empty folders, zero-byte files, replacement at a trashed path, and mixed live/trashed entries sharing a restic snapshot. Inventory references held by active streams, captured ZIP manifests, and pending upload commits. Do not prune any snapshot still needed by these operations. Pass: tests cover each case, restore never creates duplicate live paths, and cleanup does not rely on positive byte counts to remove expired folders.
- [ ] **G3.2 — Connect idle cleanup.** Process expired Trash after 30 days even without a user visiting Trash. Use a narrowly scoped background resource with existing stored-key capability; do not leave a Desk vault marked unlocked merely for maintenance. Record resumable cleanup intent so a failure between catalog purge and restic forget/prune is recoverable. Pass: expired entries are gone after an eligible idle pass, newer Trash restores, physical reclaim is measured for unreferenced content, and injected failure retries safely.

No overwrite history, restoration service, or new backup UI. Existing release backups remain operational procedure.

## G4 — Disable and delete accounts completely

Read: `internal/users/store.go`, `internal/users/profile.go`, `internal/users/access.go`, `internal/filesvc/registry.go`, `internal/filesvc/archives.go`, `internal/desk/multi_account.go`, `internal/capsule/grab.go`, `internal/capsule/store.go`, `internal/drive/drive.go`.

- [ ] **G4.1 — Disable atomically.** Persist disabled state, revoke sessions and all owned grabs, stop queued/new work, and invalidate mounted access. Define cancellation of already active transfers: bytes already delivered cannot be recalled. Pass: existing Desk session, WebDAV credentials, upload session, and grab URL cannot start new work; incomplete revocation resumes after restart.
- [ ] **G4.2 — Implement durable deletion.** First mark the account deleting and block access. Cancel/drain uploads, renders, ZIPs, and other workers. Remove vaults, keys, catalog, repository, Trash, previews, staging, generated archives, recovery-kit copies, owned capsules, and per-user settings/runtime state. Use validated server-side owner paths. Keep only a minimal cleanup marker until deletion finishes; do not retain user data for a grace period. Pass: restart at every deletion stage converges to no owned runtime assets, workers cannot recreate them, and another user's assets remain hash-identical.
- [ ] **G4.3 — Expose admin controls.** Add explicit Disable and Delete actions with an unambiguous destructive confirmation. Report pending/failed deletion accurately. Prevent deletion/disable of the last active administrator; defer administrator promotion/transfer to D4. Pass: non-admin calls fail and the admin UI never offers vault browsing.

Scope note: deletion applies to the live system and owned assets under its control. Separate historical backups are not silently rewritten. Do not claim secure erasure of physical media or external backup copies.

## G5 — Maintenance when idle

Read: `internal/app/run.go`, `internal/filesvc/registry.go`, `internal/filesvc/archives.go`, `internal/library/thumbnail.go`, `internal/capsule/grab.go`, `internal/desk/events.go`.

- [ ] **G5.1 — Add a bounded idle coordinator.** Track active storage operations across Desk, WebDAV, uploads, previews, ZIPs, and grabs. Long-lived SSE connections and health probes must not permanently prevent idle. Make the quiet interval configurable and document its initial technical default. Run one maintenance job at a time; yield/cancel safely when foreground activity resumes. Pass: a fake-clock test covers idle eligibility, continuous probes/SSE, resumed activity, shutdown, and no overlapping jobs.
- [ ] **G5.2 — Register cleanup jobs.** Include expired capsule payloads, expired/cancelled on-demand ZIPs, safe abandoned staging, bounded preview eviction, and G3 Trash cleanup. Keep the 90-minute ZIP policy. Grab payloads follow their burn/revoke/expiry lifecycle, not ZIP expiry. Use retry/backoff and restart reconciliation; never delete active temp packs just because their names resemble stale files. Pass: jobs run without visiting a listing, active work survives, and interruption does not leak reservations or falsely mark cleanup complete. Incomplete-upload expiry waits for D1; early ZIP eviction waits for D6.
- [ ] **G5.3 — Report useful status.** Record last attempt/success, duration, reclaimed bytes where measured, and safe failure category. Avoid filenames and capability IDs. Pass: an induced disk/permission failure is distinguishable from an empty cleanup pass. Notification destination waits for D5.

## G6 — RAW/HEIC photo previews first

Read: `internal/desk/capability.go`, `internal/desk/thumbnail.go`, `internal/library/thumbnail.go`, `mockup-ui/app.js`, `mockup-ui/views.js`, `deploy/Dockerfile`.

- [ ] **G6.1 — Compare local renderer options.** Identify supported RAW families and HEIC variants using documented, redistributable fixtures or Bob-provided samples. Compare output quality/orientation, peak memory, CPU/time, dependencies, image size, and maintenance requirements. Pass: record actual successful formats and failures plus a recommendation; do not claim every RAW format works. Packaging waits for D7.
- [ ] **G6.2 — Integrate a bounded renderer.** Apply time, input, output, dimensions, process, and concurrency limits. Run without network and without broad host filesystem access. Use private temporary directories on the data volume and the existing encrypted, owner/version-scoped preview cache. Preserve originals and renderer-version invalidation. Pass: supported photos show correct orientation and useful thumbnails; malformed/huge input times out safely; locked/other-user access remains denied.
- [ ] **G6.3 — Verify grid behavior.** Use current visible-card scheduling and warming; do not fetch full originals for every offscreen card. Pass: mixed supported/unsupported photos show thumbnails or stable fallback, scrolling stays usable during an upload, and a warm revisit reuses cache.

Video posters, audio artwork, faithful Office layout, and video transcoding remain later tasks. D8 controls transcoding; do not bundle it into photo work.

## G7 — Google Takeout import

Read: `mockup-ui/app.js` ingest placeholder, `mockup-ui/views.js`, `internal/library/staging.go`, `internal/library/batch.go`, `internal/catalog/catalog.go`, `internal/catalog/mutations.go`, `internal/library/archive.go`.

- [ ] **G7.1 — Specify the first Takeout subset.** Inventory a representative disposable Google Drive Takeout archive, including exported Docs formats, directories, timestamps, metadata sidecars, and split exports. Document supported archive types and limitations before accepting uploads. Choose a browser/server ingestion path consistent with G1; do not silently build a general Photos migration product. Pass: a fixture manifest states the expected resulting paths/content and progress units.
- [ ] **G7.2 — Build safe resumable ingestion.** Persist owner-scoped job and per-entry commit state. Stream extraction with quota-aware spool and bounded memory, entry counts, archive expansion, and concurrency; reject traversal, unsafe links, device entries, and malicious names. Preserve valid timestamps. Commit through the shared library service and resume without duplicating completed entries. Pass: restart midway, retry a failed item, and compare resulting paths/hashes with the fixture manifest. Parsing bounds must be distinguished from normal upload size policy.
- [ ] **G7.3 — Keep both on differing-path conflicts.** Allocate deterministic names while holding the appropriate mutation lock, preserve extensions, and include folder/file conflicts. Persist the selected destination so retries do not generate extra copies. Pass: parallel imports and normal writes never silently overwrite differing imported content. Identical-file policy waits for D9.
- [ ] **G7.4 — Replace mock progress with real UI.** Show committed/skipped/failed counts, bytes where known, resumable status, cancellation, and durable failure details in the background tray. Pass: navigation remains usable, partial failure is visible, and completed items are not imported twice.
- [ ] **G7.5 — Specify an ordinary-file exit.** Reuse bounded archive/export infrastructure for current files with a manifest and hashes; document naming and large-export handling. Full-export contents beyond current files wait for D9; there is no retained-version feature in scope.

## G8 — Failure tests and evidence

Read: `Makefile`, `.github/workflows/ci.yml`, `scripts/smoke-browser.sh`, `scripts/recovery-smoke.sh`, `scripts/container-smoke.sh`, `internal/app/run.go`, `internal/ready/ready.go`, and existing storage tests.

- [x] **G8.1 — Audit existing verification first.** Check whether `/ready` detects an unwritable/full data or temp volume rather than only path existence. Verify smoke scripts control the actual child process, use unique disposable names, clean only resources they created, fail on real errors, and assert their advertised behavior. The current browser page marker is not an authenticated workflow test. Pass: record concrete gaps, fix failing harness assumptions, and add deterministic checks where claims are unsupported.
- [ ] **G8.2 — Inject failures locally.** Exercise interrupted catalog save, upload finalize, maintenance prune, user deletion, missing/read-only/full volume, and server restart. Use test seams or isolated bounded filesystems rather than filling host disks. Pass: committed content remains readable, incomplete operations reconcile, and readiness/errors explain storage failure without secrets.
- [ ] **G8.3 — Check write ordering.** Race normal Desk and WebDAV replacements with distinct payload hashes, then verify final catalog and bytes agree with the final successful commit. A failed write must not win. Separately verify Takeout keep-both behavior. Pass: repeatable tests and race detector results; no mixed or truncated content.
- [ ] **G8.4 — Rehearse upgrade and mixed use.** On this workstation, use populated disposable older-format data. Run browsing, upload, thumbnail, ZIP, maintenance, and mounted access together. Check actual login/unlock and user isolation through the browser/API. Pass: record latency, memory, queue limits, hashes, migration result, and rollback constraints. D10 governs broader load or production-host tests; use existing small fixtures meanwhile.

G8 progress on September 22:

- G8.1 is complete. `/ready` now writes, syncs, and removes bounded probes in the configured data and temp directories. The browser smoke builds and controls a direct child binary, authenticates and unlocks a disposable account through the API, and then checks the page. Docker smoke names and removes only its own unique container and volume.
- G8.2 has deterministic coverage for missing and read-only data paths, catalog atomic-save failure, upload-finalize failure, durable staging, and restart reconciliation. Maintenance-prune, user-deletion, and genuinely full-volume injection remain open because those workflows are not yet implemented and no disk-filling test is allowed on the workstation.
- G8.3 has a repeatable Desk/WebDAV replacement race; the final bytes must equal one complete successful payload, and `go test -race ./...` passes. Takeout keep-both remains pending G7.
- G8.4 has passed the authenticated browser, restore, and disposable container smoke paths. A populated older-format upgrade rehearsal, simultaneous ZIP/thumbnail/maintenance/mounted use, and measured latency/memory report remain open.

## Acceptance and handoff

A stream is complete only when its stated pass conditions hold and unresolved decisions affecting it are settled. Passing syntax or static-page checks does not prove authenticated actions, restart recovery, disk reclamation, or deletion safety. Report the precise scope tested.

Use this entry for every delivered task:

```text
Date / task ID:
Commit:
Behavior changed:
Files changed:
Checks and fixtures:
Observed results:
Open decisions / limitations:
Next task:
```

## Decision log

September 22: Recorded eight gaps and 24 questions in `d22e4ee`.
September 22: Recorded 30-day Trash, destructive user deletion, grab revocation, idle maintenance, keep-both import conflicts, and replacement semantics in `b9a2263`.
September 22: Bob confirmed restart-resumable uploads, folder-browsing priority, RAW/HEIC priority, and Google Takeout first. Expanded this workbook into ordered implementation tasks, acceptance conditions, and explicit remaining decisions. No runtime changes or production deployment performed by this planning update.
September 22: Completed G8.1 verification hardening and the first G8.2/G8.3 failure and replacement checks. `make check`, authenticated browser smoke, recovery smoke, and updated disposable-container smoke passed. G8.2/G8.3/G8.4 remain partial as recorded above; no production data was touched.
