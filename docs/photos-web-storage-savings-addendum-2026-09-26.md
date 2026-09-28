# Photos web workbook addition — September 26, 2026

Status: requested addition to the Photos performance workbook currently being
prepared for Luna. Include this task in that workbook before implementation.
The main workbook had not yet been saved when this addition was recorded.

## Replace the dedupe meter with actual storage savings

User decision: the main meter must include exact-file deduplication, Restic
chunk deduplication and compression. Label it **Storage savings**, not Dedupe.

- [ ] Compare current logical file bytes with actual allocated repository bytes
  for the same owner/storage scope. Calculate savings as
  `1 - physical_repository_bytes / logical_file_bytes` when logical bytes > 0.
  Use allocated disk bytes, not a sum of apparent file lengths.
- [ ] Present a concrete result, for example: **450 GB of files using 300 GB
  on disk · 33% saved**. Use consistent units and explain in details that the
  physical figure refers to the original-file repository.
- [ ] Count repository metadata, retained versions and data awaiting repository
  reclamation in physical usage. Exclude Takeout staging ZIPs, upload workspace,
  temporary files and rebuildable preview caches from this comparison. These
  still consume disk and must remain included in the separate capacity meter.
- [ ] Retain exact-file savings as an optional detail where measurable. Do not
  invent separate chunk-dedupe/compression percentages or add overlapping
  savings figures together. The combined physical comparison is authoritative.
- [ ] Obtain repository allocation asynchronously and cache the result. Refresh
  after committed work, with a bounded periodic refresh during ongoing imports;
  never run recursive disk scans on the Photos request/render path. Coalesce
  refreshes and expose when the last completed measurement was taken.
- [ ] Match logical and physical measurements to a stable accounting point, or
  label them as an estimate during writes. An unavailable/failed measurement
  must not become zero physical bytes or a false 100% saving.
- [ ] Handle empty libraries and physical usage exceeding logical size without
  division errors or misleading savings. Show an empty state or **0% saved**
  with the actual byte totals and an overhead/retention explanation as appropriate.
- [ ] Preserve user isolation. Current per-user Restic repositories permit an
  owner-scoped comparison. Never divide one user's logical bytes by a shared
  repository's total physical usage. For shared storage, use a documented,
  defensible attribution method or report this figure only at a matching global
  scope with appropriate authorization; mark unsupported owner values unavailable.
- [ ] Verify with disposable local data: duplicate content, compressible unique
  content, incompressible unique content, retained/deleted content, an empty
  vault, unavailable accounting and separate owners. Confirm that the normal
  quota/capacity calculation is unchanged and Photos browsing triggers no scan.

## Deployment boundary

Implement and validate locally as part of Luna's workbook. Do not access,
restart, rebuild or deploy the production container for this task. Production
rollout waits until rehydration is finished and the main thread proceeds with
the planned deployment.
