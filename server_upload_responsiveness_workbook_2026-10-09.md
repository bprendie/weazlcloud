# Server upload responsiveness — October 9, 2026

Status: S0–S5 complete; S6 deployed and healthy. Owner unlock and live-seed
resumption verification remain pending.
The user explicitly authorized execution through production deployment.
Owner: Sol (server). Astra owns the iOS changes on an inaccessible Mac.
Implement S0–S5, document and commit the verified implementation, and prepare the
S6 production release. The user intends to deploy this work; coordinate the
actual cutover and owner unlock while the seed is active.

## Sol: execution instructions

1. Work in `/home/bobp/Code/weazlcloud`. Read repository instructions, this entire
   workbook, and `docs/mobile-upload-performance-2026-10-09.md`. Check Git status
   and recent commits first; retain unrelated changes and untracked screenshots.
2. Work through every unchecked item in S0–S5. Make routine implementation
   decisions yourself. Record decisions and test evidence here as each phase
   finishes. Do not mark a phase complete from code inspection alone.
3. Start with instrumentation and deterministic local reproductions. Confirm
   contention before claiming it explains production timeouts. Keep diagnostics
   useful in the final release, with bounded overhead and no private contents.
4. Keep the old mobile API operational throughout. Write Astra's additive API
   handoff before release; do not assume access to or modify the real iOS app.
5. Use isolated local containers and synthetic fixtures. Read-only production
   inspection is available at `bobp@weazlcloud.teralab.local`; do not benchmark
   against, reset, or restart the ongoing seed during implementation.
6. After required tests pass, update docs, commit only intended files, and push
   the branch to its configured remote. Record commit SHA and exact image digest.
   Prepare the release/rollback checklist before the coordinated S6 cutover.
7. If a safety or authorization invariant fails, fix it before release. If owner
   unlock is needed after deployment, ask for it and report verification pending;
   do not manufacture credentials or claim the queue resumed without evidence.

### Required handoff artifacts

- Completed checklist and decisions in this workbook.
- `docs/mobile-upload-responsiveness-results-2026-10-09.md`: commands, fixture and
  resource limits, baseline/candidate metrics, integrity checks, and limitations.
- `docs/mobile-upload-status-batch.md`: implemented capability discovery,
  request/response examples, limits, errors, Retry-After, and older-server fallback
  for Astra. Clearly distinguish proposed contracts from implemented contracts.
- Updated mobile API docs/schema and relevant operator settings documentation.
- Tested release image plus rollback image, commit SHA, and S6 deployment record.

### Production context: verify before using

- SSH: `bobp@weazlcloud.teralab.local`.
- Checkout: `/home/bobp/containers/weazlcloud`.
- Persistent data: `/exports/dockervolume/weazlcloud`, mounted at `/data`.
- Containers: `weazlcloud` and `deploy-weazlcloud-photo-worker-1`.
- Desk/Grab/WebDAV: ports 7272/7273/7274. Public desk:
  `https://weazlcloud.prendie.io`.
- Production `deploy/compose.yaml` contains local customization. Preserve it;
  update image references without overwriting mounts, limits, environment, or
  security settings with the repository template.
- The last observed release was `weazlcloud:release-f63cc3d4c58a`; inspect the
  running version rather than assuming it is still current. The 32-core host's
  API container was limited to 16 CPUs; use actual container limits for sizing.
- Restart relocks the owner's vault. Plan owner unlock as part of cutover.
  Existing checkpoints are rollback evidence, not disposable staging space.

## Objective and evidence

Receive encrypted photo parts promptly while finalization, browsing, and status
polling continue. Preserve every existing upload, receipt, album, Hidden flag,
Live Photo pair, source identity, and durable storage reference.

The baseline release is `f63cc3d` (catalog batching and eight finalizers on large
hosts). Do not undo its batching or assume another worker increase will help.

Observed on production October 9:

- A two-minute sample published 67 photos and stored 66 components; the earlier
  storage sample handled 43 components. Different payloads prevent a controlled
  speedup claim. The queue subsequently grew from 214 to 229.
- Another overlapping two-minute sample had 851 individual status GETs, plus
  repeated timeline/album/preparation refreshes.
- Several part PUTs failed together after 60–78 seconds. The handler maps many
  unrelated errors to `503 storage_unavailable`; logs do not establish the cause.
- The phone reports 123 Mbps upload to the public Internet (about 15 MB/s).
  This supports adequate Wi-Fi capacity, but does not benchmark the LAN route,
  iCloud export, app scheduling, or this API.
- `mobileGrantReader.Read` checks authorization before and after each read.
  `CheckDeviceGrant` shares `users.Store.mu` with `WithDeviceGrant`, which holds
  that mutex through catalog publication. This is a confirmed contention path;
  its contribution to transfer latency still needs measurement.
- `mobileparts.Append` holds the upload-session gate across body reads, encryption,
  fsync, and state publication. `Status` takes the same gate. Parallel components
  or parts of one asset therefore wait, including a Live Photo's motion component.
- The API server config sets a 60-second **idle connection** timeout, not a
  60-second request-body timeout. Do not change it based on matching numbers.

## Invariants

- Keep `parts-v1`, existing IDs, encryption, checksums, receipts, and resume behavior.
  No reset, reseed, database migration, or cleanup of pending uploads.
- A part acknowledgment requires validated, durable encrypted bytes. `stored`
  requires durable library publication; staging or previews are not completion.
- Revocation, account deletion, scope changes, vault locking, and cancellation
  must remain effective during transfers and at publication.
- Bounded memory and disk admission; stream large originals and videos. Never
  stage plaintext or buffer entire originals in RAM.
- Use cgroup-visible CPU/RAM limits. Keep the two-CPU/four-GiB deployment viable.
- No global lock may span network body reads. Do not fix throughput by removing
  authorization guards, delaying fsync acknowledgments, or adding unbounded jobs.
- Existing clients and single-upload status endpoints continue to work.
- Keep Go source/test files below 300 lines. Split by responsibility.

## S0 — Measure where requests wait

Read `internal/desk/mobile_parts.go`, `mobile_parts_worker.go`,
`internal/mobileparts/{operations,store,jobs,cancel,reservations}.go`,
`internal/users/device_grants.go`, and `internal/library/photo_catalog_queue.go`.

- [x] Add structured, bounded diagnostics for part handling: route/protocol,
  expected/received bytes, authorization wait, session/part admission wait,
  body-read time, encryption/write time where separable, sync time, metadata
  commit time, and total duration. Explain overlapping timings rather than
  summing them as independent phases.
- [x] Log the actual failure class: cancellation, read deadline, unexpected EOF,
  checksum mismatch, authorization, capacity, or filesystem/storage failure.
  Preserve compatible client status/code behavior initially; separate internal
  diagnostics from any later public error-contract change.
- [x] Track bounded counters/histograms for active receivers, queued finalizers,
  status calls, lock waits, and batch sizes. No raw tokens, URLs with secrets,
  filenames, hashes, payloads, or unbounded metric labels.
- [x] Record whether traffic arrives directly or through the configured proxy
  without trusting arbitrary forwarded headers. Never log credentials.
- [x] Add failure-path tests and collect a disposable local baseline using the
  existing throughput harness. No production load generator.

Done: a failed request can be attributed to a phase/error rather than a generic
503. Diagnostics are sampled/aggregated so observing uploads does not become a
new disk or locking bottleneck.

## S1 — Separate authorization reads from catalog persistence

- [x] Write down a lock-order table covering user/device state, publication,
  library/catalog, vault lifecycle, and upload sessions before editing locks.
- [x] Introduce a dedicated authorization/publication barrier or equivalent
  versioned design. Ordinary grant checks must read current authorization under
  a short state lock, without waiting for catalog serialization/encryption/fsync.
- [x] Keep publication atomic with respect to revocation. One candidate is a
  publication read lease, briefly validated against user state, with invalidation
  taking an exclusive lease. Never hold the general user-state mutex while
  invoking catalog/storage callbacks. Document acquisition order and fairness.
- [x] Audit every invalidation path: device revoke/reauthorize/rotation/expiry,
  account suspension/deletion, scope updates, and vault lifecycle cancellation.
  Do not introduce a stale TTL authorization cache or a lease that lasts for the
  entire upload body. Expiry must still be checked at publication.
- [x] Test a deliberately blocked catalog save while unrelated authenticated
  body reads and status requests continue. Test revoke-versus-publish ordering,
  different owners, scope loss, expiry, lock, deletion, and cancellation under race.

Done: slow catalog I/O cannot hold up ordinary grant checks; an invalidated grant
cannot start a later publication. Existing authorization isolation tests pass.

## S2 — Receive outside the upload-session gate

- [x] Split Append into short admission, unlocked encrypted streaming, and short
  durable commit. Capture the session/generation and part identity at admission.
- [x] Use bounded per-part admission for `(owner, upload, component, index)`.
  Independent parts may receive concurrently within resource limits; identical
  retries cannot double-write receipts, counters, quota, or queue markers.
- [x] Revalidate generation, state, authorization, and vault lifecycle before
  committing. Persist the part and receipt using the existing recovery rules;
  queue finalization exactly once only when every required component is durable.
- [x] Keep keys/lifecycle leases valid until writers stop. Cancellation, deletion,
  expiry sweep, and shutdown must drain active receivers before deleting their
  files or releasing quota. Prevent late publication from a cancelled receiver.
- [x] Make all admission waits context-aware. Status/parts listing must return
  committed progress without waiting for an in-flight body. Do not expose partial
  bytes as durable received bytes.
- [x] Test stalled body + responsive status, concurrent Live Photo components,
  out-of-order parts, identical/conflicting retries, cancellation while streaming,
  revoke/lock, sync failure, restart between data/receipt/state writes, and
  queue recovery. Retain existing slow-receive and duplicate-stream coverage.

Done: a slow part cannot block status or unrelated parts; duplicate/recovery and
storage ownership invariants hold under race and fault injection.

## S3 — Batch status contract for Astra

Implemented additive API; clients must discover `features.upload_status_batch_v1`
in the deployed server before using it:

`POST /api/v1/photos/uploads/status`

```json
{"upload_ids":["existing-upload-id","another-upload-id"]}
```

- [x] Accept 1–100 unique validated IDs, maximum request body 16 KiB. Bound
  response size and processing work; preserve input order.
- [x] Authenticate once, enforce owner/device/photo-kind isolation for each ID.
  Return the same `transfer` projection as individual status, including the
  existing durable receipt on `stored`. Missing and foreign IDs share a
  nondisclosing per-item `not_found` result. One missing ID does not fail others.
- [x] Keep reads short and independent of body streams. Avoid decrypting the
  library catalog or scanning all staging directories for a status batch.
  Any in-memory status cache is bounded and updated only after durable changes;
  restart falls back to encrypted session state.
- [x] Send `Cache-Control: no-store` and `Retry-After: 5` when pending uploads
  remain. This interval is guidance, not permission to poll every ID separately.
- [x] Add a capability flag `upload_status_batch_v1`, limit 100, and recommended
  interval 5 seconds to the existing mobile capability projection. Confirm naming
  fits its current schema; publish final request/response examples for Astra.
- [x] Register the literal `/status` route before generic upload-ID dispatch.
  Preserve legacy format dispatch and individual GETs; negotiate/fallback safely.
- [x] Add API/schema tests for limits, malformed input, owner/device isolation,
  Hidden uploads, Live Photos, mixed terminal/pending entries, and vault lock.

Done: Astra can replace per-upload polling with one bounded request every 3–5
seconds (5 recommended), backing off to 10–15 seconds on unchanged state and
stopping terminal polls. Until the capability exists, use slower individual GETs.
An event stream is deferred; it adds reconnect/resume complexity and is not
required for this pass. No new client build should assume this API already exists.

## S4 — Backpressure and fair scheduling

- [x] Audit receiving limits separately from finalizer limits. Bound active
  receivers and staged-but-not-stored work by owner, bytes, and global resources.
  Account for existing reservations and current disk availability.
- [x] Apply new pressure to new admission, not by rejecting or deleting already
  accepted queued jobs. Prioritize completing accepted work. Persist restart-safe
  accounting; avoid retry storms and starvation between users.
- [x] Advertise actual recommended upload concurrency and pending-work limits
  through capabilities. Keep conservative small-host defaults; use measurements
  to select large-host values instead of matching the physical core count.
- [x] Return an explicit retryable overload response with `Retry-After` before
  receiving a new body where possible. Document its distinction from disk full,
  lost authorization, corruption, and transport cancellation.
- [x] Preserve asynchronous previews and existing Restic/catalog batching.
  Revisit batch windows only if phase metrics identify a remaining bottleneck.

Done: overload slows new admission predictably while accepted uploads finish;
memory, staging reservations, and goroutines remain bounded.

## S5 — Local proof and documentation

- [x] Extend `scripts/smoke-mobile-throughput.py` with fixed comparable fixtures:
  slow body, Live Photo, large multipart video, ordinary images, and a populated
  catalog representative of the existing library. Reuse current fixtures; no
  500-photo load test or production data export.
- [x] Compare identical payloads/limits before and after, at least three runs:
  bytes accepted/sec, photos stored/min, p50/p95 part/status latency, error classes,
  lock wait, CPU/RAM, and queue growth. Distinguish receiving from finalization.
- [x] Replay the observed status-polling load alongside uploads, then repeat with
  batched polling. Inject client cancellation separately from server failures.
- [x] Test two-CPU/four-GiB and a larger cgroup configuration, both storage backends,
  plus restart/upgrade with incomplete, queued, and verifying sessions.
- [x] Gates: no hangs/deadlocks; exact restored hashes/receipts; responsive status
  during a deliberately stalled body (local p95 under 500 ms); no unexplained 5xx
  in healthy-transfer fixtures; no material small-host throughput regression
  (investigate median regression above 10%); no unbounded resource growth.
- [x] Run focused race tests, `make check`, and relevant container/mobile smokes.
  Record results and remaining limits; do not promise Internet-speed-test rates.
- [x] Update `docs/mobile-api.md`, `docs/mobile-api.yaml`, performance docs, and
  Compose env docs only for settings actually implemented. Give Astra the final
  capability, payload, response, retry, and fallback contract.

## S6 — Preserve the seed during release

- [x] Record the deployed version and a short read-only baseline immediately
  before release. Confirm all S0–S5 gates passed on the exact candidate image.
- [x] Prepare a reviewed release image and rollback image before cutover. Capture
  current queue/receipts/health and confirm free space for a verified checkpoint.
- [x] Coordinate a short pause in new app admission. Drain receivers/finalizers
  using existing graceful shutdown; checkpoint encrypted state. Never copy a
  changing catalog as an unverified backup or kill writers to speed deployment.
- [x] Preserve custom production Compose, mounts, settings, vaults, device grants,
  staged parts, receipts, and source mappings. No CPU-limit changes are implied.
- [ ] Coordinate owner unlock after restart; do not obtain/bypass owner credentials.
  Verify partial uploads resume and queued jobs finish without re-upload/reset.
- [ ] Compare short before/after samples using S0 metrics, including ongoing
  arrival rate. Check actual error classes instead of hiding 503 responses.
- [x] Roll back code if safe/compatible when integrity, authorization, or latency
  gates fail. Never restore an old checkpoint over new accepted data. Leave
  checkpoints and original staging alone unless separately approved for cleanup.

Release is complete when the seed continues, API contracts match documentation,
and measured production results distinguish fixed issues from remaining limits.

## Execution record

Fill this in as work completes; attach evidence rather than predictions.

| Phase | State | Commit / evidence | Remaining issue |
| --- | --- | --- | --- |
| S0 Diagnostics | Complete | receive diagnostics; focused tests | Live failure attribution after cutover |
| S1 Authorization | Complete | publication barrier; grant race tests | — |
| S2 Receive locks | Complete | bounded temp receivers; cancel/race/HTTP tests | — |
| S3 Batch status | Complete | API/schema tests; mobile-upload-status-batch.md | Astra must negotiate support |
| S4 Backpressure | Complete | durable marker admission; restart tests | — |
| S5 Validation/docs | Complete | full make check; exact-image container, renderer, mobile and HTTP smokes | — |
| S6 Production | Live; verification pending | 2508953; verified checkpoint; public/localhost readiness and browser | Owner unlock; ongoing seed/error sample |

### Implementation decisions

- Identical parts may receive concurrently within a bounded pool. Their commits
  compare receipts atomically under the session gate. This avoids an unbounded
  queue of duplicate body waiters and still permits exactly one durable counter
  update. Independent Live Photo components can progress concurrently.
- No status cache was added: small encrypted headers already give short reads once
  body I/O releases the session gate. Batch status reads each header once.
- No new environment overrides or event-stream transport. Capabilities carry the
  resolved receiver/backlog limits and the additive polling contract.
- Use sampled timings plus existing HTTP logs and bounded aggregate counters;
  latency percentiles and resource observations are computed by local QA fixtures.
  Forwarded-header presence never counts as proof of a trusted proxy route.
- New-session admission reads at most the configured pending-work ceiling from
  durable markers, without catalog reads or other session gates. Existing uploads
  bypass new-work limits. Oversized originals can be admitted alone.
- Lock order and lifecycle details: docs/mobile-upload-lock-order.md.

### Production deployment — October 9, 2026

- Runtime commit: `2508953cb554c648b1d1c9c829176fd1ec65abbd`; image
  `weazlcloud:release-2508953`, image ID
  `sha256:66ae67d86dfabe6b91da280ddd9170f9ea25f09c2df904a40ac44f7689c757b4`.
- Full `make check` passed. Exact-image renderer, Restic and shared container
  smokes, 250-MiB mobile fixtures on both backends at 2 CPU / 4 GiB, and
  stalled HTTP/disconnect-resume/batch-status smokes passed. Worst stalled-body
  status latency was 8.77 ms (Restic), 6.58 ms (shared).
- Production API and renderer drained with exit zero and no OOM. An XFS reflink
  checkpoint verified 68,754 file paths/sizes/modes/owners plus six canonical
  hashes before activation. Path:
  `/exports/dockervolume/weazlcloud-backups/upload-responsive-20261009/data`.
- Cutover completed at 21:15:34 UTC, 22.7 seconds after drain began. Both
  containers were healthy; mounts, limits, security, ports, settings, and
  environment matched the previous containers. Readiness passed on 7272–7274.
  Public TLS readiness, exact UI assets, and login browser smoke passed.
- Retained rollback image: `weazlcloud:release-f63cc3d4c58a`. Evidence under
  `/home/bobp/weazlcloud-releases/upload-responsive-20261009` on the host.
- Immediately before release: 84 successful part PUTs, no failed part PUTs,
  124 individual status GETs, 30 queue markers and 36 live markers over 120 s.
  The publication-rate matcher was incorrect; those old logs were replaced,
  so this sample cannot support a before/after publication speed claim.
- After graceful drain/restart: 26 queue markers and 31 live markers retained.
  The vault is relocked after restart. Owner unlock was requested; pending
  a real upload/resumption/error sample. No credentials fabricated, no queue
  reset, and no staging or checkpoint cleanup.
