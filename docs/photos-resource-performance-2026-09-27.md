# Photos resource and UI verification — September 27, 2026

This record covers the local repository only. Production was not contacted,
benchmarked, rebuilt or changed.

## Baseline limits

The local shell reported 12 available CPUs, about 31 GiB host memory, a
cpuset of `0-11`, and no readable `cpu.max` or `memory.max` value. Because this
checkout already contained the earlier Photos work and the requested baseline
was not captured before that work, a controlled before/after renderer benchmark
is unavailable. The existing September 26 browser measurements remain the
small warm-fixture observations documented in
`docs/photos-web-performance-2026-09-26.md`; they are not reused as a
throughput claim.

## Policy verification

`TestPreviewPolicyProfiles` verifies the requested 2 CPU/4 GiB, 4 CPU/8 GiB,
32 CPU/128 GiB, one-CPU and unknown-resource envelopes. `TestPreviewPolicy...`
also verifies explicit worker and memory overrides. Discovery tests now inject
nested v1/v2 mounts, ancestor constraints, fractional quotas, namespace roots,
missing/malformed values and small-host memory. The implementation combines host
RAM, affinity and GOMAXPROCS with visible cgroup limits and rejects unsafe startup
overrides. These deterministic tests are not throughput measurements.

## Implementation checks

The September 27 follow-up adds lifetime-safe source holds, account/vault
cancellation, final entry checks, bounded header probing and pipeline memory
admission (including source, scratch, cache copies and a Restic allowance).
Preparation rescans identities after restart, persists encrypted failure records,
keeps manual pause, handles full import lifetimes, and reports cache skips and
eviction as partial readiness. See `photos-smoke-followup-2026-09-27.md` for the
specific failure tests and browser flows.

No measured child-process RSS ceiling or large-host speedup is claimed. Adaptive
pressure control and strict scheduling fairness are still open. The Library UI
retains the compact breadcrumb/action bar, filter popover and floating upload tray.

## Reproduction

```sh
node --check mockup-ui/app.js
node --check mockup-ui/views.js
node --check mockup-ui/data.js
go test ./internal/library -run 'TestPreviewPolicy|TestPhotoPreparation' -count=1
```

The full repository checks and disposable browser smoke are the final gates.
No production performance conclusion is made from policy tests or this local
resource envelope.
