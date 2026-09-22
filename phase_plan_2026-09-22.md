# WeazlCloud gap workbook — September 22, 2026

Status: initial decisions recorded; remaining questions pending. Continues `phase_plan_2026-09-21.md`.

Purpose: define the next work after the existing workbook, with emphasis on dependable daily use. These are proposed tasks, not completed work. Explicit decisions below govern retention and behavior. Resolve the questions below before implementing dependent behavior.

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

## G3 — Simple Trash retention

Decision: WeazlCloud is not a recovery service. Keep 30-day Trash with automatic emptying; do not add overwrite version history or a backup/recovery service to this gap's scope. Existing operational backup procedures remain separate.

- [ ] Enforce automatic emptying of Trash after 30 days through idle maintenance.
- [ ] Verify cleanup protects live files and reclaims unreferenced storage safely.

The former version-history and recovery-service questions are superseded by this decision.

## G4 — Account lifecycle

Decisions: disabling an account revokes its grab links. Deleting a user deletes all of that user's assets, with no retirement retention period. This defines future deletion behavior; it is not an instruction to delete any existing user now.

- [ ] On deletion, invalidate sessions and cancel/drain active jobs before removing the user's vault, keys, catalog, repository, Trash, previews, staging, generated ZIPs, capsules, and other owned runtime data. Prevent in-flight work from recreating deleted assets; retry interrupted cleanup.

- [ ] Add account suspension, session revocation, administrator succession, and deliberate retirement handling.
- [ ] Ensure these controls do not expose vault contents through the admin UI.

Questions:
1. Resolved: revoke existing grab links when disabling an account.
2. Resolved: user deletion removes all owned assets; no retirement retention period.
3. Should another local account be promotable to administrator, or should there be one administrator with an explicit transfer procedure?

## G5 — Unattended maintenance

Decision: run maintenance when the node is idle. Define idle detection and pause/resume behavior around active transfers during implementation.

- [ ] Schedule expiry and abandoned-file cleanup independently of user visits; recover from interrupted work and report failures.
- [ ] Coordinate cleanup with active uploads, archives, retained snapshots, and available disk.

Questions:
1. Resolved: run when idle.
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
2. Resolved: keep both files when imported content differs at an existing path. Identical-file handling remains to be specified.
3. Should a complete export contain current files only, or also Trash and retained file versions in separate folders?

## G8 — Failure and concurrency testing

- [ ] Exercise full/read-only/missing volumes, interrupted writes, simultaneous edits, and upgrades on populated disposable data.
- [ ] Record resource limits and browsing responsiveness under mixed workloads; avoid a 500-file fixture unless separately requested.

Questions:
1. Resolved: allow replacement on simultaneous saves; the last successful committed write wins. Failed writes must not replace committed data. This is separate from the keep-both import policy.
2. What everyday load should we target: concurrent users, typical folder sizes, and largest files?
3. Can a disposable container on the production host be used for realistic disk/performance tests, or should all fault and load testing stay on this workstation?

## Proposed order

Resumable uploads, mounted-drive performance, automatic 30-day Trash emptying, and unattended maintenance are the first priorities. Use failure tests throughout implementation. Schedule account lifecycle, richer previews, and import/export according to the answers above.

## Work log

September 22, 2026: Recorded eight gaps and 24 outstanding questions. No new product decisions have been assumed. Implementation remains pending answers and task breakdown.

September 22, 2026 — Bob's decisions: simple 30-day auto-empty Trash, no overwrite history/recovery service; revoke grabs on account disable; delete all owned assets on user deletion; maintenance when idle; keep both differing import files; allow replacement for simultaneous saves. References “1a”, “2a”, “6a”, and “7a” name questions but do not identify the selected options; clarification is pending. Other unanswered questions remain open. Documentation only; no runtime changes or user deletion performed.
