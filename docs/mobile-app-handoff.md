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
