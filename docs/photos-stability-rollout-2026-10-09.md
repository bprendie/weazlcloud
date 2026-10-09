# Photos stability rollout — October 9, 2026

Production runs `weazlcloud:release-474dd74ca6bc` on both the API and isolated
photo-worker services. Source revision:
`474dd74ca6bcc589ba20a3d3dd84d034dd583ef6`. The running binary reports
`photos-stability-474dd74ca6bc`.

This release separates browsing from explicit photo selection, retains decoded
thumbnails and warm caches during ingestion, refreshes the resident Restic index
for newly committed packs, fixes seek-dependent MOV poster generation, and fits
portrait playback inside the viewer. The existing 64 MiB preview-source limit
remains; this is not a storage/upload size limit. Previously failed supported
MOV jobs can be retried through **Photos → ☰ → Retry failed previews**.

## Preservation and checkpoint

The custom production Compose configuration was retained. Only its two image
references changed. Runtime comparison confirmed identical environment values,
ports, data/import/IPC mounts, service users, security settings and resource
limits. The API keeps 16 CPUs; the network-isolated photo worker keeps 8 CPUs
and 16 GiB. API preview admission reports 8 workers, 4 background workers and
4 source readers. The worker has no vault mount.

Both previous services exited with code zero before the complete data volume
was copied using XFS reflinks to:

`/exports/dockervolume/weazlcloud-backups/photos-stability-474dd74/data`

The node/account/storage settings and owner catalog, vault envelope and node key
passed six checksum comparisons before activation. A corrected full inventory
check after startup found **60,625 files in both trees**, with no added, missing
or changed file-size/ownership/mode records. The retained repository inventory
also matched. This is not a full hash/readback of every original. No storage
migration, data pruning, password change or cache wipe was performed.

The initial inventory command used an unsupported BusyBox `find -printf`, and
the initial runtime comparison treated environment order as significant. The
services were healthy during remediation. GNU tools with checked pipelines and
nonempty-inventory assertions completed verification; environment maps matched
exactly. The private deployment helper was corrected and the final result is
recorded as healthy. Earlier diagnostic logs remain available.

## Verification

- Local `make check`, authenticated Photos browser smokes on both storage
  backends with 2 CPUs / 4 GiB, and single/bundled H.264/HEVC MOV tests passed.
  Live-upload tests preserve decoded image elements/URLs without thumbnail
  refetches; portrait playback fits desktop and phone portrait/landscape sizes.
- Native JPEG boundary/sanitizer checks and both basic browser smokes passed.
  The resident reader was additionally tested with race instrumentation against
  concurrent reads of old files and newly committed snapshots.
- The exact release image passed disposable host-side container checks on both
  backends: upload/download, WebDAV, resumable finalization/restart, sealed grabs,
  and the Restic migration fixture. The isolated worker probe passed. The host
  lacks `rg`; the worker smoke used an equivalent `grep -Eq` check in a private
  copy of its harness.
- Desk, Grab and WebDAV readiness returned HTTP 200 after deployment. The live
  private worker passed its PNG/HEIC rendering probe.
- Public Desk and Grab readiness passed with verified TLS. Chromium displayed
  the public login page without JavaScript errors or failed requests. Four
  served UI assets matched the release source by SHA-256.

Authenticated production Library/Photos browsing and a resumed iOS upload were
not exercised with new credentials during this rollout. Existing browser
sessions required sign-in after restart. Authenticated behavior was verified
with disposable local data; production readiness/UI checks are distinct.

Private image/configuration records, checkpoint inventories, checksums, smoke
logs and final verification results are under:

`/home/bobp/weazlcloud-releases/photos-stability-474dd74`

The previous image/configuration and the checkpoint remain available. Prefer a
compatible image rollback after a clean drain; restoring the data checkpoint
would discard later uploads and needs a separate restore/replay decision.
