# Photos local verification — September 30, 2026

Production was not contacted or changed during this implementation pass.
These are local results; incomplete workbook gates remain open.

## Passing checks

- All Go packages compile with `go test ./... -run '^$'`.
- Library, encrypted catalog and local account suites pass. Focused Photos,
  camera/EXIF, gallery and mobile-upload handler tests also pass with `-race`.
- `go vet ./...` passes.
- `make build VERSION=local-photos` succeeds with the portable production binary
  settings (`CGO_ENABLED=0`). The production Docker image also builds locally.
- JavaScript syntax, metadata-only justified-row tests and the Go file-size
  check pass. Embedded desk assets are regenerated from the edited source UI.
- Real handler/cookie-jar tests run without a TCP listener. They exercise actual
  Restic upload/Takeout storage, capture/caption/search, previews, owner isolation
  and frozen gallery export. The exported PNG preview decodes as an image and
  the original remains byte-identical after owner vault lock.
- Gallery tests cover encrypted manifest/pixels/originals, bad passphrases,
  cross-grant session rejection, ID substitution, non-consuming previews,
  selected ZIP readback, concurrent transfer limits, terminal burn and revoke
  cleanup, and failed mint rollback. The rendered guest script passes syntax
  checking and real HTTP handler routes deny GET-based gallery transfers.
  Additional tests cover slow-transfer metadata availability, final-admission
  retention of earlier streams, explicit revoke cancellation, encrypted selected
  ZIP restart recovery, session renewal and account deletion cleanup.
  Missing prepared ZIP output reports failure and rebuilds without spending a
  retry. A damaged photo derivative does not abort the frozen gallery; its
  original bytes remain downloadable.
- Query tests cover inherited hidden state, Photos Trash/restore, restoration
  of folder descendants and copies, root-level hiding, inherited/explicit child
  visibility, implicit mobile-folder materialization, movement across hidden
  parents, rekey/reload, deleted cursor
  continuation, complete-day
  metadata paging, midnight capture offsets, camera/outside-album filters and
  exact duplicate preference without deletion or membership changes.
  Headless API tests verify explicit owner Hidden browsing, denial of another
  owner's hidden original, album memberships and saved selection, and preservation
  of existing frozen grabs after hiding the source. Album mutation/list headers
  respect the hidden context; a 203-member metadata fixture bounds headers to 200
  IDs while membership paging reconciles every visible member. Forgetting a vault
  session clears private album metadata and forces a canonical reload.
- User rotation affects derivative geometry/cache identity, retains originals
  and survives Favorite changes. EXIF transform tests cover all seven nontrivial
  orientation transforms including mirrored pixels.
- Native simulator uses actual handler/cookie/bearer requests and Restic. It
  passes interrupted/resumed chunks after byte-engine/coordinator restart, wrong
  offsets/hashes, incomplete still/motion pairs, lost final replies, source
  revisions, two devices sharing a filename, atomic album membership, encrypted
  receipts, capacity exhaustion, wrong media, checkpoint acknowledgment, restored
  catalog detection, lock and revocation. It is a headless API simulator, not an
  iOS background scheduler.
- The native simulator now runs against both Restic and shared storage. After
  draining private work and closing the shared SQLite store, it copies the whole
  disposable data directory into a fresh location, deletes the derived Photos
  index and opens fresh account/vault/library services. Actual HTTP handlers
  verify login, vault lock/unlock, still/motion original bytes, capture/caption,
  custom album membership, hidden flags, device sync resumption, completed upload
  receipt idempotency, encrypted owner ZIP readback, configured hostname and frozen
  grab counters/access while the owner vault is locked. This filesystem recovery
  smoke does not claim a process restart or container/browser recovery gate.
- Owner ZIP tests pass encrypted job/output readback, queued/ready restart
  recovery, 90-minute ready retention, bounded range reads, durable cancellation
  and vault-lock denial. The API smoke exports a saved Photos selection, reads
  its ZIP and a range, and denies a Photos credential access to a generic Library
  archive. ZIP streaming leaves interactive metadata queries available.
- Native renderer checks pass progressive/grayscale/corruption/size/parallel
  cases, including address/undefined sanitizer runs.
- Device tests cover restart, token scope, revocation, password invalidation and
  ownership. Sync tests cover concurrent changes/deletes, encrypted checkpoint
  persistence, hidden-context binding and restored/divergent checkpoint rejection.

Final focused smoke command (all three packages passed with the race detector):

```sh
GOCACHE=/tmp/weazlcloud-m3-gocache GOPROXY=off \
RESTIC_CACHE_DIR=/tmp/weazlcloud-restic-cache \
go test -race ./internal/library ./internal/capsule ./internal/desk \
  -run 'TestPhoto|TestCustomPhotoAlbum|TestCatalogSnapshot|TestGallery|TestVersionedPhotoUpload|TestNativePhotoSimulator' \
  -count=1
```

`make vet js-check lines` also passes after the final fixes. The two JavaScript
test files contain six passing tests. `gofmt -l internal cmd` and
`git diff --check` report no formatting problems. Focused archive recovery/range
tests and the native sanitizer suite passed separately as described above.

## Metadata performance

Host: Intel i7-1365U, Linux amd64. `GOMAXPROCS=2` limits Go execution to two
processors; it does **not** claim a Docker two-CPU/four-GiB hard resource limit.
No original media was read in these fixtures. Measurements exclude HTTP/network,
browser layout, cold unlock and worker rendering.

Fresh final-pass result: the warm 100,000-row timeline returned a 100-item
metadata page in 20.1 µs per operation (300 iterations), about 84 KiB and 18
allocations. Earlier 10.2–10.9 µs results preceded the added component/source
fields and are superseded by this final measurement.

The 32,000-photo fixture measured query **plus JSON encoding**, 300 iterations
per case after warming each index:

| Query | Mean | p95 | Allocated per operation |
| --- | ---: | ---: | ---: |
| Timeline | 0.084 ms | 0.159 ms | 113 KiB |
| Favorites | 0.088 ms | 0.115 ms | 113 KiB |
| Capture year | 0.090 ms | 0.117 ms | 114 KiB |
| Folder album | 0.090 ms | 0.125 ms | 114 KiB |
| Wildcard search | 0.076 ms | 0.086 ms | 77 KiB |

Reproduce with:

```sh
GOMAXPROCS=2 go test ./internal/library -run '^$' \
  -bench 'BenchmarkPhoto(Page100K|Filters32K)' -benchtime=300x
```

## Integration checks after the permission retry

The earlier sandbox denied listeners, Docker and Git writes. After Bob's retry,
approved local commands ran outside that sandbox. Those environment failures are
superseded by these results:

- `make check`: every Go package passes normally and with the race detector;
  vet, JavaScript syntax, six layout/mode-memory tests and Go file limits pass.
  This includes actual TCP/Unix worker, desk, drive and share tests.
- `make smoke-container`: the image builds; actual Desk/WebDAV transfers,
  resumable uploads, previews and sealed grabs survive restart on Restic and
  shared storage. A disposable Restic volume migrates, verifies and reopens.
- `make smoke-browser`: basic Chromium DOM/login checks pass on both backends.
- `make smoke-recovery smoke-sharedstore`: filesystem restore, login, unlock,
  original readback and stale archive cleanup pass, as do shared-store crash
  recovery, concurrency, lifecycle and chunk-dedupe integration checks.
- The authenticated album smoke on Docker's **2 CPU / 4 GiB** limit passes
  Takeout albums, metadata escaping, cover decode, deep links, full-screen
  keyboard viewer, corruption/retry/replacement, durable pause across restart,
  Library controls, floating upload tray and mobile overflow checks. Existing
  MP3/FLAC/M4A/Ogg/Opus artwork, playback and owner isolation checks pass too.
  The initial four-photo run measured first cards at 215 ms and first decoded
  thumbnail at 239 ms. This is a tiny-fixture smoke, not a large-library claim.

The extended dated-photo regression found a missing date-formatter import in
the full-screen viewer. It is corrected; the extended browser smoke exercises
known capture dates as well as undated imports.
The same smoke found that the album form's named `id` field shadowed its DOM
ID and bypassed the delegated submit handler. Matching the form selector fixes
creation and editing; the regression requires a successful album API response.
Selected guest ZIP job routes were also missing from the share dispatcher. The
HTTP lifecycle test now prepares, polls and downloads a selected ZIP through
the public router, verifies its original bytes and exact retry count, and rejects
GET transfers. The router now forwards the narrowly scoped `zip/` routes.

`make smoke-photos PHOTOS_PYTHON=/tmp/weazlcloud-modal-venv/bin/python` passes
on both backends after those fixes. It exercises known-date viewer/favorite,
wildcard caption search, complete-day server selections, album create/edit,
gallery mint/QR, an account-free mobile guest grid/viewer, real selected/full ZIP
downloads and exact retry counters, Hidden transitions and music playback.
No page errors or horizontal overflow occurred. Browsing requested derivatives;
neither guest browsing nor warm owner scrolling requested originals.

### Constrained browser measurements

Chromium on the local host; service container hard limited to 2 CPU / 4 GiB.
The browser runs outside the container. These use the existing four-photo Takeout
fixture plus the corruption/replacement photo, not hundreds of new media files.
Twenty warmed HTTP page/date-summary pairs, five page reloads and twenty warm
Library/Photos transitions were measured. Reloads do **not** clear the vault/index
or OS cache and therefore are not cold-unlock measurements.

| Measure | Restic | Shared |
| --- | ---: | ---: |
| Warm HTTP page + dates pair p95 | 6.72 ms | 5.82 ms |
| Capture-year jump | 90.2 ms | 88.1 ms |
| Five page reloads, median | 187.3 ms | 193.7 ms |
| Twenty warm Photos viewports, p95 | 118.6 ms | 161.3 ms |
| Warm scroll JavaScript work p95 | 1.32 ms | 1.15 ms |
| First four cards / decoded thumbnail | 239 / 301 ms | 229 / 308 ms |
| Original browser requests during warm scrolling | 0 | 0 |

Scroll work is a Chrome DevTools `ScriptDuration` delta per scroll observation;
it excludes full layout/paint/frame timing. Browser request counts do not alone
prove zero backend restores; encrypted cache/source-read tests cover that boundary.
This small fixture mounts five cards. The <=300-card bound and <=2,000-item page
cache also have separate metadata/layout tests. Large-library browser, complete
frame, cold-unlock and aggregate RSS gates remain open.

Server selections, encrypted durable owner/guest ZIP jobs, source revisions,
logical still/motion ingestion, owner-selected Photos roots, album commit outbox,
bounded album membership sync and the native simulator are now implemented.
The [OpenAPI contract](photo-api.yaml) parses; 411 local references resolve and
39 operation IDs are unique. Syntax/reference validation does not claim external
OpenAPI conformance tooling or an implemented native app.

Remaining measurement gates are recorded in the workbook. Actual Safari media,
touch and color verification requires Apple hardware; Chromium does not verify
Safari. Large-library end-to-end layout, cold unlock and aggregate API/worker
RSS measurements remain separate from the metadata benchmark and tiny fixture.
Runtime SQLCipher persistence remains a prototype; encrypted JSON and private
in-memory indexes are active. The workbook remains honest about these gates.
Production was not contacted and no rollout/migration ran.

The attempted check commands and results are reproducible from the workbook;
[the recovery runbook](photos-release-runbook.md) lists all new private material,
retention/restart rules, fixture dry-run counts and rollback constraints.

## Publishing

Implementation commit `0ec910e91fa85c9d1ff4cc75909a4c65aa46a4ab` was pushed to
`origin/main`. `git ls-remote origin refs/heads/main` returned that exact hash.
The final full `make check` and both constrained authenticated browser smokes
passed before publication. Earlier read-only staging and DNS failures are
historical. Unrelated screenshots and Python caches are excluded from the commit.
Production is outside this pass and remains untouched.


## Subsequent production authorization

On October 1 Bob authorized deployment of the published release. The earlier
local-only scope above is historical. See [the production rollout record](photos-production-rollout-2026-10-01.md)
for live preservation/reconciliation checks and the still-required capture-date
backfill. Local smoke results do not imply that this existing-data repair ran.
