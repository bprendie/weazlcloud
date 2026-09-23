# Multi-user dedupe integration workbook — September 23, 2026

Owner: Bob. Implementation: Luna.
Status: D0–D3 implementation and comparative benchmark complete on disposable fixtures. D4 integration remains experimental; D4.5 is partially implemented but its full lifecycle accounting gate is open. Shared storage and migration remain disabled for production.
Continues [September 22's workbook](phase_plan_2026-09-22.md). D0 started from `72d186f`.

## What we are building

Store identical content once across the household while keeping each user's library and access separate. If Alice and Bob upload the same ISO, each gets a private library entry, but the content occupies one shared set of encrypted objects. Renaming or replacing Alice's copy must not change Bob's copy.

Keep local accounts, private encrypted catalogs, vault passwords, stored node-key convenience, shared available disk, and the silent system reserve. The application administrator must not gain a file browser for other users. The server remains trusted to process plaintext; someone controlling the host can already decrypt through stored keys. This is not end-to-end encryption against the server.

**D0–D3 are complete; D4.5 and later gates remain. Do not merge Restic repositories or migrate real data.** The shared experimental backend now has content-defined chunks and a Restic comparison, but the measured fixture allocated about 6.7% more disk than Restic. Keep it experimental until workload-representative results and all later safety gates pass.

Execution order: D0 baseline → D1 storage interface → D2 sharing prototype → D3 chunks → D4 application integration → D5 migration tooling → D6 rollback/recovery → D7 rehearsal and handoff. Build D5 retirement tooling early, but keep it disabled outside disposable tests until D6 passes.

## Rules for Luna

1. Work one checkbox at a time. Each task below says what to build and how to prove it works. Prefix implementation commits with the task ID.
2. Preserve today's behavior until a task explicitly switches it. Read the listed files; older completed checkboxes are context, not proof.
3. Use disposable users and a separate workstation data volume. Preserve `weazlcloud-local-test-data-20260922`. Do not use real grabs or production files as fixtures. No 500-file benchmark or disk-filling test.
4. All persistent state and working files belong under the configured data directory, on its volume. Never depend on container `/tmp` capacity. Preserve the production mount `/exports/dockervolume/weazlcloud` and host Compose settings in the eventual rollout instructions.
5. Stream files and migration reads. Bound buffers, workers, manifest pages, and database batches independently of total file size. No whole-ISO `ReadAll`, `Get`, or in-memory ZIP.
6. Keep Go files below 300 lines. Edit frontend source in `mockup-ui/`. Run `make desk-assets` before direct Go commands; never commit generated UI, binaries, credentials, runtime files, or screenshots.
7. Run focused behavioral tests per task and `make check` at each phase exit. Run relevant integration smoke checks when connecting real routes. Documentation-only edits need link/path and diff checks, not runtime tests.
8. Keep filenames, plaintext hashes, keys, credentials, object capabilities, and grab URLs out of logs. Progress/status may report counts, byte totals, operation types, and safe error categories.
9. Use established encryption and chunking implementations. Do not invent a cipher or derive encryption keys from file hashes. Pin dependencies and keep the current `CGO_ENABLED=0` Docker build working.
10. Record evidence and unfinished work in this file. A green unit test is not evidence of a successful real-volume migration. This workbook specifies implementation and a future operator runbook; it does not execute a production deployment.

## Current code and where the changes belong

| Area | Current behavior / files | Required integration |
|---|---|---|
| User resources | `internal/users/paths.go`, `internal/filesvc/registry.go` create a vault, catalog, and Restic repository per user | Keep private vaults/catalogs; inject one node-wide storage service |
| Catalog | `internal/catalog/catalog.go`, `internal/catalog/mutations.go` store path, size, hash, snapshot, object, and Trash state | Stable entry IDs, mutation revisions, and versioned storage references |
| Writes | `internal/library/staging.go`, `internal/library/batch.go`, `internal/library/empty.go` publish Restic snapshots | Preserve durable staging and commit semantics behind a storage interface |
| Resumable upload | `internal/upload/`, `internal/desk/multi_upload.go` persist offsets and finalization | Keep 24-hour expiry and owner checks; make backend selection durable per commit |
| Reads | `internal/library/library.go`, `internal/library/prefix.go`, Desk and WebDAV handlers | Resolve both storage formats through the authenticated user's entry |
| ZIPs/previews | `internal/library/archive.go`, `internal/filesvc/archives.go`, `internal/library/thumbnail.go` capture/read Restic references | Capture immutable storage references and hold them while jobs use them |
| Grabs | `internal/desk/multi_capsules.go`, `internal/capsule/stream.go`, `internal/capsule/grab.go` produce independent encrypted payloads | Read either source backend; preserve existing sealed payloads and lifecycle |
| Cleanup | `internal/library/maintenance.go`, `internal/library/trash_intent.go`, `internal/filesvc/maintenance.go`, `internal/idle/` | Backend-aware cleanup and global ownership/hold checks |
| Space/reporting | `internal/quota/quota.go`, `internal/desk/multi_library.go`, `internal/library/repo_size.go` | Account for shared objects, both stores during migration, and temporary workspace |
| Recovery | `internal/recovery/kit.go`, `docs/recovery.md`, `scripts/recovery-smoke.sh` | Back up shared state consistently; user kits must never contain global secrets |

## Target design — implement these boundaries

### Ownership and encryption

- Private user catalogs retain filenames, folder structure, timestamps, Trash state, and each user's wrapped content-access keys.
- The shared store holds immutable encrypted objects under opaque IDs. The node's private index maps a server-keyed fingerprint of verified plaintext to those objects. Neither client-provided hashes nor object IDs authorize reads.
- Generate random object encryption keys. A node-only wrapping key protects the index's copies of those keys. The server can therefore reuse an existing object for a verified upload without opening another user's vault.
- Each owner receives access through a key wrapped by their own vault. Bind wrappers to owner, record identity, and format using authenticated metadata. Account passwords remain separate from content encryption keys.
- For the whole-file prototype, the wrapped key opens one encrypted file object. For the chunk format, it opens a bounded, paged encrypted file manifest containing chunk order, lengths, and chunk-access keys. Reused chunks keep their existing ciphertext and key; each private file manifest can have a fresh random key.
- Use standard authenticated encryption. Specify nonce rules, format versions, authenticated lengths/order, and final-stream authentication before writing the format. Detect corruption, truncation, repeated/reordered frames, and metadata substitution.
- Never expose the node wrapping key, fingerprint key, or another owner's wrappers in an API, user export, recovery kit, or log. Admin role checks must not bypass file ownership.

### Persistent records

Suggested new packages: `internal/storage/` for the interface and adapters, `internal/objectstore/` for shared storage, and `internal/migration/` for migration orchestration. These are planned paths, not existing code.

Use an embedded transactional database for the node-wide index and operation journal; SQLite with a pinned driver compatible with the existing static build is the default implementation choice. Treat database journals/WAL files as part of the protected data directory. Store encrypted key material and opaque IDs there, not private filenames or plaintext content fingerprints.

| Record | Minimum information |
|---|---|
| Catalog entry | Stable entry ID, mutation revision, existing path/metadata/Trash fields, storage kind/version, immutable content reference, owner-wrapped access key |
| Shared object | Opaque ID, keyed fingerprint, plaintext length, stored length, format, node-wrapped random key, write/ready/delete state |
| Ownership reference | Owner ID, stable catalog entry/version ID, manifest/object reference, pending/live/Trash state, operation ID |
| Hold | Operation/job ID, immutable object/manifest reference, owner, recovery/release state; pins content during reads, writes, ZIP creation, and migration |
| Operation journal | Idempotency ID, expected catalog revision, intended reference changes, durable stage, safe failure category |
| Migration item | Owner and entry/version IDs, original legacy reference, destination reference, expected hash/length, verified state, switch/retirement state |
| Node format marker | Schema version and minimum supported reader/writer versions; check before mutating data |

Reference counts are a derived optimization. Durable ownership records, operation intents, and active holds are the authority. Reconciliation must be able to detect and repair a bad count before anything is reclaimed.

### Durable commit sequence

The catalog file, database, and object files cannot be committed with one database transaction. Implement and test this sequence:

1. Authenticate the owner and unlocked vault. Persist an operation ID and expected entry revision; reserve workspace.
2. Stream and verify the content. Write/authenticate any new objects, sync files and containing directories, and keep them pinned by the operation.
3. Persist the prepared ownership reference, wrapped access key, and catalog-change intent. Protect both old and new content at this stage.
4. Under the user's mutation lock, publish the encrypted catalog change only if the expected revision and owner lifecycle state still match.
5. Mark the reference committed, remove superseded ownership references, and release temporary holds/reservations. Garbage collection happens separately.

On restart, reconcile each journal entry against the actual catalog revision before releasing either side. Unknown, unreadable, or inconsistent state must block deletion of affected objects; it must not be interpreted as zero references. Repeating an operation must not duplicate ownership or replace a newer successful save.

### Lifecycle rules to preserve

- Normal deletion moves the ownership reference into Trash for 30 days. Restore preserves content and checks path conflicts. Overwrite creates a new immutable version and releases the old reference after commit; it does not create user-visible version history.
- Account deletion blocks access, cancels/drains owned jobs, removes ownership records and wrappers, and removes owned legacy repositories and migration leftovers. It has no grace period. Bytes another user legitimately owns remain. Report unique-byte physical cleanup as pending until completed; do not promise immediate secure erasure of disk blocks.
- Existing grab capsules stay independent sealed payloads. Source files need a hold during minting, not for the capsule's entire lifetime after sealing. Preserve burn/revoke/expiry semantics; do not make old grab URLs depend on migrated source paths.
- On-demand ZIPs still expire after 90 minutes. Once a ZIP or grab payload is fully materialized, it no longer needs a hold on the original source. Its own payload retains its existing lifecycle.
- Foreground work and migration/cleanup must coordinate through the existing idle mechanism and storage holds. A background job must not call the foreground tracker in a way that cancels itself and waits on its own completion.
- Physical space remains the final quota guard. Keep the system reserve hidden. Do not assume dedupe will save space before the server has verified content and durable reuse.

## D0 — Establish the baseline and freeze the contract

- [x] **D0.1 — Inventory every storage caller.** `docs/dedupe-design.md` maps runtime Restic operations, the legacy whole-body route, plaintext stage/recovery files, batch snapshots, ZIP captures/artifacts, thumbnail cache, Trash intents, account bootstrap migration, account deletion, vault-secret paths, and sealed capsules to their next adapter/lifecycle treatment.
- [x] **D0.2 — Build the small fixture set and measure current storage.** `scripts/dedupe-fixtures.py` produces deterministic synthetic files and a SHA-256 manifest. `scripts/dedupe-baseline.sh` runs real two-user/admin Desk handlers with Restic 0.18.0 inside a disposable Docker volume. Hashes, logical bytes, per-user and total allocated/apparent repository space, read/write timing, Go allocation/heap/RSS, and workload wall time are recorded in `docs/dedupe-design.md`. The test verifies admin isolation, previews, Trash/replacement, empty entries, and shared batch snapshot identity.
- [x] **D0.3 — Write the exact format and transaction contract.** `docs/dedupe-design.md` defines versioned AES-GCM frames and nonce/AAD rules, keyed fingerprints, private owner wrappers, index/record layout, lock ordering, crash recovery, safe errors, and disabled-by-default migration. It selects `modernc.org/sqlite v1.59.0` and `github.com/restic/chunker v0.5.0` from their published package/upstream documentation; dependencies remain deferred until their implementation phases.

Phase exit: complete. `make check`, the final isolated Restic baseline, and a `CGO_ENABLED=0` Docker image build pass. No storage format is switched and no production data is touched.

### D0 evidence

- Design and caller inventory: [docs/dedupe-design.md](docs/dedupe-design.md).
- Reproducible fixture and isolated baseline: [scripts/dedupe-fixtures.py](scripts/dedupe-fixtures.py), [scripts/dedupe-baseline.sh](scripts/dedupe-baseline.sh), `internal/library/dedupe_baseline_test.go`, and `internal/library/dedupe_baseline_helpers_test.go`.
- Actual final disposable run: 2 ordinary accounts + 1 admin; Restic 0.18.0; 20,447,511 logical upload bytes; Alice/Bob each allocated 548,864 bytes after their first identical file; separate repositories do not share that content. Upload request-time sum 16,705 ms; verified read-time sum 6,864 ms; Go test-process HWM 157,832 KiB; 27.52 s workload. Exact fixture hashes and full allocation figures are in the design note.
- Limits: synthetic bounded workload only; the Restic child process RSS is not included in the Go test-process HWM. It does not predict household savings or prove production concurrency/migration behavior.
- Data safety: the harness removed its temporary volume; `weazlcloud-local-test-data-20260922` was preserved. No production host or volume was accessed.

## D1 — Put current Restic storage behind an interface

- [x] **D1.1 — Add stable catalog identities and versioned references.** Legacy catalogs gain random stable IDs, revision 1, and version-1 Restic references on first unlocked load. Original `Snap` and `Object` values remain intact, including batch object paths and empty legacy object fields; separate same-path Trash/live entries receive separate IDs. Replacement preserves ID and advances revision; move, Trash, and restore advance revisions; copy gets a new ID. Unknown or inconsistent references fail without catalog rewrite or in-memory publication.
- [x] **D1.2 — Implement the Restic adapter.** `internal/library.Backend` owns initialization, streamed writes/reads/ranges, batch writes, immutable-reference capture, holds, snapshot listing/pruning, and drain. All runtime Restic operations now go through its Restic implementation. ZIP manifests hold captured snapshots until success/cancel/failure, and Trash cleanup durably defers pruning while a snapshot is held. Previews, downloads, ranges, Desk/WebDAV uploads, resumable upload finalization, ZIPs, grabs, empty files, and Trash remain on the legacy backend.
- [x] **D1.3 — Prepare compatibility checks before shared writes.** Startup creates/checks `.weazl-storage.json` before listeners bind; `weazlcloud -check` validates it read-only. The current marker permits only `restic-legacy` format version 1. Unknown versions/modes and unsupported reader/writer requirements reject supported starts before catalog writes. Shared references/writes and migration remain unavailable. [docs/storage-format.md](docs/storage-format.md) explains the old-binary boundary: historic images do not inspect the marker and cannot be launched against a future changed volume.

Phase exit: complete. `make check`, the static Docker build, disposable container upload/download/preview/grab smoke, browser smoke, and filesystem restart/restore smoke pass. The default browser-smoke port was occupied by an existing local service, so the smoke passed on an alternate port without stopping that service. The recovery smoke now uses a short isolated idle interval and waits for its cleanup job. The D1 bridge is not yet a supported rollback target for future shared data; D4 and D6 must validate it first.

### D1 evidence

- Catalog compatibility: [internal/catalog/identity.go](internal/catalog/identity.go), [internal/catalog/reference_test.go](internal/catalog/reference_test.go), and [docs/storage-format.md](docs/storage-format.md). Tests cover stable reload, batch object preservation, two same-path identities, revision transitions, and fail-closed unknown references.
- Storage boundary and lifecycle: [internal/library/backend.go](internal/library/backend.go), [internal/library/restic_backend.go](internal/library/restic_backend.go), [internal/library/backend_test.go](internal/library/backend_test.go). The integration test checks byte-range output and proves an active ZIP-style hold defers Trash pruning until released.
- Startup gate: [internal/storageformat/marker.go](internal/storageformat/marker.go) is checked from `Start` and normal `Run` before listeners bind; `-check` does not create the marker. Future/unknown marker tests confirm it is not rewritten.
- Validation: `make check`; `docker build -f deploy/Dockerfile`; `WEAZLCLOUD_IMAGE=weazlcloud:d1-smoke make smoke-container`; `WEAZLCLOUD_BROWSER_PORT=22772 bash scripts/smoke-browser.sh`; `WEAZLCLOUD_RECOVERY_PORT=18272 bash scripts/recovery-smoke.sh`.
- Limits: Restic's range adapter bounds memory but must stream from the beginning through the end of the dump, so a high-offset media seek still costs I/O proportional to the offset and remaining stream. Holds are process-local D1 legacy protections; shared-store durable holds belong to later phases.

## D2 — Prove safe whole-file sharing on disposable data

- [x] **D2.1 — Implement the private index and key service.** Put shared state under a dedicated directory below the configured data root, with private file permissions and random opaque object IDs. Protect fingerprint/key material, backup dependencies, and schema upgrades. All read/grant methods require an authenticated owner reference or a journaled internal operation. **Pass:** a different user, admin role, known digest, copied reference, or guessed object ID cannot obtain plaintext or a usable key.
- [x] **D2.2 — Implement streaming encrypted objects and duplicate adoption.** Hash the actual bytes server-side; compare verified digest and length through the keyed index. Fully verify upload completion before exposing the owner's reference. Use uniqueness constraints and durable staging so simultaneous identical uploads converge on one ready object. **Pass:** two users obtain independent readable entries with one payload; truncated uploads and false client hashes create no readable entry; buffers stay bounded.
- [x] **D2.3 — Implement the durable commit sequence.** Journal the boundary between object/index changes and catalog save. Inject failure after each step and retry the same operation ID. **Pass:** no lost committed file, duplicate reference, premature reclamation, leaked reservation, or stale write winning over a newer committed version.
- [x] **D2.4 — Test key and ownership lifecycle.** Change account password and vault passphrase independently, lock/unlock, restart with stored keys, replace one user's file, and delete one owner's reference. **Pass:** the other user remains hash-identical; a personal kit has no global key; locking still denies ordinary access. Document that password rewrapping does not revoke copies of previously exported keys.

Phase exit: complete for the isolated prototype. Two logical 2,940,000-byte copies converge on one authenticated 2,940,144-byte ciphertext object. Cross-owner read isolation, password/vault key lifecycles, same-content concurrent writes, stale revision rejection, frame integrity, and recovery at each injected write/publication boundary pass in the disposable-volume harness. Keep this backend experimental; production migration still disabled.

### D2 evidence

- Prototype and format notes: [docs/shared-store-d2.md](docs/shared-store-d2.md); standalone implementation: [internal/sharedstore](internal/sharedstore); isolated harness: [scripts/sharedstore-smoke.sh](scripts/sharedstore-smoke.sh), run with `make smoke-sharedstore`.
- The harness uses a fresh named Docker volume for test files, SQLite, temporary storage, and Go caches, then removes it. No configured local test volume or production data was attached.
- Runtime integration, shared-object garbage collection, account deletion, multi-process coordination, backup restore, large-file benchmarks, and chunk dedupe remain later-phase work.

## D3 — Add chunk dedupe without unbounded memory

- [x] **D3.1 — Add content-defined chunking and versioned manifests.** Implemented with `restic/chunker v0.5.0`, 512 KiB minimum, 1 MiB average, 8 MiB maximum, persisted format settings, and private authenticated manifests. Bounded record parsing, cross-boundary range reads, malformed-manifest rejection, and legacy v1 reads are covered by tests.
- [x] **D3.2 — Reuse encrypted chunks safely.** Each chunk has random encryption material and authenticated compression; private per-file manifests reference deduplicated chunks. Tests cover identical content, changed-region reuse, concurrent cross-user writes, and retention until all owning manifests are released.
- [x] **D3.3 — Compare against Restic.** `scripts/dedupe-baseline.sh` ran D0 and D3 fixtures in a disposable Docker volume, checked readback hashes, and recorded physical allocation, metadata, timings, CPU, and memory. Detailed D3 numbers and limits are recorded in `docs/dedupe-design.md`. The shared store was 6.7% larger on this compressible small synthetic fixture, despite much lower measured CPU/time; therefore it is not recommended for cutover based on this result.

Phase exit: selected chunk format is tested and documented; no opaque custom crypto or whole-file-only regression hidden in the rollout.

### D4 implementation checkpoint — 2026-09-23

The mixed-backend integration is implemented behind `WEAZLCLOUD_STORAGE_BACKEND=shared-experimental`; the default remains `restic`. It includes durable per-upload backend/operation identity, mixed reads and owner-bound copy grants, archive holds, shared Trash/account cleanup, startup and idle reconciliation, claimed-object recovery, shared aggregate metrics, and conservative source-plus-destination upload reservations. D4 evidence includes browser and Docker smoke in both storage modes, Docker restart with resumable finalize, and shared-owner deletion/collector recovery tests. D3 is now implemented; D4.5 and subsequent migration/rollback/rehearsal gates remain. Do not enable this setting for production users.

## D4 — Integrate uploads, deletion, maintenance, and reporting

- [x] **D4.1 — Route normal writes through the new backend behind a setting.** Preserve direct PUT/WebDAV and the resumable upload API, 24-hour expiry, three browser rails, and last-successful-commit replacement. Persist the backend and operation ID when a finalize attempt begins; a retry cannot switch backends halfway through. Reconcile old queued Restic commits before changing an owner's write mode. **Evidence:** `make smoke-container` restarts a disposable Docker container with a partially uploaded file, verifies its offset and reservation survive, finalizes it once, and reads identical bytes in both storage modes. Focused unit coverage also rejects reuse of an operation ID with changed payload bytes.
- [x] **D4.2 — Connect reads, previews, ZIPs, and grabs in mixed mode.** One folder may contain both reference types. Capture owner-authorized immutable references and holds for queued work; moves or replacements cannot change its captured content. Cache previews by content/version and owner, not only path. **Evidence:** `TestSharedBackendMixedLibraryLifecycle` reopens the store and vaults, then verifies shared previews, range reads, mixed Restic/shared ZIP contents, replacement stability, and reads. The container smoke checks Desk and WebDAV reads, preview, and a sealed grab after restart in both storage modes.
- [x] **D4.3 — Connect G3 and G4 lifecycle work.** Update Trash cleanup to dispatch by backend and respect migration holds. Implement or reuse G4 disable/delete coordination before enabling shared writes for real users. Remove a deleted owner's references, key wrappers, pending operations, and source copies without deleting another owner's shared objects. Old cleanup intents must finish safely or be explicitly reconciled before source migration. **Evidence:** `TestDeleteSharedOwnerDrainsJobsAndPreservesOtherOwner` deletes one of two owners of the same object while that owner has an unfinished upload and archive job; deletion drains/removes those resources, and the surviving owner can still read and upload. Migration is a later phase and is not claimed as tested here.
- [x] **D4.4 — Add global collection and reconciliation.** Use the idle coordinator. Atomically claim unreferenced objects for deletion so a concurrent writer cannot adopt an object being removed. Protect prepared commits, live/Trash records, and job/migration holds. Reconcile interrupted deletes and orphan writes before collecting; do not expire a hold merely because a timer passed while its job is alive. **Evidence:** collector tests enforce holds and all owner rows as deletion blockers; a simulated stop after durable delete-claim is completed during reopen; owner reconciliation repairs a damaged owner state and removes stale references. There is no cached refcount to corrupt, so the test corrupts the authoritative owner state instead.
- [ ] **D4.5 — Integrate quota and useful measurements.** Implemented conservative reservations for source/destination coexistence, staging, encryption overhead, SQLite/index growth, and worst-case chunk manifests; resumable upload reservations include bounded metadata overhead. Shared metrics report logical bytes, unique plaintext chunk bytes, allocated object bytes, and manifest/index allocation. Still required: lifecycle-consistent accounting across duplicate/unique writes, replacement, Trash, and migration, including migration's temporary second copy; add measured reservation/reconstruction evidence for all paths. **Pass:** the complete lifecycle matrix reports consistent figures and no per-user allowance or visible reserve percentage appears.
- [x] **D4.6 — Protect privacy in reporting.** No cross-user hash-existence endpoint, duplicate-owner list, per-upload cross-user hit count, or hash-only instant upload. Expose aggregate storage savings without filenames or ownership links; document residual timing/free-space inference. **Evidence:** app-level test compares admin and ordinary-user quota responses, confirms equal aggregate values and stated scope, and checks that neither response contains a private filename. Shared reads remain catalog- and owner-authorized.

Phase exit: D3 complete; D4.5 remains open. `make check`, authenticated browser smoke, and disposable-container smoke pass in legacy and mixed modes, including WebDAV and restart checks. Keep shared storage experimental and disabled for production until D4.5, migration, rollback, and rehearsal gates pass. Other G5 cleanup jobs remain separate.

## D5 — Build the migration engine

Migration is a journaled copy-and-switch operation, not an in-place repository merge. Initially run one migration worker during idle periods. Foreground activity cancels/yields it; it resumes from durable state without resetting user upload offsets.

Each item follows this state machine:

`discovered -> source pinned -> copied -> destination verified -> catalog switched -> source eligible for retirement -> retired`

Failure records the last durable state and retries from there. Never infer completion from the presence of a destination filename.

- [ ] **D5.1 — Build a dry-run inventory.** Inventory every user catalog: live files, folders, Trash with original deletion dates, stable versions, snapshots, batch object paths, pending upload commits, and captured job references. Classify live files and unexpired Trash for migration, and expired Trash for normal journaled cleanup during an actual run; never reset its deadline. Inventory disabled owners but defer their migration; deleting owners follow G4 cleanup and cannot be reactivated by migration. Missing keys or unreadable catalogs block that owner's retirement. Account for all old snapshot dependencies, not just visible files. Report counts, allocated storage, conservative extra-space requirements, and blockers without private paths. Dry-run must not migrate, clean, change modes, or rewrite records, and must restore the original vault lock state. **Pass:** a shared batch snapshot and a live/trashed same-path pair are represented correctly; only the explicitly requested report is written.
- [ ] **D5.2 — Stream a pinned version to shared storage.** Use exact legacy snapshot/object references and the shared writer; bypass neither authentication nor quota. Existing stored-node unlock may be used internally without leaving a previously locked Desk vault unlocked. Verify streamed source hash/length against recorded metadata; missing/invalid metadata requires an explicit repair result, not a silent guess. Read back and authenticate the entire destination through the new reader and compare the full hash before switching. **Pass:** corruption or insufficient workspace leaves the legacy entry readable and unchanged.
- [ ] **D5.3 — Switch one catalog version atomically.** Recheck entry ID, revision, owner state, and Trash state while holding the mutation lock. Switch only that version's backend reference using the D2 journal. Preserve path, MIME metadata, mtime, deletion timestamp, and folder structure. If another operation changed it, abandon/reconcile the prepared reference and re-inventory; never recreate a deleted entry or overwrite a newer save. **Pass:** concurrent rename, restore, overwrite, and account deletion all resolve without lost updates.
- [ ] **D5.4 — Resume, pause, and reconcile.** Persist completed items and source holds. On restart reconcile legacy upload stages and shared-store intents before resuming migration or source pruning. Reuse verified work where safe; restart an incomplete bounded unit where necessary and report repeated bytes honestly. **Pass:** stop at every state above, restart, and converge to one correct logical version and ownership set.
- [ ] **D5.5 — Implement an explicit storage mode and operator controls.** Provide status, dry-run, start/resume, pause, verify, write-mode switch, and legacy-retirement operations. Initial UI may be a local maintenance command; it is an operator tool, not a required user-side helper. Status includes migrated/remaining/failed counts, actual bytes copied/reused, space blocked, and safe failure category. **Pass:** rerunning each command is safe; the user can continue reading mixed storage while copying pauses.
- [ ] **D5.6 — Retire legacy data by reference, not by owner completion percentage.** Before forgetting a snapshot, prove no catalog version, pending stage, job, cleanup intent, or migration rollback reference needs it. A batch snapshot cannot be pruned just because one file migrated. Only retire a repository after all its dependencies and recovery requirements are cleared. Process retryable forget/prune and report actual reclaimed allocation. **Pass:** killing retirement midway is recoverable and shared-snapshot live/newer-Trash fixtures survive.

Phase exit: a populated disposable legacy volume migrates successfully without reuploading files from the browser, resetting passwords, changing URLs, or resetting Trash clocks.

## D6 — Recovery and rollback that preserve new writes

- [ ] **D6.1 — Define the complete backup set.** Update `docs/recovery.md`: shared object files, encrypted manifests, database plus required WAL/journal state, node keys/fingerprint parameters, user catalogs/vaults/keys, migration/commit journals, surviving legacy repositories, and existing app/capsule/upload state. Take a stopped-node backup or a proven consistent snapshot across the set; copying an active database file alone is insufficient. **Pass:** restore onto a fresh disposable volume, unlock both users, verify full file hashes, create a new duplicate, and run cleanup safely.
- [ ] **D6.2 — Keep personal recovery owner-scoped.** Update kit format/compatibility as needed; never include global index secrets or node-wide wrapping keys. Document exactly which owner's catalogs, manifests, and payloads are also needed; a key kit is not a content backup. If the source account is deleted, explain the difference between surviving external backups and data intentionally removed from the live node. **Pass:** Alice's kit cannot unwrap Bob's unique file or grant new references; legacy kit behavior remains supported or explicitly version-rejected.
- [ ] **D6.3 — Implement operational rollback without data loss.** Stop migration/shared writes, drain in-flight writes, and run the bridge release in mixed-read mode with new writes directed to legacy storage. Keep already published shared references and new data readable. Do not restore a pre-migration catalog over current state. **Pass:** files created, changed, moved, deleted, and trashed after migration began retain their current state after rollback.
- [ ] **D6.4 — Implement full reverse migration before allowing source retirement.** Stream every current shared-backed live/Trash version into a suitable legacy repository, verify it, and atomically switch its reference using the same expected-revision rules. This includes new files written only to the shared backend. Preserve tombstones and deletion times. Stop safely if capacity is insufficient. **Pass:** all current files read through legacy references and shared state can be released only after verification; no deleted file is resurrected.
- [ ] **D6.5 — Document the old-binary boundary.** The supported rollback image is the D1 bridge, not an arbitrary earlier binary. A pre-bridge downgrade needs a separately implemented/tested schema conversion and full reverse migration; otherwise declare it unsupported. Restore from a pre-upgrade backup only with an explicit acknowledgment that later writes would be lost. **Pass:** runbook has no step that swaps an old image onto mixed storage and hopes it works.

Phase exit: forward migration, bridge rollback, reverse migration, and full-volume recovery all pass locally. Source retirement stays disabled until these checks pass.

## D7 — Rehearse, then prepare the production runbook

- [ ] **D7.1 — Rehearse mixed use and failures.** Run D0 fixtures through both users while uploading, reading from WebDAV, previewing, generating a ZIP, minting a disposable grab, and performing idle migration. Inject bounded write/permission/index failures and process exits; never fill the host disk. Check the matrix below and record memory, timings, hashes, allocation, and restart boundaries. **Pass:** every row has actual evidence and remaining limits are stated.
- [ ] **D7.2 — Write exact operator commands.** After implementing the controls, add tested commands to `docs/dedupe-migration.md`. Distinguish dry-run, write-mode switch, rollback, and destructive legacy retirement. Specify image tags/digests, volume paths, backup/restore checks, disk requirements, and expected status output. No placeholder command should appear to be executable. **Pass:** Luna can rehearse the runbook from a fresh terminal with disposable data.
- [ ] **D7.3 — Prepare the deployment handoff.** Record the measured go/no-go result, supported rollback version, required free space, key-backup verification, and unresolved failures. Preserve host Compose edits and data mounts. Future production execution is a separate deployment task. **Pass:** a concrete release/runbook is ready; this checkbox does not claim production has migrated.

## Migration runbook order

| Step | Action | Completion check |
|---|---|---|
| 1 | Inventory installed image/schema and volume; create and verify a consistent recovery copy with the operator's backup process | Restore test succeeds; no reliance on a personal key kit alone |
| 2 | Deploy the bridge reader with legacy writes and migration off | Existing login, unlock, library, upload, WebDAV, preview, ZIP, and grab behavior passes |
| 3 | Run migration dry-run and space preflight | Every required source/key/reference is accounted for; insufficient space pauses safely |
| 4 | Enable shared writes only after D4 and D6 pass; persist that mode | New writes survive retry/restart; old files remain readable |
| 5 | Run idle copy/verify/switch batches | Each switched file has full destination readback verification; foreground work continues |
| 6 | Briefly drain mutations and jobs; reconcile a final inventory, then resume normal service | No undiscovered required legacy reference, pending publication, or ambiguous cleanup state |
| 7 | Take/verify a consistent recovery point containing shared state; rehearse rollback against a copy | Shared keys, index, catalogs, and payloads recover together; new writes are preserved |
| 8 | Explicitly retire eligible legacy snapshots/repositories | Reference proof passes; measured reclaimed bytes and remaining legacy dependencies are reported |

Keep legacy source data until verified switching and retirement checks finish. That temporary coexistence is migration workspace, not a new user-facing retention policy. A deleting account's source/rollback copies follow account deletion and cannot be retained merely to make migration rollback easier. Do not assume the entire source and destination will fit; preflight conservatively, pause on insufficient headroom, and never reclaim an unverified source to force progress.

## Required evidence matrix

| Test | Expected result |
|---|---|
| Two users upload identical content, simultaneously and separately | One unique payload/chunk set after reconciliation; both read identical hashes |
| One user knows another object's ID or hash | No read, key grant, or owner lookup without an authorized catalog reference |
| Admin session attempts another user's read | Denied |
| Unique file, repeated chunks, slightly changed disk image | Correct output; measured whole-file and chunk reuse separated from compression |
| Large generated stream with bounded test size | Memory bounded by workers/chunks/pages, not file length; limits stated |
| Empty file, empty folder, missing/unknown storage format | Valid empty entries work; unsupported format causes no destructive rewrite |
| Password change, vault rekey, lock/unlock, restart | Correct access; other users' data and private keys unaffected |
| Replace/delete Alice's shared file | Bob's entry and bytes unchanged |
| Trash expiry/restore around migration | Original 30-day deadline retained; restore conflicts remain safe |
| Account disable/delete during migration or ZIP | Access blocked, jobs drained, no owner resurrection; other owners remain intact |
| Live and trashed files share a Restic batch snapshot | Source is not pruned while any required reference remains |
| Failure after object write / prepared index / catalog save / final reference commit | Restart resolves intent without data loss or duplicate ownership |
| Corrupt destination, unreadable catalog, or unavailable key | Source preserved; affected retirement blocked and reported |
| Foreground work resumes during idle cleanup/migration | Background work yields without self-deadlock or losing holds |
| Low-space or read-only volume injection | Clear pause/error; no deletion of unverified source; reservations reconcile |
| Mixed-format ZIP, preview, WebDAV read, and existing grab | Correct bytes and unchanged ownership/lifecycle |
| Rollback after new writes and deletions | Current state preserved; no stale catalog restore or resurrected file |
| Shared-store volume restore onto a fresh node | Both owners' hashes verify; duplicate adoption and safe cleanup still work |

## Relationship to the September 22 workbook

- G2 remains deliberately skipped; this plan does not resume mounted-drive performance work.
- Reuse G1 uploads, G3 Trash semantics, and the completed G5.1–G5.3 idle cleanup/status infrastructure. Adapt storage references and cleanup boundaries; do not silently replace their policies.
- G4 account lifecycle must understand shared ownership before shared writes go live. D4.3 depends on delivering the relevant G4 controls and worker cancellation, not merely adding a refcount decrement.
- Capsule, ZIP, preview, Trash, and upload cleanup policies are implemented in G5.2; status persistence and the admin-only read API are implemented in G5.3. Dedupe integration must preserve these lifecycle boundaries.
- G6 photo rendering and G7 Takeout are separate workstreams. Their future reads/writes must use the storage interface.

## Evidence to record after each task

```text
Date / task ID:
Commit:
Behavior changed:
Files changed:
Commands and disposable fixture used:
Observed hashes, space, memory, and failure/restart results:
Known limits or incomplete acceptance checks:
Next task:
```

## Reference material

- [Original multi-user plan](mu_workplan.md): private catalogs, shared encrypted content, no cross-user existence API.
- [Restic 0.18 storage design](https://restic.readthedocs.io/en/v0.18.0/100_references.html): chunks, repository encryption, snapshots, and locking. Its repository passwords unwrap shared master keys; they are not user compartments. This is why the target is not one repository with a password per user.
- [Current recovery instructions](docs/recovery.md): operational volume backup and restore, distinct from a personal recovery kit.

Planning log: Written for September 23, 2026, from code at `74aa734`. This change creates the integration/migration workbook only. Every implementation checkbox is intentionally open.
