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

Recovery leaves collections, nesting, membership lists, source mappings,
operation receipts and revisions unchanged. No device adoption/rebinding occurs.
Clients should restore their local association to an unambiguous, existing
`server_id`. Keep multiple distinct target IDs unresolved instead of choosing
one silently. Do not infer deletion from a missing match or automatically create
an album when `target_exists` is false.

Before later edits, reconcile current server metadata and use the appropriate
revision check. Existing source-import writes remain device-scoped; replaying old
source creations under a new device can create duplicates. This lookup does not
make old-device source operations writable with a new credential. Use existing
server-ID collection/membership APIs where appropriate, preserving memberships;
automatic source-namespace adoption would require a separate explicit contract.

Responses are `Cache-Control: private, no-store`. Invalid input/cursor is 400;
invalid credentials 401; insufficient scope or missing request guard 403;
locked vault 423. Invalid bearer credentials never fall back to an owner cookie.
Retry after owner unlock without clearing local recovery state.

Validation: `make check` passed (unit/race suites, vet, native helpers, Go length
and JavaScript checks). Recovery tests cover revoked-device discovery from a new
credential, owner/scope/vault isolation, cursor binding, bounded pagination,
asset exclusion, stale/current revisions, deleted targets, and unchanged catalog
bytes, collections, source receipts and memberships. OpenAPI parses and local
schema references resolve. This addition has not yet been deployed.
