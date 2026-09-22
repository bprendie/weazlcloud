# WeazlCloud gap workbook — September 22, 2026

Status: scope and product decisions pending. Continues `phase_plan_2026-09-21.md`.

Purpose: define the next work after the existing workbook, with emphasis on dependable daily use. These are proposed tasks, not completed work or new retention/privacy policy. Resolve the questions below before implementing dependent behavior.

Keep identity and processing local, preserve existing vaults and host settings, retain shared available-disk storage with the silent system reserve, and keep upload size unrestricted except by available space. Cross-user deduplication still needs a separate privacy design. Stored node keys remain the current convenience choice: controlling the host permits decryption; UI administrator isolation does not prevent that.

## G1 — Resumable uploads

- [ ] Add durable upload sessions, bounded chunks, verified offsets, restart reconciliation, and clear resume/cancel controls.
- [ ] Verify interrupted large uploads resume without retransmitting completed chunks. Browser restart may require reselecting the original file; explain that explicitly.

Questions:
1. Should the first release resume after browser/server restarts, or cover network interruptions while the tab stays open first?
2. How long should incomplete uploads be retained before their reserved disk is released: 24 hours, seven days, or another duration?
3. After reopening the browser, is reselecting the original file/folder acceptable, or is persistent access through a supported browser or local helper essential?

## G2 — Mounted-drive performance

- [ ] Measure Thunar browsing, opening, saving, and copying before selecting changes to authentication, staging, caching, or protocol support.
- [ ] Check that mounted and browser clients see consistent changes without weakening vault boundaries.

Questions:
1. Which mounted-drive action matters most: browsing folders, opening large files, or saving/uploading files?
2. Must mounting remain built into Thunar with no helper, or would you accept a small local client if measurements justify it?
3. Should mounted access remain LAN/VPN-only, or should remote internet access become part of the design?

## G3 — Overwrite recovery and backups

- [ ] Design recovery for overwritten files with visible disk accounting and conflict-safe restore.
- [ ] Rehearse recovery from an independent backup location, including older populated data and required key material.

Questions:
1. Should overwrites retain previous versions automatically, or should version history be opt-in by folder?
2. What limit should govern old versions: age, version count, disk budget, or a combination?
3. Do you already have WeazlBack backing up this volume to another disk/machine, and what maximum amount of lost work is acceptable?

## G4 — Account lifecycle

- [ ] Add account suspension, session revocation, administrator succession, and deliberate retirement handling.
- [ ] Ensure these controls do not expose vault contents through the admin UI.

Questions:
1. When an account is disabled, should its existing grab links remain usable or be revoked too?
2. After an account is retired, should its vault remain indefinitely until explicit deletion, or enter a timed retention period?
3. Should another local account be promotable to administrator, or should there be one administrator with an explicit transfer procedure?

## G5 — Unattended maintenance

- [ ] Schedule expiry and abandoned-file cleanup independently of user visits; recover from interrupted work and report failures.
- [ ] Coordinate cleanup with active uploads, archives, retained snapshots, and available disk.

Questions:
1. Should maintenance run continuously at low priority, during a nightly window, or only when the node is idle?
2. Where should failures appear: an admin dashboard, locally configured email, or both?
3. Near full capacity, should cleanup remove only already-expired data, or may it also evict previews and completed on-demand ZIPs before their normal expiry?

## G6 — Media and document previews

- [ ] Prioritize formats using real workflows; evaluate local converters with bounded CPU, memory, time, and derived-data storage.
- [ ] Preserve originals and provide a clear download fallback for unsupported content.

Questions:
1. Which comes first: RAW/HEIC photos, video posters/audio artwork, or faithful Office document previews?
2. Is a larger Docker image containing local converters acceptable, or should enhanced rendering be an optional container?
3. Should unsupported video codecs get a playable derived copy, or remain download-only with a clear explanation?

## G7 — Migration and exit

- [ ] Build resumable imports with timestamps, conflict handling, verification, and useful progress.
- [ ] Export ordinary files with manifests and hashes, using bounded-memory transfer paths.

Questions:
1. What should import support first: Google Takeout, a server-mounted folder, or a browser-selected folder tree?
2. When an imported path already exists, should the default be skip identical/keep both when different, explicit replace, or ask per conflict?
3. Should a complete export contain current files only, or also Trash and retained file versions in separate folders?

## G8 — Failure and concurrency testing

- [ ] Exercise full/read-only/missing volumes, interrupted writes, simultaneous edits, and upgrades on populated disposable data.
- [ ] Record resource limits and browsing responsiveness under mixed workloads; avoid a 500-file fixture unless separately requested.

Questions:
1. For simultaneous edits to the same file, should the second save be rejected, preserved as a conflict copy, or explicitly allowed to replace the first?
2. What everyday load should we target: concurrent users, typical folder sizes, and largest files?
3. Can a disposable container on the production host be used for realistic disk/performance tests, or should all fault and load testing stay on this workstation?

## Proposed order

Resumable uploads, mounted-drive performance, overwrite recovery, and unattended maintenance are the first priorities. Use failure tests throughout implementation. Schedule account lifecycle, richer previews, and import/export according to the answers above.

## Work log

September 22, 2026: Recorded eight gaps and 24 outstanding questions. No new product decisions have been assumed. Implementation remains pending answers and task breakdown.
