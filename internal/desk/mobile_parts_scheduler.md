# Mobile finalization limits

The `parts-v1` finalizer uses a persistent worker pool. With P equal to
the lesser of `runtime.GOMAXPROCS(0)` and the visible cgroup CPU quota
(rounded down, minimum one), and M equal to max(1, min(8, visible RAM / 2 GiB)),
its defaults are:

| Limit | Default | Hard maximum |
| --- | --- | --- |
| Global workers | max(1, min(M, P / 2)) | 8 |
| Workers per owner | max(1, min(4, M, P / 4)) | 4 |

Division rounds down. One- and two-CPU hosts use one worker. Eight-CPU hosts
with at least 8 GiB of visible RAM use four workers, at most two per owner. Sixteen-CPU and larger hosts use
eight workers, at most four per owner, when visible RAM is at least 16 GiB.
RAM is the minimum of host memory and this process's cgroup v1/v2 memory limits,
including ancestors. Unknown resources or less than 4 GiB default to one worker.
The 2 GiB allowance per worker is a concurrency heuristic for full-index writer
pressure, not a hard memory reservation.

Set `WEAZLCLOUD_MOBILE_FINALIZE_WORKERS` (1–8) and
`WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER` (1–4) before starting the server
to override these defaults. Empty values select defaults; malformed, overflowing,
zero, negative and above-maximum values fail server startup validation.
The per-owner limit is then capped by the global limit. Restart after changing
configuration. Overrides may raise defaults but never the hard maxima.
The server entry point calls `desk.ValidateMobileFinalizeSettings()` for fail-fast
validation. Limits are captured once per handler at
construction, so capability reporting and the active pool agree.
`/api/v1/mobile/capabilities` reports the effective values in
`limits.mobile_finalize_workers` and `limits.mobile_finalize_workers_per_owner`.

Every second, the scheduler polls eligible unlocked owners. It admits one
session per owner per round, resuming after the last owner served and repeating
rounds until capacity is full. Active owner/session pairs cannot be admitted
twice across polls. Completed jobs release capacity independently; slower jobs
do not hold up the next polling round. Completion immediately refills from the
bounded pending scan; new sessions are discovered on a later poll. Transient
batch retries use durable bounded backoff. At most the global limit of jobs
is admitted, including jobs waiting for a worker. Remaining sessions stay in
durable staging.

Workers retain registry lifecycle leases, vault-session cancellation, grant
monitoring, and the atomic publication authorization guard. Shutdown cancels
work and waits for worker cleanup. The pool does not change upload session IDs,
transport negotiation, or upload/status/finalize response formats. These limits
bound concurrent finalization, not upload-part concurrency or storage throughput.
