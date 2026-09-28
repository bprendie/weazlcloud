# Rehydration conflict recovery — September 27, 2026

## Incident

The unattended importer stopped on Drive archive `2-006` at 06:16 UTC after
404 of 1,563 file entries. Two different PowerPoint entries normalized to the
same path because one filename started with a space. The app treated the
different-size version as fatal. The host watcher polled the failed job every
20 minutes and retained its ZIP. Eight of sixteen archives had completed
before this ninth archive stopped; one of those eight remains held after an
earlier failure.

The initial status investigation missed the durable watcher log. Its ad hoc
login also omitted `X-Weazl-Desk: 1`, so the request was rejected before normal
authentication. The existing watcher API client supplies the required header
and uses the private host credentials without printing them.

## Fix

- Preserve the existing file and import different incoming content under a
  deterministic suffix derived from the ZIP member identity. Compare actual
  SHA-256 hashes before reusing any existing destination on a retry.
- An occupied alternate name advances to the next stable candidate; it never
  overwrites unrelated content. A file blocking an incoming directory routes
  the incoming subtree to a deterministic sibling directory.
- Serialize destinations that may require conflict routing while retaining
  the existing bounded parallel import of new small files. Large files still
  stream; conflict handling does not load their bodies into memory.
- Return source/destination mappings and hashes in import progress and final
  summaries. The watcher persists these in its private archive audit state.
- Verify renamed content against both the ZIP source and stored bytes before
  considering an archive verified. Retain ZIPs with any conflict mappings,
  corruption, prior failure, or verification problem.
- Persist a failed watcher item as `failed`, rather than leaving its displayed
  state as `running`. Storage/authentication/capacity failures still stop safely.

## Local validation

The patch was built from production commit `ace4365` in an isolated checkout;
the pending Photos workbook changes are excluded. Focused Go race tests cover
different-size and same-size content conflicts, normalized names, exact
duplicate ZIP names, file/folder collisions, occupied alternate destinations,
continuation and idempotent retries. Python tests check content verification,
bad mappings and deletion holds. A disposable Docker/Restic watcher smoke
verified both versions, continued to later files, retained the problem ZIPs,
removed only the healthy verified ZIP, and produced the disk report.
Full `make check` also passed in that isolated checkout, including all Go tests,
race tests, vet, line limits and UI syntax checks.

## Deployment

Deployed at 11:27 UTC after confirming no import was running, holding the
watcher lock and saving the old image plus private watcher/source/Compose
backups. The replacement container passed health checks; mounts and environment
were compared and remained identical. Archive `2-006` resumed at 11:27:15 UTC.
No vault migration or wipe was performed, and no staged ZIP contents changed.
The preexisting deletion hold on the failed archive remains in force.

Private host backups are under
`/home/bobp/weazlcloud-import-watch/before-conflict-fix-20260927T112704Z`.
The deployed image is `weazlcloud:import-conflicts-20260927`, also tagged
`weazlcloud:local` for the existing Compose configuration. The old image remains
tagged `weazlcloud:before-import-conflicts-20260927T112704Z`. No Photos-workbook
code was included. The existing 20-minute watcher cron remains installed.

Post-resume verification passed: the job advanced to 414/1,563 entries (404
existing files hash-checked and skipped, 10 newly imported), beyond its former
404-entry stop. The conflicting PowerPoint versions were independently read
back and SHA-256 compared with their ZIP members: both matched, at 467,826 and
465,940 bytes respectively. The job remained running without an error, and its
source ZIP still existed with `requires_review` set. The durable proof is
`/home/bobp/weazlcloud-import-watch/conflict-fix-readbacks-20260927.json`.

The archive and overall rehydration are not yet complete. Subsequent progress,
verification, healthy-source cleanup and the final landed/dedupe report remain
under the existing unattended watcher. No ongoing interactive monitoring is
required for this fix.

## Remaining scope

This addresses naming conflicts. The broader recovery workbook still includes
entry-level handling of unsafe ZIP paths/special files and further catalog and
queue performance improvements. Fatal storage errors must not be relabeled as
ordinary input conflicts or ignored.
