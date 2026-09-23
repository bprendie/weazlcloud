# D0 storage inventory, baseline, and shared-store contract

Date: September 23, 2026
Status: D0 implementation evidence; shared storage is not enabled.
Scope: synthetic local tests only. No production volume or user files were used.

## D0.1 — Current storage caller inventory

All application calls to Restic are routed through `internal/restic/ops.go`; each wrapper shells out to the pinned `restic/restic:0.18.0` binary and uses repository version 2. There is no cross-user Restic repository today. `filesvc.Registry` creates one `library.Library` per account, rooted at that account's `library/` directory.

| Caller | Current behavior | D1/shared-store destination |
|---|---|---|
| `internal/library/library.go:Ensure` → `internal/restic/ops.go:Init` | Initializes each user's private repository after the vault unlocks. | Restic adapter in D1; keep as the legacy backend after D1. |
| `internal/library/staging.go:commitStaged` → `Put` | `PutReader` streams request bytes into plaintext `.staging/*.data`, records a JSON recovery journal, then creates a one-file snapshot. The journal records path, SHA-256, size, and snapshot/object IDs. | Preserve and recover legacy stages before changing backend. D4 must durably bind each upload finalization to one backend and operation ID. |
| `internal/library/batch.go:commitBatch` → `PutBatch` | Concurrent writes are grouped into a temporary `.weazl-batch-*` tree and one Restic snapshot; each catalog row points into that shared snapshot. Recovery looks for the staging journal's `BatchRoot`. | Preserve exact snapshot/object references. A snapshot cannot be retired while any file, Trash record, pending stage, or job still references it. |
| `internal/library/empty.go` → `PutBatch` | Stores an empty file via a temporary batch tree because a zero-byte stdin backup is not used. | Preserve as a legacy-specific write path behind the adapter. |
| `internal/library/library.go:Get` → `Dump` | Restores a complete file into `bytes.Buffer`; memory grows with file size. | Replace callers with streaming shared reads; do not route large objects through this API. |
| `internal/library/library.go:StreamTo` → `Dump` | Streams a file into the caller's writer; used by downloads and browser-native previews/ranges. | Shared streaming reader. Authorize through the owner's catalog before resolving a storage reference. |
| `internal/library/prefix.go` → `Dump` | Restores only a bounded prefix for WebDAV type detection. | Shared bounded-prefix reader. |
| `internal/library/archive.go:WriteArchive` → `Dump` | Streams captured files into a ZIP; `ArchiveManifest` keeps immutable Restic references in memory for the job. | Shared reader plus durable holds/reference capture before D4 ZIP integration. |
| `internal/library/maintenance.go:CleanupTrash` → `Snapshots` | Finds snapshots that might be solely owned by expired Trash records and protects snapshots referenced elsewhere in the catalog. | Backend-aware owner-reference cleanup; a shared object is collected only after all owners, Trash, operations, and holds release it. |
| `internal/library/trash_intent.go:resumeTrashCleanup` → `Snapshots`, `Forget --prune` | Persists an AES-GCM-encrypted per-user cleanup intent before pruning. Retry compares intended IDs with extant snapshots, then removes the intent. | Keep this intent recoverable during migration. D4 must reconcile it before retiring a legacy repository. |
| `internal/desk/library.go:putLibrary` | Legacy/single-user route reads the entire request body with `io.ReadAll`, then calls `Library.Put`; memory grows with upload size. | Keep only as a legacy route until changed to `PutReader`; D1 must not hide this behavior. |

The multi-account PUT path calls `PutReader`, and resumable finalization in `internal/desk/multi_upload.go` delegates completed session streams into the user's library. `internal/upload/` separately persists owner-scoped upload sessions, offsets, parts, and recovery state; it has no direct Restic calls. A new shared backend must preserve owner/session checks, 24-hour idle expiry, and exactly-once finalization.

`internal/filesvc/archives.go` stores finished ZIP payloads below the configured data directory and expires them after 90 minutes. Its job map and captured manifest are process memory, not a restartable manifest. Treat surviving ready ZIPs and in-progress archive jobs as D4 migration/read holds; do not infer that an absent in-memory job means its source reference was safely switched.

`internal/library/thumbnail.go` maintains encrypted per-user preview cache entries. `internal/capsule/` materializes independent sealed grab payloads; after sealing, a capsule no longer depends on its source file. D4 must preserve those ownership/lifecycle boundaries.

The current `Library.Dedupe` in `internal/library/maintenance.go` compares whole-file hashes only inside one user's catalog. It is a display statistic, not global physical dedupe and not Restic's block/chunk reuse.

Per-user paths are defined in `internal/users/paths.go`: `vault.json`, `node.key`, `catalog.enc`, `places.json`, and `library/`. `internal/vault/vault.go:Secrets` returns the unlocked user's Restic and drive secrets; library writes, reads, archive reads, prefix reads, and Trash pruning request the Restic secret. Catalog and Trash-intent protection use the vault wrapping key. The key derivation and AES-GCM wrapping implementation live in `internal/cryptox/` and `internal/vault/`.

Bootstrap in `internal/desk/multi_account.go` can rename the legacy single-user vault, node key, catalog, places, and entire Restic repository into the first administrator's account. This is an ownership migration path, not a shared-store adapter. Account deletion in `internal/accountlifecycle/manager.go` blocks/drains the account, removes its uploads and capsules, then `os.RemoveAll`s that user's data root, including its private repository. D4 must remove only that owner's shared references and wrappers; it must not delete bytes another owner still references.

There is no direct use of `Restic.Dump`, `Put`, `PutBatch`, `Forget`, `Snapshots`, or `Init` outside the call sites listed above and the Restic wrapper. The test-only Restic calls are excluded from runtime routing.

## D0.2 — Reproducible synthetic baseline

Run `scripts/dedupe-baseline.sh`. It creates a uniquely named Docker volume, creates all fixture, application, Restic, temporary, and Go-cache data inside that volume, runs `TestDedupeBaseline` against Restic 0.18.0, prints its evidence, and removes the volume on exit. It does not attach the configured workstation or production data volume. The test stands up the actual multi-user Desk handlers with `httptest`; it creates two ordinary users and one admin, forges separate vaults, logs in/unlocks each account, and performs HTTP uploads, reads, previews, Trash, empty-folder, and concurrent batch operations.

The Python fixture generator is deterministic and writes a SHA-256 manifest checked by the test before any upload. All names and bytes are synthetic. The disk-image-shaped fixtures are 8 MiB each; the second copies the first and changes one 64 KiB region at its midpoint. The total payload is deliberately small; this is a functional/storage baseline, not a large-file stress test or household-savings forecast.

| Fixture path | Bytes | Content |
|---|---:|---|
| Fixture path | Bytes | SHA-256 | Content |
|---|---:|---|---|
| `shared/identical.bin` | 524,288 | `7b07ee4c38d6e31ec3231cce5e813f9872dd103cd1b9bc6aa1dc939d67ac13a5` | Deterministic synthetic bytes uploaded once by each user. |
| `unique/alice.bin` | 262,144 | `8c62abb102783becde0061c7ca39259297a985adb3a7e8302443131feab2da63` | Deterministic Alice-only bytes. |
| `unique/bob.bin` | 262,144 | `de112f25611ba3d155abc2cf7565615d7749794b6e01350e11c93e63ac80ab5c` | Different deterministic Bob-only bytes. |
| `repeated/repeated-chunks.bin` | 2,097,152 | `b504614ab2a92c44224e01472e2227b5d84bd61728e0c604431075ee153045f1` | Eight copies of the same deterministic 256 KiB block. |
| `previews/sample.svg` | 115 | `ce749dd24cf47e5496b61e36c6ae5ebc8202caa5d6202726b5e9eada369f7825` | Small synthetic SVG image preview. |
| `previews/notes.md` | 50 | `e357228d1e53d6c5d30f833f8466dc3e9d486c7ab03ce64d7ebb8c5edec50d6d` | Synthetic Markdown document preview. |
| `empty/zero.bin` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | Empty regular file. |
| `empty-folder/` | — | — | Empty directory created through the library folder API. |
| `disk/disk-image-v1.img` | 8,388,608 | `fb36956e86b0879350d26548ff8701e40617cf55fc56d9911488db66eb84232c` | Deterministic disk-image-shaped base. |
| `disk/disk-image-v2.img` | 8,388,608 | `6a28a911669a9c5dd077aa48ef62d3f048b31a98ce48cf5af49ffa4b96416049` | Same base with a deterministic 64 KiB midpoint edit. |
| `versions/same-path-v1.txt` | 16 | `10756264e18291683afa11931426ccfd8d75a1c9bea7fba7a61b5d014e6dbaf1` | First version of a synthetic path. |
| `versions/same-path-v2.txt` | 25 | `d74b79f55e4d9358e000ca3211f8be68cad0352cdda5caf88acc65da957010ec` | Re-upload at the same path after the first version is trashed. |
| `batch/one.txt` | 36 | `e6cb47590b64c116a3a18bbb30ed9be9303990c45fd53adae7d2808c65cbbbfa` | Synthetic file concurrently uploaded with `batch/two.txt`. |
| `batch/two.txt` | 37 | `9ed7f936014d0e281c3124f956ef57c5250977005c02b8ddffb3d7f7ab1674e2` | Distinct synthetic file; verifies both catalog entries reference the same Restic batch snapshot. |

Every run prints `D0_FIXTURE` rows with the exact SHA-256 values generated and verified for this fixture set. The baseline also verifies download hashes for both users, unique files, repeated content, the changed disk image, empty file, same-path replacement, and both batch files; it checks SVG/Markdown previews and confirms the admin cannot read an ordinary user's library. These hashes identify only committed synthetic fixtures.

### Recorded run

Environment: fresh disposable Docker volume; `golang:1.25-bookworm`; `restic/restic:0.18.0`; Linux/amd64; 2 GiB cgroup limit. Restic reports its own build with Go 1.24.1. The HTTP workload ran in one Go test process; timings sum per-request elapsed times. Allocated bytes use filesystem block counts, apparent bytes use file sizes.

| Measurement | Observed |
|---|---:|
| Ordinary accounts + admin | 2 + 1 |
| Logical upload bytes | 20,447,511 |
| Alice repository allocated / apparent | 12,079,104 / 11,915,231 bytes |
| Bob repository allocated / apparent | 827,392 / 789,290 bytes |
| Total application data allocated / apparent | 12,955,648 / 12,714,498 bytes |
| Alice repository after one copy | 548,864 allocated bytes |
| Bob repository after one copy | 548,864 allocated bytes |
| Sum of upload request times | 16,705 ms |
| Sum of verified read request times | 6,864 ms |
| Go process cumulative allocation delta / heap at end | 73,747,072 / 69,218,528 bytes |
| Go test process high-water RSS | 157,832 KiB |
| Test workload wall time | 27.52 s |

Interpretation: identical bytes uploaded to separate per-user Restic repositories are stored independently; each initial repository allocation was 548,864 bytes for the shared fixture. Alice's total includes the repeated-block and two 8 MiB disk-image-shaped files. Restic's repository allocation is compressed/encrypted/chunked data and metadata, so the totals are not plaintext size. Go RSS excludes the separate Restic subprocess RSS; this is not a peak-memory bound for production concurrency. Request-time sums include in-process HTTP overhead. Synthetic data does not predict photo, ISO, or household savings.

## D0.3 — Shared-store format and transaction contract

This section freezes implementation defaults for D1–D5; it does not switch runtime storage or modify `go.mod`.

### Selected components and format marker

- Index: `modernc.org/sqlite v1.59.0`, an embedded SQLite driver that supports CGo-free builds. Keep `CGO_ENABLED=0`; add the exact module version and sums only when D2 implements the index. Sources: [versioned Go package documentation](https://pkg.go.dev/modernc.org/sqlite@v1.59.0) and [upstream project](https://gitlab.com/cznic/sqlite).
- Chunking: `github.com/restic/chunker v0.5.0`, using Rabin content-defined chunking. Initial parameters: minimum 512 KiB, target/average about 1 MiB, maximum 8 MiB, then a possibly smaller final chunk. Persist the algorithm/version, polynomial, and every parameter in the node format marker; never infer them from the current binary. Add the exact module version and sums in D3. Sources: [versioned package documentation](https://pkg.go.dev/github.com/restic/chunker@v0.5.0) and [upstream project](https://github.com/restic/chunker).
- Prototype object format: version 1, immutable encrypted objects; no plaintext content-addressed filenames. Node mode-0600 key material includes independent random fingerprint and wrapping keys. Use a random 192-bit opaque object ID and a fresh random 256-bit AES key per physical object. Do not derive encryption keys or nonces from content hashes.
- Cipher: AES-256-GCM via Go's standard `crypto/cipher` AEAD, consistent with the existing AES-GCM vault wrapping. Do not implement a cipher. Stream each object in 1 MiB plaintext frames. The frame counter is an unsigned 64-bit big-endian integer; a random 32-bit per-object nonce prefix plus the counter forms the 96-bit GCM nonce. A key is never reused for another object. Reject counter wrap.
- Exact frame contract: a fixed-width header carries magic, format version, opaque object ID, frame size, and nonce prefix. Each record carries a kind byte, sequence number, plaintext length, ciphertext, and 16-byte GCM tag. The AAD is the complete header followed by that record's kind, sequence, and length in fixed big-endian encoding. Data records must start at sequence zero, be contiguous, and be no larger than 1 MiB. The authenticated final record has the next sequence and a 16-byte AAD payload containing total plaintext length and data-frame count; its encrypted plaintext is empty. Readers check the tag before releasing any frame's plaintext, require exactly one valid final record, validate totals/order/IDs, and reject truncation, reordering, duplication, unknown versions, oversize lengths, or trailing bytes. Empty content is a valid zero-data-frame object with an authenticated final record. A response may have sent earlier authenticated frames before later corruption is detected; never label a failed stream successful or publish an unauthenticated frame.
- Fingerprints: use HMAC-SHA-256 under the random node fingerprint key, with a distinct domain string and explicit format/chunker version and plaintext length. D2 uses a whole-file domain; D3 uses a chunk domain. Compute over server-read bytes; a client hash is only an untrusted hint. Keep the existing plaintext SHA-256 only in that owner's encrypted catalog when compatibility requires it. Never put raw hashes, paths, usernames, or MIME metadata in the shared index.
- Key wrapping: the node wrapping key encrypts each random object key for internal indexing/adoption. A private owner catalog contains a separate AES-GCM-wrapped access key/manifest key bound as AAD to format version, owner ID, stable entry ID, and revision. D2 wraps the whole-object key per owner. D3 wraps a fresh per-file manifest key; the encrypted manifest lists ordered opaque chunk IDs, lengths, offsets, and object keys. Reused chunks keep ciphertext/key while each user's manifest and wrapper remain private. Password changes rewrap vault material; they do not rotate the node key or revoke exported copies.

SQLite lives under `<configured-data-dir>/shared-index/`; objects and staging live under sibling directories on the same configured volume. Directories are 0700 and files 0600. Enable foreign keys, WAL, `synchronous=FULL`, a bounded busy timeout, and a small bounded connection pool. Backups must include the database and its required WAL state. Tables hold the format marker; opaque objects and keyed fingerprints; node-wrapped keys; owner/entry/version references and owner-wrapped manifest keys; durable operations; holds; and migration state. Paths, filenames, content hashes, and user-visible metadata remain in encrypted per-user catalogs. Only an explicit `ready` object is readable/adoptable.

### Ownership and authority

An owner reference is valid only when the authenticated account's decrypted catalog contains the stable entry ID, revision, storage kind, and immutable object/manifest reference, and the owner wrapper authenticates for that exact tuple. The shared index never grants access by object ID, fingerprint, admin role, or a client-supplied digest. Resolve/authorize through the catalog before opening shared bytes. Admin endpoints expose no ordinary user's catalog, wrappers, object membership, or per-upload duplicate hit. The server/host remains trusted and can access node key material; this is not server-blind or end-to-end encryption.

Each future catalog entry needs a random stable entry ID and monotonically increasing revision. A same-path trashed row and a live replacement are separate identities. Rename/move changes catalog metadata and revision without changing immutable content; overwrite creates a new content reference and retires the old one only after commit. Reference counts are cached/derived data, never authority for deletion.

### Lock order and bounded work

1. Account lifecycle gate: prevent new owner work once disabling/deleting begins; drain existing jobs before removing that owner's data.
2. Per-owner catalog mutation lock: validate owner state, entry ID, and expected revision; serialize path/catalog mutations.
3. Acquire durable operation/object holds under the short storage admission/reference lock. Sort multiple opaque object IDs lexicographically when locking them.
4. Use short SQLite transactions only for journal/state transitions. Never hold a database transaction or global object lock while reading an upload, encrypting frames, serving a stream, or invoking Restic.
5. Publish the encrypted owner catalog atomically and durably; then finalize reference state. Release locks/holds after recovery state is durable.

Use bounded streaming workers, frame buffers, manifest pages, and database batches independent of total file size. Temporary plaintext, encrypted candidates, database/WAL, cache, and ZIP bytes must stay below the configured data directory on its volume; never rely on container `/tmp`. Foreground activity may pause idle migration/collection but must not self-cancel a worker that is waiting for itself.

### Durable write and crash recovery

Write order:

1. Authenticate owner and unlocked vault; acquire lifecycle/mutation checks; create an idempotent operation ID with expected entry revision and reserve worst-case workspace.
2. Stream to durable staging on the configured volume while computing actual length and keyed fingerprint. Verify expected upload length and any legacy catalog hash. Do not trust the client fingerprint.
3. For a new object, write authenticated encrypted frames to a same-volume temporary file, sync it, atomically rename to its opaque immutable path, then sync the containing directory. Verify the completed stream before marking the object ready. If an equal ready object already exists, verify its format/state and adopt it; discard the candidate only after the durable operation pins the ready object.
4. In a short DB transaction, record the prepared object/reference, owner wrapper, expected revision, and operation state. Keep the prior reference live and the new reference held.
5. Recheck account lifecycle and catalog revision under the owner lock. Atomically write/fsync the encrypted catalog with the operation ID, new immutable reference, and new revision.
6. In a short DB transaction, mark the new reference live, retire/switch the old owner reference as appropriate, mark the operation committed, and release reservation/temporary holds. Physical collection is a separate reconciled operation.

| Crash/failure point | Required recovery action |
|---|---|
| Before durable operation/staging | No published reference. Remove only provably unreferenced partial temp data; retry as a new operation. |
| During source read or encrypted temp write | Keep old catalog/reference intact. Mark or discover incomplete temp by operation ID; resume only if the staged source and journal validate, otherwise delete that temp after proving no hold/reference. |
| After object rename/fsync but before DB ready | Treat as an orphan candidate, never as readable. Reconcile journal and fingerprint; adopt only through a new durable operation after full authentication, otherwise collect after a complete scan. |
| After DB prepared reference, before catalog publication | Keep old reference live and new object held. Compare journal's expected revision/operation ID with catalog. Resume if it is still the expected mutation; otherwise abort prepared reference and release only its holds. |
| Catalog atomically published, before final DB commit | Catalog operation ID/revision proves intended publication. Finalize DB reference idempotently; do not restore an older catalog or release old data early. |
| DB commit complete, before source/old-object cleanup | New catalog remains authoritative. Resume separately journaled cleanup; old bytes may temporarily remain. |
| During read, bad tag/truncation/reorder/unknown version | Stop the read with a safe I/O/corruption error; never send the failed frame's plaintext. Do not mutate owner references or auto-delete the object. |
| Database/WAL unavailable, malformed catalog, unknown reference state | Fail closed for affected writes/collection. Preserve source and objects; report a safe category, not paths, hashes, keys, or credentials. |
| Simultaneous equal uploads | Unique keyed fingerprint plus a short transaction/admission lock converges on one ready object. Each operation retains independent owner authorization and journal. Never expose whether another user already had it. |
| Account deletion races with write/migration/job | Lifecycle gate prevents new commits, drains/cancels jobs, then removes that owner's references/wrappers. Shared bytes remain while any other durable owner/Trash/hold references them. |

Unknown, incomplete, or corrupt state is not equivalent to zero references. Reconciliation blocks collection until every affected operation, owner catalog, reference, and hold is understood. Logs and API errors contain only safe categories and operation IDs designed not to encode owner/path/hash.

### Migration and rollout defaults

Migration and shared writes are disabled by default in every release until their specific D4–D7 gates pass. D1 keeps current Restic repositories readable and writable behind an adapter. D2/D3 remain disposable experimental backends. Migration is a journaled copy, full destination readback/authentication, catalog switch, then later source retirement; it is never an in-place merge of Restic repositories. Preserve legacy Trash cleanup intents, upload stages, batch snapshots, ZIP captures, and account lifecycle work until explicitly reconciled. Do not switch a real user or production volume in D0.

Space preflight includes legacy plus shared coexistence, upload source/staging, encrypted candidates, ZIP/capsule work, manifests, SQLite WAL, and the silent system reserve. Do not assume available disk will stay constant or dedupe will save space. D0 changes no storage format, runtime setting, dependency file, or production configuration.
