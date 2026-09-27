# Rehydration recovery — 2026-09-26

Status: investigation and implementation plan. Production has not been changed
by this investigation; the import is stopped on the fifth ZIP.

## Desired result

- Google Photos content lives in `/Photos`, including albums and sidecars.
- Google Drive content lives directly in the library root, preserving its tree.
- No `Google Takeout` or `Drive` wrapper remains around these imported files.
- Routine input problems are resolved or logged per entry without stopping the
  batch. Storage integrity, authentication and disk-capacity failures stop
  safely and leave the affected source ZIP available.
- Existing imported data is migrated in place, then the remaining ZIPs resume.
- Cleanup and the final landed/dedupe report continue to follow verification.

## Observed production facts

Checked through `ssh -J bobp@jumpbox.prendie.io bobp@weazlcloud.teralab.local`.

- Four of sixteen archives completed and were verified. Their source ZIPs
  were removed. All three Photos archives and the first Drive archive finished.
- The fifth archive, `takeout-20260923T154520Z-2-002.zip`, failed after 411
  entries and 47,945,742,883 processed bytes. Its ZIP is retained.
- Library: 77,019 files and 697 folders, 226,649,020,136 logical bytes.
- Exact-file unique bytes: 200,069,049,115. Savings: 26,579,971,021 bytes.
- Twelve ZIPs remain staged, totaling 588,092,440,426 bytes. Their central
  directories contain 18,868 file entries, all Drive content; some are already
  imported from the fifth ZIP. None has a Drive root item named `Photos`.
- A read-only projection of the current catalog into the new layout, trimming
  whitespace consistently on path components, found zero duplicate destination
  paths. Twelve existing paths have whitespace at a component boundary; 28
  entries in the remaining ZIPs have such whitespace. An implementation must
  additionally validate file/ancestor conflicts and preserve an audit mapping.
- VM: four vCPUs, approximately 16 GiB RAM, generic QEMU CPU model. The guest
  already exposes AES. Docker has no explicit CPU or memory limit.
- Owner catalog: 84,285,637 bytes on disk. `Catalog.Put` copies, serializes,
  encrypts and atomically writes the whole catalog for each file. The current
  Restic batch loop calls it once per member, and library `ensure` reloads the
  catalog. These are concrete opportunities to remove repeated work.
- Importer already uses eight workers for files up to 32 MiB, in waves.
  Larger files stream individually. More cores alone cannot remove the
  serialized catalog work.

## R1 — repair paths and define the new destination contract

1. Reproduce the actual failure: the folder component
   `2023-11-05 -- Boston Film Photography Meetup ` ends with a space.
   `ensureFolder` records that spelling in its map, but `Library.Mkdir` trims
   the final space. Child file paths preserve the space inside the path.
   On the next archive, the cached folder spelling no longer matches; trying
   to create it again reports a conflict against the trimmed folder.
2. Use one destination contract throughout import, migration, resume,
   collision resolution, and Python verification. Strip only the export and
   product wrappers. Drive maps to root; Photos maps to `Photos/`.
3. Canonicalize whitespace consistently per component. Validate again after
   canonicalization: empty, dot, traversal and unsafe components cannot become
   legal by accident. Record every changed source spelling and destination.
4. Explicit root directory entries such as `Takeout/Drive/` are no-ops; an
   empty destination for that root must not be passed to `Mkdir`.
5. Preserve unknown export products under a named product folder with the
   same collision rules; never silently discard them.
6. Update the Photos sidebar, album root detection, album metadata/cache keys,
   import UI wording and documentation together.

Acceptance: the Boston folder and its children behave identically across
multiple ZIPs and a restart; Drive files land at root and Photos albums work
under `/Photos`.

## R2 — migrate the imported library without rehydrating it again

1. Hold the unattended runner lock during maintenance. Confirm no live import
   or pending storage commit before changing the catalog.
2. Save private encrypted backups of the catalog and album cache, and a backup
   of the runner state/configuration. Keep a durable migration manifest.
3. Compute and validate the complete path mapping before committing it:
   `Google Takeout/Photos/...` -> `Photos/...`;
   `Google Takeout/Drive/...` -> `...`.
4. Apply the transformation as an atomic catalog migration, retaining file
   content hashes, timestamps, identities and storage references. Restic
   snapshot/object paths remain the existing references; they must not be
   rewritten to the new display paths. Repair missing/mismatched parents and
   remove only the now-empty wrapper folders.
5. Migrate album metadata cache keys and update checkpoint sample paths and
   verification mappings. Retain original archive audit records. Record the
   layout version so a restart cannot apply the migration twice.
6. Compare every file's content reference/hash/size before and after; verify
   selected readbacks and album membership. Reuse completed archive records:
   their original ZIPs are no longer on the server.

Acceptance: all 77,019 currently stored files remain accounted for with the
same content; no payload reupload is needed; root browsing and Photos work.
Do not wipe the vault. A restart halfway through maintenance must either use
the old catalog or the complete new catalog, with an unambiguous resume path.

## R3 — make routine input errors non-blocking

- Same destination and same content: record an existing-file match and continue.
- Same destination with different content: keep both, using a deterministic
  suffix and a persisted source-to-destination mapping. Retries must find the
  same renamed destination rather than create another copy.
- File/folder collisions: preserve both by allocating a stable alternate name
  for the incoming item or subtree. Keep `/Photos` usable as a folder even if
  a future Drive export contains an incompatible root entry named `Photos`.
- Corrupt or unsafe entries: log the source path, reason and outcome, then
  continue with other entries. Do not label a valid naming conflict corruption.
- Persist progress, warnings and final destinations per archive/entry. Surface
  stopped/needs-attention status in the UI so a healthy container is not
  mistaken for a working importer.
- Backend failures, lost vault access, inadequate free space and unverifiable
  writes remain fatal for the affected work. Bounded retries may handle
  transient failures; repeating a deterministic failure forever is not recovery.
- Verification must understand renamed/skipped entries. Delete a source only
  after all its entries have a verified import or an explicitly authorized,
  durable skip record. Produce the requested final corruption/issues report.

Acceptance: mixed valid, corrupt, duplicate and conflicting entries finish
with a complete accounting; valid different content is preserved; disk/backend
failures retain the source and never produce a false success.

## R4 — remove the bottleneck, then expand useful parallelism

1. Add atomic catalog batch commits: publish a Restic batch with one catalog
   write rather than one full-catalog rewrite per file. Preserve durable
   staging manifests until the catalog commit succeeds, including recovery
   after a crash between the snapshot and catalog commit.
2. Avoid repeated full-catalog reloads during an owned import. Keep locking,
   visibility of ordinary library operations and recovery semantics explicit;
   do not introduce a stale cache as a shortcut.
3. Make worker count and staged-byte budget configurable. Start with eight
   streaming workers and a shared disk reservation budget. Measure before
   raising to sixteen; do not allocate a full file in RAM.
4. Replace waves that wait for their slowest entry with a bounded work queue.
   Serialize conflicting destinations and preserve deterministic resume.
   Permit limited concurrent larger files only within the staging budget;
   an entry larger than that budget runs alone. The existing 35 GB file proves
   that oversized individual entries must remain supported.
5. Keep storage commits coordinated per repository. Multiple independent
   archive writers are not a substitute for a properly batched commit path.
6. Measure files/sec, logical MB/sec, catalog writes, CPU, disk wait, peak
   memory and staged bytes. Validate performance with an existing-sized
   catalog; a tiny empty-vault example will miss this bottleneck.

Proxmox recommendation: eight vCPUs is a sensible initial allocation; keep
16 GiB RAM for now. This is a tuning starting point, not a measured minimum.
CPU type `host` can expose the physical CPU features if the VM stays on this
host or the migration targets have matching CPU capabilities. Heterogeneous
live-migration requirements need a compatible common CPU model instead.
See the [Proxmox CPU-model documentation discussion](https://lists.proxmox.com/pipermail/pve-devel/2023-June/057718.html).

## R5 — deploy, resume and prove forward progress

1. Regression checks: trailing whitespace, normalized-name collisions, root
   entries, duplicate ZIP members, file/folder conflicts, corrupt entries,
   cancellation/resume, and failed catalog batch commit recovery.
2. Exercise the migration and importer in a disposable local container;
   verify Photos albums, root browsing, stored bytes and the cleanup report.
3. Build the production image, keep the existing image for rollback, preserve
   the production Compose mounts/settings, and apply the backed-up migration.
4. Reconcile the runner checkpoints with the new layout, then resume the
   failed fifth ZIP. Hash-check and reuse its 411 committed entries.
5. Confirm that the formerly failing Boston entries land and the job moves
   beyond them. Check sustained progress, warnings and disk headroom before
   leaving it under the existing unattended runner.
6. At completion, verify all archive outcomes, clear the authorized staging
   sources, and report landed logical bytes, exact-file dedupe savings,
   actual Restic allocation, skipped entries and staging space reclaimed.

Completion means the corrected layout and import are verified on production;
creating this plan or rebuilding the image alone does not complete recovery.
