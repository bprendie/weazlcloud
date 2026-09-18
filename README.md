# WeazlCloud

A household library node. Your files on metal you own. Send Gil a grab link.
Mount the same library in Files over `davs://`. No FUSE. No Weazl account.

The static mockup is the contract. The API is paint.

```sh
make mockup
```

Open **http://127.0.0.1:3001**. Loopback only.

Gil’s grab page (preview): [http://127.0.0.1:3001/grab.html](http://127.0.0.1:3001/grab.html).

## Capsules (how a grab link actually works)

A capsule is **not** a share to a user. There is no Gil account. There is no
permission matrix. You mint a sealed copy of one file or one folder, wrap a
capability URL around it, and Gil opens that URL on a phone.

Think of it as a one-way envelope, not a live Dropbox folder.

```text
you, on the desk
  pick a file or a folder in the library
  mint a capsule (open or passphrase, how many grabs, how long it lives)
  send Gil the URL (and the phrase, if you set one, in a second channel)

Gil, on a phone
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
that copy. The library file can change later. Gil’s link does not. Editing
`gil-setlist.md` after you mint does not mutate an already-minted grab. If you
want Gil to have the new version, mint again and send a new URL.

A **file** capsule is that file. A **folder** capsule is the tree as it stood
at mint, delivered as a zip plus a listing on the grab page. It is not a live
directory. Gil cannot watch it update.

### How Gil gets in

Two gates. Pick one when you mint.

| Gate | What Gil needs | When to use it |
|---|---|---|
| **Open** | The URL. That is the capability. | You already trust the channel (a text you typed, a QR in the room). |
| **Passphrase** | The URL **and** a phrase you send somewhere else. | The URL might leak (email, group chat). Phrase rides SMS, a call, a sticky note. |

The passphrase is not a login. There is no reset. Wrong phrase, no file.

Permission is **read-only**. Always. Gil can grab. Gil cannot upload into your
library, mkdir, or list anything that was not sealed into that capsule.

### How long it lives

Defaults, on purpose:

- **24 hours**
- **1 grab**
- then **burn**

After the grab limit, or after expiry, or after you revoke, the unwrap key is
destroyed. The URL becomes a brick. “This grab is gone.” There is no recycle
bin for Gil.

You can raise expiry or grab count when you mint (3 days / 7 days, 5 or 20
grabs). Standing immortal Dropbox links are not the default and should not
become a habit. If the job is “Gil needs this tonight,” 24h / 1 grab is the
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
refuses to mint until that administrator-owned HTTPS name is configured. Gil’s
phone has to be able to reach the name you hang on Traefik.

The QR on Send is the same URL. Show it in the room; don’t text it twice.

### After you lock the vault

Already-minted capsules keep working. Unlock is not required for Gil to grab.
The share socket holds its own unwrap material next to the sealed payload. Lock
the vault when you walk away from the desk. Gil’s link does not care.

Revoke still works from the desk while you are unlocked.

### What a capsule is not

- Not a user. “Gil” is a label you typed so you remember who you sent it to.
- Not a live share. Nothing mutates under the URL.
- Not write/collaboration. Gil cannot put files into your node this way.
- Not backup. WeazlBack snapshots `/data`. Capsules are envelopes, not history.
- Not a public gallery. There is no listing of capsules on the grab host.

### On the desk

Library: right-click a file or folder (or the ⋯ button) → **Send grab link**.
That jumps to Send with the target selected. Mint. Copy the URL. Optionally
open the grab page as Gil.

Capsules: every live envelope, with label, gate, time left, grabs left.
Right-click → copy URL, open as Gil, revoke.

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

Library (vault must be unlocked):

- `GET /api/library` — current files (path, size, mtime). No engine words.
- `PUT /api/library?path=Documents/nug.md` — body is the file
- `GET /api/library?path=Documents/nug.md` — bytes
- `DELETE /api/library?path=Documents/nug.md` — gone from the current tree

Capsules (vault unlocked to mint/revoke; Gil does not need the vault):

- `GET /api/capsules` — live and burned envelopes (no unwrap keys)
- `POST /api/capsules` — `{path, kind, gate, passphrase, label, expiry, grabs}`
- `DELETE /api/capsules?id=` — brick the URL
- Grab host: `GET /g/<token>` page, `GET /g/<token>/meta`, `POST /g/<token>/file`

Places:

- `GET /api/places` / `POST /api/places` — `{grab: "https://…", drive: "davs://…"}`
  Users may update their drive display address; the grab value is read-only.
- `GET /api/node` / `POST /api/node` — administrator-only node hostname settings,
  with `{hostname: "grab.your.domain"}`. The value is stored in `/data/node.json`.

The quota hard cap is 97% of the filesystem containing `/data`; the UI meter
reports that usable limit as 100%. User logical usage is divided by the current
number of local users, while physical filesystem headroom remains the final
write guard. Cross-user deduplication remains a separate privacy decision.

The desk mockup talks to these endpoints when it is served by the node
(`engine.js`). `make mockup` on :3001 stays a preview with no engine.

When you hang this on the Ubuntu box: join the existing Traefik network, three HTTPS names, no host-published vault port. Backup `/data` with WeazlBack. Cut two kits.

```sh
docker compose -f deploy/compose.yaml up --build -d
```

The container does not publish 80/443. Traefik already terminates HTTPS on
this household. Attach the service to that network and hang three names:

| Name | Port | Job |
|---|---|---|
| desk | 7272 | Lite UI, unlock, mint |
| grab | 7273 | Token → sealed bytes |
| drive | 7274 | WebDAV for Files (`davs://`) |

No Traefik labels ship in this compose. No passphrase in `environment`.

See `weazlcloud_plan.md`, `weazl_ethos.md`, and `weazl_ethos_lite.md`.
