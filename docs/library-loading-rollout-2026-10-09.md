# Library loading fix — production rollout, October 9, 2026

Release `library-loading-769db5f81918` is live on both the API and isolated
photo-worker services. Source: `769db5f819183bd2e0cb666ce2f6ffc449573809`;
image: `weazlcloud:release-769db5f81918`;
image ID: `sha256:8b3858e36693d010c8da8e4d31621fc5475d6e5492fa2af982c341cc5c849702`.

The fix moves encrypted preview-cache reconciliation out of preparation-status
requests. Folder navigation no longer waits behind a complete cache inventory.
Queue-progress polling does not wait for queue initialization; reconciliation
cancels and drains with the owner vault. The UI can show that cached previews
are being checked. Existing upload indexing, date navigation and preview work
continue through the same pipeline.

## Preservation

The production checkout was fast-forwarded without replacing its custom
resolved Compose configuration. The only semantic Compose changes were the two
image references. Actual container mounts, ports, environment, user, security
settings, scratch directories and CPU/memory limits matched the previous services.
The API retains its 16-CPU limit; the isolated worker retains 8 CPUs / 16 GiB
and no network or vault mount. API preview admission reports 8 render workers,
4 background workers and 4 source readers within its existing resource policy.

Both old services stopped with exit code zero before a complete XFS reflink
checkpoint was created at:

`/exports/dockervolume/weazlcloud-backups/library-loading-769db5f81918/data`

This checkpoint reflects the already-reset Photos collection. The separately
verified `post-photos-reset-2026-10-09` checkpoint also remains. No old Takeout
collection was restored and no original was removed during deployment.

Account/node/storage settings, owner catalog, vault envelope and node key
checksums matched the stopped checkpoint and the restarted live data. The full
immutable Restic repository inventory (relative paths and sizes, excluding lock
files) also matched. Five retained Drive originals totaling 2,440,135 bytes passed
SHA-256 readback under service UID/GID 7272, and all 22,476 retained files still
referenced existing snapshots. This was a readback check, not another full
repository check or a hash of every original.

Private build, smoke, configuration, inventory and readback records are under:

`/home/bobp/weazlcloud-releases/library-loading-769db5f`

## Validation

- Full `make check` passed before rollout, including Go tests, race tests, vet
  and JavaScript checks. Additional maintenance-helper tests passed as the local
  user and in a root Docker fixture protecting service-owned private files.
- Authenticated local browser smokes passed on Restic and shared-experimental,
  each with a 2-CPU / 4-GiB limit. Coverage included Library upload controls,
  timeline scrolling/date jumps, preview preparation, Live Photo pairing and
  playback, guest transfers, and original-byte preservation.
- The exact release image passed disposable container upload/download, WebDAV,
  resumable-finalization/restart and Restic migration checks on the host before
  activation. These checks never mounted live vault data.
- The isolated worker smoke passed PNG and HEIC 320/1280 rendering and bundle
  probes. The live worker probe passed again after replacement.
- Both production containers became healthy; Desk, Grab and WebDAV readiness
  returned HTTP 200. Public Desk and Grab readiness passed through verified TLS.
- Chromium loaded the public production login page with no JavaScript errors
  or failed requests. The new preview-status UI asset is served. The first
  browser harness waited for network idle and timed out; waiting for the actual
  login control instead passed. No application change was needed for that check.

Authenticated **production** Library/Photos browsing was not exercised during
this rollout: the previously supplied login password is no longer valid and
replacement credentials have been requested. Deployment and data verification
are complete; local authenticated browser coverage is distinct from that
remaining production-session check. The reset collection is empty, so a new
large-library performance measurement also awaits the iCloud reseed.

The previous image and private Compose copy remain available. This change adds
no storage migration; prefer a compatible image/configuration rollback while
preserving current data. A full checkpoint restore would discard subsequent
uploads and therefore needs a separate restore/replay decision.
