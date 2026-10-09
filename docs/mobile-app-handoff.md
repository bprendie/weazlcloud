# Native app server handoff — October 2, 2026

Use [mobile-api.md](mobile-api.md), [mobile-api.yaml](mobile-api.yaml) and
[validation evidence](mobile-server-validation-2026-10-02.md) as the current
server contract. The scaffold's original gap table describes the earlier
baseline. Server availability does not prove PhotoKit or iOS background behavior.

| Gap | Server implementation | Native work still required |
| --- | --- | --- |
| B1 | Explicit device scopes, stable IDs, retry-safe rotation and owner reauthorization; discovery/capabilities expose instance identity. | Keychain credentials; persist a fresh replacement secret before rotation; reconcile lost replies and blocked authorization. |
| B2 | Revisioned virtual collection folders and album parents; encrypted opaque source mappings and bounded source/membership reconciliation. | Map PhotoKit albums/folders without flattening; submit parents first; retry individual conflicts and dependency failures. |
| B3 | Device sources, immutable source-revision receipts and destination CAS; empty files/folders and owned renames supported. | Persist source identity/bookmarks; supply expected entry/revision for changes; surface conflicts; never mirror phone deletion. |
| B4 | Independent encrypted 16 MiB parts; durable server auto-finalize; bounded missing pages and verified-byte counters. Ordered Photos transport remains available. | File-backed URLSession tasks, bounded phone staging and physical-device background/iCloud retrieval testing. |
| B5 | Stable-ID content/HEAD/Range/ETag and Files snapshot/delta/checkpoints; existing Photos sync plus collection snapshot/delta; retry-safe frozen grabs. | Protected offline manifests/pins, content revision reconciliation and access-loss/Hidden eviction. Offline downloaded bytes cannot be remotely recalled. |

For new enrollment request only the chosen capabilities: `photos:read`,
`photos:write`, `files:read`, `files:write`, `backup:write`, `grabs:read`,
`grabs:write`, `storage:read`. Backup routes require both backup and Files write;
minting also requires read access to the selected source. A legacy Photos token
never becomes a universal token. No device credential unlocks a server vault,
enrolls another device, administers users or changes the grab hostname.

Send `X-Weazl-Desk: 1` for mutations. Negotiate
`X-Weazl-Mobile-Contract: 1` and inspect features/grants before using additions.
Trust home/remote aliases only through explicit client configuration, valid TLS
and matching `instance_id`; discovery is not a substitute for transport trust.
Use the returned canonical grab URL and existing QR endpoint.

For Photos or file backups choose `transport: "parts-v1"` and
`commit_when_complete: true`. Creation returns the logical `receipt` and a
`transfer` using the same ID. Send each part to
`.../uploads/{id}/components/{original|motion}/parts/{index}` with exact
Content-Length and `X-Weazl-SHA256`. Verify the server's accepted-byte counters;
a successful part response is not a stored-original receipt. Server finalization
continues without a client poll, while authorized and owner-unlocked. Retain
phone originals until the durable receipt reports stored. Preview and source
organization outcomes are independent of byte preservation. Zero-byte file
backups need no parts. Photos still/motion pairs publish atomically.

Opaque source key vector: encode namespace and ID UTF-8 bytes, each prefixed
with its unsigned 64-bit big-endian byte length; SHA-256 the concatenation and
base64url encode without padding. Namespace `photokit` and ID
`A/B+C==/L0/001` produce
`ytHTxxSsOndQxguM6jcAy19XNHi2TxPQDnRz1ktX-aM`. This is identity, not a
content hash. Raw source IDs remain encrypted server metadata, never paths.

The iOS plan is `/home/bobp/Code/iOS/weazlcoud_app/BUILD_PLAN.md`. Its physical
gate still covers limited Photos permissions, cloud-only originals, Live Photos,
DNG, inaccessible providers, reboot/termination/force-quit, network changes,
phone storage pressure and credential rotation with queued tasks. Do not label
these proven by server or desktop smoke results.

## iOS Hidden uploads — October 9, 2026

Hidden is a Photos visibility scope inside the user's vault. Once that vault is
unlocked, the same authorized account/device can open Hidden. Do not add a second
server password, a separate vault, or a public/anonymous access exception.

For assets available to the app through PhotoKit, preserve `PHAsset.isHidden`
as the initial Photos upload's `hidden` boolean. Hidden fetches require
`PHFetchOptions.includeHiddenAssets = true`; respect the user's backup choices
and the assets/permissions iOS actually makes available. See
[Apple's Hidden fetch option](https://developer.apple.com/documentation/photos/phfetchoptions/includehiddenassets).
A folder/album display name of `Hidden` is not a privacy signal to the server.
Do not infer privacy from localized album names or wait until after upload to
hide an otherwise public-to-the-owner timeline item.

Use the ordinary Photos root and include these fields alongside the original
identity, hash, components, capture date and albums:

```json
{
  "root_id": "root:photos",
  "hidden": true,
  "transport": "parts-v1",
  "commit_when_complete": true
}
```

This is a partial specification, not a complete upload request. The server commits
the still/motion pair and initial Hidden state atomically. Originals keep stable
paths under `Photos/Mobile/...`; Hidden is the view, not a required physical
folder. Album memberships survive hiding. A normal album containing Hidden
assets must not expose those assets or use them as its normal cover.

Browse with `GET /api/v1/photos?mode=hidden`. For the asset original, thumbnail,
collections and memberships use their documented explicit Hidden context
(`hidden=1` where supported). Keep normal and Hidden checkpoints/caches separate.
The existing vault lock and credential revocation still deny private access.
Sharing Hidden photos retains the explicit sharing confirmation.

Hidden is part of immutable upload intent: retries must send the same value.
Changing it on a replay returns 409. After publication, use the existing photo
visibility action when the owner intentionally changes visibility; don't create
another original just to change Hidden status.

Local validation: `TestMobilePartsHTTPAutonomousHiddenOriginalAndRevoke` exercises
an autonomous encrypted upload, normal-view exclusion, Hidden timeline presence,
original byte access with the existing credential, normal-context original 404,
idempotent receipt recovery and revoked-device rejection. This is server evidence;
the shipping app lives on an inaccessible Mac and was not inspected in this pass.

## Live upload trace and recommended work — October 9, 2026

Production investigation was read-only, while the app was uploading. The service
image was `weazlcloud:release-769db5f81918`; the proxy reported Traefik 3.7.13.
No service/proxy restart or configuration change was made during this trace.

Five-minute HTTP sample, **16:20:43–16:25:43 UTC**:

| Observation | Evidence |
| --- | --- |
| Upload polling dominates request count | 1,240 upload-status GETs and 1,344 missing-parts GETs, out of 3,115 requests. |
| Incoming original parts | 62 PUTs: 54 succeeded, eight returned 503; p95 including failures was 60,001 ms. |
| Live Photo motion parts | Three succeeded, taking roughly 57–58 seconds. |
| Upload creation stalls | 42 creates; median 2,233 ms, p95 3,895 ms. |
| Unrelated browsing stalls too | Photos seek p95 3,454 ms; album listing p95 3,368 ms. |
| Actual network progress | A separate 25.08-second sample received 70.19 MB, averaging 22.39 Mbit/s (2.80 MB/s), across the container. This is not a LAN/Wi-Fi capacity measurement. |
| Resources | Roughly 1.5 TiB disk space free. The main container burst to about 1,590% CPU of its 16-core limit while the preview worker was nearly idle. A later 25-second sample averaged 1.26 cores: contention is bursty, not constant. |

The HTTP logs omit bodies, headers, query strings and response error details.
They do not identify which GETs came from the app versus an open web UI, reveal
whether iOS sent `hidden: true`, or prove the phone's Wi-Fi/iCloud bottleneck.
The numbers above are requests, not counts of completed logical photos.

### First: stop the 60-second upload failures

The inspected proxy's `https` entry point has no responding-timeout override in
its mounted static file, CLI arguments or Traefik environment. Traefik's documented
default `readTimeout` is 60 seconds for the whole request, including its body.
Eight failures at almost exactly that boundary make it the leading cause; a
slow-transfer reproduction and client task error/metrics are still needed to
prove the individual failures. A 16 MiB part needs about 2.24 Mbit/s per transfer
to finish within 60 seconds, before overhead.

Merge this into the existing **static** `entryPoints.https` block for a planned
proxy change, preserving the other entry points, ACME and routing configuration:

```yaml
entryPoints:
  https:
    address: ":443"
    transport:
      respondingTimeouts:
        readTimeout: 10m
```

This is a proposed setting, not deployed configuration. It affects all routers
using that entry point and requires a proxy restart. It does not belong under a
router or dynamic `serversTransport`; response-header timeouts address a different
leg of the request. Keep a finite limit. Validate a deliberately slow upload
lasting over 60 seconds, retry/resume and another hosted service before declaring
it fixed. See [Traefik's entry-point reference](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/).

### App agent: reduce overhead and separate the stages

1. Start with **three concurrent asset transfers**, with **one active part per
   logical upload**. The current server holds a session mutex while reading an
   entire part, so parallel still/motion or multiple parts of the same asset do
   not run independently. Parallelize across assets, then tune from device metrics.
2. Use the PUT response's accepted counters. Query missing parts at resume or
   after an ambiguous failure, not continuously. Stop polling terminal jobs.
   Poll pending commits with bounded concurrency and backoff, for example
   2, 5, 10, then 30 seconds; pause the polling loop when the app is suspended.
   Polling does not drive server finalization.
3. Free the network upload slot once every part is durably accepted. Track server
   commit in a separate, bounded pending queue and continue the next asset.
   Preserve originals and retry state until `stored`; do not count merely queued
   work as backed up. Apply backpressure if the commit backlog grows.
4. Separate **iCloud retrieval → local preparation/hash → upload → server commit
   → preview** in the status display and metrics. Prefetch a small bounded set of
   cloud originals while uploading ready ones. Keep upload slots available to
   locally ready assets, with bounded staging bytes and fair queue scheduling.
5. Use file-backed background URLSession upload tasks, persistent IDs and delegate
   callbacks. Keep part files until acceptance/reconciliation; don't encode an
   entire movie in memory. Verify request/resource timeout settings and whether
   `isDiscretionary` is appropriate for an explicitly requested backup. Apple's
   [background transfer guidance](https://developer.apple.com/documentation/foundation/downloading-files-in-the-background)
   requires file-backed uploads for transfers that survive app exit.
6. Debounce collection/timeline reloads during ingest. Do not refetch the whole
   library or query capabilities after each progress update. Recover a stale
   cursor through the documented restart path without tight retry loops.
7. Record per-stage timing, task bytes, task errors, endpoint/status, retry count,
   HTTP protocol and connection reuse with `URLSessionTaskMetrics`. Log no bearer
   tokens, filenames, source IDs or Hidden content. Compare an on-device original
   and a cloud-only original over the same connection, plus a large Live Photo.

The negotiated `parts-v1` nonfinal part size is fixed at 16 MiB. Do not silently
send smaller parts to work around the proxy timeout; variable-size negotiation
would require a new server contract. Do not raise concurrency blindly while
requests are already hitting the timeout boundary.

### Server agent: eliminate unnecessary work around new uploads

- The persistent Restic helper loads its index once at startup. New packs can
  miss that index; `thumbnailSource` deliberately falls back to fresh CLI reads.
  Live process samples showed several concurrent `restic dump` children each
  using multiple cores while the renderer idled. Refresh the reader's index
  safely after new writes, coalescing refreshes across a batch and preserving
  active reads, reference holds and memory limits. Avoid restarting it once per
  photo or increasing worker counts to hide repeated repository opens.
- `StorePhotoComponent` holds `Library.mu` across the backend write. Reads and
  upload creation also need that lock. Move slow byte storage outside the broad
  library mutation lock with explicit reference/operation ownership, then take
  the narrow lock for CAS-checked publication. Preserve deletion, revocation,
  vault lock, dedupe, crash recovery and still/motion atomicity.
- The current finalizer handles one logical upload per owner per scheduling round.
  Small media incurs separate backend operations and catalog writes. Evaluate
  bounded batch commits or a long-lived writer before adding more same-owner
  finalizers that would queue behind the same lock. Keep small-host fallbacks.
- Add safe stage metrics for staging, verification, backend write, catalog commit,
  persistent-reader hit/miss/fallback and preview queue latency. Current endpoint
  timings cannot assign an exact percentage of delay to these stages.

Acceptance: a mixed normal/Hidden/Live Photo batch keeps Hidden out of all normal
projections; browsing remains responsive during ingest; slow parts survive the
configured timeout window; lost replies resume without duplicate originals;
finalization continues after the app closes; additional photos don't force a
fresh Restic CLI read for each preview. Test vault lock, revoked credentials and
a two-CPU/four-GiB deployment as well as the large host. These are recommended
follow-up changes, not claims that production has already been optimized.
