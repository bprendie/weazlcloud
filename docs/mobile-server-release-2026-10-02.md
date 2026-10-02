# Mobile server production release — October 2, 2026

The MS0–MS8 server workbook is implemented, locally verified, published and live
on `weazlcloud.teralab.local`. Physical iOS verification remains part of the app
handoff, outside this server release.

## Release identity

- Code commit: `15c45144b660eb5d673851d1ca0ae51396407116`, pushed to `origin/main`.
- Running version: `mobile-15c45144b660`.
- Image: `weazlcloud:release-15c45144b660`.
- Production image ID: `sha256:a673f24c5441cad00600219c3e7a56a9bfab3b5d2c55f3c694e842b5e162cf37`.
- Local test image, same application source: `weazlcloud:smoke`,
  `sha256:cd7429e383ca3825a6ebef0bb737ca8e69df5347f03896bd0d11d3949dc5004d`.
- Deployment and audit: approximately 17:14–17:16 UTC, October 2.

Production was reached through `bobp@jumpbox.prendie.io`, then
`bobp@weazlcloud.teralab.local`. Direct local DNS did not resolve the target.

## Verification

The [validation record](mobile-server-validation-2026-10-02.md) records passing
full Go tests/vet/race, Go line limits, JavaScript checks, builds, and browser,
container, Photos and native mobile smokes on both storage backends. The native
fixture used 2 CPUs / 4 GiB, a generated 250 MiB original, reordered/repeated
parts, restart, automatic finalization and a hash-verified content download.
It also measured encrypted staging cleanup and sampled process memory.

After deployment:

- App and photo worker were healthy, running, with zero restarts.
- `/live` and `/ready` returned successful process/storage responses.
- Discovery returned contract version 1 and a stable instance ID across reads.
- Unauthenticated mobile capabilities returned HTTP 401.
- The running binary reported the full code commit above.
- Structural comparison of the saved and live Compose configurations confirmed
  that only service image references changed. Data/import mounts, ports, CPU and
  memory limits, worker isolation, settings and network configuration stayed intact.
- The owner catalog ciphertext checksum matched its pre-upgrade checksum.
- The live and copied Restic repositories each contained 157,372 files. These
  are repository files, not a count of Library assets or Photos.
- The existing vault and node key remained present. Disk usage was 692 GiB of
  the 2 TB filesystem, with approximately 1.4 TB free.

No owner vault was unlocked by the deployment. Catalog schema upgrades happen
lazily during supported owner-unlocked access; the restart audit verifies intact
encrypted data, not an authenticated production media session. Both-backend
authenticated behavior and migration are covered by the local fixtures.

## Recovery copy

Services were stopped gracefully before a full GNU `cp -a --reflink=always`
copy on the same XFS filesystem. The copy succeeded, repository file counts
matched, users/settings files were compared where present, and catalog checksums
were recorded before starting the candidate.

- Data copy: `/exports/dockervolume/weazlcloud-backups/mobile-15c45144b660/data`.
- Backup directory permissions: `0700`.
- Saved deployment configuration/image metadata:
  `/home/bobp/weazlcloud-releases/mobile-15c45144b660/`.
- Previous image: `weazlcloud:release-813d258`.
- Previous production repository HEAD: `e3d5ae0`.

The copy initially shares physical blocks through XFS reflinks; it is not a
second independently stored disaster-recovery backup. Later writes consume
copy-on-write space. Keep it until the release has been accepted.

Follow the [migration/recovery runbook](mobile-server-migration.md). Never start
the previous image against migrated live data: use a forward fix or restore the
complete consistent pre-upgrade copy while stopped. Restoration discards writes
made since the copy; do not restore individual catalogs in isolation.
