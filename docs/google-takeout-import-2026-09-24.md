# Google Takeout import — 2026-09-24

Goal: transfer nine roughly 50 GB Google Takeout ZIP files from a Windows PC on the same LAN, import their contents into the owner's browsable WeazlCloud library, then remove the server-side ZIP copies after verification. Keep the Windows copies until the full import is checked.

## Current state and space budget

- Production is `bobp@weazlcloud.teralab.local`. The data filesystem is `/exports/dockervolume`, mounted into the container at `/data` through `/exports/dockervolume/weazlcloud`. Do not stage on the 98 GB root filesystem.
- Private upload directory already created: `/exports/dockervolume/weazlcloud-import-2026-09-24` (owner `bobp`, group `7272`, mode `2750`). It is outside the live `/data` tree but on the same data filesystem. `/home/bobp/weazlcloud-import` links to it for FileZilla.
- Measured 2026-09-24: capacity 2,197,948,465,152 bytes; used 49,852,239,872 bytes; available 2,148,096,225,280 bytes. The application's 97% hard ceiling is about 2,132,010,011,197 bytes used, leaving about 2,082,157,771,325 bytes below the cap today.
- Budget example: 450 GB of source ZIPs plus 700 GB of imported physical data leaves about 932 GB below that cap. This is an estimate, not a guaranteed final footprint. Restic will reuse identical content within this user's repository, but do not assume a particular saving; already-compressed photos/video often compress little further. Current production uses per-user Restic repositories, so this is not cross-user dedupe.
- Space needed during import includes all retained ZIPs, per-file plaintext staging, Restic pack writes, metadata, and ordinary activity. Inspect actual ZIP totals and largest entries before import. Check free space before each archive and pause if projected peak would approach the 97% ceiling. Import one archive at a time; delete each server-side ZIP only after that archive is verified. If the real uncompressed total is far above 700 GB, transfer/import/verify/delete in smaller batches instead of staging all nine.

## Transfer now

1. In FileZilla, use **SFTP**, host `weazlcloud.teralab.local`, port `22`, user `bobp`, with the same SSH authentication that works from Windows. Choose the remote directory `/exports/dockervolume/weazlcloud-import-2026-09-24`.
2. Queue the nine ZIPs. Use one or two simultaneous transfers on the gigabit LAN; parallelism above that does not increase the link's total capacity. If a transfer breaks, resume the partial file rather than overwrite it from the start. Do not use an FTP connection or the browser upload UI.
3. After each transfer, compare its size and SHA-256 hash on Windows (`Get-FileHash -Algorithm SHA256 'D:\path\file.zip'`) and on the server (`sha256sum '/exports/dockervolume/weazlcloud-import-2026-09-24/file.zip'`). Do not import a partial or mismatched ZIP. A hash scan reads the file again, so it takes extra time, but it makes resumed transfers safe to trust.
4. Keep the ZIPs private. Production now gives only the container's group (`7272`) traversal of the staging directory (`2750`). SFTP creates uploaded files as `0600` here, so **after each ZIP transfer has finished and its hash matches**, run `chmod 640 '/home/bobp/weazlcloud-import/<filename>.zip'` on the server. The directory's setgid bit gives new files group `7272`; this chmod grants the read-only import mount access. Do not chmod the directory or ZIPs world-readable.

## Importer operation

The server build now has an owner-only Takeout page and `/api/takeout` job endpoint. Configure both `WEAZLCLOUD_IMPORT_DIR=/import` and `WEAZLCLOUD_IMPORT_OWNER=<vault username>`, and mount the staging directory read-only at `/import` inside the container. The owner must sign in and unlock the vault before starting each ZIP. Only one ZIP imports at a time. Import progress is visible on the Takeout page; after a process restart, select the same ZIP again to skip files already committed with matching hashes.

Each ZIP entry is classified independently: `Takeout/Drive/...` becomes `Google Takeout/Drive/...`, `Takeout/Google Photos/...` becomes `Google Takeout/Photos/...`, and other products become `Google Takeout/Other/...`. The sidebar Photos page displays imported photo and video files separately from Library grid view. JSON sidecars remain in Library next to the originals. The source ZIPs are never deleted by the app.

Current job status is in memory; a process restart clears the progress display. The library catalog is the durable resume marker. Rerunning an archive checks existing content hashes and skips exact matches. The job fails on a conflicting path. The implementation preserves ZIP timestamps. Up to eight files of at most 32 MiB stream concurrently into the existing Restic batch queue (at most 256 MiB of expanded input per wave); larger entries stream alone. Folder changes and repeated paths separate waves, preserving conflict checks. Progress accounts for all committed peers before a failed wave stops, so resuming remains safe. Per-photo JSON capture dates and EXIF rewriting are outside this album update.

## Photo albums

Photos now offers **All photos** and **Albums**. Each named folder directly under `Google Takeout/Photos` is an album, with a cover, item count, title and description. Opening a card shows that album's photos and videos; links support reload and browser back/forward. Year collections (`Photos from YYYY` or `YYYY`) stay in All photos unless explicit metadata names a different album. Known Trash and Failed Videos folders do not become albums.

The parser understands top-level `title`/`description` and nested `albumData` in `metadata.json`, plus known localized metadata filenames. Missing, malformed or oversized metadata falls back to the folder name; the original JSON is preserved. The folder is the stable album identity, so identical titles do not merge unrelated albums. Membership follows live library paths: it accumulates across ZIP parts and updates after moves, deletions and restores. Existing imports work without reimporting.

Album metadata is read during import and cached in `.weazl-photo-albums.enc` beside the user's catalog. The cache is encrypted with the user's vault and keyed by source path and content hash. It can be deleted and rebuilt; all album data is derived from library files. No photo/video bodies or individual photo sidecars are read to list albums. The album API uses the logged-in user's unlocked vault; the admin role does not grant access to another user's albums.

Export layouts vary; folder relationships cannot recover distinctions Google did not export (for example separate untitled albums combined into one folder). Parser compatibility references: [immich-go's Takeout format observations](https://github.com/simulot/immich-go/blob/main/docs/misc/google-takeout.md), [PhotoStructure's documented album metadata fields](https://photostructure.com/guide/albums/).

Validation: library tests cover metadata shapes, folder fallback, year exclusion, split-import membership, encrypted cache reuse after restart, metadata replacement, Trash and locked vaults. The Desk integration test imports an album ZIP and checks isolation from another account. `scripts/smoke-photo-albums.py` exercises the actual Docker/Chromium flow, including covers, preview, split ZIPs, deep links and restart (requires Python Playwright and Chromium).

## Remaining rollout checks

1. Confirm all nine ZIP transfers and compare Windows/server SHA-256 hashes. Leave the original Windows ZIPs in place.
2. Check actual compressed and uncompressed sizes and the largest entry. The job reserves twice each entry's expanded size plus overhead; ZIPs already staged on the same filesystem count toward the 97% limit.
3. Smoke test a small real archive or small sample on production, verify file counts, browsing, thumbnails, selected SHA-256 readbacks, and disk use. Then process the remaining archives one at a time.
4. Review each completed job and sample its library files before removing that ZIP from server staging. If an archive fails, leave its ZIP and select **Resume** after fixing the error.

Deletion is deliberately a separate step after each archive passes verification. The importer should never delete the only remaining source automatically on a failed or incomplete run.

## Evening batch — owner-authorized unattended run

The owner subsequently queued 14 more files after the first three completed
ZIPs. The initial count estimate was 17; the completed upload has 16 ZIPs (group 1: 3, group 2: 10, group 3: 3), totaling 765,737,951,268 compressed bytes and 770,738,737,579 expanded bytes across 95,476 file entries. The monitor was corrected to require at least 16 ZIPs matching `takeout-20260923T154520Z-*`,
including the final `takeout-20260923T154520Z-3-003.zip`. The entire batch must
remain unchanged across two 20-minute checks, have no missing part numbers,
and have no detected SFTP writer before import begins. It is run from bobp's
cron on production, using `scripts/takeout_watch/watch.py`; its private state,
log and configuration live in `/home/bobp/weazlcloud-import-watch`.

The runner signs in as bobp using the already-provisioned owner credentials;
it does not use an administrator bypass. Credentials are read directly from
the mode-0600 credentials file and never copied into logs or command lines.
It records source SHA-256, checks every ZIP entry's CRC, checks conservative
disk headroom before each ZIP, and submits one import at a time. It does not
claim to have compared Windows-side hashes, which were not provided.

The owner explicitly requested skipping corrupt data and clearing staging
after the readable data lands. `POST /api/takeout` with `skip_corrupt: true`
skips and reports source checksum/format/decompression errors. The default
remains strict. Quota failures, backend failures and content conflicts still
stop the job and retain the source for investigation. An unreadable ZIP
directory is recorded as an unreadable archive once transfer completion is
established. No outside source is fetched to replace damaged data.

Before removing a ZIP, the runner checks completed-job accounting, every
readable entry's library path and size, and SHA-256 readbacks of sample files.
It persists the verification record first, checks that the ZIP has not changed
or reopened for writing, and removes only that frozen batch member. A restart
resumes from its state; if the app lost its in-memory job, the same ZIP is
submitted again and the catalog skips existing matching files.

`report.md` and `report.json` contain landed file/byte totals, exact-file
dedupe savings from the existing library metric, actual allocated Restic
repository bytes, skipped corruption counts, source ZIP space freed and disk
space remaining. `corrupt-files.json` contains paths and errors. Physical
storage includes compression, chunk dedupe and metadata, so it is reported
separately from exact-file dedupe. Windows originals remain untouched.

Verification: `python3 -m unittest discover -s scripts/takeout_watch -v` and
`WEAZLCLOUD_IMAGE=<fresh-image> python3 scripts/smoke-takeout-watch.py` cover
the waiting gate, integrity failures, owner API, readable-file verification,
guarded cleanup and final report. The Docker smoke needs local port 7272 free.

During the live transfer the data disk developed long write queues: durable
readiness probes took around two minutes although `/live` still answered
immediately. Readiness now allows one outstanding filesystem probe per
listener, caches its result for five seconds, and bounds each HTTP wait to two
seconds. Slow or failed durable writes still return an unavailable status;
timed-out health requests no longer create an accumulating queue of probes.

The first production Photos import measured roughly one file per second when
submitting entries serially. Small-entry concurrency now feeds the existing
durable batch queue; CRC validation, per-entry quota reservations, catalog
hash checks on resume, and verified-source cleanup are unchanged. Large
entries are streamed to disk, including the 35 GB file in this batch.
