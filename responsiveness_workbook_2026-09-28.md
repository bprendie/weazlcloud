# Responsiveness remediation workbook — September 28, 2026

Owner: Bob. Implementer: Luna. Status: implemented subset deployed September 28;
remaining gates below stay open. Production settings and vaults were preserved.
The owner subsequently authorized cleanup of verified landed ZIPs; see R7 release evidence.

## Outcome

Signing in should open a usable application promptly. Opening a folder should
fetch that folder, not the whole vault. Big machines should retain useful work
in RAM and prepare derivatives faster; smaller machines keep the same features
with less caching and fewer workers. Hardware acceleration is an optimization,
never a requirement for opening an existing vault.

Read `weazl_ethos.md`, `docs/login-latency-trace-2026-09-28.md` and
`photos_resource_workbook_2026-09-27.md` first. The ethos stays implicit in the UI:
brief loading states and useful actions, without explanatory slogans.

## Evidence and priorities

Production's loopback trace measured authentication plus unlock at about 0.17 s,
library listing at 2.2 s and quota at 4.2 s. The deployed screen waits for these
serially. Listing returns 96,971 entries in 11,696,193 bytes. Catalog loading is
repeated once for listing and twice for quota's dedupe/Trash calculations.
These are endpoint measurements, not CPU profiles or WAN/browser timings.

Default Home still waits for this work in the local checkout. The newer Photos
page already has bounded paging and preview caching; reuse its ideas, not a
second independent scheduler. Avoid spending CPU to redo work that can be omitted.

Ship R0–R2 as the first login remediation, after their gates pass. R3–R4 address
large-library navigation and RAM reuse. R5–R6 optimize background throughput and
CPU instructions. R7 validates any proposed release. Do not make SIMD experiments
a prerequisite for the login fix.

## Rules for every phase

- Keep password hashing, authentication, encryption and authorization intact.
  Never reduce Argon2 cost to improve the measured login delay.
- Keep originals streaming and bounded, including ISOs. No whole-file RAM cache
  for arbitrary originals, ZIPs or large media. No plaintext metadata disk index.
- Isolate caches by owner and vault session. Lock, rekey, revoke, delete and
  account switching must invalidate responses and cancel/drain relevant work.
- Serialize durable catalog commits; publish new cache generations only after
  successful commits. A cache is not the authority for permissions or disk quota.
- Test Restic and shared storage. Do not migrate storage formats in these phases.
- Keep Go files below 300 lines. Each phase records changed files, test commands,
  results, limitations and rollback steps before its checkboxes are marked done.
- Do not contact or change production just to implement or benchmark the plan.
  A later authorized rollout may collect sanitized host capabilities and timings.

## R0 — establish useful measurements

Start in: `internal/desk/multi_account.go`, `multi_library.go`, `library.go`,
`internal/library/library.go`, `internal/catalog/catalog.go`, `mockup-ui/app.js`.

- [ ] Add opt-in local timing for authentication, unlock, catalog load/decrypt/
  parse, index construction, response encoding, quota, queue wait and rendering.
  Do not include credentials, cookies, file names, paths or content hashes.
- [ ] Record browser submit, unlock response, visible app, first folder page and
  first thumbnail times. Record long main-thread tasks and response sizes.
- [ ] Capture Go CPU/heap/allocations/mutex/block profiles locally. Include
  Restic child RSS, container working memory, I/O wait and GC in measurements.
  Keep profiling endpoints disabled by default and off public interfaces.
- [ ] Use the existing smoke fixtures for correctness. For scale, generate an
  opt-in local metadata-only fixture near 97,000 entries, including deep folders,
  mixed MIME types and repeated hashes. Do not import hundreds of new real files
  or copy production content. State that metadata simulation omits original I/O.
- [ ] Run five cold and twenty warm repetitions where practical; publish median,
  p95, maximum, allocations and resource limits. Keep raw measurements local.

Gate: reproduce the blocking request sequence and distinguish server, transfer,
JSON parsing and rendering costs. Baseline must precede the relevant change.

**Local record (2026-09-28):** production loopback measurements are in
`docs/login-latency-trace-2026-09-28.md`. Cold/warm browser repetitions,
profiles and a 97k-entry end-to-end fixture remain to be collected locally.

## R1 — make login and loading responsive

Start in: `mockup-ui/app.js`, `data.js`, `views.js`, `index.html`,
`internal/desk/multi_library.go`, `internal/library/photo_prepare.go`.

- [x] Disable duplicate form submission and show an accessible pending state
  within 100 ms. Restore controls on every failure, including network failure.
- [ ] Preserve automatic vault unlock and the separate-passphrase prompt. Do not
  turn every unlock/network error into an incorrect-password message.
- [x] Call `openDesk` once authentication/unlock succeeds. Home must not fetch
  the complete library. Load only data required for the active view; secondary
  quota, capsule, places and admin panels load independently with bounded fan-out.
- [x] Render Library rows as soon as listing finishes, without awaiting quota.
  Show a loading state instead of a misleading empty folder. Keep navigation,
  cancellation and retry usable while requests are outstanding.
- [ ] Deduplicate requests triggered by startup, route changes and SSE. Capture
  account/session/request generations; discard late results after lock or switch.
  Audit upload-session restoration before enabling uploads for a new account.
- [x] Do not synchronously build the photo index in the unlock response when
  resuming preparation. Use an owner/session-scoped, deduplicated background task
  that participates in cancellation and drain; never an untracked goroutine.
- [ ] Test slow secondary APIs, rejected credentials, separate vault password,
  expired sessions, repeated submit, rapid lock/relogin and restored sessions.

Gate: visible desk within 100 ms of the unlock response under controlled local
testing, even with quota held for five seconds. Library rows appear before that
delayed quota response. A Home login issues no full-library request. These are
UI-overhead targets, not promises about password derivation or Internet latency.

**Implemented locally:** submit pending/duplicate protection; successful unlock
opens the desk without awaiting library, quota, capsules, Places, admin, or upload
queue restoration; those secondary requests start independently. Library loading
shows a loading state and renders rows without awaiting quota. Late list/quota and
upload-restore responses are scoped to the current username/session. Photo-index
construction for resumed preparation now runs in the tracked, session-cancelled
worker path. Browser timing, delayed-quota, rapid-switch and separate-vault
password smoke gates remain pending.

## R2 — calculate storage statistics once

Start in: `internal/desk/multi_library.go`, `internal/library/maintenance.go`,
`metadata.go`, `internal/sharedstore/metrics.go`, `internal/quota/quota.go`.

- [x] Introduce one authorized storage-summary operation. Load one catalog
  snapshot for live bytes, unique-file bytes and Trash bytes, rather than calling
  `Dedupe` and `Trash` through separate `ensure` paths.
- [x] On shared storage, reuse a single metrics result. Separate logical SQL
  aggregates from physical directory walks; the current `Metrics` does both.
- [ ] Cache display summaries by committed generation; coalesce concurrent
  refreshes. Start with a five-second maximum display age for live aggregates
  and thirty seconds for physical disk walks. Include an `as_of` timestamp.
- [ ] Mutation invalidates the display summary. Upload, replacement, Trash,
  restore, purge, shared-owner removal and migration/reconciliation are included.
- [ ] Keep filesystem capacity and write reservations authoritative and current.
  Never admit an upload using a cached UI figure. Preserve the silent system
  reserve and existing dedupe scope labels; no per-user disk allocation.
- [ ] Test repeated hashes, replacement, live-versus-Trash semantics, cache expiry,
  failed commits, concurrent readers, and shared physical-metrics reuse.

Gate: a cold quota request loads the catalog at most once; a valid warm summary
does not reload it. Provisional warm p95 target: 200 ms on the 2-CPU/4-GiB test
envelope. Missing/stale display data cannot bypass quota or expose another owner.

**Implemented locally:** one catalog summary supplies logical, unique, and Trash
totals; successful durable catalog writes invalidate its aggregate. Per-owner
library display summaries are reused for up to five seconds and invalidated by
catalog generation changes. Filesystem quota admission remains independently
authoritative. Shared physical statistics still require a cold filesystem walk;
the display cache avoids repeating it during the freshness window. Small-envelope
latency and shared-storage mutation tests remain pending.

## R3 — keep an authorized catalog snapshot and indexes in RAM

Start in: `internal/catalog/catalog.go`, `identity.go`, mutation/save helpers,
`internal/library/events.go`, `metadata.go`, `internal/filesvc/registry.go`,
`internal/vault/vault.go`. Reuse the Photos index lifecycle where appropriate.

- [x] Make repeated reads reuse a validated owner/session snapshot, detect
  external catalog replacement, and coalesce reads behind the Library lock.
  Authentication still runs before handing any snapshot to a caller.
- [x] Build path and direct-child folder indexes once per catalog generation.
- [ ] Build entry-ID and reusable sort indexes once per generation.
- [ ] Audit every writer first: uploads, WebDAV, imports, restore/purge, recovery,
  maintenance and migration. A committed mutation publishes a new generation;
  failed commits leave the old valid generation intact. Handle externally changed
  catalogs via explicit invalidation/reconciliation; do not rely only on mtime.
- [x] Start with one serialized load/commit path and immutable reader snapshots.
  Move expensive read-only work outside the metadata lock using existing leases.
  Budget simultaneous old/new snapshots, index construction and active readers.
- [ ] Retain hot unlocked owners within a node-wide memory allowance; evict idle
  read caches fairly. Warming follows successful unlock and foreground demand,
  never unlocking other users' vaults to populate cache.
- [ ] On lock/revoke, stop new readers, cancel/drain leases, remove indexes and
  cached responses, and clear owned byte buffers. Do not claim Go strings or GC
  guarantee physical erasure. A locked owner cannot receive a cached result.
- [ ] Preserve safe operation with caching disabled. The current encrypted JSON
  catalog still requires a full cold load: paging alone cannot make that memory
  requirement disappear. Measure its minimum working set; do not silently exceed
  admission if it cannot fit. Report a capacity error and record a separate
  encrypted indexed-storage design if truly larger catalogs require one.

Gate: warm metadata/list/stat reads do not decrypt/parse the same catalog again.
Race and mutation tests prove freshness, session isolation, error recovery and
bounded resident snapshots. No new on-disk catalog format is required.

**Implemented locally:** each Library instance keeps its loaded catalog for the
current vault session; loads/commits have generations and external replacement is
detected. The catalog and Photos row/index buffers clear on explicit lock/rekey
and on the next request after any other session change. Path and direct-child
indexes are reused for browsing. Entry-ID/sort indexes, idle eviction and
memory-budget admission remain unimplemented; cold start still decrypts the full
encrypted JSON catalog.

## R4 — page folders, search and browser rendering

Start in: `internal/desk/library.go`, `internal/library/photo_cursor.go`,
`mockup-ui/engine.js`, `app.js`, `views.js`.

- [x] Add an authorized folder-page API: directory, limit, stable sort,
  generation-bound cursor and next cursor. Default 100 entries, maximum 200.
  Return just display metadata, not backend references or hashes.
- [x] Move global search/wildcards to server queries with paged results.
  Preserve wildcard, scope, sorting and date/size-filter semantics. Check query
  cancellation and length; a bounded in-memory scan is the fallback for complex
  patterns. A fast prefix/extension index remains optional optimization work.
- [ ] Stop building a complete browser tree for every render. Retain breadcrumb
  state, a small LRU of visited pages, and a bounded DOM window for large folders.
  Debounce search and cancel obsolete requests. A browser Web Worker is optional
  only if profiling still finds necessary heavy client work after paging.
- [ ] Audit selection across pages, select-all, move/copy, recursive folder
  actions, drag/drop, ZIP download, grabs and keyboard navigation. Actions resolve
  and authorize stable server identities; unloaded items must not be omitted.
- [ ] Patch/invalidate affected pages on committed SSE events; resync on gaps or
  reconnect. Preserve scroll and selection when still valid. Stale cursors restart
  the view safely instead of mixing generations.
- [ ] Fetch current-folder previews first, then at most one neighboring page on
  idle time. Bound in-flight requests and stop prefetch on route/session changes.
- [ ] Measure compression for bounded JSON pages and fingerprinted static assets.
  Avoid recompressing images/video or buffering originals. Private metadata and
  previews must not enter public/proxy caches; persistent browser caching requires
  an explicit privacy design. Do not delay SSE or streaming/range responses.

Gate: Library navigation no longer downloads 11.7 MB for the first screen.
Typical 100-item payload target: under 256 KiB; warm folder API p95 target: 200 ms
on the small envelope. Avoid browser tasks over 50 ms on the fixture. Keep the
existing full-list API for compatibility until callers have been audited.

**Implemented locally:** normal Library browsing requests at most 100 direct
children (200 maximum) with stable server sorting and a generation-bound cursor.
The encrypted catalog builds path/child indexes once per load or committed write;
legacy paths synthesize folder rows. The UI loads another page on demand and
reloads when a cursor goes stale. Global wildcard search and type/date/size filters
use bounded server pages. The server scans the cached catalog for matching rows;
client virtualization, search indexes and SSE page patching remain open. The 256 KiB and latency
gates have not yet been measured.

## R5 — use spare RAM and cores without competing with browsing

Coordinate with A3/A5 in the Photos resource workbook; implement one scheduler.

- [ ] Add a byte-accounted node-wide retained-cache budget, with fair owner
  shares, inactivity eviction and pressure-triggered shrinkage. Prioritize
  catalog indexes, visited-folder pages, search structures and small previews.
  Encrypted disk previews and filesystem cache remain useful lower tiers.
- [ ] Start retained RAM at `min(effective RAM / 16, 8 GiB)`. This is a ceiling,
  not an allocation or permission to warm every vault. Allow disabling it.
  This budget includes reusable metadata/index memory; do not count it twice.
- [ ] Combine that ceiling with existing preview working-memory reservations,
  pinned snapshots, source buffers, upload/archive work, subprocesses and GC
  headroom. Cold loads/rebuilds must acquire budget before allocating. Never add
  independent subsystems each assuming the entire machine is available.
- [ ] Extend resource discovery to memory pressure/working-set and storage
  latency signals. Start below concurrency ceilings, grow under sustained demand,
  and back off with cooldowns when foreground p95 or pressure worsens. File cache
  alone is not memory exhaustion. Failed discovery selects conservative settings.
- [ ] Give interactive reads the next eligible capacity and fair service across
  owners. Coalesced background jobs become foreground when a visible tile joins.
  Reserve capacity for auth/metadata, including on one-worker deployments.
- [ ] Parallelize independent preview generation and bounded chunk/hash/codec
  work where profiling justifies it. Keep catalog publication and reference
  accounting atomic; cap nested codec threads inside each outer worker.
- [ ] Audit per-call codec creation in `internal/sharedstore/chunk_codec.go`.
  Benchmark bounded codec reuse and explicit concurrency/memory options. Never
  pool mutable codecs concurrently without the dependency's documented contract.
- [ ] Leave bulk originals streaming. Background ZIP compression and import
  batches yield to browsing; a disk bottleneck is not cured by more readers.
  On two-socket hosts, measure NUMA/remote-memory effects before affinity tuning.

Initial test ceilings (proposals; not new minimum host requirements):

| Envelope | Retained RAM | Existing preview work budget | Background / total render / source readers |
| --- | ---: | ---: | ---: |
| 2 CPU / 4 GiB | 256 MiB | 512 MiB | 1 / 2 / 1 |
| 4 CPU / 8 GiB | 512 MiB | 1 GiB | 2 / 3 / 2 |
| 32 CPU / 128 GiB | 8 GiB | 8 GiB | 8 / 10 / 4 |

Cache plus preview ceilings are not total RSS limits. Required catalog working
sets may constrain concurrency before these maxima. Most of the large machine's
RAM remains available for the OS cache, other service work and headroom. Increase
retained cache only when hit rates and measured workload justify it, not to fill RAM.

Gate: compare cache disabled/enabled and concurrency 1/2/4/8 on available hardware.
Show throughput gains without more than a provisional 10% foreground p95
regression. If the large machine is unavailable, mark its benchmark pending;
policy simulation is not a 32-core performance result.

**Implemented locally (partial):** the existing renderer slots, backfill slots,
source-reader slots and byte-accounted preview-working-memory reservations are
process-wide, not multiplied per Library. The defaults match the proposed
2/4-GiB, 4/8-GiB and 32/128-GiB preview ceilings. Background previews now yield
the next available memory reservation to waiting foreground previews. This is
covered by a contention test. The new sequential zstd encoder reuse is scoped to
one manifest write, so each upload owns its encoder and chunks remain serialized.
There is still no node-wide retained RAM budget for catalog/photo indexes, no unified budget
for catalog snapshots, uploads, subprocess RSS and GC, no fair owner scheduling,
and no pressure/latency feedback or adaptive worker count. The full R5 memory and
mixed-load gates remain open; do not claim 32-core/128-GiB gains from this host.

## R6 — conditional SIMD and compiler optimization

- [ ] Inventory the actual Go toolchain, pinned dependencies and bundled Restic
  binary. The checkout uses standard Go AES-GCM/SHA-256, Argon2, Restic chunking
  and klauspost/compress; inspect which optimized paths these versions use.
- [ ] At a later authorized host inspection, record CPU model and guest/container
  feature flags. Do not infer AVX2/AVX-512/AES availability from “Xeon Gold.”
  Proxmox's exposed CPU model and OS support are part of the capability check.
- [ ] Profile the post-R1–R5 system. Candidate kernels: hashing, chunk compression,
  decompression, pixel conversion/resize and response compression. Do not start
  by replacing JSON parsing just to accelerate catalog loads we should eliminate.
- [ ] Keep the default amd64 build at `GOAMD64=v1` and retain other supported
  architectures. Prefer maintained implementations with runtime dispatch plus
  baseline fallback. A v3-compiled binary cannot fall back on a non-v3 CPU.
- [ ] Distinguish AES-NI/PCLMUL and SHA instructions from AVX. Standard Go AES
  already uses hardware support where available. No custom crypto, altered
  digests/chunk boundaries or weaker checks to improve benchmark numbers.
- [ ] Benchmark generic versus accelerated paths on identical inputs. Require
  a meaningful repeatable win (initial gate: 10% in the target workload), valid
  output, bounded memory and no foreground regression before adding complexity.
- [ ] Treat AVX-512 as a separate experiment; measure whole-service latency and
  throughput under mixed load rather than assuming wider vectors are faster.
  No AVX-specific storage format or compulsory accelerated image.
- [ ] Try Go profile-guided optimization with representative, sanitized local
  profiles before maintaining separate ISA-specific releases. Compare with PGO
  disabled; record toolchain/profile provenance. Do not ship private profiles.
- [ ] If an optional optimized image is justified, publish an explicit separate
  tag and compatibility check. Test the baseline image with accelerated paths
  disabled in libraries that support that test mode, plus actual lower-capability
  hardware/emulation where available. Never label a high-ISA build “portable.”

Technical references: Go documents baseline/runtime CPU behavior and the v3
compatibility restriction in [Minimum Requirements](https://go.dev/wiki/MinimumRequirements).
Its [AES documentation](https://pkg.go.dev/crypto/aes) describes supported hardware
implementations. Use the [pinned compression source](https://github.com/klauspost/compress/tree/v1.18.6)
for codec capabilities/options and the [Go PGO guide](https://go.dev/doc/pgo) for
profile-guided builds. These explain mechanisms, not measured WeazlCloud gains.

Gate: unsupported CPUs execute a tested standard path; encrypted data is
interoperable across variants. Record rejected experiments as well as wins.

**Implemented locally (partial):** verified Go 1.27.0, linux/amd64,
`GOAMD64=v1`, klauspost/compress v1.18.6, and Restic 0.19.1. No ISA-specific
build was introduced. Added sequential encoder reuse inside each shared-store
manifest write, retaining the existing compression level, CRC and `zstd-v1`
format. On the available i7-1365U, repeated-data microbenchmarks showed
512-KiB encoding improve from about 2.0 ms / 12.5 MB allocated to 0.15 ms /
0.55 MB; 8-MiB encoding improved from about 4.6 ms / 12.0 MB to 2.4 ms /
0.23 MB. These are synthetic single-thread codec results, not end-to-end upload
gains. Existing shared-store round-trip and modified-region tests pass, including
under the race detector. CPU feature inventory, representative PGO, codec
concurrency/memory under concurrent uploads, and production-host measurements
remain pending.

## R7 — verification and rollout handoff

- [ ] Run focused tests for each change, then `make check` on the finished code.
  Run disposable Restic/shared browser and restart smokes, including constrained
  2-CPU/4-GiB testing, owner isolation and the separate vault-password flow.
- [ ] Repeat R0 measurements for the released phases, including mixed foreground,
  upload and preview work. Distinguish cold startup, warm session and cache eviction.
  Record request counts, payload bytes, p95 latency and total process/child RSS.
- [ ] Test killed/restarted workers, disk/cache failures, cancelled requests,
  mutation races and memory pressure. Keep known-correct originals and caches
  disabled as rollback options. New indexes must be rebuildable.
- [ ] Update README with implemented settings only. Publish standard defaults,
  optional acceleration, cache privacy/lifetime, measured limitations and how to
  disable cache/backfill. Update the older workbook rather than duplicating work.
- [ ] Prepare an explicit deployment checklist for later approval: verify import
  remains complete, retain review-held ZIPs, save old image/config, preserve mounts
  and vaults, deploy with aggressive warming off, run login/readback checks, then
  enable optional tuning gradually. Roll back the image if acceptance fails.

Required handoff: phase status table, measurements before/after, reproducible
commands, known limitations and rollback. Planning completion is not permission
to push, restart production, remove review holds or claim unmeasured speedups.

| Phase | Local status |
| --- | --- |
| R0 | Production request baseline recorded; local profiles/browser repetitions pending |
| R1 | Core nonblocking login changes implemented; browser latency/failure smokes pending |
| R2 | Single-pass catalog summary and five-second owner cache implemented; small-envelope/shared-store gates pending |
| R3 | Session catalog reuse, external-file detection, lock clearing, path/child indexes implemented; entry-ID index and memory controls pending |
| R4 | Folder and search/filter paging active; client LRU/virtualization, SSE patching, payload limits and browser smoke pending |
| R5 | Partial; existing global preview scheduler gets foreground memory priority; retained-cache budget, unified memory admission, pressure feedback, fairness and mixed-load gates pending |
| R6 | Partial; per-manifest sequential zstd encoder reuse benchmarked and implemented; ISA/PGO experiments and representative host gates pending |
| R7 | Full local checks, container/recovery/constrained-browser smokes and authorized production rollout passed; unmeasured performance and fault gates remain open |

**Local verification so far:** JavaScript syntax, Go line limits, all-package Go
compile (`go test ./... -run '^$'`), `go vet ./...`, the full Library,
catalog/vault and filesvc tests, targeted desk handler tests, and focused catalog
and Library race tests pass. On the available 13th Gen Intel i7-1365U, a metadata-only 97k-entry catalog
summary took 6.05 ms and 6.99 MB in a single first-pass run, then 18.65 ns with
zero allocations for warm reads. Paged wildcard search over the 97k fixture took
19.1 ms and allocated 12.4 MB while returning at most 100 rows. These are local
microbenchmarks, not production-Xeon or browser measurements. Full desk/browser
smoke and the remaining workbook gates are pending; this workspace cannot bind a
localhost test server (`operation not permitted`), so browser and full HTTP-server
smokes could not run in that restricted session.

**Release follow-up, 2026-09-28:** after workspace/network access was restored,
full `make check` passed (all Go tests, race tests, vet, line checks and JavaScript
syntax). Fresh-image Restic and shared-store container smokes passed, including
readback after restart and disposable migration. The recovery smoke passed.
Chromium Photos/Library/music and upload smokes also passed in a 2-CPU/4-GiB
container. These checks do not close the unmeasured cache, pressure-feedback or
scaling gates. See [release evidence](docs/release-2026-09-28.md).

**Authorized rollout:** release `7bb01f4` is live, with unchanged catalog metadata,
healthy probes, authenticated paging, thumbnail and original readback checks.
The prior image, source/config and stopped data snapshot are retained for rollback.
The owner's later explicit cleanup instruction superseded ZIP retention: the final
three sources were reverified and removed, freeing 149.40 GiB. Full audit and
remaining performance limitations are in the release evidence linked above.
