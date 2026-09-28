# Photos web performance workbook results — September 26, 2026

## Scope and environment

This is a local implementation and disposable-container evaluation. Production
was not contacted, changed, restarted, or deployed. The code started from commit
`ace4365`. The working tree already contained user changes and untracked files;
an accurate before-implementation status snapshot was not recorded.

Measurements ran on Linux `7.2.5-3-omarchy`, Go
`go1.27.0-X:nodwarf5`, with an Intel Core i7-1365U (13th Gen). The browser
smokes used the existing small album fixture in disposable Docker containers
and Chromium. Both storage backends completed the album, account-isolation,
pagination, preview, direct-link and restart checks.

## Results

There is no comparable before-change timing set. The planned cold/warm p50/p95,
bytes, request counts, peak memory, catalog loads and original restores were not
captured before implementation. Do not interpret the following as a measured
before/after improvement or as a production result.

An in-memory benchmark paged 100 rows from a synthetic 100,000-row photo index.
Three 100-iteration runs measured 8.588, 6.789 and 6.291 microseconds per page,
with approximately 26.6 KB and 17 allocations per page. This isolates index
pagination; it does not model disk, encryption, browser work or image rendering.

The local Restic-backed browser smoke reported the first four cards in 62 ms,
the first thumbnail in 101 ms and four rendered DOM cards. The shared-storage
smoke reported 49 ms, 85 ms and four cards. These used a tiny fixture with a
warm server index; one photo was already warm as an album cover. They are smoke
observations, not representative cold-cache or large-library latency samples.
The direct Photos route made no full-library request in the tested flow.

`make check` passed, including Go tests, vet, race tests, line limits and
JavaScript syntax checks. The focused Restic and shared-storage Chromium smokes
both passed. The smoke also checked photo albums, deep links, restart behavior,
preview preparation, music metadata/artwork/playback, and locked/other-owner
isolation. Test ZIPs remained in the disposable fixture as expected.

## Implemented behavior

- An encrypted owner-specific derived photo index, reconciled from catalog
  state, with paginated cursor APIs and incremental mutation updates.
- Preview lookup through indexed IDs, with current-entry authorization checks.
- Encrypted content-addressed preview caching, resource limits and coalesced
  thumbnail work; a resumable, opt-in preparation job for grid previews.
- A paginated Photos view with near-viewport page loading, bounded rendered DOM,
  preview-by-ID, album covers and preparation status/actions.
- README guidance for preview preparation and its resource controls.

## Remaining work and limits

- Capture reproducible before/after cold and warm p50/p95, bytes and request
  counts, peak memory, catalog work and original-content restores on the same
  fixture. Measure first-screen readiness on a meaningfully larger collection.
- The DOM is virtualized, but client metadata retained from visited pages is not
  strictly capped. Bound retained page data and safely refetch pages when users
  scroll back.
- Change generations refresh the page rather than applying every affected row
  while preserving a precise scroll anchor.
- Correct EXIF orientation is not implemented. WebP/HEIC/AVIF and other
  unsupported images keep existing fallback behavior; video transcoding is out
  of scope.
- Preparation retries transient failures a bounded number of times and
  continues past bad files, but does not persist a per-content permanent failure
  cache or offer a durable failed-item retry list.
- The API does not expose a separate index-readiness/progress state for initial
  index reconciliation.
- The synthetic 100k index benchmark is not a 100k browser test. No claim is
  made about large-library DOM, memory or end-to-end performance.
- Local checks do not establish production performance or validate the active
  Takeout rehydration. Production rollout remains a separate, user-controlled
  step after import completion.

## Reproduction

Run `make check`, then build/use the local image and run the existing browser
smoke against each backend. The shared-storage run used:

```sh
WEAZLCLOUD_IMAGE=weazlcloud:photos-workbook-local \
WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental \
WEAZLCLOUD_ALBUM_PORT=29181 \
/tmp/weazl-layout-browser/bin/python scripts/smoke-photo-albums.py
```

The Restic run used the same command with
`WEAZLCLOUD_SMOKE_STORAGE_BACKEND=restic` and an unused local port. The index
benchmark is `BenchmarkPhotoIndexPage100Of100k` in
`internal/library/photo_index_benchmark_test.go`.
