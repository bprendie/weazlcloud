# Photo pre-upload content lookup

`POST /api/v1/photos/lookup` checks SHA-256 **and original byte size** against the
signed-in owner's published Photos catalog. It uses an in-memory checksum index;
warm lookups do not download originals, invoke Restic or generate previews. The
index is rebuilt from the catalog on first use after normal vault/storage
initialization and maintained as files change.

Use an account session or device bearer credential with `photos:read`, and send
`X-Weazl-Desk: 1`. The vault must be unlocked. Discover support through
`/api/v1/mobile/capabilities` (`features.photo_content_lookup`) or
`/api/v1/photos/capabilities` (`content_lookup`).

```http
POST /api/v1/photos/lookup
Authorization: Bearer <device-token>
X-Weazl-Desk: 1
Content-Type: application/json

{"items":[{"sha256":"<64 hexadecimal characters>","size":12345}],"include_hidden":true}
```

```json
{
  "generation": 42,
  "results": [{
    "sha256": "<lowercase SHA-256>",
    "size": 12345,
    "exists": true,
    "matches": [{
      "asset_id": "logical-photo-id",
      "revision": 1,
      "component_id": "original",
      "component_asset_id": "original-file-id",
      "hidden": false,
      "archived": false
    }],
    "match_count": 1,
    "has_more_matches": false
  }]
}
```

- Send 1–200 items per request, at most 64 KiB of JSON. Size must be a positive
  integer. Uppercase hex is accepted. Results preserve input order, including
  repeated queries. Unknown JSON fields and trailing JSON are rejected.
- Misses have `exists:false`, `matches:[]`, `match_count:0` and
  `has_more_matches:false`. Each result includes at most 20 matches, sorted by
  asset ID then component file ID. `match_count` counts every eligible match.
- Hidden files/folders are excluded unless `include_hidden:true`; this includes
  both normal and Hidden photos and requires no separate password. Archived
  photos remain eligible. Lookups never change either flag.
- Staged uploads, Trash, files outside Photos and sidecar JSON are excluded.
  Other users' vaults are never searched, even with shared storage deduplication.
- For Live Photos, query the still and motion bytes separately. Motion returns
  `component_id:"motion"` and the **parent** logical `asset_id`. Both components
  must match the same parent before treating a pair as present. A standalone
  video reports `component_id:"original"`. If matches are truncated, do not
  assume an unreturned pair exists; use the normal upload/reconciliation flow.
- Hash the exact original bytes that would be uploaded, using streaming hashing
  on the phone. A re-encoded/exported variant is different content.

This is a read-only observation, **not a backup receipt or reservation**. A match
can subsequently be deleted. It does not create a source mapping, album
membership, Hidden flag or durable device acknowledgment; reconcile those using
the existing APIs. Do not mark a full backup complete solely from this lookup.
It checks catalog identities, not the physical integrity of stored blobs.

Responses use `Cache-Control: private, no-store`. Invalid requests return 400
(`invalid_request`), invalid credentials 401, insufficient scope 403 and a locked
vault 423 (`vault_locked`). Retry after owner unlock; device tokens cannot unlock
vaults. Existing upload and receipt APIs remain the fallback for absent content.

## Local verification (2026-10-09)

`make check` passed, including the full Go unit/race suites, vet, native helper
checks, JavaScript checks and the Go file-length gate. Both OpenAPI documents
parse successfully.

Targeted tests cover owner and credential isolation, Hidden and archived entries,
Live Photo components, opaque originals, exact size matching, Trash/restore,
renames, replacement, invalid requests, cancellation, bounded responses and vault
lock cache clearing. Disposable Docker smoke tests passed on both `restic` and
`shared-experimental` with 2 CPUs/4 GiB, including real multipart uploads, lookup
identity and lookup recovery after restart, plus the existing 250 MiB transfer.

The metadata-only `BenchmarkPhotoContentLookup100K` measured approximately
0.20 ms per 200-item batch against 100,000 indexed records on a local i7-1365U.
This is a warm in-process result; it excludes first-load, HTTP and client hashing
costs and is not a production latency guarantee.

## Production rollout (2026-10-09)

Deployed `weazlcloud:release-091de04ab396` (commit
`091de04ab39687dd7f41abc599033f95b775f1ea`) to both the API and isolated renderer
at 19:39 UTC. Exact-image container smoke tests passed on both storage backends;
renderer probes and the mobile checks above passed again on this release image.
The transferred image ID matched locally and on production.

Both old containers drained with exit code zero before a fresh XFS reflink
checkpoint at `/exports/dockervolume/weazlcloud-backups/photo-lookup-20261009/data`.
Canonical state hashes and all 69,267 file sizes, modes and owners matched the
checkpoint. Vaults, uploaded originals, receipts and staged upload state were
preserved. Cutover took 23.7 seconds; image names were the only Compose change.
Runtime environment, mounts, ports, resource limits and container security
settings were verified unchanged.

Desk, Grab, WebDAV and renderer readiness passed. Public TLS readiness, exact UI
assets and browser login rendering passed; the public lookup rejects anonymous
requests with 401. No owner credential was used for a production content lookup.
Owner login/unlock is required after restart. The iOS app must call the new API;
deploying it does not automatically reconcile a failed seed or create backup
receipts for existing matches.

Release logs and configuration backups are in
`/home/bobp/weazlcloud-releases/photo-lookup-20261009` on production. Preserve
post-cutover uploads when rolling back; do not overwrite live data with the
pre-cutover checkpoint.
