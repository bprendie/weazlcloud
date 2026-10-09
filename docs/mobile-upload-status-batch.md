# Upload status batches for Astra

Implemented by the October 9 upload-responsiveness release. Discover support
before using it: `GET /api/v1/mobile/capabilities` returns
`features.upload_status_batch_v1: true`, `limits.upload_status_batch_items: 100`,
and `limits.upload_status_poll_seconds: 5`.

## Request and response

`POST /api/v1/photos/uploads/status` requires the existing device bearer token
with `photos:write` and the existing mutating-request guard (`X-Weazl-Desk: 1`).
Owner-cookie callers must supply their enrolled `device_id` query parameter.
The vault must be unlocked. Maximum body: 16 KiB; 1–100 unique 32-character hex
upload IDs. Unknown fields, trailing JSON, malformed IDs, and duplicate IDs are
rejected with HTTP 400. This endpoint handles `parts-v1` photo uploads only.

```json
{"upload_ids":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]}
```

HTTP 200, preserving request order:

```json
{
  "items": [
    {
      "upload_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "transfer": {
        "id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "transport": "parts-v1",
        "status": "queued",
        "components": [],
        "part_size": 16777216,
        "updated_at": "2026-10-09T21:00:00Z",
        "expires_at": "2026-10-10T21:00:00Z"
      }
    },
    {"upload_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","code":"not_found"}
  ]
}
```

The example omits component details for brevity. A real transfer uses the same
projection as `GET /api/v1/photos/uploads/{id}`. `stored` includes its existing
`result` receipt. Private keys, source metadata, and admission grants are never
returned. Missing, foreign-owner/device, and non-photo IDs all produce
`not_found`. Expired sessions produce `staging_expired`; corrupt sessions produce
`staging_corrupt`; other per-item read failures produce `status_unavailable`.
One bad ID does not fail the healthy items.

All responses use `Cache-Control: no-store`. Pending uploading/queued/verifying
items cause `Retry-After: 5`. HTTP 401/403 means authentication/scope failure;
423 means owner unlock is required. Do not reset the backup ledger for either.

## Polling and pressure

Poll only uploads awaiting final receipts, using one batch approximately every
five seconds. Back off to 10–15 seconds on unchanged results, with jitter. Honor
Retry-After. Stop polling stored/cancelled uploads; surface failed uploads for
recovery. A delayed status response must never cancel a healthy PUT. Keep
transfer lanes independent of finalization and batch timeline refreshes.

Until the feature is advertised, fall back to bounded, slower individual GETs.
Older servers may reject this literal route; do not repeatedly probe it or use
batch mode on legacy ordered uploads. File-backup batch status is not implemented.

Capabilities also advertise `recommended_upload_concurrency` (at most four
assets), `mobile_receive_workers`, `mobile_receive_workers_per_owner`,
`mobile_pending_uploads_per_owner`, and `mobile_pending_bytes_per_owner`.
These receiving limits are independent of finalizer concurrency. Multiple parts
or components may be received independently; sequential parts per file remain a
simple client policy. Do not multiply file lanes by an unbounded part pool.

The server may respond `429 {"code":"upload_busy",...}` with `Retry-After: 5`
when receiver capacity or the accepted-work backlog is full. Retry the same source
identity/part after backoff; keep accepted receipts and existing encrypted parts.
Admission pressure never discards already accepted work. This is separate from
HTTP 507 insufficient storage, 423 vault lock, 401 invalid authorization, and
422 checksum mismatch. New-upload coordination may already have allocated a
stable ID when admission fails; repeat the same create to reconcile it.

Backlog defaults are 512 active sessions per owner and declared component bytes
of `min(32 GiB,max(1 GiB,visible_RAM/4))`, reconstructed from durable live markers.
One original larger than this byte budget can be admitted alone, subject to disk
and quota checks: this is a backlog budget, not a maximum file size. An old seed
already above the new limits can continue receiving and finalizing its accepted
sessions; only new admission waits for room.

## Server diagnostics

Sampled `mobile receive` logs separate session admission, body read, file sync,
commit wait, and durable commit time. Slow/error requests also report authorization
check time, body-read time, HTTP protocol/TLS, and presence of proxy headers.
Header presence does not establish a trusted proxy or identify the network route.
Read time includes authorization and network time; commit includes its guard.
Nested timings must not be added together as independent stages. Logs contain
aggregate counts/bytes and fixed error classes, without private metadata or tokens.
