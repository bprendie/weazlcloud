# Upload responsiveness results — October 9, 2026

Implementation addresses two reproduced blocking paths: authorization checks
waiting for catalog persistence, and status/other components waiting for a body
that holds the upload-session mutex. Finalization/storage still has serialized
catalog and Restic work; this release does not claim to saturate a gigabit link.

## Design and compatibility

See [lock ordering](mobile-upload-lock-order.md) and
[Astra's API handoff](mobile-upload-status-batch.md). Existing encrypted parts,
coordinator IDs, receipts, grants, and catalog formats remain compatible.
Independent receivers use encrypted temp files and short guarded commits. Concurrent
identical parts compare the durable receipt, preserving exactly-once counters;
this replaces a long per-part gate with bounded concurrent receiving and an atomic
commit comparison. Cancellation joins receivers before cleanup. New-work count
and byte limits reconstruct from durable live headers after restart, while already
accepted sessions bypass new-admission pressure. No new environment setting.

Timings are sampled wall-time logs and harness percentiles, with bounded aggregate
counters. Read time includes authorization checks; guarded commit time includes
its barrier. These overlapping phases must not be summed. Proxy-header presence
is observed but is not a trusted route classification. Actual underlying failure
classes remain separate from the compatible public HTTP mapping.

## Validation

Focused race tests cover slow publication with responsive grant reads,
revoke-versus-publish ordering and expiry (existing grant tests), stalled original
with responsive status and motion upload, duplicate receipt counters, revoked
commit denial, cancellation/owner drain, foreign cancellation isolation, finalizer
versus active duplicate cleanup, admission reconstruction, and batch API limits,
scope/device isolation, capabilities, terminal polling headers, and vault lock.

Real HTTP slow-body smoke passed on both backends: worst status response 9.74 ms
(Restic) and 9.06 ms (shared), with zero partial bytes
acknowledged. After a forced client disconnect, retry resumed the same upload and
readback matched SHA-256. A later shared run measured 40.03 ms under concurrent
local testing. These are local measurements, not Internet/Wi-Fi latency promises.

Three cold baseline/candidate runs used 12 deterministic valid 768×768 PNGs,
20.261 MiB total, identical payload hashes and limits, plus an extra polling
thread issuing up to seven individual GETs per second. Client receipt observations
have up to 0.5-second polling lag. Small and large suites shared the local machine
with other checks; the large catalog fixture has 24,000 synthetic folders and
33,346,784 encrypted bytes, not 24,000 uploaded photos or copied production data.

| Resource/catalog fixture | Backend | Baseline median all-stored | Candidate median all-stored | Baseline/candidate median status p95 |
| --- | --- | ---: | ---: | ---: |
| 2 CPU / 4 GiB; empty catalog | Restic | 11.678 s | 12.205 s | 7.10 / 3.43 ms |
| 2 CPU / 4 GiB; empty catalog | Shared experimental | 2.652 s | 2.159 s | 8.69 / 7.69 ms |
| 8 CPU / 8 GiB; 24K folders | Restic | 19.715 s | 19.050 s | 88.35 / 89.28 ms |
| 8 CPU / 8 GiB; 24K folders | Shared experimental | 10.095 s | 9.940 s | 260.12 / 65.45 ms |

Storage throughput is broadly comparable. The small Restic median is 4.5% slower,
below the workbook's 10% investigation threshold. These short fixtures do not
reproduce a long client-held body or establish production throughput improvement;
the dedicated stalled-body tests prove the blocking behavior is fixed.

Disposable old-to-new upgrades passed both backends: queued work resumed without
new finalize requests, partial tail receipts and IDs survived, and restored
hashes, albums, Hidden visibility, Live Photo components, and stored replay matched.

One initial full race suite hit TempDir cleanup racing native-photo background
work. Three focused repetitions passed; the simulator cleanup now explicitly
Drains the Library before locking and removing its temporary directory. An initial
HTTP fixture lacked the required X-Weazl-Desk header; this was corrected, and the
standalone real HTTP fixtures passed. These initial failures are retained in local
logs rather than counted as successful verification.

## Commands and evidence

Local logs under `.build/upload-responsive-*` contain the runs and image IDs.
The harness pins image IDs and prints the deterministic dataset hash. Build the
isolated catalog helper with:

```sh
CGO_ENABLED=0 go build -o .build/mobile-bench-catalog ./scripts/mobile-bench-catalog
PATH="$PWD/.build:$PATH" make check
python3 -u scripts/smoke-mobile-throughput.py --image NEW --old-image OLD \
  --cpus 2 --memory 4g --concurrency 2 --repeats 3 --polling-load
python3 -u scripts/smoke-mobile-throughput.py --image NEW --old-image OLD \
  --cpus 8 --memory 8g --concurrency 4 --repeats 3 --catalog-rows 24000 --polling-load
python3 -u scripts/smoke-mobile-throughput.py --image NEW \
  --cpus 2 --memory 4g --only-responsiveness
```

The helper requires a disposable fixture sentinel and the sole account named
throughput. It runs only against a stopped synthetic fixture; it is not installed
in the server image. Extra part latency/resource metrics are available in the
extended harness. CPU/RAM snapshots are observations, not peak-memory guarantees.
The larger upload smoke uses the existing 250-MiB stream fixture; media pairing
uses a generated valid MOV. No production load tests or production content export.

## Final release gates and production record

Pending: final make check, exact release-image container/mobile smokes, the
S6 production cutover. All four three-run comparisons and batch polling passed.
Do not treat this document as deployment evidence until those results are added.

Batch polling fixture (2 CPU / 4 GiB): nine batch calls across 36.7 seconds with
Restic (p95 6.87 ms), and one across 7.15 seconds with shared (3.07 ms), with zero
batch errors. The deliberate individual-load fixtures used up to seven GETs/sec;
these are separate runs with differing duration, not an exact request-count ratio.
The harness's receipt observers still use individual GETs to preserve comparable
completion timing. Resource snapshots after verification were about 152–159 MiB;
these are not peak-memory measurements.
