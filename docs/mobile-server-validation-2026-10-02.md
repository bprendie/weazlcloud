# Mobile server release evidence — October 2, 2026

Frozen candidate: `weazlcloud:smoke`, version `mobile-server-local`, image ID
`sha256:cd7429e383ca3825a6ebef0bb737ca8e69df5347f03896bd0d11d3949dc5004d`.
Application code and release documentation are frozen. All server release gates
are **PASS**. Production rollout is **complete**; the [release record](mobile-server-release-2026-10-02.md)
gives the pushed commit, deployed image, recovery copy and live audit. Companion documents: [contract](mobile-api.md),
[OpenAPI](mobile-api.yaml), [decisions](mobile-server-decisions.md),
[migration](mobile-server-migration.md).

## Checks and coverage

| Check | Current result and evidence |
| --- | --- |
| `make build`, frozen image build | **PASS**, version and image identity above. |
| `make check` | **PASS, exit 0**: all Go tests, vet, race, line limits and JS checks. Log `/tmp/weazlcloud-mobile-release-check.log`. |
| Full freeze storage suite | **PASS**. Log `/tmp/weazlcloud-mobile-freeze-storage.log`. |
| `go test -race ./internal/mobileparts -count=1 -timeout=90s` | **PASS**, 8.560 s; includes bad-receipt queue isolation. |
| `go test -race ./internal/upload` | **PASS**; corrupt/future/over-quota neighbors preserved while valid sessions restore. Reservation regression `go test -race ./internal/upload -run TestEncryptedPartialReservationMatchesRestart -count=1` also **PASS**, 1.439 s. |
| Frozen-image container smoke, both backends | **PASS, exit 0**. Restic migration migrated, verified and reopened mixed mode; shared Desk/WebDAV, resumable finalize, preview and sealed grab/read survived restart. Commands below. |
| Frozen-image browser smoke, both backends | **PASS, exit 0** (`make smoke-browser`). |
| Frozen-image native extended Photos + 250 MiB, both backends | **PASS, exit 0**. Local measurements below. |
| Isolated frozen-image Photos smoke | **PASS, exit 0**, both backends. `make smoke-photos PHOTOS_PYTHON=/tmp/weazlcloud-modal-venv/bin/python`; log `/tmp/weazlcloud-mobile-release-photos-isolated.log`. |
| Integrated owner deletion | **PASS**: `TestMobileOwnerDeleteDrainsPartsJobsMappingsGrabsAndPreservesSharedNeighbor`, normal 4.755 s / race 7.711 s. Active parts/jobs, mappings and grabs cleaned up; neighboring owners' shared references preserved. |
| API contracts and docs | **PASS**: PyYAML 6.0.3; mobile 69 paths, 90 unique operations, 517 resolved local references, 24 DTO audits, 81 route/scope rows; Photos 35 paths parsed/references resolved. Effective parameters, cancellation responses and doc whitespace checked. Structural validation does not prove generated-client compatibility. |
| Compose quiet configuration validation | **PASS**; configuration validation, not runtime coverage. |
| SSH through jumpbox | Healthy; operational reachability only. |

Container commands executed against the frozen image:

```sh
WEAZLCLOUD_IMAGE=weazlcloud:smoke WEAZLCLOUD_CONTAINER_PORT=29272 bash scripts/container-smoke.sh
WEAZLCLOUD_IMAGE=weazlcloud:smoke WEAZLCLOUD_CONTAINER_PORT=29372 WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental bash scripts/container-smoke.sh
```

Focused coverage includes stable Files identity/captured reads across overwrite,
rename and path reuse on both backends; owner/device isolation; lock/revoke read
interruption; HEAD/ranges/ETags/preconditions; bounded snapshots, deltas,
tombstones/checkpoints and expired/restored cursors. Authorization rebind,
coordinated cancellation and expired-stage fixtures passed. Integrated owner deletion also verifies active-work cleanup and preservation of
shared neighboring owners.

## Native measurements and fixture limits

Local **2 CPU / 4 GiB**, extended Photos plus 250 MiB transfer, same frozen image.

| Measurement | Restic | Shared |
| --- | --- | --- |
| Complete fixture | 22.4 s | 15 s |
| 250 MiB transfer | 8.3 s; 30.3 MiB/s | 7.6 s; 33.1 MiB/s |
| PID 1 RSS, initial → sampled peak | 20.3 → 145.3 MiB | 23.1 → 147.1 MiB |
| RSS samples | 27 | 24 |
| Cgroup anonymous memory | 140.5 MiB | 140.9 MiB |
| Reported kernel peak memory | 295.3 MiB | 568.4 MiB |
| Allocated stage bytes before final missing part | 245,506,048 | 245,506,048 |
| Encrypted WZA1 payloads before final missing part | 15 | 15 |
| Allocated stage bytes after stored cleanup | 16,384 | 16,384 |
| Payloads after stored cleanup | 0 | 0 |
| Encrypted receipts retained after cleanup | 4; maximum 3,012 bytes | 4; maximum 2,992 bytes |

Both same-image disk-extended reruns completed with exit 0. Allocated staging
bytes are measured with `du`; they are not the logical upload length or whole-volume
usage. Encrypted receipts remain after payload cleanup.

Sampled process RSS and kernel/cgroup memory have different scopes; these samples
do not establish an unsampled RSS maximum. Local transfer rates are **not LAN
throughput claims** or phone/sustained-load guarantees. Added CI coverage is not
an executed CI result.

Photos UI checks use five synthetic photos: they do not establish large-library
scale or real-camera/phone behavior. Both isolated runs passed with unchanged
thresholds, superseding the earlier concurrent timing miss.

| Photos isolated measurement | Restic | Shared |
| --- | --- | --- |
| First four cards | 396 ms | 433 ms |
| First thumbnail | 488 ms | 520 ms |
| Scroll script p95 | 9.765 ms | Not supplied |
| Timeline release p95 | Not supplied | 123.84 ms |

The scroll-script and timeline-release figures measure different operations;
do not compare them as the same latency. All final-image end-to-end checks above
passed, including the disk-extended rerun and integrated owner-delete fixture.
Full migration-twice and old/new generated-client matrix evidence is not recorded.

## Physical iOS and production gates

Physical iOS verification is **unrun**, separate from server/image checks:
PhotoKit full/limited access and cloud-only originals; Live Photo/DNG resources;
provider/bookmark loss; Share extension/app lock; lock/reboot and termination versus
force-quit; background scheduling; Wi-Fi/cellular transitions; phone staging limits;
and credential rotation with queued URLSession tasks. Record completion, wakeups,
retransmits and peak staging on physical devices. Keep ordered-PATCH fallback until
device evidence supports removal. Server passes do not establish iOS readiness.
Production rollout and audit passed; see the [release record](mobile-server-release-2026-10-02.md).
