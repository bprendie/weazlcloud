# WeazlCloud Plan

Status: draft. Open this first in a fresh repo. Parent ethos wins on cryptography, sovereignty, the LLM boundary, and flow state.

Copy into the repo root at init:

- `~/weazl_ethos.md`
- `~/weazl_ethos_lite.md`
- `~/weazl_skill.md`

Weazl knows. Weazl is wise. Weazl will never tax the gig.

## North star

WeazlCloud is a **household library node**. It replaces the part of iCloud/OneDrive that is actually a live folder plus “send this file to Gil’s phone.” It is not disaster recovery. It is not a Weazl-operated VPS. It is not a user table.

The first and dominant path is:

```text
unlock or forge the vault
  → put a weazldoc in the library
  → mint a grab link (file or folder; open or passphrase; read-only)
  → Gil opens the URL on a phone and grabs it
```

The architect’s daily path is the same library in their Linux file manager over WebDAV (`davs://`). No FUSE. No rclone mount. No extra daemon.

A stretch path, not a v0 gate: drop an iCloud / Google Takeout / OneDrive export onto the node and ingest it. Those dumps are full of the same bytes in many folders. Restic’s job is to keep one copy.

Success means the Gil path and the Files path run on metal the architect owns, in Docker, with a Weazl Lite desk as the control surface, without Gil creating an account, and without the vault socket facing the internet as the grab hostname.

WeazlBack remains the only history that counts as backup. Snapshot this node’s data volume. Cloud uses restic as the **library engine** (dedupe, packs). Cloud does not grow a snapshot browser, a retention policy, or a rewind-the-household UI.

## Product law (household node)

Lite ethos reserved this slot: if a later product is genuinely a household node, say so in that product’s law. This is that law.

1. **Zero Cloud SaaS.** No Weazl account, no Weazl relay, no telemetry, no escrow, no forgotten-passphrase recovery. A VPS the architect rents and administers is still their store. A VPS Weazl sells is not this product.
2. **Unlock is the vault passphrase.** Do not invent a web-app username, JWT user table, OIDC, or “Gil as a login.” Gil is a label on a capsule, not an identity.
3. **Four listeners, one vault.** A listening desk, a grab URL, a drive WebDAV, and a vault in memory are different threats. Do not smuggle unlock onto the grab hostname because a phone needed a link. Do not hang WebDAV on the grab socket.
4. **Capsules, not live paths.** A share URL serves a sealed export of one file or one folder. It cannot list the rest of the library. It cannot unlock. Editing after mint does not mutate an already-minted link unless the architect explicitly replaces the payload.
5. **A file is present or it is not.** No Optimized Storage, no remote-only stubs, no FUSE over restic as the Drive. WebDAV materializes real bytes. The file manager talks WebDAV; it does not mount a FUSE helper Weazl ships.
6. **Restic is the library engine.** Dedupe, chunking, and packs are restic’s job. A pinned restic binary lives in the image. Password goes in through a pipe or a `0600` file, never argv or Compose `environment`. The desk shows a **current library**, not `restic snapshots`. Generations, prune ritual, and machine restore stay in WeazlBack. Nested WeazlBack of `/data` is how the node itself survives disk death.
7. **Lite desk is the control plane.** Mockup first. One Go process. Embedded vanilla HTML/CSS/JS. No Electron, no npm in production, no frontend framework, no webfonts from the network. Do not wrap a TUI in a browser terminal.
8. **TUI is the operator sibling**, not the Gil surface. SSH-safe node status, lock, and diagnostics. 80x24 must still work. On Omarchy the TUI may ship later; it must not block the desk.
9. **Idle means idle.** The node is allowed to be a daemon because the job continues with every interface closed. A closed desk owns no animation clock and must not poll the binary. Progress is pushed from work already in flight.
10. **Destruction is explicit.** Revoke, lock, rotate the drive token, and nuke speak in consequences. Burn-after-read is the default for grab links. Standing immortal Dropbox links do not ship as the default.
11. **Ingress is already HTTPS.** This household sits behind a Traefik the architect already runs. WeazlCloud does not ship Traefik, Caddy, labels, Let’s Encrypt, or a tourist mesh. Bind internally. The architect hangs hostnames. `WEAZLCLOUD_PUBLIC_BASE` is an HTTPS name that already exists.
12. **A recovery kit is not a spare passphrase.** Cut an offline `.wzck` envelope (WeazlBack’s kit, for this vault). Same passphrase. Stolen kit is a brick. Forget the phrase and the kit is a brick too. The kit holds vault wrap, restic access, Places, and the drive token — not the library packs.

If a feature needs a second password in front of the vault, a recovery quiz, a public gallery, collaborative editing, CalDAV, CardDAV, WOPI/Collabora, Find My, or a Weazl-hosted relay, it does not ship.

## Cleanroom: inspired by OxiCloud, not a fork

OxiCloud (AtalayaLabs, MIT) is prior art for a small self-hosted file node: live CAS, share links, WebDAV, Docker. Its license would allow a fork. We are **not** forking it.

| Take as concept | Leave on the floor |
|---|---|
| Live namespace over content-addressed packs | Their repo, identifiers, SPA, Docker image |
| Capability share links with expiry and optional password | JWT, Argon2 user table, OIDC, admin/roles/quotas |
| WebDAV as the file-manager adapter | CalDAV, CardDAV, WOPI, Collabora, i18n suite |
| Docker as the node deploy | Postgres as a required personality |
| File is metadata + blob refs, not a POSIX tree on disk | Nextcloud-compatible identity API as the product |
| | Their trademark and dashboard chrome |

Do not copy OxiCloud source, CSS, URL layouts, env names, or schema. Implement from this document. Restic does the chunking. `golang.org/x/net/webdav` (or equivalent stdlib-adjacent) does the drive. Do not vendor their crates via a rewrite.

**OxiCloud** is their trademark. This product is WeazlCloud.

## Jobs and non-jobs

### v0 jobs

- Household node in Docker, one binary, one data volume, pinned restic.
- Vault: Argon2id wrap, AES-GCM at rest, no forgotten-passphrase recovery.
- Recovery kit: encrypted `.wzck` envelope, same phrase, offline copies.
- Library: designated files/folders, restic repo on `/data`, current-tree catalog.
- Lite desk: unlock/forge, browse library, mint grab link, list/revoke capsules, cut a kit.
- Grab: file or folder. Gate is **open** (URL is enough) or **passphrase** (second channel). Permission is **read-only**. Gil cannot write back.
- Grab page: one yellow button. File Gil can open (Markdown, PDF, image, zip). Folder page lists the sealed tree. QR on the desk.
- Capsule policy: expiry, grab limit (default 1), revoke. Default 24h / 1 grab / burn.
- Drive: WebDAV for the architect’s Linux file manager over `davs://`. No FUSE.
- Places: HTTPS grab base, HTTPS drive base. Refuse to mint if grab base is unset.

### Stretch (after v0, still this product)

- **Takeout ingest.** The architect already downloaded a Google Takeout, an iCloud data copy, or a OneDrive/Microsoft export. WeazlCloud walks that tree (folder or zip on the volume / via Drive PUT / desk pick) and puts files into the library. Identical bytes across albums, re-exports, and “original + JPEG” pairs store once. This is where restic earns the rent.
- Preserve a prefix (`takeout/google-2026-09/…`) so the dump is findable. Do not invent Memories, face dossiers, or album SaaS.
- Google Photos JSON sidecars are metadata sitting next to the file, not a second original.
- Multiple takeouts onto the same node keep deduping against what is already there.

This is not a live connector. Weazl does not OAuth to Apple, Google, or Microsoft. Weazl does not poll their APIs. The dump comes to the node; the node does not go to the dump.

### Not v0

- Multi-device live sync mesh (laptop ↔ desk ↔ phone as peers).
- Photos Memories, face dossiers, public albums.
- Nextcloud client compatibility as the product.
- iOS Files provider, Android app.
- Selective sync / on-demand placeholders.
- Sharing a live folder that mutates under Gil’s URL.
- Write/collaboration on a share (Gil uploading into a capsule).
- Snapshot browser, restic forget/prune as a Cloud ritual.
- Postgres, Redis, object-store clustering.
- Shipping Traefik, Caddy, or ACME.
- Takeout ingest (stretch; mockup still shows the desk so the contract does not forget it).
- Live sync with iCloud, Google Drive, or OneDrive.

### Never this product

- WeazlMail, WeazlWrite, WeazlBack, Keychain, Find My.
- Optimized Storage.
- Forgotten-passphrase recovery, escrow, reset questions.
- Monetized relay.
- FUSE as the Drive.
- OAuth into Apple / Google / Microsoft as a WeazlCloud identity or a standing sync peer.

Photos as a date-indexed originals tree may follow v0. They are an ingest layout on the same restic library, not a gallery SaaS. Local viewers open real files.

## Architecture

```text
 architect browser ── Traefik (already there, HTTPS) ──► DESK socket
                                                         (Lite UI + API)
                                                               │
                                                               │ unlock, mint, revoke
                                                               ▼
                                                        in-memory DEK
                                                               │
              ┌──────────────────────────────┬─────────────────┼─────────────────┐
              ▼                              ▼                 ▼                 ▼
         vault/catalog                  restic repo      capsule store      drive token
         (SQLite AES-GCM)               (library packs)  (sealed exports)   (in vault)
              │                              │
              │                              │
 Gil phone ── Traefik ──► SHARE socket       │
              (grab hostname                 │
               architect owns)               │
              token → sealed bytes           │
              no vault, no library list      │
                                             │
 Files app ── Traefik ──► DRIVE socket ──────┘
              (davs hostname)     WebDAV → restic dump/backup
              drive token auth    no grab routes, no unlock HTML
```

Library push/pull between the architect’s other machines is a later socket, still on the user’s path (LAN or VPN they run). It is not the share socket.

The household already has Traefik. WeazlCloud joins that Docker network and listens. It does not become the proxy.

### Key hierarchy

Follow WeazlMail / WeazlBack, not OxiCloud’s “AES key in `.env`.”

- Vault gets a random 256-bit DEK.
- DEK is wrapped by an Argon2id-derived passphrase key (interactive unlock).
- DEK is also wrapped by a node key on the data volume, mode `0600`, so a household container can remount the library after restart without stuffing the passphrase into Compose environment or argv.
- At forge, generate a restic repository password and a drive token. Both live inside the vault, wrapped by the DEK.
- Passphrase never in URL, localStorage, query strings, argv, or environment.
- Restic is fed the password through a pipe (WeazlBack’s runner), never `RESTIC_PASSWORD` in Compose.
- Explicit **Lock** zeroes the in-memory DEK. Desk mutations stop. Drive and grab of already-minted capsules keep working only insofar as their own unwrap material allows: grab yes (capsule key), drive no (needs DEK / node-key autoload).
- Node-key autoload on boot is a spoken install choice, not a silent default in the mockup. Autoload is what lets Files keep working after a reboot without opening the desk.
- Closing the tab detaches. It does not lock.

Share capsules are sealed at mint time with a key that is **not** the vault DEK sitting in RAM:

- Token is unforgeable (≥128 bits, path-safe).
- Capsule payload is AES-GCM ciphertext on disk.
- Unwrap material lives with the capsule record, usable by the share socket after lock.
- **Open** gate: URL is the capability. No extra wrap.
- **Passphrase** gate: additional wrap. URL in one channel, phrase in another.
- **Read-only**: Gil can grab the sealed file or browse the sealed folder. Gil cannot write, mkdir, or PROPFIND the library.
- A folder capsule is a sealed tree (listing + bytes, or a zip plus listing) taken at mint. It is not a live directory.
- Revoke deletes unwrap material and the payload. A stolen URL becomes a brick.
- Default policy: 24 hours, 1 grab, then burn.

The share socket must not gain catalog list, restic snapshot list, DEK unwrap, or WebDAV.

### Recovery kit (`.wzck`)

Same idea as WeazlBack’s `.wzrk`. Different magic. Do not emit a WeazlBack kit and call it done.

```text
format    weazlcloud-recovery
version   1
KDF       Argon2id
AEAD      AES-GCM
```

Encrypted payload is a zip of:

- `vault.enc` — passphrase-wrapped DEK and the restic password / drive token material
- `config.json` — Places (grab base, drive base), restic repo path on the volume, node identity
- `manifest.json` — schema, created time, SHA-256 of each member
- warning: `NO RECOVERY: the vault passphrase is required and cannot be reset.`

Rules, copied in spirit from WeazlBack:

- The kit does not contain the passphrase.
- Export to an explicit path, `0600`, fsync, reopen, decrypt, compare fingerprints before success.
- Never overwrite an existing kit without preview and confirmation.
- Recommend two offline copies on separate media.
- A stolen USB is a brick. A forgotten passphrase is still game over. The forge screen says both.
- Old kits remain valid until keys are rotated. Rotation is explicit; cut a new kit after.

The kit does not hold library packs. After disk death you still need the restic volume (or a WeazlBack snapshot of `/data`) plus this envelope plus the passphrase.

Desk language: **Cut a recovery kit.** Not “backup your password.” Not a quiz.

### Library (restic)

```text
catalog (encrypted SQLite, current tree cache)
  files     → path, size, mtime, restic object id, present/absent

restic repo on /data/library
  password  → in the vault, never in Compose
  current   → tagged snapshot the desk treats as the library
```

- Put stages bytes, then `restic backup`. Identical bytes land once because restic dedupes.
- Get / mint / WebDAV GET dump or restore into a staging path, then stream. No FUSE.
- Current tree is the catalog cache, reconciled from restic. The desk does not say “snapshot.”
- Delete stays until the architect confirms. `restic forget` / prune is not an unattended Cloud job. If packs must be dropped, that is an explicit destroy, spoken in consequences.
- Interrupt mid-put, retry, catalog consistent. Restic locks are handled the way WeazlBack already does (unlock stale, then retry). Do not invent a second locker.
- Do not nest a second restic “backup this library” inside Cloud. WeazlBack snapshots `/data`.

Engine words (restic, pack, snapshot, CDC, DEK) belong in diagnostics, not on the home rail.

### Takeout ingest (stretch)

The dump is a directory tree the architect already has. Ingest is a bounded walk plus library put. Restic sees the bytes.

Typical waste these dumps are famous for:

- Google Photos albums each holding a copy of the same original
- A second Takeout overlapping the first
- iCloud “original + resized JPEG”
- OneDrive folder downloads stacked on an already-copied Documents tree

Desk copy: **Bring a takeout.** Pick Google, iCloud, or OneDrive. Point at the folder or zip. One yellow button. Footer shows files walked and bytes kept, not an API spinner.

Do not:

- Open an OAuth window
- Ask for an app password to iCloud
- Watch a live Drive folder in someone else’s cloud
- Parse this into a gallery product

Do:

- Skip obvious sidecars or store them next to the file (`metadata.json`, `.DS_Store` dropped)
- Keep relative paths under a prefix
- Dedup against the library that is already there
- Let the architect confirm before delete of the source dump (Weazl does not nuke the zip)

### Drive (WebDAV, no FUSE)

This is how the live folder shows up in GNOME Files, Nemo, Thunar, Dolphin.

- In-process WebDAV on the **drive** listener. `golang.org/x/net/webdav` or equivalent.
- HTTPS is Traefik’s job. The file manager uses `davs://`.
- Auth is the **drive token** from the vault, not a user table. Username may be `weazl` and is not an identity. The vault passphrase does not go into the GNOME keyring.
- Token rotation is explicit. Old token dies. Cut a new kit after rotate.
- PROPFIND/GET/PUT/DELETE/MKCOL map onto the current library. PUT is a library put (restic dedupes). DELETE is catalog-delete-until-confirmed, same as the desk.
- Drive is read-write for the architect. Gil’s grab is read-only. Do not confuse them.
- Drive listener serves no desk HTML, no unlock, no capsule admin.
- If the vault is locked and node-key autoload is off, WebDAV is 401. Files does not unlock the vault.
- Do not ship davfs2, rclone, or a FUSE helper. Document the Files “Connect to Server” path. That is enough for v0.

### Binds

| Socket | Default bind | Docker | Job |
|---|---|---|---|
| Desk | `:7272` on the container network | not published on the host | Unlock, browse, mint, revoke, kit |
| Share | `:7273` on the container network | not published on the host | Token → sealed bytes |
| Drive | `:7274` on the container network | not published on the host | WebDAV |
| Library sync | later, LAN/VPN | not in v0 | Device pairing |

The architect’s Traefik (already on the network) routes three HTTPS names to those three ports. WeazlCloud Compose does not include Traefik service definitions, labels-as-product, or ACME.

`WEAZLCLOUD_PUBLIC_BASE` (desk field **Where can a phone reach the grab socket?**) is required before mint. If unset, the desk refuses to mint instead of texting Gil `http://127.0.0.1/...`.

`WEAZLCLOUD_DRIVE_BASE` (desk field **Where does Files connect?**) is the `davs://` name. If unset, Places still explains the bind, and Drive auth still works on the LAN name the architect types into Files.

The architect already knows how to hang a hostname on their box. Weazl does not become Cloudflare. If the node is down, the link is dead. The desk says so.

### One process

One statically compiled Go binary serves desk assets, desk API, share HTTP, WebDAV, and the restic runner. No Node runtime. No sidecar PHP. Postgres is not a v0 dependency; encrypted SQLite on the volume is the catalog cache. Split a process only after a measured need.

## Lite desk (contract)

The static mockup is the contract. The API is paint.

Proven craft: WeazlBack Lite (`../weazlback_universal`), WeazlTunes web. Canonical mark: `weazlhead.png`. Public site for vibe, not for CRT cosplay on the desk: https://weazl.studio

- Persistent numbered sidebar, hero in one sentence, sticky footer for the thing happening now.
- Keyboard first. `?` is the short-route card.
- Outcome language. “Mint a grab link” not “POST /api/shares”. “Connect from Files” not “WebDAV PROPFIND.”
- One primary action per screen.
- Responsive for laptop and phone. Gil’s grab page is a phone. The architect desk is a laptop.
- Strict CSP. Same-origin request mark. No third-party scripts.
- Status on the page stays sanitized. Real paths and engine diagnostics stay behind unlock.

Mockup serves **http://127.0.0.1:3001** (`make mockup`). Loopback only. No engine.

### Rail (v0)

```text
THE JOB
1  Home
2  Library
3  Send
4  Capsules
5  Places

THE FINE PRINT
   Cut a recovery kit
   Bring a takeout
   Check store
   Destroy a capsule
```

**Home** hero: “Your files. Your node. Send a grab link.”  
**Library**: browse/upload, select a file or a folder.  
**Send**: target (file or folder), gate (Open / Passphrase), permission (Read-only), expiry, grabs, one yellow button, then URL + QR.  
**Capsules**: who (label), what (file or folder), gate, expiry, grabs left, revoke. “Gil” is a note you typed, not a login.  
**Places**: grab HTTPS base, drive `davs://` base, drive token reveal/rotate, volume health.

Grab page (share socket): Weazl mark, name, size, expiry, **Grab**. Folder: sealed listing + Grab all. Optional passphrase field. No library chrome, no “create an account,” no rest of the tree.

Empty states say what to do next. Toasts report outcomes. Errors do not leak secrets.

## Docker

Household node, Navidrome-class: Compose is how it stays up.

```text
deploy/compose.yaml
deploy/Dockerfile
```

Requirements:

- Multi-stage build. Final image is the static binary plus tzdata/certs, pinned restic, non-root user, no compiler.
- One volume: `/data` (vault, catalog, restic repo, capsules, node key).
- Do not publish 80/443/7272/7273/7274 on the host. Join the existing Traefik network. Document the three internal ports. Do not bake Traefik labels into the product; a commented example in README is not a dependency.
- Health: `/ready` on desk (liveness of the process). Share `/ready` and drive `/ready` return no library metadata.
- Read-only root filesystem if it does not fight SQLite/restic; data dir writable.
- No passphrase, restic password, or drive token in Compose `environment`. Unlock via desk, or node-key autoload if the architect enabled it.
- `restart: unless-stopped`.
- Drop Linux capabilities. No Docker-in-Docker.
- Pin base image digests at release.

Native (non-Docker) binary on Omarchy is welcome later. Docker is the v0 run shape because the node is always-on metal, not a laptop widget.

## Repo shape

```text
weazlcloud_plan.md
weazl_ethos.md
weazl_ethos_lite.md
weazl_skill.md
README.md
Makefile
weazlhead.png
cmd/weazlcloud/          # one main
internal/
  vault/
  catalog/
  restic/                # runner: pipe password, backup/dump/ls
  capsule/
  desk/                  # HTTP, CSP, CSRF, unlock
  share/                 # grab socket only
  drive/                 # WebDAV only
  recovery/              # .wzck envelope
  config/
  buildinfo/
desk/                    # production assets, embedded
mockup-ui/               # static contract; make mockup
deploy/
  Dockerfile
  compose.yaml
scripts/
  check-go-lines.sh
```

300 LOC law on every Go file. `scripts/check-go-lines.sh` as in WeazlMail. A parser parses. A restic runner runs restic. The share handler does not import the catalog lister. The drive handler does not import the desk router.

## Threat model (v0)

### Assets

- Plaintext library files and catalog metadata.
- Vault DEK, passphrase-derived wrap, node key, restic password, drive token.
- Capsule payloads and unwrap material.
- Recovery kit ciphertext.
- Public hostnames / Traefik routes the architect already owns.

### Trust

- The node is trusted while uncompromised, same bet as Navidrome.
- Disk at rest is a thief without wraps.
- Traefik and Gil’s phone are untrusted. The grab token is the capability. Traefik is the architect’s; Weazl still does not put unlock on the grab vhost.
- The file manager is a client on the drive socket. The drive token is the capability. GNOME keyring holding that token is accepted; GNOME keyring holding the vault passphrase is not the design.
- Recovery media is losable. Ciphertext plus non-secret bootstrap only.
- WeazlBack’s SSH target remains an untrusted ciphertext store for *backups*, not for live grab.

### Controls

| Threat | Control |
|---|---|
| Curious proxy / stolen URL after expiry | Token entropy; burn-after-read; optional passphrase; revoke |
| Share socket walks the library | Separate handler graph; no catalog list; tests that forbidden routes 404 |
| Grab hostname unlocks the vault | Separate listeners; tests that unlock is not on share or drive |
| Drive token in Files keyring | Token ≠ vault passphrase; rotate is explicit |
| Passphrase in Compose/logs | Never env/argv; restic via pipe; sanitized status |
| Forgotten passphrase | No mitigation by design; forge warning; kit still needs the phrase |
| Stolen recovery kit | Argon2id + AES-GCM; decrypt fails without passphrase |
| Capsule surviving revoke | Delete ciphertext and unwrap; refuse token |
| Node disk theft | Restic encrypted repo + encrypted catalog; node key + volume together is still theft — LUKS the disk |
| Live file mutation under a link | Capsule is a copy sealed at mint |
| FUSE helper as an extra attack surface | Do not ship one |
| XSS on grab page | Strict CSP, no third-party JS, filename sanitised |
| Compromised unlocked session | No protection; lock exists |

### Explicit non-goals

- No protection after an attacker controls an unlocked node session.
- No claim that one node copy protects against disk death (that is WeazlBack, plus the kit).
- No E2E story where WeazlCloud cannot read the object it stores. This is a trusted household node, not Tahoe-LAFS. Fragment-key URLs (Send-style) are a later upgrade, not v0.
- No forgotten-passphrase recovery dressed up as a kit.

## Phases

Mockup and law before engine. Do not skip Phase 0.

### Phase 0 — Contract

Static Lite mockup on **http://127.0.0.1:3001**: unlock/forge, home, library (fixture files and folders), send (file or folder, open or passphrase, read-only, URL + QR fixture), capsules, places (grab base + `davs://` drive), recovery kit, takeout ingest (stretch, fixture dump + dedupe promise), grab page as a separate HTML that can be opened as Gil (file, folder, passphrase, burned).

Exit: `make mockup` serves loopback 3001. Keyboard chords visible. One yellow button on Send and Grab. No engine.

### Phase 1 — Node skeleton

Go binary, embedded stub assets, config validation, graceful shutdown, `/ready` on each listener, multi-stage non-root image with restic, Compose joining an external network, volume, desk `:7272`, share `:7273`, drive `:7274`. Share and drive 404 bodies are not the desk.

Exit: `docker compose up` health passes. Share cannot serve desk HTML. Drive cannot serve unlock. Race tests for bind split. No Traefik container in this compose.

### Phase 2 — Vault and kit

Forge, unlock, lock, no-recovery copy, Argon2id, AES-GCM catalog, node-key wrap, restic password + drive token born at forge, passphrase never in env. Desk POST unlock. CSRF/origin/CSP. Cut and verify `.wzck`.

Exit: tests prove canaries (passphrase, DEK, restic password, drive token) absent from logs, status JSON, argv, and Compose. Lock zeroes memory (best-effort: subsequent library ops fail). Restart with node-key autoload off stays locked. Kit decrypts with the phrase and fails without it. Kit contains no library plaintext.

### Phase 3 — Library (restic)

Put/get/delete file and folder, restic backup/dump, catalog cache, duplicate put does not double store. Desk Library view talks to the API.

Exit: fixture of two files sharing bytes; `du` on the restic repo shows one copy of that pack data. Interrupt mid-put, retry, catalog consistent. Image contains restic. Image does not contain a FUSE helper. Desk copy never says “snapshot.”

### Phase 4 — Capsules (the Gil path)

Mint from a file or a folder: copy bytes into a sealed capsule, policy, label. Open vs passphrase. Read-only. Desk shows URL + QR. Grab page on the share socket. Folder listing is the sealed tree. Default 24h / 1 grab. Revoke. Places requires public grab base.

Exit: phone-sized browser (or `curl` from a non-loopback address) downloads the file. Folder grab lists only sealed members. After one grab, second grab fails. After revoke, token is a brick. Share logs contain no filename secrets beyond what the grab page already showed. Vault lock does not need to be held for grab of an already-minted capsule. A later edit of the library file does not change the minted payload.

### Phase 5 — Drive (WebDAV)

WebDAV on `:7274`, drive-token auth, PROPFIND/GET/PUT/DELETE against the current library. Document Files → Connect to Server → `davs://…`.

Exit: from a Linux file manager (or `cadaver`/`curl -X PROPFIND`) list, read, and write a file without FUSE. Drive listener rejects unlock POST and grab routes. Locked vault without autoload → 401. Token rotate bricks the old password in Files.

### Phase 6 — Release checks

`go test ./...` with race. Line-count gate. Docker rebuild from clean. Volume survives recreate. No host-published vault port. README: attach to existing Traefik, three names, backup `/data` with WeazlBack, cut two kits. Manual: mint a weazldoc, send to a real phone; mount the drive in Files.

Later, after the core is in use: takeout ingest (Google / iCloud / OneDrive dumps, local walk, restic dedupe); device pairing + pull of the current tree to a second machine; Nextcloud-client dialect if Files+grab is not enough; photos date ingest; Send-style fragment keys; Omarchy TUI/widget. None of these gate v0. Takeout is the first stretch because a household leaving those clouds is the actual migration, and the dump is where duplicate bytes live.

## Language

- **Node** — the always-on process and its volume.
- **Desk** — Lite UI (loopback in the mockup; Traefik name in the house).
- **Library** — current files in the restic-backed store.
- **Capsule** — sealed grab payload (one file or one folder).
- **Grab link** — capability URL. Not “share with user.”
- **Open** — URL is the capability; no passphrase.
- **Passphrase** — extra wrap; phrase on a second channel.
- **Read-only** — Gil can grab; cannot write back.
- **Drive** — WebDAV for the architect’s file manager. `davs://`. No FUSE.
- **Places** — where a phone can reach grab, and where Files can reach drive.
- **Recovery kit** — offline `.wzck` envelope. Not a spare passphrase.
- **Weazldoc** — whatever file is being sent (WeazlWrite export, PDF, image). The capsule is bytes Gil can open. Do not invent a `.weazldoc` format that only Weazl reads.
- **Takeout** — a dump the architect already exported from iCloud, Google, or OneDrive. Ingest, don’t connect.

Engine words (restic, WebDAV, PROPFIND, pack, DEK) belong in diagnostics, not on the home rail. Outcome language on the rail: library, send, grab, Files, kit.

## Implementation notes for a later session

- Start in this directory as the repo root. Do not fold this into WeazlBack.
- Read ethos files before code. If a PR violates product law, it does not ship.
- Mockup first, even if the implementer is impatient. Craft source: `../weazlback_universal`. Mark: `weazlhead.png`.
- Prefer stdlib HTTP. Desk, share, and drive may share a process and must not share a router table.
- Restic runner: follow WeazlBack’s pipe-password pattern. Pin the restic version. Do not rewrite restic.
- SQLite via the same CGO pattern as WeazlWrite/WeazlMail if needed; keep it in `internal/catalog`.
- WeazlBack coexistence: “backup the volume.” Do not point a second restic at individual pack files as a live Cloud job.
- Do not add CalDAV “while we are here.”
- Do not add Traefik “while we are here.”
- Measure idle RSS and wakeups of the container with no clients before claiming the node is cheap.

## Open questions (do not block Phase 0–5)

- Node-key autoload default on Docker installs vs always unlock after reboot. Lean: off until Places is configured, then ask once. Autoload is what makes Files survive a reboot.
- Whether a weazldoc from WeazlWrite is imported by file upload, Drive PUT, or a later local socket. v0 is upload/select on the desk plus Drive PUT.
- Drive token vs “use the vault passphrase in Files.” Lean: drive token, so the keyring is not the vault.
- WebDAV class 2 locking if GNOME starts stomping files. Measure before adding.
- Fragment-key URLs once the grab page is boring.
- Takeout layout: keep vendor folders vs flatten photos into a date tree. Lean: keep the dump prefix, then optionally date-index photos as a later ingest pass. Do not block v0 on this.

The system works for the architect. Lite lets Gil grab one file without moving the vault. Files sees the same library without FUSE. The gig remains untaxed.
