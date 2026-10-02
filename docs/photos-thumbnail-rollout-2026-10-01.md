# Thumbnail cache and Photos controls rollout — October 1, 2026

Release candidate: `d432dd7` (implementation `2707421`, test-fixture correction
and rollback drill `d432dd7`). All CI jobs passed for that candidate. The live clean-stop gate then rejected
the old image's shutdown; it was immediately restarted without backup or
replacement. A lifecycle follow-up is undergoing local/release checks.

The change reuses an authenticated Restic index, keeps the durable worker queue
continuously supplied, emits a shared-source grid/viewer bundle, and adds bounded
encrypted disk, private RAM and browser caches. Photos maintenance actions live
in a hamburger menu. The date rail is inset from the browser scrollbar, reserves
its own column, accepts drags from year labels and commits navigation history
before a slower summary response can interfere.

## Preserved deployment and recovery point

The production API and private worker remain on their existing allocations:
16 CPU for the API; eight CPU / 16 GiB for the isolated renderer; 32 host CPUs /
128 GiB host RAM. Existing bind mounts, import root, hostname, authentication,
backend and environment settings are preserved. The API also receives a
90-second stop grace period to accommodate the new 60-second bounded drain. No global preparation
run is started, and the owner's manual pause remains enabled.

Before deployment: no active uploads/imports; metadata repair completed with
reported unresolved/failed sources. Inventory: 96,971 Library entries, 37,082
media assets, 34,313 known / 2,769 unknown capture dates, 16 albums. The private
inventory includes stable identity, capture-date and membership fingerprints,
plus three original SHA-256 samples. Private reports and configuration copies:
`/home/bobp/weazlcloud-rollouts/2026-10-01-thumbnail-cache` on the server.

Previous image retained as `weazlcloud:rollback-thumbnail-20261001` (running code
`878fec4`; `3c410fd` differs only in docs/tests). The proposed release image is
`weazlcloud:release-d432dd7`. The data checkpoint will be taken only after a clean
API stop, using a required reflink copy rather than a live partial catalog copy.

## Validation

Local full checks, native sanitizer tests, pinned/local Restic tests, both-backend
container/browser/Photos smokes, and single-worker-default race tests passed.
The final Photos smoke deliberately delays a date summary between successive
jumps and verifies Back history. Mouse, touch, keyboard, rail/toolbar spacing and
private browser-cache reuse pass. The full local details and measurement limits
are in [the performance report](photos-thumbnail-performance-2026-10-01.md).

The disposable image rollback drill passed for Restic and shared experimental:
new image → prior `3c410fd` → new image. It exercises old queue writes after clean
journal export, preserving pause, IDs, original hashes and 320/1280 preview hashes.
This is an image rollback test, not a restoration of stale production data.

A pre-upgrade sample of three existing assets / six thumbnail variants had no
errors. Thirty warm requests through server-local HTTP measured 2.70 ms p50 and
3.83 ms p95. Existing cache bytes were retained; this is not cold backfill throughput.

Post-upgrade reconciliation, health and browser evidence will be recorded here
once the cutover completes. Sustained full-collection throughput and its one-hour
target remain unmeasured; the small local fixture does not establish a production ETA.

The lifecycle follow-up explicitly closes request admission and SSE streams,
checkpoints archive workers while retaining ready ZIPs, exports encrypted photo
journals before vault lock, and closes persistent readers. The targeted app test
checks an open SSE connection, increased snapshot sequence and retained ZIP bytes.
Both-backend image rollback drills now keep SSE open during the new-image stop
and assert the exported journal is empty. They passed with the lifecycle fix.
