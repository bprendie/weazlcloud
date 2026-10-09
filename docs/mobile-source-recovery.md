# Album and folder source recovery

`POST /api/v1/photos/source-collections/lookup` is a read-only recovery lookup.
Discover `features.source_collection_recovery` in mobile capabilities first.
The authenticated owner session or scoped device (`photos:read`) determines the
catalog. The vault must be unlocked. Send `X-Weazl-Desk: 1` for this POST.

```json
{"source_ids":["opaque-album-id","opaque-folder-id"],"limit":200}
```

Optional `namespace` restricts to an exact source namespace. Omit it to search
all namespaces. Searches always include all of this owner's device IDs, including
revoked/previous devices: the current credential still must be valid. Owner and
device IDs cannot be supplied to redirect or narrow the lookup. Another owner's
catalog is never searched, including when the caller is an administrator.

```json
{
  "matches": [
    {
      "device_id": "previous-device-id",
      "namespace": "photokit",
      "source_id": "opaque-album-id",
      "kind": "album",
      "server_id": "pa_existing-album-id",
      "source_revision": "source-r3",
      "server_revision": 5,
      "current_server_revision": 8,
      "target_exists": true
    }
  ],
  "has_more": false
}
```

`server_revision` is the revision saved in the mapping. Server edits can advance
`current_server_revision` independently; recovery reports both without updating
anything. A deleted album/folder still returns its mapping, with
`target_exists: false` and `current_server_revision: null`. A source ID with no
mapping has no match. Asset mappings and album member IDs are excluded.

Limits: 1–200 unique nonempty source IDs, at most 4096 UTF-8 bytes each; optional
namespace at most 200 bytes; no NULs; body at most 2 MiB. `limit` defaults to 200
and accepts 1–200. Capabilities advertise `source_collection_lookup_items: 200`
and `source_collection_lookup_matches: 200`.

When `has_more` is true, repeat the same IDs/namespace with `cursor: next_cursor`.
An ID may match several devices/namespaces; consume every page. Order is stable
by an internal mapping key, not input order, newest device, or title. Cursors
are encrypted and bound to owner, query and endpoint; changing the ID order is
fine. Tampered/foreign/query-mismatched cursors return 400. Each page reports
current state under a catalog lock, but pagination is a live traversal, not a
snapshot. Concurrent additions earlier in key order require a fresh pass. Merge
by `(device_id,namespace,source_id)` and repeat recovery if source import continues.

The lookup leaves collections, nesting, memberships, mappings and receipts
unchanged. To continue syncing under the current device, adopt one selected
existing target with `POST /api/v1/photos/source-collections/recover`.
Discover `features.source_collection_recovery_write`; this POST requires
`photos:write`, an unlocked vault and `X-Weazl-Desk: 1`. Bearer requests always
bind the new mapping to the authenticated device. Owner-cookie callers must
include `device_id` for an active owner device with `photos:write`.

```json
{
  "operation_id": "unique-retry-stable-id",
  "from_device_id": "previous-device-id",
  "from_namespace": "photokit",
  "from_source_id": "opaque-album-id",
  "from_source_revision": "source-r3",
  "from_server_revision": 5,
  "namespace": "photokit-current",
  "source_id": "opaque-album-id",
  "server_id": "pa_existing-album-id",
  "expected_server_revision": 8
}
```

Use values from a single lookup match: `from_server_revision` is its recorded
`server_revision`; `expected_server_revision` is its current server revision.
The latter must be non-null and `target_exists` true. Supply the destination
source ID and namespace from the current device. A successful response is
`{"mapping":{...}}`, containing the existing server ID, the old source revision,
and the **current** server revision. Keep `operation_id` stable across retries.
The server stores a durable receipt, so an identical retry returns the current adopted mapping without repeating
the write, provided that target still exists. A different request using the same
operation ID conflicts.

The write verifies the old mapping, target ID, and current target revision
atomically with authorization. It adds only a current-device source mapping and
receipt; the old mapping, album, folder hierarchy, original files and all member
IDs remain intact. A current-device mapping for the same source identity already
in use is a conflict. Distinct targets sharing a source ID require client or
owner selection; do not adopt one automatically. `404 source_not_found` means the
selected old mapping is absent. `409 stale_revision` means the old mapping or
target changed/disappeared; run lookup again. `409 mapping_conflict` means the
new identity or operation ID is already occupied; do not overwrite it. Malformed
input is 400, locked vault 423, and authorization failures 401/403.

After adoption, use `mapping.server_revision` as the next source-operation
`expected_revision`. The copied source revision describes the last source version
that was already committed on the old device; it does not claim that newer phone
changes are synced. Import any changed source revision normally. Adopt parent
folders before subsequent album source updates that refer to those parents.
Adopting an album does not resend or prune its memberships.

The lookup still exposes deleted targets with `target_exists: false`; recovery
writes refuse them. A source ID with no match does not imply that the server
should delete or recreate an album. Pages are live reads, so repeat lookup if
source imports continue concurrently.

Responses are `Cache-Control: private, no-store`. Invalid input/cursor is 400;
invalid credentials 401; insufficient scope or missing request guard 403;
locked vault 423. Invalid bearer credentials never fall back to an owner cookie.
Retry after owner unlock without clearing local recovery state.

Validation: the read lookup and guarded adoption write passed `make check`,
including full race tests, vet, native helpers, Go length and JavaScript checks.
Focused race tests cover owner/device/scope isolation, durable replay, stale
revisions, deleted targets, save rollback, and unchanged album memberships and
folder hierarchy. A subsequent ordinary source update under the adopted device
was verified to keep the existing album ID, parent folder and member IDs.
OpenAPI parses with all local references resolved. The exact image passed
renderer, Restic/shared container, and 250 MiB mobile upload/restart/readback
smokes on both storage backends at 2 CPU / 4 GiB.

Production release `weazlcloud:release-30cf2f5` (runtime commit
`30cf2f5160e7a9d2f149a4c4ff57f1c8f4626ae1`, image ID
`sha256:c31d45fc17d4153470b6b9d71f8f32ab6078b797364acbbec166ca59ae8ded7b`)
was activated October 9 at 22:02:16 UTC. Both containers were healthy with
unchanged mounts, security, settings, ports and resource limits. API, Grab,
WebDAV and renderer readiness passed; public TLS/UI browser smoke passed; both
recovery routes required authentication on the live Desk. The API and worker
exited cleanly for cutover. A verified checkpoint of 76,927 files and six
canonical hashes is retained at
`/exports/dockervolume/weazlcloud-backups/source-recovery-write-20261009/data`.
Previous image `weazlcloud:release-b1dbfb8` remains available for code rollback.
No accepted staging or vault assets were reset. Production seed publication
resumes after the owner vault is unlocked following restart; that continuation
must be checked separately from HTTP readiness.
