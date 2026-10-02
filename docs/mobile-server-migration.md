# Mobile server migration and recovery runbook

October 2, 2026. Implementation notes and operator recovery templates. The
[production release record](mobile-server-release-2026-10-02.md) gives the executed
upgrade, recovery copy and integrity audit. Physical iOS testing is separate.

## Pre-release baseline and scope

Operator-supplied read-only observations: production app and photo worker were
healthy on `weazlcloud:release-813d258`; pre-upgrade repository HEAD was
`e3d5ae0`, with dirty custom Compose JSON. Production data is
`/exports/dockervolume/weazlcloud`, approximately 645 GiB of owner Restic library,
with approximately 1.4 TB free on XFS. No active upload staging or import watcher
was observed. These observations are not a drain guarantee: recheck immediately
before stopping services. The operator reports that GNU `cp --reflink=always`
passed on a 1 MiB XFS test, `cmp` verified the copy, and the probe was removed.
The sibling directory `/exports/dockervolume/weazlcloud-backups` was created with
mode 0700. The later full consistent copy and deployment passed; see the release record.

Local implementation baseline is `a077baf336764ead17cbe983e44907a89efa16fd` plus the
implementation later released as `15c45144b660eb5d673851d1ca0ae51396407116`.
Preserve the production custom configuration. Never pull over conflicting local
work or replace the custom Compose file with the repository example.

## Formats and exact paths

Let `DATA` be the configured data root and `OWNER` the 32-hex account ID.
Production `DATA` is the path above; the image mounts it at `/data`.

| State | Path / supported format |
| --- | --- |
| Users/devices | `DATA/users.json`, document `state_version` 1/2; legacy missing state version handled by the user reader |
| Owner vault/node wrapper | `DATA/users/OWNER/vault.json`, `node.key`; retain both with the data snapshot |
| Owner catalog | `DATA/users/OWNER/catalog.enc`, owner-vault-wrapped JSON; legacy schema 0/1 and schema 2 |
| Catalog upgrade recovery | `catalog.enc.pre-schema-2.enc` alongside that catalog; exact encrypted input, mode 0600 |
| Restic originals | `DATA/users/OWNER/library/`, unchanged by the catalog schema upgrade |
| Photo logical receipts | `DATA/users/OWNER/.weazl-photo-ingest/ID.enc`, encrypted receipt version 1 |
| Photo component intents | `DATA/users/OWNER/.weazl-photo-components/KEY.enc`, encrypted version 1 storage identity/operation; removed after pending catalog reference settlement |
| Native parts/jobs | `DATA/users/OWNER/.weazl-mobile-parts/ID/session.enc`, encrypted version 1; immutable part manifests and authenticated `.wza` bytes in that directory |
| Ordered uploads | `DATA/uploads/OWNER/ID.json`; legacy plaintext JSON format 0 or ASCII `WZU2` followed by newline and owner-wrapped JSON format 2; encrypted segments live under `ID.part/` |
| Backup sources/receipts | `DATA/users/OWNER/.weazl-backups/KEY.enc`, encrypted version 1 records |
| Photo processing queue | `DATA/users/OWNER/.weazl-photo-jobs.enc` and `.weazl-photo-jobs.journal.enc`; snapshot version 1 plus authenticated journal |
| Shared store | `DATA/shared-index/`, shared object/chunk directories and `shared-staging/`; snapshot the entire data root, not selected catalogs |
| Discovery identity | `DATA/instance_id`; retain for the same node, intentionally regenerate for an independent clone while stopped |

Schema 2 encodes `files` as `{"entries":[...]}` rather than a bare array. It also
persists collection folders, album parents, source mappings and operation receipts.
Old binaries expecting an array fail catalog decoding instead of silently losing
these fields. All file JSON consumers use the catalog decoder or `Catalog` APIs;
a files-only projection must never be marshalled back as a catalog.

Future catalog schemas, photo receipts, component intents, ordered manifest
formats and native session versions fail closed. Migration inventory does not
reconcile, expire or rewrite upload manifests. It counts ordered unfinished
sessions and native `uploading`, `queued`, `verifying`, `failed` sessions;
`complete` ordered sessions and `stored`/`cancelled` native sessions do not block.
Unreadable, oversized or future native manifests block inventory rather than
appearing idle.

## Lazy upgrade and crash recovery

An owner-unlocked catalog load validates/decrypts the whole supported document
before writing anything. Read-only loads upgrade only in memory. A writable
legacy load first atomically saves the exact ciphertext to the recovery path,
fsyncs it and its directory, then atomically publishes schema 2. Original media
is neither reimported nor relocated. Existing album IDs, revisions and memberships
remain at collection root; virtual parents never change physical Hidden ancestry.

A crash before catalog publication leaves the legacy catalog plus its encrypted
recovery copy; retry uses that same copy. A successful schema-2 reload performs no
rewrite solely for schema conversion. If an existing recovery copy differs from
the legacy input, migration stops with `ErrMigrationRecoveryConflict`; investigate
snapshot provenance while stopped. Do not delete or overwrite the copy to force
an upgrade. Unknown future schemas leave both the input and recovery path alone.
A directory-sync error after rename has an uncertain publication outcome: reload
from disk before retrying a mutation, rather than trusting old in-memory state.

The recovery copy is **catalog-only**. It is not a sufficient downgrade backup.
Never restore it alone over newer shared references, credentials, receipts or
staging. Prefer a forward fix. To run an old image, restore an entire consistent
pre-upgrade data snapshot with its matching configuration/images while stopped.
Any upload, metadata edit, device enrollment/rotation, share or deletion after
that snapshot is lost by restoration. Client sync checkpoints from the abandoned
branch require a fresh snapshot when the journal rejects them.

## Local fixture commands and evidence

These are repeatable local tests using temporary fixtures, not production paths:

```bash
go test ./internal/catalog -run TestCatalogMigration -count=1
go test ./internal/migration -run 'TestMigrationRun|TestPendingUploads' -count=1
go test ./internal/library -run 'TestUnsupportedOriginal|TestCollectionSnapshots|TestPhotoAndSourcePublication' -count=1
go test ./internal/photoingest -run 'TestPartsReceipt|TestSharedParts|TestOrderedPhotoFinalize|TestPhotoReceiptFuture' -count=1
go test ./internal/desk -run 'TestNativePhotoSimulator|TestVersionedPhotoUpload|TestMobileCollectionsDispatcher|TestMobilePartsHTTP' -count=1
```

The migration fixture restores the same pre-upgrade ciphertext and migrates twice,
comparing IDs, revisions, counts, memberships, hashes, Hidden/capture/favorite
metadata, references and logical/unique/trash accounting. It compares exact bytes
and byte totals of its synthetic encrypted media/receipt files. This is not a
measurement of production physical dedupe savings. Separate native simulator and
shared-parts tests exercise actual Restic/shared byte reads, backend selection,
reference identity, shared dedupe and encryption of live shared source staging.
Native cancellation recovers a published logical pair as a stored receipt and
returns conflict, including the catalog-before-receipt crash window. Unpublished
cancellation durably tombstones that source revision, forgets private component
rows and releases shared owner references; repeated cleanup is safe. Shared payload
collection and Restic pruning remain later maintenance, so cancellation does not
promise immediate physical disk reclamation. Stored or source-deleted originals
are preserved. The shared fixture verifies doubled logical bytes with unchanged
unique chunk bytes/count, rather than comparing private encrypted manifest IDs.
It rereads the canonical original bytes after pending cleanup. Cancellation after
catalog publication is also tested on Restic, with the original bytes preserved.
Future schemas, recovery-write failure, recovery conflict, the crash boundary
before publication and refusal by an old array-only writer have dedicated checks.

The image pins `restic/restic:0.18.0`. Native component writes use an opaque flat
entry ID as `--stdin-filename`, preserving the nested logical path only in the
catalog. This avoids that version's requirement for existing parent directories;
no plaintext directories or assembled originals are created. The regression
checks immutable reference reuse, atomic publication and reading the original
through its flat storage reference. Local validation used the binary extracted
from that pinned image, including a race-enabled regression. To repeat locally:

```bash
task_restic_dir=$(mktemp -d)
RUNNER_TEMP="$task_restic_dir" bash scripts/ci-install-restic.sh
PATH="$task_restic_dir:$PATH" go test -race ./internal/library \
  -run '^TestPhotoComponentResticUsesFlatStorageKey$' -count=1
```

These fixtures cover migration and Photos failure boundaries. Full-suite,
container/resource, account lifecycle and backup/transport results are in the
[validation record](mobile-server-validation-2026-10-02.md). Physical-device
verification remains separate; local fixtures do not measure production throughput.

Schema-2 collection cursors are encrypted, domain-bound (`photo-collections`),
versioned and bound to `hidden=1` versus normal context. Old/future/other-domain
cursors require reset. Snapshot pages share an exact journal baseline; mutation
invalidates continuation. Completed snapshots issue delta checkpoints. Delta
pages advance through at most the requested 1–200 journal records, even when
filtering unrelated records produces an empty page. Collection headers/deltas
contain no media membership or cover IDs; asset mappings are excluded. Fetch
memberships separately in the explicit visibility context. Legacy Photos sync
skips collection/source-mapping record kinds.

Lock order for Photos, sources and backups is `library -> users -> catalog`.
Bearer finalization captures Photos-write authority, binds to the vault session,
and guards the final catalog CAS after stream verification. Revocation cannot
publish a new logical asset. Parts and ordered Photos stream to the configured
backend without an assembled plaintext `.staging` copy. Unsupported originals
remain readable originals but return `preview_unsupported: true` and no derivative
identity; thumbnail paths reject them before cache lookup or decoder work.

Transport `CancelCoordinated` may hold its job gate while calling photo cleanup;
the photo coordinator never calls back into the transport engine. A nil cleanup
result means a durable cancelled revision. If photo cancellation returns
`ErrIdempotencyConflict`, read the recovered stored photo status and return it as
the stored result, so transport records publication instead of cancellation.

## Production operation templates — NOT EXECUTED

The operator must select the real custom Compose JSON path and verify its service
names. The repository names below are defaults, not a claim about the custom file.
Require a clean or explicitly preserved source checkout before the later pull;
record any dirty build inputs separately from the embedded commit identifier.

```bash
export MOBILE_COMPOSE_JSON=/absolute/path/to/existing-custom-compose.json
export MOBILE_APP_SERVICE=weazlcloud
export MOBILE_WORKER_SERVICE=weazlcloud-photo-worker
export MOBILE_SNAPSHOT=pre-mobile-2026-10-02-unique-suffix
: "${MOBILE_COMPOSE_JSON:?}" "${MOBILE_SNAPSHOT:?}"
docker compose -f "$MOBILE_COMPOSE_JSON" config --services
docker compose -f "$MOBILE_COMPOSE_JSON" ps
```

First save the existing JSON (including its custom settings), record current
image IDs/digests and the Git state, then update/build while the old containers
remain running. Use an ephemeral root Debian container for copies when host sudo
is unavailable. The backup location is outside the live data tree but on the
same XFS filesystem. A reflink snapshot shares blocks initially; later writes
consume CoW blocks. Quota uses actual filesystem headroom and reservations;
`du` totals of copied references do not prove new physical allocations or savings.

```bash
# Save configuration before any pull/build. No service stop yet.
docker run --rm --user 0 --mount type=bind,src=/exports/dockervolume,dst=/vol \
  --mount "type=bind,src=$MOBILE_COMPOSE_JSON,dst=/input/compose.json,readonly" \
  debian:bookworm-slim sh -eu -c \
  'mkdir -p "/vol/weazlcloud-backups/$1"; cp -a /input/compose.json "/vol/weazlcloud-backups/$1/compose.before.json"' sh "$MOBILE_SNAPSHOT"
# Only after preserving/resolving dirty source work:
git pull --ff-only
commit=$(git rev-parse HEAD)
short=$(git rev-parse --short HEAD)
docker build --build-arg VERSION=mobile-server-2026-10-02 \
  --build-arg COMMIT="$commit" -f deploy/Dockerfile \
  -t "weazlcloud:release-$short" .
docker run --rm --entrypoint /usr/local/bin/weazlcloud \
  "weazlcloud:release-$short" -version
# The small reflink probe passed; recheck workloads and verify drain before stop:
docker compose -f "$MOBILE_COMPOSE_JSON" stop "$MOBILE_APP_SERVICE" "$MOBILE_WORKER_SERVICE"
docker run --rm --user 0 --mount type=bind,src=/exports/dockervolume,dst=/vol \
  debian:bookworm-slim sh -eu -c \
  'test ! -e "/vol/weazlcloud-backups/$1/data"; cp --reflink=always -a /vol/weazlcloud "/vol/weazlcloud-backups/$1/data"; sync' sh "$MOBILE_SNAPSHOT"
```

If the copy fails, do not continue deployment. Keep the snapshot incomplete and
inspect the failure; never silently fall back to a large conventional copy.
After successful snapshot completion, change **only** the app/worker `image`
references in the preserved custom Compose JSON to the validated candidate tag.
Compare before/after JSON: mounts, environment, commands, networks, resources,
health checks and security settings must be identical. Save the final JSON beside
the snapshot, then run:

```bash
docker compose -f "$MOBILE_COMPOSE_JSON" config
docker compose -f "$MOBILE_COMPOSE_JSON" up -d --no-build "$MOBILE_APP_SERVICE" "$MOBILE_WORKER_SERVICE"
docker compose -f "$MOBILE_COMPOSE_JSON" ps
docker compose -f "$MOBILE_COMPOSE_JSON" logs --tail 100 "$MOBILE_APP_SERVICE" "$MOBILE_WORKER_SERVICE"
```

Verify both health checks and the main binary's version/commit, preserve the
mounts, unlock as the owner when required, and verify IDs/counts, byte reads,
Hidden pairs, collection pagination, resumed jobs and existing frozen grabs.
Record snapshot path, image IDs, source state, free space, duration and outcomes.
Do not delete the pre-upgrade snapshot or encrypted recovery copies in this gate.
If forward recovery is impossible, stop both services and restore the **complete**
snapshot and its matching Compose/images; retain the failed live tree for analysis.
There is intentionally no template that rolls an old image onto migrated live data.
