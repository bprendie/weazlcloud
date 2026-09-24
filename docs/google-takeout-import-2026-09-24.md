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

Current job status is in memory; a process restart clears the progress display. The library catalog is the durable resume marker. Rerunning an archive checks existing content hashes and skips exact matches. The job fails on a conflicting path. The implementation preserves ZIP timestamps and uses one worker for bounded disk use. Per-photo JSON capture dates and EXIF rewriting are outside this album update.

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
