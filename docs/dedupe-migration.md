# Shared storage migration operations

The migration command is an offline operator tool. Stop all three WeazlCloud services before inventory, copying, or verification. It checks the default local service ports and takes an exclusive data-directory lock. It processes one file at a time and stores state under `/data/shared-index/`; a switched catalog entry is the authority when a process restarts.

The command unlocks each account with its stored node key and locks it again before moving to the next account. Disabled accounts are inventoried but skipped. Deleting accounts are ignored. Live files and Trash entries younger than 30 days are eligible. Older Trash remains in Restic for normal cleanup. No source snapshot is removed by this tool.

## Operator sequence

Run from the production host. Preserve the current Compose file and make a stopped, complete copy of `/exports/dockervolume/weazlcloud` using the host's backup process before migration. Keep that copy until recovery and rollback rehearsals pass.

```sh
cd ~/containers/weazlcloud
docker stop weazlcloud
docker run --rm --volumes-from weazlcloud weazlcloud:local -migrate dry-run
```

The JSON report gives account/file counts, expired Trash, unique legacy snapshots, staged records, pending resumable uploads, source allocated bytes, a destination upper bound that assumes no dedupe savings, and the peak per-file reservation. It flags an unassigned legacy root catalog. It never prints usernames, paths, hashes, or credentials. Resolve blocked accounts, staged uploads, pending resumable sessions, or an unassigned root catalog before proceeding. Dry-run reads catalog upgrades in memory and does not write the catalogs or account file.

After reviewing the report, start or resume the migration:

```sh
docker run --rm --volumes-from weazlcloud weazlcloud:local -migrate start
docker run --rm --volumes-from weazlcloud weazlcloud:local -migrate status
docker run --rm --volumes-from weazlcloud weazlcloud:local -migrate verify
```

`start` can be interrupted and safely rerun. `pause` writes a persistent pause marker; `start` then reports the paused state without copying. `resume` clears the marker and continues. The copier holds the legacy source while streaming it, checks source size and SHA-256, reserves conservative workspace against the global 97% volume limit, authenticates the destination, checks its full readback hash, and switches one encrypted catalog entry. A restart reconciles an interrupted shared-store operation from the catalog and journal.

The migrator leaves legacy Restic repositories intact. After a successful full verification, configure the application with `WEAZLCLOUD_STORAGE_BACKEND=shared-experimental` before starting the service so it opens both storage formats. Keep the backup and Restic repositories. The `retire` action is intentionally disabled until reverse migration and recovery acceptance are implemented and rehearsed.

## Current limitations

- Migration runs offline and serially. It does not run as an idle background worker while users continue working.
- A malformed or missing source hash/reference blocks that item; there is no automatic metadata repair.
- The inventory counts per-user staging records and resumable upload records. Any account with pending uploads is skipped so a session cannot later overwrite a migrated entry. Active archive/grab jobs cannot exist while the service is stopped. Completed sealed grabs are independent payloads and do not need source migration.
- The operator must review the dry-run report and ensure the destination and backup fit. The reservation is conservative per file; shared dedupe may reduce final allocation.
- Rollback to Restic is not implemented yet. Do not retire Restic snapshots or repositories.
