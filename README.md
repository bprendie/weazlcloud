# WeazlCloud

A household library node. Your files on metal you own. Send recipient a grab link.
Mount the same library in Files over `davs://`. Accounts and vaults stay on your own host.

## Photos and music in the library

See the [October 2 Photos selection release](docs/release-2026-10-02.md) for
current behavior and verification.

Hover over a photo to reveal its selection circle (always visible on touch).
Select one, then click more tiles or Shift-click a range. The selection bar offers
**Archive** to remove photos from the timeline and **Hide** to keep them out of
normal Photos search and albums too. Restore them from **Archive** or **Hidden**;
Escape clears selection. Originals and album membership are retained. Hidden
photos remain accessible in Library and do not have a separate password.

Photos has a continuous capture-date rail: drag to a month, choose a day, or use
the keyboard to move through the timeline. Album, Favorites, Hidden and search
views keep their own scope. Dates come from metadata; undated files stay in
**Unknown date**. Open the **☰ Photos menu** for date inspection/repair,
duplicate review and preview preparation. **Repair dates** runs on the server with pause/resume and retry controls. It preserves
originals and owner corrections. JPEG EXIF and valid Takeout sidecars are the
current capture sources; import dates are never substituted. See the
[timeline verification record](docs/photos-timeline-verification-2026-10-01.md)
and [Photos API](docs/photo-api.md) for capabilities and safe repair.

Date repair shares one authenticated Restic index across parallel metadata
readers, with bounded memory and a fallback for small hosts. See the
[extraction performance record](docs/photos-metadata-performance-2026-10-01.md)
for the production benchmark and operating limits.

**Photos → Albums** recognizes named albums imported from Google Takeout.
Photos imports live under `/Photos`; Drive imports go directly to the library
root. Album titles and descriptions come from the album metadata, with folder names
as a fallback. Importing another ZIP extends the same album; folders with the
same displayed title stay separate. Yearly collections stay in **All photos**.
The original files and JSON sidecars remain in the library. See the
[Takeout import guide](docs/google-takeout-import-2026-09-24.md) for staging,
verification and source ZIP cleanup. ZIPs with corruption, a failed attempt, or
failed verification are retained for review even after a successful retry.

Takeout imports batch up to eight small files through the encrypted storage
queue. Files larger than 32 MiB stream individually, so large videos, disk
images and ISO files stay out of application memory. Interrupted imports
resume by checking the hashes of files already committed to the library.

Filename conflicts preserve both versions: incoming files receive a stable
`(takeout-…)` suffix, and a file blocking an incoming folder gets a separate
folder for the imported tree. Retries reuse those destinations after checking
content hashes. The batch continues, with source-to-destination mappings in its
audit record. ZIPs containing these conflicts remain staged for review; cleanup
requires verification of the renamed content too.

**Library → Grid** shows embedded music cover art, title, artist, album and
available genre, date and track tags, alongside the filename and playback
controls. Supported tags: MP3 (ID3v2.2–2.4), FLAC, M4A/iTunes, Ogg/Vorbis and
Opus. Artwork uses embedded JPEG, PNG or GIF images. Missing, malformed or
unsupported tags retain a music icon; playback depends on the browser's codec
support. WAV, raw AAC and ID3v1-only tracks currently use that fallback.

Previews load as cards approach the viewport. Music metadata and scaled covers
are cached encrypted inside the owner's vault storage; replacements invalidate
the cache. No artwork service, external lookup or local helper is required.
Audio is streamed through the server's metadata reader, never loaded wholesale
into memory or extracted to a temporary plaintext file. Metadata is limited to
8 MiB, artwork to 16 million decoded pixels, and cold reads to 30 seconds. An
M4A file with metadata after the audio may take longer on its first preview.

**Photos** uses a private, encrypted index and loads the first 100 photos without
listing the whole library or reading original image bytes. Use **Photos → ☰ → Prepare previews**
to build grid and viewer previews together in the background; preparation is opt-in,
resumes after restart, and pauses for a locked vault, bulk storage work or low disk
space. Explicit preparation reads each supported source once and creates an
encrypted 320px grid image, 1280px viewer image and private ThumbHash placeholder.
On-demand requests render only the missing requested sizes. A completed or manually
paused job is not restarted merely to fill the new viewer variants. The private worker renders WebP and TIFF previews
when supported by the installed FFmpeg build, plus a bounded first-frame poster
for supported video files. HEIC/HEIF and AVIF use libheif through anonymous
memory files because Alpine FFmpeg lacks their demuxer; unavailable codecs use
the preview fallback. Preparation shows active render count and average source-read
progress. Ordinary video playback uses the original browser-compatible stream. Paired Live
Photos have an explicit, bounded compatible motion preview. The originals are unchanged. The automatic disk
cache target is the smaller of 64 GiB and one tenth of current cache bytes plus
free space above the silent system reserve. Owners share this allowance; there is
no implicit 4-GiB owner cap. The default is 200,000 encrypted cache records per
owner, including small manifest segments. Set
`WEAZLCLOUD_PREVIEW_OWNER_BYTES`, `WEAZLCLOUD_PREVIEW_OWNER_FILES` and
`WEAZLCLOUD_PREVIEW_NODE_BYTES` to impose explicit caps (byte values are positive integers).
These limits apply to generated previews, not the library quota.

The Photos timeline uses an inset date rail clear of the browser scrollbar. Dragging
from year labels works with mouse or touch; date labels update locally and one
seek is issued on release. Current cards remain visible while that seek loads.
Wheel scrolling continues in both directions after a date jump; the grid and rail
share one scroll pane with bounded page retention. Live Photos open as a still
with explicit motion playback. The Photos menu can inspect/repair imported pairs
using embedded identifiers and report preview failures. See the
[browsing/Live Photo release](docs/photos-browsing-remediation-2026-10-02.md) for
contracts, recovery scope and device-validation limits.
The timeline groups items by capture day, offers a Recently added view
and month jump, and opens images and videos in a full-screen viewer. The viewer
supports keyboard/swipe navigation, adjacent-image prefetch, capture metadata
and original download while preserving the timeline position. Date anchors,
zoom and an explicit owner-only Hidden view are included.

Docker images include a **libjpeg-turbo JPEG renderer**. It uses runtime CPU
feature detection (including SIMD where supported), scaled JPEG decoding, and
bilinear resizing. There is no AVX requirement: PNG, GIF, CMYK JPEG, and native
builds without the helper use the Go renderer. Set
`WEAZLCLOUD_PREVIEW_RENDERER=auto` (default), `go` (disable native rendering), or
`turbo` (require the installed helper). Invalid settings or a broken installed
helper reject startup. A corrupt native JPEG fails that preview rather than
silently caching a partial image.

The shared-decode JPEG helper uses pipes only, with no plaintext image files. Existing
64-MiB input / 32-million-pixel limits remain; native workers additionally have a
512-MiB address-space ceiling, 30-second CPU limit and 35-second wall timeout.
Cancellation kills and reaps the helper. The Go process remains CGO-free. The
`media-v4` cache identity regenerates derivatives lazily; old encrypted caches
remain subject to normal eviction. Originals are never rewritten. See the
[acceleration workbook](accelerated_previews_workbook_2026-09-28.md) for measurements
and limitations. Local native tests need a C compiler, libjpeg-turbo development
headers and `cjpeg`/`djpeg`: run `bash scripts/test-native-preview.sh`.

The [October 1 thumbnail workbook](photos_thumbnail_cache_workbook_2026-10-01.md)
adds a reusable authenticated Restic reader, continuously supplied workers and an
encrypted incremental job journal. Grid and viewer requests share source reads;
one canceled request does not cancel surviving consumers. Existing `media-v4`
previews remain usable. Set `WEAZLCLOUD_PREVIEW_READER=off` for the bounded CLI
reader or `WEAZLCLOUD_PREVIEW_BUNDLE=off` for the legacy single-output renderer.
See [measurements and limits](docs/photos-thumbnail-performance-2026-10-01.md) and
[the verified rollout](docs/photos-thumbnail-rollout-2026-10-01.md).

Compressed server RAM cache bytes count against preview memory, with a ceiling of
`min(2 GiB, effective RAM/64, preview budget/4)`. Rendering reclaims them before
waiting for memory. Browser reuse is session-only: at most 32 MiB/256 entries,
shared by grid and viewer, with URLs released after mounted images let go.
Lock/account/visibility changes clear the applicable cache. Network responses stay
private/no-store; neither cache persists decrypted originals or changes vault access.

Preview workers use the minimum visible CPU, affinity, execution, and nested
cgroup v1/v2 limits. Their memory admission budget is one eighth of visible
host/container memory, capped at 8 GiB; incomplete discovery selects a conservative
one-worker policy with at most 256 MiB. Sources are bounded, with conservative dimension admission or a small-host header
probe before reserving compressed bytes, decoded pixels, scratch, cache copies, and
an allowance for the backend reader. Fallback Restic preview children receive a
128 MiB `GOMEMLIMIT` heap target. Persistent indexes have a separate charged
allowance and leave capacity for a full source/decode pipeline; small hosts use
the CLI fallback when a resident index would crowd out rendering. These are conservative admission estimates,
not a hard whole-container RSS limit.

Operators can set `WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS`,
`WEAZLCLOUD_PREVIEW_TOTAL_WORKERS`, `WEAZLCLOUD_PREVIEW_SOURCE_READERS` and
`WEAZLCLOUD_PREVIEW_MEMORY_BYTES`. Invalid values reject startup; memory overrides
cannot exceed the detected preview budget. CPU ceilings still apply. These
settings do not change the service's disk quota. Raster work is coalesced and
limited to 256 outstanding distinct jobs across owners; a full queue returns a
retryable error. One cancelled tile request does not cancel another waiter.
Preview working-memory reservations are shared by the process, and waiting visible
previews get the next available reservation ahead of background preparation. Queued owner work shares bounded FIFO admission; a foreground request can promote
a coalesced job waiting for a background slot. Active native work is not preempted.

Set `WEAZLCLOUD_PHOTO_SCHEDULE=quiet|balanced|fast` in the deployment environment
to control background preparation. `balanced` is the default; `fast` can use up to
the full photo worker allowance while reserving one render slot for foreground
requests. `quiet` pauses backlog work while on-demand previews continue. Every mode
remains within the worker concurrency derived from at most half the effective CPU
allocation. The selected mode and effective limits are reported by the photo
preparation status endpoint. Manual pause remains encrypted and survives restart.

Photos now has a capture-date timeline with justified rows, Favorites, Archive,
Hidden, filtered wildcard search and owner-created albums. Date buckets honor a
known capture timezone instead of using import dates. Captions/date corrections
and rotation leave originals unchanged; exact-duplicate review can mark a preferred
copy without deleting files or changing album membership. Photos Trash follows
the existing 30-day retention and filters hidden items in its own explicit context.

Albums and selected photos can become a frozen gallery grab with the node's
admin-configured hostname, passphrase option, expiry, QR and revocation. Guests
browse re-encoded previews without spending retries; explicit original or ZIP
transfers spend one each. Originals may retain embedded location metadata.
Gallery capsules retain encrypted copies and their ZIP until burn/expiry/revoke;
ordinary on-demand download ZIPs expire 90 minutes after becoming ready. Owner
ZIP jobs and payloads are encrypted, recover after restart, and support bounded
range reads. Guest selection ZIPs prepare as encrypted background jobs without
spending a retry; an explicit download spends one.

The versioned Photos API includes revocable device credentials, resumable still/
motion-pair uploads, source revisions, owner-selected Photos roots, atomic album
membership and a durable processing outbox. Large selections stay on the server;
change/checkpoint sync and album memberships use bounded pages. See the [API checkpoint](docs/photo-api.md),
[execution workbook](plan_modal.md), [recovery runbook](docs/photos-release-runbook.md) and
[local verification record](docs/photos-local-verification-2026-09-30.md) for
implemented behavior and remaining release gates. The [OpenAPI contract](docs/photo-api.yaml)
is ready for a native client; the iOS app itself and runtime SQLCipher integration
remain future work. Both-backend browser/container checks and thumbnail/cache
measurements are recorded in the [October 1 performance report](docs/photos-thumbnail-performance-2026-10-01.md).

The [mobile server API](docs/mobile-api.md) adds scoped device credentials with
retry-safe rotation, nested photo album folders, source organization, recurring
one-way file backups, stable file reads and offline sync. Negotiated `parts-v1`
uploads stage authenticated encrypted parts on the data volume and finalize in
bounded server workers after all components arrive; the app need not remain open.
Source revisions and destination revision checks prevent a backup from silently
overwriting unrelated files. Hidden and unsupported camera originals remain
preserved independently of preview support. Grab creation accepts an
`Idempotency-Key` so a lost response can recover the same capsule.

See the [October 2 workbook](mobile_server_workbook_2026-10-02.md),
[mobile OpenAPI](docs/mobile-api.yaml), [migration runbook](docs/mobile-server-migration.md)
and [validation record](docs/mobile-server-validation-2026-10-02.md).
The server contract supports the native scaffold; PhotoKit, iCloud retrieval,
file-provider access and iOS background scheduling still require physical-device
validation. Never roll an older image onto a migrated catalog; use a forward fix
or restore a complete consistent pre-upgrade copy.

The supplied Docker Compose file runs a private `weazlcloud-photo-worker` sidecar
with no network listener. The API sends it authorized media bytes through a
permission-restricted Unix socket; the worker receives no vault keys or paths and
does not write source data to disk. The worker has its own hard CPU and memory
limits, including FFmpeg child processes. Set `WEAZLCLOUD_PHOTO_CPUS` to half the CPU allocation available to the
WeazlCloud deployment (for example `16` on a 32-CPU host); the Compose default is
`1.0` CPU for a small two-CPU host. `WEAZLCLOUD_PHOTO_MEMORY_LIMIT` defaults to
`2g`. Leave `WEAZLCLOUD_PREVIEW_WORKER_SOCKET` unset for a standalone run without
Compose; previews then use the bounded in-process renderer and worker pool.

Preparation resumes by checking current content identities and reusable caches,
not trusting old slice positions. Manual pause survives restart and index changes.
Failure records are encrypted, capped at 100,000 per owner, and retryable through
**Retry failed previews**. A corrupt image does not prevent later photos from
being prepared; inability to persist a checkpoint pauses with a visible error.
Cache skips, write failures and eviction produce partial readiness. Lock, rekey,
revocation and shutdown invalidate preview work; imports pause background dispatch
through their entire lifetime. A cold raster render with known dimensions uses
one admitted source read. Bundles conservatively reserve memory for older files
without dimensions; small hosts use a bounded header probe followed by a separate
admitted full read only when the conservative reservation cannot fit.

Adaptive pressure feedback, owner queue fairness, node-wide accounting for retained
catalog/index RAM, and measured large-host scaling remain follow-up work. Shared-store writes
reuse one zstd encoder sequentially per manifest without changing the stored
format; this reduces per-chunk allocation but has not been measured on production.
See the
[local verification record](docs/photos-smoke-followup-2026-09-27.md) before rollout.

The album and music browser smoke uses a disposable Docker volume and synthetic
fixtures (requires Python Playwright and Chromium). After `make smoke-container`,
run `make smoke-photos PHOTOS_PYTHON=/path/to/playwright-venv/bin/python` for both
storage backends with 2 CPU / 4 GiB container limits. It checks split albums,
dated viewers, search, editing, Hidden folders, gallery ZIPs, covers, playback,
vault locking and owner isolation. CI runs the same authenticated workflow.

## Development preview

The static mockup is the contract. The API is paint.

```sh
make mockup
```

Open **http://127.0.0.1:3001**. Loopback only.

recipient’s grab page (preview): [http://127.0.0.1:3001/grab.html](http://127.0.0.1:3001/grab.html).

## Capsules (how a grab link actually works)

A capsule is **not** a share to a user. There is no recipient account. There is no
permission matrix. You mint a sealed copy of one file or one folder, wrap a
capability URL around it, and recipient opens that URL on a phone.

Think of it as a one-way envelope, not a live Dropbox folder.

```text
you, on the desk
  pick a file or a folder in the library
  mint a capsule (open or passphrase, how many grabs, how long it lives)
  send recipient the URL (and the phrase, if you set one, in a second channel)

recipient, on a phone
  opens https://grab.your.domain/g/<token>
  sees the name, the size, Grab
  taps Grab
  gets bytes a normal phone can open (markdown, PDF, image, zip)
  does not see the rest of your library
  does not unlock the vault
  cannot write back
```

### What is sealed

At mint, WeazlCloud **copies** the current bytes into the capsule and encrypts
that copy. The library file can change later. recipient’s link does not. Editing
`setlist.md` after you mint does not mutate an already-minted grab. If you
want recipient to have the new version, mint again and send a new URL.

A **file** capsule is that file. A **folder** capsule is the tree as it stood
at mint, delivered as a zip plus a listing on the grab page. It is not a live
directory. recipient cannot watch it update.

### How recipient gets in

Two gates. Pick one when you mint.

| Gate | What recipient needs | When to use it |
|---|---|---|
| **Open** | The URL. That is the capability. | You already trust the channel (a text you typed, a QR in the room). |
| **Passphrase** | The URL **and** a phrase you send somewhere else. | The URL might leak (email, group chat). Phrase rides SMS, a call, a sticky note. |

The passphrase is not a login. There is no reset. Wrong phrase, no file.

Permission is **read-only**. Always. recipient can grab. recipient cannot upload into your
library, mkdir, or list anything that was not sealed into that capsule.

### How long it lives

Defaults, on purpose:

- **24 hours**
- **1 grab**
- then **burn**

After the grab limit, or after expiry, or after you revoke, the unwrap key is
destroyed. The URL becomes a brick. “This grab is gone.” There is no recycle
bin for recipient.

You can raise expiry or grab count when you mint (3 days / 7 days, 5 or 20
grabs). Standing immortal Dropbox links are not the default and should not
become a habit. If the job is “recipient needs this tonight,” 24h / 1 grab is the
whole product.

**Revoke** is explicit. It bricks the URL even if time and grabs remain.

### What the URL is

```text
https://grab.your.domain/g/<token>
```

The token is random, long, and unguessable. Knowing the URL is the capability
(plus the phrase, if you set one). Traefik already terminates HTTPS on that
name. The grab hostname talks **only** to the share listener (`:7273`). It
cannot unlock the vault. It cannot list the library. It cannot mint. Hang
**desk** and **grab** on different names so a stolen grab URL is not a stolen
house.

The node administrator sets the grab hostname in **Admin** before anyone
mints. Users can see the hostname in Places, but cannot change it. The desk
refuses to mint until that administrator-owned HTTPS name is configured. recipient’s
phone has to be able to reach the name you hang on Traefik.

The QR on Send is the same URL. Show it in the room; don’t text it twice.

### After you lock the vault

Already-minted capsules keep working. Unlock is not required for recipient to grab.
The share socket holds its own unwrap material next to the sealed payload. Lock
the vault when you walk away from the desk. recipient’s link does not care.

Revoke still works from the desk while you are unlocked.

### What a capsule is not

- Not a user. “recipient” is a label you typed so you remember who you sent it to.
- Not a live share. Nothing mutates under the URL.
- Not write/collaboration. recipient cannot put files into your node this way.
- Not backup. WeazlBack snapshots `/data`. Capsules are envelopes, not history.
- Not a public gallery. There is no listing of capsules on the grab host.

### On the desk

Library: right-click a file or folder (or the ⋯ button) → **Send grab link**.
That jumps to Send with the target selected. Mint. Copy the URL. Optionally
open the grab page as recipient.

Capsules: every live envelope, with label, gate, time left, grabs left.
Right-click → copy URL, open as recipient, revoke.

Destroy a capsule (fine print) is the same revoke, spoken in consequences.

## Node (multiuser foundation)

```sh
make check
make run
```

Native binds loopback: desk `127.0.0.1:7272`, share `7273`, drive `7274`.
`GET /ready` on each listener. Share and drive do not serve the desk.

The node has a local identity authority. There are no hosted accounts, OIDC,
OAuth, SSO, external directories, or third-party identity providers. Each user
gets a separate vault, catalog, restic library, and session. The first account
created on a fresh node is the local administrator.

Set `WEAZLCLOUD_SECURE_COOKIES=true` when HTTPS terminates in front of the
node. Leave it unset for direct HTTP loopback development.

Background maintenance waits until storage activity has been quiet for five
minutes. Set `WEAZLCLOUD_MAINTENANCE_QUIET` to a positive Go duration such as
`2m` to change that technical interval. Health probes and the library event
stream do not reset the quiet timer.

The drive listener is WebDAV. In Thunar, use **File → Connect to Server** and
enter `dav://HOST:7274/` for a direct HTTP connection, or `davs://HOST/` when
the drive name is behind the HTTPS reverse proxy. Authenticate with the local
WeazlCloud username and account password. The listener unlocks only that
user's vault and presents only that user's library.

Desk API (same-origin header `X-Weazl-Desk: 1` on POST):

- `GET /api/status` — setup, local authentication, vault, and sanitized quota state
- `POST /api/bootstrap` — `{username, password, vault_passphrase, confirm}` on a fresh node
- `POST /api/login` / `POST /api/logout`
- `POST /api/users` — administrator creates another local user
- `GET /api/me`
- `POST /api/unlock` / `POST /api/lock` — current user’s vault
- `GET /api/quota` — physical volume usage and the 97% usable limit
- `POST /api/kit` — writes `weazlcloud-recovery.wzck` on the volume

The passphrase never lives in Compose `environment`. Lose it and the kit is a brick too.
An existing single-user root vault is adopted by the first administrator after
its vault passphrase verifies.

WebDAV uses each user's stored node key to unlock that user's vault after a
restart or an in-memory lock, so a mounted drive can reconnect. Desk sessions
and vault unlock remain separate. Rekey replaces the stored node key. Anyone
who controls the node host is inside the vault decryption trust boundary.

Library (vault must be unlocked):

- `GET /api/library` — current files (path, size, mtime). No engine words.
- `PUT /api/library?path=Documents/nug.md` — body is the file
- `GET /api/library?path=Documents/nug.md` — bytes
- `DELETE /api/library?path=Documents/nug.md` — gone from the current tree

Capsules (vault unlocked to mint/revoke; recipient does not need the vault):

- `GET /api/capsules` — live and burned envelopes (no unwrap keys)
- `POST /api/capsules` — `{path, kind, gate, passphrase, label, expiry, grabs}`
- `DELETE /api/capsules?id=` — brick the URL
- Grab host: `GET /g/<token>` page, `GET /g/<token>/meta`, `POST /g/<token>/file`

Places:

- `GET /api/places` / `POST /api/places` — `{grab: "https://…", drive: "davs://…"}`
  Users may update their drive display address; the grab value is read-only.
- `GET /api/node` / `POST /api/node` — administrator-only node hostname settings,
  with `{hostname: "grab.your.domain"}`. The value is stored in `/data/node.json`.

The storage hard cap is 97% of the filesystem containing `/data`; the UI meter
reports that usable limit as 100%. Approved users share whatever usable disk is
available. User logical usage is informational, while physical filesystem
headroom remains the final write guard. Cross-user deduplication remains a
separate privacy decision.

The desk mockup talks to these endpoints when it is served by the node
(`engine.js`). `make mockup` on :3001 stays a preview with no engine.

Join the API service to your reverse-proxy network and retain a backup of `/data`.

```sh
cp deploy/compose.env.example deploy/compose.env
# Edit the environment file for your host and existing data mount.
docker compose --env-file deploy/compose.env -f deploy/compose.yaml config --quiet
docker compose --env-file deploy/compose.env -f deploy/compose.yaml up --build -d
```

Both services build from the same checkout and use `WEAZLCLOUD_IMAGE` (default
`weazlcloud:local`). `WEAZLCLOUD_DATA_SOURCE` can select an existing absolute host
directory; unset keeps the named volume. Optional preview settings pass through
without replacing automatic sizing. See the [Compose guide](docs/docker-compose.md)
for mounts, resource settings and upgrades. Keep an existing installation's host
override and vault mount.

The container does not publish 80/443. Traefik already terminates HTTPS on
this household. Route the desk and grab listeners separately; WebDAV can stay
private:

| Name | Port | Job |
|---|---|---|
| desk | 7272 | Lite UI, unlock, mint |
| grab | 7273 | Token → sealed bytes |
| drive | 7274 | WebDAV for Files (`davs://`) |

No Traefik labels ship in this compose. No passphrase in `environment`.

See `weazlcloud_plan.md`, `weazl_ethos.md`, and `weazl_ethos_lite.md`.
